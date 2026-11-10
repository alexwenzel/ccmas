package domain

import (
	"bytes"
	"encoding/json"
	"time"
)

// Persistente Ausführungsdatensätze (Spezifikation „Projekt, Profile und Arbeitsauftrag“, Vertrag
// Execution). Eine logische Ausführung (Execution) hat einen oder mehrere Versuche
// (ExecutionAttempt); ein Resume ist ein neuer Versuch derselben Ausführung. Die Ebenen bleiben
// getrennte Felder mit getrennten Typen: Ausführungszustand (State), beobachteter Prozesszustand
// (OSProcessState mit Beobachtungszeit) und Prozessidentität (Process). Der Auftragszustand liegt
// am Arbeitsauftrag, die Verfügbarkeit am Agenten; nichts wird aus einer anderen Ebene abgeleitet.
//
// Der Datensatz ist nicht runner.Execution (der Wert mit allen Ereignissen des Runners): er hält,
// was der Dienst führend speichert. Die JSON-Namen überlappender Felder sind dieselben
// (execution_id, attempt, skill_snapshot_id, state, started_at, ended_at).

// Grenzen des Versuchsdatensatzes.
const (
	// MaxAttemptSummaryBytes begrenzt die kurze, redigierte Zusammenfassung des Ergebnisses.
	MaxAttemptSummaryBytes = 2048
	// MaxAttemptSpecBytes begrenzt den prompt-freien Spec-Datensatz.
	MaxAttemptSpecBytes = 64 << 10
	maxFieldBytes       = 512
)

// Quellen des terminalen Ergebnisses (TerminalResult.Source), wie sie der ExecutionTracker setzt.
const (
	TerminalFromResult      = "result"       // Nachricht result
	TerminalFromError       = "error"        // terminaler Fehler des Runners
	TerminalFromProcessExit = "process_exit" // Prozessende ohne Ergebnis (Absturz)
)

// TerminalSources liefert die Quellen.
func TerminalSources() []string {
	return []string{TerminalFromResult, TerminalFromError, TerminalFromProcessExit}
}

// Execution ist die logische Ausführung: wer (Rolle, optional Agent) was (optional Auftrag) ausführt.
// Auftrag und Agent sind optional, etwa für die Planung des Supervisors ohne Auftrag.
type Execution struct {
	ExecutionID string    `json:"execution_id"`
	ProjectID   string    `json:"project_id"`
	WorkOrderID string    `json:"work_order_id,omitempty"`
	AgentID     string    `json:"agent_id,omitempty"`
	Role        Role      `json:"role"`
	CreatedAt   time.Time `json:"created_at"`
}

// Check prüft die Invarianten einer logischen Ausführung.
func (e Execution) Check() error {
	if err := CheckID(e.ExecutionID); err != nil {
		return err
	}
	if err := CheckID(e.ProjectID); err != nil {
		return err
	}
	for _, id := range []string{e.WorkOrderID, e.AgentID} {
		if id != "" {
			if err := CheckID(id); err != nil {
				return err
			}
		}
	}
	if !e.Role.Valid() {
		return newError(CatRole, "/role", "unbekannte Rolle %q", e.Role)
	}
	if e.CreatedAt.IsZero() {
		return newError(CatTimestamp, "/created_at", "Erstellungszeit fehlt")
	}
	return nil
}

// ExecutionAttempt ist ein technischer Ausführungsversuch (execution_id + attempt).
type ExecutionAttempt struct {
	ExecutionID string `json:"execution_id"`
	Attempt     int    `json:"attempt"`

	// Wirksames Profil (unveränderlich gespeichert) und Skill-Snapshot der Ausführung.
	ProfileID       string `json:"profile_id"`
	ProfileVersion  int    `json:"profile_version"`
	SkillSnapshotID string `json:"skill_snapshot_id,omitempty"`
	SessionID       string `json:"session_id,omitempty"`

	// State ist der Ausführungszustand dieses Versuchs (nur diese Ebene).
	State ExecutionState `json:"state"`

	// Process ist die Prozessidentität (PID und Startzeit, beide oder keins); OSProcessState ist der
	// zuletzt beobachtete Zustand dieses Prozesses mit der Zeit der Beobachtung. Beides sagt nichts
	// über State.
	Process        *ProcessIdentity `json:"process,omitempty"`
	OSProcessState OSProcessState   `json:"os_process_state,omitempty"`
	OSObservedAt   time.Time        `json:"os_process_observed_at,omitzero"`

	Worktree string `json:"worktree,omitempty"`
	Branch   string `json:"branch,omitempty"`

	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at,omitzero"`

	// Terminal ist das eine terminale Ergebnis (nur bei beendetem Versuch). Outcome ist das gemeldete
	// Ergebnis und nur bei Quelle „result“ gesetzt. Summary ist kurz und redigiert.
	Terminal *TerminalResult `json:"terminal,omitempty"`
	Outcome  Outcome         `json:"outcome,omitempty"`
	Summary  string          `json:"summary,omitempty"`

	// Spec ist der RunSpec ohne Prompt-Text (nur Länge und SHA-256), als JSON-Objekt.
	Spec json.RawMessage `json:"spec,omitempty"`
}

// expectedState liefert den Endzustand, den eine Quelle (und bei „result“ das Ergebnis) festlegt.
func expectedTerminalState(source string, outcome Outcome) (ExecutionState, bool) {
	switch source {
	case TerminalFromError:
		return ExecControlledFailure, true
	case TerminalFromProcessExit:
		return ExecCrashed, true
	case TerminalFromResult:
		switch outcome {
		case OutcomeSucceeded:
			return ExecSucceeded, true
		case OutcomeControlledFailure:
			return ExecControlledFailure, true
		case OutcomeCancelled:
			return ExecCancelled, true
		}
	}
	return "", false
}

// Check prüft die Invarianten eines Versuchs. Ein Versuch hat höchstens ein terminales Ergebnis:
// genau die Endzustände tragen Terminal und Endzeit, und Quelle, Ergebnis und Endzustand passen
// zusammen (Fehler -> kontrolliert fehlgeschlagen, Prozessende ohne Ergebnis -> abgestürzt).
func (a ExecutionAttempt) Check() error {
	if err := CheckID(a.ExecutionID); err != nil {
		return err
	}
	if a.Attempt < 1 {
		return newError(CatValue, "/attempt", "Versuch %d ungültig (ab 1)", a.Attempt)
	}
	if err := CheckID(a.ProfileID); err != nil {
		return err
	}
	if a.ProfileVersion < 1 {
		return newError(CatValue, "/profile_version", "Profilversion %d ungültig (ab 1)", a.ProfileVersion)
	}
	if a.SkillSnapshotID != "" {
		if err := CheckID(a.SkillSnapshotID); err != nil {
			return err
		}
	}
	if len(a.SessionID) > maxFieldBytes || !plainText(a.SessionID, false) {
		return newError(CatValue, "/session_id", "Session-ID ungültig")
	}
	if !contains(ExecutionStates(), a.State) {
		return newError(CatState, "/state", "unbekannter Ausführungszustand %q", a.State)
	}
	if err := a.checkProcess(); err != nil {
		return err
	}
	if a.Worktree != "" {
		if err := CheckCanonicalPath(a.Worktree); err != nil {
			return err
		}
	}
	if len(a.Branch) > maxFieldBytes || !plainText(a.Branch, false) {
		return newError(CatValue, "/branch", "Branch ungültig")
	}
	if a.StartedAt.IsZero() {
		return newError(CatTimestamp, "/started_at", "Startzeit fehlt")
	}
	if err := a.checkTerminal(); err != nil {
		return err
	}
	if len(a.Summary) > MaxAttemptSummaryBytes {
		return newError(CatSize, "/summary", "Zusammenfassung hat %d Bytes, erlaubt sind %d", len(a.Summary), MaxAttemptSummaryBytes)
	}
	if !plainText(a.Summary, true) {
		return newError(CatValue, "/summary", "Zusammenfassung enthält Steuerzeichen oder ungültiges UTF-8")
	}
	return a.checkSpec()
}

func (a ExecutionAttempt) checkProcess() error {
	if p := a.Process; p != nil {
		if p.PID <= 0 {
			return newError(CatValue, "/process/pid", "PID %d ungültig", p.PID)
		}
		if p.StartTime.IsZero() {
			return newError(CatTimestamp, "/process/start_time", "PID %d ohne Startzeit ist keine Identität", p.PID)
		}
	}
	if a.OSProcessState != "" && !contains(OSProcessStates(), a.OSProcessState) {
		return newError(CatState, "/os_process_state", "unbekannter Prozesszustand %q", a.OSProcessState)
	}
	switch {
	case a.OSProcessState != "" && a.OSObservedAt.IsZero():
		return newError(CatTimestamp, "/os_process_observed_at", "beobachteter Prozesszustand ohne Beobachtungszeit")
	case a.OSProcessState == "" && !a.OSObservedAt.IsZero():
		return newError(CatValue, "/os_process_state", "Beobachtungszeit ohne Prozesszustand")
	case a.OSProcessState != "" && a.Process == nil:
		return newError(CatValue, "/process", "Prozesszustand ohne Prozessidentität")
	}
	return nil
}

func (a ExecutionAttempt) checkTerminal() error {
	terminal := a.State.IsTerminal()
	if terminal != (a.Terminal != nil) {
		return newError(CatState, "/terminal", "Zustand %q und terminales Ergebnis passen nicht zusammen", a.State)
	}
	if !terminal {
		if !a.EndedAt.IsZero() {
			return newError(CatTimestamp, "/ended_at", "ein laufender Versuch hat keine Endzeit")
		}
		if a.Outcome != "" {
			return newError(CatValue, "/outcome", "ein laufender Versuch hat kein Ergebnis")
		}
		return nil
	}
	t := a.Terminal
	switch {
	case t.Attempt != a.Attempt:
		return newError(CatStale, "/terminal/attempt", "Ergebnis gehört zu Versuch %d, nicht zu %d", t.Attempt, a.Attempt)
	case t.State != a.State:
		return newError(CatState, "/terminal/state", "Ergebnis endet in %q, der Versuch in %q", t.State, a.State)
	case !contains(TerminalSources(), t.Source):
		return newError(CatValue, "/terminal/source", "unbekannte Quelle %q", t.Source)
	case t.Source != TerminalFromProcessExit && t.ExitCode != 0:
		return newError(CatValue, "/terminal/exit_code", "Exit-Code gibt es nur bei Prozessende")
	case t.Source == TerminalFromResult && a.Outcome == "":
		return newError(CatValue, "/outcome", "ein Ergebnis braucht sein Outcome")
	case t.Source != TerminalFromResult && a.Outcome != "":
		return newError(CatValue, "/outcome", "ein Outcome gibt es nur bei der Quelle %q", TerminalFromResult)
	}
	want, ok := expectedTerminalState(t.Source, a.Outcome)
	if !ok {
		return newError(CatValue, "/outcome", "unbekanntes Ergebnis %q", a.Outcome)
	}
	if want != a.State {
		return newError(CatState, "/state", "Quelle %q mit Ergebnis %q ergibt %q, nicht %q", t.Source, a.Outcome, want, a.State)
	}
	if a.EndedAt.IsZero() {
		return newError(CatTimestamp, "/ended_at", "ein beendeter Versuch braucht eine Endzeit")
	}
	if a.EndedAt.Before(a.StartedAt) {
		return newError(CatTimestamp, "/ended_at", "Ende liegt vor dem Start")
	}
	return nil
}

// forbiddenSpecKeys sind Schlüssel, die Prompt-Text tragen würden.
var forbiddenSpecKeys = []string{"prompt", "system_prompt", "output_schema"}

func (a ExecutionAttempt) checkSpec() error {
	if len(a.Spec) == 0 {
		return nil
	}
	if len(a.Spec) > MaxAttemptSpecBytes {
		return newError(CatSize, "/spec", "Spec hat %d Bytes, erlaubt sind %d", len(a.Spec), MaxAttemptSpecBytes)
	}
	if !json.Valid(a.Spec) {
		return newError(CatSyntax, "/spec", "Spec ist kein gültiges JSON")
	}
	trimmed := bytes.TrimSpace(a.Spec)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return newError(CatValue, "/spec", "Spec muss ein JSON-Objekt sein")
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &obj); err != nil {
		return newError(CatValue, "/spec", "Spec nicht lesbar: %v", err)
	}
	for _, k := range forbiddenSpecKeys {
		if _, bad := obj[k]; bad {
			return newError(CatValue, "/spec/"+k, "der Spec-Datensatz enthält keinen Prompt-Text (nur Länge und Hash)")
		}
	}
	return nil
}

// Agent ist der persistente Agent eines Projekts: Rolle und Verfügbarkeit. Die Verfügbarkeit ist
// eine eigene Ebene; sie wird nicht aus Ausführungs- oder Auftragszustand abgeleitet. Revision zählt
// die Änderungen des Datensatzes (ab 1) für Compare-and-Set.
type Agent struct {
	ProjectID    string            `json:"project_id"`
	AgentID      string            `json:"agent_id"`
	Role         Role              `json:"role"`
	Availability AgentAvailability `json:"availability"`
	Revision     int64             `json:"revision"`
	UpdatedAt    time.Time         `json:"updated_at"`
}

// Check prüft die Invarianten eines Agenten.
func (a Agent) Check() error {
	if err := CheckID(a.ProjectID); err != nil {
		return err
	}
	if err := CheckID(a.AgentID); err != nil {
		return err
	}
	if !a.Role.Valid() {
		return newError(CatRole, "/role", "unbekannte Rolle %q", a.Role)
	}
	if !contains(AgentAvailabilities(), a.Availability) {
		return newError(CatState, "/availability", "unbekannte Verfügbarkeit %q", a.Availability)
	}
	if a.Revision < 1 {
		return newError(CatValue, "/revision", "Revision %d ungültig (ab 1)", a.Revision)
	}
	if a.UpdatedAt.IsZero() {
		return newError(CatTimestamp, "/updated_at", "Änderungszeit fehlt")
	}
	return nil
}
