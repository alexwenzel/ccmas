package orchestrator

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/alexwenzel/ccmas/internal/domain"
	"github.com/alexwenzel/ccmas/internal/persistence"
)

// ---- Tabelle der Auftragsübergänge ------------------------------------------------------------------------------
//
// Der Auftragszustand ist eine eigene Ebene: Er folgt aus der Reservierung und aus dem Ende eines Versuchs, nie aus dem
// Prozess. Diese Tabelle ist die einzige Stelle, die beides festlegt.
//
// Reservierung (orderStartTargets), je Rolle der Zustand, aus dem sie startet, und der Zustand danach:
//
//	implementer, concept, documentation   waiting, in_progress, validation, review, waiting_integration  ->  in_progress
//	                                      (in_progress bleibt in_progress; Dokumentation startet auch aus completed und lässt
//	                                      den Auftrag completed)
//	reviewer, concept_reviewer            validation -> review (review bleibt review)
//	supervisor                            hat keinen Auftrag und reserviert nicht
//
// Terminaler Versuch (orderFinishTarget): Nur ein erfolgreicher (succeeded) Versuch der Rollen implementer, concept und
// documentation bewegt den Auftrag, und nur aus in_progress nach validation. Jedes andere Ergebnis (kontrolliertes Scheitern,
// Absturz, Abbruch) und jeder Versuch eines Prüfers lässt den Auftrag, wie er ist: Ein Absturz ändert den Auftrag nicht
// (die Crash-Serie und ihre Folgen gehören zur Wiederherstellung, M09), und das Urteil eines Reviews setzt der Supervisor
// auf Grundlage des Berichts, nicht das Ende des Prozesses.
//
// Nie wird ein Auftrag durch ein Ergebnis eines Runners completed: Abgeschlossen wird ein Auftrag erst nach der
// Integration (waiting_integration -> completed), außerhalb dieser Schicht.
//
// Begründung gegen die Startzustände des Validators (startableStates: waiting, in_progress, validation, review,
// waiting_integration; Dokumentation zusätzlich completed): Der Validator lässt Arbeit aus diesen Zuständen beauftragen, und
// „Implementierung beendet“ heißt validation, review, waiting_integration oder completed ohne aktive Ausführung. Das Ende
// eines erfolgreichen Versuchs führt deshalb genau nach validation: dem Zustand, aus dem der Validator den Review zulässt
// (er verlangt für reviewer validation oder review). Eine erneute Arbeit (Reparatur nach Befunden, Konzept- oder
// Dokumentationsarbeit) setzt den Auftrag wieder auf in_progress, weil die „Implementierung beendet“-Bedingung für spätere
// Prüfungen dann wieder gilt, sobald der Versuch erfolgreich endet. Der Start eines Prüfers ist enger als beim Validator
// (der für concept_reviewer jeden startbaren Zustand zulässt): Ein Review gehört nach validation; aus jedem anderen Zustand
// wäre die Zustandswirkung nicht definiert, und der Start scheitert lieber sichtbar (Klasse state), als den Auftrag still zu
// ändern.

var orderStartTargets = map[domain.Role]map[domain.WorkOrderState]domain.WorkOrderState{
	domain.RoleImplementer:     orderBuilderStarts(false),
	domain.RoleConcept:         orderBuilderStarts(false),
	domain.RoleDocumentation:   orderBuilderStarts(true),
	domain.RoleReviewer:        orderCheckerStarts(),
	domain.RoleConceptReviewer: orderCheckerStarts(),
	domain.RoleSupervisor:      {}, // ohne Auftrag
}

func orderBuilderStarts(afterCompletion bool) map[domain.WorkOrderState]domain.WorkOrderState {
	m := map[domain.WorkOrderState]domain.WorkOrderState{}
	for _, s := range []domain.WorkOrderState{domain.WOWaiting, domain.WOInProgress, domain.WOValidation, domain.WOReview, domain.WOWaitingIntegration} {
		m[s] = domain.WOInProgress
	}
	if afterCompletion {
		m[domain.WOCompleted] = domain.WOCompleted
	}
	return m
}

func orderCheckerStarts() map[domain.WorkOrderState]domain.WorkOrderState {
	return map[domain.WorkOrderState]domain.WorkOrderState{domain.WOValidation: domain.WOReview, domain.WOReview: domain.WOReview}
}

// orderStartTarget liefert den Auftragszustand, in dem die Reservierung der Rolle endet, und ob die Rolle aus from überhaupt
// starten darf. Ist der Zielzustand der aktuelle, bleibt der Auftrag unverändert.
func orderStartTarget(role domain.Role, from domain.WorkOrderState) (domain.WorkOrderState, bool) {
	to, ok := orderStartTargets[role][from]
	return to, ok
}

// orderFinishTarget liefert den Auftragszustand nach dem terminalen Ende eines Versuchs der Rolle (Tabelle oben). Ein
// Ergebnis, das den Auftrag nicht bewegt, liefert from.
func orderFinishTarget(role domain.Role, from domain.WorkOrderState, end domain.ExecutionState) domain.WorkOrderState {
	builder := role == domain.RoleImplementer || role == domain.RoleConcept || role == domain.RoleDocumentation
	if builder && end == domain.ExecSucceeded && from == domain.WOInProgress {
		return domain.WOValidation
	}
	return from
}

// ---- Ereignisse der Runner --------------------------------------------------------------------------------------

// ApplyEvent wendet eine Nachricht des Runners auf den gespeicherten Zustand an, in einer Transaktion:
//
//   - Die Regel des Ausführungszustands ist domain.StepOnEvent (keine zweite Kopie). Ein Verstoß ist ein Fehler der Klasse
//     stale, sequence, id oder message_type und ändert nichts.
//   - Fencing: Das Ereignis muss zur jüngsten Lease-Generation des Auftrags gehören, und die Lease muss aktiv sein. Ein
//     Ereignis eines älteren Versuchs, einer beendeten, abgelaufenen oder widerrufenen Lease ändert nichts (Klasse stale):
//     keine Zeile, kein Audit-Ereignis, keine Revision. Der Aufrufer protokolliert den Fehler. Einzige Ausnahme: Ist die Lease
//     durch CancelTask widerrufen und der Versuch in stopping, schließt genau sein terminales Ergebnis den Versuch ab; Auftrag
//     und neue Versuche bleiben unberührt, der Agent wird wieder verfügbar.
//   - Wirkung: session_started setzt die Session und bewegt den Versuch nach running (der Agent von reserved nach active);
//     question legt die Frage an und bewegt nach awaiting_answer; ein terminales Ergebnis beendet den Versuch (FinishAttempt),
//     gibt die Lease frei, macht den Agenten wieder verfügbar und bewegt den Auftrag nach der Tabelle. activity, tool_event
//     und nicht terminale Fehler ändern nichts Gespeichertes.
//   - Ein doppeltes terminales Ergebnis ist ein Fehler der Klasse sequence ohne zweite Wirkung.
func (t *Tasks) ApplyEvent(ctx context.Context, ev domain.RunEvent) error {
	const op = "ApplyEvent"
	env := ev.Envelope
	if err := taskCheckIDs(op, env.ProjectID, env.ExecutionID); err != nil {
		return err
	}
	_, err := t.update(ctx, t.actor, op, func(tx *persistence.Tx) error {
		exec, cur, err := taskLoadAttempt(tx, env.ProjectID, env.ExecutionID)
		if err != nil {
			return err
		}
		step, err := domain.StepOnEvent(taskPosition(exec, cur), ev)
		if err != nil {
			return err
		}
		return t.applyStep(tx, op, exec, cur, step, ev.Payload)
	})
	return err
}

// ApplyProcessExit meldet das Ende des Runner-Prozesses von Versuch attempt (domain.StepOnProcessExit). Ohne terminales
// Ergebnis ist das ein Absturz, auch bei Exit-Code 0: Der Versuch endet als crashed, die Lease wird freigegeben, der Agent ist
// wieder verfügbar, der Auftrag bleibt unverändert. Nach einem terminalen Ergebnis ändert das Prozessende nichts (kein
// Fehler). Es gelten dieselben Fencing-Regeln wie bei ApplyEvent.
func (t *Tasks) ApplyProcessExit(ctx context.Context, projectID, executionID string, attempt, exitCode int) error {
	const op = "ApplyProcessExit"
	if err := taskCheckIDs(op, projectID, executionID); err != nil {
		return err
	}
	_, err := t.update(ctx, t.actor, op, func(tx *persistence.Tx) error {
		exec, cur, err := taskLoadAttempt(tx, projectID, executionID)
		if err != nil {
			return err
		}
		step, err := domain.StepOnProcessExit(taskPosition(exec, cur), attempt, exitCode)
		if err != nil {
			return err
		}
		if step.Terminal == nil { // der Versuch war schon beendet
			return nil
		}
		return t.applyStep(tx, op, exec, cur, step, nil)
	})
	return err
}

func taskPosition(exec domain.Execution, cur domain.ExecutionAttempt) domain.ExecutionPosition {
	return domain.ExecutionPosition{ProjectID: exec.ProjectID, ExecutionID: exec.ExecutionID, Attempt: cur.Attempt, State: cur.State}
}

// taskLoadAttempt liest die Ausführung und ihren jüngsten Versuch: die Stelle, an der sich die Ausführung befindet.
func taskLoadAttempt(tx *persistence.Tx, projectID, execID string) (domain.Execution, domain.ExecutionAttempt, error) {
	exec, err := tx.Execution(projectID, execID)
	if err != nil {
		return domain.Execution{}, domain.ExecutionAttempt{}, err
	}
	cur, err := taskLatestAttempt(tx, projectID, execID)
	return exec, cur, err
}

// taskFence prüft, dass das Ereignis zur jüngsten, aktiven Lease des Auftrags gehört. Ausführungen ohne Auftrag (Supervisor)
// haben keine Lease und keine Absicherung. attemptOnly ist die Ausnahme eines Versuchs, der nach CancelTask in stopping
// sein terminales Ergebnis meldet.
func taskFence(tx *persistence.Tx, op string, exec domain.Execution, cur domain.ExecutionAttempt, step domain.ExecutionStep) (lease *domain.ExecutionLease, attemptOnly bool, err error) {
	if exec.WorkOrderID == "" {
		return nil, false, nil
	}
	stale := func(format string, args ...any) error {
		return taskErr(TaskStale, op, "%s", fmt.Sprintf(format, args...))
	}
	l, err := tx.AttemptLease(exec.ProjectID, exec.ExecutionID, cur.Attempt)
	if err != nil {
		return nil, false, err
	}
	if l == nil {
		return nil, false, stale("der Versuch %s#%d ist an keine Lease gebunden", exec.ExecutionID, cur.Attempt)
	}
	all, err := tx.Leases(exec.ProjectID, exec.WorkOrderID)
	if err != nil {
		return nil, false, err
	}
	if newest := all[len(all)-1]; l.LeaseID != newest.LeaseID {
		return nil, false, stale("der Versuch %s#%d gehört zur Lease-Generation %d, die jüngste ist %d", exec.ExecutionID, cur.Attempt, l.Generation, newest.Generation)
	}
	switch {
	case l.State == domain.LeaseActive:
		return l, false, nil
	case l.State == domain.LeaseRevoked && cur.State == domain.ExecStopping && step.Terminal != nil:
		return l, true, nil
	}
	return nil, false, stale("die Lease %s (Generation %d) des Versuchs %s#%d ist %s", l.LeaseID, l.Generation, exec.ExecutionID, cur.Attempt, l.State)
}

// applyStep schreibt die Wirkung eines Schritts der Schrittfunktion. Davor liegt nur Lesen: Ein Fehler (auch Fencing) hat
// nichts geschrieben.
func (t *Tasks) applyStep(tx *persistence.Tx, op string, exec domain.Execution, cur domain.ExecutionAttempt, step domain.ExecutionStep, payload any) error {
	lease, attemptOnly, err := taskFence(tx, op, exec, cur, step)
	if err != nil {
		return err
	}
	project := exec.ProjectID
	switch p := payload.(type) {
	case domain.SessionStarted:
		if p.SessionID != "" {
			if err := tx.SetSession(project, exec.ExecutionID, cur.Attempt, p.SessionID); err != nil {
				return err
			}
		}
		if err := tx.TransitionExecution(project, exec.ExecutionID, cur.Attempt, step.From, step.To); err != nil {
			return err
		}
		return taskAgentMove(tx, project, exec.AgentID, domain.AgentReserved, domain.AgentActive)
	case domain.QuestionPayload:
		if err := tx.CreateQuestion(project, domain.Question{QuestionID: p.QuestionID, WorkOrderID: exec.WorkOrderID, ExecutionID: exec.ExecutionID,
			Attempt: cur.Attempt, Text: p.Question, Context: p.Context, Recommendation: p.Recommendation, State: domain.QuestionOpen}); err != nil {
			return err
		}
		return tx.TransitionExecution(project, exec.ExecutionID, cur.Attempt, step.From, step.To)
	}
	if step.Terminal == nil {
		return nil // activity, tool_event, nicht terminaler Fehler: nichts zu speichern
	}

	if err := tx.FinishAttempt(project, exec.ExecutionID, cur.Attempt, step.From, persistence.AttemptResult{
		Terminal: *step.Terminal, Outcome: step.Outcome, Summary: taskSummary(step, payload)}); err != nil {
		return err
	}
	if err := taskAgentRelease(tx, project, exec.AgentID); err != nil {
		return err
	}
	if attemptOnly {
		return nil // Lease widerrufen, Auftrag abgebrochen: nur der Versuch schließt ab
	}
	if lease != nil {
		if err := tx.TransitionLease(project, lease.LeaseID, domain.LeaseActive, domain.LeaseReleased); err != nil {
			return err
		}
	}
	if exec.WorkOrderID == "" {
		return nil
	}
	order, err := tx.WorkOrder(project, exec.WorkOrderID)
	if err != nil {
		return err
	}
	if to := orderFinishTarget(exec.Role, order.State, step.Terminal.State); to != order.State {
		return tx.TransitionWorkOrder(project, exec.WorkOrderID, order.State, to)
	}
	return nil
}

// taskAgentMove setzt die Verfügbarkeit von from nach to, wenn der Agent gerade in from ist. Was ein Bediener inzwischen
// anders gesetzt hat (zum Beispiel unavailable), bleibt.
func taskAgentMove(tx *persistence.Tx, projectID, agentID string, from, to domain.AgentAvailability) error {
	if agentID == "" {
		return nil
	}
	a, err := tx.Agent(projectID, agentID)
	if err != nil {
		return err
	}
	if a.Availability != from {
		return nil
	}
	return tx.SetAgentAvailability(projectID, agentID, from, to)
}

// taskAgentRelease macht einen reservierten oder aktiven Agenten wieder verfügbar.
func taskAgentRelease(tx *persistence.Tx, projectID, agentID string) error {
	if agentID == "" {
		return nil
	}
	a, err := tx.Agent(projectID, agentID)
	if err != nil {
		return err
	}
	if a.Availability == domain.AgentReserved || a.Availability == domain.AgentActive {
		return tx.SetAgentAvailability(projectID, agentID, a.Availability, domain.AgentAvailable)
	}
	return nil
}

// taskSummary ist die kurze Zusammenfassung des Versuchs im Datensatz: die des Ergebnisses, bei einem Fehler Code und
// Meldung, bei einem Absturz der Exit-Code. Sie wird bereinigt (ungültiges UTF-8, Steuerzeichen) und gekürzt, damit ein
// Ereignis des Runners nie an der Formprüfung des Datensatzes scheitert und den Versuch offen ließe.
func taskSummary(step domain.ExecutionStep, payload any) string {
	switch p := payload.(type) {
	case domain.Result:
		return taskClean(p.Summary, domain.MaxAttemptSummaryBytes)
	case domain.ErrorPayload:
		return taskClean(p.Code+": "+p.Message, domain.MaxAttemptSummaryBytes)
	}
	if step.Terminal != nil && step.Terminal.Source == domain.TerminalFromProcessExit {
		return fmt.Sprintf("Prozess endete mit Exit-Code %d ohne Ergebnis", step.Terminal.ExitCode)
	}
	return ""
}

// taskClean ersetzt ungültiges UTF-8 und Steuerzeichen (außer Zeilenumbruch und Tabulator) und kürzt auf höchstens max Bytes
// an einer Zeichengrenze.
func taskClean(s string, max int) string {
	s = strings.ToValidUTF8(s, "�")
	s = strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\t':
			return r
		case r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0):
			return ' '
		}
		return r
	}, s)
	if len(s) <= max {
		return s
	}
	s = s[:max]
	for !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}

// ---- Abbrechen und Wiederaufnehmen ------------------------------------------------------------------------------

// CancelTask bricht einen Auftrag ab, mit Akteur und Grund im Auditverlauf. In einer Transaktion: Der Auftrag wird cancelled,
// die aktive Lease revoked, ein laufender oder wartender Versuch stopping (das Stoppsignal an den Runner sendet die Runtime,
// M04). Sein terminales Ergebnis schließt später nur den Versuch ab (ApplyEvent, Ausnahme des Fencings).
//
// Ein Versuch, der noch nie einen Prozess hatte (initializing ohne Prozessidentität), hat nichts, das zu stoppen oder zu
// melden wäre: Er wird direkt kontrolliert beendet. Der Weg läuft über die vorhandenen Zustände: initializing -> stopping
// (StepOnStopSent) und stopping -> cancelled mit dem Ergebnis cancelled (die Schrittfunktion für ein Ergebnis cancelled).
// Das ist kein Absturz und braucht keinen neuen Quellwert des terminalen Ergebnisses; die Zusammenfassung und das Audit des
// Abbruchs nennen den Ursprung. Der Agent wird dann sofort wieder verfügbar.
//
// Ein zweiter Aufruf für einen bereits abgebrochenen Auftrag ist idempotent und ändert nichts. Ein abgeschlossener Auftrag
// lässt sich nicht abbrechen (Klasse state).
func (t *Tasks) CancelTask(ctx context.Context, projectID, workOrderID, actor, reason string) error {
	const op = "CancelTask"
	if err := taskCheckIDs(op, projectID, workOrderID, actor); err != nil {
		return err
	}
	_, err := t.update(ctx, actor, op, func(tx *persistence.Tx) error {
		order, err := tx.WorkOrder(projectID, workOrderID)
		if err != nil {
			return err
		}
		if order.State == domain.WOCancelled {
			return nil
		}
		if !order.State.CanTransition(domain.WOCancelled) {
			return taskErr(TaskState, op, "der Auftrag %s ist %s und lässt sich nicht abbrechen", workOrderID, order.State)
		}
		lease, err := tx.ActiveLease(projectID, workOrderID)
		if err != nil {
			return err
		}
		execs, err := tx.Executions(projectID, workOrderID)
		if err != nil {
			return err
		}
		for _, e := range execs {
			cur, err := taskLatestAttempt(tx, projectID, e.ExecutionID)
			if err != nil {
				return err
			}
			if cur.State.IsTerminal() {
				continue
			}
			if err := taskStopAttempt(tx, e, cur); err != nil {
				return err
			}
		}
		if lease != nil {
			if err := tx.TransitionLease(projectID, lease.LeaseID, domain.LeaseActive, domain.LeaseRevoked); err != nil {
				return err
			}
			if lease.ExecutionID == "" { // reserviert, aber nie gestartet: es gibt nichts, das zu Ende käme
				if err := taskAgentRelease(tx, projectID, lease.AgentID); err != nil {
					return err
				}
			}
		}
		if err := tx.TransitionWorkOrder(projectID, workOrderID, order.State, domain.WOCancelled); err != nil {
			return err
		}
		return tx.RecordWorkOrderAction(projectID, workOrderID, persistence.WorkOrderCancelled, reason)
	})
	return err
}

// taskStopAttempt stoppt einen nicht beendeten Versuch (Regel: domain.StepOnStopSent) oder, ohne Prozess, beendet ihn
// kontrolliert (Beschreibung bei CancelTask).
func taskStopAttempt(tx *persistence.Tx, exec domain.Execution, cur domain.ExecutionAttempt) error {
	project := exec.ProjectID
	if cur.State == domain.ExecStopping {
		return nil
	}
	pos := taskPosition(exec, cur)
	stop, err := domain.StepOnStopSent(pos)
	if err != nil {
		return err
	}
	if err := tx.TransitionExecution(project, exec.ExecutionID, cur.Attempt, stop.From, stop.To); err != nil {
		return err
	}
	if cur.State != domain.ExecInitializing || cur.Process != nil {
		return nil // Die Runtime sendet das Stoppsignal; das Ergebnis meldet der Runner.
	}
	pos.State = stop.To
	done, err := domain.StepOnEvent(pos, domain.RunEvent{
		Envelope: domain.Envelope{ProtocolVersion: domain.ProtocolVersion, ProjectID: project, ExecutionID: exec.ExecutionID, Attempt: cur.Attempt, Type: domain.MsgResult},
		Payload:  domain.Result{Outcome: domain.OutcomeCancelled},
	})
	if err != nil {
		return err
	}
	if err := tx.FinishAttempt(project, exec.ExecutionID, cur.Attempt, done.From, persistence.AttemptResult{
		Terminal: *done.Terminal, Outcome: done.Outcome, Summary: "durch CancelTask beendet, bevor ein Prozess gestartet war"}); err != nil {
		return err
	}
	return taskAgentRelease(tx, project, exec.AgentID)
}

// ResumeTask gibt einen blockierten Auftrag zur Wiederaufnahme frei (blocked -> waiting), mit Akteur und Grund im
// Auditverlauf. Es startet nichts: Die nächste Reservierung braucht einen neuen Plan und den Validator. Jeder andere Zustand
// ist ein Fehler der Klasse state. Das Zurücksetzen der Crash-Serie gehört zur Wiederherstellung (M09).
func (t *Tasks) ResumeTask(ctx context.Context, projectID, workOrderID, actor, reason string) error {
	const op = "ResumeTask"
	if err := taskCheckIDs(op, projectID, workOrderID, actor); err != nil {
		return err
	}
	_, err := t.update(ctx, actor, op, func(tx *persistence.Tx) error {
		order, err := tx.WorkOrder(projectID, workOrderID)
		if err != nil {
			return err
		}
		if order.State != domain.WOBlocked {
			return taskErr(TaskState, op, "der Auftrag %s ist %s; nur ein blockierter Auftrag wird wieder aufgenommen", workOrderID, order.State)
		}
		if err := tx.TransitionWorkOrder(projectID, workOrderID, domain.WOBlocked, domain.WOWaiting); err != nil {
			return err
		}
		return tx.RecordWorkOrderAction(projectID, workOrderID, persistence.WorkOrderResumed, reason)
	})
	return err
}
