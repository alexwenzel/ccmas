package domain

import (
	"errors"
	"testing"
	"time"
)

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func sessionStartedPayload() map[string]any {
	return map[string]any{"session_id": "sess-1", "model": "model-a", "effort": "high", "claude_version": "2.1.311"}
}

// eventFor baut eine gültige Runner-Nachricht für den Tracker-Test.
func eventFor(t *testing.T, attempt int, typ string, payload map[string]any) RunEvent {
	t.Helper()
	m := baseEvent(typ, payload)
	m["attempt"] = attempt
	ev, err := ParseRunEvent(mustJSON(t, m))
	if err != nil {
		t.Fatalf("Testnachricht %s ungültig: %v", typ, err)
	}
	return ev
}

func mustApply(t *testing.T, tr *ExecutionTracker, typ string, payload map[string]any) {
	t.Helper()
	if err := tr.Apply(eventFor(t, tr.Attempt(), typ, payload)); err != nil {
		t.Fatalf("Apply(%s): %v", typ, err)
	}
}

var (
	resultOK    = map[string]any{"outcome": "succeeded", "summary": "ok"}
	resultFail  = map[string]any{"outcome": "controlled_failure", "summary": "Tests rot"}
	errTerminal = map[string]any{"code": "sdk_failure", "message": "SDK-Fehler", "kind": "runtime", "terminal": true}
	errWarning  = map[string]any{"code": "slow_tool", "message": "Werkzeug langsam", "kind": "runtime", "terminal": false}
	question    = map[string]any{"question_id": "q-1", "question": "Welche Variante?", "context": "Kontext"}
)

// TestAttemptHasAtMostOneTerminalResult (Akzeptanzkriterium 2).
func TestAttemptHasAtMostOneTerminalResult(t *testing.T) {
	for name, second := range map[string]struct {
		typ     string
		payload map[string]any
	}{
		"zweites Ergebnis":                {"result", resultOK},
		"Ergebnis nach terminalem Fehler": {"result", resultFail},
		"terminaler Fehler":               {"error", errTerminal},
	} {
		t.Run(name, func(t *testing.T) {
			tr := NewExecutionTracker("proj-1", "exec-1", 1)
			mustApply(t, tr, "session_started", sessionStartedPayload())
			mustApply(t, tr, "result", resultOK)
			if tr.State() != ExecSucceeded {
				t.Fatalf("Zustand = %s", tr.State())
			}
			first, _ := tr.Terminal()

			err := tr.Apply(eventFor(t, 1, second.typ, second.payload))
			wantCategory(t, err, CatSequence)
			if tr.State() != ExecSucceeded {
				t.Fatalf("zweites terminales Ergebnis änderte den Zustand auf %s", tr.State())
			}
			if got, _ := tr.Terminal(); got != first {
				t.Fatalf("terminales Ergebnis verändert: %+v -> %+v", first, got)
			}
		})
	}

	t.Run("terminaler Fehler zuerst, dann Ergebnis", func(t *testing.T) {
		tr := NewExecutionTracker("proj-1", "exec-1", 1)
		mustApply(t, tr, "session_started", sessionStartedPayload())
		mustApply(t, tr, "error", errTerminal)
		if tr.State() != ExecControlledFailure {
			t.Fatalf("Zustand = %s", tr.State())
		}
		wantCategory(t, tr.Apply(eventFor(t, 1, "result", resultOK)), CatSequence)
		if tr.State() != ExecControlledFailure {
			t.Fatalf("Zustand = %s", tr.State())
		}
	})

	t.Run("Nachrichten nach dem Ergebnis", func(t *testing.T) {
		tr := NewExecutionTracker("proj-1", "exec-1", 1)
		mustApply(t, tr, "session_started", sessionStartedPayload())
		mustApply(t, tr, "result", resultFail)
		for typ, p := range map[string]map[string]any{
			"activity":        {"kind": "idle", "summary": "x"},
			"tool_event":      {"tool_use_id": "t1", "tool": "Bash", "phase": "started"},
			"question":        question,
			"session_started": sessionStartedPayload(),
			"error":           errWarning,
		} {
			wantCategory(t, tr.Apply(eventFor(t, 1, typ, p)), CatSequence)
			if tr.State() != ExecControlledFailure {
				t.Fatalf("%s nach Ergebnis änderte den Zustand auf %s", typ, tr.State())
			}
		}
	})

	t.Run("nicht terminaler Fehler ist kein Ergebnis", func(t *testing.T) {
		tr := NewExecutionTracker("proj-1", "exec-1", 1)
		mustApply(t, tr, "session_started", sessionStartedPayload())
		mustApply(t, tr, "error", errWarning)
		if tr.State() != ExecRunning {
			t.Fatalf("Zustand = %s", tr.State())
		}
		if _, ok := tr.Terminal(); ok {
			t.Fatal("nicht terminaler Fehler darf kein terminales Ergebnis sein")
		}
		mustApply(t, tr, "result", resultOK)
	})
}

// TestStaleAttemptDoesNotChangeState: alte, verspätete Nachrichten verändern den Zustand
// eines neuen Versuchs nicht.
func TestStaleAttemptDoesNotChangeState(t *testing.T) {
	tr := NewExecutionTracker("proj-1", "exec-1", 1)
	mustApply(t, tr, "session_started", sessionStartedPayload())
	if err := tr.ProcessExited(1, 137); err != nil {
		t.Fatal(err)
	}
	if tr.State() != ExecCrashed {
		t.Fatalf("Zustand = %s", tr.State())
	}

	if err := tr.BeginAttempt(2); err != nil {
		t.Fatal(err)
	}
	if tr.Attempt() != 2 || tr.State() != ExecInitializing {
		t.Fatalf("Versuch %d, Zustand %s", tr.Attempt(), tr.State())
	}
	mustApply(t, tr, "session_started", sessionStartedPayload())

	for typ, p := range map[string]map[string]any{
		"result": resultOK, "error": errTerminal, "question": question,
		"activity": {"kind": "idle", "summary": "alt"},
	} {
		err := tr.Apply(eventFor(t, 1, typ, p))
		wantCategory(t, err, CatStale)
		if tr.State() != ExecRunning || tr.Attempt() != 2 {
			t.Fatalf("verspätete %s-Nachricht aus Versuch 1 änderte Versuch %d auf %s", typ, tr.Attempt(), tr.State())
		}
	}
	if _, ok := tr.Terminal(); ok {
		t.Fatal("Versuch 2 hat noch kein terminales Ergebnis")
	}

	// Ein noch nicht begonnener Versuch wird nicht stillschweigend übernommen.
	wantCategory(t, tr.Apply(eventFor(t, 3, "result", resultOK)), CatSequence)
	if tr.State() != ExecRunning || tr.Attempt() != 2 {
		t.Fatal("Nachricht aus unbekanntem Versuch änderte den Zustand")
	}

	mustApply(t, tr, "result", resultOK)
	if tr.State() != ExecSucceeded {
		t.Fatalf("Zustand = %s", tr.State())
	}
}

func TestBeginAttemptRules(t *testing.T) {
	tr := NewExecutionTracker("proj-1", "exec-1", 1)
	mustApply(t, tr, "session_started", sessionStartedPayload())
	// Ein laufender Versuch kann nicht überschrieben werden.
	wantCategory(t, tr.BeginAttempt(2), CatSequence)
	mustApply(t, tr, "result", resultOK)
	// Versuchsnummern wachsen streng monoton.
	wantCategory(t, tr.BeginAttempt(1), CatSequence)
	wantCategory(t, tr.BeginAttempt(3), CatSequence)
	if err := tr.BeginAttempt(2); err != nil {
		t.Fatal(err)
	}
}

func TestTrackerStateMachine(t *testing.T) {
	t.Run("Frage und Antwort", func(t *testing.T) {
		tr := NewExecutionTracker("proj-1", "exec-1", 1)
		mustApply(t, tr, "session_started", sessionStartedPayload())
		mustApply(t, tr, "question", question)
		if tr.State() != ExecAwaitingAnswer {
			t.Fatalf("Zustand = %s", tr.State())
		}
		// Während der Rückfrage meldet der Runner keine weitere Frage und keine Werkzeuge.
		wantCategory(t, tr.Apply(eventFor(t, 1, "question", question)), CatSequence)
		wantCategory(t, tr.Apply(eventFor(t, 1, "tool_event", map[string]any{"tool_use_id": "t1", "tool": "Bash", "phase": "started"})), CatSequence)
		if err := tr.AnswerSent(); err != nil {
			t.Fatal(err)
		}
		if tr.State() != ExecRunning {
			t.Fatalf("Zustand = %s", tr.State())
		}
		wantCategory(t, tr.AnswerSent(), CatSequence)
	})

	t.Run("Ereignisse vor session_started", func(t *testing.T) {
		tr := NewExecutionTracker("proj-1", "exec-1", 1)
		wantCategory(t, tr.Apply(eventFor(t, 1, "activity", map[string]any{"kind": "idle", "summary": "x"})), CatSequence)
		wantCategory(t, tr.Apply(eventFor(t, 1, "result", resultOK)), CatSequence)
		if tr.State() != ExecInitializing {
			t.Fatalf("Zustand = %s", tr.State())
		}
		// Ein Fehler beim Start ist ein kontrolliertes terminales Ergebnis.
		mustApply(t, tr, "error", errTerminal)
		if tr.State() != ExecControlledFailure {
			t.Fatalf("Zustand = %s", tr.State())
		}
	})

	t.Run("Abbruch nur über Stopp", func(t *testing.T) {
		cancelled := map[string]any{"outcome": "cancelled", "summary": "abgebrochen"}
		tr := NewExecutionTracker("proj-1", "exec-1", 1)
		mustApply(t, tr, "session_started", sessionStartedPayload())
		// Der Runner kann sich nicht selbst „abgebrochen“ melden.
		wantCategory(t, tr.Apply(eventFor(t, 1, "result", cancelled)), CatSequence)
		if err := tr.StopSent(); err != nil {
			t.Fatal(err)
		}
		if tr.State() != ExecStopping {
			t.Fatalf("Zustand = %s", tr.State())
		}
		mustApply(t, tr, "result", cancelled)
		if tr.State() != ExecCancelled {
			t.Fatalf("Zustand = %s", tr.State())
		}
		wantCategory(t, tr.StopSent(), CatSequence)
	})

	t.Run("falsches Projekt oder falsche Ausführung", func(t *testing.T) {
		tr := NewExecutionTracker("proj-1", "exec-1", 1)
		m := baseEvent("session_started", sessionStartedPayload())
		m["execution_id"] = "exec-other"
		ev, err := ParseRunEvent(mustJSON(t, m))
		if err != nil {
			t.Fatal(err)
		}
		wantCategory(t, tr.Apply(ev), CatID)
		m = baseEvent("session_started", sessionStartedPayload())
		m["project_id"] = "proj-other"
		ev, _ = ParseRunEvent(mustJSON(t, m))
		wantCategory(t, tr.Apply(ev), CatID)
		if tr.State() != ExecInitializing {
			t.Fatal("fremde Nachricht änderte den Zustand")
		}
	})

	t.Run("Fehler sind ContractError", func(t *testing.T) {
		tr := NewExecutionTracker("proj-1", "exec-1", 1)
		err := tr.Apply(eventFor(t, 1, "result", resultOK))
		var ce *ContractError
		if !errors.As(err, &ce) {
			t.Fatalf("erwartet *ContractError, erhalten %T", err)
		}
	})
}
