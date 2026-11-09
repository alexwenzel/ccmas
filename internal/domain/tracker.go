package domain

import "sync"

// TerminalResult ist das eine terminale Ergebnis eines Versuchs.
type TerminalResult struct {
	Attempt int
	State   ExecutionState
	// Source nennt die Quelle: "result", "error" oder "process_exit".
	Source string
	// ExitCode ist nur bei Source "process_exit" belegt.
	ExitCode int
}

// ExecutionTracker prüft die Nachrichtenfolge einer Ausführung und führt den
// Ausführungszustand des aktuellen Versuchs. Er erzwingt:
//
//   - pro (execution_id, attempt) höchstens ein terminales Ergebnis (result oder
//     terminaler error, sonst Prozessende ohne Ergebnis = Absturz);
//   - Nachrichten nach dem terminalen Ergebnis verändern nichts;
//   - Nachrichten älterer Versuche (CatStale) und nicht begonnener Versuche (CatSequence)
//     verändern den Zustand nicht;
//   - nur erlaubte Zustandsübergänge (ExecutionState.CanTransition).
//
// Der Tracker kennt den Auftragszustand nicht und leitet ihn nicht ab: Ein Prozessende mit
// Code 0 ohne Ergebnis ist ein Absturz, aber kein Erfolg und kein Auftragsabschluss.
type ExecutionTracker struct {
	mu          sync.Mutex
	projectID   string
	executionID string
	attempt     int
	state       ExecutionState
	terminal    *TerminalResult
}

// NewExecutionTracker beginnt mit dem ersten Versuch im Zustand „Initialisierung“.
func NewExecutionTracker(projectID, executionID string, attempt int) *ExecutionTracker {
	return &ExecutionTracker{projectID: projectID, executionID: executionID, attempt: attempt, state: ExecInitializing}
}

// Attempt liefert den aktuellen Versuch.
func (t *ExecutionTracker) Attempt() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.attempt
}

// State liefert den Zustand des aktuellen Versuchs.
func (t *ExecutionTracker) State() ExecutionState {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.state
}

// Terminal liefert das terminale Ergebnis des aktuellen Versuchs, falls vorhanden.
func (t *ExecutionTracker) Terminal() (TerminalResult, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.terminal == nil {
		return TerminalResult{}, false
	}
	return *t.terminal, true
}

// BeginAttempt eröffnet den nächsten Versuch (zum Beispiel Resume). Der bisherige Versuch
// muss beendet sein, die Nummer wächst um genau eins.
func (t *ExecutionTracker) BeginAttempt(attempt int) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.state.IsTerminal() {
		return newError(CatSequence, "", "Versuch %d läuft noch (%s)", t.attempt, t.state)
	}
	if attempt != t.attempt+1 {
		return newError(CatSequence, "", "nächster Versuch muss %d sein, nicht %d", t.attempt+1, attempt)
	}
	t.attempt, t.state, t.terminal = attempt, ExecInitializing, nil
	return nil
}

func (t *ExecutionTracker) transition(to ExecutionState) error {
	if !t.state.CanTransition(to) {
		return newError(CatSequence, "", "Übergang %s -> %s ist nicht erlaubt", t.state, to)
	}
	t.state = to
	return nil
}

func (t *ExecutionTracker) finish(to ExecutionState, source string, exitCode int) error {
	if err := t.transition(to); err != nil {
		return err
	}
	t.terminal = &TerminalResult{Attempt: t.attempt, State: to, Source: source, ExitCode: exitCode}
	return nil
}

// Apply verarbeitet eine Nachricht des Runners. Ein Fehler lässt den Zustand unverändert.
func (t *ExecutionTracker) Apply(ev RunEvent) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	env := ev.Envelope
	if env.ProjectID != t.projectID || env.ExecutionID != t.executionID {
		return newError(CatID, "", "Nachricht gehört zu %s/%s, nicht zu %s/%s",
			env.ProjectID, env.ExecutionID, t.projectID, t.executionID)
	}
	if env.Attempt < t.attempt {
		return newError(CatStale, "/attempt", "Nachricht aus Versuch %d, aktuell ist Versuch %d", env.Attempt, t.attempt)
	}
	if env.Attempt > t.attempt {
		return newError(CatSequence, "/attempt", "Versuch %d wurde nicht begonnen (aktuell %d)", env.Attempt, t.attempt)
	}
	if t.state.IsTerminal() {
		return newError(CatSequence, "", "Versuch %d ist bereits beendet (%s); %s wird nicht übernommen", t.attempt, t.state, env.Type)
	}

	switch p := ev.Payload.(type) {
	case SessionStarted:
		if t.state != ExecInitializing {
			return newError(CatSequence, "", "session_started im Zustand %s", t.state)
		}
		return t.transition(ExecRunning)
	case Activity, ToolEvent:
		if t.state != ExecRunning && t.state != ExecStopping {
			return newError(CatSequence, "", "%s im Zustand %s", env.Type, t.state)
		}
		return nil
	case QuestionPayload:
		if t.state != ExecRunning {
			return newError(CatSequence, "", "question im Zustand %s", t.state)
		}
		return t.transition(ExecAwaitingAnswer)
	case Result:
		switch p.Outcome {
		case OutcomeSucceeded:
			return t.finish(ExecSucceeded, "result", 0)
		case OutcomeControlledFailure:
			return t.finish(ExecControlledFailure, "result", 0)
		case OutcomeCancelled:
			return t.finish(ExecCancelled, "result", 0)
		}
		return newError(CatValue, "/payload/outcome", "unbekanntes Ergebnis %q", p.Outcome)
	case ErrorPayload:
		if !p.Terminal {
			return nil // Warnung ohne Zustandswirkung
		}
		return t.finish(ExecControlledFailure, "error", 0)
	}
	return newError(CatMessageType, "", "Payload %T ist keine Runner-Meldung", ev.Payload)
}

// AnswerSent vermerkt, dass die Antwort an den Runner gesendet wurde (wartet -> läuft).
func (t *ExecutionTracker) AnswerSent() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.state != ExecAwaitingAnswer {
		return newError(CatSequence, "", "answer im Zustand %s", t.state)
	}
	return t.transition(ExecRunning)
}

// StopSent vermerkt, dass Interrupt oder Stopp gesendet wurde (-> wird beendet).
func (t *ExecutionTracker) StopSent() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.transition(ExecStopping)
}

// ProcessExited meldet das Ende des Runner-Prozesses. Ohne vorheriges terminales Ergebnis ist
// das ein Absturz, auch bei Exit-Code 0. Nach einem terminalen Ergebnis ändert es nichts.
func (t *ExecutionTracker) ProcessExited(attempt, exitCode int) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if attempt < t.attempt {
		return newError(CatStale, "", "Prozess gehört zu Versuch %d, aktuell ist Versuch %d", attempt, t.attempt)
	}
	if attempt > t.attempt {
		return newError(CatSequence, "", "Versuch %d wurde nicht begonnen (aktuell %d)", attempt, t.attempt)
	}
	if t.state.IsTerminal() {
		return nil
	}
	return t.finish(ExecCrashed, "process_exit", exitCode)
}
