package domain

import (
	"errors"
	"slices"
	"testing"
)

// stepPos ist die Stelle, an der die Schrittfunktion in den Tests steht.
func stepPos(state ExecutionState) ExecutionPosition {
	return ExecutionPosition{ProjectID: "proj-1", ExecutionID: "exec-1", Attempt: 1, State: state}
}

// TestStepOnEventTable: erwarteter Folgezustand je (Zustand, Nachricht), von Hand ausgeschrieben. Die Tabelle ist die
// Regel des Ausführungszustands in lesbarer Form; die Implementierung wird nicht gespiegelt.
func TestStepOnEventTable(t *testing.T) {
	type want struct {
		to       ExecutionState
		terminal bool
		cat      Category // leer: kein Fehler
	}
	activity := map[string]any{"kind": "idle", "summary": "x"}
	tool := map[string]any{"tool_use_id": "t1", "tool": "Bash", "phase": "started"}
	resultCancelled := map[string]any{"outcome": "cancelled", "summary": "abgebrochen"}

	type msg struct {
		typ string
		pl  map[string]any
	}
	msgs := map[string]msg{
		"session_started": {"session_started", sessionStartedPayload()},
		"activity":        {"activity", activity},
		"tool_event":      {"tool_event", tool},
		"question":        {"question", question},
		"result ok":       {"result", resultOK},
		"result fail":     {"result", resultFail},
		"result cancel":   {"result", resultCancelled},
		"error terminal":  {"error", errTerminal},
		"error warning":   {"error", errWarning},
	}
	seq := func(s ExecutionState) want { return want{to: s, cat: CatSequence} }
	ok := func(s ExecutionState) want { return want{to: s} }
	fin := func(s ExecutionState) want { return want{to: s, terminal: true} }

	table := map[ExecutionState]map[string]want{
		ExecInitializing: {
			"session_started": ok(ExecRunning), "activity": seq(ExecInitializing), "tool_event": seq(ExecInitializing),
			"question": seq(ExecInitializing), "result ok": seq(ExecInitializing), "result fail": fin(ExecControlledFailure),
			"result cancel": seq(ExecInitializing), "error terminal": fin(ExecControlledFailure), "error warning": ok(ExecInitializing),
		},
		ExecRunning: {
			"session_started": seq(ExecRunning), "activity": ok(ExecRunning), "tool_event": ok(ExecRunning),
			"question": ok(ExecAwaitingAnswer), "result ok": fin(ExecSucceeded), "result fail": fin(ExecControlledFailure),
			"result cancel": seq(ExecRunning), "error terminal": fin(ExecControlledFailure), "error warning": ok(ExecRunning),
		},
		ExecAwaitingAnswer: {
			"session_started": seq(ExecAwaitingAnswer), "activity": seq(ExecAwaitingAnswer), "tool_event": seq(ExecAwaitingAnswer),
			"question": seq(ExecAwaitingAnswer), "result ok": seq(ExecAwaitingAnswer), "result fail": fin(ExecControlledFailure),
			"result cancel": seq(ExecAwaitingAnswer), "error terminal": fin(ExecControlledFailure), "error warning": ok(ExecAwaitingAnswer),
		},
		ExecStopping: {
			"session_started": seq(ExecStopping), "activity": ok(ExecStopping), "tool_event": ok(ExecStopping),
			"question": seq(ExecStopping), "result ok": fin(ExecSucceeded), "result fail": fin(ExecControlledFailure),
			"result cancel": fin(ExecCancelled), "error terminal": fin(ExecControlledFailure), "error warning": ok(ExecStopping),
		},
	}
	// Nach einem Endzustand verändert keine Nachricht des Versuchs etwas, jede ist ein Fehler der Klasse sequence.
	for _, s := range ExecutionStates() {
		if s.IsTerminal() {
			row := map[string]want{}
			for name := range msgs {
				row[name] = seq(s)
			}
			table[s] = row
		}
	}

	for _, state := range ExecutionStates() {
		for name, m := range msgs {
			t.Run(string(state)+"/"+name, func(t *testing.T) {
				w := table[state][name]
				step, err := StepOnEvent(stepPos(state), eventFor(t, 1, m.typ, m.pl))
				if w.cat != "" {
					wantCategory(t, err, w.cat)
					if step != (ExecutionStep{}) {
						t.Fatalf("bei einem Fehler bleibt der Schritt leer: %+v", step)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				if step.From != state || step.To != w.to {
					t.Fatalf("%s -> %s, erwartet %s -> %s", step.From, step.To, state, w.to)
				}
				if (step.Terminal != nil) != w.terminal {
					t.Fatalf("Terminal = %+v, erwartet terminal=%v", step.Terminal, w.terminal)
				}
				if w.terminal && (step.Terminal.State != w.to || step.Terminal.Attempt != 1) {
					t.Fatalf("Terminal = %+v", step.Terminal)
				}
				if got, want := step.Changed(), state != w.to; got != want {
					t.Fatalf("Changed() = %v, erwartet %v", got, want)
				}
			})
		}
	}
}

func TestStepOnEventSourceAndOutcome(t *testing.T) {
	for name, c := range map[string]struct {
		typ     string
		pl      map[string]any
		source  string
		outcome Outcome
	}{
		"Ergebnis erfolgreich":  {"result", resultOK, TerminalFromResult, OutcomeSucceeded},
		"Ergebnis kontrolliert": {"result", resultFail, TerminalFromResult, OutcomeControlledFailure},
		"terminaler Fehler":     {"error", errTerminal, TerminalFromError, ""},
	} {
		t.Run(name, func(t *testing.T) {
			step, err := StepOnEvent(stepPos(ExecRunning), eventFor(t, 1, c.typ, c.pl))
			if err != nil {
				t.Fatal(err)
			}
			if step.Terminal == nil || step.Terminal.Source != c.source || step.Terminal.ExitCode != 0 || step.Outcome != c.outcome {
				t.Fatalf("Schritt = %+v (Terminal %+v)", step, step.Terminal)
			}
		})
	}
	step, err := StepOnEvent(stepPos(ExecRunning), eventFor(t, 1, "activity", map[string]any{"kind": "idle", "summary": "x"}))
	if err != nil || step.Terminal != nil || step.Outcome != "" {
		t.Fatalf("Aktivität: %+v, %v", step, err)
	}
}

// TestStepOnEventRejectsForeignOrStale: Fremde IDs, alte und noch nicht begonnene Versuche liefern die jeweilige
// Fehlerklasse, gleich in welchem Zustand die Stelle ist.
func TestStepOnEventRejectsForeignOrStale(t *testing.T) {
	pos := ExecutionPosition{ProjectID: "proj-1", ExecutionID: "exec-1", Attempt: 2, State: ExecRunning}
	wantCategory(t, firstErr(StepOnEvent(pos, eventFor(t, 1, "result", resultOK))), CatStale)
	wantCategory(t, firstErr(StepOnEvent(pos, eventFor(t, 3, "result", resultOK))), CatSequence)

	foreign := pos
	foreign.ExecutionID = "exec-9"
	wantCategory(t, firstErr(StepOnEvent(foreign, eventFor(t, 2, "result", resultOK))), CatID)
	foreign = pos
	foreign.ProjectID = "proj-9"
	wantCategory(t, firstErr(StepOnEvent(foreign, eventFor(t, 2, "result", resultOK))), CatID)

	// Ein Payload, der keine Runner-Meldung ist, hat die Klasse message_type.
	bad := eventFor(t, 2, "activity", map[string]any{"kind": "idle", "summary": "x"})
	bad.Payload = struct{ X int }{1}
	wantCategory(t, firstErr(StepOnEvent(pos, bad)), CatMessageType)
}

func firstErr(_ ExecutionStep, err error) error { return err }

func TestStepOnAnswerAndStop(t *testing.T) {
	for _, s := range ExecutionStates() {
		step, err := StepOnAnswerSent(stepPos(s))
		if s == ExecAwaitingAnswer {
			if err != nil || step.From != s || step.To != ExecRunning || step.Terminal != nil {
				t.Errorf("AnswerSent in %s: %+v, %v", s, step, err)
			}
		} else {
			wantCategory(t, err, CatSequence)
		}

		step, err = StepOnStopSent(stepPos(s))
		if s.IsTerminal() || s == ExecStopping {
			wantCategory(t, err, CatSequence)
		} else if err != nil || step.To != ExecStopping || step.Terminal != nil || step.From != s {
			t.Errorf("StopSent in %s: %+v, %v", s, step, err)
		}
	}
}

func TestStepOnProcessExit(t *testing.T) {
	for _, s := range ExecutionStates() {
		step, err := StepOnProcessExit(stepPos(s), 1, 137)
		switch {
		case s.IsTerminal():
			// Nach einem terminalen Ergebnis ändert das Prozessende nichts und ist kein Fehler.
			if err != nil || step.Changed() || step.Terminal != nil || step.To != s {
				t.Errorf("Prozessende nach %s: %+v, %v", s, step, err)
			}
		default:
			want := &TerminalResult{Attempt: 1, State: ExecCrashed, Source: TerminalFromProcessExit, ExitCode: 137}
			if err != nil || step.To != ExecCrashed || step.Terminal == nil || *step.Terminal != *want {
				t.Errorf("Prozessende in %s: %+v, %v", s, step, err)
			}
		}
	}
	// Auch Exit-Code 0 ohne Ergebnis ist ein Absturz.
	step, err := StepOnProcessExit(stepPos(ExecRunning), 1, 0)
	if err != nil || step.To != ExecCrashed || step.Terminal == nil || step.Terminal.Source != TerminalFromProcessExit {
		t.Errorf("Exit 0 ohne Ergebnis: %+v, %v", step, err)
	}
	pos := ExecutionPosition{ProjectID: "proj-1", ExecutionID: "exec-1", Attempt: 2, State: ExecRunning}
	wantCategory(t, firstErr(StepOnProcessExit(pos, 1, 9)), CatStale)
	wantCategory(t, firstErr(StepOnProcessExit(pos, 3, 9)), CatSequence)
}

// TestTrackerAndStepAgree: der Tracker führt dieselbe Regel wie die reine Funktion. Über alle Zustände und alle
// Nachrichtenarten (auch fremde IDs, alte und künftige Versuche) liefern beide denselben Folgezustand, dasselbe terminale
// Ergebnis und dieselbe Fehlerklasse; ein Fehler lässt den Tracker unverändert.
func TestTrackerAndStepAgree(t *testing.T) {
	cases := []struct {
		name string
		ev   func(t *testing.T, attempt int) RunEvent
	}{
		{"session_started", func(t *testing.T, a int) RunEvent { return eventFor(t, a, "session_started", sessionStartedPayload()) }},
		{"activity", func(t *testing.T, a int) RunEvent {
			return eventFor(t, a, "activity", map[string]any{"kind": "idle", "summary": "x"})
		}},
		{"tool_event", func(t *testing.T, a int) RunEvent {
			return eventFor(t, a, "tool_event", map[string]any{"tool_use_id": "t1", "tool": "Bash", "phase": "started"})
		}},
		{"question", func(t *testing.T, a int) RunEvent { return eventFor(t, a, "question", question) }},
		{"result ok", func(t *testing.T, a int) RunEvent { return eventFor(t, a, "result", resultOK) }},
		{"result fail", func(t *testing.T, a int) RunEvent { return eventFor(t, a, "result", resultFail) }},
		{"result cancel", func(t *testing.T, a int) RunEvent {
			return eventFor(t, a, "result", map[string]any{"outcome": "cancelled", "summary": "x"})
		}},
		{"error terminal", func(t *testing.T, a int) RunEvent { return eventFor(t, a, "error", errTerminal) }},
		{"error warning", func(t *testing.T, a int) RunEvent { return eventFor(t, a, "error", errWarning) }},
	}
	category := func(err error) Category {
		var ce *ContractError
		if errors.As(err, &ce) {
			return ce.Category()
		}
		if err != nil {
			t.Fatalf("kein ContractError: %v", err)
		}
		return ""
	}
	for _, state := range ExecutionStates() {
		for _, c := range cases {
			for _, attempt := range []int{1, 2, 3} { // aktuell ist Versuch 2
				name := string(state) + "/" + c.name + "/" + string(rune('0'+attempt))
				t.Run(name, func(t *testing.T) {
					tr := &ExecutionTracker{projectID: "proj-1", executionID: "exec-1", attempt: 2, state: state}
					if state.IsTerminal() {
						tr.terminal = &TerminalResult{Attempt: 2, State: state, Source: TerminalFromResult}
					}
					before, hadBefore := tr.Terminal()
					ev := c.ev(t, attempt)
					step, stepErr := StepOnEvent(ExecutionPosition{ProjectID: "proj-1", ExecutionID: "exec-1", Attempt: 2, State: state}, ev)
					trErr := tr.Apply(ev)
					if category(stepErr) != category(trErr) {
						t.Fatalf("Fehlerklasse: Funktion %q, Tracker %q", category(stepErr), category(trErr))
					}
					if stepErr != nil {
						if tr.State() != state {
							t.Fatalf("Fehler änderte den Tracker: %s -> %s", state, tr.State())
						}
						if got, had := tr.Terminal(); had != hadBefore || got != before {
							t.Fatal("Fehler änderte das terminale Ergebnis")
						}
						return
					}
					if tr.State() != step.To {
						t.Fatalf("Folgezustand: Funktion %s, Tracker %s", step.To, tr.State())
					}
					got, had := tr.Terminal()
					switch {
					case step.Terminal != nil:
						if !had || got != *step.Terminal {
							t.Fatalf("terminales Ergebnis: Funktion %+v, Tracker %+v (%v)", *step.Terminal, got, had)
						}
					case had != hadBefore || got != before:
						t.Fatal("ohne terminalen Schritt verändert der Tracker sein Ergebnis")
					}
				})
			}
		}

		t.Run(string(state)+"/Antwort, Stopp, Prozessende", func(t *testing.T) {
			pos := ExecutionPosition{ProjectID: "proj-1", ExecutionID: "exec-1", Attempt: 2, State: state}
			mk := func() *ExecutionTracker {
				tr := &ExecutionTracker{projectID: "proj-1", executionID: "exec-1", attempt: 2, state: state}
				if state.IsTerminal() {
					tr.terminal = &TerminalResult{Attempt: 2, State: state, Source: TerminalFromResult}
				}
				return tr
			}
			type op struct {
				name string
				step func() (ExecutionStep, error)
				do   func(*ExecutionTracker) error
			}
			for _, o := range []op{
				{"AnswerSent", func() (ExecutionStep, error) { return StepOnAnswerSent(pos) }, func(tr *ExecutionTracker) error { return tr.AnswerSent() }},
				{"StopSent", func() (ExecutionStep, error) { return StepOnStopSent(pos) }, func(tr *ExecutionTracker) error { return tr.StopSent() }},
				{"ProcessExited(2)", func() (ExecutionStep, error) { return StepOnProcessExit(pos, 2, 3) }, func(tr *ExecutionTracker) error { return tr.ProcessExited(2, 3) }},
				{"ProcessExited(1)", func() (ExecutionStep, error) { return StepOnProcessExit(pos, 1, 3) }, func(tr *ExecutionTracker) error { return tr.ProcessExited(1, 3) }},
				{"ProcessExited(3)", func() (ExecutionStep, error) { return StepOnProcessExit(pos, 3, 3) }, func(tr *ExecutionTracker) error { return tr.ProcessExited(3, 3) }},
			} {
				tr := mk()
				step, stepErr := o.step()
				trErr := o.do(tr)
				if category(stepErr) != category(trErr) {
					t.Errorf("%s: Fehlerklasse Funktion %q, Tracker %q", o.name, category(stepErr), category(trErr))
					continue
				}
				if stepErr == nil && tr.State() != step.To {
					t.Errorf("%s: Folgezustand Funktion %s, Tracker %s", o.name, step.To, tr.State())
				}
				if stepErr != nil && tr.State() != state {
					t.Errorf("%s: Fehler änderte den Tracker", o.name)
				}
			}
		})
	}
	if !slices.Contains(ExecutionStates(), ExecCancelled) {
		t.Fatal("Zustandsliste unvollständig")
	}
}
