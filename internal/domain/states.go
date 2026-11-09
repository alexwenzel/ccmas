package domain

// Vier Ebenen werden getrennt geführt, jede mit einem eigenen Typ (kein gemeinsames
// String-Alias): Agentenverfügbarkeit, technischer Ausführungsversuch, fachlicher Auftrag
// und Betriebssystem-Prozesszustand. Erlaubte Übergänge stehen je Ebene in einer Tabelle;
// CanTransition fragt sie ab. Kein Zustand einer Ebene wird aus einem anderen abgeleitet:
// Ein Prozess-Exit mit Code 0 beendet weder den Versuch als erfolgreich noch den Auftrag.

// ---- Agentenverfügbarkeit ---------------------------------------------------

// AgentAvailability beschreibt, ob ein Agent Arbeit übernehmen kann.
type AgentAvailability string

const (
	AgentAvailable   AgentAvailability = "available"
	AgentReserved    AgentAvailability = "reserved"
	AgentActive      AgentAvailability = "active"
	AgentUnavailable AgentAvailability = "unavailable"
)

// AgentAvailabilities liefert alle Zustände.
func AgentAvailabilities() []AgentAvailability {
	return []AgentAvailability{AgentAvailable, AgentReserved, AgentActive, AgentUnavailable}
}

var agentTransitions = map[AgentAvailability][]AgentAvailability{
	AgentAvailable:   {AgentReserved, AgentUnavailable},
	AgentReserved:    {AgentActive, AgentAvailable, AgentUnavailable},
	AgentActive:      {AgentAvailable, AgentUnavailable},
	AgentUnavailable: {AgentAvailable},
}

// CanTransition meldet, ob der Übergang s -> to erlaubt ist.
func (s AgentAvailability) CanTransition(to AgentAvailability) bool {
	return contains(agentTransitions[s], to)
}

// ---- Ausführungszustand -----------------------------------------------------

// ExecutionState ist der Zustand eines technischen Ausführungsversuchs.
type ExecutionState string

const (
	ExecInitializing      ExecutionState = "initializing"
	ExecRunning           ExecutionState = "running"
	ExecAwaitingAnswer    ExecutionState = "awaiting_answer"
	ExecStopping          ExecutionState = "stopping"
	ExecSucceeded         ExecutionState = "succeeded"
	ExecControlledFailure ExecutionState = "controlled_failure"
	ExecCrashed           ExecutionState = "crashed"
	ExecCancelled         ExecutionState = "cancelled"
)

// ExecutionStates liefert alle Zustände.
func ExecutionStates() []ExecutionState {
	return []ExecutionState{
		ExecInitializing, ExecRunning, ExecAwaitingAnswer, ExecStopping,
		ExecSucceeded, ExecControlledFailure, ExecCrashed, ExecCancelled,
	}
}

// Ein Versuch besitzt höchstens ein terminales Ergebnis: Endzustände haben keine
// Übergänge. Abbruch (cancelled) ist nur über stopping erreichbar.
var executionTransitions = map[ExecutionState][]ExecutionState{
	ExecInitializing:   {ExecRunning, ExecStopping, ExecControlledFailure, ExecCrashed},
	ExecRunning:        {ExecAwaitingAnswer, ExecStopping, ExecSucceeded, ExecControlledFailure, ExecCrashed},
	ExecAwaitingAnswer: {ExecRunning, ExecStopping, ExecControlledFailure, ExecCrashed},
	ExecStopping:       {ExecSucceeded, ExecControlledFailure, ExecCrashed, ExecCancelled},
}

// CanTransition meldet, ob der Übergang s -> to erlaubt ist.
func (s ExecutionState) CanTransition(to ExecutionState) bool {
	return contains(executionTransitions[s], to)
}

// IsTerminal meldet, ob s ein Endzustand des Versuchs ist.
func (s ExecutionState) IsTerminal() bool {
	switch s {
	case ExecSucceeded, ExecControlledFailure, ExecCrashed, ExecCancelled:
		return true
	}
	return false
}

// ---- Auftragszustand --------------------------------------------------------

// WorkOrderState ist der fachliche Zustand eines Arbeitsauftrags.
type WorkOrderState string

const (
	WOWaiting            WorkOrderState = "waiting" // wartet auf Voraussetzungen oder Kapazität
	WOInProgress         WorkOrderState = "in_progress"
	WOValidation         WorkOrderState = "validation"
	WOReview             WorkOrderState = "review"
	WOWaitingIntegration WorkOrderState = "waiting_integration"
	WOCompleted          WorkOrderState = "completed"
	WOBlocked            WorkOrderState = "blocked"
	WOCancelled          WorkOrderState = "cancelled"
)

// WorkOrderStates liefert alle Zustände.
func WorkOrderStates() []WorkOrderState {
	return []WorkOrderState{
		WOWaiting, WOInProgress, WOValidation, WOReview, WOWaitingIntegration,
		WOCompleted, WOBlocked, WOCancelled,
	}
}

// Abgeschlossen wird ein Auftrag nur nach der Integration (wartet auf Integration ->
// abgeschlossen). Befunde, Merge-Konflikte und Ausfälle führen zurück nach „in Bearbeitung“
// beziehungsweise „wartet“.
var workOrderTransitions = map[WorkOrderState][]WorkOrderState{
	WOWaiting:            {WOInProgress, WOBlocked, WOCancelled},
	WOInProgress:         {WOWaiting, WOValidation, WOBlocked, WOCancelled},
	WOValidation:         {WOInProgress, WOReview, WOBlocked, WOCancelled},
	WOReview:             {WOInProgress, WOWaitingIntegration, WOBlocked, WOCancelled},
	WOWaitingIntegration: {WOInProgress, WOCompleted, WOBlocked, WOCancelled},
	WOBlocked:            {WOWaiting, WOInProgress, WOCancelled},
}

// CanTransition meldet, ob der Übergang s -> to erlaubt ist.
func (s WorkOrderState) CanTransition(to WorkOrderState) bool {
	return contains(workOrderTransitions[s], to)
}

// IsTerminal meldet, ob s ein Endzustand des Auftrags ist.
func (s WorkOrderState) IsTerminal() bool { return s == WOCompleted || s == WOCancelled }

// ---- Fragezustand -----------------------------------------------------------

// QuestionState ist der Zustand einer Rückfrage.
type QuestionState string

const (
	QuestionOpen       QuestionState = "open"
	QuestionAnswered   QuestionState = "answered"
	QuestionDelivered  QuestionState = "delivered" // Zustellung an den Worker bestätigt
	QuestionSuperseded QuestionState = "superseded"
	QuestionCancelled  QuestionState = "cancelled"
)

// QuestionStates liefert alle Zustände.
func QuestionStates() []QuestionState {
	return []QuestionState{QuestionOpen, QuestionAnswered, QuestionDelivered, QuestionSuperseded, QuestionCancelled}
}

// Keine Antwort ist keine Freigabe: aus „offen“ gibt es keinen Weg nach „zugestellt“.
var questionTransitions = map[QuestionState][]QuestionState{
	QuestionOpen:     {QuestionAnswered, QuestionSuperseded, QuestionCancelled},
	QuestionAnswered: {QuestionDelivered, QuestionSuperseded, QuestionCancelled},
}

// CanTransition meldet, ob der Übergang s -> to erlaubt ist.
func (s QuestionState) CanTransition(to QuestionState) bool {
	return contains(questionTransitions[s], to)
}

// ---- Betriebssystem-Prozesszustand ------------------------------------------

// OSProcessState ist der vom Betriebssystem beobachtete Zustand eines Prozesses. Er wird
// gemessen, nicht gesteuert; daher gibt es keine Übergangstabelle. Er sagt nichts über
// Ausführungs- oder Auftragszustand.
type OSProcessState string

const (
	ProcRunning  OSProcessState = "running"
	ProcSleeping OSProcessState = "sleeping"
	ProcStopped  OSProcessState = "stopped"
	ProcZombie   OSProcessState = "zombie"
	ProcExited   OSProcessState = "exited"
	ProcUnknown  OSProcessState = "unknown"
)

// OSProcessStates liefert alle Zustände.
func OSProcessStates() []OSProcessState {
	return []OSProcessState{ProcRunning, ProcSleeping, ProcStopped, ProcZombie, ProcExited, ProcUnknown}
}
