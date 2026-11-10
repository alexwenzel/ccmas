package orchestrator

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alexwenzel/ccmas/internal/domain"
	"github.com/alexwenzel/ccmas/internal/persistence"
	"github.com/alexwenzel/ccmas/internal/profiles"
	"github.com/alexwenzel/ccmas/internal/supervisor"
)

// ---- Gleichzeitige Reservierung ---------------------------------------------------------------------------------

// TestConcurrentReservation: 20 Goroutinen über zwei Store-Instanzen auf derselben Datei reservieren denselben Auftrag. Es
// entsteht genau eine Lease, ein Versuch und ein Auftragsübergang; alle anderen bekommen einen stabilen Fehler.
func TestConcurrentReservation(t *testing.T) {
	w := newTasksWorld(t)
	w.plan("batch-1", w.impl("dec-1", "wo-1"))
	other := w.newTasks(w.secondStore())
	revBefore, orderRev := w.revision(), w.order("wo-1").Revision

	req := ReserveTaskRequest{ProjectID: tasksProject, WorkOrderID: "wo-1", ExpectedRevision: orderRev, BatchID: "batch-1", DecisionID: "dec-1"}
	const n = 20
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		winners []domain.ExecutionLease
		errs    []error
	)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tk := w.tasks
			if i%2 == 1 {
				tk = other
			}
			<-start
			l, err := tk.ReserveTask(tasksCtx(t), req)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, err)
			} else {
				winners = append(winners, l)
			}
		}()
	}
	close(start)
	wg.Wait()

	if len(winners) != 1 || len(errs) != n-1 {
		t.Fatalf("%d Gewinner und %d Fehler bei %d Reservierungen, erwartet 1 und %d (Fehler: %v)", len(winners), len(errs), n, n-1, errs)
	}
	for _, err := range errs {
		tasksWantClass(t, err, TaskConflict, TaskRevision, TaskStale)
	}
	ls := w.leases("wo-1")
	if len(ls) != 1 || ls[0].LeaseID != winners[0].LeaseID || ls[0].Generation != 1 || ls[0].State != domain.LeaseActive {
		t.Fatalf("Leases = %+v, Gewinner %+v", ls, winners[0])
	}
	if es := w.executions("wo-1"); len(es) != 1 || es[0].ExecutionID != winners[0].ExecutionID {
		t.Errorf("Ausführungen = %+v", es)
	}
	if a := w.attempt(winners[0].ExecutionID, 1); a.State != domain.ExecInitializing {
		t.Errorf("Versuch = %+v", a)
	}
	if got := w.revision(); got != revBefore+1 {
		t.Errorf("Revision %d -> %d, erwartet genau +1", revBefore, got)
	}
	var moves int
	for _, e := range w.events(domain.AuditWorkOrderStateChanged) {
		if e.EntityID == "wo-1" {
			moves++
		}
	}
	if moves != 1 {
		t.Errorf("%d Auftragsübergänge, erwartet 1", moves)
	}
	if got := len(w.events(domain.AuditLeaseAcquired)); got != 1 {
		t.Errorf("%d Ereignisse lease.acquired, erwartet 1", got)
	}
}

// TestConcurrentReservationDistinctPlans: Konkurrieren 20 verschiedene, auf derselben Revision geplante Batches um denselben
// Auftrag, gewinnt genau einer; die übrigen sind durch die Revision veraltet.
func TestConcurrentReservationDistinctPlans(t *testing.T) {
	w := newTasksWorld(t)
	other := w.newTasks(w.secondStore())
	const n = 20
	for i := 0; i < n; i++ {
		w.plan(batchName(i), w.impl("dec-1", "wo-1"))
	}
	revBefore := w.revision()
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		okN  int
		errs []error
	)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tk := w.tasks
			if i%2 == 1 {
				tk = other
			}
			<-start
			_, err := tk.ReserveBatch(tasksCtx(t), tasksProject, batchName(i))
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, err)
			} else {
				okN++
			}
		}()
	}
	close(start)
	wg.Wait()
	if okN != 1 {
		t.Fatalf("%d erfolgreiche Reservierungen, erwartet 1 (Fehler: %v)", okN, errs)
	}
	for _, err := range errs {
		tasksWantClass(t, err, TaskRevision, TaskConflict, TaskStale)
	}
	if ls := w.leases("wo-1"); len(ls) != 1 {
		t.Errorf("Leases = %+v", ls)
	}
	if got := w.revision(); got != revBefore+1 {
		t.Errorf("Revision %d -> %d, erwartet genau +1", revBefore, got)
	}
}

func batchName(i int) string { return "batch-" + string(rune('a'+i/10)) + string(rune('a'+i%10)) }

// ---- Abhängigkeiten ---------------------------------------------------------------------------------------------

// TestDependencyGate: Eine Abhängigkeit gibt nur frei, wenn sie abgeschlossen ist. blocked, cancelled und ein Auftrag mit
// kontrolliert gescheitertem oder abgestürztem Versuch geben nichts frei. Das lehnen der Validator und, unabhängig davon, der
// eigene Schutz der Reservierung ab.
func TestDependencyGate(t *testing.T) {
	implFixture := tasksFixture(t, "agent-profile--implementer.json", domain.ParseAgentProfile)
	cases := map[string]func(t *testing.T, w *tasksWorld){
		"blocked": func(t *testing.T, w *tasksWorld) {
			tasksUpdate(t, w.store, func(tx *persistence.Tx) error {
				return tx.TransitionWorkOrder(tasksProject, "dep", domain.WOWaiting, domain.WOBlocked)
			})
		},
		"cancelled": func(t *testing.T, w *tasksWorld) {
			tasksUpdate(t, w.store, func(tx *persistence.Tx) error {
				return tx.TransitionWorkOrder(tasksProject, "dep", domain.WOWaiting, domain.WOCancelled)
			})
		},
		"in_progress mit kontrolliert gescheitertem Versuch": func(t *testing.T, w *tasksWorld) {
			tasksUpdate(t, w.store, func(tx *persistence.Tx) error {
				if err := tx.TransitionWorkOrder(tasksProject, "dep", domain.WOWaiting, domain.WOInProgress); err != nil {
					return err
				}
				if err := tasksStartExecution(tx, t, "exec-dep", "dep", "agent-1", domain.RoleImplementer, implFixture, tasksEarly); err != nil {
					return err
				}
				return tx.FinishAttempt(tasksProject, "exec-dep", 1, domain.ExecRunning, persistence.AttemptResult{
					Terminal: domain.TerminalResult{Attempt: 1, State: domain.ExecControlledFailure, Source: domain.TerminalFromError}, Summary: "gescheitert"})
			})
		},
		"in_progress mit abgestürztem Versuch": func(t *testing.T, w *tasksWorld) {
			tasksUpdate(t, w.store, func(tx *persistence.Tx) error {
				if err := tx.TransitionWorkOrder(tasksProject, "dep", domain.WOWaiting, domain.WOInProgress); err != nil {
					return err
				}
				if err := tasksStartExecution(tx, t, "exec-dep", "dep", "agent-1", domain.RoleImplementer, implFixture, tasksEarly); err != nil {
					return err
				}
				return tx.FinishAttempt(tasksProject, "exec-dep", 1, domain.ExecRunning, persistence.AttemptResult{
					Terminal: domain.TerminalResult{Attempt: 1, State: domain.ExecCrashed, Source: domain.TerminalFromProcessExit, ExitCode: 1}, Summary: "abgestürzt"})
			})
		},
		"in_progress ohne Versuch": func(t *testing.T, w *tasksWorld) {
			tasksUpdate(t, w.store, func(tx *persistence.Tx) error {
				return tx.TransitionWorkOrder(tasksProject, "dep", domain.WOWaiting, domain.WOInProgress)
			})
		},
		"validation": func(t *testing.T, w *tasksWorld) {
			tasksUpdate(t, w.store, func(tx *persistence.Tx) error {
				for _, to := range []domain.WorkOrderState{domain.WOInProgress, domain.WOValidation} {
					if err := tx.TransitionWorkOrder(tasksProject, "dep", map[domain.WorkOrderState]domain.WorkOrderState{
						domain.WOInProgress: domain.WOWaiting, domain.WOValidation: domain.WOInProgress}[to], to); err != nil {
						return err
					}
				}
				return nil
			})
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			w := newTasksWorld(t)
			tasksUpdate(t, w.store, func(tx *persistence.Tx) error {
				if _, err := tx.CreateWorkOrder(tasksOrder(t, "dep", "ms-1", supervisor.OrderImplementation)); err != nil {
					return err
				}
				_, err := tx.CreateWorkOrder(tasksOrder(t, "d-1", "ms-1", supervisor.OrderImplementation, "dep"))
				return err
			})
			setup(t, w)

			// Der Validator lehnt die Umsetzung des Abhängigen ab.
			snap := w.snapshot()
			dec := tasksImplementation(t, snap, "dec-1", "d-1")
			impl := w.profileFor(dec)
			batch := tasksBatch(snap, dec)
			_, err := supervisor.Validator{}.Accept(batch, snap, w.pol)
			re, isRej := supervisor.AsRejection(err)
			if !isRej || !re.Has(supervisor.CodePreconditionUnmet) {
				t.Fatalf("Validator: %v", err)
			}

			// Ein (gefälschter) angenommener Plan reserviert dennoch nichts: ReserveBatch prüft den Batch erneut.
			profs := map[string]domain.AgentProfile{"dec-1": impl}
			w.record(persistence.PlanRecord{Batch: batch, Verdict: supervisor.VerdictAccepted, Delegations: profs,
				PlanningSnapshotSHA256: tasksSum(snap), CurrentSnapshotSHA256: tasksSum(snap)})
			err = w.unchanged(func() error {
				_, err := w.tasks.ReserveBatch(tasksCtx(t), tasksProject, "batch-v")
				return err
			})
			tasksWantClass(t, err, TaskRejected)
			var te *TaskError
			if !errors.As(err, &te) || len(te.Rejections) == 0 {
				t.Errorf("keine strukturierten Ablehnungen: %v", err)
			}

			// Der eigene Schutz der Reservierung greift auch ohne Validator.
			err = w.unchanged(func() error {
				_, err := w.reserveDirect("batch-v", "dec-1", "d-1", impl, nil, "")
				return err
			})
			tasksWantClass(t, err, TaskDependency)
		})
	}

	t.Run("abgeschlossen gibt frei", func(t *testing.T) {
		w := newTasksWorld(t)
		tasksUpdate(t, w.store, func(tx *persistence.Tx) error {
			if _, err := tx.CreateWorkOrder(tasksOrder(t, "dep", "ms-1", supervisor.OrderImplementation)); err != nil {
				return err
			}
			if _, err := tx.CreateWorkOrder(tasksOrder(t, "d-1", "ms-1", supervisor.OrderImplementation, "dep")); err != nil {
				return err
			}
			return tasksComplete(tx, "dep")
		})
		l := w.reserve("batch-ok", "d-1")
		if l.WorkOrderID != "d-1" || l.Generation != 1 {
			t.Errorf("Lease = %+v", l)
		}
	})
}

// reserveDirect ruft die Reservierung einer Delegation in einer eigenen Transaktion unter Umgehung des Validators auf (gegen
// einen gespeicherten angenommenen Plan).
func (w *tasksWorld) reserveDirect(batchID, decisionID, orderID string, prof domain.AgentProfile, expected *int64, agentID string) (domain.ExecutionLease, error) {
	w.t.Helper()
	var l domain.ExecutionLease
	_, err := w.store.Update(tasksCtx(w.t), "orchestrator", func(tx *persistence.Tx) error {
		sc := taskScope{projectID: tasksProject, goalID: "ms-1", batchID: batchID, at: w.clock.Now(), expires: w.clock.Now().Add(time.Hour)}
		var err error
		l, err = w.tasks.reserveOne(tx, sc, taskDelegation{decisionID: decisionID, orderID: orderID, profile: prof}, taskPick{agentID: agentID, expectedRevision: expected})
		return err
	})
	return l, err
}

// ---- Batch ------------------------------------------------------------------------------------------------------

// TestReserveBatchAtomic: Ein Batch reserviert alle Delegationen oder keine.
func TestReserveBatchAtomic(t *testing.T) {
	t.Run("beide, wenn zwei Agenten verfügbar sind", func(t *testing.T) {
		w := newTasksWorld(t)
		w.addAgents(domain.RoleImplementer, "agent-2")
		w.plan("batch-1", w.impl("dec-1", "wo-1"), w.impl("dec-2", "wo-2"))
		revBefore := w.revision()
		ls, err := w.tasks.ReserveBatch(tasksCtx(t), tasksProject, "batch-1")
		if err != nil || len(ls) != 2 {
			t.Fatalf("ReserveBatch: %v %v", ls, err)
		}
		if ls[0].WorkOrderID != "wo-1" || ls[0].AgentID != "agent-1" || ls[1].WorkOrderID != "wo-2" || ls[1].AgentID != "agent-2" {
			t.Errorf("Zuordnung (Reihenfolge des Batches, Agenten aufsteigend): %+v", ls)
		}
		if got := w.revision(); got != revBefore+1 {
			t.Errorf("Revision %d -> %d, erwartet genau +1 für den ganzen Batch", revBefore, got)
		}
		for _, id := range []string{"wo-1", "wo-2"} {
			if o := w.order(id); o.State != domain.WOInProgress {
				t.Errorf("%s ist %s", id, o.State)
			}
		}
		for _, id := range []string{"agent-1", "agent-2"} {
			if a := w.agent(id); a.Availability != domain.AgentReserved {
				t.Errorf("%s ist %s", id, a.Availability)
			}
		}
	})

	t.Run("keine, wenn der zweite Agent fehlt", func(t *testing.T) {
		w := newTasksWorld(t)
		w.plan("batch-1", w.impl("dec-1", "wo-1"), w.impl("dec-2", "wo-2"))
		err := w.unchanged(func() error {
			_, err := w.tasks.ReserveBatch(tasksCtx(t), tasksProject, "batch-1")
			return err
		})
		tasksWantClass(t, err, TaskUnavailable)
		// Mit dem zweiten Agenten gelingt derselbe Batch (keine Spur des ersten Versuchs).
		w.addAgents(domain.RoleImplementer, "agent-2")
		w.plan("batch-2", w.impl("dec-1", "wo-1"), w.impl("dec-2", "wo-2"))
		if ls, err := w.tasks.ReserveBatch(tasksCtx(t), tasksProject, "batch-2"); err != nil || len(ls) != 2 {
			t.Fatalf("zweiter Versuch: %v %v", ls, err)
		}
	})

	t.Run("veraltete Revision", func(t *testing.T) {
		w := newTasksWorld(t)
		w.plan("batch-1", w.impl("dec-1", "wo-1"))
		tasksUpdate(t, w.store, func(tx *persistence.Tx) error { // irgendeine Planungsänderung
			return tx.SetRepairRounds(tasksProject, "wo-2", 1, 0)
		})
		err := w.unchanged(func() error {
			_, err := w.tasks.ReserveBatch(tasksCtx(t), tasksProject, "batch-1")
			return err
		})
		tasksWantClass(t, err, TaskRevision)
	})

	t.Run("abgelehnter Plan", func(t *testing.T) {
		w := newTasksWorld(t)
		snap := w.snapshot()
		batch := tasksBatch(snap, tasksImplementation(t, snap, "dec-1", "wo-1"))
		batch.BatchID = "batch-rej"
		rej := []supervisor.Rejection{{Code: supervisor.CodePreconditionUnmet, DecisionID: "dec-1", Reason: "abgelehnt"}}
		w.record(persistence.PlanRecord{Batch: batch, Verdict: supervisor.VerdictRejected, Rejections: rej,
			PlanningSnapshotSHA256: tasksSum(snap), CurrentSnapshotSHA256: tasksSum(snap)})
		err := w.unchanged(func() error {
			_, err := w.tasks.ReserveBatch(tasksCtx(t), tasksProject, "batch-rej")
			return err
		})
		tasksWantClass(t, err, TaskRejected)
		var te *TaskError
		if !errors.As(err, &te) || len(te.Rejections) != 1 || te.Rejections[0].Code != supervisor.CodePreconditionUnmet {
			t.Errorf("Ablehnungen des Plans nicht übernommen: %v", err)
		}
	})

	t.Run("erneute Validierung lehnt ab", func(t *testing.T) {
		w := newTasksWorld(t)
		snap := w.snapshot()
		// Ein auf der aktuellen Revision angenommen gespeicherter Plan für einen Auftrag, den der Validator nicht starten lässt.
		tasksUpdate(t, w.store, func(tx *persistence.Tx) error {
			return tx.TransitionWorkOrder(tasksProject, "wo-1", domain.WOWaiting, domain.WOBlocked)
		})
		snap = w.snapshot()
		dec := tasksImplementation(t, snap, "dec-1", "wo-1")
		batch := tasksBatch(snap, dec)
		batch.BatchID = "batch-forged"
		w.record(persistence.PlanRecord{Batch: batch, Verdict: supervisor.VerdictAccepted, Delegations: map[string]domain.AgentProfile{"dec-1": w.profileFor(dec)},
			PlanningSnapshotSHA256: tasksSum(snap), CurrentSnapshotSHA256: tasksSum(snap)})
		err := w.unchanged(func() error {
			_, err := w.tasks.ReserveBatch(tasksCtx(t), tasksProject, "batch-forged")
			return err
		})
		tasksWantClass(t, err, TaskRejected)
		var te *TaskError
		if !errors.As(err, &te) || len(te.Rejections) == 0 {
			t.Errorf("keine strukturierten Ablehnungen: %v", err)
		}
	})

	t.Run("geändertes Profil", func(t *testing.T) {
		w := newTasksWorld(t)
		snap := w.snapshot()
		batch := tasksBatch(snap, tasksImplementation(t, snap, "dec-1", "wo-1"))
		batch.BatchID = "batch-prof"
		vb, err := supervisor.Validator{}.Accept(batch, snap, w.pol)
		if err != nil {
			t.Fatal(err)
		}
		changed := vb.Decisions[0].Delegation.Profile
		changed.Version++ // die Konfiguration hat sich nach der Planung geändert
		w.record(persistence.PlanRecord{Batch: batch, Verdict: supervisor.VerdictAccepted, Delegations: map[string]domain.AgentProfile{"dec-1": changed},
			PlanningSnapshotSHA256: tasksSum(snap), CurrentSnapshotSHA256: tasksSum(snap)})
		err = w.unchanged(func() error {
			_, err := w.tasks.ReserveBatch(tasksCtx(t), tasksProject, "batch-prof")
			return err
		})
		tasksWantClass(t, err, TaskConflict)
		if !strings.Contains(err.Error(), "Profil") {
			t.Errorf("Meldung nennt das Profil nicht: %v", err)
		}
	})

	t.Run("dieselbe Entscheidung zweimal", func(t *testing.T) {
		w := newTasksWorld(t)
		w.plan("batch-1", w.impl("dec-1", "wo-1"))
		if _, err := w.tasks.ReserveBatch(tasksCtx(t), tasksProject, "batch-1"); err != nil {
			t.Fatal(err)
		}
		err := w.unchanged(func() error {
			_, err := w.tasks.ReserveBatch(tasksCtx(t), tasksProject, "batch-1")
			return err
		})
		tasksWantClass(t, err, TaskConflict)
		if n := len(w.leases("wo-1")); n != 1 {
			t.Errorf("%d Leases", n)
		}
	})

	t.Run("unbekannter Batch", func(t *testing.T) {
		w := newTasksWorld(t)
		err := w.unchanged(func() error {
			_, err := w.tasks.ReserveBatch(tasksCtx(t), tasksProject, "batch-404")
			return err
		})
		tasksWantClass(t, err, TaskNotFound)
	})

	t.Run("Aktionen ohne Delegation werden nicht ausgeführt", func(t *testing.T) {
		w := newTasksWorld(t)
		wait := domain.Decision{SchemaVersion: 1, DecisionID: "dec-w", ProjectID: tasksProject, StateRevision: w.revision(),
			Target: domain.DecisionTarget{Kind: domain.KindWorkOrder, ID: "wo-2"}, Action: domain.ActionWait, Preconditions: []domain.Precondition{},
			Rationale: "Es gibt nichts zu tun."}
		w.plan("batch-1", w.impl("dec-1", "wo-1"), wait)
		ls, err := w.tasks.ReserveBatch(tasksCtx(t), tasksProject, "batch-1")
		if err != nil || len(ls) != 1 || ls[0].WorkOrderID != "wo-1" {
			t.Fatalf("ReserveBatch: %v %v", ls, err)
		}
		if n := len(w.leases("wo-2")) + len(w.executions("wo-2")); n != 0 {
			t.Errorf("für wo-2 (wait) entstand etwas: %d", n)
		}
	})
}

// TestReserveCapacity: Der Supervisor belegt keinen Platz, Reviewer schon. Passt ein Batch nicht hinein, ändert sich nichts; der
// eigene Schutz der Reservierung greift unabhängig vom Validator.
func TestReserveCapacity(t *testing.T) {
	supProfile := tasksFixture(t, "agent-profile--supervisor_xhigh.json", domain.ParseAgentProfile)
	revProfile := tasksFixture(t, "agent-profile--effort_fallback.json", domain.ParseAgentProfile)
	for _, max := range []int{1, 2, 3, 16} {
		t.Run(itoaT(max), func(t *testing.T) {
			w := newTasksWorld(t)
			cfg := tasksFixture(t, "project-config--full.json", domain.ParseProjectConfig)
			cfg.MaxWorkers = max
			var ids, agents []string
			for i := 0; i < max+1; i++ {
				ids = append(ids, "cap-"+itoaT(100+i))
				agents = append(agents, "ca-"+itoaT(100+i))
			}
			w.addOrders(ids...)
			w.addAgents(domain.RoleImplementer, agents...)
			tasksUpdate(t, w.store, func(tx *persistence.Tx) error {
				if err := tx.UpdateProjectConfig(cfg); err != nil {
					return err
				}
				if err := tx.RecordProfile(tasksProject, supProfile); err != nil {
					return err
				}
				// Der Supervisor läuft (ohne Auftrag) und belegt keinen Platz.
				if err := tx.CreateExecution(domain.Execution{ExecutionID: "exec-sup", ProjectID: tasksProject, Role: domain.RoleSupervisor, CreatedAt: tasksEarly}); err != nil {
					return err
				}
				if err := tx.StartAttempt(tasksProject, domain.ExecutionAttempt{ExecutionID: "exec-sup", Attempt: 1, ProfileID: supProfile.ProfileID,
					ProfileVersion: supProfile.Version, State: domain.ExecInitializing, StartedAt: tasksEarly}); err != nil {
					return err
				}
				if max == 1 {
					return nil
				}
				// Ein laufender Reviewer belegt einen Platz.
				return tasksStartExecution(tx, t, "exec-rev", "wo-2", "rev-1", domain.RoleReviewer, revProfile, tasksEarly)
			})
			free := max
			if max > 1 {
				free = max - 1
			}

			// Ein Platz zu viel: abgelehnt, nichts ändert sich.
			var over []domain.Decision
			for i := 0; i <= free; i++ {
				over = append(over, w.impl("dec-"+itoaT(i), ids[i]))
			}
			snap := w.snapshot()
			batch := tasksBatch(snap, over...)
			batch.BatchID = "batch-over"
			_, err := supervisor.Validator{}.Accept(batch, snap, w.pol)
			re, isRej := supervisor.AsRejection(err)
			if !isRej || !re.Has(supervisor.CodeCapacityExceeded) {
				t.Fatalf("Validator: %v", err)
			}
			profs := map[string]domain.AgentProfile{}
			for _, d := range over {
				profs[d.DecisionID] = w.profileFor(d)
			}
			w.record(persistence.PlanRecord{Batch: batch, Verdict: supervisor.VerdictAccepted, Delegations: profs,
				PlanningSnapshotSHA256: tasksSum(snap), CurrentSnapshotSHA256: tasksSum(snap)})
			err = w.unchanged(func() error {
				_, err := w.tasks.ReserveBatch(tasksCtx(t), tasksProject, "batch-over")
				return err
			})
			tasksWantClass(t, err, TaskRejected)

			// Genau so viele wie Plätze frei sind: alle werden reserviert.
			fit := make([]domain.Decision, 0, free)
			for i := 0; i < free; i++ {
				fit = append(fit, w.impl("dec-"+itoaT(i), ids[i]))
			}
			w.plan("batch-fit", fit...)
			ls, err := w.tasks.ReserveBatch(tasksCtx(t), tasksProject, "batch-fit")
			if err != nil || len(ls) != free {
				t.Fatalf("ReserveBatch(%d): %v %v", free, ls, err)
			}

			// Der Schutz der Reservierung selbst: ein weiterer Platz ist nicht frei (gespeicherter Plan, Validator umgangen).
			extra := tasksImplementation(t, w.snapshot(), "dec-x", ids[free])
			implProfile := w.profileFor(extra)
			single := tasksBatch(w.snapshot(), extra)
			single.BatchID = "batch-extra"
			w.record(persistence.PlanRecord{Batch: single, Verdict: supervisor.VerdictAccepted, Delegations: map[string]domain.AgentProfile{"dec-x": implProfile},
				PlanningSnapshotSHA256: tasksSum(single), CurrentSnapshotSHA256: tasksSum(single)})
			err = w.unchanged(func() error {
				_, err := w.reserveDirect("batch-extra", "dec-x", ids[free], implProfile, nil, "")
				return err
			})
			tasksWantClass(t, err, TaskCapacity)
		})
	}
}

func itoaT(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}

// ---- Agentenwahl und Einzelreservierung -------------------------------------------------------------------------

func TestReserveAgentChoice(t *testing.T) {
	t.Run("erster verfügbarer Agent der Rolle in ID-Reihenfolge", func(t *testing.T) {
		w := newTasksWorld(t)
		w.addAgents(domain.RoleImplementer, "agent-2", "agent-0")
		tasksUpdate(t, w.store, func(tx *persistence.Tx) error {
			return tx.SetAgentAvailability(tasksProject, "agent-0", domain.AgentAvailable, domain.AgentUnavailable)
		})
		l := w.reserve("batch-1", "wo-1")
		if l.AgentID != "agent-1" { // agent-0 ist nicht verfügbar, agent-1 kommt vor agent-2; der Reviewer rev-1 passt nicht zur Rolle
			t.Errorf("Agent = %s", l.AgentID)
		}
	})

	t.Run("Reservierte Agenten sind nicht verfügbar", func(t *testing.T) {
		w := newTasksWorld(t)
		w.reserve("batch-1", "wo-1")
		w.plan("batch-2", w.impl("dec-1", "wo-2"))
		err := w.unchanged(func() error {
			_, err := w.tasks.ReserveBatch(tasksCtx(t), tasksProject, "batch-2")
			return err
		})
		tasksWantClass(t, err, TaskUnavailable)
	})

	t.Run("profile_id nennt den Agenten", func(t *testing.T) {
		pol, offers := tasksPolicyWith(t, map[string]profiles.Agent{"agent-2": {Role: domain.RoleImplementer}}, map[domain.Role]string{domain.RoleImplementer: "agent-2"})
		w := newTasksWorldWith(t, pol, offers)
		w.addAgents(domain.RoleImplementer, "agent-2")
		l := w.reserve("batch-1", "wo-1")
		if l.AgentID != "agent-2" {
			t.Fatalf("Agent = %s, erwartet der genannte agent-2 (agent-1 wäre der erste verfügbare)", l.AgentID)
		}
		a := w.attempt(l.ExecutionID, 1)
		if a.ProfileID != "implementer:agent-2" {
			t.Errorf("Profil = %s", a.ProfileID)
		}
	})

	t.Run("profile_id nennt einen unverfügbaren Agenten", func(t *testing.T) {
		pol, offers := tasksPolicyWith(t, map[string]profiles.Agent{"agent-2": {Role: domain.RoleImplementer}}, map[domain.Role]string{domain.RoleImplementer: "agent-2"})
		w := newTasksWorldWith(t, pol, offers)
		w.addAgents(domain.RoleImplementer, "agent-2")
		tasksUpdate(t, w.store, func(tx *persistence.Tx) error {
			return tx.SetAgentAvailability(tasksProject, "agent-2", domain.AgentAvailable, domain.AgentUnavailable)
		})
		w.plan("batch-1", w.impl("dec-1", "wo-1"))
		err := w.unchanged(func() error { // kein Ausweichen auf agent-1
			_, err := w.tasks.ReserveBatch(tasksCtx(t), tasksProject, "batch-1")
			return err
		})
		tasksWantClass(t, err, TaskUnavailable)
	})

	t.Run("ReserveTask mit genanntem Agenten", func(t *testing.T) {
		w := newTasksWorld(t)
		w.addAgents(domain.RoleImplementer, "agent-2")
		w.plan("batch-1", w.impl("dec-1", "wo-1"))
		l, err := w.tasks.ReserveTask(tasksCtx(t), ReserveTaskRequest{ProjectID: tasksProject, WorkOrderID: "wo-1", AgentID: "agent-2",
			ExpectedRevision: w.order("wo-1").Revision, BatchID: "batch-1", DecisionID: "dec-1"})
		if err != nil || l.AgentID != "agent-2" {
			t.Fatalf("ReserveTask: %+v %v", l, err)
		}
		if a := w.agent("agent-1"); a.Availability != domain.AgentAvailable {
			t.Errorf("agent-1 ist %s", a.Availability)
		}
	})

	for name, tc := range map[string]struct {
		agent string
		want  TaskClass
	}{
		"Agent einer anderen Rolle": {"rev-1", TaskState},
		"unbekannter Agent":         {"agent-404", TaskNotFound},
	} {
		t.Run(name, func(t *testing.T) {
			w := newTasksWorld(t)
			w.plan("batch-1", w.impl("dec-1", "wo-1"))
			err := w.unchanged(func() error {
				_, err := w.tasks.ReserveTask(tasksCtx(t), ReserveTaskRequest{ProjectID: tasksProject, WorkOrderID: "wo-1", AgentID: tc.agent,
					ExpectedRevision: w.order("wo-1").Revision, BatchID: "batch-1", DecisionID: "dec-1"})
				return err
			})
			tasksWantClass(t, err, tc.want)
		})
	}

	t.Run("ReserveTask mit nicht verfügbarem Agenten", func(t *testing.T) {
		w := newTasksWorld(t)
		w.addAgents(domain.RoleImplementer, "agent-2")
		tasksUpdate(t, w.store, func(tx *persistence.Tx) error {
			return tx.SetAgentAvailability(tasksProject, "agent-2", domain.AgentAvailable, domain.AgentUnavailable)
		})
		w.plan("batch-1", w.impl("dec-1", "wo-1"))
		err := w.unchanged(func() error {
			_, err := w.tasks.ReserveTask(tasksCtx(t), ReserveTaskRequest{ProjectID: tasksProject, WorkOrderID: "wo-1", AgentID: "agent-2",
				ExpectedRevision: w.order("wo-1").Revision, BatchID: "batch-1", DecisionID: "dec-1"})
			return err
		})
		tasksWantClass(t, err, TaskUnavailable)
	})
}

func TestReserveTaskBindsToDecision(t *testing.T) {
	w := newTasksWorld(t)
	w.addAgents(domain.RoleImplementer, "agent-2")
	w.plan("batch-1", w.impl("dec-1", "wo-1"), w.impl("dec-2", "wo-2"))
	rev := w.order("wo-1").Revision
	req := ReserveTaskRequest{ProjectID: tasksProject, WorkOrderID: "wo-1", ExpectedRevision: rev, BatchID: "batch-1", DecisionID: "dec-1"}

	// Ein Batch mit mehreren Delegationen gehört zu ReserveBatch.
	err := w.unchanged(func() error { _, err := w.tasks.ReserveTask(tasksCtx(t), req); return err })
	tasksWantClass(t, err, TaskInvalid)
	if !strings.Contains(err.Error(), "ReserveBatch") {
		t.Errorf("Fehler verweist nicht auf ReserveBatch: %v", err)
	}

	w.plan("batch-2", w.impl("dec-1", "wo-1"))
	for name, mod := range map[string]func(r *ReserveTaskRequest){
		"falscher Auftrag":        func(r *ReserveTaskRequest) { r.WorkOrderID = "wo-2" },
		"unbekannte Entscheidung": func(r *ReserveTaskRequest) { r.DecisionID = "dec-404" },
		"veraltete Revision":      func(r *ReserveTaskRequest) { r.ExpectedRevision = rev + 7 },
		"Revision 0":              func(r *ReserveTaskRequest) { r.ExpectedRevision = 0 },
		"ID fehlt":                func(r *ReserveTaskRequest) { r.BatchID = "" },
	} {
		t.Run(name, func(t *testing.T) {
			r := req
			r.BatchID = "batch-2"
			mod(&r)
			err := w.unchanged(func() error { _, err := w.tasks.ReserveTask(tasksCtx(t), r); return err })
			tasksWantClass(t, err, TaskInvalid, TaskNotFound, TaskRevision, TaskConflict)
		})
	}

	req.BatchID = "batch-2"
	l, err := w.tasks.ReserveTask(tasksCtx(t), req)
	if err != nil || l.WorkOrderID != "wo-1" || l.ExecutionID == "" || l.Attempt != 1 {
		t.Fatalf("ReserveTask: %+v %v", l, err)
	}
	// Die Lease ist an die Entscheidung gebunden und an den Versuch gebunden.
	tasksView(t, w.store, func(r *persistence.ReadTx) error {
		got, err := r.DecisionLease(tasksProject, "batch-2", "dec-1")
		if err != nil || got == nil || got.LeaseID != l.LeaseID {
			t.Errorf("DecisionLease = %+v %v", got, err)
		}
		return nil
	})
}

// TestReserveCreatesEverythingInOneTransaction: Ausführung, Versuch 1, Lease, Auftragsübergang und Agent entstehen zusammen;
// die Revision steigt genau einmal; alles ist auditiert; die Lease gilt Laufzeit plus Zuschlag.
func TestReserveCreatesEverythingInOneTransaction(t *testing.T) {
	w := newTasksWorld(t)
	w.plan("batch-1", w.impl("dec-1", "wo-1"))
	revBefore, eventsBefore := w.revision(), len(w.events(""))
	before := w.clock.Now()
	ls, err := w.tasks.ReserveBatch(tasksCtx(t), tasksProject, "batch-1")
	if err != nil || len(ls) != 1 {
		t.Fatal(err)
	}
	l := ls[0]
	if err := l.Check(); err != nil || l.State != domain.LeaseActive || l.Generation != 1 || l.AgentID != "agent-1" || l.Attempt != 1 {
		t.Fatalf("Lease = %+v (%v)", l, err)
	}
	if got := w.revision(); got != revBefore+1 {
		t.Errorf("Revision %d -> %d", revBefore, got)
	}
	exec := w.executions("wo-1")
	if len(exec) != 1 || exec[0].Role != domain.RoleImplementer || exec[0].AgentID != "agent-1" || exec[0].ExecutionID != l.ExecutionID {
		t.Fatalf("Ausführungen = %+v", exec)
	}
	a := w.attempt(l.ExecutionID, 1)
	if a.State != domain.ExecInitializing || a.ProfileID != "implementer" || a.ProfileVersion < 1 {
		t.Errorf("Versuch = %+v", a)
	}
	tasksView(t, w.store, func(r *persistence.ReadTx) error { // die gespeicherte Profilversion ist die des Versuchs
		p, err := r.Profile(tasksProject, a.ProfileID, a.ProfileVersion)
		if err != nil || p.Role != domain.RoleImplementer {
			t.Errorf("Profil: %+v %v", p, err)
		}
		return nil
	})
	if o := w.order("wo-1"); o.State != domain.WOInProgress {
		t.Errorf("Auftrag = %s", o.State)
	}
	if a := w.agent("agent-1"); a.Availability != domain.AgentReserved {
		t.Errorf("Agent = %s", a.Availability)
	}
	want := []string{domain.AuditExecutionCreated, domain.AuditAttemptStarted, domain.AuditLeaseAcquired, domain.AuditWorkOrderStateChanged, domain.AuditAgentAvailability}
	have := map[string]int{}
	for _, e := range w.events("")[eventsBefore:] {
		have[e.Kind]++
		if e.Actor != "orchestrator" {
			t.Errorf("Ereignis %s: Akteur %s", e.Kind, e.Actor)
		}
		if slices.Contains(want, e.Kind) && e.StateRevision != revBefore+1 {
			t.Errorf("Ereignis %s trägt Revision %d, erwartet %d", e.Kind, e.StateRevision, revBefore+1)
		}
	}
	for _, k := range want {
		if have[k] != 1 {
			t.Errorf("Ereignis %s %d-mal (alle: %v)", k, have[k], have)
		}
	}
	// Gültigkeit: attempt_runtime_minutes (90 in der Fixture) plus Zuschlag.
	wantExpiry := l.AcquiredAt.Add(90*time.Minute + DefaultLeaseGrace)
	if !l.ExpiresAt.Equal(wantExpiry) || l.AcquiredAt.Before(before) {
		t.Errorf("Gültigkeit %v bis %v, erwartet bis %v", l.AcquiredAt, l.ExpiresAt, wantExpiry)
	}
}

// TestReserveGuards: weitere Schutzprüfungen der Reservierung (je ohne Änderung).
func TestReserveGuards(t *testing.T) {
	var impl domain.AgentProfile
	forge := func(w *tasksWorld, batchID, orderID string) {
		snap := w.snapshot()
		dec := tasksImplementation(t, snap, "dec-1", orderID)
		impl = w.profileFor(dec)
		batch := tasksBatch(snap, dec)
		batch.BatchID = batchID
		w.record(persistence.PlanRecord{Batch: batch, Verdict: supervisor.VerdictAccepted, Delegations: map[string]domain.AgentProfile{"dec-1": impl},
			PlanningSnapshotSHA256: tasksSum(snap), CurrentSnapshotSHA256: tasksSum(snap)})
	}

	t.Run("Auftrag außerhalb des beauftragten Umfangs", func(t *testing.T) {
		w := newTasksWorld(t)
		forge(w, "batch-far", "wo-1")
		// "far" gehört zu ms-2; der Validator kennt ihn nicht, der Schutz weist ihn trotzdem ab.
		err := w.unchanged(func() error {
			_, err := w.reserveDirect("batch-far", "dec-1", "far", impl, nil, "")
			return err
		})
		tasksWantClass(t, err, TaskScope, TaskInvalid)
	})

	for _, state := range []domain.WorkOrderState{domain.WOBlocked, domain.WOCancelled} {
		t.Run("Auftrag "+string(state), func(t *testing.T) {
			w := newTasksWorld(t)
			forge(w, "batch-s", "wo-1")
			tasksUpdate(t, w.store, func(tx *persistence.Tx) error {
				return tx.TransitionWorkOrder(tasksProject, "wo-1", domain.WOWaiting, state)
			})
			err := w.unchanged(func() error {
				_, err := w.reserveDirect("batch-s", "dec-1", "wo-1", impl, nil, "")
				return err
			})
			tasksWantClass(t, err, TaskState)
		})
	}

	t.Run("aktive Lease", func(t *testing.T) {
		w := newTasksWorld(t)
		w.reserve("batch-1", "wo-1")
		forge(w, "batch-2", "wo-1")
		err := w.unchanged(func() error {
			_, err := w.reserveDirect("batch-2", "dec-1", "wo-1", impl, nil, "")
			return err
		})
		tasksWantClass(t, err, TaskConflict)
	})

	t.Run("nicht beendeter Versuch ohne Lease", func(t *testing.T) {
		w := newTasksWorld(t)
		w.addAgents(domain.RoleImplementer, "agent-2")
		tasksUpdate(t, w.store, func(tx *persistence.Tx) error {
			return tasksStartExecution(tx, t, "exec-x", "wo-1", "agent-2", domain.RoleImplementer,
				tasksFixture(t, "agent-profile--implementer.json", domain.ParseAgentProfile), tasksEarly)
		})
		forge(w, "batch-2", "wo-1")
		err := w.unchanged(func() error {
			_, err := w.reserveDirect("batch-2", "dec-1", "wo-1", impl, nil, "")
			return err
		})
		tasksWantClass(t, err, TaskConflict)
	})

	t.Run("Profil ohne Rolle Supervisor", func(t *testing.T) {
		w := newTasksWorld(t)
		forge(w, "batch-2", "wo-1")
		sup := tasksFixture(t, "agent-profile--supervisor_xhigh.json", domain.ParseAgentProfile)
		err := w.unchanged(func() error {
			_, err := w.reserveDirect("batch-2", "dec-1", "wo-1", sup, nil, "")
			return err
		})
		tasksWantClass(t, err, TaskInvalid)
	})

	t.Run("Revision des Auftrags", func(t *testing.T) {
		w := newTasksWorld(t)
		forge(w, "batch-2", "wo-1")
		stale := w.order("wo-1").Revision + 1
		err := w.unchanged(func() error {
			_, err := w.reserveDirect("batch-2", "dec-1", "wo-1", impl, &stale, "")
			return err
		})
		tasksWantClass(t, err, TaskRevision)
	})
}

// ---- Revision 0 -------------------------------------------------------------------------------------------------

// TestReserveOnRevisionZero: Ein Projekt, das in derselben Transaktion angelegt und geändert wird, bleibt auf Revision 0. Ein
// Plan auf Revision 0 ist gültig, ReserveBatch arbeitet darauf ohne Sonderfall; die Reservierung setzt die Revision auf 1.
func TestReserveOnRevisionZero(t *testing.T) {
	pol, offers := tasksPolicy(t)
	s, clock, _ := tasksTestStoreAt(t)
	tk, err := NewTasks(s, pol, offers, TasksFixedGoal("ms-1"), "orchestrator", TasksPolicy{Now: clock.Now})
	if err != nil {
		t.Fatal(err)
	}
	res := tasksUpdate(t, s, func(tx *persistence.Tx) error {
		if _, err := tx.CreateProject(tasksFixture(t, "project-config--full.json", domain.ParseProjectConfig)); err != nil {
			return err
		}
		if _, err := tx.CreateAgent(tasksProject, "agent-1", domain.RoleImplementer); err != nil {
			return err
		}
		if _, err := tx.CreateGoal(persistence.GoalRecord{ProjectID: tasksProject, IntegrationMode: domain.ModePR,
			Goal: supervisor.Goal{Kind: domain.KindMilestone, ID: "ms-1", Title: "M", AcceptanceCriteria: []string{"A"}, Scope: []string{}, OutOfScope: []string{}}}); err != nil {
			return err
		}
		if _, err := tx.CreateWorkOrder(tasksOrder(t, "wo-1", "ms-1", supervisor.OrderImplementation)); err != nil {
			return err
		}
		snap, err := BuildSnapshot(tx, tasksProject, "ms-1", offers)
		if err != nil {
			return err
		}
		if snap.StateRevision != 0 {
			t.Errorf("Snapshot in der anlegenden Transaktion: Revision %d", snap.StateRevision)
		}
		batch := tasksBatch(snap, tasksImplementation(t, snap, "dec-1", "wo-1"))
		batch.BatchID = "batch-0"
		vb, err := supervisor.Validator{}.Accept(batch, snap, pol)
		if err != nil {
			return err
		}
		return tx.RecordPlan(persistence.PlanRecord{Batch: batch, Verdict: supervisor.VerdictAccepted,
			Delegations:            map[string]domain.AgentProfile{"dec-1": vb.Decisions[0].Delegation.Profile},
			PlanningSnapshotSHA256: tasksSum(snap), CurrentSnapshotSHA256: tasksSum(snap)})
	})
	if got, ok := res.Revisions[tasksProject]; !ok || got != 0 {
		t.Fatalf("Revision nach der Anlage = %d (gemeldet: %v), erwartet 0", got, ok)
	}

	ls, err := tk.ReserveBatch(tasksCtx(t), tasksProject, "batch-0")
	if err != nil || len(ls) != 1 {
		t.Fatalf("ReserveBatch auf Revision 0: %v %v", ls, err)
	}
	var rev int64
	tasksView(t, s, func(r *persistence.ReadTx) error {
		var err error
		rev, err = r.Revision(tasksProject)
		return err
	})
	if rev != 1 {
		t.Errorf("Revision nach der Reservierung = %d, erwartet 1", rev)
	}
	// Derselbe Plan ist jetzt veraltet und bereits ausgeführt.
	_, err = tk.ReserveBatch(tasksCtx(t), tasksProject, "batch-0")
	tasksWantClass(t, err, TaskConflict)
}

// ---- Fehlerabbildung --------------------------------------------------------------------------------------------

func TestTaskErrorClasses(t *testing.T) {
	err := &TaskError{Class: TaskStale, Op: "ApplyEvent", Message: "m", Err: errors.New("ursache")}
	if TaskClassOf(err) != TaskStale || !errors.Is(err, &TaskError{Class: TaskStale}) || errors.Is(err, &TaskError{Class: TaskConflict}) {
		t.Errorf("Klasse/Is: %v", err)
	}
	if TaskClassOf(errors.New("x")) != "" || TaskClassOf(nil) != "" {
		t.Error("Klasse eines fremden Fehlers")
	}
	wrapped := errors.Join(errors.New("a"), err)
	if TaskClassOf(wrapped) != TaskStale {
		t.Error("Klasse hinter errors.Join nicht gefunden")
	}
	if !strings.Contains(err.Error(), "ApplyEvent") || !strings.Contains(err.Error(), "stale") {
		t.Errorf("Meldung = %q", err.Error())
	}
}

// TestTaskErrorMapping: Fehler der Persistenzschicht und des Vertrags werden stabil auf Klassen abgebildet; ein Abbruch des
// Kontexts bleibt unverändert.
func TestTaskErrorMapping(t *testing.T) {
	w := newTasksWorld(t)
	ctx, cancel := context.WithCancel(tasksCtx(t))
	cancel()
	_, err := w.tasks.ReserveBatch(ctx, tasksProject, "batch-1")
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Abbruch des Kontexts = %v", err)
	}
	if _, err := w.tasks.ReserveBatch(tasksCtx(t), "unbekannt", "batch-1"); TaskClassOf(err) == "" {
		t.Errorf("fremdes Projekt ohne Klasse: %v", err)
	}
	if _, err := w.tasks.ReserveBatch(tasksCtx(t), "ungültig id!", "batch-1"); TaskClassOf(err) != TaskInvalid {
		t.Errorf("ungültige ID: %v", err)
	}
}
