package persistence

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/alexwenzel/ccmas/internal/domain"
)

// Outbox für externe Aktionen (CC-45, Spezifikation „Externe Aktionen ohne Dubletten“). Die Absicht, in einem externen
// System zu schreiben, wird vor der Ausführung gespeichert (EnqueueOutbox, in derselben Transaktion wie die auslösende
// Änderung). Der Lebenslauf folgt domain.OutboxState; die Datenbank erzwingt dieselbe Tabelle durch Trigger.
//
// Alle Änderungen erfolgen mit erwartetem Ist-Zustand (sonst conflict), prüfen CanTransition (sonst state) und schreiben
// ihr Audit-Ereignis (Ebene outbox) in derselben Transaktion. Die Outbox ist Journal und erhöht state_revision nicht (siehe
// doc.go). Zeiten, nach denen entschieden wird (Fälligkeit, Claim-Ablauf), übergibt der Aufrufer; die Uhr des Stores stempelt
// nur updated_at und die Ereignisse.
//
// Fehlertexte: Es wird nur die Fehlerkennung gespeichert (domain.OutboxError: Code und kurze Meldung, begrenzt, ohne
// Steuerzeichen, typische Geheimnisse maskiert: domain.RedactSecrets). Die Maskierung ist nur die zweite Sicherung: Adapter
// dürfen keine Zugangsdaten, Header oder Antwortkörper in Meldungen geben; das kann keine Prüfung beweisen. Das
// Audit-Ereignis trägt nur den Code, nie die Meldung und nie die Nutzdaten.

// maxClaimBatch begrenzt die Aktionen eines Claim-Aufrufs.
const maxClaimBatch = 1000

const outboxColumns = `action_id, project_id, operation_key, kind, target, payload_json, state, attempts, reconcile_attempts,
	next_attempt_at, claim_expires_at, last_error, external_ref, created_at, updated_at`

func scanOutbox(sc interface{ Scan(...any) error }) (domain.OutboxAction, error) {
	var (
		a                  domain.OutboxAction
		payload, state     string
		next, claim, ref   sql.NullString
		createdS, updatedS string
	)
	if err := sc.Scan(&a.ActionID, &a.ProjectID, &a.OperationKey, &a.Kind, &a.Target, &payload, &state, &a.Attempts,
		&a.ReconcileAttempts, &next, &claim, &a.LastError, &ref, &createdS, &updatedS); err != nil {
		return a, err
	}
	a.Payload, a.State, a.ExternalRef = json.RawMessage(payload), domain.OutboxState(state), str(ref)
	var err error
	if a.NextAttemptAt, err = scanNullTime(next); err != nil {
		return a, err
	}
	if a.ClaimExpiresAt, err = scanNullTime(claim); err != nil {
		return a, err
	}
	if a.CreatedAt, err = scanTime(createdS); err != nil {
		return a, err
	}
	a.UpdatedAt, err = scanTime(updatedS)
	return a, err
}

// ---- Lesen -------------------------------------------------------------------------------------------------

// OutboxAction liefert eine Aktion des Projekts.
func (r *reader) OutboxAction(projectID, actionID string) (domain.OutboxAction, error) {
	if err := checkIDs(projectID, actionID); err != nil {
		return domain.OutboxAction{}, err
	}
	a, err := scanOutbox(r.q.QueryRowContext(r.ctx, "SELECT "+outboxColumns+" FROM outbox WHERE project_id = ? AND action_id = ?", projectID, actionID))
	if errors.Is(err, sql.ErrNoRows) {
		return a, errf(CodeNotFound, "Outbox-Aktion %q existiert nicht", actionID)
	}
	return a, mapErr(err)
}

// OutboxActionByKey liefert die Aktion zu einem operation_key des Projekts.
func (r *reader) OutboxActionByKey(projectID, key string) (domain.OutboxAction, error) {
	if err := checkIDs(projectID); err != nil {
		return domain.OutboxAction{}, err
	}
	if err := domain.CheckOperationKey(key); err != nil {
		return domain.OutboxAction{}, invalid(err)
	}
	a, err := scanOutbox(r.q.QueryRowContext(r.ctx, "SELECT "+outboxColumns+" FROM outbox WHERE project_id = ? AND operation_key = ?", projectID, key))
	if errors.Is(err, sql.ErrNoRows) {
		return a, errf(CodeNotFound, "Outbox-Aktion zum Schlüssel %q existiert nicht", key)
	}
	return a, mapErr(err)
}

func (r *reader) outboxList(where string, limit int, args ...any) ([]domain.OutboxAction, error) {
	query := "SELECT " + outboxColumns + " FROM outbox WHERE " + where
	if limit > 0 {
		query += " LIMIT " + itoa(int64(limit))
	}
	rows, err := r.q.QueryContext(r.ctx, query, args...)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	var out []domain.OutboxAction
	for rows.Next() {
		a, err := scanOutbox(rows)
		if err != nil {
			return nil, mapErr(err)
		}
		out = append(out, a)
	}
	return out, mapErr(rows.Err())
}

const dueOutboxWhere = `project_id = ? AND ((state = 'pending' AND (next_attempt_at IS NULL OR next_attempt_at <= ?))
	OR (state = 'retry_wait' AND next_attempt_at <= ?)) ORDER BY COALESCE(next_attempt_at, created_at), created_at, action_id`

// DueOutbox liefert die Aktionen, die jetzt ausgeführt werden dürfen: pending (ohne oder mit erreichtem Zeitpunkt) und
// retry_wait mit erreichtem Zeitpunkt, in fester Reihenfolge (Fälligkeit, Erzeugung, ID). Limit 0 heißt unbegrenzt.
func (r *reader) DueOutbox(projectID string, now time.Time, limit int) ([]domain.OutboxAction, error) {
	if err := checkIDs(projectID); err != nil {
		return nil, err
	}
	return r.outboxList(dueOutboxWhere, limit, projectID, fmtTime(now), fmtTime(now))
}

// DueReconcileOutbox liefert die Aktionen im Abgleich (reconciling), deren nächster Abgleich fällig ist.
func (r *reader) DueReconcileOutbox(projectID string, now time.Time, limit int) ([]domain.OutboxAction, error) {
	if err := checkIDs(projectID); err != nil {
		return nil, err
	}
	return r.outboxList(`project_id = ? AND state = 'reconciling' AND (next_attempt_at IS NULL OR next_attempt_at <= ?)
		ORDER BY COALESCE(next_attempt_at, updated_at), action_id`, limit, projectID, fmtTime(now))
}

// OutboxActions liefert die Aktionen des Projekts in den genannten Zuständen (ohne Angabe: alle), nach Erzeugung sortiert.
// needs_attention nennt den sichtbaren Klärungsbedarf.
func (r *reader) OutboxActions(projectID string, states ...domain.OutboxState) ([]domain.OutboxAction, error) {
	if err := checkIDs(projectID); err != nil {
		return nil, err
	}
	where, args := "project_id = ?", []any{projectID}
	if len(states) > 0 {
		marks := make([]string, len(states))
		for i, s := range states {
			if !s.Valid() {
				return nil, invalidf("unbekannter Outbox-Zustand %q", s)
			}
			marks[i] = "?"
			args = append(args, string(s))
		}
		where += " AND state IN (" + strings.Join(marks, ",") + ")"
	}
	return r.outboxList(where+" ORDER BY created_at, action_id", 0, args...)
}

// OutboxWakeup liefert den frühesten Zeitpunkt, zu dem die Outbox wieder etwas tun muss: ein nächster Versuch oder Abgleich,
// oder der Ablauf eines Claims. ok=false: nichts steht an. ok=true mit Nullzeit: es ist sofort etwas fällig (eine neue
// Aktion oder ein Abgleich ohne Zeitpunkt).
func (r *reader) OutboxWakeup(projectID string) (at time.Time, ok bool, err error) {
	if err := checkIDs(projectID); err != nil {
		return time.Time{}, false, err
	}
	var immediate int
	var earliest sql.NullString
	err = r.q.QueryRowContext(r.ctx, `SELECT
		EXISTS (SELECT 1 FROM outbox WHERE project_id = ?1 AND state IN ('pending','reconciling') AND next_attempt_at IS NULL),
		(SELECT min(t) FROM (
			SELECT next_attempt_at AS t FROM outbox WHERE project_id = ?1 AND state IN ('pending','retry_wait','reconciling') AND next_attempt_at IS NOT NULL
			UNION ALL
			SELECT claim_expires_at AS t FROM outbox WHERE project_id = ?1 AND state = 'claimed'))`, projectID).Scan(&immediate, &earliest)
	if err != nil {
		return time.Time{}, false, mapErr(err)
	}
	if immediate == 1 {
		return time.Time{}, true, nil
	}
	if !earliest.Valid {
		return time.Time{}, false, nil
	}
	at, err = scanTime(earliest.String)
	return at, err == nil, err
}

// ---- Schreiben ---------------------------------------------------------------------------------------------

// EnqueueOutbox speichert die Absicht einer externen Aktion im Zustand pending und schreibt ihr Ereignis. Der Aufruf gehört in
// dieselbe Transaktion wie die fachliche Änderung, die die Aktion auslöst: Ein Rollback nimmt beides zurück, ein Festschreiben
// hinterlässt beides. Gleicher Schlüssel mit gleichem Inhalt (Art, Ziel, kanonische Nutzdaten) ist ein No-op ohne Ereignis
// (created=false, auch nach einem Fortschritt der Aktion); mit anderem Inhalt, auch in einem anderen Projekt, ein conflict
// (operation_key ist global eindeutig). Eine Aktion ohne Projektbezug im Schlüssel, mit abweichender Aktions-ID oder nicht im
// Anfangszustand ist invalid, ein unbekanntes Projekt not_found. Leere ActionID: wird aus dem Schlüssel gebildet.
func (t *Tx) EnqueueOutbox(a domain.OutboxAction) (action domain.OutboxAction, created bool, err error) {
	err = t.write(func() error {
		if a.ActionID == "" && a.OperationKey != "" {
			a.ActionID = domain.OutboxActionID(a.OperationKey)
		}
		if a.State == "" {
			a.State = domain.OutboxPending
		}
		if err := a.Check(); err != nil {
			return invalid(err)
		}
		if a.State != domain.OutboxPending || a.Attempts != 0 || a.ReconcileAttempts != 0 || a.LastError != "" {
			return invalidf("eine neue Outbox-Aktion beginnt pending, ohne Versuche und ohne Fehler")
		}
		payload, err := a.CanonicalPayload()
		if err != nil {
			return invalid(err)
		}

		var existingProject string
		err = t.q.QueryRowContext(t.ctx, "SELECT project_id FROM outbox WHERE operation_key = ?", a.OperationKey).Scan(&existingProject)
		switch {
		case err == nil:
			if existingProject != a.ProjectID {
				return errf(CodeConflict, "der Schlüssel %q gehört zu einem anderen Projekt", a.OperationKey)
			}
			cur, err := t.OutboxActionByKey(a.ProjectID, a.OperationKey)
			if err != nil {
				return err
			}
			if cur.ActionID != a.ActionID || cur.Kind != a.Kind || cur.Target != a.Target || string(cur.Payload) != string(payload) {
				return errf(CodeConflict, "der Schlüssel %q existiert mit anderem Inhalt", a.OperationKey)
			}
			action = cur
			return nil
		case !errors.Is(err, sql.ErrNoRows):
			return err
		}
		if _, err := t.revision(a.ProjectID); err != nil {
			return err
		}
		now := fmtTime(t.at)
		if _, err := t.q.ExecContext(t.ctx, `INSERT INTO outbox (action_id, project_id, operation_key, kind, target, payload_json, state,
			next_attempt_at, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, 'pending', ?, ?, ?)`,
			a.ActionID, a.ProjectID, a.OperationKey, a.Kind, a.Target, string(payload), nullTime(a.NextAttemptAt), now, now); err != nil {
			return err
		}
		if action, err = t.OutboxAction(a.ProjectID, a.ActionID); err != nil {
			return err
		}
		created = true
		return t.journal(a.ProjectID, eventSpec{level: domain.AuditLevelOutbox, kind: domain.AuditOutboxEnqueued, entityType: "outbox",
			entityID: a.ActionID, to: string(domain.OutboxPending),
			details: map[string]string{"action_kind": a.Kind, "operation_key": a.OperationKey}})
	})
	if err != nil {
		return domain.OutboxAction{}, false, err
	}
	return action, created, nil
}

// outboxMove führt eine Aktion von from nach to: Übergangstabelle (sonst state), erwarteter Ist-Zustand (sonst conflict),
// Änderung durch mutate, Ereignis. Sie läuft innerhalb von write.
func (t *Tx) outboxMove(projectID, actionID string, from, to domain.OutboxState, kind string, mutate func(*domain.OutboxAction), extra map[string]string) (domain.OutboxAction, error) {
	if err := checkIDs(projectID, actionID); err != nil {
		return domain.OutboxAction{}, err
	}
	if !from.CanTransition(to) {
		return domain.OutboxAction{}, stateErr("Outbox-Zustand %q -> %q ist nicht erlaubt", from, to)
	}
	cur, err := t.OutboxAction(projectID, actionID)
	if err != nil {
		return domain.OutboxAction{}, err
	}
	if cur.State != from {
		return domain.OutboxAction{}, errf(CodeConflict, "Outbox-Aktion %q ist %q, erwartet war %q", actionID, cur.State, from)
	}
	next := cur
	next.State, next.UpdatedAt = to, t.at
	next.ClaimExpiresAt = time.Time{}
	mutate(&next)
	res, err := t.q.ExecContext(t.ctx, `UPDATE outbox SET state = ?, attempts = ?, reconcile_attempts = ?, next_attempt_at = ?,
		claim_expires_at = ?, last_error = ?, external_ref = ?, updated_at = ?
		WHERE project_id = ? AND action_id = ? AND state = ?`,
		string(to), next.Attempts, next.ReconcileAttempts, nullTime(next.NextAttemptAt), nullTime(next.ClaimExpiresAt), next.LastError,
		orNil(next.ExternalRef), fmtTime(next.UpdatedAt), projectID, actionID, string(from))
	if err != nil {
		return domain.OutboxAction{}, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return domain.OutboxAction{}, errf(CodeConflict, "Outbox-Aktion %q hat sich geändert", actionID)
	}
	details := map[string]string{"action_kind": next.Kind, "attempts": itoa(int64(next.Attempts))}
	if next.ReconcileAttempts > 0 {
		details["reconcile_attempts"] = itoa(int64(next.ReconcileAttempts))
	}
	for k, v := range extra {
		details[k] = v
	}
	if err := t.journal(projectID, eventSpec{level: domain.AuditLevelOutbox, kind: kind, entityType: "outbox", entityID: actionID,
		from: string(from), to: string(to), details: details}); err != nil {
		return domain.OutboxAction{}, err
	}
	return next, nil
}

// moveOne führt eine Änderung als eigene write-Operation aus.
func (t *Tx) moveOne(projectID, actionID string, from, to domain.OutboxState, kind string, mutate func(*domain.OutboxAction), extra map[string]string) (domain.OutboxAction, error) {
	var out domain.OutboxAction
	err := t.write(func() error {
		var err error
		out, err = t.outboxMove(projectID, actionID, from, to, kind, mutate, extra)
		return err
	})
	if err != nil {
		return domain.OutboxAction{}, err
	}
	return out, nil
}

// ClaimOutbox beansprucht bis zu limit fällige Aktionen (pending und retry_wait mit erreichtem Zeitpunkt) in fester
// Reihenfolge: Zustand claimed, Versuch +1, Claim bis now+ttl. Auswahl und Änderung geschehen in einer IMMEDIATE-Transaktion;
// zwei konkurrierende Aufrufe, auch aus zwei Prozessen, nehmen dieselbe Aktion nie zweimal.
func (t *Tx) ClaimOutbox(projectID string, now time.Time, ttl time.Duration, limit int) ([]domain.OutboxAction, error) {
	var out []domain.OutboxAction
	err := t.write(func() error {
		switch {
		case now.IsZero():
			return invalidf("der Zeitpunkt des Claims fehlt")
		case ttl <= 0:
			return invalidf("die Gültigkeit des Claims muss positiv sein")
		case limit < 1 || limit > maxClaimBatch:
			return invalidf("die Zahl der Claims muss zwischen 1 und %d liegen", maxClaimBatch)
		}
		due, err := t.DueOutbox(projectID, now, limit)
		if err != nil {
			return err
		}
		expires := now.Add(ttl)
		for _, a := range due {
			claimed, err := t.outboxMove(projectID, a.ActionID, a.State, domain.OutboxClaimed, domain.AuditOutboxClaimed, func(n *domain.OutboxAction) {
				n.Attempts++
				n.NextAttemptAt = time.Time{}
				n.ClaimExpiresAt = expires
			}, nil)
			if err != nil {
				return err
			}
			out = append(out, claimed)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ExpireOutboxClaims überführt Aktionen, deren Claim zum Zeitpunkt now abgelaufen ist, nach reconciling. Der Prozess, der
// den Aufruf machte, ist gestorben oder hängt: Ob das externe System die Aktion ausgeführt hat, ist unbekannt. Ein
// abgelaufener Claim gilt deshalb als ungewiss und geht in den Abgleich, nie zurück nach pending. Der Abgleich ist sofort fällig.
func (t *Tx) ExpireOutboxClaims(projectID string, now time.Time) ([]domain.OutboxAction, error) {
	var out []domain.OutboxAction
	err := t.write(func() error {
		if now.IsZero() {
			return invalidf("der Zeitpunkt fehlt")
		}
		claimed, err := t.outboxList(`project_id = ? AND state = 'claimed' AND claim_expires_at <= ? ORDER BY claim_expires_at, action_id`, 0, projectID, fmtTime(now))
		if err != nil {
			return err
		}
		e := domain.OutboxError{Code: "claim_expired", Message: "Claim abgelaufen; Ausgang des Aufrufs unbekannt"}
		for _, a := range claimed {
			moved, err := t.outboxMove(projectID, a.ActionID, domain.OutboxClaimed, domain.OutboxReconciling, domain.AuditOutboxClaimExpired,
				func(n *domain.OutboxAction) { n.LastError = e.String() }, map[string]string{"error_code": e.Normalized().Code})
			if err != nil {
				return err
			}
			out = append(out, moved)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// CompleteOutbox schließt die Aktion als erledigt ab: succeeded mit der externen Referenz. from ist claimed (Aufruf gelang)
// oder reconciling (der Abgleich fand das Objekt, oder das Ergebnis traf nach Ablauf des Claims ein). Aus needs_attention
// führt nur ResolveOutbox.
func (t *Tx) CompleteOutbox(projectID, actionID string, from domain.OutboxState, externalRef string) (domain.OutboxAction, error) {
	if err := domain.CheckExternalRef(externalRef); err != nil {
		return domain.OutboxAction{}, t.fail(invalid(err))
	}
	return t.moveOne(projectID, actionID, from, domain.OutboxSucceeded, domain.AuditOutboxSucceeded, func(n *domain.OutboxAction) {
		n.NextAttemptAt, n.ExternalRef = time.Time{}, externalRef
	}, map[string]string{"external_ref": externalRef, "via": string(from)})
}

// FailOutbox hält den Fehlschlag eines Aufrufs fest (die Aktion ist claimed). Wiederholbar (domain.OutboxRetryable): retry_wait
// mit dem Zeitpunkt retryAt (Pflicht). Dauerhaft (domain.OutboxPermanent): needs_attention. Ungewiss oder Erfolg sind keine
// Fehlerklassen dieser Methode (MarkOutboxUncertain, CompleteOutbox). Die Fehlerkennung wird begrenzt gespeichert.
func (t *Tx) FailOutbox(projectID, actionID string, class domain.OutboxOutcome, e domain.OutboxError, retryAt time.Time) (domain.OutboxAction, error) {
	var (
		to   domain.OutboxState
		kind string
	)
	switch class {
	case domain.OutboxRetryable:
		if retryAt.IsZero() {
			return domain.OutboxAction{}, t.fail(invalidf("ein wiederholbarer Fehler braucht den Zeitpunkt des nächsten Versuchs"))
		}
		to, kind = domain.OutboxRetryWait, domain.AuditOutboxRetryScheduled
	case domain.OutboxPermanent:
		to, kind = domain.OutboxNeedsAttention, domain.AuditOutboxNeedsAttention
		retryAt = time.Time{}
	default:
		return domain.OutboxAction{}, t.fail(invalidf("die Fehlerklasse %q gehört nicht zu FailOutbox", class))
	}
	extra := map[string]string{"error_code": e.Normalized().Code, "class": string(class)}
	if !retryAt.IsZero() {
		extra["next_attempt_at"] = fmtTime(retryAt)
	}
	return t.moveOne(projectID, actionID, domain.OutboxClaimed, to, kind, func(n *domain.OutboxAction) {
		n.NextAttemptAt, n.LastError = retryAt, e.String()
	}, extra)
}

// MarkOutboxUncertain hält einen ungewissen Ausgang fest (die Aktion ist claimed): Timeout, abgebrochene Verbindung oder eine
// Antwort ohne verwertbare Referenz beweisen weder Erfolg noch Misserfolg. Die Aktion geht nach reconciling, nie nach
// retry_wait oder pending. nextLookup ist der Zeitpunkt des ersten Abgleichs (Nullzeit: sofort).
func (t *Tx) MarkOutboxUncertain(projectID, actionID string, e domain.OutboxError, nextLookup time.Time) (domain.OutboxAction, error) {
	extra := map[string]string{"error_code": e.Normalized().Code}
	if !nextLookup.IsZero() {
		extra["next_attempt_at"] = fmtTime(nextLookup)
	}
	return t.moveOne(projectID, actionID, domain.OutboxClaimed, domain.OutboxReconciling, domain.AuditOutboxUncertain, func(n *domain.OutboxAction) {
		n.NextAttemptAt, n.LastError = nextLookup, e.String()
	}, extra)
}

// RetryOutboxReconcile hält einen nicht schlüssigen Abgleich fest: die Aktion bleibt reconciling, der Abgleichzähler steigt um
// 1, nextLookup ist der Zeitpunkt des nächsten Abgleichs.
func (t *Tx) RetryOutboxReconcile(projectID, actionID string, e domain.OutboxError, nextLookup time.Time) (domain.OutboxAction, error) {
	extra := map[string]string{"error_code": e.Normalized().Code}
	if !nextLookup.IsZero() {
		extra["next_attempt_at"] = fmtTime(nextLookup)
	}
	return t.moveOne(projectID, actionID, domain.OutboxReconciling, domain.OutboxReconciling, domain.AuditOutboxReconcileRetry, func(n *domain.OutboxAction) {
		n.ReconcileAttempts++
		n.NextAttemptAt, n.LastError = nextLookup, e.String()
	}, extra)
}

// RequeueOutbox beendet den Abgleich mit dem Beleg „das externe Objekt existiert nicht“: zurück nach pending (sofort fällig),
// Abgleichzähler zurückgesetzt. Die Versuche bleiben, damit die Höchstzahl weiter greift. Nur mit sicherem Beleg aufrufen.
func (t *Tx) RequeueOutbox(projectID, actionID string) (domain.OutboxAction, error) {
	return t.moveOne(projectID, actionID, domain.OutboxReconciling, domain.OutboxPending, domain.AuditOutboxRequeued, func(n *domain.OutboxAction) {
		n.NextAttemptAt, n.ReconcileAttempts = time.Time{}, 0
	}, nil)
}

// EscalateOutbox macht den Klärungsbedarf sichtbar: needs_attention aus claimed, retry_wait oder reconciling (Höchstzahl der
// Versuche oder Abgleiche erreicht, Abgleich dauerhaft unmöglich).
func (t *Tx) EscalateOutbox(projectID, actionID string, from domain.OutboxState, e domain.OutboxError) (domain.OutboxAction, error) {
	return t.moveOne(projectID, actionID, from, domain.OutboxNeedsAttention, domain.AuditOutboxNeedsAttention, func(n *domain.OutboxAction) {
		n.NextAttemptAt, n.LastError = time.Time{}, e.String()
	}, map[string]string{"error_code": e.Normalized().Code})
}

// CancelOutbox bricht eine noch nicht begonnene Aktion ab (cancelled): aus pending oder retry_wait, etwa weil die auslösende
// Änderung zurückgenommen wurde. Der Grund ist Pflicht. Aus needs_attention bricht ResolveOutbox ab.
func (t *Tx) CancelOutbox(projectID, actionID string, from domain.OutboxState, reason string) (domain.OutboxAction, error) {
	if err := checkReason(reason); err != nil {
		return domain.OutboxAction{}, t.fail(err)
	}
	return t.moveOne(projectID, actionID, from, domain.OutboxCancelled, domain.AuditOutboxCancelled, func(n *domain.OutboxAction) {
		n.NextAttemptAt = time.Time{}
	}, map[string]string{"reason": reason})
}

// ResolveOutbox beendet einen Klärungsbedarf ausdrücklich (die Aktion ist needs_attention): erneut versuchen (pending, Zähler
// zurückgesetzt), als erledigt bestätigen (succeeded mit externer Referenz) oder abbrechen (cancelled). Akteur ist der Akteur
// der Transaktion; Weg, Grund und Referenz stehen im Ereignis outbox.resolved. Wer erneut versuchen lässt, verantwortet, dass
// das externe Objekt nicht existiert.
func (t *Tx) ResolveOutbox(projectID, actionID string, r domain.OutboxResolution) (domain.OutboxAction, error) {
	if err := r.Check(); err != nil {
		return domain.OutboxAction{}, t.fail(invalid(err))
	}
	extra := map[string]string{"resolution": string(r.Action), "reason": r.Reason}
	var (
		to     domain.OutboxState
		mutate func(*domain.OutboxAction)
	)
	switch r.Action {
	case domain.ResolveRetry:
		to = domain.OutboxPending
		mutate = func(n *domain.OutboxAction) { n.Attempts, n.ReconcileAttempts, n.NextAttemptAt = 0, 0, time.Time{} }
	case domain.ResolveConfirm:
		to = domain.OutboxSucceeded
		extra["external_ref"] = r.ExternalRef
		mutate = func(n *domain.OutboxAction) { n.ExternalRef, n.NextAttemptAt = r.ExternalRef, time.Time{} }
	default:
		to = domain.OutboxCancelled
		mutate = func(n *domain.OutboxAction) { n.NextAttemptAt = time.Time{} }
	}
	return t.moveOne(projectID, actionID, domain.OutboxNeedsAttention, to, domain.AuditOutboxResolved, mutate, extra)
}

// checkReason prüft den Grund einer Abbruchs.
func checkReason(reason string) error {
	if strings.TrimSpace(reason) == "" {
		return invalidf("ein Grund ist Pflicht")
	}
	return invalid(checkText("Grund", reason, domain.MaxOutboxReasonBytes, false, true))
}

// fail verbraucht die Transaktion mit einem Fehler vor dem Schreiben (wie ein Fehler innerhalb von write).
func (t *Tx) fail(err error) error {
	return t.write(func() error { return err })
}
