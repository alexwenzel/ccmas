package supervisor

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/alexwenzel/ccmas/internal/domain"
)

// Verdict ist das Urteil über einen Planungsversuch.
type Verdict string

const (
	// VerdictAccepted: Der Validator hat den Plan angenommen.
	VerdictAccepted Verdict = "accepted"
	// VerdictRejected: Der Validator hat den Plan abgelehnt (Codes und Text stehen im Versuch).
	VerdictRejected Verdict = "rejected"
	// VerdictNoOutput: Die Sitzung lieferte keine gültige strukturierte Ausgabe.
	VerdictNoOutput Verdict = "no_structured_output"
	// VerdictSupervisorFailed: Der Supervisor lief nicht bis zu einem Ergebnis (Runtime-Fehler, Absturz, Abo-Limit).
	VerdictSupervisorFailed Verdict = "supervisor_failed"
	// VerdictStateUnavailable: Der aktuelle Zustand ließ sich nicht holen oder ist ungültig.
	VerdictStateUnavailable Verdict = "state_unavailable"
)

// SkillRecord hält einen Skill des Snapshots fest, aus dem Prompt und Sitzung stammen.
type SkillRecord struct {
	ID         string `json:"id"`
	Version    int    `json:"version"`
	TreeSHA256 string `json:"tree_sha256"`
	InPrompt   bool   `json:"in_prompt"` // Inhalt (SKILL.md) steht im Prompt
}

// Attempt ist das Protokoll eines Planungsversuchs: genug, um später nachzuvollziehen, womit welcher Plan entstand und
// warum er galt oder nicht galt. Er enthält nie den Text des Prompts (nur Länge und Hash), wohl aber die rohe
// strukturierte Ausgabe des Modells. Kein Feld enthält Zugangsdaten.
type Attempt struct {
	Number      int       `json:"number"` // ab 1; Versuch 2 und 3 sind Neuplanungen
	ExecutionID string    `json:"execution_id"`
	StartedAt   time.Time `json:"started_at"`
	DurationMS  int64     `json:"duration_ms"`

	// Planungsgrundlage.
	SnapshotRevision int64    `json:"snapshot_revision"`
	SnapshotSHA256   string   `json:"snapshot_sha256"` // der vollständige Planungs-Snapshot (kanonisch serialisiert)
	ContextBytes     int      `json:"context_bytes"`   // der dem Modell gezeigte (gegebenenfalls reduzierte) Zustand
	ContextReduced   []string `json:"context_reduced"` // angewandte Reduktionsregeln, leer: unverkürzt
	PromptBytes      int      `json:"prompt_bytes"`    // Auftragstext
	PromptSHA256     string   `json:"prompt_sha256"`   //
	SystemPromptSHA  string   `json:"system_prompt_sha256"`
	SystemPromptSize int      `json:"system_prompt_bytes"`
	SchemaSHA256     string   `json:"output_schema_sha256"`
	Replan           bool     `json:"replan"`                    // wiederholt nach Ablehnung, veraltetem Stand oder fehlender Ausgabe
	FeedbackSHA256   string   `json:"feedback_sha256,omitempty"` // Hash der Rückmeldung an das Modell

	// Skills.
	SkillSnapshotID string        `json:"skill_snapshot_id"`
	Skills          []SkillRecord `json:"skills"`

	// Wirksames Profil des Supervisors: angefordert, wirksam, Herkunft.
	AgentID        string              `json:"agent_id,omitempty"`
	Profile        domain.AgentProfile `json:"profile"`
	EffectiveModel string              `json:"effective_model,omitempty"` // Modell laut session_started

	// Sitzung.
	SessionID        string   `json:"session_id,omitempty"`
	LoadedSnapshotID string   `json:"loaded_skill_snapshot_id,omitempty"` // Snapshot laut session_started
	LoadedSkills     []string `json:"loaded_skills,omitempty"`            // Skills, die die CLI laut session_started geladen hat
	ClaudeVersion    string   `json:"claude_version,omitempty"`
	SDKVersion       string   `json:"sdk_version,omitempty"`
	ExecState        string   `json:"exec_state,omitempty"`
	TerminalCode     string   `json:"terminal_code,omitempty"` // Code des terminalen Fehlers, falls einer
	Violations       int      `json:"violations,omitempty"`    // verworfene Nachrichten des Runners

	// Ergebnis und Urteil.
	RawOutput       json.RawMessage `json:"raw_output,omitempty"` // rohe strukturierte Ausgabe
	Verdict         Verdict         `json:"verdict"`
	Stale           bool            `json:"stale"` // Zustandsrevision hatte sich geändert
	CurrentRevision int64           `json:"current_revision,omitempty"`
	RejectionCodes  []Code          `json:"rejection_codes,omitempty"`
	RejectionText   string          `json:"rejection_text,omitempty"`
	FailureDetail   string          `json:"failure_detail,omitempty"`
}

// SuspendReason nennt, warum die Planung ausgesetzt ist.
type SuspendReason string

const (
	// SuspendBudgetExhausted: Auch nach der erlaubten Zahl von Neuplanungen liegt kein zulässiger Plan vor.
	SuspendBudgetExhausted SuspendReason = "replan_budget_exhausted"
	// SuspendSupervisorUnavailable: Der Supervisor läuft nicht (Runtime-Fehler, Absturz, nicht auflösbares Profil oder Skills).
	SuspendSupervisorUnavailable SuspendReason = "supervisor_unavailable"
	// SuspendSubscriptionLimit: Das Abo-Limit ist erreicht.
	SuspendSubscriptionLimit SuspendReason = "subscription_limit"
	// SuspendStateUnavailable: Der aktuelle Zustand fehlt oder ist ungültig.
	SuspendStateUnavailable SuspendReason = "state_unavailable"
	// SuspendContextTooLarge: Der Zustand passt auch nach den Reduktionsregeln nicht in das Kontextbudget.
	SuspendContextTooLarge SuspendReason = "context_too_large"
)

// ErrPlanningSuspended ist der Fehlertyp „Planung ausgesetzt“ (errors.Is). Die Disposition wartet dann sichtbar und
// kontrolliert; Go ersetzt den Supervisor nie durch einen eigenen Plan (Spezifikation „Intelligente Arbeitsverteilung
// als Herzstück“).
var ErrPlanningSuspended = errors.New("Planung ausgesetzt")

// SuspendedError ist der sichtbare Wartezustand der Planung mit Grund und allen Versuchen.
type SuspendedError struct {
	Reason   SuspendReason
	Detail   string
	Attempts []Attempt
	Cause    error
}

func (e *SuspendedError) Error() string {
	if e == nil {
		return "<nil>"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Planung ausgesetzt (%s) nach %d Versuch(en)", e.Reason, len(e.Attempts))
	if e.Detail != "" {
		b.WriteString(": " + e.Detail)
	}
	return b.String()
}

// Is erkennt ErrPlanningSuspended und den Fehlertyp.
func (e *SuspendedError) Is(target error) bool {
	if target == ErrPlanningSuspended {
		return true
	}
	_, ok := target.(*SuspendedError)
	return ok
}

// Unwrap liefert die Ursache, falls es eine gibt.
func (e *SuspendedError) Unwrap() error { return e.Cause }

// AsSuspended liefert den Wartezustand, wenn err einer ist.
func AsSuspended(err error) (*SuspendedError, bool) {
	var se *SuspendedError
	if errors.As(err, &se) && se != nil {
		return se, true
	}
	return nil, false
}
