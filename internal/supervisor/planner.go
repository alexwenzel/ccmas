package supervisor

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/alexwenzel/ccmas/internal/domain"
	"github.com/alexwenzel/ccmas/internal/profiles"
	"github.com/alexwenzel/ccmas/internal/skills"
	"github.com/alexwenzel/ccmas/internal/workers/runner"
	"github.com/alexwenzel/ccmas/prompts"
)

// Standardwerte des Planers.
const (
	// DefaultMaxReplans: höchstens zwei Neuplanungen nach einer Ablehnung (insgesamt höchstens drei Versuche).
	DefaultMaxReplans = 2
	// DefaultMaxTurns begrenzt die Runden einer Planungssitzung (strukturierte Ausgabe samt Nachbesserungen des SDK).
	DefaultMaxTurns = 8
	// DefaultMaxRuntime ist die Laufzeitgrenze je Planungsversuch.
	DefaultMaxRuntime = 10 * time.Minute
	// LeadingSkill ist der führende Supervisor-Skill; ohne ihn plant der Planer nicht.
	LeadingSkill = "ccmas-supervisor"
)

// Runtime führt eine Ausführung aus. *runner.Runner erfüllt sie; Tests setzen einen Fake ein.
type Runtime interface {
	Execute(ctx context.Context, req runner.Request) (runner.Execution, error)
}

// SnapshotSource liefert den aktuellen Zustand eines Projekts: zur Validierung jedes Plans und nach einem veralteten
// Stand. Der Planungs-Snapshot, den Plan bekommt, ist nur die Grundlage der Planung; geprüft wird gegen diesen Zustand.
type SnapshotSource interface {
	CurrentSnapshot(ctx context.Context, projectID string) (ProjectSnapshot, error)
}

// ProfileResolver löst das Profil der Rolle supervisor auf (opus/high aus der Konfiguration; ein individueller
// Agentenwert hat Vorrang). *profiles.Resolver erfüllt die Schnittstelle.
type ProfileResolver interface {
	ResolveProfile(role domain.Role, agentID string, overrides profiles.Overrides, caps profiles.Capabilities) (domain.AgentProfile, error)
}

// PlannerConfig konfiguriert den Planer. Runtime, Source, Policy, Catalog, ClaudeBinary und SnapshotDir sind Pflicht.
type PlannerConfig struct {
	Runtime Runtime
	Source  SnapshotSource
	// Policy prüft jeden Plan (Validator) und liefert Resolver und Modellfähigkeiten für das Supervisor-Profil.
	Policy Policy
	// Profiles löst das Supervisor-Profil auf; Standard: Policy.Resolver.
	Profiles ProfileResolver
	// Catalog muss mit skills.LoadChecked geladen sein (Rollenregeln und Verträge geprüft).
	Catalog      *skills.Catalog
	ClaudeBinary string
	// SnapshotDir ist das Verzeichnis der Skill-Snapshots (inhaltsadressiert, schreibgeschützt, wiederverwendet).
	SnapshotDir string

	// SystemPrompt ersetzt prompts/supervisor.md (Standard: prompts.Supervisor()).
	SystemPrompt string
	// Overrides sind ausdrückliche Auftragswerte für das Supervisor-Profil. Nur für Prüfläufe (zum Beispiel ein kleiner
	// Rauchlauf mit einem schlanken Modell); der Betrieb löst das Profil über Konfiguration und Agent auf.
	Overrides profiles.Overrides

	// MaxReplans: Neuplanungen nach Ablehnung, veraltetem Stand oder fehlender Ausgabe (Standard DefaultMaxReplans; ein
	// negativer Wert heißt: keine Neuplanung).
	MaxReplans   int
	MaxTurns     int           // Standard DefaultMaxTurns
	MaxRuntime   time.Duration // Standard DefaultMaxRuntime
	ContextLimit int           // Bytes des Planungszustands, Standard DefaultContextLimit

	Now   func() time.Time           // Standard time.Now
	NewID func(prefix string) string // Standard runner.NewID
}

// Planner plant mit dem Claude-Supervisor: Er stellt Prompt und Zustand zusammen, führt eine readonly-Sitzung mit
// strukturierter Ausgabe aus, setzt Projekt und Revision aus dem Snapshot und lässt den Validator den Plan gegen den
// aktuellen Zustand prüfen. Er wählt nichts aus und ersetzt den Supervisor nie: Ohne Supervisor oder ohne zulässigen Plan
// wartet die Planung sichtbar (*SuspendedError).
type Planner struct{ cfg PlannerConfig }

// NewPlanner prüft die Konfiguration und setzt die Standardwerte.
func NewPlanner(cfg PlannerConfig) (*Planner, error) {
	switch {
	case cfg.Runtime == nil:
		return nil, errors.New("planner: Runtime fehlt")
	case cfg.Source == nil:
		return nil, errors.New("planner: SnapshotSource fehlt")
	case cfg.Catalog == nil || !cfg.Catalog.Checked():
		return nil, errors.New("planner: Skill-Katalog fehlt oder ist nicht mit skills.LoadChecked geladen")
	case cfg.ClaudeBinary == "":
		return nil, errors.New("planner: Claude-Binary fehlt")
	case cfg.SnapshotDir == "":
		return nil, errors.New("planner: Snapshot-Verzeichnis fehlt")
	}
	if err := cfg.Policy.Check(); err != nil {
		return nil, fmt.Errorf("planner: %w", err)
	}
	if cfg.Profiles == nil {
		cfg.Profiles = cfg.Policy.Resolver
	}
	if cfg.SystemPrompt == "" {
		cfg.SystemPrompt = prompts.Supervisor()
	}
	switch {
	case cfg.MaxReplans == 0:
		cfg.MaxReplans = DefaultMaxReplans
	case cfg.MaxReplans < 0:
		cfg.MaxReplans = 0
	}
	if cfg.MaxTurns == 0 {
		cfg.MaxTurns = DefaultMaxTurns
	}
	if cfg.MaxRuntime == 0 {
		cfg.MaxRuntime = DefaultMaxRuntime
	}
	if cfg.ContextLimit == 0 {
		cfg.ContextLimit = DefaultContextLimit
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.NewID == nil {
		cfg.NewID = runner.NewID
	}
	return &Planner{cfg: cfg}, nil
}

// PlanResult ist ein angenommener Plan mit dem Protokoll aller Versuche.
type PlanResult struct {
	// Batch ist der angenommene Plan, mit Projekt und Revision aus dem Planungs-Snapshot.
	Batch domain.DecisionBatch
	// Validated trägt das wirksame Profil je Beauftragung (Validator.Accept).
	Validated ValidatedBatch
	Attempts  []Attempt
}

// Plan plant für den Snapshot (Ticketvertrag). Ein Fehler vom Typ *SuspendedError (errors.Is(err, ErrPlanningSuspended))
// ist der sichtbare Wartezustand mit allen Versuchen; ein anderer Fehler ist ein Abbruch des Aufrufers (Kontext).
func (p *Planner) Plan(ctx context.Context, snap ProjectSnapshot) (domain.DecisionBatch, error) {
	res, err := p.PlanWithRecord(ctx, snap)
	if err != nil {
		return domain.DecisionBatch{}, err
	}
	return res.Batch, nil
}

// feedback ist, was das Modell nach einem nicht angenommenen Versuch zurückbekommt.
type feedback struct {
	text  string
	batch []byte // der nicht angenommene Vorschlag (roh), falls vorhanden
}

// PlanWithRecord plant wie Plan und liefert zusätzlich das Protokoll aller Versuche.
//
// Ablauf je Versuch: Profil und Skill-Snapshot auflösen, Zustand und Prompt zusammenstellen, readonly-Sitzung mit
// strukturierter Ausgabe, Ausgabe lesen, Projekt und Revision aus dem Planungs-Snapshot setzen, gegen den aktuellen Zustand der
// SnapshotSource validieren. Bei Ablehnung, veraltetem Stand oder fehlender Ausgabe folgt höchstens MaxReplans-mal eine
// Neuplanung mit dem aktuellen Zustand und der Rückmeldung. Fehlt der Supervisor oder ist das Budget erschöpft, endet die
// Planung mit *SuspendedError; Go liefert nie einen Ersatzplan.
func (p *Planner) PlanWithRecord(ctx context.Context, snap ProjectSnapshot) (PlanResult, error) {
	var attempts []Attempt
	suspend := func(reason SuspendReason, detail string, cause error) (PlanResult, error) {
		return PlanResult{}, &SuspendedError{Reason: reason, Detail: detail, Attempts: attempts, Cause: cause}
	}
	planning := snap
	var fb *feedback
	for n := 1; n <= 1+p.cfg.MaxReplans; n++ {
		if err := ctx.Err(); err != nil {
			return PlanResult{}, err
		}
		if probs := planning.Problems(); len(probs) > 0 {
			return suspend(SuspendStateUnavailable, "Planungs-Snapshot ungültig: "+probs[0].String(), nil)
		}
		att, step := p.attempt(ctx, n, planning, fb)
		if ctx.Err() != nil {
			return PlanResult{}, ctx.Err()
		}
		attempts = append(attempts, att.record)
		if step.err != nil {
			return suspend(step.reason, step.detail, step.err)
		}
		if step.accepted != nil {
			return PlanResult{Batch: step.accepted.batch, Validated: step.accepted.validated, Attempts: attempts}, nil
		}
		planning, fb = step.next, step.feedback
	}
	last := attempts[len(attempts)-1]
	return suspend(SuspendBudgetExhausted,
		fmt.Sprintf("kein zulässiger Plan nach %d Versuchen (letztes Urteil: %s)", len(attempts), last.Verdict), nil)
}

type accepted struct {
	batch     domain.DecisionBatch
	validated ValidatedBatch
}

// outcome ist das Ergebnis eines Versuchs für die Schleife: angenommen, Neuplanung (next, feedback) oder Wartezustand (err).
type outcome struct {
	accepted *accepted
	next     ProjectSnapshot
	feedback *feedback
	err      error
	reason   SuspendReason
	detail   string
}

type attemptState struct{ record Attempt }

func (p *Planner) stop(rec Attempt, v Verdict, reason SuspendReason, detail string, cause error) (attemptState, outcome) {
	rec.Verdict, rec.FailureDetail = v, detail
	if cause == nil {
		cause = errors.New(detail)
	}
	return attemptState{rec}, outcome{err: cause, reason: reason, detail: detail}
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// attempt führt einen Versuch aus. Der Eintrag im Protokoll entsteht in jedem Fall.
func (p *Planner) attempt(ctx context.Context, n int, planning ProjectSnapshot, fb *feedback) (st attemptState, out outcome) {
	start := p.cfg.Now()
	defer func() { st.record.DurationMS = p.cfg.Now().Sub(start).Milliseconds() }()
	rec := Attempt{Number: n, StartedAt: start.UTC(), SnapshotRevision: planning.StateRevision, Replan: n > 1,
		Skills: []SkillRecord{}, ContextReduced: []string{}}

	snapJSON, err := json.Marshal(planning)
	if err != nil {
		return p.stop(rec, VerdictStateUnavailable, SuspendStateUnavailable, "Planungs-Snapshot nicht serialisierbar: "+err.Error(), nil)
	}
	rec.SnapshotSHA256 = sha256Hex(snapJSON)

	// 1. Zustand für das Modell (begrenzt, nichts Offenes wird weggelassen).
	pc, err := BuildPlanningContext(planning, p.cfg.ContextLimit)
	if err != nil {
		return p.stop(rec, VerdictStateUnavailable, SuspendContextTooLarge, err.Error(), err)
	}
	rec.ContextBytes, rec.ContextReduced = pc.Bytes, pc.Reductions

	// 2. Profil des Supervisors: Konfiguration, individueller Agent, ausdrückliche Auftragswerte.
	agent := ""
	for _, offer := range planning.Roles {
		if offer.Role == domain.RoleSupervisor {
			agent = offer.AgentID
			break
		}
	}
	profile, err := p.cfg.Profiles.ResolveProfile(domain.RoleSupervisor, agent, p.cfg.Overrides, p.cfg.Policy.Caps)
	if err != nil {
		return p.stop(rec, VerdictSupervisorFailed, SuspendSupervisorUnavailable, "Profil der Rolle supervisor nicht auflösbar: "+err.Error(), err)
	}
	rec.AgentID, rec.Profile = agent, profile
	if profile.PolicyProfile != runner.PolicyReadonly {
		return p.stop(rec, VerdictSupervisorFailed, SuspendSupervisorUnavailable,
			fmt.Sprintf("Die Planung läuft nur mit der Policy %s, das Profil der Rolle supervisor hat %q", runner.PolicyReadonly, profile.PolicyProfile), nil)
	}

	// 3. Skill-Snapshot der Rolle; sein Inhalt (die führenden Skills) geht deterministisch in den Prompt.
	snapshot, err := p.cfg.Catalog.Resolve(domain.RoleSupervisor, profile.SkillIDs)
	if err != nil {
		return p.stop(rec, VerdictSupervisorFailed, SuspendSupervisorUnavailable, "Skill-Snapshot der Rolle supervisor nicht auflösbar: "+err.Error(), err)
	}
	snapPath, err := p.writeSnapshot(snapshot)
	if err != nil {
		return p.stop(rec, VerdictSupervisorFailed, SuspendSupervisorUnavailable, "Skill-Snapshot nicht schreibbar: "+err.Error(), err)
	}
	rec.SkillSnapshotID = snapshot.ID()
	leading, records := p.leadingSkills(snapshot)
	rec.Skills = records
	if !slices.ContainsFunc(leading, func(s promptSkill) bool { return s.id == LeadingSkill }) {
		return p.stop(rec, VerdictSupervisorFailed, SuspendSupervisorUnavailable,
			fmt.Sprintf("Der führende Skill %s fehlt im Skill-Snapshot der Rolle supervisor (Skills: %v)", LeadingSkill, snapshot.SkillIDs()), nil)
	}

	// 4. Prompt.
	prompt := buildPrompt(planning, pc, leading, fb)
	rec.PromptBytes, rec.PromptSHA256 = len(prompt), sha256Hex([]byte(prompt))
	rec.SystemPromptSize, rec.SystemPromptSHA = len(p.cfg.SystemPrompt), sha256Hex([]byte(p.cfg.SystemPrompt))
	schema := ModelOutputSchema()
	rec.SchemaSHA256 = sha256Hex(schema)
	if fb != nil {
		rec.FeedbackSHA256 = sha256Hex([]byte(fb.text))
	}

	// 5. Sitzung: readonly, neutrales temporäres Verzeichnis, kleine Rundenzahl, Laufzeitgrenze.
	cwd, err := os.MkdirTemp("", "ccmas-plan-")
	if err != nil {
		return p.stop(rec, VerdictSupervisorFailed, SuspendSupervisorUnavailable, "neutrales Verzeichnis nicht anlegbar: "+err.Error(), err)
	}
	defer os.RemoveAll(cwd)
	rec.ExecutionID = p.cfg.NewID("plan")
	req := runner.Request{
		ProjectID: planning.ProjectID, ExecutionID: rec.ExecutionID, Attempt: 1, WorkOrderID: planning.Goal.ID,
		Profile: profile, Prompt: prompt, Cwd: cwd, ClaudeBinary: p.cfg.ClaudeBinary,
		MaxRuntimeSeconds: max(1, int(p.cfg.MaxRuntime/time.Second)), MaxTurns: p.cfg.MaxTurns,
		SystemPrompt: p.cfg.SystemPrompt, OutputSchema: schema,
		SkillSnapshot: &domain.SkillSnapshotRef{SnapshotID: snapshot.ID(), Path: snapPath},
	}
	exec, err := p.cfg.Runtime.Execute(ctx, req)
	if err != nil {
		return p.stop(rec, VerdictSupervisorFailed, SuspendSupervisorUnavailable, "Supervisor nicht ausführbar: "+err.Error(), err)
	}
	noteExecution(&rec, exec)

	raw, bad := evaluate(exec)
	switch {
	case bad != nil && bad.kind == badUnavailable:
		reason := SuspendSupervisorUnavailable
		if bad.limit {
			reason = SuspendSubscriptionLimit
		}
		return p.stop(rec, VerdictSupervisorFailed, reason, bad.detail, nil)
	case bad != nil: // keine gültige strukturierte Ausgabe: Neuplanung mit Rückmeldung
		rec.Verdict, rec.FailureDetail = VerdictNoOutput, bad.detail
		next, err := p.refresh(ctx, planning)
		if err != nil {
			return p.stop(rec, VerdictStateUnavailable, SuspendStateUnavailable, err.Error(), err)
		}
		text := "Die letzte Sitzung lieferte keine gültige strukturierte Ausgabe: " + bad.detail +
			" Gib das Planungsergebnis ausschließlich als strukturierte Ausgabe zurück."
		return attemptState{rec}, outcome{next: next, feedback: &feedback{text: text}}
	}
	rec.RawOutput = raw

	// 6. Lesen, Projekt und Revision aus dem Planungs-Snapshot setzen, gegen den aktuellen Zustand prüfen.
	current, err := p.refresh(ctx, planning)
	if err != nil {
		return p.stop(rec, VerdictStateUnavailable, SuspendStateUnavailable, err.Error(), err)
	}
	rec.CurrentRevision = current.StateRevision
	rec.Stale = current.StateRevision != planning.StateRevision

	batch, derr := decodeModelBatch(raw)
	if derr != nil {
		// Eine Ausgabe, die sich nicht als Planungsergebnis lesen lässt, ist eine Ablehnung des Batches (schema).
		rc := &collector{}
		rc.add(-1, "", CodeSchema, "%s", derr.Error())
		rej := rc.err().(*RejectionError)
		return p.rejected(rec, current, raw, rej)
	}
	batch = stamp(batch, planning, n)
	vb, err := Validator{}.Accept(batch, current, p.cfg.Policy)
	if err != nil {
		rej, ok := AsRejection(err)
		if !ok {
			return p.stop(rec, VerdictSupervisorFailed, SuspendSupervisorUnavailable, err.Error(), err)
		}
		return p.rejected(rec, current, raw, rej)
	}
	rec.Verdict = VerdictAccepted
	return attemptState{rec}, outcome{accepted: &accepted{batch: batch, validated: vb}}
}

func (p *Planner) rejected(rec Attempt, current ProjectSnapshot, raw []byte, rej *RejectionError) (attemptState, outcome) {
	rec.Verdict = VerdictRejected
	rec.RejectionCodes, rec.RejectionText = rej.Codes(), rej.Error()
	return attemptState{rec}, outcome{next: current, feedback: &feedback{text: rej.Error(), batch: raw}}
}

// refresh holt den aktuellen Zustand und prüft ihn auf Projekt und Gültigkeit.
func (p *Planner) refresh(ctx context.Context, planning ProjectSnapshot) (ProjectSnapshot, error) {
	cur, err := p.cfg.Source.CurrentSnapshot(ctx, planning.ProjectID)
	if err != nil {
		return ProjectSnapshot{}, fmt.Errorf("aktueller Zustand nicht lesbar: %w", err)
	}
	if cur.ProjectID != planning.ProjectID {
		return ProjectSnapshot{}, fmt.Errorf("aktueller Zustand gehört zu Projekt %q, geplant wird für %q", cur.ProjectID, planning.ProjectID)
	}
	if probs := cur.Problems(); len(probs) > 0 {
		return ProjectSnapshot{}, fmt.Errorf("aktueller Zustand ungültig: %s", probs[0])
	}
	return cur, nil
}

// writeSnapshot schreibt den Skill-Snapshot (oder verwendet den vorhandenen gleicher Kennung wieder) und liefert den
// kanonischen Pfad, wie ihn der Runner erwartet.
func (p *Planner) writeSnapshot(s skills.Snapshot) (string, error) {
	return writeSnapshotDir(s, p.cfg.SnapshotDir)
}

// ---- Auswertung der Ausführung ----------------------------------------------------------------------------

type badKind int

const (
	badUnavailable badKind = iota + 1 // der Supervisor lief nicht bis zu einem Ergebnis
	badNoOutput                       // Ergebnis ohne gültige strukturierte Ausgabe
)

type badRun struct {
	kind   badKind
	detail string
	limit  bool
}

// Codes der strukturierten Ausgabe im Runner (Vertrag: runner-v1, Fehlercodes in contracts.md).
var structuredOutputCodes = []string{"structured_output_failed", "structured_output_missing", "structured_output_invalid", "structured_output_too_large"}

// evaluate liest die strukturierte Ausgabe einer Ausführung oder sagt, warum es keine gibt.
func evaluate(exec runner.Execution) (json.RawMessage, *badRun) {
	if raw, ok := exec.StructuredOutput(); ok {
		if res, _ := exec.Result(); res.Outcome == domain.OutcomeSucceeded {
			return raw, nil
		}
	}
	for _, w := range exec.Warnings() {
		if w.Kind == domain.ErrKindSubscriptionLimit {
			return nil, &badRun{kind: badUnavailable, limit: true, detail: "Abo-Limit erreicht (" + w.Code + ")"}
		}
	}
	if te, ok := exec.TerminalError(); ok {
		if slices.Contains(structuredOutputCodes, te.Code) {
			return nil, &badRun{kind: badNoOutput, detail: fmt.Sprintf("%s: %s", te.Code, te.Message)}
		}
		return nil, &badRun{kind: badUnavailable, detail: fmt.Sprintf("Runner meldet %s: %s", te.Code, te.Message)}
	}
	if res, ok := exec.Result(); ok {
		if res.Outcome == domain.OutcomeSucceeded {
			return nil, &badRun{kind: badNoOutput, detail: "structured_output_missing: Ergebnis ohne strukturierte Ausgabe."}
		}
		return nil, &badRun{kind: badUnavailable, detail: fmt.Sprintf("Sitzung endete mit %s: %s", res.Outcome, res.Summary)}
	}
	if exec.Crashed {
		return nil, &badRun{kind: badUnavailable, detail: fmt.Sprintf("Runner-Prozess ohne Ergebnis beendet (Exit-Code %d)", exec.ExitCode)}
	}
	return nil, &badRun{kind: badUnavailable, detail: fmt.Sprintf("Sitzung ohne Ergebnis (Zustand %s)", exec.State)}
}

// noteExecution übernimmt die Angaben der Sitzung ins Protokoll.
func noteExecution(rec *Attempt, exec runner.Execution) {
	rec.ExecState, rec.Violations = string(exec.State), len(exec.Violations)
	if ss := exec.SessionStarted; ss != nil {
		rec.SessionID, rec.EffectiveModel = ss.SessionID, ss.Model
		rec.LoadedSnapshotID, rec.LoadedSkills = ss.SkillSnapshotID, slices.Clone(ss.Skills)
		rec.ClaudeVersion, rec.SDKVersion = ss.ClaudeVersion, ss.SDKVersion
	}
	if te, ok := exec.TerminalError(); ok {
		rec.TerminalCode = te.Code
	}
}

// decodeModelBatch liest die Ausgabe des Modells als Planungsergebnis. Felder, die Go selbst setzt, dürfen fehlen; unbekannte
// Felder und Folgedaten sind Fehler.
func decodeModelBatch(raw []byte) (domain.DecisionBatch, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var b domain.DecisionBatch
	if err := dec.Decode(&b); err != nil {
		return domain.DecisionBatch{}, fmt.Errorf("Die strukturierte Ausgabe ist kein Planungsergebnis: %v", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return domain.DecisionBatch{}, errors.New("Die strukturierte Ausgabe enthält Daten nach dem Planungsergebnis")
	}
	return b, nil
}

// stamp setzt, was Go bestimmt: Schemaversion, Batch-ID, Projekt und Zustandsrevision (Batch und Entscheidungen) aus dem
// Planungs-Snapshot. Was das Modell dort angab, wird überschrieben: Der Bezug zum Zustand kommt nie aus der Modellausgabe, damit
// die Erkennung veralteter Stände aussagekräftig bleibt.
func stamp(b domain.DecisionBatch, planning ProjectSnapshot, attempt int) domain.DecisionBatch {
	b.SchemaVersion = 1
	b.BatchID = fmt.Sprintf("plan-r%d-v%d", planning.StateRevision, attempt)
	b.ProjectID, b.StateRevision = planning.ProjectID, planning.StateRevision
	ds := make([]domain.Decision, len(b.Decisions))
	for i, d := range b.Decisions {
		d.SchemaVersion = 1
		d.ProjectID, d.StateRevision = planning.ProjectID, planning.StateRevision
		ds[i] = d
	}
	b.Decisions = ds
	return b
}

// ---- Skills und Prompt -----------------------------------------------------------------------------------

type promptSkill struct {
	id, tree string
	version  int
	body     string
}

// leadingSkills nennt die Skills, deren Inhalt in den Prompt übernommen wird: die Standardausstattung der Rolle supervisor
// im Snapshot (default_for), aufsteigend nach ID, jeweils der Text von SKILL.md ohne Frontmatter. Der Snapshot ist die Quelle;
// der Katalog wird für den Inhalt nicht erneut gelesen.
func (p *Planner) leadingSkills(s skills.Snapshot) ([]promptSkill, []SkillRecord) {
	var out []promptSkill
	var records []SkillRecord
	for _, d := range s.Document().Skills {
		def, ok := p.cfg.Catalog.Skill(d.ID)
		rec := SkillRecord{ID: d.ID, Version: d.Version, TreeSHA256: d.TreeSHA256}
		if ok && def.DefaultForRole(domain.RoleSupervisor) {
			if raw, found := s.File(d.ID, "SKILL.md"); found {
				out = append(out, promptSkill{id: d.ID, tree: d.TreeSHA256, version: d.Version, body: skillBody(string(raw))})
				rec.InPrompt = true
			}
		}
		records = append(records, rec)
	}
	return out, records
}

// skillBody entfernt das Frontmatter einer SKILL.md.
func skillBody(s string) string {
	rest, ok := strings.CutPrefix(s, "---\n")
	if !ok {
		return strings.TrimSpace(s)
	}
	_, body, found := strings.Cut(rest, "\n---\n")
	if !found {
		return strings.TrimSpace(s)
	}
	return strings.TrimSpace(body)
}

// freeSlots ist die Zahl freier Worker-Plätze des Snapshots (der Supervisor zählt nicht mit).
func freeSlots(s ProjectSnapshot) (free, active int) {
	for _, a := range s.Capacity.Active {
		if a.Role != domain.RoleSupervisor {
			active++
		}
	}
	return max(0, s.Capacity.MaxWorkers-active), active
}

const maxFeedbackBatchBytes = 16 << 10

// buildPrompt setzt den Auftragstext deterministisch zusammen: Kopf, Entscheidungsabläufe (Inhalt der führenden Skills aus
// dem geprüften Snapshot), Zustand und, bei einer Neuplanung, die Rückmeldung zum letzten Versuch. Gleiche Eingaben ergeben
// dieselben Bytes.
func buildPrompt(planning ProjectSnapshot, pc PlanningContext, leading []promptSkill, fb *feedback) string {
	var b strings.Builder
	free, active := freeSlots(planning)
	g := planning.Goal
	fmt.Fprintf(&b, "# Planungsauftrag\n\nProjekt: %s\nZustandsrevision: %d\nBeauftragtes Ziel: %s %s", planning.ProjectID, planning.StateRevision, g.Kind, g.ID)
	if g.Title != "" {
		fmt.Fprintf(&b, " (%s)", g.Title)
	}
	fmt.Fprintf(&b, "\nIntegrationsmodus: %s\nWorker-Plätze: %d frei von %d (laufende Worker-Ausführungen: %d)\n", planning.IntegrationMode, free, planning.Capacity.MaxWorkers, active)
	b.WriteString("\nPlane die nächste Arbeit für dieses Ziel nach den Entscheidungsabläufen unten und gib das Planungsergebnis als strukturierte Ausgabe zurück.\n")

	b.WriteString("\n# Entscheidungsabläufe (Skills)\n")
	for _, s := range leading {
		fmt.Fprintf(&b, "\n## Skill %s (Version %d, tree_sha256 %s)\n\n%s\n", s.id, s.version, s.tree, s.body)
	}

	b.WriteString("\n# Zustand (JSON)\n")
	if len(pc.Reductions) > 0 {
		fmt.Fprintf(&b, "\nHinweis: Der Zustand wurde zur Größenbegrenzung verkürzt (%s). Offene und blockierte Aufträge sind vollständig.\n", strings.Join(pc.Reductions, ", "))
	}
	b.WriteString("\n```json\n" + pc.JSON + "\n```\n")

	if fb != nil {
		b.WriteString("\n# Rückmeldung zum letzten Versuch\n\nDer letzte Versuch wurde nicht angenommen. Plane auf dem oben stehenden aktuellen Zustand neu und behebe jeden genannten Punkt.\n\n")
		b.WriteString(fb.text + "\n")
		if len(fb.batch) > 0 {
			shown := compactJSON(fb.batch)
			if len(shown) > maxFeedbackBatchBytes {
				shown = shorten(shown, maxFeedbackBatchBytes)
			}
			b.WriteString("\nDein abgelehnter Vorschlag:\n\n```json\n" + shown + "\n```\n")
		}
	}
	return b.String()
}

func compactJSON(raw []byte) string {
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return string(raw)
	}
	return buf.String()
}
