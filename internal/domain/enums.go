package domain

// Aufzählungen der Verträge. Jede Aufzählung ist ein eigener Typ; die Werte entsprechen
// den enum-Listen der Schemas unter contracts/ (ein Test hält beide synchron).

// Action ist eine zulässige Supervisor-Aktion.
type Action string

const (
	ActionPlanExtend         Action = "plan_extend"         // Plan ergänzen
	ActionConceptWork        Action = "concept_work"        // Konzeptarbeit beauftragen
	ActionImplementation     Action = "implementation"      // Implementierung anfordern
	ActionReview             Action = "review"              // Review anfordern
	ActionAskQuestion        Action = "ask_question"        // Rückfrage stellen
	ActionRequestIntegration Action = "request_integration" // Integration anfordern
	ActionWait               Action = "wait"                // warten
	ActionRequestCompletion  Action = "request_completion"  // Zielabschluss anfordern
)

// DecisionActions liefert genau die vereinbarten Aktionen.
func DecisionActions() []Action {
	return []Action{
		ActionPlanExtend, ActionConceptWork, ActionImplementation, ActionReview,
		ActionAskQuestion, ActionRequestIntegration, ActionWait, ActionRequestCompletion,
	}
}

// Valid meldet, ob a eine vereinbarte Aktion ist.
func (a Action) Valid() bool { return contains(DecisionActions(), a) }

// Role ist eine Agentenrolle.
type Role string

const (
	RoleSupervisor      Role = "supervisor"
	RoleProjectSetup    Role = "project_setup"
	RoleConcept         Role = "concept"
	RoleConceptReviewer Role = "concept_reviewer"
	RoleImplementer     Role = "implementer"
	RoleReviewer        Role = "reviewer"
	RoleDocumentation   Role = "documentation"
	RoleCompletion      Role = "completion"
	RoleQuestionHandler Role = "question_handler"
)

// Roles liefert alle bekannten Rollen.
func Roles() []Role {
	return []Role{
		RoleSupervisor, RoleProjectSetup, RoleConcept, RoleConceptReviewer, RoleImplementer,
		RoleReviewer, RoleDocumentation, RoleCompletion, RoleQuestionHandler,
	}
}

// Valid meldet, ob r eine bekannte Rolle ist.
func (r Role) Valid() bool { return contains(Roles(), r) }

// Runtime ist ein Runtime-Adapter. Zunächst ist nur Claude vollständig umgesetzt.
type Runtime string

const RuntimeClaude Runtime = "claude"

// Runtimes liefert die bekannten Runtimes.
func Runtimes() []Runtime { return []Runtime{RuntimeClaude} }

// Effort ist die Denkintensität einer Modellausführung (getrennt vom Aufwand der Arbeit).
type Effort string

const (
	EffortLow    Effort = "low"
	EffortMedium Effort = "medium"
	EffortHigh   Effort = "high"
	EffortXHigh  Effort = "xhigh"
	EffortMax    Effort = "max"
)

// Efforts liefert alle Effort-Stufen aufsteigend.
func Efforts() []Effort {
	return []Effort{EffortLow, EffortMedium, EffortHigh, EffortXHigh, EffortMax}
}

// Valid meldet, ob e eine bekannte Stufe ist. Ob ein Modell sie unterstützt, prüft Catalog.
func (e Effort) Valid() bool { return contains(Efforts(), e) }

// IntegrationMode ist der Auftragsmodus: PR (Standard) oder Auto.
type IntegrationMode string

const (
	ModePR   IntegrationMode = "pr"
	ModeAuto IntegrationMode = "auto"
)

// IntegrationModes liefert die Modi.
func IntegrationModes() []IntegrationMode { return []IntegrationMode{ModePR, ModeAuto} }

// Channel ist ein Bedienkanal.
type Channel string

const (
	ChannelCLI    Channel = "cli"
	ChannelTUI    Channel = "tui"
	ChannelRemote Channel = "remote"
)

// TargetKind bezeichnet den Zielbezug einer Entscheidung oder eines Auftrags.
type TargetKind string

const (
	KindTicket    TargetKind = "ticket"
	KindFeature   TargetKind = "feature"
	KindMilestone TargetKind = "milestone"
	KindWorkOrder TargetKind = "work_order"
)

// TargetKinds liefert die Zielarten einer Entscheidung.
func TargetKinds() []TargetKind {
	return []TargetKind{KindTicket, KindFeature, KindMilestone, KindWorkOrder}
}

// PreconditionKind bezeichnet eine Voraussetzung einer Entscheidung.
type PreconditionKind string

const (
	PrecWorkOrderCompleted PreconditionKind = "work_order_completed"
	PrecConceptApproved    PreconditionKind = "concept_version_approved"
	PrecReviewApproved     PreconditionKind = "review_approved"
	PrecCIPassed           PreconditionKind = "ci_passed"
	PrecCapacityAvailable  PreconditionKind = "capacity_available"
)

// MessageType ist der Typ einer Runner-Nachricht.
type MessageType string

const (
	// Go nach Python.
	MsgStart     MessageType = "start"
	MsgAnswer    MessageType = "answer"
	MsgInterrupt MessageType = "interrupt"
	MsgStop      MessageType = "stop"

	// Python nach Go.
	MsgSessionStarted MessageType = "session_started"
	MsgActivity       MessageType = "activity"
	MsgToolEvent      MessageType = "tool_event"
	MsgQuestion       MessageType = "question"
	MsgResult         MessageType = "result"
	MsgError          MessageType = "error"
)

// MessageTypesToRunner liefert die Typen Go -> Python.
func MessageTypesToRunner() []MessageType {
	return []MessageType{MsgStart, MsgAnswer, MsgInterrupt, MsgStop}
}

// MessageTypesFromRunner liefert die Typen Python -> Go.
func MessageTypesFromRunner() []MessageType {
	return []MessageType{MsgSessionStarted, MsgActivity, MsgToolEvent, MsgQuestion, MsgResult, MsgError}
}

// Outcome ist das Ergebnis eines Versuchs im result.
type Outcome string

const (
	OutcomeSucceeded         Outcome = "succeeded"
	OutcomeControlledFailure Outcome = "controlled_failure"
	OutcomeCancelled         Outcome = "cancelled"
)

// Outcomes liefert die möglichen Ergebnisse.
func Outcomes() []Outcome {
	return []Outcome{OutcomeSucceeded, OutcomeControlledFailure, OutcomeCancelled}
}

// ErrorKind klassifiziert eine Fehlermeldung des Runners.
type ErrorKind string

const (
	ErrKindRuntime           ErrorKind = "runtime"
	ErrKindSubscriptionLimit ErrorKind = "subscription_limit"
	ErrKindProtocol          ErrorKind = "protocol"
	ErrKindInternal          ErrorKind = "internal"
)

// ErrorKinds liefert die Fehlerarten.
func ErrorKinds() []ErrorKind {
	return []ErrorKind{ErrKindRuntime, ErrKindSubscriptionLimit, ErrKindProtocol, ErrKindInternal}
}

// StopReason nennt den Grund eines Interrupts oder Stopps.
type StopReason string

const (
	ReasonUserRequest StopReason = "user_request"
	ReasonTimeout     StopReason = "timeout"
	ReasonShutdown    StopReason = "shutdown"
	ReasonSuperseded  StopReason = "superseded"
)

// StopReasons liefert die Gründe.
func StopReasons() []StopReason {
	return []StopReason{ReasonUserRequest, ReasonTimeout, ReasonShutdown, ReasonSuperseded}
}

// ToolPhase ist die Phase eines Werkzeugereignisses.
type ToolPhase string

const (
	ToolStarted  ToolPhase = "started"
	ToolFinished ToolPhase = "finished"
	ToolFailed   ToolPhase = "failed"
)

// ToolPhases liefert die Phasen.
func ToolPhases() []ToolPhase { return []ToolPhase{ToolStarted, ToolFinished, ToolFailed} }

// ActivityKind ist die Art einer beobachteten Aktivität.
type ActivityKind string

const (
	ActivityAssistantText ActivityKind = "assistant_text"
	ActivityThinking      ActivityKind = "thinking"
	ActivityWaiting       ActivityKind = "waiting"
	ActivityIdle          ActivityKind = "idle"
)

// ActivityKinds liefert die Aktivitätsarten.
func ActivityKinds() []ActivityKind {
	return []ActivityKind{ActivityAssistantText, ActivityThinking, ActivityWaiting, ActivityIdle}
}

// Verdict ist das Urteil eines ReviewReport.
type Verdict string

const (
	VerdictApproved Verdict = "FREIGEGEBEN"
	VerdictFindings Verdict = "BEFUNDE"
)

// Verdicts liefert die Urteile.
func Verdicts() []Verdict { return []Verdict{VerdictApproved, VerdictFindings} }

// Severity ist die Schwere eines Befunds.
type Severity string

const (
	SeverityBlocker Severity = "blocker"
	SeverityMajor   Severity = "major"
	SeverityMinor   Severity = "minor"
)

func contains[T comparable](list []T, v T) bool {
	for _, e := range list {
		if e == v {
			return true
		}
	}
	return false
}
