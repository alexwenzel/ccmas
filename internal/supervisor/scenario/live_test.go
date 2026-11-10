package scenario_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alexwenzel/ccmas/internal/skills"
	"github.com/alexwenzel/ccmas/internal/supervisor"
	"github.com/alexwenzel/ccmas/internal/supervisor/scenario"
	"github.com/alexwenzel/ccmas/internal/workers/runner"
)

// Live-Szenarien des Supervisors (CC-14, W14.3): Nachweis des Ergebnisziels von M02 mit echten Läufen. Der echte Planer läuft über
// den Go-Runner-Adapter und den echten Python-Runner mit dem Supervisorprofil der Beispielkonfiguration (ohne Overrides), dem
// Skill-Katalog dieses Worktrees (skills.LoadChecked) und den synthetischen Zuständen aus diesem Paket, nie dem echten Board.
// Jede Sitzung verbraucht Abo-Kontingent; das Budget sind SessionLimit Sitzungen für alle Szenarien zusammen.
//
//	CCMAS_LIVE_TESTS=1 go test ./internal/supervisor/scenario -run TestLiveSupervisorScenarios -count=1 -timeout 90m -v
//
// Ohne weitere Angabe laufen die Szenarien, für die noch kein Beleg existiert. Mit CCMAS_LIVE_SCENARIOS=S1,S3 laufen genau diese;
// ein vorhandener Beleg wird nie überschrieben. Eine Wiederholung ist nur nach einem Infrastrukturfehler im ersten Lauf zulässig
// (Abo-Limit, Zeitüberschreitung, Prozessfehler: Grund supervisor_unavailable oder subscription_limit) und braucht den Grund in
// CCMAS_LIVE_REPEAT_REASON; sie wird als zweiter Lauf an den Beleg gehängt. Ein inhaltlich schwacher Plan ist kein Grund.
// CCMAS_EVIDENCE_DIR ersetzt das Belegverzeichnis (zum Beispiel für einen Probelauf außerhalb des Repositories).

// recordingSource ist die SnapshotSource des Tests: Sie liefert nacheinander die Zustände (der letzte bleibt) und merkt sich,
// was sie geliefert hat, damit der Beleg den Prüfzustand jedes Versuchs vollständig enthält. Test-Double, nur hier.
type recordingSource struct {
	mu     sync.Mutex
	states []supervisor.ProjectSnapshot
	calls  int
	served []supervisor.ProjectSnapshot
}

func (s *recordingSource) CurrentSnapshot(_ context.Context, _ string) (supervisor.ProjectSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur := s.states[min(s.calls, len(s.states)-1)]
	s.calls++
	s.served = append(s.served, cur)
	return cur, nil
}

// countingRuntime zählt die echten Sitzungen und startet keine, wenn das Budget erschöpft wäre.
type countingRuntime struct {
	inner supervisor.Runtime
	mu    sync.Mutex
	used  *int
	limit int
}

var errBudget = errors.New("Sitzungsbudget erschöpft: keine weitere echte Sitzung")

func (c *countingRuntime) Execute(ctx context.Context, req runner.Request) (runner.Execution, error) {
	c.mu.Lock()
	if *c.used >= c.limit {
		c.mu.Unlock()
		return runner.Execution{}, errBudget
	}
	*c.used++
	c.mu.Unlock()
	return c.inner.Execute(ctx, req)
}

func descriptionOf(id scenario.ID) string {
	switch id {
	case scenario.S1:
		return "Drei Aufträge: sc1-a und sc1-b unabhängig und startbar, sc1-c hängt an sc1-a. Erwartet: A und B parallel, C wartet."
	case scenario.S2Routine:
		return "Routinekonzept (Version 0.1, ohne Freigabe) mit abhängiger Umsetzung. Erwartet: Konzeptarbeit, Konzeptreview oder eine Frage, nie Implementierung."
	case scenario.S2Strategic:
		return "Strategisches Konzept (scope_change, Version 0.1, ohne Freigabe) mit abhängiger Umsetzung. Erwartet: Frage an den Nutzer oder klärende Konzeptarbeit, nie Freigabe über den concept_reviewer, nie Implementierung."
	case scenario.S3:
		return "max_workers 2, eine Ausführung läuft, sc3-fin ist fertig ohne Review, sc3-new ist startbar. Erwartet: Review zuerst, höchstens ein freier Platz."
	case scenario.S4:
		return "Review von sc4-a mit BEFUNDE, Reparaturrunde 1 von 3. Erwartet: Reparatur (Implementierung durch implementer), keine Integration."
	case scenario.S5:
		return "PR-Modus, sc5-a freigegeben, PR #42 offen mit dem freigegebenen Commit, CI bestanden. Erwartet: wait."
	case scenario.S6:
		return "sc6-a ist ein Fehlerauftrag ohne Konzeptbezug, FREIGEGEBEN (Berichtsversion kein-konzept), noch kein PR. Während der Planung ändert sich die Zustandsrevision (12 auf 13): " +
			"der erste Plan wird mit stale_revision abgelehnt, die Neuplanung läuft auf dem frischen Snapshot (13) und wird angenommen. Erwartet: Integration von sc6-a."
	}
	return ""
}

func TestLiveSupervisorScenarios(t *testing.T) {
	if os.Getenv("CCMAS_LIVE_TESTS") != "1" {
		t.Skip("Live-Test: nur mit CCMAS_LIVE_TESTS=1 (verbraucht Abo-Kontingent)")
	}
	e := newEvEnv(t)
	binary, err := runner.ResolveClaudeBinary("", os.Getenv)
	if err != nil {
		t.Skipf("kein Claude-Binary: %v", err)
	}
	dir := os.Getenv("CCMAS_EVIDENCE_DIR")
	if dir == "" {
		dir = evidenceDir
	}
	used, err := countSessions(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("bereits belegte Sitzungen: %d von %d", used, SessionLimit)

	wanted := scenario.IDs()
	if env := os.Getenv("CCMAS_LIVE_SCENARIOS"); env != "" {
		wanted = nil
		for _, s := range strings.Split(env, ",") {
			wanted = append(wanted, scenario.ID(strings.TrimSpace(s)))
		}
	}
	repeatReason := os.Getenv("CCMAS_LIVE_REPEAT_REASON")

	for _, id := range wanted {
		path := evidenceFile(dir, id)
		_, statErr := os.Stat(path)
		exists := statErr == nil
		if exists && repeatReason == "" {
			t.Logf("%s: Beleg vorhanden, übersprungen", id)
			continue
		}
		t.Run(string(id), func(t *testing.T) {
			// Pro Szenario sind höchstens 1 + DefaultMaxReplans Sitzungen möglich: ohne diesen Platz startet das Szenario nicht,
			// damit das Budget nie überschritten wird.
			if need := 1 + supervisor.DefaultMaxReplans; used+need > SessionLimit {
				t.Skipf("Sitzungsbudget: %d von %d belegt, ein Szenario braucht bis zu %d", used, SessionLimit, need)
			}
			runScenario(t, e, id, binary, dir, path, repeatReason, &used, runner.New(runner.Config{SoftwareRoot: e.root}))
		})
	}
}

// runScenario plant ein Szenario mit dem Planer, schreibt den Beleg nach path und zählt die Sitzungen in used. inner führt die
// Sitzungen aus: im Live-Test der echte Runner; der Test der Belegkette setzt eine Fake-Runtime ein.
func runScenario(t *testing.T, e *evEnv, id scenario.ID, binary, dir, path, repeatReason string, used *int, inner supervisor.Runtime) {
	t.Helper()
	snap, err := scenario.Snapshot(id, e.offers)
	if err != nil {
		t.Fatal(err)
	}

	// Frühere Läufe (nur für die dokumentierte Wiederholung).
	var ev scenarioEvidence
	if _, err := os.Stat(path); err == nil {
		ev, err = loadEvidence(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := repeatAllowed(ev); err != nil {
			t.Fatalf("%s: %v", id, err)
		}
	} else if repeatReason != "" {
		t.Fatalf("%s: Wiederholungsgrund angegeben, aber kein Beleg vorhanden", id)
	}

	// Zustände, die die SnapshotSource liefert. S6: Zur Validierung des ersten Versuchs hat sich der Zustand geändert
	// (Revision + 1, gleichzeitige Änderung simuliert); danach bleibt der neue Stand.
	states := []supervisor.ProjectSnapshot{snap}
	if id == scenario.S6 {
		changed := snap
		changed.StateRevision++
		states = []supervisor.ProjectSnapshot{changed}
	}
	src := &recordingSource{states: states}

	snapDir := t.TempDir()
	t.Cleanup(func() {
		entries, _ := os.ReadDir(snapDir)
		for _, en := range entries {
			skills.RemoveSnapshotDir(filepath.Join(snapDir, en.Name()))
		}
	})
	rt := &countingRuntime{inner: inner, used: used, limit: SessionLimit}
	planner, err := supervisor.NewPlanner(supervisor.PlannerConfig{
		Runtime: rt, Source: src, Policy: e.pol, Catalog: e.cat, ClaudeBinary: binary, SnapshotDir: snapDir,
		// keine Overrides, keine Fremdwerte: Profil, Systemprompt, Schema, Rundenzahl und Laufzeitgrenze sind die Standardwerte des Planers
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	before := *used
	result, planErr := planner.PlanWithRecord(ctx, snap)

	run := runEvidence{Run: len(ev.Runs) + 1, RepeatReason: repeatReason}
	var attempts []supervisor.Attempt
	switch {
	case planErr == nil:
		run.Outcome, attempts = "accepted", result.Attempts
	default:
		se, ok := supervisor.AsSuspended(planErr)
		if !ok {
			t.Fatalf("Planung abgebrochen (kein Wartezustand, kein Beleg): %v", planErr)
		}
		run.Outcome, attempts = "suspended", se.Attempts
		run.SuspendReason, run.SuspendDetail = string(se.Reason), se.Detail
	}

	// Alle bisher gelieferten Zustände nach Hash, damit der Beleg den Prüfzustand vollständig trägt.
	byHash := map[string]supervisor.ProjectSnapshot{}
	for _, s := range append([]supervisor.ProjectSnapshot{snap}, src.served...) {
		byHash[snapshotHash(t, s)] = s
	}
	for _, a := range attempts {
		planning, ok := byHash[a.SnapshotSHA256] // der erste Versuch plant auf dem Snapshot des Szenarios, jede Neuplanung auf einem gelieferten Zustand
		if !ok {
			t.Fatalf("Versuch %d: Planungs-Snapshot %s weder der des Szenarios noch von der SnapshotSource geliefert", a.Number, a.SnapshotSHA256)
		}
		run.Attempts = append(run.Attempts, attemptEvidenceOf(t, id, a, planning, byHash))
		if a.SessionID != "" {
			run.Sessions++
		}
	}
	if got := *used - before; got != run.Sessions {
		t.Errorf("Zähler: %d Sitzungen gestartet, %d Versuche mit Session-ID", got, run.Sessions)
	}

	if run.Outcome == "accepted" {
		batch := result.Batch
		run.AcceptedBatch = &batch
		acceptedOn, ok := byHash[attempts[len(attempts)-1].SnapshotSHA256]
		if !ok {
			t.Fatal("Planungs-Snapshot des letzten Versuchs unbekannt")
		}
		run.Predicates = scenario.EvaluateDetailed(id, batch, acceptedOn)
		met := true
		for _, p := range run.Predicates {
			met = met && p.Met
		}
		run.PredicatesMet = &met
	}

	// Kontrollen des Aufbaus (nicht der Planungsqualität): echtes Modell laut Sitzung, Skills in der Sitzung.
	for _, a := range run.Attempts {
		if a.SessionID != "" && a.EffectiveModel != a.Profile.Model {
			t.Errorf("Versuch %d: wirksames Modell %q ≠ angefordertem %q", a.Number, a.EffectiveModel, a.Profile.Model)
		}
		if a.SessionID != "" && a.LoadedSkillSnapshotID != a.SkillSnapshotID {
			t.Errorf("Versuch %d: Skill-Snapshot der Sitzung %q ≠ %q", a.Number, a.LoadedSkillSnapshotID, a.SkillSnapshotID)
		}
	}

	ev.Evidence, ev.Scenario, ev.Description = "supervisor-scenario", string(id), descriptionOf(id)
	ev.Invocation = "CCMAS_LIVE_TESTS=1 go test ./internal/supervisor/scenario -run TestLiveSupervisorScenarios: echter Planer (internal/supervisor) über den Go-Runner-Adapter und den echten Python-Runner; " +
		"Supervisorprofil aus der Beispielkonfiguration per `profile init` in einem temporären Konfigurationsverzeichnis, keine Overrides; Skill-Katalog des Worktrees über skills.LoadChecked; synthetischer Zustand aus internal/supervisor/scenario"
	ev.Note = "Nachweis des Ergebnisziels von M02 (W14.3). Das Urteil über den Plan fällen der Validator (Zulässigkeit) und die Prädikate des Pakets scenario (Inhalt); ein Szenario, das sie verfehlt oder in " +
		"Planung ausgesetzt endet, bleibt mit diesem Ergebnis stehen. Der wirksame Effort ist angefordert, nicht gemessen (das SDK meldet ihn nicht). Die Zustände sind synthetisch, nicht das Board."
	ev.Redaction = "keine Pfade, keine Konto-, E-Mail- oder Organisationsdaten; nur IDs, Hashes, Versionen, Zustände, Ausgaben des Modells und Urteile (geprüft beim Schreiben und im Offline-Test)"
	ev.PolicyInputs = e.hashes
	ev.Runs = append(ev.Runs, run)
	ev.Final = finalEvidence{Run: run.Run, Outcome: run.Outcome, AttemptCount: len(run.Attempts), Sessions: run.Sessions, PredicatesMet: run.PredicatesMet}

	tmp := path + ".new"
	if err := writeEvidenceFile(tmp, ev); err != nil {
		t.Fatalf("Beleg nicht schreibbar: %v", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		t.Fatal(err)
	}
	met := "-"
	if run.PredicatesMet != nil {
		met = fmt.Sprint(*run.PredicatesMet)
	}
	var sessions []string
	for _, a := range run.Attempts {
		sessions = append(sessions, fmt.Sprintf("%s(%s %v)", a.SessionID, a.Verdict, a.Validation.Codes))
	}
	t.Logf("%s: %s, %d Versuch(e), Prädikate erfüllt: %s; Sitzungen: %s", id, run.Outcome, len(run.Attempts), met, strings.Join(sessions, " "))
	for _, p := range run.Predicates {
		if !p.Met {
			t.Logf("  Prädikat %s verletzt: %v", p.Name, p.Violations)
		}
	}
	if run.Outcome == "suspended" {
		t.Logf("  Planung ausgesetzt (%s): %s", run.SuspendReason, run.SuspendDetail)
	}
}

// repeatAllowed: Eine Wiederholung ist nur nach genau einem Lauf zulässig, der an der Infrastruktur scheiterte (Prozessfehler,
// Zeitüberschreitung, Abo-Limit: supervisor_unavailable, subscription_limit). Ein inhaltlich schwacher Plan ist kein Grund.
func repeatAllowed(prev scenarioEvidence) error {
	if len(prev.Runs) != 1 || prev.Runs[0].Outcome != "suspended" || !slices.Contains([]string{"supervisor_unavailable", "subscription_limit"}, prev.Runs[0].SuspendReason) {
		return fmt.Errorf("Wiederholung nur nach einem einzigen Lauf, der an der Infrastruktur scheiterte (supervisor_unavailable, subscription_limit); vorhanden: %d Lauf/Läufe", len(prev.Runs))
	}
	return nil
}

// attemptEvidenceOf übersetzt das Versuchsprotokoll des Planers ins Belegformat.
func attemptEvidenceOf(t *testing.T, id scenario.ID, a supervisor.Attempt, planning supervisor.ProjectSnapshot, byHash map[string]supervisor.ProjectSnapshot) attemptEvidence {
	t.Helper()
	if got := snapshotHash(t, planning); got != a.SnapshotSHA256 {
		t.Fatalf("Versuch %d: Planungs-Snapshot (%s) ≠ protokolliertem Hash (%s)", a.Number, got, a.SnapshotSHA256)
	}
	planRaw, err := json.Marshal(planning)
	if err != nil {
		t.Fatal(err)
	}
	ae := attemptEvidence{
		Number: a.Number, Date: a.StartedAt.UTC().Format("2006-01-02"), StartedAt: a.StartedAt.UTC().Format(time.RFC3339), Scenario: string(id),
		Snapshot: planRaw, SnapshotSHA256: a.SnapshotSHA256, SnapshotRevision: a.SnapshotRevision, ContextBytes: a.ContextBytes,
		ContextReductions:  nonNilStrings(a.ContextReduced),
		ValidationRevision: a.CurrentRevision, ValidationSnapshotSHA256: a.CurrentSnapshotSHA256, Stale: a.Stale,
		PromptSHA256: a.PromptSHA256, PromptBytes: a.PromptBytes, SystemPromptSHA256: a.SystemPromptSHA, SystemPromptBytes: a.SystemPromptSize,
		OutputSchemaSHA256: a.SchemaSHA256, FeedbackSHA256: a.FeedbackSHA256, Replan: a.Replan,
		SkillSnapshotID: a.SkillSnapshotID, Skills: a.Skills, LoadedSkillSnapshotID: a.LoadedSnapshotID, LoadedSkills: nonNilStrings(a.LoadedSkills),
		Profile: a.Profile, EffectiveModel: a.EffectiveModel, EffortEffective: "nicht gemessen (das SDK meldet den wirksamen Effort nicht; angefordert: " + string(a.Profile.RequestedEffort) + ")",
		SDKVersion: a.SDKVersion, CLIVersion: a.ClaudeVersion, SessionID: a.SessionID, ExecutionID: a.ExecutionID, DurationMS: a.DurationMS,
		TerminalCode: a.TerminalCode, FailureDetail: a.FailureDetail,
		RawOutput: a.RawOutput, StampedBatch: a.StampedBatch, Verdict: string(a.Verdict),
		Validation: validation{Accepted: a.Verdict == supervisor.VerdictAccepted, Codes: nonNilCodes(a.RejectionCodes), Rejections: nonNilRejections(a.Rejections), Text: a.RejectionText},
	}
	if a.CurrentSnapshotSHA256 != "" && a.CurrentSnapshotSHA256 != a.SnapshotSHA256 {
		cur, ok := byHash[a.CurrentSnapshotSHA256]
		if !ok {
			t.Fatalf("Versuch %d: Prüfzustand %s unbekannt", a.Number, a.CurrentSnapshotSHA256)
		}
		ae.ValidationSnapshot, _ = json.Marshal(cur)
	}
	if ae.ValidationSnapshotSHA256 == "" { // kein Zustand geholt (Sitzung ohne Ergebnis): es gab nichts zu prüfen
		ae.ValidationSnapshotSHA256, ae.ValidationRevision = a.SnapshotSHA256, a.SnapshotRevision
	}
	return ae
}

func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func nonNilCodes(c []supervisor.Code) []supervisor.Code {
	if c == nil {
		return []supervisor.Code{}
	}
	return c
}

func nonNilRejections(r []supervisor.Rejection) []supervisor.Rejection {
	if r == nil {
		return []supervisor.Rejection{}
	}
	return r
}
