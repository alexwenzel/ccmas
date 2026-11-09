package domain

import (
	"reflect"
	"testing"
	"time"
)

// transitionCase beschreibt eine erwartete Zustandstabelle unabhängig von der Implementierung:
// Schlüssel sind Ausgangszustände, Werte die erlaubten Zielzustände (leer = Endzustand).
type transitionTable[S ~string] map[S][]S

// checkTable prüft alle Paare (from, to): erlaubt genau dann, wenn in der Tabelle genannt.
func checkTable[S ~string](t *testing.T, table transitionTable[S], all []S, can func(from, to S) bool) {
	t.Helper()
	if len(table) != len(all) {
		t.Fatalf("Tabelle deckt %d von %d Zuständen ab", len(table), len(all))
	}
	for _, from := range all {
		allowed, ok := table[from]
		if !ok {
			t.Fatalf("Zustand %q fehlt in der Tabelle", from)
		}
		for _, to := range all {
			want := false
			for _, a := range allowed {
				if a == to {
					want = true
				}
			}
			if got := can(from, to); got != want {
				t.Errorf("CanTransition(%s -> %s) = %v, erwartet %v", from, to, got, want)
			}
		}
	}
}

func TestExecutionStateTransitions(t *testing.T) {
	table := transitionTable[ExecutionState]{
		ExecInitializing:      {ExecRunning, ExecStopping, ExecControlledFailure, ExecCrashed},
		ExecRunning:           {ExecAwaitingAnswer, ExecStopping, ExecSucceeded, ExecControlledFailure, ExecCrashed},
		ExecAwaitingAnswer:    {ExecRunning, ExecStopping, ExecControlledFailure, ExecCrashed},
		ExecStopping:          {ExecSucceeded, ExecControlledFailure, ExecCrashed, ExecCancelled},
		ExecSucceeded:         {},
		ExecControlledFailure: {},
		ExecCrashed:           {},
		ExecCancelled:         {},
	}
	checkTable(t, table, ExecutionStates(), func(f, to ExecutionState) bool { return f.CanTransition(to) })

	for _, s := range []ExecutionState{ExecSucceeded, ExecControlledFailure, ExecCrashed, ExecCancelled} {
		if !s.IsTerminal() {
			t.Errorf("%s muss terminal sein", s)
		}
	}
	for _, s := range []ExecutionState{ExecInitializing, ExecRunning, ExecAwaitingAnswer, ExecStopping} {
		if s.IsTerminal() {
			t.Errorf("%s darf nicht terminal sein", s)
		}
	}
	if ExecutionState("bogus").CanTransition(ExecRunning) || ExecInitializing.CanTransition("bogus") {
		t.Error("unbekannte Zustände dürfen keine gültigen Übergänge haben")
	}
}

func TestWorkOrderStateTransitions(t *testing.T) {
	table := transitionTable[WorkOrderState]{
		WOWaiting:            {WOInProgress, WOBlocked, WOCancelled},
		WOInProgress:         {WOWaiting, WOValidation, WOBlocked, WOCancelled},
		WOValidation:         {WOInProgress, WOReview, WOBlocked, WOCancelled},
		WOReview:             {WOInProgress, WOWaitingIntegration, WOBlocked, WOCancelled},
		WOWaitingIntegration: {WOInProgress, WOCompleted, WOBlocked, WOCancelled},
		WOBlocked:            {WOWaiting, WOInProgress, WOCancelled},
		WOCompleted:          {},
		WOCancelled:          {},
	}
	checkTable(t, table, WorkOrderStates(), func(f, to WorkOrderState) bool { return f.CanTransition(to) })
	if !WOCompleted.IsTerminal() || !WOCancelled.IsTerminal() || WOBlocked.IsTerminal() {
		t.Error("abgeschlossen/abgebrochen sind terminal, blockiert nicht")
	}
}

func TestAgentAvailabilityTransitions(t *testing.T) {
	table := transitionTable[AgentAvailability]{
		AgentAvailable:   {AgentReserved, AgentUnavailable},
		AgentReserved:    {AgentActive, AgentAvailable, AgentUnavailable},
		AgentActive:      {AgentAvailable, AgentUnavailable},
		AgentUnavailable: {AgentAvailable},
	}
	checkTable(t, table, AgentAvailabilities(), func(f, to AgentAvailability) bool { return f.CanTransition(to) })
}

func TestQuestionStateTransitions(t *testing.T) {
	table := transitionTable[QuestionState]{
		QuestionOpen:       {QuestionAnswered, QuestionSuperseded, QuestionCancelled},
		QuestionAnswered:   {QuestionDelivered, QuestionSuperseded, QuestionCancelled},
		QuestionDelivered:  {},
		QuestionSuperseded: {},
		QuestionCancelled:  {},
	}
	checkTable(t, table, QuestionStates(), func(f, to QuestionState) bool { return f.CanTransition(to) })
}

// TestStateTypesAreSeparate (Akzeptanzkriterium 3): Agentenverfügbarkeit, Ausführungs-,
// Auftrags- und OS-Prozesszustand sind eigene Typen, kein gemeinsames Alias.
func TestStateTypesAreSeparate(t *testing.T) {
	types := []reflect.Type{
		reflect.TypeOf(AgentAvailability("")),
		reflect.TypeOf(ExecutionState("")),
		reflect.TypeOf(WorkOrderState("")),
		reflect.TypeOf(OSProcessState("")),
		reflect.TypeOf(QuestionState("")),
	}
	for i, a := range types {
		if a.Kind() != reflect.String || a.Name() == "string" || a.PkgPath() == "" {
			t.Errorf("%v muss ein eigener benannter Typ sein", a)
		}
		for _, b := range types[i+1:] {
			if a == b {
				t.Errorf("%v und %v sind derselbe Typ", a, b)
			}
		}
	}
	// ProcessSample trägt den OS-Zustand als eigenen Typ, nicht als Ausführungs- oder Auftragszustand.
	f, ok := reflect.TypeOf(ProcessSample{}).FieldByName("State")
	if !ok || f.Type != reflect.TypeOf(OSProcessState("")) {
		t.Errorf("ProcessSample.State muss OSProcessState sein, ist %v", f.Type)
	}
	// AgentSnapshot führt die Ebenen getrennt.
	snap := reflect.TypeOf(AgentSnapshot{})
	for field, want := range map[string]reflect.Type{
		"Availability":   types[0],
		"ExecutionState": types[1],
		"WorkOrderState": types[2],
	} {
		f, ok := snap.FieldByName(field)
		if !ok || f.Type != want {
			t.Errorf("AgentSnapshot.%s hat Typ %v, erwartet %v", field, f.Type, want)
		}
	}
}

// TestProcessExitDoesNotCompleteWorkOrder: Ein Prozess-Exit mit Code 0 beendet weder
// automatisch die Ausführung als erfolgreich noch den Auftrag.
func TestProcessExitDoesNotCompleteWorkOrder(t *testing.T) {
	tr := NewExecutionTracker("proj-1", "exec-1", 1)
	mustApply(t, tr, "session_started", sessionStartedPayload())

	if err := tr.ProcessExited(1, 0); err != nil {
		t.Fatal(err)
	}
	if got := tr.State(); got != ExecCrashed {
		t.Fatalf("Prozessende ohne Ergebnis = %s, erwartet %s", got, ExecCrashed)
	}

	// Der fachliche Auftrag wird nicht aus dem Prozessende abgeleitet: aus „in Bearbeitung“
	// führt kein direkter Weg nach „abgeschlossen“, auch nicht nach erfolgreicher Ausführung.
	if WOInProgress.CanTransition(WOCompleted) {
		t.Fatal("in Bearbeitung -> abgeschlossen darf nicht direkt möglich sein")
	}
	if WOReview.CanTransition(WOCompleted) {
		t.Fatal("Review -> abgeschlossen setzt die Integration voraus")
	}

	// Auch ein erfolgreiches Ergebnis ändert nur den Ausführungszustand.
	ok := NewExecutionTracker("proj-1", "exec-1", 1)
	mustApply(t, ok, "session_started", sessionStartedPayload())
	mustApply(t, ok, "result", map[string]any{"outcome": "succeeded", "summary": "fertig"})
	if ok.State() != ExecSucceeded {
		t.Fatalf("Zustand = %s", ok.State())
	}
	// Das anschließende Prozessende nach terminalem Ergebnis ändert nichts.
	if err := ok.ProcessExited(1, 0); err != nil {
		t.Fatal(err)
	}
	if ok.State() != ExecSucceeded {
		t.Fatalf("Prozessende nach Ergebnis änderte den Zustand auf %s", ok.State())
	}
}

func TestProcessSample(t *testing.T) {
	start := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	cpu, rss := 150.0, uint64(1<<20)
	s := ProcessSample{
		PID: 4242, StartTime: start, ParentPID: 1, ParentStartTime: start.Add(-time.Hour),
		State: ProcRunning, CPUPercent: &cpu, RSSBytes: &rss, SampledAt: start.Add(time.Minute),
	}
	if err := s.Check(); err != nil {
		t.Fatalf("gültige Messung: %v", err)
	}
	// Identität = PID und Startzeit: dieselbe PID mit anderer Startzeit ist ein anderer Prozess.
	reused := s
	reused.StartTime = start.Add(time.Hour)
	if s.Identity() == reused.Identity() {
		t.Fatal("wiederverwendete PID darf nicht dieselbe Identität haben")
	}
	if s.Identity() != (ProcessIdentity{PID: 4242, StartTime: start}) {
		t.Fatal("Identität falsch")
	}
	if s.ParentIdentity() != (ProcessIdentity{PID: 1, StartTime: s.ParentStartTime}) {
		t.Fatal("Elternidentität falsch")
	}

	// Unbekannte Werte bleiben unbekannt (nil), sie werden nicht mit 0 erfunden.
	unknown := s
	unknown.CPUPercent, unknown.RSSBytes = nil, nil
	if err := unknown.Check(); err != nil {
		t.Fatalf("unbekannte Werte sind erlaubt: %v", err)
	}

	for name, mutate := range map[string]func(p *ProcessSample){
		"PID 0":             func(p *ProcessSample) { p.PID = 0 },
		"ohne Startzeit":    func(p *ProcessSample) { p.StartTime = time.Time{} },
		"negative CPU":      func(p *ProcessSample) { v := -1.0; p.CPUPercent = &v },
		"unbekannter Staat": func(p *ProcessSample) { p.State = "flying" },
		"Eltern ohne Zeit":  func(p *ProcessSample) { p.ParentStartTime = time.Time{} },
		"ohne Messzeit":     func(p *ProcessSample) { p.SampledAt = time.Time{} },
		"Messung vor Start": func(p *ProcessSample) { p.SampledAt = p.StartTime.Add(-time.Second) },
	} {
		t.Run(name, func(t *testing.T) {
			bad := s
			mutate(&bad)
			if err := bad.Check(); err == nil {
				t.Fatal("ungültige Messung akzeptiert")
			}
		})
	}
}
