package orchestrator

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/alexwenzel/ccmas/internal/domain"
	"github.com/alexwenzel/ccmas/internal/persistence"
	"github.com/alexwenzel/ccmas/internal/profiles"
	"github.com/alexwenzel/ccmas/internal/supervisor"
)

// tasksWorld ist eine echte SQLite-Datei mit Projekt, Agenten (agent-1 Implementierer, rev-1 Reviewer), den Zielen ms-1
// (beauftragt) und ms-2 (fremd), den Aufträgen wo-1 und wo-2 (ms-1) sowie far (ms-2), und einem Tasks auf dem Ziel ms-1. Der
// Store und Tasks teilen sich eine Uhr.
type tasksWorld struct {
	t      *testing.T
	store  *persistence.Store
	path   string
	clock  *tasksClock
	tasks  *Tasks
	pol    supervisor.Policy
	offers SnapshotOffers
}

func newTasksWorld(t *testing.T) *tasksWorld {
	t.Helper()
	pol, offers := tasksPolicy(t)
	return newTasksWorldWith(t, pol, offers)
}

func newTasksWorldWith(t *testing.T, pol supervisor.Policy, offers SnapshotOffers) *tasksWorld {
	t.Helper()
	s, clock, path := tasksTestStoreAt(t)
	tasksBase(t, s)
	w := &tasksWorld{t: t, store: s, path: path, clock: clock, pol: pol, offers: offers}
	tasksUpdate(t, s, func(tx *persistence.Tx) error {
		for _, o := range []persistence.WorkOrderRecord{
			tasksOrder(t, "wo-1", "ms-1", supervisor.OrderImplementation),
			tasksOrder(t, "wo-2", "ms-1", supervisor.OrderImplementation),
			tasksOrder(t, "far", "ms-2", supervisor.OrderImplementation),
		} {
			if _, err := tx.CreateWorkOrder(o); err != nil {
				return err
			}
		}
		return nil
	})
	w.tasks = w.newTasks(s)
	return w
}

func (w *tasksWorld) newTasks(s *persistence.Store) *Tasks {
	w.t.Helper()
	tk, err := NewTasks(s, w.pol, w.offers, TasksFixedGoal("ms-1"), "orchestrator", TasksPolicy{Now: w.clock.Now})
	if err != nil {
		w.t.Fatalf("NewTasks: %v", err)
	}
	return tk
}

// secondStore öffnet eine zweite Store-Instanz auf derselben Datei (eigene Verbindungen).
func (w *tasksWorld) secondStore() *persistence.Store {
	w.t.Helper()
	s, err := persistence.Open(tasksCtx(w.t), w.path, persistence.WithClock(w.clock.Now))
	if err != nil {
		w.t.Fatalf("zweiter Store: %v", err)
	}
	w.t.Cleanup(func() { _ = s.Close() })
	return s
}

func (w *tasksWorld) snapshot() supervisor.ProjectSnapshot {
	w.t.Helper()
	return tasksBuild(w.t, w.store, "ms-1", w.offers)
}

// impl ist eine Umsetzungs-Entscheidung für den Auftrag auf dem aktuellen Stand.
func (w *tasksWorld) impl(decisionID, orderID string) domain.Decision {
	w.t.Helper()
	return tasksImplementation(w.t, w.snapshot(), decisionID, orderID)
}

// profileFor löst das wirksame Profil einer Delegation wie der Validator auf (für gefälschte Pläne, die der Validator nie
// annähme).
func (w *tasksWorld) profileFor(d domain.Decision) domain.AgentProfile {
	w.t.Helper()
	sel := d.Selection
	p, err := w.pol.Resolver.ResolveProfile(sel.Role, "", profiles.Overrides{Supervisor: profiles.Selection{Model: sel.Model, Effort: sel.Effort, SkillIDs: sel.SkillIDs}}, w.pol.Caps)
	if err != nil {
		w.t.Fatalf("ResolveProfile: %v", err)
	}
	return p
}

func tasksSum(v any) string {
	raw, _ := json.Marshal(v)
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:])
}

// plan lässt den Validator den Batch auf dem aktuellen Snapshot prüfen und hält ihn mit den wirksamen Profilen als
// angenommenen Plan fest, wie es der Planer tut. Ein abgelehnter Batch ist ein Fehler des Tests.
func (w *tasksWorld) plan(batchID string, ds ...domain.Decision) domain.DecisionBatch {
	w.t.Helper()
	snap := w.snapshot()
	batch := tasksBatch(snap, ds...)
	batch.BatchID = batchID
	vb, err := supervisor.Validator{}.Accept(batch, snap, w.pol)
	if err != nil {
		w.t.Fatalf("Batch %s abgelehnt: %v", batchID, err)
	}
	profs := map[string]domain.AgentProfile{}
	for _, d := range vb.Decisions {
		if d.Delegation != nil {
			profs[d.Decision.DecisionID] = d.Delegation.Profile
		}
	}
	w.record(persistence.PlanRecord{Batch: batch, Verdict: supervisor.VerdictAccepted, Delegations: profs,
		PlanningSnapshotSHA256: tasksSum(snap), CurrentSnapshotSHA256: tasksSum(snap)})
	return batch
}

func (w *tasksWorld) record(p persistence.PlanRecord) {
	w.t.Helper()
	tasksUpdate(w.t, w.store, func(tx *persistence.Tx) error { return tx.RecordPlan(p) })
}

// reserve plant (Validator) und reserviert einen Auftrag über ReserveBatch.
func (w *tasksWorld) reserve(batchID, orderID string) domain.ExecutionLease {
	w.t.Helper()
	w.plan(batchID, w.impl("dec-1", orderID))
	ls, err := w.tasks.ReserveBatch(tasksCtx(w.t), tasksProject, batchID)
	if err != nil {
		w.t.Fatalf("ReserveBatch %s: %v", batchID, err)
	}
	if len(ls) != 1 {
		w.t.Fatalf("ReserveBatch %s: %d Leases", batchID, len(ls))
	}
	return ls[0]
}

func tasksEvent(execID string, attempt int, typ domain.MessageType, payload any) domain.RunEvent {
	return domain.RunEvent{
		Envelope: domain.Envelope{ProtocolVersion: domain.ProtocolVersion, ProjectID: tasksProject, ExecutionID: execID, Attempt: attempt,
			CorrelationID: "corr-1", Type: typ, Timestamp: time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)},
		Payload: payload,
	}
}

func tasksSession(execID string) domain.RunEvent {
	return tasksEvent(execID, 1, domain.MsgSessionStarted, domain.SessionStarted{SessionID: "sess-" + execID, Model: "m", Effort: domain.EffortHigh, ClaudeVersion: "1"})
}

func tasksResult(execID string, attempt int, outcome domain.Outcome) domain.RunEvent {
	return tasksEvent(execID, attempt, domain.MsgResult, domain.Result{Outcome: outcome, Summary: "fertig"})
}

// apply wendet ein Ereignis an und scheitert, wenn es einen Fehler gibt.
func (w *tasksWorld) apply(ev domain.RunEvent) {
	w.t.Helper()
	if err := w.tasks.ApplyEvent(tasksCtx(w.t), ev); err != nil {
		w.t.Fatalf("ApplyEvent %s: %v", ev.Envelope.Type, err)
	}
}

// ---- Lesen ------------------------------------------------------------------------------------------------------

func (w *tasksWorld) order(id string) persistence.WorkOrderRecord {
	w.t.Helper()
	var o persistence.WorkOrderRecord
	tasksView(w.t, w.store, func(r *persistence.ReadTx) error {
		var err error
		o, err = r.WorkOrder(tasksProject, id)
		return err
	})
	return o
}

func (w *tasksWorld) agent(id string) domain.Agent {
	w.t.Helper()
	var a domain.Agent
	tasksView(w.t, w.store, func(r *persistence.ReadTx) error {
		var err error
		a, err = r.Agent(tasksProject, id)
		return err
	})
	return a
}

func (w *tasksWorld) attempt(execID string, n int) domain.ExecutionAttempt {
	w.t.Helper()
	var a domain.ExecutionAttempt
	tasksView(w.t, w.store, func(r *persistence.ReadTx) error {
		var err error
		a, err = r.Attempt(tasksProject, execID, n)
		return err
	})
	return a
}

func (w *tasksWorld) leases(order string) []domain.ExecutionLease {
	w.t.Helper()
	var ls []domain.ExecutionLease
	tasksView(w.t, w.store, func(r *persistence.ReadTx) error {
		var err error
		ls, err = r.Leases(tasksProject, order)
		return err
	})
	return ls
}

func (w *tasksWorld) executions(order string) []domain.Execution {
	w.t.Helper()
	var es []domain.Execution
	tasksView(w.t, w.store, func(r *persistence.ReadTx) error {
		var err error
		es, err = r.Executions(tasksProject, order)
		return err
	})
	return es
}

func (w *tasksWorld) revision() int64 {
	w.t.Helper()
	var rev int64
	tasksView(w.t, w.store, func(r *persistence.ReadTx) error {
		var err error
		rev, err = r.Revision(tasksProject)
		return err
	})
	return rev
}

func (w *tasksWorld) events(kind string) []domain.AuditEvent {
	w.t.Helper()
	var out []domain.AuditEvent
	tasksView(w.t, w.store, func(r *persistence.ReadTx) error {
		evs, err := r.Events(tasksProject, persistence.EventFilter{})
		for _, e := range evs {
			if kind == "" || e.Kind == kind {
				out = append(out, e)
			}
		}
		return err
	})
	return out
}

// dump hält den gesamten führenden Zustand als Text fest: Revision, Ereignisanzahl, Aufträge (Zustand, Zeilenrevision), Agenten,
// Ausführungen mit Versuchen, Leases und Fragen. Zwei gleiche Dumps heißen: nichts hat sich verändert.
func (w *tasksWorld) dump() string {
	w.t.Helper()
	var b strings.Builder
	tasksView(w.t, w.store, func(r *persistence.ReadTx) error {
		rev, err := r.Revision(tasksProject)
		if err != nil {
			return err
		}
		evs, err := r.Events(tasksProject, persistence.EventFilter{})
		if err != nil {
			return err
		}
		fmt.Fprintf(&b, "revision=%d events=%d\n", rev, len(evs))
		orders, err := r.WorkOrders(tasksProject)
		if err != nil {
			return err
		}
		for _, o := range orders {
			fmt.Fprintf(&b, "order %s %s rev=%d\n", o.WorkOrderID, o.State, o.Revision)
			ls, err := r.Leases(tasksProject, o.WorkOrderID)
			if err != nil {
				return err
			}
			for _, l := range ls {
				fmt.Fprintf(&b, "  lease %s gen=%d %s %s#%d agent=%s released=%v\n", l.LeaseID, l.Generation, l.State, l.ExecutionID, l.Attempt, l.AgentID, l.ReleasedAt)
			}
		}
		agents, err := r.Agents(tasksProject)
		if err != nil {
			return err
		}
		for _, a := range agents {
			fmt.Fprintf(&b, "agent %s %s rev=%d\n", a.AgentID, a.Availability, a.Revision)
		}
		execs, err := r.Executions(tasksProject, "")
		if err != nil {
			return err
		}
		for _, e := range execs {
			fmt.Fprintf(&b, "exec %s %s order=%s agent=%s\n", e.ExecutionID, e.Role, e.WorkOrderID, e.AgentID)
			as, err := r.Attempts(tasksProject, e.ExecutionID)
			if err != nil {
				return err
			}
			for _, a := range as {
				term := ""
				if a.Terminal != nil {
					term = fmt.Sprintf("%s/%d", a.Terminal.Source, a.Terminal.ExitCode)
				}
				fmt.Fprintf(&b, "  attempt %d %s session=%q terminal=%s outcome=%s ended=%v\n", a.Attempt, a.State, a.SessionID, term, a.Outcome, a.EndedAt)
			}
		}
		qs, err := r.Questions(tasksProject)
		if err != nil {
			return err
		}
		for _, q := range qs {
			fmt.Fprintf(&b, "question %s %s\n", q.QuestionID, q.State)
		}
		return nil
	})
	return b.String()
}

// tasksWantClass verlangt einen TaskError der Klasse (eine von mehreren).
func tasksWantClass(t *testing.T, err error, want ...TaskClass) {
	t.Helper()
	if err == nil {
		t.Fatalf("kein Fehler, erwartet Klasse %v", want)
	}
	got := TaskClassOf(err)
	for _, c := range want {
		if got == c {
			return
		}
	}
	t.Fatalf("Fehlerklasse %q, erwartet %v: %v", got, want, err)
}

// tasksUnchanged verlangt, dass fn den Zustand nicht verändert (Zeilen, Ereignisse, Revision) und liefert dessen Fehler.
func (w *tasksWorld) unchanged(fn func() error) error {
	w.t.Helper()
	before := w.dump()
	err := fn()
	if after := w.dump(); after != before {
		w.t.Errorf("der Zustand hat sich verändert:\n--- vorher\n%s--- nachher\n%s", before, after)
	}
	return err
}

// tasksAgents legt weitere Implementierer-Agenten an.
func (w *tasksWorld) addAgents(role domain.Role, ids ...string) {
	w.t.Helper()
	tasksUpdate(w.t, w.store, func(tx *persistence.Tx) error {
		for _, id := range ids {
			if _, err := tx.CreateAgent(tasksProject, id, role); err != nil {
				return err
			}
		}
		return nil
	})
}

// tasksOrdersIn legt weitere Aufträge im Ziel ms-1 an.
func (w *tasksWorld) addOrders(ids ...string) {
	w.t.Helper()
	tasksUpdate(w.t, w.store, func(tx *persistence.Tx) error {
		for _, id := range ids {
			if _, err := tx.CreateWorkOrder(tasksOrder(w.t, id, "ms-1", supervisor.OrderImplementation)); err != nil {
				return err
			}
		}
		return nil
	})
}
