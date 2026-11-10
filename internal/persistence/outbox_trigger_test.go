package persistence

import (
	"strings"
	"testing"
	"time"

	"github.com/alexwenzel/ccmas/internal/domain"
)

// obInsertSQL legt direkt per SQL eine Zeile an (Projekt ccmas, Art asana.comment). cols und vals ergänzen Spalten.
func obInsertSQL(t *testing.T, s *Store, id, key, state, cols, vals string) error {
	t.Helper()
	return mapErr(execSQL(t, s, `INSERT INTO outbox (action_id, project_id, operation_key, kind, payload_json, state, created_at, updated_at`+cols+`)
		VALUES (?, 'ccmas', ?, 'asana.comment', '{}', ?, ?, ?`+vals+`)`, id, key, state, ts, ts))
}

// TestOutboxInsertTriggers: Eine neue Zeile beginnt pending, ohne Versuche, Claim, Referenz und Fehler, und ihr
// Schlüssel beginnt mit <Projekt>/<Art>/ (operation_key ist global eindeutig).
func TestOutboxInsertTriggers(t *testing.T) {
	s := obStore(t)
	if err := obInsertSQL(t, s, "ok-1", "ccmas/asana.comment/ok1", "pending", "", ""); err != nil {
		t.Fatalf("gültige Zeile: %v", err)
	}
	if err := obInsertSQL(t, s, "ok-2", "ccmas/asana.comment/a/b", "pending", ", next_attempt_at", ", '"+ts+"'"); err != nil {
		t.Errorf("Start mit Zeitpunkt: %v", err)
	}
	bad := []struct{ name, key, state, cols, vals string }{
		{"Zustand claimed", "ccmas/asana.comment/b1", "claimed", ", claim_expires_at", ", '" + ts + "'"},
		{"Zustand succeeded", "ccmas/asana.comment/b2", "succeeded", ", external_ref", ", 'g'"},
		{"Zustand needs_attention", "ccmas/asana.comment/b3", "needs_attention", "", ""},
		{"mit Versuch", "ccmas/asana.comment/b4", "pending", ", attempts", ", 1"},
		{"mit Abgleichversuch", "ccmas/asana.comment/b5", "pending", ", reconcile_attempts", ", 1"},
		{"mit Claim", "ccmas/asana.comment/b6", "pending", ", claim_expires_at", ", '" + ts + "'"},
		{"mit Referenz", "ccmas/asana.comment/b7", "pending", ", external_ref", ", 'g'"},
		{"mit Fehler", "ccmas/asana.comment/b8", "pending", ", last_error", ", 'x'"},
		{"Schlüssel ohne Projektbezug", "asana:comment:wo-1:1", "pending", "", ""},
		{"Schlüssel eines anderen Projekts", "other/asana.comment/x", "pending", "", ""},
		{"Schlüssel einer anderen Art", "ccmas/asana.update/x", "pending", "", ""},
		{"Schlüssel ohne stabile Teile", "ccmas/asana.comment/", "pending", "", ""},
		{"Schlüssel nur Präfix", "ccmas/asana.comment", "pending", "", ""},
	}
	for i, c := range bad {
		wantCode(t, obInsertSQL(t, s, "bad-"+itoa(int64(i)), c.key, c.state, c.cols, c.vals), CodeConstraint)
	}
	if n := obCount(t, s); n != 2 {
		t.Errorf("Zeilen: %d", n)
	}
	// Doppelter Schlüssel bleibt ein Konflikt (global eindeutig).
	wantCode(t, obInsertSQL(t, s, "dup", "ccmas/asana.comment/ok1", "pending", "", ""), CodeConflict)
	// Der Abgleichzähler ist nicht negativ.
	wantCode(t, mapErr(execSQL(t, s, "UPDATE outbox SET reconcile_attempts = -1 WHERE action_id = 'ok-1'")), CodeConstraint)
}

// obInState legt in einem frischen Store eine Aktion an und führt sie über die API in den gewünschten Zustand.
func obInState(t *testing.T, state domain.OutboxState) (*Store, domain.OutboxAction) {
	t.Helper()
	s := obStore(t)
	a := obAction(t)
	obEnqueue(t, s, a)
	if state == domain.OutboxPending {
		return s, a
	}
	if state == domain.OutboxCancelled {
		obDo(t, s, func(tx *Tx) (domain.OutboxAction, error) {
			return tx.CancelOutbox(projectID, a.ActionID, domain.OutboxPending, "weg")
		})
		return s, a
	}
	obClaim(t, s, obT0, time.Minute, 5)
	e := domain.OutboxError{Code: "x"}
	switch state {
	case domain.OutboxClaimed:
	case domain.OutboxRetryWait:
		obDo(t, s, func(tx *Tx) (domain.OutboxAction, error) {
			return tx.FailOutbox(projectID, a.ActionID, domain.OutboxRetryable, e, obT0.Add(time.Minute))
		})
	case domain.OutboxReconciling:
		obDo(t, s, func(tx *Tx) (domain.OutboxAction, error) {
			return tx.MarkOutboxUncertain(projectID, a.ActionID, e, time.Time{})
		})
	case domain.OutboxSucceeded:
		obDo(t, s, func(tx *Tx) (domain.OutboxAction, error) {
			return tx.CompleteOutbox(projectID, a.ActionID, domain.OutboxClaimed, "gid-1")
		})
	case domain.OutboxNeedsAttention:
		obDo(t, s, func(tx *Tx) (domain.OutboxAction, error) {
			return tx.FailOutbox(projectID, a.ActionID, domain.OutboxPermanent, e, time.Time{})
		})
	default:
		t.Fatalf("Zustand %s", state)
	}
	return s, a
}

// TestOutboxTransitionTriggers: Auch direktes SQL kann keinen unzulässigen Übergang, keinen falschen Zähler und keine
// Zeile in unstimmiger Form schreiben. Zulässige Übergänge gehen durch.
func TestOutboxTransitionTriggers(t *testing.T) {
	T := "'" + ts + "'"
	const (
		P, C, W, R, S, N, X = domain.OutboxPending, domain.OutboxClaimed, domain.OutboxRetryWait, domain.OutboxReconciling,
			domain.OutboxSucceeded, domain.OutboxNeedsAttention, domain.OutboxCancelled
	)
	cases := []struct {
		name string
		from domain.OutboxState
		set  string
		ok   bool
	}{
		// pending
		{"pending -> succeeded", P, "state='succeeded', external_ref='g'", false},
		{"pending -> reconciling", P, "state='reconciling'", false},
		{"pending -> retry_wait", P, "state='retry_wait', next_attempt_at=" + T, false},
		{"pending: Versuch ohne Übergang", P, "attempts=1", false},
		{"pending -> claimed ohne Versuch", P, "state='claimed', claim_expires_at=" + T, false},
		{"pending -> claimed mit zwei Versuchen", P, "state='claimed', attempts=2, claim_expires_at=" + T, false},
		{"pending -> claimed ohne Ablauf", P, "state='claimed', attempts=1", false},
		{"pending -> claimed", P, "state='claimed', attempts=1, claim_expires_at=" + T, true},
		{"pending -> cancelled", P, "state='cancelled'", true},
		// claimed
		{"claimed -> pending", C, "state='pending', claim_expires_at=NULL", false},
		{"claimed -> claimed", C, "state='claimed', attempts=2, claim_expires_at=" + T, false},
		{"claimed -> cancelled", C, "state='cancelled', claim_expires_at=NULL", false},
		{"claimed -> succeeded ohne Referenz", C, "state='succeeded', claim_expires_at=NULL", false},
		{"claimed -> succeeded mit leerer Referenz", C, "state='succeeded', claim_expires_at=NULL, external_ref=''", false},
		{"claimed -> succeeded mit stehendem Claim", C, "state='succeeded', external_ref='g'", false},
		{"claimed -> succeeded", C, "state='succeeded', claim_expires_at=NULL, external_ref='g'", true},
		{"claimed -> retry_wait ohne Zeitpunkt", C, "state='retry_wait', claim_expires_at=NULL", false},
		{"claimed -> retry_wait mit Versuch +1", C, "state='retry_wait', claim_expires_at=NULL, next_attempt_at=" + T + ", attempts=2", false},
		{"claimed -> retry_wait", C, "state='retry_wait', claim_expires_at=NULL, next_attempt_at=" + T, true},
		{"claimed -> reconciling mit stehendem Claim", C, "state='reconciling'", false},
		{"claimed -> reconciling", C, "state='reconciling', claim_expires_at=NULL", true},
		{"claimed -> needs_attention", C, "state='needs_attention', claim_expires_at=NULL", true},
		// retry_wait
		{"retry_wait -> pending", W, "state='pending', next_attempt_at=NULL", false},
		{"retry_wait -> claimed ohne Versuch", W, "state='claimed', next_attempt_at=NULL, claim_expires_at=" + T, false},
		{"retry_wait -> claimed", W, "state='claimed', attempts=2, next_attempt_at=NULL, claim_expires_at=" + T, true},
		{"retry_wait -> succeeded", W, "state='succeeded', next_attempt_at=NULL, external_ref='g'", false},
		{"retry_wait -> reconciling", W, "state='reconciling', next_attempt_at=NULL", false},
		{"retry_wait -> needs_attention", W, "state='needs_attention', next_attempt_at=NULL", true},
		{"retry_wait -> cancelled", W, "state='cancelled', next_attempt_at=NULL", true},
		// reconciling
		{"reconciling -> reconciling ohne Zähler", R, "last_error='x'", false},
		{"reconciling -> reconciling Zähler +2", R, "reconcile_attempts=2", false},
		{"reconciling -> reconciling", R, "reconcile_attempts=1", true},
		{"reconciling -> claimed", R, "state='claimed', attempts=2, claim_expires_at=" + T, false},
		{"reconciling -> retry_wait", R, "state='retry_wait', next_attempt_at=" + T, false},
		{"reconciling -> pending mit Zähler", R, "state='pending', reconcile_attempts=1", false},
		{"reconciling -> pending mit Versuch +1", R, "state='pending', attempts=2", false},
		{"reconciling -> pending", R, "state='pending'", true},
		{"reconciling -> succeeded ohne Referenz", R, "state='succeeded'", false},
		{"reconciling -> succeeded", R, "state='succeeded', external_ref='g'", true},
		{"reconciling -> needs_attention", R, "state='needs_attention'", true},
		{"reconciling -> cancelled", R, "state='cancelled'", false},
		// Endzustände
		{"succeeded -> pending", S, "state='pending', external_ref=NULL", false},
		{"succeeded -> cancelled", S, "state='cancelled', external_ref=NULL", false},
		{"succeeded: Referenz ändern", S, "external_ref='anders'", false},
		{"succeeded: Fehlertext ändern", S, "last_error='x'", false},
		{"cancelled -> pending", X, "state='pending'", false},
		{"cancelled -> claimed", X, "state='claimed', attempts=1, claim_expires_at=" + T, false},
		// Klärung
		{"needs_attention -> pending mit alten Versuchen", N, "state='pending'", false},
		{"needs_attention -> pending", N, "state='pending', attempts=0", true},
		{"needs_attention -> claimed", N, "state='claimed', attempts=2, claim_expires_at=" + T, false},
		{"needs_attention -> reconciling", N, "state='reconciling'", false},
		{"needs_attention -> succeeded ohne Referenz", N, "state='succeeded'", false},
		{"needs_attention -> succeeded", N, "state='succeeded', external_ref='g'", true},
		{"needs_attention -> cancelled", N, "state='cancelled'", true},
		// unveränderliche Spalten
		{"Art ändern", P, "kind='asana.update'", false},
		{"Schlüssel ändern", P, "operation_key='ccmas/asana.comment/anders'", false},
		{"Nutzdaten ändern", P, "payload_json='{\"x\":1}'", false},
		{"Ziel ändern", P, "target='anderes'", false},
		{"Aktions-ID ändern", P, "action_id='neu'", false},
		{"Erzeugungszeit ändern", P, "created_at='2026-01-01T00:00:00.000000000Z'", false},
	}
	for _, c := range cases {
		s, a := obInState(t, c.from)
		err := mapErr(execSQL(t, s, "UPDATE outbox SET "+c.set+" WHERE action_id = ?", a.ActionID))
		switch {
		case c.ok && err != nil:
			t.Errorf("%s: abgewiesen: %v", c.name, err)
		case !c.ok && err == nil:
			t.Errorf("%s: angenommen", c.name)
		case !c.ok && CodeOf(err) != CodeConstraint:
			t.Errorf("%s: Code %q (%v)", c.name, CodeOf(err), err)
		}
		if !c.ok {
			if cur := obGet(t, s, a.ActionID); cur.State != c.from || cur.Attempts != obGetAttempts(c.from) {
				t.Errorf("%s: Zeile verändert: %+v", c.name, cur)
			}
		}
		// Die Übergangstabelle der Datenbank stimmt mit der von Go überein, soweit der Test den Zielzustand nennt.
		if c.ok && strings.Contains(c.set, "state='") {
			to := domain.OutboxState(strings.SplitN(strings.SplitN(c.set, "state='", 2)[1], "'", 2)[0])
			if !c.from.CanTransition(to) {
				t.Errorf("%s: Datenbank erlaubt, was domain.CanTransition verbietet", c.name)
			}
		}
	}
}

// obGetAttempts liefert die Versuche, die eine mit obInState erzeugte Aktion im Zustand hat.
func obGetAttempts(state domain.OutboxState) int {
	if state == domain.OutboxPending || state == domain.OutboxCancelled {
		return 0
	}
	return 1
}

// TestOutboxIsAppendOnly: Aktionen der Outbox werden nicht gelöscht, auch nicht erledigte oder abgebrochene (Trigger der
// Migration 003). Das Journal belegt, welche externe Absicht bestand und wie sie endete.
func TestOutboxIsAppendOnly(t *testing.T) {
	s := obStore(t)
	done, open := obAction(t, "wo-1", "done"), obAction(t, "wo-1", "open")
	obEnqueue(t, s, done)
	obClaim(t, s, obT0, time.Minute, 1)
	obDo(t, s, func(tx *Tx) (domain.OutboxAction, error) {
		return tx.CompleteOutbox(projectID, done.ActionID, domain.OutboxClaimed, "gid-1")
	})
	obEnqueue(t, s, open)
	events := countEvents(t, s)

	for name, stmt := range map[string]string{
		"erledigte Aktion": "DELETE FROM outbox WHERE state = 'succeeded'",
		"offene Aktion":    "DELETE FROM outbox WHERE action_id = '" + open.ActionID + "'",
		"alle Aktionen":    "DELETE FROM outbox",
		"nach Projekt":     "DELETE FROM outbox WHERE project_id = 'ccmas'",
	} {
		err := execSQL(t, s, stmt)
		if err == nil {
			t.Errorf("%s: gelang", name)
			continue
		}
		wantCode(t, mapErr(err), CodeConstraint)
	}
	if obCount(t, s) != 2 || countEvents(t, s) != events {
		t.Error("das Journal hat sich verändert")
	}
	if got := obGet(t, s, done.ActionID); got.State != domain.OutboxSucceeded || got.ExternalRef != "gid-1" {
		t.Errorf("erledigte Aktion verändert: %+v", got)
	}
	// Fortschreiben über die Repositories bleibt möglich.
	obDo(t, s, func(tx *Tx) (domain.OutboxAction, error) {
		return tx.CancelOutbox(projectID, open.ActionID, domain.OutboxPending, "nicht mehr nötig")
	})
}
