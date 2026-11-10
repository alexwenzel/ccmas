package orchestrator

import (
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/alexwenzel/ccmas/internal/domain"
	"github.com/alexwenzel/ccmas/internal/persistence"
	"github.com/alexwenzel/ccmas/internal/supervisor"
)

// ---- Tabelle der Auftragsübergänge ------------------------------------------------------------------------------

var (
	tasksBuilders = []domain.Role{domain.RoleImplementer, domain.RoleConcept, domain.RoleDocumentation}
	tasksChecking = []domain.Role{domain.RoleReviewer, domain.RoleConceptReviewer}
)

// TestOrderTransitionsByRole: Die Tabelle ist die Festlegung (tabellengetrieben, vollständig über Rollen und Zustände).
func TestOrderTransitionsByRole(t *testing.T) {
	type start struct {
		to domain.WorkOrderState
		ok bool
	}
	buildStarts := map[domain.WorkOrderState]start{
		domain.WOWaiting: {domain.WOInProgress, true}, domain.WOInProgress: {domain.WOInProgress, true},
		domain.WOValidation: {domain.WOInProgress, true}, domain.WOReview: {domain.WOInProgress, true},
		domain.WOWaitingIntegration: {domain.WOInProgress, true},
	}
	checkStarts := map[domain.WorkOrderState]start{
		domain.WOValidation: {domain.WOReview, true}, domain.WOReview: {domain.WOReview, true},
	}
	for _, role := range domain.Roles() {
		for _, from := range domain.WorkOrderStates() {
			var want start
			switch {
			case slices.Contains(tasksBuilders, role):
				want = buildStarts[from]
				if role == domain.RoleDocumentation && from == domain.WOCompleted { // der Validator lässt Dokumentation nach dem Abschluss zu
					want = start{domain.WOCompleted, true}
				}
			case slices.Contains(tasksChecking, role):
				want = checkStarts[from]
			}
			to, ok := orderStartTarget(role, from)
			if ok != want.ok || (ok && to != want.to) {
				t.Errorf("Start %s aus %s = (%s, %v), erwartet (%s, %v)", role, from, to, ok, want.to, want.ok)
			}
			if ok && to != from && !from.CanTransition(to) {
				t.Errorf("Start %s: %s -> %s ist in der Übergangstabelle des Auftrags nicht erlaubt", role, from, to)
			}
		}
	}

	// Terminaler Versuch: nur der erfolgreiche Versuch einer Arbeitsrolle bewegt den Auftrag, und nur aus in_progress.
	for _, role := range domain.Roles() {
		for _, from := range domain.WorkOrderStates() {
			for _, end := range domain.ExecutionStates() {
				if !end.IsTerminal() {
					continue
				}
				want := from
				if slices.Contains(tasksBuilders, role) && end == domain.ExecSucceeded && from == domain.WOInProgress {
					want = domain.WOValidation
				}
				if got := orderFinishTarget(role, from, end); got != want {
					t.Errorf("Ende %s/%s aus %s = %s, erwartet %s", role, end, from, got, want)
				}
			}
		}
	}

	// Die Tabellen passen zusammen: Was die Implementierung hinterlässt, ist ein Startzustand der Prüfung.
	after := orderFinishTarget(domain.RoleImplementer, domain.WOInProgress, domain.ExecSucceeded)
	if _, ok := orderStartTarget(domain.RoleReviewer, after); !ok {
		t.Errorf("nach der Implementierung (%s) lässt sich kein Review starten", after)
	}
}

// TestRunnerNeverCompletesOrder: Kein Ergebnis eines Runners macht einen Auftrag zu completed; das geschieht erst nach der
// Integration, außerhalb dieser Schicht.
func TestRunnerNeverCompletesOrder(t *testing.T) {
	for _, role := range domain.Roles() {
		for _, from := range domain.WorkOrderStates() {
			for _, end := range domain.ExecutionStates() {
				if end.IsTerminal() && from != domain.WOCompleted && orderFinishTarget(role, from, end) == domain.WOCompleted {
					t.Errorf("%s/%s aus %s schließt den Auftrag ab", role, end, from)
				}
			}
			if to, ok := orderStartTarget(role, from); ok && from != domain.WOCompleted && to == domain.WOCompleted {
				t.Errorf("Start von %s aus %s schließt den Auftrag ab", role, from)
			}
		}
	}

	w := newTasksWorld(t)
	l := w.reserve("batch-1", "wo-1")
	w.apply(tasksSession(l.ExecutionID))
	w.apply(tasksResult(l.ExecutionID, 1, domain.OutcomeSucceeded))
	if o := w.order("wo-1"); o.State != domain.WOValidation {
		t.Errorf("Auftrag nach erfolgreichem Versuch = %s, erwartet validation", o.State)
	}
	if o := w.order("wo-1"); o.State == domain.WOCompleted {
		t.Error("ein Runner-Ergebnis hat den Auftrag abgeschlossen")
	}
}

// ---- Verspätete und doppelte Ereignisse -------------------------------------------------------------------------

// TestStaleLeaseEvent: Nach dem Absturz von E1/A1 und der neuen Reservierung (Generation 2, E2) ändert ein spätes Ergebnis von
// E1 nichts: weder Zeilen noch Ereignisse noch Revision.
func TestStaleLeaseEvent(t *testing.T) {
	w := newTasksWorld(t)
	l1 := w.reserve("batch-1", "wo-1")
	w.apply(tasksSession(l1.ExecutionID))
	if err := w.tasks.ApplyProcessExit(tasksCtx(t), tasksProject, l1.ExecutionID, 1, 137); err != nil {
		t.Fatalf("ApplyProcessExit: %v", err)
	}
	l2 := w.reserve("batch-2", "wo-1")
	if l2.Generation != 2 || l2.ExecutionID == l1.ExecutionID {
		t.Fatalf("zweite Reservierung: %+v (erste %+v)", l2, l1)
	}

	late := map[string]domain.RunEvent{
		"result succeeded":  tasksResult(l1.ExecutionID, 1, domain.OutcomeSucceeded),
		"session_started":   tasksSession(l1.ExecutionID),
		"activity":          tasksEvent(l1.ExecutionID, 1, domain.MsgActivity, domain.Activity{Kind: domain.ActivityKind("tool"), Summary: "x"}),
		"result Versuch 2":  tasksResult(l1.ExecutionID, 2, domain.OutcomeSucceeded),
		"terminaler Fehler": tasksEvent(l1.ExecutionID, 1, domain.MsgError, domain.ErrorPayload{Code: "x", Message: "m", Kind: domain.ErrorKind("runtime"), Terminal: true}),
	}
	for name, ev := range late {
		t.Run(name, func(t *testing.T) {
			err := w.unchanged(func() error { return w.tasks.ApplyEvent(tasksCtx(t), ev) })
			tasksWantClass(t, err, TaskStale, TaskSequence)
		})
	}
	if a := w.attempt(l2.ExecutionID, 1); a.State != domain.ExecInitializing {
		t.Errorf("E2/A1 ist %s", a.State)
	}
	if o := w.order("wo-1"); o.State != domain.WOInProgress {
		t.Errorf("Auftrag = %s", o.State)
	}
	if ls := w.leases("wo-1"); len(ls) != 2 || ls[0].State != domain.LeaseReleased || ls[1].State != domain.LeaseActive {
		t.Errorf("Leases = %+v", ls)
	}
	// Auch das späte Prozessende des alten Versuchs (nach dem Ende ohne Wirkung) ändert nichts.
	if err := w.unchanged(func() error {
		return w.tasks.ApplyProcessExit(tasksCtx(t), tasksProject, l1.ExecutionID, 1, 0)
	}); err != nil {
		t.Errorf("spätes Prozessende eines beendeten Versuchs: %v", err)
	}
	// Ein Prozessende eines nicht begonnenen Versuchs fällt aus der Reihenfolge.
	err := w.unchanged(func() error { return w.tasks.ApplyProcessExit(tasksCtx(t), tasksProject, l2.ExecutionID, 2, 0) })
	tasksWantClass(t, err, TaskSequence)
}

// TestEventFencedByLease: Gehört der Versuch nicht mehr zur aktiven Lease (abgelaufen, widerrufen, freigegeben), ändert ein
// Ereignis des Versuchs nichts, auch wenn der Versuch selbst noch nicht beendet ist.
func TestEventFencedByLease(t *testing.T) {
	for _, to := range []domain.LeaseState{domain.LeaseExpired, domain.LeaseRevoked, domain.LeaseReleased} {
		t.Run(string(to), func(t *testing.T) {
			w := newTasksWorld(t)
			l := w.reserve("batch-1", "wo-1")
			w.apply(tasksSession(l.ExecutionID))
			tasksUpdate(t, w.store, func(tx *persistence.Tx) error {
				return tx.TransitionLease(tasksProject, l.LeaseID, domain.LeaseActive, to)
			})
			for name, run := range map[string]func() error{
				"result": func() error {
					return w.tasks.ApplyEvent(tasksCtx(t), tasksResult(l.ExecutionID, 1, domain.OutcomeSucceeded))
				},
				"activity": func() error {
					return w.tasks.ApplyEvent(tasksCtx(t), tasksEvent(l.ExecutionID, 1, domain.MsgActivity, domain.Activity{Kind: domain.ActivityKind("tool"), Summary: "x"}))
				},
				"Prozessende": func() error { return w.tasks.ApplyProcessExit(tasksCtx(t), tasksProject, l.ExecutionID, 1, 1) },
			} {
				err := w.unchanged(run)
				if err == nil {
					t.Errorf("%s: kein Fehler bei beendeter Lease (%s)", name, to)
					continue
				}
				tasksWantClass(t, err, TaskStale)
			}
		})
	}
}

// TestDuplicateCompletion: Dasselbe Ergebnis zweimal ergibt einen Abschluss, ein Abschluss-Ereignis, eine Auditspur und eine
// Revisionserhöhung.
func TestDuplicateCompletion(t *testing.T) {
	w := newTasksWorld(t)
	l := w.reserve("batch-1", "wo-1")
	w.apply(tasksSession(l.ExecutionID))
	revBefore, finishedBefore := w.revision(), len(w.events(domain.AuditAttemptFinished))

	ev := tasksResult(l.ExecutionID, 1, domain.OutcomeSucceeded)
	w.apply(ev)
	if got := w.revision(); got != revBefore+1 {
		t.Fatalf("Revision %d -> %d, erwartet +1", revBefore, got)
	}
	if o := w.order("wo-1"); o.State != domain.WOValidation {
		t.Errorf("Auftrag = %s", o.State)
	}
	if a := w.attempt(l.ExecutionID, 1); a.State != domain.ExecSucceeded || a.Terminal == nil || a.Terminal.Source != domain.TerminalFromResult || a.Outcome != domain.OutcomeSucceeded || a.Summary != "fertig" {
		t.Errorf("Versuch = %+v", a)
	}
	if ls := w.leases("wo-1"); len(ls) != 1 || ls[0].State != domain.LeaseReleased || ls[0].ReleasedAt.IsZero() {
		t.Errorf("Lease = %+v", ls)
	}
	if a := w.agent("agent-1"); a.Availability != domain.AgentAvailable {
		t.Errorf("Agent = %s", a.Availability)
	}
	afterFirst := w.dump()

	for i := 0; i < 3; i++ {
		err := w.tasks.ApplyEvent(tasksCtx(t), ev)
		tasksWantClass(t, err, TaskSequence)
	}
	if w.dump() != afterFirst {
		t.Errorf("das wiederholte Ergebnis hat etwas verändert:\n%s\n---\n%s", afterFirst, w.dump())
	}
	if got := len(w.events(domain.AuditAttemptFinished)); got != finishedBefore+1 {
		t.Errorf("%d Abschluss-Ereignisse, erwartet genau eins", got-finishedBefore)
	}
	if n := len(w.events(domain.AuditLeaseReleased)); n != 1 {
		t.Errorf("%d Ereignisse lease.released", n)
	}

	// Ein anderes Ergebnis nach dem Abschluss ändert ebenfalls nichts.
	err := w.unchanged(func() error {
		return w.tasks.ApplyEvent(tasksCtx(t), tasksResult(l.ExecutionID, 1, domain.OutcomeControlledFailure))
	})
	tasksWantClass(t, err, TaskSequence)
}

// TestProcessExitZeroIsCrash: Ein Prozessende ohne terminales Ergebnis ist ein Absturz, auch bei Exit-Code 0. Die Lease wird
// freigegeben, der Agent ist wieder verfügbar, der Auftrag bleibt unverändert.
func TestProcessExitZeroIsCrash(t *testing.T) {
	for _, code := range []int{0, 1, 137} {
		t.Run(itoaT(code), func(t *testing.T) {
			w := newTasksWorld(t)
			l := w.reserve("batch-1", "wo-1")
			w.apply(tasksSession(l.ExecutionID))
			orderBefore := w.order("wo-1")
			revBefore := w.revision()

			if err := w.tasks.ApplyProcessExit(tasksCtx(t), tasksProject, l.ExecutionID, 1, code); err != nil {
				t.Fatalf("ApplyProcessExit: %v", err)
			}
			a := w.attempt(l.ExecutionID, 1)
			if a.State != domain.ExecCrashed || a.Terminal == nil || a.Terminal.Source != domain.TerminalFromProcessExit || a.Terminal.ExitCode != code || a.Outcome != "" {
				t.Errorf("Versuch = %+v", a)
			}
			if o := w.order("wo-1"); o.State != domain.WOInProgress || o.Revision != orderBefore.Revision {
				t.Errorf("ein Absturz hat den Auftrag verändert: %+v", o)
			}
			if ls := w.leases("wo-1"); ls[0].State != domain.LeaseReleased {
				t.Errorf("Lease = %+v", ls[0])
			}
			if ag := w.agent("agent-1"); ag.Availability != domain.AgentAvailable {
				t.Errorf("Agent = %s", ag.Availability)
			}
			if got := w.revision(); got != revBefore+1 {
				t.Errorf("Revision %d -> %d", revBefore, got)
			}
			// Wiederholt: keine zweite Wirkung, kein Fehler (Prozessende nach terminalem Zustand).
			if err := w.unchanged(func() error {
				return w.tasks.ApplyProcessExit(tasksCtx(t), tasksProject, l.ExecutionID, 1, code)
			}); err != nil {
				t.Errorf("wiederholtes Prozessende: %v", err)
			}
		})
	}

	t.Run("nach dem Ergebnis ohne Wirkung", func(t *testing.T) {
		w := newTasksWorld(t)
		l := w.reserve("batch-1", "wo-1")
		w.apply(tasksSession(l.ExecutionID))
		w.apply(tasksResult(l.ExecutionID, 1, domain.OutcomeSucceeded))
		if err := w.unchanged(func() error {
			return w.tasks.ApplyProcessExit(tasksCtx(t), tasksProject, l.ExecutionID, 1, 0)
		}); err != nil {
			t.Errorf("Prozessende nach Ergebnis: %v", err)
		}
		if a := w.attempt(l.ExecutionID, 1); a.State != domain.ExecSucceeded {
			t.Errorf("Versuch = %s", a.State)
		}
	})

	t.Run("unbekannte Ausführung", func(t *testing.T) {
		w := newTasksWorld(t)
		err := w.unchanged(func() error { return w.tasks.ApplyProcessExit(tasksCtx(t), tasksProject, "exec-404", 1, 0) })
		tasksWantClass(t, err, TaskNotFound)
	})
}

// ---- Ablauf eines Versuchs --------------------------------------------------------------------------------------

func TestApplyEventLifecycle(t *testing.T) {
	t.Run("session_started", func(t *testing.T) {
		w := newTasksWorld(t)
		l := w.reserve("batch-1", "wo-1")
		revBefore := w.revision()
		w.apply(tasksSession(l.ExecutionID))
		a := w.attempt(l.ExecutionID, 1)
		if a.State != domain.ExecRunning || a.SessionID != "sess-"+l.ExecutionID {
			t.Errorf("Versuch = %+v", a)
		}
		if ag := w.agent("agent-1"); ag.Availability != domain.AgentActive {
			t.Errorf("Agent = %s, erwartet active", ag.Availability)
		}
		if got := w.revision(); got != revBefore+1 {
			t.Errorf("Revision %d -> %d", revBefore, got)
		}
		// Dasselbe Ereignis noch einmal: außerhalb der Reihenfolge, ohne Wirkung.
		err := w.unchanged(func() error { return w.tasks.ApplyEvent(tasksCtx(t), tasksSession(l.ExecutionID)) })
		tasksWantClass(t, err, TaskSequence)
	})

	t.Run("Aktivität, Werkzeug und Warnung ändern nichts", func(t *testing.T) {
		w := newTasksWorld(t)
		l := w.reserve("batch-1", "wo-1")
		w.apply(tasksSession(l.ExecutionID))
		for name, ev := range map[string]domain.RunEvent{
			"activity": tasksEvent(l.ExecutionID, 1, domain.MsgActivity, domain.Activity{Kind: domain.ActivityKind("tool"), Summary: "liest"}),
			"tool":     tasksEvent(l.ExecutionID, 1, domain.MsgToolEvent, domain.ToolEvent{ToolUseID: "t1", Tool: "Read", Phase: domain.ToolPhase("start")}),
			"Warnung":  tasksEvent(l.ExecutionID, 1, domain.MsgError, domain.ErrorPayload{Code: "w", Message: "m", Kind: domain.ErrorKind("runtime"), Terminal: false}),
		} {
			if err := w.unchanged(func() error { return w.tasks.ApplyEvent(tasksCtx(t), ev) }); err != nil {
				t.Errorf("%s: %v", name, err)
			}
		}
	})

	t.Run("Frage", func(t *testing.T) {
		w := newTasksWorld(t)
		l := w.reserve("batch-1", "wo-1")
		w.apply(tasksSession(l.ExecutionID))
		q := tasksEvent(l.ExecutionID, 1, domain.MsgQuestion, domain.QuestionPayload{QuestionID: "q-1", Question: "Welche Variante?", Context: "Zwei Wege", Recommendation: "A"})
		w.apply(q)
		if a := w.attempt(l.ExecutionID, 1); a.State != domain.ExecAwaitingAnswer {
			t.Errorf("Versuch = %s", a.State)
		}
		tasksView(t, w.store, func(r *persistence.ReadTx) error {
			qs, err := r.Questions(tasksProject)
			if err != nil || len(qs) != 1 {
				t.Fatalf("Fragen: %v %v", qs, err)
			}
			got := qs[0]
			if got.QuestionID != "q-1" || got.WorkOrderID != "wo-1" || got.ExecutionID != l.ExecutionID || got.Attempt != 1 || got.State != domain.QuestionOpen ||
				got.Text != "Welche Variante?" || got.Context != "Zwei Wege" || got.Recommendation != "A" {
				t.Errorf("Frage = %+v", got)
			}
			return nil
		})
		if o := w.order("wo-1"); o.State != domain.WOInProgress {
			t.Errorf("Auftrag = %s", o.State)
		}
		// Dieselbe Frage noch einmal: kein zweiter Eintrag.
		err := w.unchanged(func() error { return w.tasks.ApplyEvent(tasksCtx(t), q) })
		tasksWantClass(t, err, TaskSequence)
	})

	t.Run("terminaler Fehler", func(t *testing.T) {
		w := newTasksWorld(t)
		l := w.reserve("batch-1", "wo-1")
		w.apply(tasksSession(l.ExecutionID))
		w.apply(tasksEvent(l.ExecutionID, 1, domain.MsgError, domain.ErrorPayload{Code: "boom", Message: "kaputt", Kind: domain.ErrorKind("runtime"), Terminal: true}))
		a := w.attempt(l.ExecutionID, 1)
		if a.State != domain.ExecControlledFailure || a.Terminal.Source != domain.TerminalFromError || !strings.Contains(a.Summary, "boom") {
			t.Errorf("Versuch = %+v", a)
		}
		if o := w.order("wo-1"); o.State != domain.WOInProgress {
			t.Errorf("Auftrag = %s, bleibt in_progress", o.State)
		}
		if ls := w.leases("wo-1"); ls[0].State != domain.LeaseReleased {
			t.Errorf("Lease = %s", ls[0].State)
		}
		if ag := w.agent("agent-1"); ag.Availability != domain.AgentAvailable {
			t.Errorf("Agent = %s", ag.Availability)
		}
	})

	t.Run("kontrolliertes Scheitern bewegt den Auftrag nicht", func(t *testing.T) {
		w := newTasksWorld(t)
		l := w.reserve("batch-1", "wo-1")
		w.apply(tasksSession(l.ExecutionID))
		w.apply(tasksResult(l.ExecutionID, 1, domain.OutcomeControlledFailure))
		if o := w.order("wo-1"); o.State != domain.WOInProgress {
			t.Errorf("Auftrag = %s", o.State)
		}
	})

	t.Run("Zusammenfassung wird bereinigt und gekürzt", func(t *testing.T) {
		w := newTasksWorld(t)
		l := w.reserve("batch-1", "wo-1")
		w.apply(tasksSession(l.ExecutionID))
		long := strings.Repeat("ä", 1500) + "\x00\x1b[31m" + string([]byte{0xff}) + strings.Repeat("z", 100)
		w.apply(tasksEvent(l.ExecutionID, 1, domain.MsgResult, domain.Result{Outcome: domain.OutcomeSucceeded, Summary: long}))
		a := w.attempt(l.ExecutionID, 1)
		if a.State != domain.ExecSucceeded || len(a.Summary) > domain.MaxAttemptSummaryBytes || !utf8.ValidString(a.Summary) || a.Summary == "" {
			t.Errorf("Versuch %s, Zusammenfassung %d Bytes", a.State, len(a.Summary))
		}
		if strings.ContainsAny(a.Summary, "\x00\x1b") {
			t.Error("Steuerzeichen in der Zusammenfassung")
		}
	})

	t.Run("unbekannte Ausführung", func(t *testing.T) {
		w := newTasksWorld(t)
		err := w.unchanged(func() error { return w.tasks.ApplyEvent(tasksCtx(t), tasksSession("exec-404")) })
		tasksWantClass(t, err, TaskNotFound)
	})

	t.Run("Ereignis ohne bekannte Nutzlast", func(t *testing.T) {
		w := newTasksWorld(t)
		l := w.reserve("batch-1", "wo-1")
		err := w.unchanged(func() error {
			return w.tasks.ApplyEvent(tasksCtx(t), tasksEvent(l.ExecutionID, 1, domain.MsgActivity, "kein Payload"))
		})
		tasksWantClass(t, err, TaskClass(domain.CatMessageType))
	})

	t.Run("Ausführung ohne Auftrag", func(t *testing.T) {
		// Der Supervisor läuft ohne Auftrag und ohne Lease; seine Ereignisse brauchen keine Lease.
		w := newTasksWorld(t)
		sup := tasksFixture(t, "agent-profile--supervisor_xhigh.json", domain.ParseAgentProfile)
		tasksUpdate(t, w.store, func(tx *persistence.Tx) error {
			if err := tx.RecordProfile(tasksProject, sup); err != nil {
				return err
			}
			if err := tx.CreateExecution(domain.Execution{ExecutionID: "exec-sup", ProjectID: tasksProject, Role: domain.RoleSupervisor, CreatedAt: tasksEarly}); err != nil {
				return err
			}
			return tx.StartAttempt(tasksProject, domain.ExecutionAttempt{ExecutionID: "exec-sup", Attempt: 1, ProfileID: sup.ProfileID, ProfileVersion: sup.Version,
				State: domain.ExecInitializing, StartedAt: tasksEarly})
		})
		w.apply(tasksSession("exec-sup"))
		w.apply(tasksResult("exec-sup", 1, domain.OutcomeSucceeded))
		if a := w.attempt("exec-sup", 1); a.State != domain.ExecSucceeded {
			t.Errorf("Supervisor-Versuch = %s", a.State)
		}
	})
}

// ---- Abbrechen und Wiederaufnehmen ------------------------------------------------------------------------------

func TestCancelTask(t *testing.T) {
	cancel := func(w *tasksWorld, order string) error {
		return w.tasks.CancelTask(tasksCtx(w.t), tasksProject, order, "alice", "Umfang entfällt")
	}

	t.Run("laufender Versuch", func(t *testing.T) {
		w := newTasksWorld(t)
		l := w.reserve("batch-1", "wo-1")
		w.apply(tasksSession(l.ExecutionID))
		revBefore := w.revision()
		if err := cancel(w, "wo-1"); err != nil {
			t.Fatalf("CancelTask: %v", err)
		}
		if got := w.revision(); got != revBefore+1 {
			t.Errorf("Revision %d -> %d", revBefore, got)
		}
		if o := w.order("wo-1"); o.State != domain.WOCancelled {
			t.Errorf("Auftrag = %s", o.State)
		}
		if ls := w.leases("wo-1"); len(ls) != 1 || ls[0].State != domain.LeaseRevoked {
			t.Errorf("Leases = %+v", ls)
		}
		if a := w.attempt(l.ExecutionID, 1); a.State != domain.ExecStopping {
			t.Errorf("Versuch = %s, erwartet stopping (das Stoppsignal sendet die Runtime)", a.State)
		}
		if ag := w.agent("agent-1"); ag.Availability != domain.AgentActive {
			t.Errorf("Agent = %s (frei erst, wenn der Versuch endet)", ag.Availability)
		}
		evs := w.events(domain.AuditWorkOrderCancelled)
		if len(evs) != 1 || evs[0].Actor != "alice" || evs[0].Details["reason"] != "Umfang entfällt" || evs[0].EntityID != "wo-1" {
			t.Errorf("Audit des Abbruchs = %+v", evs)
		}
		for _, kind := range []string{domain.AuditWorkOrderStateChanged, domain.AuditLeaseRevoked, domain.AuditExecutionStateChanged} {
			found := false
			for _, e := range w.events(kind) {
				found = found || (e.Actor == "alice" && e.StateRevision == revBefore+1)
			}
			if !found {
				t.Errorf("kein Ereignis %s mit Akteur alice", kind)
			}
		}

		// Zweiter Aufruf: idempotent.
		if err := w.unchanged(func() error { return cancel(w, "wo-1") }); err != nil {
			t.Errorf("zweiter Abbruch: %v", err)
		}
		// Während des Stoppens ändert eine Aktivität nichts (die Lease ist widerrufen).
		err := w.unchanged(func() error {
			return w.tasks.ApplyEvent(tasksCtx(t), tasksEvent(l.ExecutionID, 1, domain.MsgActivity, domain.Activity{Kind: domain.ActivityKind("tool"), Summary: "x"}))
		})
		tasksWantClass(t, err, TaskStale)

		// Das spätere terminale Ergebnis schließt nur den Versuch ab.
		leasesBefore, execsBefore := len(w.leases("wo-1")), len(w.executions("wo-1"))
		revBefore = w.revision()
		w.apply(tasksResult(l.ExecutionID, 1, domain.OutcomeCancelled))
		if a := w.attempt(l.ExecutionID, 1); a.State != domain.ExecCancelled || a.Terminal.Source != domain.TerminalFromResult {
			t.Errorf("Versuch = %+v", a)
		}
		if ag := w.agent("agent-1"); ag.Availability != domain.AgentAvailable {
			t.Errorf("Agent = %s", ag.Availability)
		}
		if o := w.order("wo-1"); o.State != domain.WOCancelled {
			t.Errorf("Auftrag = %s", o.State)
		}
		if ls := w.leases("wo-1"); len(ls) != leasesBefore || ls[0].State != domain.LeaseRevoked {
			t.Errorf("Leases = %+v", ls)
		}
		if len(w.executions("wo-1")) != execsBefore {
			t.Error("neue Ausführung entstanden")
		}
		if got := w.revision(); got != revBefore+1 {
			t.Errorf("Revision %d -> %d", revBefore, got)
		}
		// Idempotent: dasselbe Ergebnis noch einmal und das Prozessende danach ändern nichts.
		err = w.unchanged(func() error {
			return w.tasks.ApplyEvent(tasksCtx(t), tasksResult(l.ExecutionID, 1, domain.OutcomeCancelled))
		})
		tasksWantClass(t, err, TaskSequence)
		if err := w.unchanged(func() error {
			return w.tasks.ApplyProcessExit(tasksCtx(t), tasksProject, l.ExecutionID, 1, 0)
		}); err != nil {
			t.Errorf("Prozessende nach dem Abschluss: %v", err)
		}
		if err := w.unchanged(func() error { return cancel(w, "wo-1") }); err != nil {
			t.Errorf("Abbruch nach dem Abschluss des Versuchs: %v", err)
		}
	})

	t.Run("späteres Ergebnis succeeded bewegt den Auftrag nicht", func(t *testing.T) {
		w := newTasksWorld(t)
		l := w.reserve("batch-1", "wo-1")
		w.apply(tasksSession(l.ExecutionID))
		if err := cancel(w, "wo-1"); err != nil {
			t.Fatal(err)
		}
		w.apply(tasksResult(l.ExecutionID, 1, domain.OutcomeSucceeded))
		if a := w.attempt(l.ExecutionID, 1); a.State != domain.ExecSucceeded {
			t.Errorf("Versuch = %s", a.State)
		}
		if o := w.order("wo-1"); o.State != domain.WOCancelled {
			t.Errorf("Auftrag = %s, bleibt cancelled", o.State)
		}
		if ag := w.agent("agent-1"); ag.Availability != domain.AgentAvailable {
			t.Errorf("Agent = %s", ag.Availability)
		}
	})

	t.Run("Prozessende nach dem Abbruch beendet den Versuch als Absturz", func(t *testing.T) {
		w := newTasksWorld(t)
		l := w.reserve("batch-1", "wo-1")
		w.apply(tasksSession(l.ExecutionID))
		if err := cancel(w, "wo-1"); err != nil {
			t.Fatal(err)
		}
		if err := w.tasks.ApplyProcessExit(tasksCtx(t), tasksProject, l.ExecutionID, 1, 143); err != nil {
			t.Fatal(err)
		}
		if a := w.attempt(l.ExecutionID, 1); a.State != domain.ExecCrashed {
			t.Errorf("Versuch = %s", a.State)
		}
		if ag := w.agent("agent-1"); ag.Availability != domain.AgentAvailable {
			t.Errorf("Agent = %s", ag.Availability)
		}
		if ls := w.leases("wo-1"); ls[0].State != domain.LeaseRevoked {
			t.Errorf("Lease = %s", ls[0].State)
		}
	})

	t.Run("wartender Versuch", func(t *testing.T) {
		w := newTasksWorld(t)
		l := w.reserve("batch-1", "wo-1")
		w.apply(tasksSession(l.ExecutionID))
		w.apply(tasksEvent(l.ExecutionID, 1, domain.MsgQuestion, domain.QuestionPayload{QuestionID: "q-1", Question: "Frage?", Context: "Kontext"}))
		if err := cancel(w, "wo-1"); err != nil {
			t.Fatal(err)
		}
		if a := w.attempt(l.ExecutionID, 1); a.State != domain.ExecStopping {
			t.Errorf("Versuch = %s", a.State)
		}
	})

	t.Run("Versuch ohne Prozess wird kontrolliert beendet", func(t *testing.T) {
		w := newTasksWorld(t)
		l := w.reserve("batch-1", "wo-1") // initializing, noch kein Prozess
		if err := cancel(w, "wo-1"); err != nil {
			t.Fatal(err)
		}
		a := w.attempt(l.ExecutionID, 1)
		if a.State != domain.ExecCancelled || a.Terminal == nil || a.Terminal.Source != domain.TerminalFromResult || a.Outcome != domain.OutcomeCancelled {
			t.Errorf("Versuch = %+v (kontrolliert beendet, kein Absturz)", a)
		}
		if !strings.Contains(a.Summary, "CancelTask") {
			t.Errorf("Zusammenfassung nennt den Ursprung nicht: %q", a.Summary)
		}
		if ag := w.agent("agent-1"); ag.Availability != domain.AgentAvailable {
			t.Errorf("Agent = %s", ag.Availability)
		}
		if ls := w.leases("wo-1"); ls[0].State != domain.LeaseRevoked {
			t.Errorf("Lease = %s", ls[0].State)
		}
		if o := w.order("wo-1"); o.State != domain.WOCancelled {
			t.Errorf("Auftrag = %s", o.State)
		}
		for _, e := range w.events(domain.AuditAttemptFinished) {
			if e.Details["source"] == domain.TerminalFromProcessExit {
				t.Errorf("Absturz gemeldet: %+v", e)
			}
		}
		// Ein spätes Ereignis oder Prozessende ändert nichts mehr.
		if err := w.unchanged(func() error { return w.tasks.ApplyProcessExit(tasksCtx(t), tasksProject, l.ExecutionID, 1, 0) }); err != nil {
			t.Errorf("Prozessende: %v", err)
		}
		err := w.unchanged(func() error { return w.tasks.ApplyEvent(tasksCtx(t), tasksSession(l.ExecutionID)) })
		tasksWantClass(t, err, TaskSequence)
	})

	t.Run("Versuch mit Prozess, aber ohne Session, wird gestoppt", func(t *testing.T) {
		w := newTasksWorld(t)
		l := w.reserve("batch-1", "wo-1")
		tasksUpdate(t, w.store, func(tx *persistence.Tx) error {
			return tx.RecordProcess(tasksProject, l.ExecutionID, 1, domain.ProcessIdentity{PID: 4242, StartTime: tasksEarly}, domain.ProcRunning, tasksWhen)
		})
		if err := cancel(w, "wo-1"); err != nil {
			t.Fatal(err)
		}
		if a := w.attempt(l.ExecutionID, 1); a.State != domain.ExecStopping {
			t.Errorf("Versuch = %s, erwartet stopping (es gibt einen Prozess, der zu stoppen ist)", a.State)
		}
	})

	t.Run("Auftrag ohne Reservierung", func(t *testing.T) {
		w := newTasksWorld(t)
		if err := cancel(w, "wo-2"); err != nil {
			t.Fatal(err)
		}
		if o := w.order("wo-2"); o.State != domain.WOCancelled {
			t.Errorf("Auftrag = %s", o.State)
		}
	})

	t.Run("Fehler ohne Änderung", func(t *testing.T) {
		w := newTasksWorld(t)
		tasksUpdate(t, w.store, func(tx *persistence.Tx) error { return tasksComplete(tx, "wo-2") })
		for name, tc := range map[string]struct {
			order, actor, reason string
			want                 TaskClass
		}{
			"abgeschlossener Auftrag": {"wo-2", "alice", "x", TaskState},
			"unbekannter Auftrag":     {"wo-404", "alice", "x", TaskNotFound},
			"Grund fehlt":             {"wo-1", "alice", "", TaskInvalid},
			"Grund mit Steuerzeichen": {"wo-1", "alice", "a\x00b", TaskInvalid},
			"Akteur ungültig":         {"wo-1", "al ice", "x", TaskInvalid},
		} {
			err := w.unchanged(func() error {
				return w.tasks.CancelTask(tasksCtx(t), tasksProject, tc.order, tc.actor, tc.reason)
			})
			if TaskClassOf(err) != tc.want {
				t.Errorf("%s: %v, erwartet Klasse %s", name, err, tc.want)
			}
		}
	})

	t.Run("gleichzeitig", func(t *testing.T) {
		w := newTasksWorld(t)
		l := w.reserve("batch-1", "wo-1")
		w.apply(tasksSession(l.ExecutionID))
		other := w.newTasks(w.secondStore())
		revBefore := w.revision()
		errs := make(chan error, 8)
		for i := 0; i < 8; i++ {
			go func() {
				tk := w.tasks
				if i%2 == 1 {
					tk = other
				}
				errs <- tk.CancelTask(tasksCtx(t), tasksProject, "wo-1", "alice", "Umfang entfällt")
			}()
		}
		for i := 0; i < 8; i++ {
			if err := <-errs; err != nil {
				t.Errorf("CancelTask: %v", err)
			}
		}
		if got := w.revision(); got != revBefore+1 {
			t.Errorf("Revision %d -> %d, erwartet genau +1", revBefore, got)
		}
		if n := len(w.events(domain.AuditWorkOrderCancelled)); n != 1 {
			t.Errorf("%d Abbruch-Ereignisse", n)
		}
	})
}

// TestCancelDoesNotFreeDependents: Ein abgebrochener Auftrag gibt keinen abhängigen frei.
func TestCancelDoesNotFreeDependents(t *testing.T) {
	w := newTasksWorld(t)
	tasksUpdate(t, w.store, func(tx *persistence.Tx) error {
		_, err := tx.CreateWorkOrder(tasksOrder(t, "d-1", "ms-1", supervisor.OrderImplementation, "wo-1"))
		return err
	})
	if err := w.tasks.CancelTask(tasksCtx(t), tasksProject, "wo-1", "alice", "entfällt"); err != nil {
		t.Fatal(err)
	}
	snap := w.snapshot()
	_, err := supervisor.Validator{}.Accept(tasksBatch(snap, tasksImplementation(t, snap, "dec-1", "d-1")), snap, w.pol)
	if err == nil {
		t.Fatal("der Abhängige eines abgebrochenen Auftrags wurde freigegeben")
	}
}

func TestResumeAudited(t *testing.T) {
	block := func(w *tasksWorld, order string) {
		tasksUpdate(w.t, w.store, func(tx *persistence.Tx) error {
			return tx.TransitionWorkOrder(tasksProject, order, domain.WOWaiting, domain.WOBlocked)
		})
	}
	w := newTasksWorld(t)
	block(w, "wo-1")
	revBefore := w.revision()
	execsBefore := len(w.executions("wo-1"))
	if err := w.tasks.ResumeTask(tasksCtx(t), tasksProject, "wo-1", "alice", "Blocker behoben"); err != nil {
		t.Fatalf("ResumeTask: %v", err)
	}
	if o := w.order("wo-1"); o.State != domain.WOWaiting {
		t.Errorf("Auftrag = %s", o.State)
	}
	if got := w.revision(); got != revBefore+1 {
		t.Errorf("Revision %d -> %d", revBefore, got)
	}
	evs := w.events(domain.AuditWorkOrderResumed)
	if len(evs) != 1 || evs[0].Actor != "alice" || evs[0].Details["reason"] != "Blocker behoben" || evs[0].EntityID != "wo-1" {
		t.Errorf("Audit der Wiederaufnahme = %+v", evs)
	}
	var moved bool
	for _, e := range w.events(domain.AuditWorkOrderStateChanged) {
		moved = moved || (e.EntityID == "wo-1" && e.FromState == "blocked" && e.ToState == "waiting" && e.Actor == "alice")
	}
	if !moved {
		t.Error("kein Zustandswechsel blocked -> waiting mit Akteur alice im Audit")
	}
	// Kein automatischer Neustart.
	if len(w.executions("wo-1")) != execsBefore || len(w.leases("wo-1")) != 0 {
		t.Error("die Wiederaufnahme hat etwas gestartet")
	}
	if a := w.agent("agent-1"); a.Availability != domain.AgentAvailable {
		t.Errorf("Agent = %s", a.Availability)
	}

	for _, tc := range []struct {
		name, order, actor, reason string
		want                       TaskClass
	}{
		{"nicht blockiert (wartet)", "wo-1", "alice", "x", TaskState},
		{"unbekannt", "wo-404", "alice", "x", TaskNotFound},
		{"Grund fehlt", "wo-2", "alice", "", TaskInvalid},
	} {
		if tc.name == "Grund fehlt" {
			block(w, "wo-2")
		}
		err := w.unchanged(func() error {
			return w.tasks.ResumeTask(tasksCtx(t), tasksProject, tc.order, tc.actor, tc.reason)
		})
		if TaskClassOf(err) != tc.want {
			t.Errorf("%s: %v, erwartet Klasse %s", tc.name, err, tc.want)
		}
	}
}
