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
// Die Regel selbst steht in den reinen Funktionen von execution_step.go (StepOnEvent und Verwandte); der Tracker hält nur
// den Zustand und übernimmt deren Schritte. Er kennt den Auftragszustand nicht und leitet ihn nicht ab: Ein Prozessende mit
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

// position ist die aktuelle Stelle des Trackers für die reinen Schrittfunktionen (execution_step.go).
func (t *ExecutionTracker) position() ExecutionPosition {
	return ExecutionPosition{ProjectID: t.projectID, ExecutionID: t.executionID, Attempt: t.attempt, State: t.state}
}

// take übernimmt einen Schritt der reinen Funktionen; ein Fehler lässt den Zustand unverändert.
func (t *ExecutionTracker) take(step ExecutionStep, err error) error {
	if err != nil {
		return err
	}
	t.state = step.To
	if step.Terminal != nil {
		t.terminal = step.Terminal
	}
	return nil
}

// Apply verarbeitet eine Nachricht des Runners (Regel: StepOnEvent). Ein Fehler lässt den Zustand unverändert.
func (t *ExecutionTracker) Apply(ev RunEvent) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.take(StepOnEvent(t.position(), ev))
}

// AnswerSent vermerkt, dass die Antwort an den Runner gesendet wurde (wartet -> läuft).
func (t *ExecutionTracker) AnswerSent() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.take(StepOnAnswerSent(t.position()))
}

// StopSent vermerkt, dass Interrupt oder Stopp gesendet wurde (-> wird beendet).
func (t *ExecutionTracker) StopSent() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.take(StepOnStopSent(t.position()))
}

// ProcessExited meldet das Ende des Runner-Prozesses. Ohne vorheriges terminales Ergebnis ist
// das ein Absturz, auch bei Exit-Code 0. Nach einem terminalen Ergebnis ändert es nichts.
func (t *ExecutionTracker) ProcessExited(attempt, exitCode int) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.take(StepOnProcessExit(t.position(), attempt, exitCode))
}
