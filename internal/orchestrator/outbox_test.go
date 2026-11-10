package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/alexwenzel/ccmas/internal/domain"
	"github.com/alexwenzel/ccmas/internal/persistence"
	"github.com/alexwenzel/ccmas/internal/persistence/persistencetest"
)

// ---- Testumgebung ------------------------------------------------------------------------------------------

// outboxClock ist eine von Hand gestellte Uhr (nebenläufig nutzbar). Kein Test wartet wirklich.
type outboxClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *outboxClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *outboxClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// outboxFake ist das „externe System“ samt Adapter: Es merkt sich, welche Objekte (Marker) es angelegt hat, und zählt
// die Aufrufe je operation_key. Es gehört nur in Tests.
type outboxFake struct {
	t        *testing.T
	mu       sync.Mutex
	remote   map[string]string // Marker -> externe Referenz
	executes map[string]int
	lookups  map[string]int
	// onExecute entscheidet je Aufruf (call zählt ab 1), ob das Objekt wirklich angelegt wird und was der Adapter meldet.
	onExecute func(a domain.OutboxAction, call int) (create bool, res ExecuteResult)
	// onLookup überschreibt die Suche; ok=false: Standardverhalten (Marker im externen System suchen).
	onLookup func(a domain.OutboxAction, call int) (res LookupResult, ok bool)
}

func newOutboxFake(t *testing.T) *outboxFake {
	return &outboxFake{t: t, remote: map[string]string{}, executes: map[string]int{}, lookups: map[string]int{}}
}

// outboxOK ist das Ergebnis eines gelungenen Aufrufs samt Referenz.
func outboxOK(a domain.OutboxAction) ExecuteResult {
	return ExecuteResult{Outcome: domain.OutboxSuccess, ExternalRef: outboxRef(a)}
}

func outboxRef(a domain.OutboxAction) string {
	return "ext-" + strings.TrimPrefix(a.ActionID, "op-")[:8]
}

func (f *outboxFake) Execute(_ context.Context, a domain.OutboxAction) ExecuteResult {
	f.mu.Lock()
	f.executes[a.OperationKey]++
	call := f.executes[a.OperationKey]
	hook := f.onExecute
	f.mu.Unlock()
	if a.State != domain.OutboxClaimed {
		f.t.Errorf("Execute für %s im Zustand %s: nur beanspruchte Aktionen dürfen ausgeführt werden", a.OperationKey, a.State)
	}
	create, res := true, outboxOK(a)
	if hook != nil {
		create, res = hook(a, call)
	}
	if create {
		f.mu.Lock()
		f.remote[a.Marker()] = outboxRef(a)
		f.mu.Unlock()
	}
	return res
}

func (f *outboxFake) Lookup(_ context.Context, a domain.OutboxAction) LookupResult {
	f.mu.Lock()
	f.lookups[a.OperationKey]++
	call := f.lookups[a.OperationKey]
	hook := f.onLookup
	ref, found := f.remote[a.Marker()]
	f.mu.Unlock()
	if hook != nil {
		if res, ok := hook(a, call); ok {
			return res
		}
	}
	if found {
		return LookupResult{Status: LookupFound, ExternalRef: ref}
	}
	return LookupResult{Status: LookupAbsent}
}

func (f *outboxFake) executed(a domain.OutboxAction) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.executes[a.OperationKey]
}

func (f *outboxFake) looked(a domain.OutboxAction) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lookups[a.OperationKey]
}

func (f *outboxFake) totalExecutes() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.executes {
		n += c
	}
	return n
}

type outboxEnv struct {
	t      *testing.T
	store  *persistence.Store
	clock  *outboxClock
	fake   *outboxFake
	policy OutboxPolicy
	ob     *Outbox
}

func outboxPolicy(c *outboxClock) OutboxPolicy {
	return OutboxPolicy{
		ClaimTTL: 2 * time.Minute, ExecuteTimeout: 30 * time.Second, LookupTimeout: 30 * time.Second,
		MaxAttempts: 4, MaxReconcileAttempts: 3, BatchSize: 20,
		BackoffInitial: 10 * time.Second, BackoffMax: time.Minute, PollInterval: time.Minute,
		Now: c.now,
	}
}

func outboxOpen(t *testing.T, path string, c *outboxClock) *persistence.Store {
	t.Helper()
	s, err := persistence.Open(msgCtx(t), path, persistence.WithClock(c.now))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func newOutboxEnv(t *testing.T, mod ...func(*OutboxPolicy)) *outboxEnv {
	t.Helper()
	clock := &outboxClock{t: time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)}
	s := outboxOpen(t, filepath.Join(t.TempDir(), "state", "ccmas.db"), clock)
	if err := s.Migrate(msgCtx(t)); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	persistencetest.Seed(t, s)
	env := &outboxEnv{t: t, store: s, clock: clock, fake: newOutboxFake(t), policy: outboxPolicy(clock)}
	for _, m := range mod {
		m(&env.policy)
	}
	env.ob = env.newOutbox(s)
	return env
}

func (e *outboxEnv) newOutbox(s *persistence.Store) *Outbox {
	e.t.Helper()
	ob, err := NewOutbox(s, "orchestrator", map[string]ExternalAdapter{"asana": e.fake}, e.policy)
	if err != nil {
		e.t.Fatalf("NewOutbox: %v", err)
	}
	return ob
}

// restart schließt den Store (der Prozess stirbt) und öffnet dieselbe Datei neu.
func (e *outboxEnv) restart() {
	e.t.Helper()
	path := e.store.Path()
	_ = e.store.Close()
	e.store = outboxOpen(e.t, path, e.clock)
	e.ob = e.newOutbox(e.store)
}

func (e *outboxEnv) action(parts ...string) domain.OutboxAction {
	e.t.Helper()
	a, err := domain.NewOutboxAction(msgProject, "asana.comment", "task-1", []byte(`{"text":"fertig"}`), parts...)
	if err != nil {
		e.t.Fatal(err)
	}
	return a
}

func (e *outboxEnv) enqueue(as ...domain.OutboxAction) {
	e.t.Helper()
	if _, err := e.ob.EnqueueBatch(msgCtx(e.t), as); err != nil {
		e.t.Fatalf("EnqueueBatch: %v", err)
	}
}

func (e *outboxEnv) run() OutboxRun {
	e.t.Helper()
	r, err := e.ob.RunOnce(msgCtx(e.t), msgProject)
	if err != nil {
		e.t.Fatalf("RunOnce: %v", err)
	}
	for _, st := range r.Steps {
		if st.Err != nil {
			e.t.Errorf("Schritt %s %s: %v", st.Kind, st.ActionID, st.Err)
		}
	}
	return r
}

func (e *outboxEnv) get(a domain.OutboxAction) domain.OutboxAction {
	e.t.Helper()
	var got domain.OutboxAction
	err := e.store.View(msgCtx(e.t), func(r *persistence.ReadTx) error {
		var err error
		got, err = r.OutboxAction(msgProject, a.ActionID)
		return err
	})
	if err != nil {
		e.t.Fatalf("OutboxAction: %v", err)
	}
	return got
}

func (e *outboxEnv) kinds() []string {
	e.t.Helper()
	var out []string
	err := e.store.View(msgCtx(e.t), func(r *persistence.ReadTx) error {
		evs, err := r.Events(msgProject, persistence.EventFilter{Level: domain.AuditLevelOutbox})
		for _, ev := range evs {
			out = append(out, ev.Kind)
		}
		return err
	})
	if err != nil {
		e.t.Fatal(err)
	}
	return out
}

func (e *outboxEnv) wantState(a domain.OutboxAction, want domain.OutboxState) domain.OutboxAction {
	e.t.Helper()
	got := e.get(a)
	if got.State != want {
		e.t.Fatalf("%s: Zustand %s (Fehler %q), erwartet %s", a.OperationKey, got.State, got.LastError, want)
	}
	return got
}

// ---- Die Tests des Tickets ------------------------------------------------------------------------------

// TestCrashAfterRemoteSuccess: Der Adapter hat das Objekt mit Marker angelegt, dann stirbt der Prozess vor Complete.
// Nach Neustart und Ablauf des Claims findet der Abgleich den Marker: succeeded mit der Referenz, ohne zweites Execute.
func TestCrashAfterRemoteSuccess(t *testing.T) {
	env := newOutboxEnv(t)
	a := env.action("wo-1", "result")
	env.enqueue(a)
	env.fake.onExecute = func(a domain.OutboxAction, call int) (bool, ExecuteResult) {
		// Das externe Objekt entsteht (create=true); der Prozess stirbt, bevor sein Ergebnis gespeichert wird.
		_ = env.store.Close()
		return true, ExecuteResult{Outcome: domain.OutboxSuccess, ExternalRef: outboxRef(a)}
	}
	if _, err := env.ob.RunOnce(msgCtx(t), msgProject); err == nil {
		t.Log("RunOnce meldete trotz geschlossenem Store keinen Fehler (der Schritt trägt ihn)")
	}
	if env.fake.executed(a) != 1 || len(env.fake.remote) != 1 {
		t.Fatalf("Execute %d, externe Objekte %d", env.fake.executed(a), len(env.fake.remote))
	}

	env.restart()
	env.fake.onExecute = nil
	// Der Claim ist als Beleg des laufenden Aufrufs noch gültig: nichts geschieht, vor allem kein zweites Execute.
	env.wantState(a, domain.OutboxClaimed)
	env.run()
	env.wantState(a, domain.OutboxClaimed)
	if env.fake.executed(a) != 1 || env.fake.looked(a) != 0 {
		t.Fatalf("vor Ablauf des Claims: Execute %d, Lookup %d", env.fake.executed(a), env.fake.looked(a))
	}

	env.clock.advance(env.policy.ClaimTTL)
	r := env.run()
	got := env.wantState(a, domain.OutboxSucceeded)
	if got.ExternalRef != outboxRef(a) {
		t.Errorf("Referenz %q, erwartet %q", got.ExternalRef, outboxRef(a))
	}
	if env.fake.executed(a) != 1 {
		t.Errorf("Execute wurde %d-mal aufgerufen: blindes zweites Create", env.fake.executed(a))
	}
	if env.fake.looked(a) != 1 || len(env.fake.remote) != 1 {
		t.Errorf("Lookup %d, externe Objekte %d", env.fake.looked(a), len(env.fake.remote))
	}
	kinds := env.kinds()
	for _, want := range []string{domain.AuditOutboxEnqueued, domain.AuditOutboxClaimed, domain.AuditOutboxClaimExpired, domain.AuditOutboxSucceeded} {
		if !slices.Contains(kinds, want) {
			t.Errorf("Ereignis %s fehlt in %v", want, kinds)
		}
	}
	if slices.Contains(kinds, domain.AuditOutboxRequeued) {
		t.Errorf("die Aktion wurde neu eingereiht: %v", kinds)
	}
	var steps []string
	for _, st := range r.Steps {
		steps = append(steps, fmt.Sprintf("%s:%s>%s", st.Kind, st.From, st.To))
	}
	if want := []string{"expire:claimed>reconciling", "reconcile:reconciling>succeeded"}; !slices.Equal(steps, want) {
		t.Errorf("Schritte %v, erwartet %v", steps, want)
	}
	// Ein weiterer Durchlauf ändert nichts mehr.
	env.run()
	if env.fake.totalExecutes() != 1 {
		t.Errorf("Execute insgesamt %d", env.fake.totalExecutes())
	}
}

// TestBatchPartialFailure: Fünf Aktionen, drei Erfolge, ein wiederholbarer und ein dauerhafter Fehler. Jede Aktion wird
// einzeln bestätigt; ein Teilfehler macht Erfolge nicht rückgängig; Wiederholungen betreffen nur die wiederholbare.
func TestBatchPartialFailure(t *testing.T) {
	env := newOutboxEnv(t)
	var as []domain.OutboxAction
	for i := 1; i <= 5; i++ {
		as = append(as, env.action("wo-1", fmt.Sprintf("part-%d", i)))
	}
	byKey := map[string]int{}
	for i, a := range as {
		byKey[a.OperationKey] = i + 1
	}
	env.fake.onExecute = func(a domain.OutboxAction, call int) (bool, ExecuteResult) {
		switch byKey[a.OperationKey] {
		case 4:
			if call == 1 {
				return false, ExecuteResult{Outcome: domain.OutboxRetryable, Err: domain.OutboxError{Code: "http_429", Message: "Rate Limit"}}
			}
		case 5:
			return false, ExecuteResult{Outcome: domain.OutboxPermanent, Err: domain.OutboxError{Code: "http_403", Message: "verboten"}}
		}
		return true, outboxOK(a)
	}
	env.enqueue(as...)
	start := env.clock.now()
	env.run()

	for _, a := range as[:3] {
		got := env.wantState(a, domain.OutboxSucceeded)
		if got.ExternalRef != outboxRef(a) {
			t.Errorf("%s: Referenz %q", a.OperationKey, got.ExternalRef)
		}
	}
	retry := env.wantState(as[3], domain.OutboxRetryWait)
	if want := start.Add(BackoffDelay(1, env.policy.BackoffInitial, env.policy.BackoffMax)); !retry.NextAttemptAt.Equal(want) || !strings.HasPrefix(retry.LastError, "http_429") {
		t.Errorf("wiederholbar: nächster Versuch %v (erwartet %v), Fehler %q", retry.NextAttemptAt, want, retry.LastError)
	}
	perm := env.wantState(as[4], domain.OutboxNeedsAttention)
	if !perm.NextAttemptAt.IsZero() || !strings.HasPrefix(perm.LastError, "http_403") {
		t.Errorf("dauerhaft: %+v", perm)
	}
	// Jede der drei Erfolge ist einzeln bestätigt (je ein Ereignis).
	n := 0
	for _, k := range env.kinds() {
		if k == domain.AuditOutboxSucceeded {
			n++
		}
	}
	if n != 3 {
		t.Errorf("%d Erfolgsereignisse, erwartet 3", n)
	}

	// Vor Fälligkeit geschieht nichts.
	env.run()
	if env.fake.totalExecutes() != 5 {
		t.Fatalf("Execute-Aufrufe vor Fälligkeit: %d", env.fake.totalExecutes())
	}
	// Nach Fälligkeit wird nur die wiederholbare Aktion erneut ausgeführt.
	env.clock.advance(BackoffDelay(1, env.policy.BackoffInitial, env.policy.BackoffMax))
	env.run()
	env.wantState(as[3], domain.OutboxSucceeded)
	for i, a := range as {
		want := 1
		if i == 3 {
			want = 2
		}
		if got := env.fake.executed(a); got != want {
			t.Errorf("Teil %d: Execute %d-mal, erwartet %d", i+1, got, want)
		}
	}
	env.wantState(as[4], domain.OutboxNeedsAttention)
}

// TestTimeoutIsNotFailure: Ein Timeout beweist weder Erfolg noch Misserfolg. Die Aktion geht nach reconciling, nicht nach
// retry_wait und nicht nach pending; der Abgleich findet das trotz Timeout angelegte Objekt.
func TestTimeoutIsNotFailure(t *testing.T) {
	env := newOutboxEnv(t)
	a := env.action("wo-1", "result")
	env.enqueue(a)
	env.fake.onExecute = func(a domain.OutboxAction, call int) (bool, ExecuteResult) {
		// Das Objekt entstand, die Antwort kam zu spät.
		return true, ExecuteResult{Outcome: ClassifyTransportError(context.DeadlineExceeded), Err: domain.OutboxError{Code: "timeout", Message: "Antwort blieb aus"}}
	}
	start := env.clock.now()
	env.run()
	got := env.wantState(a, domain.OutboxReconciling)
	if got.Attempts != 1 || got.ReconcileAttempts != 0 || !strings.HasPrefix(got.LastError, "timeout") ||
		!got.NextAttemptAt.Equal(start.Add(env.policy.BackoffInitial)) {
		t.Errorf("nach Timeout: %+v", got)
	}
	// Die Aktion steht weder in der Warteschlange noch wird sie vor dem Abgleichzeitpunkt erneut ausgeführt.
	env.run()
	if env.fake.executed(a) != 1 || env.fake.looked(a) != 0 {
		t.Fatalf("zu früh: Execute %d, Lookup %d", env.fake.executed(a), env.fake.looked(a))
	}
	env.clock.advance(env.policy.BackoffInitial)
	env.run()
	if got := env.wantState(a, domain.OutboxSucceeded); got.ExternalRef != outboxRef(a) {
		t.Errorf("Referenz %q", got.ExternalRef)
	}
	if env.fake.executed(a) != 1 {
		t.Errorf("Execute %d-mal: Timeout wurde wie ein Fehlschlag behandelt", env.fake.executed(a))
	}
	if slices.Contains(env.kinds(), domain.AuditOutboxRetryScheduled) {
		t.Error("ein Timeout hat eine Wiederholung eingeplant")
	}
}

// TestReconcileNotFoundRetries: Belegt der Abgleich sicher „nicht vorhanden“, geht die Aktion nach pending, und ein
// erneuter Versuch ist zulässig.
func TestReconcileNotFoundRetries(t *testing.T) {
	env := newOutboxEnv(t)
	a := env.action("wo-1", "result")
	env.enqueue(a)
	env.fake.onExecute = func(a domain.OutboxAction, call int) (bool, ExecuteResult) {
		if call == 1 { // Anfrage ging verloren: nichts wurde angelegt, der Adapter weiß es nicht
			return false, ExecuteResult{Outcome: domain.OutboxUncertain, Err: domain.OutboxError{Code: "connection_reset"}}
		}
		return true, outboxOK(a)
	}
	env.run()
	env.wantState(a, domain.OutboxReconciling)

	env.clock.advance(env.policy.BackoffInitial)
	r := env.run()
	got := env.wantState(a, domain.OutboxSucceeded)
	if got.Attempts != 2 || env.fake.executed(a) != 2 || env.fake.looked(a) != 1 || got.ReconcileAttempts != 0 {
		t.Errorf("Versuche %d, Execute %d, Lookup %d, Abgleichversuche %d", got.Attempts, env.fake.executed(a), env.fake.looked(a), got.ReconcileAttempts)
	}
	var steps []string
	for _, st := range r.Steps {
		steps = append(steps, fmt.Sprintf("%s:%s>%s", st.Kind, st.From, st.To))
	}
	if want := []string{"reconcile:reconciling>pending", "execute:claimed>succeeded"}; !slices.Equal(steps, want) {
		t.Errorf("Schritte %v, erwartet %v", steps, want)
	}
	if !slices.Contains(env.kinds(), domain.AuditOutboxRequeued) {
		t.Errorf("Ereignisse %v", env.kinds())
	}
}

// Auch „sicher nicht vorhanden“ öffnet keinen endlosen Kreislauf: Die Höchstzahl der Versuche gilt weiter.
func TestReconcileNotFoundRespectsMaxAttempts(t *testing.T) {
	env := newOutboxEnv(t, func(p *OutboxPolicy) { p.MaxAttempts = 2 })
	a := env.action("wo-1", "result")
	env.enqueue(a)
	env.fake.onExecute = func(domain.OutboxAction, int) (bool, ExecuteResult) {
		return false, ExecuteResult{Outcome: domain.OutboxUncertain, Err: domain.OutboxError{Code: "timeout"}}
	}
	for range 4 {
		env.run()
		env.clock.advance(env.policy.BackoffMax)
	}
	got := env.wantState(a, domain.OutboxNeedsAttention)
	if env.fake.executed(a) != 2 || !strings.HasPrefix(got.LastError, "retries_exhausted") {
		t.Errorf("Execute %d, Fehler %q", env.fake.executed(a), got.LastError)
	}
}

// TestUnresolvableNeedsAttention: Ein nicht abgleichbarer Fall erzeugt sichtbaren Klärungsbedarf.
func TestUnresolvableNeedsAttention(t *testing.T) {
	env := newOutboxEnv(t) // MaxReconcileAttempts 3
	a := env.action("wo-1", "result")
	env.enqueue(a)
	env.fake.onExecute = func(domain.OutboxAction, int) (bool, ExecuteResult) {
		return false, ExecuteResult{Outcome: domain.OutboxUncertain, Err: domain.OutboxError{Code: "timeout"}}
	}
	env.fake.onLookup = func(domain.OutboxAction, int) (LookupResult, bool) {
		return LookupResult{Status: LookupUnknown, Err: domain.OutboxError{Code: "http_503", Message: "Dienst nicht erreichbar"}}, true
	}
	env.run()
	for want := 1; want <= 3; want++ {
		env.clock.advance(env.policy.BackoffMax)
		env.run()
		got := env.wantState(a, domain.OutboxReconciling)
		if got.ReconcileAttempts != want {
			t.Fatalf("Abgleichversuche %d, erwartet %d", got.ReconcileAttempts, want)
		}
	}
	env.clock.advance(env.policy.BackoffMax)
	env.run()
	got := env.wantState(a, domain.OutboxNeedsAttention)
	if !strings.HasPrefix(got.LastError, "reconcile_exhausted") || got.ReconcileAttempts != 3 {
		t.Errorf("Eskalation: %+v", got)
	}
	if env.fake.executed(a) != 1 || env.fake.looked(a) != 4 {
		t.Errorf("Execute %d (erwartet 1: nie blind wiederholt), Lookup %d (erwartet 4)", env.fake.executed(a), env.fake.looked(a))
	}
	// Weitere Durchläufe fassen die Aktion nicht mehr an.
	env.clock.advance(time.Hour)
	env.run()
	if env.fake.executed(a) != 1 || env.fake.looked(a) != 4 {
		t.Errorf("nach Eskalation: Execute %d, Lookup %d", env.fake.executed(a), env.fake.looked(a))
	}

	// Sichtbar: in der Liste mit Klärungsbedarf und in der Zählung je Zustand von Inspect (ccmas status).
	list, err := env.ob.NeedsAttention(msgCtx(t), msgProject)
	if err != nil || len(list) != 1 || list[0].ActionID != a.ActionID {
		t.Fatalf("NeedsAttention: %+v %v", list, err)
	}
	rep, err := persistence.Inspect(msgCtx(t), env.store.Path())
	if err != nil {
		t.Fatal(err)
	}
	var counts map[string]int
	for _, p := range rep.Data.Projects {
		if p.ProjectID == msgProject {
			counts = p.Outbox
		}
	}
	if counts["needs_attention"] != 1 || counts["reconciling"] != 0 || counts["pending"] != 0 {
		t.Errorf("Inspect zählt Outbox: %v", counts)
	}
}

// Kann der Abgleich dauerhaft nicht gelingen (zum Beispiel keine Berechtigung), wartet er nicht auf die Höchstzahl.
func TestBlockedLookupNeedsAttentionAtOnce(t *testing.T) {
	env := newOutboxEnv(t)
	a := env.action("wo-1", "result")
	env.enqueue(a)
	env.fake.onExecute = func(domain.OutboxAction, int) (bool, ExecuteResult) {
		return false, ExecuteResult{Outcome: domain.OutboxUncertain, Err: domain.OutboxError{Code: "timeout"}}
	}
	env.fake.onLookup = func(domain.OutboxAction, int) (LookupResult, bool) {
		return LookupResult{Status: LookupBlocked, Err: domain.OutboxError{Code: "http_403"}}, true
	}
	env.run()
	env.clock.advance(env.policy.BackoffInitial)
	env.run()
	got := env.wantState(a, domain.OutboxNeedsAttention)
	if !strings.HasPrefix(got.LastError, "lookup_blocked") && !strings.HasPrefix(got.LastError, "http_403") {
		t.Errorf("Fehler %q", got.LastError)
	}
	if env.fake.executed(a) != 1 {
		t.Errorf("Execute %d", env.fake.executed(a))
	}
}

// TestBackoffBounded: Die Abstände wachsen, sind gedeckelt, nach der Höchstzahl folgt needs_attention, und ein
// abgebrochener Kontext unterbricht.
func TestBackoffBounded(t *testing.T) {
	init, max := 10*time.Second, time.Minute
	want := []time.Duration{10 * time.Second, 20 * time.Second, 40 * time.Second, time.Minute, time.Minute, time.Minute}
	for i, w := range want {
		if got := BackoffDelay(i+1, init, max); got != w {
			t.Errorf("BackoffDelay(%d) = %v, erwartet %v", i+1, got, w)
		}
	}
	for _, n := range []int{0, -3} {
		if got := BackoffDelay(n, init, max); got != init {
			t.Errorf("BackoffDelay(%d) = %v", n, got)
		}
	}
	// Kein Überlauf bei großen Versuchszahlen.
	for _, n := range []int{62, 63, 64, 1000, 1 << 30} {
		if got := BackoffDelay(n, init, max); got != max {
			t.Errorf("BackoffDelay(%d) = %v, erwartet Deckel", n, got)
		}
	}
	if got := BackoffDelay(3, time.Hour, time.Minute); got != time.Minute {
		t.Errorf("Anfangswert über dem Deckel: %v", got)
	}

	// Ablauf: immer wiederholbarer Fehler, MaxAttempts 4.
	env := newOutboxEnv(t)
	a := env.action("wo-1", "result")
	env.enqueue(a)
	env.fake.onExecute = func(domain.OutboxAction, int) (bool, ExecuteResult) {
		return false, ExecuteResult{Outcome: domain.OutboxRetryable, Err: domain.OutboxError{Code: "http_503"}}
	}
	var gaps []time.Duration
	for range env.policy.MaxAttempts - 1 {
		env.run()
		got := env.wantState(a, domain.OutboxRetryWait)
		gap := got.NextAttemptAt.Sub(env.clock.now())
		gaps = append(gaps, gap)
		env.clock.advance(gap - time.Nanosecond)
		env.run() // noch nicht fällig
		if env.fake.executed(a) != got.Attempts {
			t.Fatalf("vor Fälligkeit erneut ausgeführt: %d", env.fake.executed(a))
		}
		env.clock.advance(time.Nanosecond)
	}
	if !slices.Equal(gaps, []time.Duration{10 * time.Second, 20 * time.Second, 40 * time.Second}) {
		t.Errorf("Abstände %v", gaps)
	}
	env.run() // vierter und letzter Versuch
	got := env.wantState(a, domain.OutboxNeedsAttention)
	if got.Attempts != 4 || env.fake.executed(a) != 4 || !strings.HasPrefix(got.LastError, "retries_exhausted") {
		t.Errorf("nach der Höchstzahl: %+v, Execute %d", got, env.fake.executed(a))
	}
	env.clock.advance(time.Hour)
	env.run()
	if env.fake.executed(a) != 4 {
		t.Errorf("nach needs_attention erneut ausgeführt: %d", env.fake.executed(a))
	}
}

// TestBackoffInterruptible: Run wartet über die injizierte Wartefunktion und endet mit dem Kontext; es schläft nie wirklich.
func TestBackoffInterruptible(t *testing.T) {
	env := newOutboxEnv(t, func(p *OutboxPolicy) { p.MaxAttempts = 10 })
	ctx, cancel := context.WithCancel(msgCtx(t))
	defer cancel()
	var waits []time.Duration
	env.policy.Wait = func(ctx context.Context, d time.Duration) error {
		waits = append(waits, d)
		if len(waits) == 3 {
			cancel()
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		env.clock.advance(d)
		return nil
	}
	env.ob = env.newOutbox(env.store)
	a := env.action("wo-1", "result")
	env.enqueue(a)
	env.fake.onExecute = func(domain.OutboxAction, int) (bool, ExecuteResult) {
		return false, ExecuteResult{Outcome: domain.OutboxRetryable, Err: domain.OutboxError{Code: "http_503"}}
	}

	err := env.ob.Run(ctx, msgProject)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run endete mit %v, erwartet context.Canceled", err)
	}
	if want := []time.Duration{10 * time.Second, 20 * time.Second, 40 * time.Second}; !slices.Equal(waits, want) {
		t.Errorf("Wartezeiten %v, erwartet %v (Backoff bis zur nächsten Fälligkeit)", waits, want)
	}
	if env.fake.executed(a) != 3 {
		t.Errorf("Execute %d, erwartet 3", env.fake.executed(a))
	}

	// Ein bereits abgebrochener Kontext führt nichts mehr aus.
	before := env.fake.totalExecutes()
	if _, err := env.ob.RunOnce(ctx, msgProject); !errors.Is(err, context.Canceled) {
		t.Errorf("RunOnce mit abgebrochenem Kontext: %v", err)
	}
	if env.fake.totalExecutes() != before {
		t.Error("RunOnce führte mit abgebrochenem Kontext aus")
	}
}

// Die Standard-Wartefunktion endet mit dem Kontext, ohne zu schlafen.
func TestDefaultWaitStopsWithContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := defaultOutboxWait(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Errorf("defaultOutboxWait mit abgebrochenem Kontext: %v", err)
	}
	if err := defaultOutboxWait(context.Background(), 0); err != nil {
		t.Errorf("defaultOutboxWait(0): %v", err)
	}
}

// TestNeedsAttentionResolution: Die drei Klärungswege sind auditiert, mit Akteur und Grund.
func TestNeedsAttentionResolution(t *testing.T) {
	env := newOutboxEnv(t)
	retry, confirm, cancel := env.action("wo-1", "retry"), env.action("wo-1", "confirm"), env.action("wo-1", "cancel")
	env.enqueue(retry, confirm, cancel)
	env.fake.onExecute = func(a domain.OutboxAction, call int) (bool, ExecuteResult) {
		if call == 1 {
			return false, ExecuteResult{Outcome: domain.OutboxPermanent, Err: domain.OutboxError{Code: "http_403", Message: "verboten"}}
		}
		return true, outboxOK(a)
	}
	env.run()
	list, err := env.ob.NeedsAttention(msgCtx(t), msgProject)
	if err != nil || len(list) != 3 {
		t.Fatalf("NeedsAttention: %d %v", len(list), err)
	}
	ctx := msgCtx(t)

	// Erneut versuchen: zurück nach pending, Zähler zurückgesetzt; der nächste Durchlauf führt wieder aus.
	got, err := env.ob.Resolve(ctx, msgProject, retry.ActionID, "alex", domain.OutboxResolution{Action: domain.ResolveRetry, Reason: "Berechtigung erteilt"})
	if err != nil || got.State != domain.OutboxPending || got.Attempts != 0 {
		t.Fatalf("retry: %+v %v", got, err)
	}
	// Als erledigt bestätigen: mit externer Referenz, ohne Ausführung.
	got, err = env.ob.Resolve(ctx, msgProject, confirm.ActionID, "alex", domain.OutboxResolution{Action: domain.ResolveConfirm, ExternalRef: "gid-manuell", Reason: "Kommentar liegt vor"})
	if err != nil || got.State != domain.OutboxSucceeded || got.ExternalRef != "gid-manuell" {
		t.Fatalf("confirm: %+v %v", got, err)
	}
	// Abbrechen.
	got, err = env.ob.Resolve(ctx, msgProject, cancel.ActionID, "alex", domain.OutboxResolution{Action: domain.ResolveCancel, Reason: "Ticket gelöscht"})
	if err != nil || got.State != domain.OutboxCancelled {
		t.Fatalf("cancel: %+v %v", got, err)
	}
	env.run()
	env.wantState(retry, domain.OutboxSucceeded)
	if env.fake.executed(retry) != 2 || env.fake.executed(confirm) != 1 || env.fake.executed(cancel) != 1 {
		t.Errorf("Execute: retry %d, confirm %d, cancel %d", env.fake.executed(retry), env.fake.executed(confirm), env.fake.executed(cancel))
	}
	if list, _ := env.ob.NeedsAttention(msgCtx(t), msgProject); len(list) != 0 {
		t.Errorf("Klärungsbedarf bleibt: %d", len(list))
	}

	// Audit: Akteur, Grund, Weg.
	var resolved []domain.AuditEvent
	err = env.store.View(ctx, func(r *persistence.ReadTx) error {
		evs, err := r.Events(msgProject, persistence.EventFilter{Level: domain.AuditLevelOutbox})
		for _, e := range evs {
			if e.Kind == domain.AuditOutboxResolved {
				resolved = append(resolved, e)
			}
		}
		return err
	})
	if err != nil || len(resolved) != 3 {
		t.Fatalf("Klärungsereignisse: %d %v", len(resolved), err)
	}
	for i, w := range []struct{ how, reason, to string }{
		{"retry", "Berechtigung erteilt", "pending"}, {"confirm", "Kommentar liegt vor", "succeeded"}, {"cancel", "Ticket gelöscht", "cancelled"},
	} {
		e := resolved[i]
		if e.Actor != "alex" || e.Details["resolution"] != w.how || e.Details["reason"] != w.reason || e.ToState != w.to || e.FromState != "needs_attention" {
			t.Errorf("Ereignis %d: %+v", i, e)
		}
	}

	// Ohne Grund, mit ungültigem Akteur oder außerhalb von needs_attention wird nichts geklärt.
	extra := env.action("wo-1", "extra")
	env.enqueue(extra)
	for name, c := range map[string]struct {
		actor string
		res   domain.OutboxResolution
		code  persistence.Code
	}{
		"ohne Grund":                 {"alex", domain.OutboxResolution{Action: domain.ResolveCancel}, persistence.CodeInvalid},
		"Akteur ungültig":            {"a b", domain.OutboxResolution{Action: domain.ResolveCancel, Reason: "x"}, persistence.CodeInvalid},
		"nicht im Klärungszustand":   {"alex", domain.OutboxResolution{Action: domain.ResolveCancel, Reason: "x"}, persistence.CodeConflict},
		"bestätigen ohne Referenz":   {"alex", domain.OutboxResolution{Action: domain.ResolveConfirm, Reason: "x"}, persistence.CodeInvalid},
		"unbekannter Klärungsweg":    {"alex", domain.OutboxResolution{Action: "vergessen", Reason: "x"}, persistence.CodeInvalid},
		"bereits abgeschlossene Tat": {"alex", domain.OutboxResolution{Action: domain.ResolveRetry, Reason: "x"}, persistence.CodeConflict},
	} {
		id := extra.ActionID
		if name == "bereits abgeschlossene Tat" {
			id = retry.ActionID
		}
		if _, err := env.ob.Resolve(ctx, msgProject, id, c.actor, c.res); persistence.CodeOf(err) != c.code {
			t.Errorf("%s: Code %q (%v), erwartet %q", name, persistence.CodeOf(err), err, c.code)
		}
	}
	env.wantState(extra, domain.OutboxPending)
}

// ---- Weitere Verträge --------------------------------------------------------------------------------------

func TestMissingAdapterNeedsAttention(t *testing.T) {
	env := newOutboxEnv(t)
	a, err := domain.NewOutboxAction(msgProject, "jira.comment", "", nil, "wo-1", "x")
	if err != nil {
		t.Fatal(err)
	}
	env.enqueue(a)
	env.run()
	got := env.wantState(a, domain.OutboxNeedsAttention)
	if !strings.HasPrefix(got.LastError, "no_adapter") {
		t.Errorf("Fehler %q", got.LastError)
	}
}

func TestAdapterPanicIsUncertain(t *testing.T) {
	env := newOutboxEnv(t)
	a := env.action("wo-1", "result")
	env.enqueue(a)
	env.fake.onExecute = func(domain.OutboxAction, int) (bool, ExecuteResult) { panic("kaputt") }
	env.run()
	got := env.wantState(a, domain.OutboxReconciling)
	if !strings.HasPrefix(got.LastError, "adapter_panic") {
		t.Errorf("Fehler %q", got.LastError)
	}
}

// Ein Erfolg ohne brauchbare Referenz ist nicht verlässlich genug zum Abschließen: Der Abgleich holt die Referenz.
func TestSuccessWithoutUsableReferenceIsUncertain(t *testing.T) {
	for name, ref := range map[string]string{"leer": "", "Steuerzeichen": "gid\n1", "zu lang": strings.Repeat("x", 300)} {
		t.Run(name, func(t *testing.T) {
			env := newOutboxEnv(t)
			a := env.action("wo-1", "result")
			env.enqueue(a)
			env.fake.onExecute = func(a domain.OutboxAction, call int) (bool, ExecuteResult) {
				return true, ExecuteResult{Outcome: domain.OutboxSuccess, ExternalRef: ref}
			}
			env.run()
			env.wantState(a, domain.OutboxReconciling)
			env.clock.advance(env.policy.BackoffInitial)
			env.run()
			got := env.wantState(a, domain.OutboxSucceeded)
			if got.ExternalRef != outboxRef(a) || env.fake.executed(a) != 1 {
				t.Errorf("Referenz %q, Execute %d", got.ExternalRef, env.fake.executed(a))
			}
		})
	}
}

// Dauert der Aufruf länger als der Claim und wurde der Claim inzwischen abgelaufen, geht das Ergebnis nicht verloren.
func TestLateCompletionAfterClaimExpiry(t *testing.T) {
	env := newOutboxEnv(t)
	other := env.newOutbox(env.store)
	a := env.action("wo-1", "result")
	env.enqueue(a)
	first := true
	env.fake.onLookup = func(domain.OutboxAction, int) (LookupResult, bool) {
		return LookupResult{Status: LookupUnknown, Err: domain.OutboxError{Code: "busy"}}, true
	}
	env.fake.onExecute = func(a domain.OutboxAction, call int) (bool, ExecuteResult) {
		if first {
			first = false
			env.clock.advance(env.policy.ClaimTTL + time.Second)
			if _, err := other.RunOnce(msgCtx(t), msgProject); err != nil { // ein anderer Prozess sieht den abgelaufenen Claim
				t.Errorf("RunOnce (anderer Prozess): %v", err)
			}
		}
		return true, outboxOK(a)
	}
	env.run()
	got := env.wantState(a, domain.OutboxSucceeded)
	if got.ExternalRef != outboxRef(a) || env.fake.executed(a) != 1 {
		t.Errorf("Referenz %q, Execute %d", got.ExternalRef, env.fake.executed(a))
	}
}

// TestConcurrentRunOnce: Zwei Prozesse (zwei Store-Instanzen) führen jede Aktion genau einmal aus.
func TestConcurrentRunOnce(t *testing.T) {
	env := newOutboxEnv(t, func(p *OutboxPolicy) { p.BatchSize = 3 })
	second := outboxOpen(t, env.store.Path(), env.clock)
	obs := []*Outbox{env.ob, env.newOutbox(second)}
	var as []domain.OutboxAction
	for i := range 18 {
		as = append(as, env.action("wo-1", fmt.Sprintf("c%02d", i)))
	}
	env.enqueue(as...)
	var wg sync.WaitGroup
	for w := range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 20 {
				r, err := obs[w%2].RunOnce(msgCtx(t), msgProject)
				if err != nil {
					t.Errorf("RunOnce: %v", err)
					return
				}
				if len(r.Steps) == 0 {
					return
				}
			}
		}()
	}
	wg.Wait()
	for _, a := range as {
		env.wantState(a, domain.OutboxSucceeded)
		if env.fake.executed(a) != 1 {
			t.Errorf("%s: Execute %d-mal", a.OperationKey, env.fake.executed(a))
		}
	}
}

func TestEnqueueIsIdempotentAndAtomic(t *testing.T) {
	env := newOutboxEnv(t)
	a, b := env.action("wo-1", "a"), env.action("wo-1", "b")
	n, err := env.ob.EnqueueBatch(msgCtx(t), []domain.OutboxAction{a, b})
	if err != nil || n != 2 {
		t.Fatalf("EnqueueBatch: %d %v", n, err)
	}
	created, err := env.ob.Enqueue(msgCtx(t), a)
	if err != nil || created {
		t.Errorf("Enqueue derselben Aktion: %v %v", created, err)
	}
	// Eine Batch mit einem ungültigen Eintrag schreibt nichts, auch nicht die gültigen davor.
	good, bad := env.action("wo-1", "c"), env.action("wo-1", "d")
	bad.OperationKey = "ohne-projekt"
	if _, err := env.ob.EnqueueBatch(msgCtx(t), []domain.OutboxAction{good, bad}); persistence.CodeOf(err) != persistence.CodeInvalid {
		t.Errorf("Batch mit ungültigem Eintrag: %v", err)
	}
	err = env.store.View(msgCtx(t), func(r *persistence.ReadTx) error {
		if _, err := r.OutboxAction(msgProject, good.ActionID); persistence.CodeOf(err) != persistence.CodeNotFound {
			t.Errorf("gültiger Eintrag der gescheiterten Batch gespeichert: %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestOutboxPolicy(t *testing.T) {
	clock := &outboxClock{t: time.Unix(0, 0)}
	s := outboxOpen(t, filepath.Join(t.TempDir(), "state", "ccmas.db"), clock)
	if err := s.Migrate(msgCtx(t)); err != nil {
		t.Fatal(err)
	}
	d := DefaultOutboxPolicy()
	if d.ClaimTTL <= d.ExecuteTimeout || d.ClaimTTL <= d.LookupTimeout || d.MaxAttempts < 2 || d.MaxReconcileAttempts < 1 ||
		d.BackoffInitial <= 0 || d.BackoffMax < d.BackoffInitial || d.BatchSize < 1 {
		t.Errorf("Standardwerte unstimmig: %+v", d)
	}
	// Eine leere Richtlinie ergibt die Standardwerte.
	if _, err := NewOutbox(s, "orchestrator", nil, OutboxPolicy{}); err != nil {
		t.Errorf("leere Richtlinie: %v", err)
	}
	bad := map[string]func(p *OutboxPolicy){
		"Claim kürzer als der Aufruf": func(p *OutboxPolicy) { p.ClaimTTL, p.ExecuteTimeout = time.Second, time.Minute },
		"Claim kürzer als die Suche":  func(p *OutboxPolicy) { p.ClaimTTL, p.LookupTimeout = time.Second, time.Minute },
		"Deckel unter Anfangswert":    func(p *OutboxPolicy) { p.BackoffInitial, p.BackoffMax = time.Minute, time.Second },
		"negative Versuche":           func(p *OutboxPolicy) { p.MaxAttempts = -1 },
		"negativer Batch":             func(p *OutboxPolicy) { p.BatchSize = -1 },
	}
	for name, mod := range bad {
		p := outboxPolicy(clock)
		mod(&p)
		if _, err := NewOutbox(s, "orchestrator", nil, p); err == nil {
			t.Errorf("%s: Richtlinie angenommen", name)
		}
	}
	if _, err := NewOutbox(s, "kein gültiger Akteur", nil, d); err == nil {
		t.Error("ungültiger Akteur angenommen")
	}
}

func TestClassifyTransportError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want domain.OutboxOutcome
	}{
		{"kein Fehler", nil, domain.OutboxSuccess},
		{"Zeitüberschreitung des Kontexts", context.DeadlineExceeded, domain.OutboxUncertain},
		{"Abbruch des Kontexts", context.Canceled, domain.OutboxUncertain},
		{"Timeout im Netz", &net.DNSError{IsTimeout: true}, domain.OutboxUncertain},
		{"Verbindung abgebrochen (EOF)", io.EOF, domain.OutboxUncertain},
		{"unerwartetes Ende", fmt.Errorf("lesen: %w", io.ErrUnexpectedEOF), domain.OutboxUncertain},
		{"Verbindung zurückgesetzt", &net.OpError{Op: "read", Err: syscall.ECONNRESET}, domain.OutboxUncertain},
		{"kaputte Leitung", &net.OpError{Op: "write", Err: syscall.EPIPE}, domain.OutboxUncertain},
		{"Verbindung verweigert", &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}, domain.OutboxRetryable},
		{"Name nicht gefunden", &net.DNSError{IsNotFound: true}, domain.OutboxRetryable},
		{"Unbekanntes ist ungewiss", errors.New("seltsam"), domain.OutboxUncertain},
	}
	for _, c := range cases {
		if got := ClassifyTransportError(c.err); got != c.want {
			t.Errorf("%s: %s, erwartet %s", c.name, got, c.want)
		}
	}
}
