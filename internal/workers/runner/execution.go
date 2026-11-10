package runner

import (
	"encoding/json"
	"time"

	"github.com/alexwenzel/ccmas/internal/domain"
)

// SpecRecord ist der RunSpec einer Ausführung ohne Prompt-Text: der Datensatz hält nur Länge und
// SHA-256 des Prompts.
type SpecRecord struct {
	WorkOrderID       string                   `json:"work_order_id"`
	ProfileID         string                   `json:"profile_id,omitempty"`
	ProfileVersion    int                      `json:"profile_version,omitempty"`
	Role              domain.Role              `json:"role"`
	Runtime           domain.Runtime           `json:"runtime"`
	Model             string                   `json:"model"`
	Effort            domain.Effort            `json:"effort"`
	SkillIDs          []string                 `json:"skill_ids"`
	PolicyProfile     string                   `json:"policy_profile"`
	Cwd               string                   `json:"cwd"`
	ClaudeBinary      string                   `json:"claude_binary"`
	PromptBytes       int                      `json:"prompt_bytes"`
	PromptSHA256      string                   `json:"prompt_sha256"`
	ResumeSessionID   string                   `json:"resume_session_id,omitempty"`
	MaxRuntimeSeconds int                      `json:"max_runtime_seconds"`
	SkillSnapshot     *domain.SkillSnapshotRef `json:"skill_snapshot,omitempty"`
}

// Violation ist eine Nachricht oder Zeile des Runners, die nicht übernommen wurde: ungültig,
// übergroß, außerhalb der Reihenfolge, aus einem alten Versuch. Der Zustand der Ausführung
// ändert sich dadurch nicht.
type Violation struct {
	Category domain.Category `json:"category"`
	Message  string          `json:"message"`
}

// Execution ist der Ausführungsdatensatz eines Versuchs. Er ist ein Wert; die Persistenz in
// SQLite folgt in M03.
type Execution struct {
	ProjectID   string `json:"project_id"`
	ExecutionID string `json:"execution_id"`
	Attempt     int    `json:"attempt"`

	// Profile ist eine unveränderliche Kopie des aufgelösten AgentProfile.
	Profile domain.AgentProfile `json:"profile"`
	Spec    SpecRecord          `json:"spec"`
	// SkillSnapshotID ist die Kennung des von Go geschriebenen Snapshots (leer ohne Skills).
	SkillSnapshotID string `json:"skill_snapshot_id,omitempty"`

	SessionStarted *domain.SessionStarted `json:"session_started,omitempty"`
	// Events sind alle übernommenen Nachrichten des Runners in Eingangsreihenfolge.
	Events []domain.RunEvent `json:"events"`
	// Terminal ist das terminale Ergebnis (Ergebnis, terminaler Fehler oder Prozessende ohne Ergebnis).
	Terminal *domain.TerminalResult `json:"terminal,omitempty"`
	State    domain.ExecutionState  `json:"state"`
	// Crashed ist wahr, wenn der Prozess ohne terminales Ergebnis endete, auch mit Exit-Code 0.
	Crashed  bool   `json:"crashed"`
	ExitCode int    `json:"exit_code"`
	ExitNote string `json:"exit_note,omitempty"`

	// StopSent: Go hat stop gesendet (Abbruch). ForcedKill: die Prozessgruppe musste beendet werden.
	StopSent   bool `json:"stop_sent,omitempty"`
	ForcedKill bool `json:"forced_kill,omitempty"`

	Violations    []Violation `json:"violations,omitempty"`
	DroppedEvents int         `json:"dropped_events,omitempty"`

	// Stderr ist das Ende der redigierten Logs des Runners (begrenzt). Der Runner protokolliert
	// nie den Prompt.
	Stderr          string `json:"stderr,omitempty"`
	StderrTruncated bool   `json:"stderr_truncated,omitempty"`

	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at"`
}

// JSON liefert den Datensatz eingerückt als JSON.
func (e Execution) JSON() ([]byte, error) {
	return json.MarshalIndent(e, "", "  ")
}

// Result liefert das Ergebnis, wenn der Versuch mit einer Nachricht „result“ endete.
func (e Execution) Result() (domain.Result, bool) {
	if e.Terminal == nil || e.Terminal.Source != "result" {
		return domain.Result{}, false
	}
	for i := len(e.Events) - 1; i >= 0; i-- {
		if r, ok := e.Events[i].Payload.(domain.Result); ok {
			return r, true
		}
	}
	return domain.Result{}, false
}

// TerminalError liefert den terminalen Fehler, wenn der Versuch so endete.
func (e Execution) TerminalError() (domain.ErrorPayload, bool) {
	if e.Terminal == nil || e.Terminal.Source != "error" {
		return domain.ErrorPayload{}, false
	}
	for i := len(e.Events) - 1; i >= 0; i-- {
		if p, ok := e.Events[i].Payload.(domain.ErrorPayload); ok && p.Terminal {
			return p, true
		}
	}
	return domain.ErrorPayload{}, false
}

// Warnings liefert die nicht terminalen Fehlermeldungen des Runners (zum Beispiel subscription_limit).
func (e Execution) Warnings() []domain.ErrorPayload {
	var out []domain.ErrorPayload
	for _, ev := range e.Events {
		if p, ok := ev.Payload.(domain.ErrorPayload); ok && !p.Terminal {
			out = append(out, p)
		}
	}
	return out
}

func cloneProfile(p domain.AgentProfile) domain.AgentProfile {
	out := p
	if p.SkillIDs != nil {
		out.SkillIDs = append([]string{}, p.SkillIDs...)
	}
	if p.Fallback != nil {
		f := *p.Fallback
		out.Fallback = &f
	}
	if p.ValueSources != nil {
		v := *p.ValueSources
		out.ValueSources = &v
	}
	return out
}
