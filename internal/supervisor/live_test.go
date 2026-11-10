package supervisor

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/alexwenzel/ccmas/config"
	"github.com/alexwenzel/ccmas/internal/domain"
	"github.com/alexwenzel/ccmas/internal/profiles"
	"github.com/alexwenzel/ccmas/internal/skills"
	"github.com/alexwenzel/ccmas/internal/workers/runner"
	"github.com/alexwenzel/ccmas/prompts"
)

// Live-Rauchlauf des Planers (CC-14, W14.2): prüft nur die Durchleitung durch den echten Runner und die echte CLI.
// Systemprompt, Skill-Snapshot und strukturierte Ausgabe müssen bei einem trivialen synthetischen Snapshot zu einem gültig
// lesbaren Planungsergebnis führen. Es ist kein Szenariobeleg (der folgt in W14.3) und prüft nicht die Planungsqualität.
//
// Der Lauf verbraucht Abo-Kontingent und läuft nur ausdrücklich, mit einem schlanken Modell über ausdrückliche Auftragswerte
// (Overrides.Order), nicht über das Supervisorprofil:
//
//	CCMAS_LIVE_TESTS=1 go test ./internal/supervisor -run TestLivePlanner -count=1 -v
//
// Mit CCMAS_EVIDENCE_DIR=<Verzeichnis> entsteht der redigierte Beleg live-supervisor-planner.jsonl.

const liveProbe = "CCMAS-PROBE-7Q4"

func TestLivePlannerPassThrough(t *testing.T) {
	if os.Getenv("CCMAS_LIVE_TESTS") != "1" {
		t.Skip("Live-Test: nur mit CCMAS_LIVE_TESTS=1 (verbraucht Abo-Kontingent)")
	}
	root, err := filepath.Abs(repoRoot)
	if err != nil {
		t.Fatal(err)
	}
	cat, rules, err := skills.LoadChecked(root)
	if err != nil {
		t.Fatal(err)
	}
	caps, err := profiles.ParseCapabilities(config.ModelsExample())
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := profiles.ParseConfig(config.AgentsExample())
	if err != nil {
		t.Fatal(err)
	}
	res, err := profiles.NewResolver(cfg, cat)
	if err != nil {
		t.Fatal(err)
	}
	pol, err := NewPolicy(res, caps, cat, rules)
	if err != nil {
		t.Fatal(err)
	}
	var lean string
	for _, id := range caps.ModelIDs() {
		if strings.Contains(id, "haiku") {
			lean = id
		}
	}
	if lean == "" {
		t.Skip("kein schlankes Modell (haiku) in der Beispielkonfiguration")
	}
	binary, err := runner.ResolveClaudeBinary("", os.Getenv)
	if err != nil {
		t.Skipf("kein Claude-Binary: %v", err)
	}

	// Trivialer synthetischer Snapshot: ein startbarer Auftrag, ein freigegebenes Konzept.
	snap := ProjectSnapshot{
		SchemaVersion: SnapshotSchemaVersion, ProjectID: "ccmas", StateRevision: 1,
		Goal:            Goal{Kind: domain.KindTicket, ID: "tk-live", Title: "Rauchlauf", AcceptanceCriteria: []string{"Auftrag umgesetzt"}, Scope: []string{}, OutOfScope: []string{}},
		IntegrationMode: domain.ModePR,
		Capacity:        Capacity{MaxWorkers: 3, Active: []ActiveExecution{}},
		Orders: []Order{{
			ID: "wo-live", Kind: OrderImplementation, Title: "Kleine, gut beschriebene Änderung", State: domain.WOWaiting, Mode: domain.ModePR, DependsOn: []string{},
			Concept: approvedConcept("1.0"),
		}},
		OpenQuestions: []OpenQuestion{},
		Roles:         BuildRoleOffers(cfg, caps, cat, nil),
		Skills:        BuildSkillOffers(cat),
	}
	if err := snap.Check(); err != nil {
		t.Fatal(err)
	}
	src := &fakeSource{snaps: []ProjectSnapshot{snap}}

	// Systemprompt der Rolle plus eine Kennung: Taucht sie in der Begründung des Plans auf, hat der Prompt das Modell erreicht.
	system := prompts.Supervisor() + "\nFür diesen Rauchlauf gilt zusätzlich: Nenne in der Begründung des Plans (Feld rationale auf oberster Ebene) das Kennwort " + liveProbe + ".\n"

	snapDir := t.TempDir()
	t.Cleanup(func() {
		entries, _ := os.ReadDir(snapDir)
		for _, en := range entries {
			skills.RemoveSnapshotDir(filepath.Join(snapDir, en.Name()))
		}
	})
	planner, err := NewPlanner(PlannerConfig{
		Runtime: runner.New(runner.Config{SoftwareRoot: root}), Source: src, Policy: pol, Catalog: cat, ClaudeBinary: binary, SnapshotDir: snapDir,
		SystemPrompt: system, MaxReplans: -1, MaxRuntime: 5 * time.Minute,
		Overrides: profiles.Overrides{Order: profiles.Selection{Model: lean, Effort: domain.EffortLow}},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	result, planErr := planner.PlanWithRecord(ctx, snap)

	var attempts []Attempt
	if planErr == nil {
		attempts = result.Attempts
	} else if se, ok := AsSuspended(planErr); ok {
		attempts = se.Attempts // abgelehnt ist erlaubt: geprüft wird nur die Durchleitung
	} else {
		t.Fatalf("Planung: %v", planErr)
	}
	if len(attempts) != 1 {
		t.Fatalf("%d Versuche (ein Lauf erwartet)", len(attempts))
	}
	a := attempts[0]
	t.Logf("Session %s, Modell %s (angefordert %s, Herkunft %s), CLI %s, SDK %s, Urteil %s %v, Dauer %d ms",
		a.SessionID, a.EffectiveModel, a.Profile.RequestedModel, a.Profile.ValueSources.Model, a.ClaudeVersion, a.SDKVersion, a.Verdict, a.RejectionCodes, a.DurationMS)

	// 1. Die Sitzung lief über den echten Runner, mit dem verlangten Modell.
	if a.SessionID == "" || a.SDKVersion == "" || a.ClaudeVersion == "" {
		t.Fatalf("keine Sitzung: %+v (Planung: %v)", a, planErr)
	}
	if a.Profile.ValueSources.Model != domain.SourceOrder || a.EffectiveModel != lean {
		t.Errorf("Modell %q (Herkunft %s), erwartet %q aus dem Auftragswert", a.EffectiveModel, a.Profile.ValueSources.Model, lean)
	}
	// 2. Skill-Snapshot: genau die Skills der Rolle supervisor sind in der Sitzung geladen.
	var want []string
	for _, s := range a.Skills {
		want = append(want, s.ID)
	}
	loaded := slices.Clone(a.LoadedSkills)
	slices.Sort(loaded)
	if a.LoadedSnapshotID != a.SkillSnapshotID || !slices.Equal(loaded, want) || len(want) < 2 {
		t.Errorf("Skills in der Sitzung %v (Snapshot %s), erwartet %v (Snapshot %s)", loaded, a.LoadedSnapshotID, want, a.SkillSnapshotID)
	}
	// 3. Strukturierte Ausgabe: gültig lesbar als Planungsergebnis (Go setzt Projekt und Revision, dann der Vertrag).
	if len(a.RawOutput) == 0 {
		t.Fatalf("keine strukturierte Ausgabe: Urteil %s, %s", a.Verdict, a.FailureDetail)
	}
	b, err := decodeModelBatch(a.RawOutput)
	if err != nil {
		t.Fatalf("Ausgabe nicht lesbar: %v\n%s", err, a.RawOutput)
	}
	stamped := stamp(b, snap, 1)
	rawBatch, _ := json.Marshal(stamped)
	if _, err := domain.ParseDecisionBatch(rawBatch); err != nil {
		t.Errorf("kein gültiger DecisionBatch: %v\n%s", err, rawBatch)
	}
	// 4. Systemprompt: die Kennung erreicht das Modell.
	probeSeen := strings.Contains(b.Rationale, liveProbe)
	if !probeSeen {
		t.Errorf("die Kennung %s des Systemprompts steht nicht in der Begründung: %q", liveProbe, b.Rationale)
	}
	writeLiveEvidence(t, root, a, planErr == nil, probeSeen, len(b.Decisions))
}

func writeLiveEvidence(t *testing.T, root string, a Attempt, accepted, probeSeen bool, decisions int) {
	t.Helper()
	target := os.Getenv("CCMAS_EVIDENCE_DIR")
	if target == "" {
		return
	}
	meta := map[string]any{
		"evidence": "live-supervisor-planner", "date": time.Now().Format("2006-01-02"), "sdk_version": a.SDKVersion, "cli_version": a.ClaudeVersion,
		"invocation": "go test ./internal/supervisor -run TestLivePlanner mit CCMAS_LIVE_TESTS=1: Planer mit echtem Go-Adapter und Runner; Modell und Effort als " +
			"ausdrückliche Auftragswerte (Overrides.Order), nicht das Supervisorprofil; eine Sitzung, keine Neuplanung",
		"note": "Rauchlauf der Durchleitung (W14.2), kein Szenariobeleg: Systemprompt (mit Kennung), Skill-Snapshot der Rolle supervisor und strukturierte " +
			"Ausgabe durch den echten Runner. Synthetischer Snapshot mit einem Auftrag. Der Effort ist angefordert, nicht gemessen.",
		"redaction": "keine Pfade, keine Konto-, E-Mail- oder Organisationsdaten; nur IDs, Hashes, Versionen, Urteil und die rohe strukturierte Ausgabe",
	}
	rec := map[string]any{
		"run": "planner_passthrough", "session_id": a.SessionID, "model": a.EffectiveModel, "requested_model": a.Profile.RequestedModel,
		"model_source": a.Profile.ValueSources.Model, "requested_effort": a.Profile.RequestedEffort, "effort_source": a.Profile.ValueSources.Effort,
		"skill_snapshot_id": a.SkillSnapshotID, "loaded_skill_snapshot_id": a.LoadedSnapshotID, "loaded_skills": a.LoadedSkills,
		"skills": a.Skills, "system_prompt_sha256": a.SystemPromptSHA, "prompt_sha256": a.PromptSHA256, "output_schema_sha256": a.SchemaSHA256,
		"snapshot_sha256": a.SnapshotSHA256, "context_bytes": a.ContextBytes, "duration_ms": a.DurationMS, "verdict": a.Verdict,
		"accepted_by_validator": accepted, "rejection_codes": a.RejectionCodes, "decisions": decisions, "system_prompt_probe_seen": probeSeen,
		"structured_output": json.RawMessage(a.RawOutput),
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	for _, v := range []any{meta, rec} {
		if err := enc.Encode(v); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "live-supervisor-planner.jsonl"), buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}
