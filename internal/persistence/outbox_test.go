package persistence

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alexwenzel/ccmas/internal/domain"
	"github.com/alexwenzel/ccmas/internal/supervisor"
)

var obT0 = time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)

func obStore(t *testing.T) *Store {
	t.Helper()
	s := newStore(t)
	seedBase(t, s)
	return s
}

func obAction(t *testing.T, parts ...string) domain.OutboxAction {
	t.Helper()
	if len(parts) == 0 {
		parts = []string{"wo-1", "result"}
	}
	a, err := domain.NewOutboxAction(projectID, "asana.comment", "task-1", json.RawMessage(`{"text":"fertig"}`), parts...)
	must(t, err)
	return a
}

func obEnqueue(t *testing.T, s *Store, a domain.OutboxAction) (domain.OutboxAction, UpdateResult) {
	t.Helper()
	var got domain.OutboxAction
	res := mustUpdate(t, s, func(tx *Tx) error {
		var err error
		got, _, err = tx.EnqueueOutbox(a)
		return err
	})
	return got, res
}

// obDo führt fn in einer Update-Transaktion aus und liefert die Aktion zurück.
func obDo(t *testing.T, s *Store, fn func(tx *Tx) (domain.OutboxAction, error)) domain.OutboxAction {
	t.Helper()
	var got domain.OutboxAction
	mustUpdate(t, s, func(tx *Tx) error {
		var err error
		got, err = fn(tx)
		return err
	})
	return got
}

func obClaim(t *testing.T, s *Store, now time.Time, ttl time.Duration, limit int) []domain.OutboxAction {
	t.Helper()
	var got []domain.OutboxAction
	mustUpdate(t, s, func(tx *Tx) error {
		var err error
		got, err = tx.ClaimOutbox(projectID, now, ttl, limit)
		return err
	})
	return got
}

func obGet(t *testing.T, s *Store, id string) domain.OutboxAction {
	t.Helper()
	var a domain.OutboxAction
	mustView(t, s, func(r *ReadTx) error {
		var err error
		a, err = r.OutboxAction(projectID, id)
		return err
	})
	return a
}

func obEvents(t *testing.T, s *Store) []domain.AuditEvent {
	t.Helper()
	var evs []domain.AuditEvent
	mustView(t, s, func(r *ReadTx) error {
		var err error
		evs, err = r.Events(projectID, EventFilter{Level: domain.AuditLevelOutbox})
		return err
	})
	return evs
}

func obKinds(evs []domain.AuditEvent) []string {
	var out []string
	for _, e := range evs {
		out = append(out, e.Kind)
	}
	return out
}

func obCount(t *testing.T, s *Store) int {
	t.Helper()
	var n int
	must(t, s.r.QueryRowContext(ctxT(t), "SELECT count(*) FROM outbox").Scan(&n))
	return n
}

// ---- Enqueue ---------------------------------------------------------------------------------------------

func TestEnqueueKeyConflict(t *testing.T) {
	s := obStore(t)
	a := obAction(t)
	got, res := obEnqueue(t, s, a)
	if got.State != domain.OutboxPending || got.Attempts != 0 || got.CreatedAt.IsZero() || len(res.EventIDs) != 1 {
		t.Fatalf("Aktion %+v, Ereignisse %v", got, res.EventIDs)
	}
	events := countEvents(t, s)

	// Gleicher Schlüssel, gleicher Inhalt: No-op ohne Ereignis und ohne neue Zeile.
	again, res := obEnqueue(t, s, a)
	if len(res.EventIDs) != 0 || obCount(t, s) != 1 || countEvents(t, s) != events || again.ActionID != got.ActionID {
		t.Errorf("zweites Enqueue: Ereignisse %v, Zeilen %d", res.EventIDs, obCount(t, s))
	}
	// Auch nach einem Fortschritt der Aktion bleibt das erneute Enqueue ein No-op (der Zustand zählt nicht).
	obClaim(t, s, obT0, time.Minute, 5)
	if _, res := obEnqueue(t, s, a); len(res.EventIDs) != 0 {
		t.Error("Enqueue einer laufenden Aktion schreibt ein Ereignis")
	}
	if cur := obGet(t, s, a.ActionID); cur.State != domain.OutboxClaimed {
		t.Errorf("Enqueue hat den Zustand verändert: %s", cur.State)
	}

	// Gleicher Schlüssel, anderer Inhalt: conflict.
	for name, mod := range map[string]func(*domain.OutboxAction){
		"Nutzdaten": func(x *domain.OutboxAction) { x.Payload = json.RawMessage(`{"text":"anders"}`) },
		"Ziel":      func(x *domain.OutboxAction) { x.Target = "task-2" },
	} {
		b := a
		mod(&b)
		_, err := s.Update(ctxT(t), "tester", func(tx *Tx) error { _, _, err := tx.EnqueueOutbox(b); return err })
		if CodeOf(err) != CodeConflict {
			t.Errorf("%s: Code %q (%v), erwartet conflict", name, CodeOf(err), err)
		}
	}
	// Dieselbe Aktions-ID unter anderem Schlüssel ist ebenfalls ein Konflikt.
	c := obAction(t, "wo-1", "other")
	c.ActionID = a.ActionID
	_, err := s.Update(ctxT(t), "tester", func(tx *Tx) error { _, _, err := tx.EnqueueOutbox(c); return err })
	wantCode(t, err, CodeInvalid) // Die Aktions-ID folgt aus dem Schlüssel, eine abweichende ist ungültig.

	// Ein Schlüssel ohne Projektbezug ist invalid, und es wird nichts geschrieben.
	d := obAction(t, "wo-2", "x")
	d.OperationKey = "asana:comment:wo-2:x"
	_, err = s.Update(ctxT(t), "tester", func(tx *Tx) error { _, _, err := tx.EnqueueOutbox(d); return err })
	wantCode(t, err, CodeInvalid)
	// Ein Schlüssel eines anderen Projekts bei dieser Aktion ebenso.
	e := obAction(t, "wo-3", "x")
	e.OperationKey, e.ActionID = "other/asana.comment/wo-3/x", domain.OutboxActionID("other/asana.comment/wo-3/x")
	_, err = s.Update(ctxT(t), "tester", func(tx *Tx) error { _, _, err := tx.EnqueueOutbox(e); return err })
	wantCode(t, err, CodeInvalid)
	if obCount(t, s) != 1 {
		t.Errorf("Zeilen nach Fehlern: %d", obCount(t, s))
	}
	// Eine Aktion für ein unbekanntes Projekt: not_found.
	f, _ := domain.NewOutboxAction("nope", "asana.comment", "", nil, "x")
	_, err = s.Update(ctxT(t), "tester", func(tx *Tx) error { _, _, err := tx.EnqueueOutbox(f); return err })
	wantCode(t, err, CodeNotFound)
}

// Enqueue nimmt nur neue Aktionen an: Zustand pending, ohne Versuche, Claim oder Referenz.
func TestEnqueueRequiresInitialValues(t *testing.T) {
	s := obStore(t)
	for name, mod := range map[string]func(*domain.OutboxAction){
		"succeeded":   func(a *domain.OutboxAction) { a.State = domain.OutboxSucceeded; a.ExternalRef = "gid" },
		"mit Versuch": func(a *domain.OutboxAction) { a.Attempts = 1 },
		"mit Fehler":  func(a *domain.OutboxAction) { a.LastError = "x" },
	} {
		a := obAction(t, "wo-1", strings.NewReplacer(" ", "-").Replace(name))
		mod(&a)
		_, err := s.Update(ctxT(t), "tester", func(tx *Tx) error { _, _, err := tx.EnqueueOutbox(a); return err })
		if CodeOf(err) != CodeInvalid {
			t.Errorf("%s: Code %q (%v)", name, CodeOf(err), err)
		}
	}
	if obCount(t, s) != 0 {
		t.Error("ungültige Aktionen wurden geschrieben")
	}
}

// TestEnqueueInSameTransaction: Die Absicht entsteht in derselben Transaktion wie die auslösende Änderung. Ein Rollback
// der Änderung hinterlässt keine Outbox-Zeile; ein Festschreiben beides.
func TestEnqueueInSameTransaction(t *testing.T) {
	s := obStore(t)
	revBefore := revisionOf(t, s, projectID)
	eventsBefore := countEvents(t, s)
	boom := fmt.Errorf("auslösende Änderung scheitert")
	_, err := s.Update(ctxT(t), "tester", func(tx *Tx) error {
		if _, err := tx.CreateWorkOrder(WorkOrderRecord{WorkOrder: fxWorkOrder(t, "wo-9"), GoalID: "ms-1", Kind: supervisor.OrderBug}); err != nil {
			return err
		}
		if _, _, err := tx.EnqueueOutbox(obAction(t, "wo-9", "created")); err != nil {
			return err
		}
		return boom
	})
	if err != boom {
		t.Fatalf("Fehler %v", err)
	}
	if obCount(t, s) != 0 || revisionOf(t, s, projectID) != revBefore || countEvents(t, s) != eventsBefore {
		t.Errorf("Rollback hinterließ Spuren: Zeilen %d, Revision %d, Ereignisse %d", obCount(t, s), revisionOf(t, s, projectID), countEvents(t, s))
	}
	mustView(t, s, func(r *ReadTx) error {
		if _, err := r.WorkOrder(projectID, "wo-9"); CodeOf(err) != CodeNotFound {
			t.Errorf("Auftrag nach Rollback: %v", err)
		}
		return nil
	})

	res := mustUpdate(t, s, func(tx *Tx) error {
		if _, err := tx.CreateWorkOrder(WorkOrderRecord{WorkOrder: fxWorkOrder(t, "wo-9"), GoalID: "ms-1", Kind: supervisor.OrderBug}); err != nil {
			return err
		}
		_, _, err := tx.EnqueueOutbox(obAction(t, "wo-9", "created"))
		return err
	})
	// Die Änderung des Planungszustands erhöht die Revision um 1; die Outbox selbst erhöht sie nicht (Journal).
	if res.Revisions[projectID] != revBefore+1 || obCount(t, s) != 1 {
		t.Errorf("Revisionen %v, Zeilen %d", res.Revisions, obCount(t, s))
	}
}

// Die Outbox ist Journal: Jede Änderung der Outbox allein lässt state_revision unverändert.
func TestOutboxDoesNotBumpRevision(t *testing.T) {
	s := obStore(t)
	rev := revisionOf(t, s, projectID)
	a := obAction(t)
	obEnqueue(t, s, a)
	obClaim(t, s, obT0, time.Minute, 1)
	obDo(t, s, func(tx *Tx) (domain.OutboxAction, error) {
		return tx.CompleteOutbox(projectID, a.ActionID, domain.OutboxClaimed, "gid-1")
	})
	if got := revisionOf(t, s, projectID); got != rev {
		t.Errorf("Revision %d -> %d", rev, got)
	}
	for _, e := range obEvents(t, s) {
		if e.StateRevision != rev {
			t.Errorf("Ereignis %s trägt Revision %d, erwartet %d", e.Kind, e.StateRevision, rev)
		}
	}
}

// ---- Lebenslauf --------------------------------------------------------------------------------------------

func TestOutboxLifecycleAndAudit(t *testing.T) {
	s := obStore(t)
	a := obAction(t)
	obEnqueue(t, s, a)

	claimed := obClaim(t, s, obT0, 2*time.Minute, 10)
	if len(claimed) != 1 || claimed[0].State != domain.OutboxClaimed || claimed[0].Attempts != 1 ||
		!claimed[0].ClaimExpiresAt.Equal(obT0.Add(2*time.Minute)) {
		t.Fatalf("Claim: %+v", claimed)
	}
	if again := obClaim(t, s, obT0, 2*time.Minute, 10); len(again) != 0 {
		t.Errorf("zweiter Claim nimmt die Aktion erneut: %+v", again)
	}
	done := obDo(t, s, func(tx *Tx) (domain.OutboxAction, error) {
		return tx.CompleteOutbox(projectID, a.ActionID, domain.OutboxClaimed, "gid-42")
	})
	if done.State != domain.OutboxSucceeded || done.ExternalRef != "gid-42" || !done.ClaimExpiresAt.IsZero() {
		t.Errorf("Complete: %+v", done)
	}
	if cur := obGet(t, s, a.ActionID); cur.State != domain.OutboxSucceeded || cur.ExternalRef != "gid-42" || cur.Attempts != 1 {
		t.Errorf("gespeichert: %+v", cur)
	}
	// Eine abgeschlossene Aktion bleibt abgeschlossen.
	_, err := s.Update(ctxT(t), "tester", func(tx *Tx) error {
		_, err := tx.CompleteOutbox(projectID, a.ActionID, domain.OutboxSucceeded, "gid-43")
		return err
	})
	wantCode(t, err, CodeState)
	if got := obGet(t, s, a.ActionID); got.ExternalRef != "gid-42" {
		t.Errorf("Referenz überschrieben: %q", got.ExternalRef)
	}

	evs := obEvents(t, s)
	if want := []string{domain.AuditOutboxEnqueued, domain.AuditOutboxClaimed, domain.AuditOutboxSucceeded}; !slices.Equal(obKinds(evs), want) {
		t.Fatalf("Ereignisse %v, erwartet %v", obKinds(evs), want)
	}
	last := evs[2]
	if last.Level != domain.AuditLevelOutbox || last.EntityType != "outbox" || last.EntityID != a.ActionID ||
		last.FromState != "claimed" || last.ToState != "succeeded" || last.Actor != "tester" {
		t.Errorf("Ereignis %+v", last)
	}
	// Die Nutzdaten stehen nie im Ereignis.
	for _, e := range evs {
		for k, v := range e.Details {
			if strings.Contains(v, "fertig") {
				t.Errorf("Ereignis %s verrät Nutzdaten in %s", e.Kind, k)
			}
		}
	}
}

func TestClaimSelectsOnlyDueActions(t *testing.T) {
	s := obStore(t)
	a1, a2, a3, a4 := obAction(t, "wo-1", "a"), obAction(t, "wo-1", "b"), obAction(t, "wo-1", "c"), obAction(t, "wo-1", "d")
	for _, a := range []domain.OutboxAction{a1, a2, a3, a4} {
		obEnqueue(t, s, a)
	}
	// a1 wartet bis T0+10s, a2 ist neu und fällig.
	obClaim(t, s, obT0, time.Minute, 1) // nimmt a1 (die älteste)
	obDo(t, s, func(tx *Tx) (domain.OutboxAction, error) {
		return tx.FailOutbox(projectID, a1.ActionID, domain.OutboxRetryable, domain.OutboxError{Code: "rate_limited"}, obT0.Add(10*time.Second))
	})
	if cur := obGet(t, s, a1.ActionID); cur.State != domain.OutboxRetryWait || !cur.NextAttemptAt.Equal(obT0.Add(10*time.Second)) || !strings.HasPrefix(cur.LastError, "rate_limited") {
		t.Fatalf("retry_wait: %+v", cur)
	}
	got := obClaim(t, s, obT0.Add(5*time.Second), time.Minute, 2)
	if ids := idsOf(got); !slices.Equal(ids, []string{a2.ActionID, a3.ActionID}) {
		t.Fatalf("Claim vor Fälligkeit: %v", ids)
	}
	got = obClaim(t, s, obT0.Add(5*time.Second), time.Minute, 10)
	if ids := idsOf(got); !slices.Equal(ids, []string{a4.ActionID}) {
		t.Fatalf("Rest ohne a1: %v", ids)
	}
	got = obClaim(t, s, obT0.Add(10*time.Second), time.Minute, 10)
	if ids := idsOf(got); !slices.Equal(ids, []string{a1.ActionID}) || got[0].Attempts != 2 || !got[0].NextAttemptAt.IsZero() {
		t.Fatalf("fällige Wiederholung: %+v", got)
	}
	// Ungültige Parameter.
	_, err := s.Update(ctxT(t), "tester", func(tx *Tx) error { _, err := tx.ClaimOutbox(projectID, obT0, time.Minute, 0); return err })
	wantCode(t, err, CodeInvalid)
	_, err = s.Update(ctxT(t), "tester", func(tx *Tx) error { _, err := tx.ClaimOutbox(projectID, obT0, 0, 1); return err })
	wantCode(t, err, CodeInvalid)
}

func idsOf(as []domain.OutboxAction) []string {
	var out []string
	for _, a := range as {
		out = append(out, a.ActionID)
	}
	return out
}

// TestConcurrentClaim: Zwei Store-Instanzen auf derselben Datei und mehrere Goroutinen nehmen jede Aktion genau einmal.
func TestConcurrentClaim(t *testing.T) {
	s1 := obStore(t)
	s2 := openStore(t, s1.Path())
	const n = 24
	for i := range n {
		obEnqueue(t, s1, obAction(t, "wo-1", fmt.Sprintf("c%02d", i)))
	}
	var (
		mu     sync.Mutex
		claims = map[string]int{}
		wg     sync.WaitGroup
	)
	for w := range 6 {
		st := []*Store{s1, s2}[w%2]
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				var got []domain.OutboxAction
				_, err := st.Update(ctxT(t), "worker", func(tx *Tx) error {
					var err error
					got, err = tx.ClaimOutbox(projectID, obT0, time.Minute, 3)
					return err
				})
				if err != nil {
					t.Errorf("Claim: %v", err)
					return
				}
				if len(got) == 0 {
					return
				}
				mu.Lock()
				for _, a := range got {
					claims[a.ActionID]++
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if len(claims) != n {
		t.Errorf("%d von %d Aktionen beansprucht", len(claims), n)
	}
	for id, c := range claims {
		if c != 1 {
			t.Errorf("Aktion %s wurde %d-mal beansprucht", id, c)
		}
	}
	mustView(t, s1, func(r *ReadTx) error {
		as, err := r.OutboxActions(projectID, domain.OutboxClaimed)
		if len(as) != n || err != nil {
			t.Errorf("claimed: %d %v", len(as), err)
		}
		for _, a := range as {
			if a.Attempts != 1 {
				t.Errorf("%s: Versuche %d", a.ActionID, a.Attempts)
			}
		}
		return err
	})
}

// ---- Ungewissheit und Abgleich -----------------------------------------------------------------------------

// Ein abgelaufener Claim (der Prozess starb während des Aufrufs) ist ungewiss: Er geht in den Abgleich, nie nach pending.
func TestExpiredClaimGoesToReconcilingNeverPending(t *testing.T) {
	s := obStore(t)
	a := obAction(t)
	obEnqueue(t, s, a)
	obClaim(t, s, obT0, time.Minute, 1)

	// Neustart des Dienstes; der Claim ist noch gültig.
	s = closeAndReopen(t, s)
	var expired []domain.OutboxAction
	mustUpdate(t, s, func(tx *Tx) error {
		var err error
		expired, err = tx.ExpireOutboxClaims(projectID, obT0.Add(59*time.Second))
		return err
	})
	if len(expired) != 0 || obGet(t, s, a.ActionID).State != domain.OutboxClaimed {
		t.Fatalf("gültiger Claim abgelaufen: %+v", expired)
	}
	// Nach dem Ablauf: reconciling, mit Fehlerkennung, ohne Claim, sofort zum Abgleich fällig.
	mustUpdate(t, s, func(tx *Tx) error {
		var err error
		expired, err = tx.ExpireOutboxClaims(projectID, obT0.Add(time.Minute))
		return err
	})
	if len(expired) != 1 {
		t.Fatalf("abgelaufen: %+v", expired)
	}
	cur := obGet(t, s, a.ActionID)
	if cur.State != domain.OutboxReconciling || !cur.ClaimExpiresAt.IsZero() || cur.Attempts != 1 || !strings.HasPrefix(cur.LastError, "claim_expired") {
		t.Errorf("nach Ablauf: %+v", cur)
	}
	// Ein abgelaufener Claim wird auch von Claim nicht neu vergeben.
	if got := obClaim(t, s, obT0.Add(time.Hour), time.Minute, 5); len(got) != 0 {
		t.Errorf("reconciling wird beansprucht: %+v", got)
	}
	if kinds := obKinds(obEvents(t, s)); !slices.Contains(kinds, domain.AuditOutboxClaimExpired) {
		t.Errorf("Ereignisse %v", kinds)
	}
	var due []domain.OutboxAction
	mustView(t, s, func(r *ReadTx) error {
		var err error
		due, err = r.DueReconcileOutbox(projectID, obT0.Add(time.Minute), 10)
		return err
	})
	if len(due) != 1 {
		t.Errorf("zum Abgleich fällig: %+v", due)
	}
}

func TestMarkUncertainAndReconcileTransitions(t *testing.T) {
	s := obStore(t)
	a := obAction(t)
	obEnqueue(t, s, a)
	obClaim(t, s, obT0, time.Minute, 1)

	u := obDo(t, s, func(tx *Tx) (domain.OutboxAction, error) {
		return tx.MarkOutboxUncertain(projectID, a.ActionID, domain.OutboxError{Code: "timeout", Message: "context deadline exceeded"}, obT0.Add(20*time.Second))
	})
	if u.State != domain.OutboxReconciling || u.ReconcileAttempts != 0 || !u.NextAttemptAt.Equal(obT0.Add(20*time.Second)) || !strings.HasPrefix(u.LastError, "timeout: ") {
		t.Fatalf("MarkUncertain: %+v", u)
	}
	// Vor dem Zeitpunkt nicht fällig, danach fällig.
	for at, want := range map[time.Time]int{obT0.Add(19 * time.Second): 0, obT0.Add(20 * time.Second): 1} {
		var due []domain.OutboxAction
		mustView(t, s, func(r *ReadTx) error {
			var err error
			due, err = r.DueReconcileOutbox(projectID, at, 10)
			return err
		})
		if len(due) != want {
			t.Errorf("fällig um %s: %d, erwartet %d", at, len(due), want)
		}
	}
	// Ungewisser Abgleich: bleibt reconciling, Zähler +1, nächster Zeitpunkt gesetzt.
	r1 := obDo(t, s, func(tx *Tx) (domain.OutboxAction, error) {
		return tx.RetryOutboxReconcile(projectID, a.ActionID, domain.OutboxError{Code: "lookup_failed"}, obT0.Add(time.Minute))
	})
	r2 := obDo(t, s, func(tx *Tx) (domain.OutboxAction, error) {
		return tx.RetryOutboxReconcile(projectID, a.ActionID, domain.OutboxError{Code: "lookup_failed"}, obT0.Add(2*time.Minute))
	})
	if r1.State != domain.OutboxReconciling || r1.ReconcileAttempts != 1 || r2.ReconcileAttempts != 2 || r2.Attempts != 1 {
		t.Fatalf("Abgleichzähler: %+v %+v", r1, r2)
	}
	// „Nicht vorhanden“: zurück nach pending, Abgleichzähler zurückgesetzt, Versuche bleiben (Obergrenze greift weiter).
	p := obDo(t, s, func(tx *Tx) (domain.OutboxAction, error) { return tx.RequeueOutbox(projectID, a.ActionID) })
	if p.State != domain.OutboxPending || p.ReconcileAttempts != 0 || p.Attempts != 1 || !p.NextAttemptAt.IsZero() {
		t.Fatalf("Requeue: %+v", p)
	}
	again := obClaim(t, s, obT0.Add(time.Hour), time.Minute, 1)
	if len(again) != 1 || again[0].Attempts != 2 {
		t.Fatalf("erneuter Claim: %+v", again)
	}
	// Gefunden: reconciling -> succeeded mit Referenz, ohne zweiten Versuch.
	obDo(t, s, func(tx *Tx) (domain.OutboxAction, error) {
		return tx.MarkOutboxUncertain(projectID, a.ActionID, domain.OutboxError{Code: "timeout"}, time.Time{})
	})
	f := obDo(t, s, func(tx *Tx) (domain.OutboxAction, error) {
		return tx.CompleteOutbox(projectID, a.ActionID, domain.OutboxReconciling, "gid-77")
	})
	if f.State != domain.OutboxSucceeded || f.ExternalRef != "gid-77" || f.Attempts != 2 || !f.NextAttemptAt.IsZero() {
		t.Errorf("Complete aus reconciling: %+v", f)
	}
	kinds := obKinds(obEvents(t, s))
	for _, want := range []string{domain.AuditOutboxUncertain, domain.AuditOutboxReconcileRetry, domain.AuditOutboxRequeued, domain.AuditOutboxSucceeded} {
		if !slices.Contains(kinds, want) {
			t.Errorf("Ereignis %s fehlt in %v", want, kinds)
		}
	}
}

func TestFailClasses(t *testing.T) {
	s := obStore(t)
	retry, perm, bad := obAction(t, "wo-1", "r"), obAction(t, "wo-1", "p"), obAction(t, "wo-1", "b")
	for _, a := range []domain.OutboxAction{retry, perm, bad} {
		obEnqueue(t, s, a)
	}
	obClaim(t, s, obT0, time.Minute, 10)
	r := obDo(t, s, func(tx *Tx) (domain.OutboxAction, error) {
		return tx.FailOutbox(projectID, retry.ActionID, domain.OutboxRetryable, domain.OutboxError{Code: "http_503"}, obT0.Add(time.Minute))
	})
	p := obDo(t, s, func(tx *Tx) (domain.OutboxAction, error) {
		return tx.FailOutbox(projectID, perm.ActionID, domain.OutboxPermanent, domain.OutboxError{Code: "http_403", Message: "verboten"}, time.Time{})
	})
	if r.State != domain.OutboxRetryWait || !r.NextAttemptAt.Equal(obT0.Add(time.Minute)) || !r.ClaimExpiresAt.IsZero() {
		t.Errorf("wiederholbar: %+v", r)
	}
	if p.State != domain.OutboxNeedsAttention || !p.NextAttemptAt.IsZero() || p.LastError != "http_403: verboten" {
		t.Errorf("dauerhaft: %+v", p)
	}
	// wiederholbar ohne Zeitpunkt, und ungewiss/Erfolg sind keine Fehlerklassen von Fail.
	for _, c := range []struct {
		class domain.OutboxOutcome
		at    time.Time
	}{{domain.OutboxRetryable, time.Time{}}, {domain.OutboxUncertain, obT0}, {domain.OutboxSuccess, obT0}, {"", obT0}} {
		_, err := s.Update(ctxT(t), "tester", func(tx *Tx) error {
			_, err := tx.FailOutbox(projectID, bad.ActionID, c.class, domain.OutboxError{Code: "x"}, c.at)
			return err
		})
		wantCode(t, err, CodeInvalid)
	}
	if cur := obGet(t, s, bad.ActionID); cur.State != domain.OutboxClaimed {
		t.Errorf("ungültiger Fail hat verändert: %s", cur.State)
	}
	// Fail nur aus claimed.
	_, err := s.Update(ctxT(t), "tester", func(tx *Tx) error {
		_, err := tx.FailOutbox(projectID, perm.ActionID, domain.OutboxPermanent, domain.OutboxError{Code: "x"}, time.Time{})
		return err
	})
	wantCode(t, err, CodeConflict)
}

// TestOutboxErrorTextIsBounded: Ein langer Fehlertext mit Steuerzeichen wird begrenzt und ohne Steuerzeichen gespeichert.
func TestOutboxErrorTextIsBounded(t *testing.T) {
	s := obStore(t)
	a := obAction(t)
	obEnqueue(t, s, a)
	obClaim(t, s, obT0, time.Minute, 1)
	nasty := "Antwort:\n\x1b[31mrot\x00\r\n" + strings.Repeat("Äußerung ", 400)
	got := obDo(t, s, func(tx *Tx) (domain.OutboxAction, error) {
		return tx.FailOutbox(projectID, a.ActionID, domain.OutboxPermanent, domain.OutboxError{Code: "Bad Request!", Message: nasty}, time.Time{})
	})
	stored := obGet(t, s, a.ActionID).LastError
	if stored != got.LastError {
		t.Error("zurückgegebener und gespeicherter Text weichen ab")
	}
	if len(stored) > 400 || len(stored) >= len(nasty) {
		t.Errorf("Text nicht begrenzt: %d Bytes", len(stored))
	}
	for _, r := range stored {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) {
			t.Errorf("Steuerzeichen %q gespeichert", r)
		}
	}
	if !strings.HasPrefix(stored, "bad_request_: Antwort:") {
		t.Errorf("Text %q", stored)
	}
	// Im Audit-Ereignis steht nur die Fehlerkennung, nie der Meldungstext.
	for _, e := range obEvents(t, s) {
		for k, v := range e.Details {
			if strings.Contains(v, "Äußerung") || strings.Contains(v, "Antwort") {
				t.Errorf("Ereignis %s trägt Meldungstext in %s", e.Kind, k)
			}
		}
	}
	// Auch das Datenbankgrenze (1024 Bytes) hält: direkt geschrieben wird abgewiesen.
	wantCode(t, mapErr(execSQL(t, s, "UPDATE outbox SET last_error = ? WHERE action_id = ?", strings.Repeat("x", 1025), a.ActionID)), CodeConstraint)
}

// TestOutboxErrorTextIsRedacted: Typische Geheimnisse im Meldungstext eines Adapters gelangen weder in die Spalte last_error
// noch in den zurückgegebenen Wert noch in ein Ereignis (zweite Sicherung; die Werte sind erfunden).
func TestOutboxErrorTextIsRedacted(t *testing.T) {
	s := obStore(t)
	a := obAction(t)
	obEnqueue(t, s, a)
	obClaim(t, s, obT0, time.Minute, 1)
	leaks := []string{"ghp_ErfundenerWert1234567890abcdef", "pw-erfunden", "erfundenerBearer99"}
	msg := "POST https://nutzer:pw-erfunden@example.org/x?api_key=zzz fehlgeschlagen, Authorization: Bearer erfundenerBearer99, " + leaks[0]
	got := obDo(t, s, func(tx *Tx) (domain.OutboxAction, error) {
		return tx.FailOutbox(projectID, a.ActionID, domain.OutboxPermanent, domain.OutboxError{Code: "http_401", Message: msg}, time.Time{})
	})
	stored := obGet(t, s, a.ActionID).LastError
	for _, text := range []string{stored, got.LastError} {
		for _, leak := range append(leaks, "api_key=zzz") {
			if strings.Contains(text, leak) {
				t.Errorf("%q enthält %q", text, leak)
			}
		}
		if !strings.HasPrefix(text, "http_401: POST ") || !strings.Contains(text, "[redigiert]") || !strings.Contains(text, "fehlgeschlagen") {
			t.Errorf("Text %q", text)
		}
	}
	for _, e := range obEvents(t, s) {
		for k, v := range e.Details {
			for _, leak := range leaks {
				if strings.Contains(v, leak) {
					t.Errorf("Ereignis %s trägt %q in %s", e.Kind, leak, k)
				}
			}
		}
	}
}

func TestEscalateCancelAndResolve(t *testing.T) {
	s := obStore(t)
	mk := func(name string) domain.OutboxAction {
		a := obAction(t, "wo-1", name)
		obEnqueue(t, s, a)
		return a
	}
	// Aktion in needs_attention bringen: pending -> claimed -> needs_attention.
	toAttention := func(a domain.OutboxAction) {
		obClaim(t, s, obT0, time.Minute, 100)
		obDo(t, s, func(tx *Tx) (domain.OutboxAction, error) {
			return tx.EscalateOutbox(projectID, a.ActionID, domain.OutboxClaimed, domain.OutboxError{Code: "retries_exhausted", Message: "5 Versuche"})
		})
	}
	retry, confirm, cancel := mk("retry"), mk("confirm"), mk("cancel")
	toAttention(retry)
	if cur := obGet(t, s, retry.ActionID); cur.State != domain.OutboxNeedsAttention || cur.LastError != "retries_exhausted: 5 Versuche" {
		t.Fatalf("Eskalation: %+v", cur)
	}
	// Die beiden anderen wurden im selben Claim mitgenommen; auch sie eskalieren.
	for _, a := range []domain.OutboxAction{confirm, cancel} {
		obDo(t, s, func(tx *Tx) (domain.OutboxAction, error) {
			return tx.EscalateOutbox(projectID, a.ActionID, domain.OutboxClaimed, domain.OutboxError{Code: "reconcile_exhausted"})
		})
	}
	var attention []domain.OutboxAction
	mustView(t, s, func(r *ReadTx) error {
		var err error
		attention, err = r.OutboxActions(projectID, domain.OutboxNeedsAttention)
		return err
	})
	if len(attention) != 3 {
		t.Fatalf("Klärungsbedarf: %d", len(attention))
	}

	resolve := func(a domain.OutboxAction, actor string, r domain.OutboxResolution) (domain.OutboxAction, error) {
		var got domain.OutboxAction
		_, err := s.Update(ctxT(t), actor, func(tx *Tx) error {
			var err error
			got, err = tx.ResolveOutbox(projectID, a.ActionID, r)
			return err
		})
		return got, err
	}
	got, err := resolve(retry, "alex", domain.OutboxResolution{Action: domain.ResolveRetry, Reason: "Zugriff repariert"})
	must(t, err)
	if got.State != domain.OutboxPending || got.Attempts != 0 || got.ReconcileAttempts != 0 {
		t.Errorf("retry: %+v", got)
	}
	got, err = resolve(confirm, "alex", domain.OutboxResolution{Action: domain.ResolveConfirm, ExternalRef: "gid-9", Reason: "Kommentar gefunden"})
	must(t, err)
	if got.State != domain.OutboxSucceeded || got.ExternalRef != "gid-9" {
		t.Errorf("confirm: %+v", got)
	}
	got, err = resolve(cancel, "alex", domain.OutboxResolution{Action: domain.ResolveCancel, Reason: "Ticket gelöscht"})
	must(t, err)
	if got.State != domain.OutboxCancelled || !got.State.IsTerminal() {
		t.Errorf("cancel: %+v", got)
	}

	var resolved []domain.AuditEvent
	for _, e := range obEvents(t, s) {
		if e.Kind == domain.AuditOutboxResolved {
			resolved = append(resolved, e)
		}
	}
	if len(resolved) != 3 {
		t.Fatalf("%d Klärungsereignisse", len(resolved))
	}
	for i, want := range []struct{ action, to, ref string }{{"retry", "pending", ""}, {"confirm", "succeeded", "gid-9"}, {"cancel", "cancelled", ""}} {
		e := resolved[i]
		if e.Actor != "alex" || e.FromState != "needs_attention" || e.ToState != want.to || e.Details["resolution"] != want.action ||
			e.Details["reason"] == "" || e.Details["external_ref"] != want.ref {
			t.Errorf("Klärungsereignis %d: %+v", i, e)
		}
	}
	// Klären geht nur aus needs_attention und nur mit gültigem Vertrag.
	_, err = resolve(retry, "alex", domain.OutboxResolution{Action: domain.ResolveCancel, Reason: "zu spät"})
	wantCode(t, err, CodeConflict)
	_, err = resolve(confirm, "alex", domain.OutboxResolution{Action: domain.ResolveCancel})
	wantCode(t, err, CodeInvalid)
	_, err = resolve(confirm, "alex", domain.OutboxResolution{Action: domain.ResolveConfirm, Reason: "x"})
	wantCode(t, err, CodeInvalid)
	_, err = resolve(domain.OutboxAction{ActionID: "unbekannt"}, "alex", domain.OutboxResolution{Action: domain.ResolveCancel, Reason: "x"})
	wantCode(t, err, CodeNotFound)

	// Abbrechen einer noch nicht begonnenen Aktion.
	w := mk("withdrawn")
	cancelled := obDo(t, s, func(tx *Tx) (domain.OutboxAction, error) {
		return tx.CancelOutbox(projectID, w.ActionID, domain.OutboxPending, "Auftrag zurückgezogen")
	})
	if cancelled.State != domain.OutboxCancelled {
		t.Errorf("CancelOutbox: %+v", cancelled)
	}
	_, err = s.Update(ctxT(t), "tester", func(tx *Tx) error {
		_, err := tx.CancelOutbox(projectID, w.ActionID, domain.OutboxCancelled, "nochmal")
		return err
	})
	wantCode(t, err, CodeState)
}

// Mit erwartetem Ist-Zustand: ein falscher Erwartungswert ändert nichts und liefert conflict.
func TestOutboxExpectedStateAndScoping(t *testing.T) {
	s := obStore(t)
	a := obAction(t)
	obEnqueue(t, s, a)
	_, err := s.Update(ctxT(t), "tester", func(tx *Tx) error {
		_, err := tx.CompleteOutbox(projectID, a.ActionID, domain.OutboxClaimed, "gid") // ist aber pending
		return err
	})
	wantCode(t, err, CodeConflict)
	// pending -> succeeded ist nach der Tabelle nicht erlaubt: state, nicht conflict.
	_, err = s.Update(ctxT(t), "tester", func(tx *Tx) error {
		_, err := tx.CompleteOutbox(projectID, a.ActionID, domain.OutboxPending, "gid")
		return err
	})
	wantCode(t, err, CodeState)
	if cur := obGet(t, s, a.ActionID); cur.State != domain.OutboxPending {
		t.Errorf("Zustand verändert: %s", cur.State)
	}
	// Eine Aktion eines anderen Projekts ist unsichtbar.
	mustView(t, s, func(r *ReadTx) error {
		if _, err := r.OutboxAction("anderes", a.ActionID); CodeOf(err) != CodeNotFound {
			t.Errorf("fremdes Projekt: %v", err)
		}
		if _, err := r.OutboxActionByKey("anderes", a.OperationKey); CodeOf(err) != CodeNotFound {
			t.Errorf("fremdes Projekt, Schlüssel: %v", err)
		}
		if got, err := r.OutboxActionByKey(projectID, a.OperationKey); err != nil || got.ActionID != a.ActionID {
			t.Errorf("nach Schlüssel: %+v %v", got, err)
		}
		if _, err := r.OutboxActionByKey(projectID, "ccmas/asana.comment/nope"); CodeOf(err) != CodeNotFound {
			t.Errorf("unbekannter Schlüssel: %v", err)
		}
		return nil
	})
}

func TestOutboxWakeup(t *testing.T) {
	s := obStore(t)
	check := func(wantOK bool, want time.Time) {
		t.Helper()
		mustView(t, s, func(r *ReadTx) error {
			at, ok, err := r.OutboxWakeup(projectID)
			if err != nil || ok != wantOK || !at.Equal(want) {
				t.Errorf("Wakeup %v %v %v, erwartet %v %v", at, ok, err, want, wantOK)
			}
			return nil
		})
	}
	check(false, time.Time{})
	a, b := obAction(t, "wo-1", "a"), obAction(t, "wo-1", "b")
	obEnqueue(t, s, a)
	obEnqueue(t, s, b)
	check(true, time.Time{})             // neue Aktionen sind sofort fällig
	obClaim(t, s, obT0, time.Minute, 10) // beide claimed bis T0+1m
	check(true, obT0.Add(time.Minute))
	obDo(t, s, func(tx *Tx) (domain.OutboxAction, error) {
		return tx.FailOutbox(projectID, a.ActionID, domain.OutboxRetryable, domain.OutboxError{Code: "x"}, obT0.Add(10*time.Second))
	})
	check(true, obT0.Add(10*time.Second))
}
