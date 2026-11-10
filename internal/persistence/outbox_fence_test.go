package persistence

import (
	"strings"
	"testing"
	"time"

	"github.com/alexwenzel/ccmas/internal/domain"
)

// obSecondClaim führt eine Aktion durch einen ersten Claim (Versuch 1), dessen Ablauf, den Abgleich mit „nicht vorhanden“ und
// einen zweiten Claim (Versuch 2). Es liefert die Aktion im zweiten Claim.
func obSecondClaim(t *testing.T, s *Store, a domain.OutboxAction) domain.OutboxAction {
	t.Helper()
	if got := obClaim(t, s, obT0, time.Minute, 1); len(got) != 1 || got[0].Attempts != 1 {
		t.Fatalf("erster Claim: %+v", got)
	}
	mustUpdate(t, s, func(tx *Tx) error {
		_, err := tx.ExpireOutboxClaims(projectID, obT0.Add(time.Minute), 0)
		return err
	})
	obDo(t, s, func(tx *Tx) (domain.OutboxAction, error) { return tx.RequeueOutbox(projectID, a.ActionID) })
	got := obClaim(t, s, obT0.Add(2*time.Minute), time.Minute, 1)
	if len(got) != 1 || got[0].Attempts != 2 || got[0].State != domain.OutboxClaimed {
		t.Fatalf("zweiter Claim: %+v", got)
	}
	return got[0]
}

// Ein später Aufruf des ersten Claims darf den zweiten Claim derselben Aktion nicht abschließen, als fehlgeschlagen oder
// ungewiss markieren oder eskalieren (Fencing mit dem Versuchszähler). Jede abgewiesene Änderung ist ein conflict ohne
// Änderung und ohne Ereignis; mit dem eigenen Zähler gelingt sie.
func TestStaleClaimCannotChangeNewerClaim(t *testing.T) {
	s := obStore(t)
	a := obAction(t)
	obEnqueue(t, s, a)
	obSecondClaim(t, s, a)
	before := obGet(t, s, a.ActionID)
	eventsBefore := len(obEvents(t, s))

	stale := map[string]func(tx *Tx) error{
		"Complete": func(tx *Tx) error {
			_, err := tx.CompleteOutbox(projectID, a.ActionID, domain.OutboxClaimed, 1, "gid-alt")
			return err
		},
		"Fail wiederholbar": func(tx *Tx) error {
			_, err := tx.FailOutbox(projectID, a.ActionID, 1, domain.OutboxRetryable, domain.OutboxError{Code: "http_503"}, obT0.Add(time.Hour))
			return err
		},
		"Fail dauerhaft": func(tx *Tx) error {
			_, err := tx.FailOutbox(projectID, a.ActionID, 1, domain.OutboxPermanent, domain.OutboxError{Code: "http_403"}, time.Time{})
			return err
		},
		"Uncertain": func(tx *Tx) error {
			_, err := tx.MarkOutboxUncertain(projectID, a.ActionID, 1, domain.OutboxError{Code: "timeout"}, time.Time{})
			return err
		},
		"Escalate": func(tx *Tx) error {
			_, err := tx.EscalateOutbox(projectID, a.ActionID, domain.OutboxClaimed, 1, domain.OutboxError{Code: "retries_exhausted"})
			return err
		},
	}
	for name, fn := range stale {
		_, err := s.Update(ctxT(t), "tester", func(tx *Tx) error { return fn(tx) })
		wantCode(t, err, CodeConflict)
		if err != nil && !strings.Contains(err.Error(), "Versuch") {
			t.Errorf("%s: %v", name, err)
		}
		if got := obGet(t, s, a.ActionID); got.State != before.State || got.Attempts != before.Attempts || !got.UpdatedAt.Equal(before.UpdatedAt) ||
			!got.ClaimExpiresAt.Equal(before.ClaimExpiresAt) || got.LastError != before.LastError || got.ExternalRef != "" {
			t.Errorf("%s hat den zweiten Claim verändert: %+v", name, got)
		}
	}
	if n := len(obEvents(t, s)); n != eventsBefore {
		t.Errorf("abgewiesene Änderungen schrieben Ereignisse: %d -> %d", eventsBefore, n)
	}

	// Der Inhaber des zweiten Claims (Zähler 2) kommt durch.
	done := obDo(t, s, func(tx *Tx) (domain.OutboxAction, error) {
		return tx.CompleteOutbox(projectID, a.ActionID, domain.OutboxClaimed, 2, "gid-neu")
	})
	if done.State != domain.OutboxSucceeded || done.ExternalRef != "gid-neu" {
		t.Errorf("Complete mit eigenem Zähler: %+v", done)
	}
}

// Die Übernahme eines späten Erfolgs aus reconciling gilt nur, solange seitdem kein neuer Claim begann.
func TestLateSuccessFromReconcilingNeedsSameClaim(t *testing.T) {
	s := obStore(t)
	a := obAction(t)
	obEnqueue(t, s, a)
	obClaim(t, s, obT0, time.Minute, 1)
	mustUpdate(t, s, func(tx *Tx) error {
		_, err := tx.ExpireOutboxClaims(projectID, obT0.Add(time.Minute), 0)
		return err
	})
	// Derselbe Claim (Zähler 1) im Abgleich: Der späte Erfolg wird übernommen.
	done := obDo(t, s, func(tx *Tx) (domain.OutboxAction, error) {
		return tx.CompleteOutbox(projectID, a.ActionID, domain.OutboxReconciling, 1, "gid-spaet")
	})
	if done.State != domain.OutboxSucceeded {
		t.Fatalf("später Erfolg: %+v", done)
	}

	// Mit einem neuen Claim (Zähler 2) und erneutem Abgleich ist der Zähler 1 veraltet.
	s2 := obStore(t)
	obEnqueue(t, s2, a)
	obSecondClaim(t, s2, a)
	obDo(t, s2, func(tx *Tx) (domain.OutboxAction, error) {
		return tx.MarkOutboxUncertain(projectID, a.ActionID, 2, domain.OutboxError{Code: "timeout"}, time.Time{})
	})
	_, err := s2.Update(ctxT(t), "tester", func(tx *Tx) error {
		_, err := tx.CompleteOutbox(projectID, a.ActionID, domain.OutboxReconciling, 1, "gid-alt")
		return err
	})
	wantCode(t, err, CodeConflict)
	if got := obGet(t, s2, a.ActionID); got.State != domain.OutboxReconciling || got.ExternalRef != "" {
		t.Errorf("veralteter Erfolg übernommen: %+v", got)
	}
}

func TestFenceValueIsRequired(t *testing.T) {
	s := obStore(t)
	a := obAction(t)
	obEnqueue(t, s, a)
	obClaim(t, s, obT0, time.Minute, 1)
	for name, fn := range map[string]func(tx *Tx) error{
		"Complete": func(tx *Tx) error {
			_, err := tx.CompleteOutbox(projectID, a.ActionID, domain.OutboxClaimed, 0, "gid")
			return err
		},
		"Fail": func(tx *Tx) error {
			_, err := tx.FailOutbox(projectID, a.ActionID, 0, domain.OutboxPermanent, domain.OutboxError{Code: "x"}, time.Time{})
			return err
		},
		"Uncertain": func(tx *Tx) error {
			_, err := tx.MarkOutboxUncertain(projectID, a.ActionID, -1, domain.OutboxError{Code: "x"}, time.Time{})
			return err
		},
		"Escalate": func(tx *Tx) error {
			_, err := tx.EscalateOutbox(projectID, a.ActionID, domain.OutboxClaimed, 0, domain.OutboxError{Code: "x"})
			return err
		},
	} {
		_, err := s.Update(ctxT(t), "tester", fn)
		if CodeOf(err) != CodeInvalid {
			t.Errorf("%s ohne Fencing-Wert: %v", name, err)
		}
	}
}

// Ein abgelaufener Claim macht den ersten Abgleich frühestens lookupDelay nach dem Ablauf fällig.
func TestExpiredClaimLookupDelay(t *testing.T) {
	s := obStore(t)
	a := obAction(t)
	obEnqueue(t, s, a)
	obClaim(t, s, obT0, time.Minute, 1) // läuft ab um obT0+1min
	mustUpdate(t, s, func(tx *Tx) error {
		_, err := tx.ExpireOutboxClaims(projectID, obT0.Add(90*time.Second), 45*time.Second)
		return err
	})
	cur := obGet(t, s, a.ActionID)
	if want := obT0.Add(time.Minute + 45*time.Second); cur.State != domain.OutboxReconciling || !cur.NextAttemptAt.Equal(want) {
		t.Fatalf("nach Ablauf: %+v, erwartet Abgleich um %s", cur, want)
	}
	due := func(at time.Time) int {
		var n int
		mustView(t, s, func(r *ReadTx) error {
			d, err := r.DueReconcileOutbox(projectID, at, 10)
			n = len(d)
			return err
		})
		return n
	}
	if due(obT0.Add(time.Minute+44*time.Second)) != 0 || due(obT0.Add(time.Minute+45*time.Second)) != 1 {
		t.Errorf("Fälligkeit des ersten Abgleichs stimmt nicht")
	}
	_, err := s.Update(ctxT(t), "tester", func(tx *Tx) error {
		_, err := tx.ExpireOutboxClaims(projectID, obT0, -time.Second)
		return err
	})
	wantCode(t, err, CodeInvalid)
}

// Der Grund von Abbruch und Klärung ist Freitext: Er wird vor dem Speichern und im Audit-Ereignis maskiert.
func TestReasonIsRedactedBeforeStoring(t *testing.T) {
	const secret = "erfundenerWert12345"
	s := obStore(t)
	cancelled, escalated := obAction(t, "wo-1", "cancel"), obAction(t, "wo-1", "resolve")
	obEnqueue(t, s, cancelled)
	obEnqueue(t, s, escalated)
	obDo(t, s, func(tx *Tx) (domain.OutboxAction, error) {
		return tx.CancelOutbox(projectID, cancelled.ActionID, domain.OutboxPending, "Ticket weg, token="+secret+" nicht mehr nötig")
	})
	obClaim(t, s, obT0, time.Minute, 5)
	obDo(t, s, func(tx *Tx) (domain.OutboxAction, error) {
		return tx.FailOutbox(projectID, escalated.ActionID, 1, domain.OutboxPermanent, domain.OutboxError{Code: "http_403"}, time.Time{})
	})
	obDo(t, s, func(tx *Tx) (domain.OutboxAction, error) {
		return tx.ResolveOutbox(projectID, escalated.ActionID, domain.OutboxResolution{Action: domain.ResolveCancel, Reason: "Zugang password=" + secret + " geprüft"})
	})
	seen := 0
	for _, e := range obEvents(t, s) {
		if e.Kind != domain.AuditOutboxCancelled && e.Kind != domain.AuditOutboxResolved {
			continue
		}
		seen++
		reason := e.Details["reason"]
		if strings.Contains(reason, secret) || !strings.Contains(reason, "[redigiert]") {
			t.Errorf("%s: Grund nicht maskiert: %q", e.Kind, reason)
		}
	}
	if seen != 2 {
		t.Errorf("%d Ereignisse mit Grund", seen)
	}
}
