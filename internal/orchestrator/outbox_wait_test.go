package orchestrator

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alexwenzel/ccmas/internal/domain"
	"github.com/alexwenzel/ccmas/internal/persistence"
)

// ---- Mindestwartezeit vor dem ersten Abgleich (Befund F-B1) -------------------------------------------------------

// Nach einem Timeout kann der Server den Schreibvorgang noch abschließen. Vor Ablauf der Mindestwartezeit gibt es keinen
// Abgleich und damit kein zweites Execute; die Aktion bleibt reconciling. Findet der Abgleich danach das Objekt, bleibt es bei
// einem Execute.
func TestLateServerWriteAfterTimeoutIsNotDuplicated(t *testing.T) {
	env := newOutboxEnv(t)
	a := env.action("wo-1", "result")
	env.enqueue(a)
	env.fake.onExecute = func(a domain.OutboxAction, call int) (bool, ExecuteResult) {
		// Der Client gibt auf, der Server schreibt erst später (siehe unten).
		return false, ExecuteResult{Outcome: domain.OutboxUncertain, Err: domain.OutboxError{Code: "timeout"}}
	}
	env.run()
	env.wantState(a, domain.OutboxReconciling)

	// Der Server schließt den Schreibvorgang 20 s nach dem Ende des Aufrufs ab, vor Ablauf der Mindestwartezeit.
	env.clock.advance(20 * time.Second)
	env.fake.mu.Lock()
	env.fake.remote[a.Marker()] = outboxRef(a)
	env.fake.mu.Unlock()
	env.clock.advance(env.policy.MinReconcileWait - 20*time.Second - time.Second)
	env.run()
	env.wantState(a, domain.OutboxReconciling)
	if env.fake.executed(a) != 1 || env.fake.looked(a) != 0 {
		t.Fatalf("vor Ablauf der Wartezeit: Execute %d, Lookup %d", env.fake.executed(a), env.fake.looked(a))
	}

	env.clock.advance(time.Second)
	env.run()
	if got := env.wantState(a, domain.OutboxSucceeded); got.ExternalRef != outboxRef(a) {
		t.Errorf("Referenz %q", got.ExternalRef)
	}
	if env.fake.executed(a) != 1 || env.fake.looked(a) != 1 {
		t.Errorf("Execute %d, Lookup %d: Duplikat nach Timeout", env.fake.executed(a), env.fake.looked(a))
	}
}

// Ein „nicht vorhanden“ führt erst nach der Mindestwartezeit nach pending und zu einem erneuten Versuch.
func TestAbsentLeadsToRetryOnlyAfterWait(t *testing.T) {
	env := newOutboxEnv(t)
	a := env.action("wo-1", "result")
	env.enqueue(a)
	env.fake.onExecute = func(a domain.OutboxAction, call int) (bool, ExecuteResult) {
		if call == 1 {
			return false, ExecuteResult{Outcome: domain.OutboxUncertain, Err: domain.OutboxError{Code: "timeout"}}
		}
		return true, outboxOK(a)
	}
	env.run()
	env.wantState(a, domain.OutboxReconciling)

	env.clock.advance(env.policy.MinReconcileWait - time.Second)
	env.run()
	env.wantState(a, domain.OutboxReconciling)
	if env.fake.executed(a) != 1 || env.fake.looked(a) != 0 {
		t.Fatalf("eine Sekunde vor Ablauf: Execute %d, Lookup %d", env.fake.executed(a), env.fake.looked(a))
	}

	env.clock.advance(time.Second)
	env.run()
	got := env.wantState(a, domain.OutboxSucceeded)
	if env.fake.looked(a) != 1 || env.fake.executed(a) != 2 || got.Attempts != 2 {
		t.Errorf("Lookup %d, Execute %d, Versuche %d: erwartet ein Abgleich und ein erneuter Versuch", env.fake.looked(a), env.fake.executed(a), got.Attempts)
	}
}

// Ein abgelaufener Claim wartet ab dem Ablauf des Claims, nicht ab dem Zeitpunkt, zu dem ein Prozess ihn bemerkt.
func TestExpiredClaimWaitsFromExpiry(t *testing.T) {
	env := newOutboxEnv(t)
	other := env.newOutbox(env.store)
	a := env.action("wo-1", "result")
	env.enqueue(a)
	first := true
	env.fake.onExecute = func(a domain.OutboxAction, call int) (bool, ExecuteResult) {
		if first {
			first = false
			claimedAt := env.clock.now()
			// Der Claim läuft ab; ein anderer Prozess bemerkt es erst 10 s später.
			env.clock.advance(env.policy.ClaimTTL + 10*time.Second)
			if _, err := other.RunOnce(msgCtx(t), msgProject); err != nil {
				t.Errorf("RunOnce (anderer Prozess): %v", err)
			}
			cur := env.get(a)
			if want := claimedAt.Add(env.policy.ClaimTTL + env.policy.MinReconcileWait); cur.State != domain.OutboxReconciling || !cur.NextAttemptAt.Equal(want) {
				t.Errorf("nach Ablauf: Zustand %s, erster Abgleich um %s, erwartet %s", cur.State, cur.NextAttemptAt, want)
			}
			if env.fake.looked(a) != 0 {
				t.Errorf("Abgleich %d-mal vor Ablauf der Wartezeit", env.fake.looked(a))
			}
		}
		return true, outboxOK(a)
	}
	env.run()
	env.wantState(a, domain.OutboxSucceeded) // das späte Ergebnis wird aus reconciling übernommen (derselbe Claim)
	if env.fake.executed(a) != 1 {
		t.Errorf("Execute %d", env.fake.executed(a))
	}
}

func TestMinReconcileWaitPolicy(t *testing.T) {
	clock := &outboxClock{t: time.Unix(0, 0)}
	s := outboxOpen(t, t.TempDir()+"/state/ccmas.db", clock)
	if err := s.Migrate(msgCtx(t)); err != nil {
		t.Fatal(err)
	}
	build := func(mod func(p *OutboxPolicy)) (OutboxPolicy, error) {
		p := OutboxPolicy{}
		mod(&p)
		ob, err := NewOutbox(s, "orchestrator", nil, p)
		if err != nil {
			return p, err
		}
		return ob.policy, nil
	}
	if d := DefaultOutboxPolicy(); d.MinReconcileWait != d.ExecuteTimeout+reconcileWaitBuffer || d.MinReconcileWait != 45*time.Second {
		t.Errorf("Standard %v", d.MinReconcileWait)
	}
	if p, err := build(func(*OutboxPolicy) {}); err != nil || p.MinReconcileWait != 45*time.Second {
		t.Errorf("leere Richtlinie: %v %v", p.MinReconcileWait, err)
	}
	// Der Standard folgt einem geänderten ExecuteTimeout.
	if p, err := build(func(p *OutboxPolicy) { p.ExecuteTimeout, p.ClaimTTL = time.Minute, 5*time.Minute }); err != nil || p.MinReconcileWait != time.Minute+reconcileWaitBuffer {
		t.Errorf("abgeleiteter Standard: %v %v", p.MinReconcileWait, err)
	}
	if p, err := build(func(p *OutboxPolicy) { p.MinReconcileWait = 90 * time.Second }); err != nil || p.MinReconcileWait != 90*time.Second {
		t.Errorf("gesetzter Wert: %v %v", p.MinReconcileWait, err)
	}
	if p, err := build(func(p *OutboxPolicy) { p.MinReconcileWait = p.ExecuteTimeout }); err != nil {
		t.Errorf("gleich ExecuteTimeout ist erlaubt: %v %v", p.MinReconcileWait, err)
	}
	if _, err := build(func(p *OutboxPolicy) { p.ExecuteTimeout, p.MinReconcileWait = 20*time.Second, 10*time.Second }); err == nil {
		t.Error("Wartezeit unter ExecuteTimeout angenommen")
	}
	if _, err := build(func(p *OutboxPolicy) { p.MinReconcileWait = -time.Second }); err == nil {
		t.Error("negative Wartezeit angenommen")
	}
}

// ---- Fencing des Claims (Befund F-B2) ----------------------------------------------------------------------------

// Ein Aufruf überlebt die ClaimTTL. Danach läuft der Abgleich mit „nicht vorhanden“ nach der Wartezeit, ein neuer Claim
// beginnt. Das späte Ergebnis des ersten Aufrufs darf den zweiten Claim weder abschließen noch als fehlgeschlagen oder
// ungewiss markieren noch eskalieren.
func TestStaleExecuteResultCannotChangeSecondClaim(t *testing.T) {
	late := map[string]ExecuteResult{
		"Erfolg":       {Outcome: domain.OutboxSuccess, ExternalRef: "ext-spaet"},
		"wiederholbar": {Outcome: domain.OutboxRetryable, Err: domain.OutboxError{Code: "http_503"}},
		"dauerhaft":    {Outcome: domain.OutboxPermanent, Err: domain.OutboxError{Code: "http_403"}},
		"ungewiss":     {Outcome: domain.OutboxUncertain, Err: domain.OutboxError{Code: "timeout"}},
	}
	for name, lateResult := range late {
		t.Run(name, func(t *testing.T) {
			env := newOutboxEnv(t)
			other := env.newOutbox(env.store)
			a := env.action("wo-1", "result")
			env.enqueue(a)

			started1, done1 := make(chan struct{}), make(chan struct{})
			release1, release2 := make(chan struct{}), make(chan struct{})
			secondClaimed := make(chan struct{})
			env.fake.onExecute = func(a domain.OutboxAction, call int) (bool, ExecuteResult) {
				if call == 1 { // der hängende erste Aufruf
					close(started1)
					<-release1
					return false, lateResult
				}
				close(secondClaimed) // der zweite Claim ist im Gang
				<-release2
				return true, outboxOK(a)
			}

			var (
				wg                    sync.WaitGroup
				firstRun, secondRun   OutboxRun
				firstErr, secondError error
			)
			wg.Add(2)
			go func() {
				defer wg.Done()
				defer close(done1)
				firstRun, firstErr = env.ob.RunOnce(msgCtx(t), msgProject)
			}()
			<-started1 // erster Claim: Zustand claimed, Versuch 1

			// Der Claim läuft ab; ein anderer Prozess nimmt die Aktion in den Abgleich, wartet die Mindestzeit ab, findet nichts
			// („nicht vorhanden“), reiht neu ein und beginnt den zweiten Claim.
			env.clock.advance(env.policy.ClaimTTL)
			if _, err := other.RunOnce(msgCtx(t), msgProject); err != nil {
				t.Fatalf("RunOnce (Ablauf): %v", err)
			}
			env.wantState(a, domain.OutboxReconciling)
			env.clock.advance(env.policy.MinReconcileWait)
			go func() {
				defer wg.Done()
				secondRun, secondError = other.RunOnce(msgCtx(t), msgProject)
			}()
			<-secondClaimed
			if got := env.wantState(a, domain.OutboxClaimed); got.Attempts != 2 {
				t.Fatalf("zweiter Claim: %+v", got)
			}

			// Das späte Ergebnis des ersten Aufrufs trifft ein, während der zweite Claim läuft.
			close(release1)
			<-done1
			got := env.wantState(a, domain.OutboxClaimed)
			if got.Attempts != 2 || got.ExternalRef != "" || strings.Contains(got.LastError, "http_") || strings.Contains(got.LastError, "timeout") {
				t.Errorf("der zweite Claim wurde verändert: %+v", got)
			}

			close(release2)
			wg.Wait()
			if firstErr != nil || secondError != nil {
				t.Errorf("RunOnce: %v / %v", firstErr, secondError)
			}
			if len(firstRun.Steps) != 1 || persistence.CodeOf(firstRun.Steps[0].Err) != persistence.CodeConflict {
				t.Errorf("Schritt des ersten Aufrufs: %+v, erwartet conflict", firstRun.Steps)
			}
			if len(secondRun.Steps) == 0 {
				t.Fatal("zweiter Lauf ohne Schritte")
			}
			final := env.wantState(a, domain.OutboxSucceeded)
			if final.Attempts != 2 || final.ExternalRef != outboxRef(a) {
				t.Errorf("Ergebnis des zweiten Claims: %+v", final)
			}
		})
	}
}

// ---- Unterbrochene Aufrufe (H-3) und Freitext (H-5) -----------------------------------------------------------------

// ctxAdapter lässt den Test den Kontext des Aufrufs steuern. Er gehört nur in Tests.
type ctxAdapter struct {
	execute func(ctx context.Context, a domain.OutboxAction) ExecuteResult
	calls   int
}

func (c *ctxAdapter) Execute(ctx context.Context, a domain.OutboxAction) ExecuteResult {
	c.calls++
	return c.execute(ctx, a)
}

func (c *ctxAdapter) Lookup(context.Context, domain.OutboxAction) LookupResult {
	return LookupResult{Status: LookupUnknown}
}

// Meldet der Adapter „wiederholbar“, obwohl die Zeitgrenze oder ein Abbruch den Aufruf beendet hat, ist der Ausgang ungewiss:
// Der Server kann noch schreiben. Die Aktion geht in den Abgleich, nicht in die Wiederholung.
func TestRetryableAfterInterruptedCallIsUncertain(t *testing.T) {
	for name, retryable := range map[string]bool{"Abbruch während des Aufrufs": true, "kein Abbruch": false} {
		t.Run(name, func(t *testing.T) {
			env := newOutboxEnv(t)
			ad := &ctxAdapter{}
			cancelCtx, cancel := context.WithCancel(msgCtx(t))
			defer cancel()
			ad.execute = func(ctx context.Context, a domain.OutboxAction) ExecuteResult {
				if retryable {
					cancel() // der Aufruf wird abgebrochen; der Adapter hält das für einen wiederholbaren Fehler
				}
				return ExecuteResult{Outcome: domain.OutboxRetryable, Err: domain.OutboxError{Code: "http_503", Message: "nicht erreichbar"}}
			}
			ob, err := NewOutbox(env.store, "orchestrator", map[string]ExternalAdapter{"asana": ad}, env.policy)
			if err != nil {
				t.Fatal(err)
			}
			a := env.action("wo-1", "result")
			env.enqueue(a)
			_, _ = ob.RunOnce(cancelCtx, msgProject) // bei Abbruch endet der Durchlauf mit dem Fehler des Kontexts
			got := env.get(a)
			if ad.calls != 1 {
				t.Fatalf("Execute %d-mal", ad.calls)
			}
			if retryable {
				if got.State != domain.OutboxReconciling || !strings.HasPrefix(got.LastError, "call_interrupted") || strings.Contains(got.LastError, "retry") {
					t.Errorf("nach unterbrochenem Aufruf: %+v", got)
				}
				if !got.NextAttemptAt.Equal(env.clock.now().Add(env.policy.MinReconcileWait)) {
					t.Errorf("erster Abgleich um %s", got.NextAttemptAt)
				}
				if slices.Contains(env.kinds(), domain.AuditOutboxRetryScheduled) {
					t.Error("eine Wiederholung wurde eingeplant")
				}
			} else if got.State != domain.OutboxRetryWait {
				// Ohne Unterbrechung bleibt ein wiederholbarer Fehler wiederholbar.
				t.Errorf("wiederholbarer Fehler ohne Abbruch: %+v", got)
			}
		})
	}
}

// Der Freitext einer Klärung wird maskiert gespeichert (Befund H-5), im Audit und in der Antwort.
func TestResolveReasonIsRedacted(t *testing.T) {
	const secret = "erfundenerWert12345"
	env := newOutboxEnv(t)
	a := env.action("wo-1", "result")
	env.enqueue(a)
	env.fake.onExecute = func(domain.OutboxAction, int) (bool, ExecuteResult) {
		return false, ExecuteResult{Outcome: domain.OutboxPermanent, Err: domain.OutboxError{Code: "http_403"}}
	}
	env.run()
	env.wantState(a, domain.OutboxNeedsAttention)
	if _, err := env.ob.Resolve(msgCtx(t), msgProject, a.ActionID, "alex",
		domain.OutboxResolution{Action: domain.ResolveCancel, Reason: "Zugang geprüft, password=" + secret + " war falsch"}); err != nil {
		t.Fatal(err)
	}
	found := false
	err := env.store.View(msgCtx(t), func(r *persistence.ReadTx) error {
		evs, err := r.Events(msgProject, persistence.EventFilter{Level: domain.AuditLevelOutbox})
		for _, e := range evs {
			if e.Kind == domain.AuditOutboxResolved {
				found = true
				if reason := e.Details["reason"]; strings.Contains(reason, secret) || !strings.Contains(reason, "[redigiert]") {
					t.Errorf("Grund im Audit: %q", reason)
				}
			}
		}
		return err
	})
	if err != nil || !found {
		t.Fatalf("Ereignis outbox.resolved: %v %v", found, err)
	}
}
