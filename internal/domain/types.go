// Package domain definiert die gemeinsamen Daten- und Nachrichtenverträge von CCMAS
// einmalig: Typen, Zustände mit erlaubten Übergängen, strikte Prüfung gegen die
// JSON-Schemas unter contracts/ und die Invarianten, die ein Schema nicht ausdrückt.
//
// Alle anderen Pakete verwenden ausschließlich diese Typen; sie definieren keine eigenen
// Varianten. Die JSON-Namen sind mit dem Python-Runner identisch, weil beide Seiten
// dieselben Schema-Dateien verwenden. Regeln und Kompatibilität stehen in
// docs/architecture/contracts.md.
package domain

import (
	"encoding/json"
	"time"
)

// ---- Projekt, Profile, Arbeitsauftrag (project-v1) --------------------------

// VaultRef verweist auf den Wissensbereich eines Projekts im Obsidian-Vault.
type VaultRef struct {
	ID          string `json:"id"`
	Path        string `json:"path"`         // kanonischer absoluter Pfad des Vaults
	ProjectArea string `json:"project_area"` // relativer Pfad im Vault
}

// ResourceLimits sind die technischen Startvorgaben (konfigurierbar).
type ResourceLimits struct {
	MaxParallelBuilds       int `json:"max_parallel_builds"`
	MonitorIntervalSeconds  int `json:"monitor_interval_seconds"`
	PlanningIntervalSeconds int `json:"planning_interval_seconds"`
	AttemptRuntimeMinutes   int `json:"attempt_runtime_minutes"`
}

// ProjectConfig ist die gespeicherte Projektzuordnung. Namen sind auf stabile IDs und
// kanonische Pfade aufgelöst; der Basisbranch ist fest develop.
type ProjectConfig struct {
	SchemaVersion   int             `json:"schema_version"`
	ProjectID       string          `json:"project_id"`
	CodeDir         string          `json:"code_dir"`
	ExecutionDir    string          `json:"execution_dir"`
	Repository      string          `json:"repository"` // owner/name
	BaseBranch      string          `json:"base_branch"`
	Vault           VaultRef        `json:"vault"`
	AsanaProjectGID string          `json:"asana_project_gid"`
	MaxWorkers      int             `json:"max_workers"`
	DefaultMode     IntegrationMode `json:"default_mode"`
	Channels        []Channel       `json:"channels"`
	Resources       ResourceLimits  `json:"resources"`
}

// Standardwerte laut Spezifikation („Parameter und Profile“).
const (
	SchemaVersion     = 1
	BaseBranch        = "develop"
	DefaultMaxWorkers = 3
	MinWorkers        = 1
	MaxWorkers        = 16
)

// DefaultProjectConfig liefert die Vorbelegung, auf die Parse die Konfiguration dekodiert.
func DefaultProjectConfig() ProjectConfig {
	return ProjectConfig{
		SchemaVersion: SchemaVersion,
		BaseBranch:    BaseBranch,
		MaxWorkers:    DefaultMaxWorkers,
		DefaultMode:   ModePR,
		Channels:      []Channel{ChannelCLI, ChannelTUI},
		Resources: ResourceLimits{
			MaxParallelBuilds: 1, MonitorIntervalSeconds: 5, PlanningIntervalSeconds: 60, AttemptRuntimeMinutes: 60,
		},
	}
}

// AgentProfile beschreibt das aufgelöste Profil einer Ausführung (versioniert). Model, Effort
// und SkillIDs sind die wirksamen Werte. Die vier optionalen Felder (seit CC-46) machen
// Anforderung, Reduktion und Herkunft sichtbar; ohne sie gilt: angefordert gleich wirksam,
// Herkunft unbekannt.
type AgentProfile struct {
	ProfileID     string   `json:"profile_id"`
	Version       int      `json:"version"`
	Role          Role     `json:"role"`
	Runtime       Runtime  `json:"runtime"`
	Model         string   `json:"model"`
	Effort        Effort   `json:"effort"`
	SkillIDs      []string `json:"skill_ids"`
	PolicyProfile string   `json:"policy_profile"`

	RequestedModel  string          `json:"requested_model,omitempty"`
	RequestedEffort Effort          `json:"requested_effort,omitempty"`
	Fallback        *EffortFallback `json:"fallback,omitempty"`
	ValueSources    *ValueSources   `json:"value_sources,omitempty"`
}

// FallbackEffortReduction ist die einzige Art eines sichtbaren Fallbacks: die ausdrücklich
// genehmigte Reduktion einer nicht unterstützten Effort-Stufe.
const FallbackEffortReduction = "effort_reduction"

// EffortFallback hält eine in models.yaml genehmigte Reduktion fest (From = angefordert,
// To = wirksam, Source = Quelle der Genehmigung). Der wirksame Wert ist abgeleitet
// (Konfiguration plus dokumentierte Regel), nicht gemessen.
type EffortFallback struct {
	Kind   string `json:"kind"`
	From   Effort `json:"from"`
	To     Effort `json:"to"`
	Source string `json:"source"`
}

// ValueSource nennt die Stufe der Rangfolge, aus der ein Wert stammt.
type ValueSource string

const (
	SourceOrder      ValueSource = "order"      // ausdrücklicher Auftragswert
	SourceAgent      ValueSource = "agent"      // individueller Agentenwert
	SourceSupervisor ValueSource = "supervisor" // Wahl des Supervisors
	SourceRole       ValueSource = "role"       // Rollenstandard (bei Skills auch die Standardausstattung des Katalogs)
)

// ValueSourceLevels liefert die Stufen in Rangfolge, höchste zuerst.
func ValueSourceLevels() []ValueSource {
	return []ValueSource{SourceOrder, SourceAgent, SourceSupervisor, SourceRole}
}

// Valid meldet, ob s eine bekannte Stufe ist.
func (s ValueSource) Valid() bool { return contains(ValueSourceLevels(), s) }

// ValueSources hält je Feld die Herkunft des wirksamen Werts.
type ValueSources struct {
	Model    ValueSource `json:"model"`
	Effort   ValueSource `json:"effort"`
	SkillIDs ValueSource `json:"skill_ids"`
}

// WorkOrderTarget nennt Ticket, Feature oder Milestone eines Auftrags.
type WorkOrderTarget struct {
	Kind TargetKind `json:"kind"`
	Ref  string     `json:"ref"`
}

// WorkOrder ist der persistente fachliche Arbeitsauftrag. Sein Zustand ist unabhängig vom
// Zustand der Ausführungsversuche und vom Betriebssystemzustand ihrer Prozesse.
type WorkOrder struct {
	WorkOrderID        string          `json:"work_order_id"`
	ProjectID          string          `json:"project_id"`
	Target             WorkOrderTarget `json:"target"`
	UserOrder          string          `json:"user_order"`
	Goal               string          `json:"goal"`
	AcceptanceCriteria []string        `json:"acceptance_criteria"`
	AllowedScope       []string        `json:"allowed_scope"` // relative Pfade im Worktree
	Mode               IntegrationMode `json:"mode"`
	PlanVersion        int64           `json:"plan_version"`
	State              WorkOrderState  `json:"state"`
}

// ---- Entscheidung und Integration (decision-v1) -----------------------------

// DecisionTarget bezeichnet das Ziel einer Entscheidung.
type DecisionTarget struct {
	Kind TargetKind `json:"kind"`
	ID   string     `json:"id"`
}

// Precondition ist eine Voraussetzung, die vor der Ausführung erfüllt sein muss.
type Precondition struct {
	Kind PreconditionKind `json:"kind"`
	Ref  string           `json:"ref,omitempty"`
}

// Selection ist die Rollen-, Skill-, Modell- und Effort-Wahl des Supervisors.
type Selection struct {
	Role     Role     `json:"role"`
	SkillIDs []string `json:"skill_ids"`
	Model    string   `json:"model"`
	Effort   Effort   `json:"effort"`
}

// ReviewTarget bestimmt den zu prüfenden Stand eindeutig.
type ReviewTarget struct {
	WorkOrderID    string `json:"work_order_id"`
	Commit         string `json:"commit"`
	ConceptVersion string `json:"concept_version"`
}

// DecisionQuestion ist die vom Supervisor gestellte Rückfrage.
type DecisionQuestion struct {
	Text           string `json:"text"`
	Context        string `json:"context"`
	Recommendation string `json:"recommendation,omitempty"`
}

// Decision ist das strukturierte Ergebnis des Supervisors. Eine formal gültige Decision
// ist allein keine Ausführungsfreigabe; Check prüft sie gegen den aktuellen Zustand.
type Decision struct {
	SchemaVersion int               `json:"schema_version"`
	DecisionID    string            `json:"decision_id"`
	ProjectID     string            `json:"project_id"`
	StateRevision int64             `json:"state_revision"`
	Target        DecisionTarget    `json:"target"`
	Action        Action            `json:"action"`
	Preconditions []Precondition    `json:"preconditions"`
	Rationale     string            `json:"rationale"`
	Selection     *Selection        `json:"selection,omitempty"`
	ReviewTarget  *ReviewTarget     `json:"review_target,omitempty"`
	Question      *DecisionQuestion `json:"question,omitempty"`
}

// IntegrationRequest fordert die Integration eines geprüften Stands an.
type IntegrationRequest struct {
	RequestID      string          `json:"request_id"`
	DecisionID     string          `json:"decision_id"`
	WorkOrderID    string          `json:"work_order_id"`
	Mode           IntegrationMode `json:"mode"`
	BaseBranch     string          `json:"base_branch"`
	HeadCommit     string          `json:"head_commit"`
	Branch         string          `json:"branch"`
	PRNumber       int64           `json:"pr_number,omitempty"`
	ReviewReportID string          `json:"review_report_id"`
	ConceptVersion string          `json:"concept_version"`
	CreatedAt      time.Time       `json:"created_at"`
}

// ---- Review (runner-v1) -----------------------------------------------------

// Finding ist ein Befund eines Reviews.
type Finding struct {
	FindingID string   `json:"finding_id"`
	Severity  Severity `json:"severity"`
	Summary   string   `json:"summary"`
	Location  string   `json:"location,omitempty"`
}

// ReviewReport ist der unabhängige Bericht für einen eindeutig bestimmten Stand. Er ist an
// Commit und Konzeptversion gebunden; verändert sich der Stand, ist er nicht übertragbar.
type ReviewReport struct {
	ReportID            string    `json:"report_id"`
	WorkOrderID         string    `json:"work_order_id"`
	ReviewerExecutionID string    `json:"reviewer_execution_id"`
	Commit              string    `json:"commit"`
	ConceptVersion      string    `json:"concept_version"`
	Verdict             Verdict   `json:"verdict"`
	Findings            []Finding `json:"findings"`
	CreatedAt           time.Time `json:"created_at"`
}

// ---- Runner-Protokoll (runner-v1) -------------------------------------------

// ProtocolVersion ist die Protokollversion des Umschlags.
const ProtocolVersion = "1"

// MaxMessageBytes begrenzt eine Runner-Nachricht (eine JSON-Zeile, Umschlag und Payload).
// Der Wert steht auch als x-max-message-bytes in runner-v1.schema.json; Python liest ihn von dort.
const MaxMessageBytes = 1 << 20

// Envelope ist der Umschlag jeder Runner-Nachricht.
type Envelope struct {
	ProtocolVersion string          `json:"protocol_version"`
	ProjectID       string          `json:"project_id"`
	ExecutionID     string          `json:"execution_id"`
	Attempt         int             `json:"attempt"`
	CorrelationID   string          `json:"correlation_id"`
	Type            MessageType     `json:"type"`
	Timestamp       time.Time       `json:"timestamp"`
	Payload         json.RawMessage `json:"payload"`
}

// RunSpec ist der Ausführungsauftrag (Payload von start).
type RunSpec struct {
	WorkOrderID       string   `json:"work_order_id"`
	ProfileID         string   `json:"profile_id,omitempty"`
	ProfileVersion    int      `json:"profile_version,omitempty"`
	Role              Role     `json:"role"`
	Runtime           Runtime  `json:"runtime"`
	Model             string   `json:"model"`
	Effort            Effort   `json:"effort"`
	SkillIDs          []string `json:"skill_ids"`
	PolicyProfile     string   `json:"policy_profile"`
	Cwd               string   `json:"cwd"`
	ClaudeBinary      string   `json:"claude_binary"`
	Prompt            string   `json:"prompt"`
	ResumeSessionID   string   `json:"resume_session_id,omitempty"`
	MaxRuntimeSeconds int      `json:"max_runtime_seconds"`
	// SkillSnapshot verweist auf den unveränderlichen Skill-Snapshot der Ausführung (CC-35). Pflicht
	// für den Runner, sobald SkillIDs nicht leer ist; der Vertrag erzwingt das nicht (siehe contracts.md).
	SkillSnapshot *SkillSnapshotRef `json:"skill_snapshot,omitempty"`
}

// SkillSnapshotRef benennt einen geschriebenen Skill-Snapshot: seine Kennung und das Verzeichnis
// (absoluter, kanonischer Pfad; Layout und Hashes: docs/architecture/skill-migration.md).
type SkillSnapshotRef struct {
	SnapshotID string `json:"snapshot_id"`
	Path       string `json:"path"`
}

// Answer ist die Antwort auf eine Frage (Payload von answer). DeliveryConfirmedAt wird
// erst nach bestätigter Zustellung gesetzt.
type Answer struct {
	AnswerID            string     `json:"answer_id"`
	QuestionID          string     `json:"question_id"`
	Text                string     `json:"text"`
	CreatedAt           time.Time  `json:"created_at"`
	DeliveryConfirmedAt *time.Time `json:"delivery_confirmed_at,omitzero"`
}

// InterruptPayload ist der Payload von interrupt.
type InterruptPayload struct {
	Reason StopReason `json:"reason"`
}

// StopPayload ist der Payload von stop.
type StopPayload struct {
	Reason StopReason `json:"reason"`
}

// SessionStarted ist der Payload von session_started; Model und Effort sind wirksame Werte.
type SessionStarted struct {
	SessionID     string `json:"session_id"`
	Model         string `json:"model"`
	Effort        Effort `json:"effort"`
	ClaudeVersion string `json:"claude_version"`
	// Optionale Angaben zur wirksamen Konfiguration (CC-17): Berechtigungsmodus,
	// gestartetes Binary und Art der Anmeldung (nie Konto-Daten).
	PermissionMode string     `json:"permission_mode,omitempty"`
	ClaudeBinary   string     `json:"claude_binary,omitempty"`
	AuthSource     AuthSource `json:"auth_source,omitempty"`
	// Skills der Sitzung (CC-35): Snapshot, aus dem geladen wurde, und die Skill-IDs (ohne
	// Plugin-Präfix), die die CLI laut Init-Meldung wirksam geladen hat. Nur gesetzt, wenn der
	// Auftrag einen Snapshot trug; eine leere Liste heißt dort „keine Skills“ und fällt in Go mit
	// einem fehlenden Feld zusammen.
	SkillSnapshotID string   `json:"skill_snapshot_id,omitempty"`
	Skills          []string `json:"skills,omitempty"`
}

// Activity ist der Payload von activity.
type Activity struct {
	Kind    ActivityKind `json:"kind"`
	Summary string       `json:"summary"`
}

// ToolEvent ist der Payload von tool_event.
type ToolEvent struct {
	ToolUseID string    `json:"tool_use_id"`
	Tool      string    `json:"tool"`
	Phase     ToolPhase `json:"phase"`
	Summary   string    `json:"summary,omitempty"`
}

// QuestionPayload ist der Payload von question. Zustand, Auftrag und Ausführung ergänzt Go
// aus dem Umschlag; daraus entsteht die persistente Question.
type QuestionPayload struct {
	QuestionID     string `json:"question_id"`
	Question       string `json:"question"`
	Context        string `json:"context"`
	Recommendation string `json:"recommendation,omitempty"`
}

// Result ist der Payload von result (terminal).
type Result struct {
	Outcome      Outcome       `json:"outcome"`
	Summary      string        `json:"summary"`
	ReviewReport *ReviewReport `json:"review_report,omitempty"`
}

// ErrorPayload ist der Payload von error. Terminal=true ist ein terminales Ergebnis.
type ErrorPayload struct {
	Code     string    `json:"code"`
	Message  string    `json:"message"`
	Kind     ErrorKind `json:"kind"`
	Terminal bool      `json:"terminal"`
}

// RunEvent ist eine validierte Nachricht Python -> Go. Payload ist SessionStarted,
// Activity, ToolEvent, QuestionPayload, Result oder ErrorPayload.
type RunEvent struct {
	Envelope Envelope
	Payload  any
}

// RunCommand ist eine validierte Nachricht Go -> Python. Payload ist RunSpec, Answer,
// InterruptPayload oder StopPayload.
type RunCommand struct {
	Envelope Envelope
	Payload  any
}

// ---- Fragen -----------------------------------------------------------------

// Question ist die persistente Rückfrage eines Workers. Jede Frage besitzt eine ID,
// Auftrag, Ausführung, Kontext, Empfehlung, Erstellungszeit und einen Zustand.
type Question struct {
	QuestionID          string        `json:"question_id"`
	WorkOrderID         string        `json:"work_order_id"`
	ExecutionID         string        `json:"execution_id"`
	Attempt             int           `json:"attempt"`
	Text                string        `json:"text"`
	Context             string        `json:"context"`
	Recommendation      string        `json:"recommendation,omitempty"`
	State               QuestionState `json:"state"`
	CreatedAt           time.Time     `json:"created_at"`
	AnswerID            string        `json:"answer_id,omitempty"`
	DeliveryConfirmedAt time.Time     `json:"delivery_confirmed_at,omitzero"`
}

// ---- Prozessmessung und Agentenübersicht (nur Go) ---------------------------

// ProcessIdentity identifiziert einen Prozess eindeutig: PID und Startzeit. Eine
// wiederverwendete PID hat eine andere Startzeit und damit eine andere Identität.
type ProcessIdentity struct {
	PID       int
	StartTime time.Time
}

// ProcessSample ist eine Messung eines Prozesses. CPUPercent und RSSBytes sind nil, wenn
// der Wert unbekannt ist; unbekannte Werte werden nicht mit 0 erfunden. CPU wird mit
// 100 Prozent je ausgelastetem Kern angegeben, RSS in Bytes (Summen sind keine exakten
// exklusiven Werte).
type ProcessSample struct {
	PID             int            `json:"pid"`
	StartTime       time.Time      `json:"start_time"`
	ParentPID       int            `json:"parent_pid,omitempty"`
	ParentStartTime time.Time      `json:"parent_start_time,omitzero"`
	State           OSProcessState `json:"state"`
	CPUPercent      *float64       `json:"cpu_percent,omitempty"`
	RSSBytes        *uint64        `json:"rss_bytes,omitempty"`
	ExitCode        *int           `json:"exit_code,omitempty"`
	ExecutionID     string         `json:"execution_id,omitempty"`
	Attempt         int            `json:"attempt,omitempty"`
	SampledAt       time.Time      `json:"sampled_at"`
}

// AgentSnapshot fasst den Stand eines Agenten für Übersicht und Persistenz zusammen. Die
// Ebenen bleiben getrennte Felder mit getrennten Typen: Verfügbarkeit, Ausführungszustand,
// Auftragszustand und (in Process) der Betriebssystemzustand.
type AgentSnapshot struct {
	AgentID        string            `json:"agent_id"`
	Role           Role              `json:"role"`
	ProfileID      string            `json:"profile_id"`
	ProfileVersion int               `json:"profile_version"`
	Availability   AgentAvailability `json:"availability"`
	ExecutionID    string            `json:"execution_id,omitempty"`
	Attempt        int               `json:"attempt,omitempty"`
	ExecutionState ExecutionState    `json:"execution_state,omitempty"`
	WorkOrderID    string            `json:"work_order_id,omitempty"`
	WorkOrderState WorkOrderState    `json:"work_order_state,omitempty"`
	Model          string            `json:"model,omitempty"`  // wirksames Modell
	Effort         Effort            `json:"effort,omitempty"` // wirksame Effort-Stufe
	LastActivity   *Activity         `json:"last_activity,omitempty"`
	LastActivityAt time.Time         `json:"last_activity_at,omitzero"`
	Process        *ProcessSample    `json:"process,omitempty"`
	SampledAt      time.Time         `json:"sampled_at"`
}
