package domain

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func validExecution() Execution {
	return Execution{
		ExecutionID: "exec-1", ProjectID: "demo", WorkOrderID: "wo-1", AgentID: "agent-1",
		Role: RoleImplementer, CreatedAt: time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC),
	}
}

func validAttempt() ExecutionAttempt {
	start := time.Date(2026, 11, 10, 12, 0, 1, 0, time.UTC)
	return ExecutionAttempt{
		ExecutionID: "exec-1", Attempt: 1, ProfileID: "implementer", ProfileVersion: 1,
		SkillSnapshotID: "sha256:" + strings.Repeat("a", 64), SessionID: "sess-1",
		State: ExecRunning, Process: &ProcessIdentity{PID: 4711, StartTime: start},
		OSProcessState: ProcSleeping, OSObservedAt: start.Add(time.Second),
		Worktree: "/work/demo/wo-1", Branch: "feature/wo-1", StartedAt: start,
		Spec: json.RawMessage(`{"role":"implementer","prompt_bytes":10,"prompt_sha256":"x"}`),
	}
}

func finished(a ExecutionAttempt, state ExecutionState, source string, exit int, outcome Outcome) ExecutionAttempt {
	a.State = state
	a.EndedAt = a.StartedAt.Add(time.Minute)
	a.Terminal = &TerminalResult{Attempt: a.Attempt, State: state, Source: source, ExitCode: exit}
	a.Outcome = outcome
	a.Summary = "fertig"
	return a
}

func TestExecutionCheck(t *testing.T) {
	if err := validExecution().Check(); err != nil {
		t.Fatalf("gültige Ausführung abgewiesen: %v", err)
	}
	// Auftrag und Agent sind optional (etwa Supervisor-Planung ohne Auftrag).
	e := validExecution()
	e.WorkOrderID, e.AgentID = "", ""
	if err := e.Check(); err != nil {
		t.Fatalf("Ausführung ohne Auftrag und Agent: %v", err)
	}
	cases := map[string]struct {
		mutate func(e *Execution)
		want   Category
	}{
		"ID ungültig":      {func(e *Execution) { e.ExecutionID = "a b" }, CatID},
		"Projekt leer":     {func(e *Execution) { e.ProjectID = "" }, CatID},
		"Auftrag ungültig": {func(e *Execution) { e.WorkOrderID = "../x" }, CatID},
		"Agent ungültig":   {func(e *Execution) { e.AgentID = "a/b" }, CatID},
		"Rolle unbekannt":  {func(e *Execution) { e.Role = "boss" }, CatRole},
		"Zeit fehlt":       {func(e *Execution) { e.CreatedAt = time.Time{} }, CatTimestamp},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			e := validExecution()
			tc.mutate(&e)
			wantCategory(t, e.Check(), tc.want)
		})
	}
}

func TestExecutionAttemptCheck(t *testing.T) {
	if err := validAttempt().Check(); err != nil {
		t.Fatalf("gültiger Versuch abgewiesen: %v", err)
	}
	// Alle vier terminalen Wege, jeweils mit zum Zustand passender Quelle.
	for name, a := range map[string]ExecutionAttempt{
		"result succeeded":  finished(validAttempt(), ExecSucceeded, "result", 0, OutcomeSucceeded),
		"result failure":    finished(validAttempt(), ExecControlledFailure, "result", 0, OutcomeControlledFailure),
		"result cancelled":  finished(validAttempt(), ExecCancelled, "result", 0, OutcomeCancelled),
		"terminaler Fehler": finished(validAttempt(), ExecControlledFailure, "error", 0, ""),
		"Absturz":           finished(validAttempt(), ExecCrashed, "process_exit", 0, ""),
		"Absturz mit Code":  finished(validAttempt(), ExecCrashed, "process_exit", 137, ""),
	} {
		if err := a.Check(); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	// Ohne Prozess und Profil-Skills ist ein früher Versuch gültig.
	early := validAttempt()
	early.State, early.Process, early.OSProcessState, early.OSObservedAt = ExecInitializing, nil, "", time.Time{}
	early.SkillSnapshotID, early.SessionID, early.Worktree, early.Branch, early.Spec = "", "", "", "", nil
	if err := early.Check(); err != nil {
		t.Fatalf("früher Versuch: %v", err)
	}

	cases := map[string]struct {
		mutate func(a *ExecutionAttempt)
		want   Category
	}{
		"Ausführungs-ID":            {func(a *ExecutionAttempt) { a.ExecutionID = "" }, CatID},
		"Versuch 0":                 {func(a *ExecutionAttempt) { a.Attempt = 0 }, CatValue},
		"Profil-ID":                 {func(a *ExecutionAttempt) { a.ProfileID = "a b" }, CatID},
		"Profilversion 0":           {func(a *ExecutionAttempt) { a.ProfileVersion = 0 }, CatValue},
		"Snapshot-ID":               {func(a *ExecutionAttempt) { a.SkillSnapshotID = "a b" }, CatID},
		"Session mit Steuerzeichen": {func(a *ExecutionAttempt) { a.SessionID = "x\ny" }, CatValue},
		"Zustand unbekannt":         {func(a *ExecutionAttempt) { a.State = "paused" }, CatState},
		"PID 0":                     {func(a *ExecutionAttempt) { a.Process.PID = 0 }, CatValue},
		"Prozess ohne Startzeit":    {func(a *ExecutionAttempt) { a.Process.StartTime = time.Time{} }, CatTimestamp},
		"OS-Zustand unbekannt":      {func(a *ExecutionAttempt) { a.OSProcessState = "dreaming" }, CatState},
		"OS-Zustand ohne Zeit":      {func(a *ExecutionAttempt) { a.OSObservedAt = time.Time{} }, CatTimestamp},
		"Zeit ohne OS-Zustand":      {func(a *ExecutionAttempt) { a.OSProcessState = "" }, CatValue},
		"OS-Zustand ohne Prozess":   {func(a *ExecutionAttempt) { a.Process = nil }, CatValue},
		"Worktree relativ":          {func(a *ExecutionAttempt) { a.Worktree = "work/x" }, CatPath},
		"Branch mit Steuerzeichen":  {func(a *ExecutionAttempt) { a.Branch = "a\tb" }, CatValue},
		"Start fehlt":               {func(a *ExecutionAttempt) { a.StartedAt = time.Time{} }, CatTimestamp},
		"Ende vor Start": {func(a *ExecutionAttempt) {
			*a = finished(*a, ExecSucceeded, "result", 0, OutcomeSucceeded)
			a.EndedAt = a.StartedAt.Add(-time.Second)
		}, CatTimestamp},
		"terminal ohne Ergebnis": {func(a *ExecutionAttempt) { a.State = ExecSucceeded }, CatState},
		"Ergebnis ohne terminalen Zustand": {func(a *ExecutionAttempt) {
			*a = finished(*a, ExecSucceeded, "result", 0, OutcomeSucceeded)
			a.State = ExecRunning
		}, CatState},
		"Ergebnis eines anderen Versuchs": {func(a *ExecutionAttempt) {
			*a = finished(*a, ExecSucceeded, "result", 0, OutcomeSucceeded)
			a.Terminal.Attempt = 2
		}, CatStale},
		"Ergebnis mit anderem Zustand": {func(a *ExecutionAttempt) {
			*a = finished(*a, ExecSucceeded, "result", 0, OutcomeSucceeded)
			a.Terminal.State = ExecCrashed
		}, CatState},
		"Quelle unbekannt": {func(a *ExecutionAttempt) {
			*a = finished(*a, ExecCrashed, "magic", 0, "")
		}, CatValue},
		"Exit-Code ohne Prozessende": {func(a *ExecutionAttempt) {
			*a = finished(*a, ExecSucceeded, "result", 3, OutcomeSucceeded)
		}, CatValue},
		"Outcome fehlt bei result": {func(a *ExecutionAttempt) {
			*a = finished(*a, ExecSucceeded, "result", 0, "")
		}, CatValue},
		"Outcome passt nicht zum Zustand": {func(a *ExecutionAttempt) {
			*a = finished(*a, ExecSucceeded, "result", 0, OutcomeCancelled)
		}, CatState},
		"Outcome bei Absturz": {func(a *ExecutionAttempt) {
			*a = finished(*a, ExecCrashed, "process_exit", 0, OutcomeSucceeded)
		}, CatValue},
		"Absturz als Erfolg": {func(a *ExecutionAttempt) {
			*a = finished(*a, ExecSucceeded, "process_exit", 0, "")
		}, CatState},
		"Fehler als Erfolg": {func(a *ExecutionAttempt) {
			*a = finished(*a, ExecSucceeded, "error", 0, "")
		}, CatState},
		"Zusammenfassung zu lang":           {func(a *ExecutionAttempt) { a.Summary = strings.Repeat("x", MaxAttemptSummaryBytes+1) }, CatSize},
		"Zusammenfassung mit Steuerzeichen": {func(a *ExecutionAttempt) { a.Summary = "a\x00b" }, CatValue},
		"Spec kein Objekt":                  {func(a *ExecutionAttempt) { a.Spec = json.RawMessage(`[1]`) }, CatValue},
		"Spec kein JSON":                    {func(a *ExecutionAttempt) { a.Spec = json.RawMessage(`{`) }, CatSyntax},
		"Spec mit Prompt":                   {func(a *ExecutionAttempt) { a.Spec = json.RawMessage(`{"prompt":"geheim"}`) }, CatValue},
		"Spec mit Systemprompt":             {func(a *ExecutionAttempt) { a.Spec = json.RawMessage(`{"system_prompt":"x"}`) }, CatValue},
		"Spec zu groß": {func(a *ExecutionAttempt) {
			a.Spec = json.RawMessage(`{"x":"` + strings.Repeat("a", MaxAttemptSpecBytes) + `"}`)
		}, CatSize},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			a := validAttempt()
			tc.mutate(&a)
			wantCategory(t, a.Check(), tc.want)
		})
	}
}

// TestAttemptLayersAreSeparateFields: Ausführungs-, Prozess- und Auftragsebene teilen sich keinen Typ.
func TestAttemptLayersAreSeparateFields(t *testing.T) {
	a := validAttempt()
	a.State = ExecSucceeded // ein Zustandswechsel der Ausführung ...
	if a.OSProcessState != ProcSleeping || a.Process == nil {
		t.Fatal("... ändert den beobachteten Prozesszustand nicht")
	}
}

func TestAgentCheck(t *testing.T) {
	a := Agent{ProjectID: "demo", AgentID: "agent-1", Role: RoleImplementer, Availability: AgentAvailable, Revision: 1,
		UpdatedAt: time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)}
	if err := a.Check(); err != nil {
		t.Fatalf("gültiger Agent: %v", err)
	}
	for name, mutate := range map[string]func(a *Agent) Category{
		"ID":            func(a *Agent) Category { a.AgentID = "x y"; return CatID },
		"Projekt":       func(a *Agent) Category { a.ProjectID = ""; return CatID },
		"Rolle":         func(a *Agent) Category { a.Role = "x"; return CatRole },
		"Verfügbarkeit": func(a *Agent) Category { a.Availability = "away"; return CatState },
		"Revision":      func(a *Agent) Category { a.Revision = 0; return CatValue },
		"Zeit":          func(a *Agent) Category { a.UpdatedAt = time.Time{}; return CatTimestamp },
	} {
		t.Run(name, func(t *testing.T) {
			b := a
			want := mutate(&b)
			wantCategory(t, b.Check(), want)
		})
	}
}
