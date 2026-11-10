package persistence

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/alexwenzel/ccmas/internal/domain"
)

// UpdateResult beschreibt, was eine festgeschriebene Update-Transaktion bewirkt hat.
type UpdateResult struct {
	// Revisions nennt je Projekt die neue Zustandsrevision, falls die Transaktion sie erhöht hat.
	Revisions map[string]int64
	// EventIDs sind die IDs der in dieser Transaktion geschriebenen Audit-Ereignisse in Schreibreihenfolge.
	EventIDs []int64
}

// reader ist der lesende Teil von Tx und ReadTx. Die Lesemethoden stehen hier einmal; mutierende Methoden
// gibt es nur auf *Tx.
type reader struct {
	ctx context.Context
	q   querier
}

// ReadTx ist eine konsistente Lesetransaktion (View).
type ReadTx struct{ *reader }

// Tx ist eine Änderungstransaktion (Update). Jede mutierende Methode schreibt ihr Audit-Ereignis in derselben
// Transaktion; ein Rollback nimmt beides zurück. Scheitert ein Aufruf, ist die Transaktion verbraucht: weitere
// Änderungen liefern denselben Fehler, und Update schreibt nichts fest, auch wenn der Callback den Fehler
// ignoriert. Ein Tx ist nicht nebenläufig nutzbar und nach der Rückkehr des Callbacks ungültig.
type Tx struct {
	*reader
	s        *Store
	sqlTx    *sql.Tx
	actor    string
	at       time.Time
	revs     map[string]int64
	bumped   map[string]bool
	eventIDs []int64
	failed   error
	finished bool
}

// View führt fn in einer konsistenten Lesetransaktion auf dem Lesepool aus.
func (s *Store) View(ctx context.Context, fn func(*ReadTx) error) error {
	if err := s.checkOpen(); err != nil {
		return err
	}
	tx, err := s.r.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return mapErr(err)
	}
	defer tx.Rollback()
	if err := s.checkSchema(ctx, tx); err != nil {
		return err
	}
	return fn(&ReadTx{&reader{ctx: ctx, q: tx}})
}

// Update führt fn in einer IMMEDIATE-Transaktion auf dem Schreibpool aus. actor nennt, wer die Änderung
// veranlasst; er erscheint in jedem Audit-Ereignis. Gibt fn einen Fehler zurück (oder tritt ein Panic auf), wird
// zurückgerollt: weder Änderung noch Ereignis noch Revisionsänderung bleiben bestehen. Die Zustandsrevision eines
// Projekts steigt bei Festschreiben genau um 1, wenn die Transaktion mindestens eine Änderung des
// Planungszustands dieses Projekts enthält (siehe doc.go).
func (s *Store) Update(ctx context.Context, actor string, fn func(*Tx) error) (UpdateResult, error) {
	if err := s.checkOpen(); err != nil {
		return UpdateResult{}, err
	}
	if err := domain.CheckID(actor); err != nil {
		return UpdateResult{}, invalid(err)
	}
	sqlTx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return UpdateResult{}, mapErr(err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = sqlTx.Rollback()
		}
	}()
	if err := s.checkSchema(ctx, sqlTx); err != nil {
		return UpdateResult{}, err
	}
	tx := &Tx{
		reader: &reader{ctx: ctx, q: sqlTx}, s: s, sqlTx: sqlTx, actor: actor, at: s.now(),
		revs: map[string]int64{}, bumped: map[string]bool{},
	}
	defer func() { tx.finished = true }()
	if err := fn(tx); err != nil {
		return UpdateResult{}, err
	}
	if tx.failed != nil {
		return UpdateResult{}, tx.failed
	}
	tx.finished = true
	if err := sqlTx.Commit(); err != nil {
		committed = true // database/sql hat die Transaktion beendet
		return UpdateResult{}, mapErr(err)
	}
	committed = true
	res := UpdateResult{Revisions: map[string]int64{}, EventIDs: tx.eventIDs}
	for p := range tx.bumped {
		res.Revisions[p] = tx.revs[p]
	}
	return res, nil
}

// write führt eine mutierende Operation aus: nicht nach Ende der Transaktion, nicht nach einem früheren Fehler;
// ein Fehler verbraucht die Transaktion.
func (t *Tx) write(fn func() error) error {
	if t.finished {
		return errf(CodeClosed, "Transaktion ist beendet")
	}
	if t.failed != nil {
		return t.failed
	}
	if err := fn(); err != nil {
		err = mapErr(err)
		t.failed = err
		return err
	}
	return nil
}

// revision liefert die Revision des Projekts in dieser Transaktion (nach einer Erhöhung der neue Wert).
func (t *Tx) revision(project string) (int64, error) {
	if rev, ok := t.revs[project]; ok {
		return rev, nil
	}
	var rev int64
	err := t.q.QueryRowContext(t.ctx, "SELECT state_revision FROM projects WHERE project_id = ?", project).Scan(&rev)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, errf(CodeNotFound, "Projekt %q existiert nicht", project)
	}
	if err != nil {
		return 0, mapErr(err)
	}
	t.revs[project] = rev
	return rev, nil
}

// bump erhöht die Revision des Projekts höchstens einmal je Transaktion und liefert den neuen Wert.
func (t *Tx) bump(project string) (int64, error) {
	if t.bumped[project] {
		return t.revs[project], nil
	}
	var rev int64
	err := t.q.QueryRowContext(t.ctx,
		"UPDATE projects SET state_revision = state_revision + 1, updated_at = ? WHERE project_id = ? RETURNING state_revision",
		fmtTime(t.at), project).Scan(&rev)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, errf(CodeNotFound, "Projekt %q existiert nicht", project)
	}
	if err != nil {
		return 0, mapErr(err)
	}
	t.revs[project], t.bumped[project] = rev, true
	return rev, nil
}

// change hält eine Änderung des Planungszustands fest: Revision erhöhen, Ereignis schreiben.
func (t *Tx) change(project string, ev eventSpec) error {
	if _, err := t.bump(project); err != nil {
		return err
	}
	return t.emit(project, ev)
}

// journal hält einen Journaleintrag fest: Ereignis schreiben, Revision unverändert.
func (t *Tx) journal(project string, ev eventSpec) error { return t.emit(project, ev) }

// eventSpec beschreibt ein Ereignis; Projekt, Zeit, Akteur und Revision ergänzt die Transaktion.
type eventSpec struct {
	level      domain.AuditLevel
	kind       string
	entityType string
	entityID   string
	from, to   string
	details    map[string]string
}

func (t *Tx) emit(project string, sp eventSpec) error {
	rev, err := t.revision(project)
	if err != nil {
		return err
	}
	ev := domain.AuditEvent{
		ProjectID: project, OccurredAt: t.at, Actor: t.actor, Kind: sp.kind, EntityType: sp.entityType,
		EntityID: sp.entityID, Level: sp.level, FromState: sp.from, ToState: sp.to, StateRevision: rev, Details: sp.details,
	}
	if err := ev.Check(); err != nil {
		return invalid(err)
	}
	details, err := ev.DetailsJSON()
	if err != nil {
		return invalid(err)
	}
	var id int64
	err = t.q.QueryRowContext(t.ctx, `INSERT INTO events
		(project_id, occurred_at, actor, kind, entity_type, entity_id, level, from_state, to_state, state_revision, details_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING event_id`,
		project, fmtTime(ev.OccurredAt), ev.Actor, ev.Kind, ev.EntityType, ev.EntityID, string(ev.Level),
		ev.FromState, ev.ToState, ev.StateRevision, string(details)).Scan(&id)
	if err != nil {
		return mapErr(err)
	}
	t.eventIDs = append(t.eventIDs, id)
	return nil
}
