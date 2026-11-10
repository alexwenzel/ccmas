package domain

// Die Regel des Ausführungszustands als reine Funktionen. Zu einer Stelle (Projekt, Ausführung, Versuch, aktueller
// Zustand) und einer Meldung liefern sie den Folgezustand, gegebenenfalls das eine terminale Ergebnis, oder einen Fehler
// der Klasse id, message_type, stale oder sequence. Sie verändern nichts und kennen weder Speicher noch Auftragszustand.
//
// Es gibt genau diese eine Regel: Der ExecutionTracker (im Speicher) und die persistente Verarbeitung im Orchestrator
// rufen beide diese Funktionen auf. Eine zweite Kopie der Regel würde mit der Zeit auseinanderlaufen.

// ExecutionPosition ist die Stelle, an der sich ein Ausführungsversuch befindet: wessen Versuch es ist und in welchem
// Ausführungszustand er steht.
type ExecutionPosition struct {
	ProjectID   string
	ExecutionID string
	Attempt     int
	State       ExecutionState
}

// ExecutionStep ist das Ergebnis einer Meldung: der Übergang From -> To. Bleibt der Zustand gleich (zum Beispiel bei
// einer Aktivitätsmeldung), ist Changed false. Terminal ist genau dann gesetzt, wenn die Meldung den Versuch beendet;
// Outcome ist dann bei der Quelle „result“ das gemeldete Ergebnis, sonst leer.
type ExecutionStep struct {
	From     ExecutionState
	To       ExecutionState
	Terminal *TerminalResult
	Outcome  Outcome
}

// Changed meldet, ob der Schritt den Zustand ändert.
func (s ExecutionStep) Changed() bool { return s.From != s.To }

func (p ExecutionPosition) stay() ExecutionStep { return ExecutionStep{From: p.State, To: p.State} }

func (p ExecutionPosition) move(to ExecutionState) (ExecutionStep, error) {
	if !p.State.CanTransition(to) {
		return ExecutionStep{}, newError(CatSequence, "", "Übergang %s -> %s ist nicht erlaubt", p.State, to)
	}
	return ExecutionStep{From: p.State, To: to}, nil
}

func (p ExecutionPosition) finish(to ExecutionState, source string, exitCode int, outcome Outcome) (ExecutionStep, error) {
	step, err := p.move(to)
	if err != nil {
		return ExecutionStep{}, err
	}
	step.Terminal = &TerminalResult{Attempt: p.Attempt, State: to, Source: source, ExitCode: exitCode}
	step.Outcome = outcome
	return step, nil
}

// checkAttempt prüft, ob eine Meldung des Versuchs attempt zur aktuellen Stelle passt: ein älterer Versuch ist verspätet
// (stale), ein noch nicht begonnener fällt aus der Reihenfolge (sequence).
func (p ExecutionPosition) checkAttempt(attempt int, location, staleLead string) error {
	switch {
	case attempt < p.Attempt:
		return newError(CatStale, location, "%s %d, aktuell ist Versuch %d", staleLead, attempt, p.Attempt)
	case attempt > p.Attempt:
		return newError(CatSequence, location, "Versuch %d wurde nicht begonnen (aktuell %d)", attempt, p.Attempt)
	}
	return nil
}

// StepOnEvent verarbeitet eine Nachricht des Runners. Ein Fehler hinterlässt keinen Schritt.
//
// Erzwungen werden: Projekt und Ausführung der Nachricht gehören zur Stelle (id); Nachrichten älterer Versuche sind
// verspätet (stale), Nachrichten nicht begonnener Versuche fallen aus der Reihenfolge (sequence); nach dem terminalen
// Ergebnis ändert nichts mehr etwas (sequence); nur Übergänge der Tabelle von ExecutionState sind erlaubt (sequence).
// Ein terminaler Fehler ist ein kontrolliertes Scheitern, ein nicht terminaler eine Warnung ohne Zustandswirkung.
func StepOnEvent(pos ExecutionPosition, ev RunEvent) (ExecutionStep, error) {
	env := ev.Envelope
	if env.ProjectID != pos.ProjectID || env.ExecutionID != pos.ExecutionID {
		return ExecutionStep{}, newError(CatID, "", "Nachricht gehört zu %s/%s, nicht zu %s/%s",
			env.ProjectID, env.ExecutionID, pos.ProjectID, pos.ExecutionID)
	}
	if err := pos.checkAttempt(env.Attempt, "/attempt", "Nachricht aus Versuch"); err != nil {
		return ExecutionStep{}, err
	}
	if pos.State.IsTerminal() {
		return ExecutionStep{}, newError(CatSequence, "", "Versuch %d ist bereits beendet (%s); %s wird nicht übernommen", pos.Attempt, pos.State, env.Type)
	}

	switch p := ev.Payload.(type) {
	case SessionStarted:
		if pos.State != ExecInitializing {
			return ExecutionStep{}, newError(CatSequence, "", "session_started im Zustand %s", pos.State)
		}
		return pos.move(ExecRunning)
	case Activity, ToolEvent:
		if pos.State != ExecRunning && pos.State != ExecStopping {
			return ExecutionStep{}, newError(CatSequence, "", "%s im Zustand %s", env.Type, pos.State)
		}
		return pos.stay(), nil
	case QuestionPayload:
		if pos.State != ExecRunning {
			return ExecutionStep{}, newError(CatSequence, "", "question im Zustand %s", pos.State)
		}
		return pos.move(ExecAwaitingAnswer)
	case Result:
		switch p.Outcome {
		case OutcomeSucceeded:
			return pos.finish(ExecSucceeded, TerminalFromResult, 0, p.Outcome)
		case OutcomeControlledFailure:
			return pos.finish(ExecControlledFailure, TerminalFromResult, 0, p.Outcome)
		case OutcomeCancelled:
			return pos.finish(ExecCancelled, TerminalFromResult, 0, p.Outcome)
		}
		return ExecutionStep{}, newError(CatValue, "/payload/outcome", "unbekanntes Ergebnis %q", p.Outcome)
	case ErrorPayload:
		if !p.Terminal {
			return pos.stay(), nil // Warnung ohne Zustandswirkung
		}
		return pos.finish(ExecControlledFailure, TerminalFromError, 0, "")
	}
	return ExecutionStep{}, newError(CatMessageType, "", "Payload %T ist keine Runner-Meldung", ev.Payload)
}

// StepOnAnswerSent vermerkt, dass die Antwort an den Runner gesendet wurde (wartet -> läuft).
func StepOnAnswerSent(pos ExecutionPosition) (ExecutionStep, error) {
	if pos.State != ExecAwaitingAnswer {
		return ExecutionStep{}, newError(CatSequence, "", "answer im Zustand %s", pos.State)
	}
	return pos.move(ExecRunning)
}

// StepOnStopSent vermerkt, dass Interrupt oder Stopp gesendet wurde (-> wird beendet).
func StepOnStopSent(pos ExecutionPosition) (ExecutionStep, error) {
	return pos.move(ExecStopping)
}

// StepOnProcessExit meldet das Ende des Runner-Prozesses von Versuch attempt. Ohne vorheriges terminales Ergebnis ist
// das ein Absturz, auch bei Exit-Code 0. Nach einem terminalen Ergebnis ändert es nichts (kein Fehler). Ein Prozess eines
// älteren Versuchs ist verspätet (stale), der eines nicht begonnenen fällt aus der Reihenfolge (sequence).
func StepOnProcessExit(pos ExecutionPosition, attempt, exitCode int) (ExecutionStep, error) {
	if err := pos.checkAttempt(attempt, "", "Prozess gehört zu Versuch"); err != nil {
		return ExecutionStep{}, err
	}
	if pos.State.IsTerminal() {
		return pos.stay(), nil
	}
	return pos.finish(ExecCrashed, TerminalFromProcessExit, exitCode, "")
}
