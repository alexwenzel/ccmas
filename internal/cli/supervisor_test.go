package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexwenzel/ccmas/internal/domain"
	"github.com/alexwenzel/ccmas/internal/skills"
)

// ---- profile resolve: Wahl des Supervisors ---------------------------------------------------------

func resolveJSON(t *testing.T, e *profileEnv, args ...string) domain.AgentProfile {
	t.Helper()
	var p domain.AgentProfile
	decode(t, e.ok("profile", append([]string{"resolve"}, append(args, "--json")...)...), &p)
	return p
}

func TestProfileResolveSupervisorChoice(t *testing.T) {
	e := newProfileEnv(t).initialized()
	// Rollenstandard der Dokumentation: claude-sonnet-5-5, medium, Standardausstattung (tsDoc).
	base := resolveJSON(t, e, "--role", "documentation")
	if base.Model != "claude-sonnet-5-5" || base.Effort != domain.EffortMedium ||
		base.ValueSources.Model != domain.SourceRole || base.ValueSources.Effort != domain.SourceRole || base.ValueSources.SkillIDs != domain.SourceRole {
		t.Fatalf("Rollenstandard = %+v", base)
	}

	p := resolveJSON(t, e, "--role", "documentation",
		"--supervisor-model", "claude-opus-5-5", "--supervisor-effort", "max", "--supervisor-skill", tsDoc)
	if p.Model != "claude-opus-5-5" || p.Effort != domain.EffortMax || len(p.SkillIDs) != 1 || p.SkillIDs[0] != tsDoc {
		t.Fatalf("Wahl des Supervisors nicht wirksam: %+v", p)
	}
	want := domain.ValueSources{Model: domain.SourceSupervisor, Effort: domain.SourceSupervisor, SkillIDs: domain.SourceSupervisor}
	if *p.ValueSources != want {
		t.Errorf("Herkunft = %+v, erwartet %+v", *p.ValueSources, want)
	}
	if _, err := domain.ParseAgentProfile(mustMarshal(t, p)); err != nil {
		t.Errorf("Ausgabe verletzt den Vertrag agent-profile: %v", err)
	}

	// Jedes Feld einzeln: nur der Effort wird vom Supervisor gewählt.
	p = resolveJSON(t, e, "--role", "documentation", "--supervisor-effort", "high")
	if p.Model != base.Model || p.Effort != domain.EffortHigh || p.ValueSources.Model != domain.SourceRole || p.ValueSources.Effort != domain.SourceSupervisor {
		t.Errorf("einzelnes Feld: %+v", p)
	}

	// Text-Ausgabe nennt die Herkunft.
	text := e.ok("profile", "resolve", "--role", "documentation", "--supervisor-model", "claude-opus-5-5")
	if !strings.Contains(text, "claude-opus-5-5 (Herkunft: supervisor)") {
		t.Errorf("Text:\n%s", text)
	}
}

// TestProfileResolvePrecedenceOrderAgentSupervisorRole belegt die Rangfolge Auftrag > Agent > Supervisor > Rolle
// über die CLI, je Feld einzeln.
func TestProfileResolvePrecedenceOrderAgentSupervisorRole(t *testing.T) {
	e := newProfileEnv(t).initialized()
	e.ok("agent", "set", "doc-1", "--role", "documentation", "--model", "claude-haiku-5-5", "--effort", "low")
	sup := []string{"--supervisor-model", "claude-opus-5-5", "--supervisor-effort", "high"}

	// Rolle: sonnet/medium.
	role := resolveJSON(t, e, "--role", "documentation")
	// Supervisor vor Rolle.
	p := resolveJSON(t, e, append([]string{"--role", "documentation"}, sup...)...)
	if p.Model != "claude-opus-5-5" || p.Effort != domain.EffortHigh || p.ValueSources.Model != domain.SourceSupervisor || p.ValueSources.Effort != domain.SourceSupervisor {
		t.Errorf("Supervisor > Rolle: %+v (Rolle: %+v)", p, role)
	}
	// Agent vor Supervisor.
	p = resolveJSON(t, e, append([]string{"--role", "documentation", "--agent", "doc-1"}, sup...)...)
	if p.Model != "claude-haiku-5-5" || p.Effort != domain.EffortLow || p.ValueSources.Model != domain.SourceAgent || p.ValueSources.Effort != domain.SourceAgent {
		t.Errorf("Agent > Supervisor: %+v", p)
	}
	// Auftrag vor Agent und Supervisor; das Modell bleibt beim Agenten (Rangfolge je Feld).
	p = resolveJSON(t, e, append([]string{"--role", "documentation", "--agent", "doc-1", "--effort", "max"}, sup...)...)
	if p.Effort != domain.EffortMax || p.ValueSources.Effort != domain.SourceOrder || p.Model != "claude-haiku-5-5" || p.ValueSources.Model != domain.SourceAgent {
		t.Errorf("Auftrag > Agent > Supervisor: %+v", p)
	}
	p = resolveJSON(t, e, append([]string{"--role", "documentation", "--agent", "doc-1", "--model", "claude-sonnet-5-5"}, sup...)...)
	if p.Model != "claude-sonnet-5-5" || p.ValueSources.Model != domain.SourceOrder || p.Effort != domain.EffortLow || p.ValueSources.Effort != domain.SourceAgent {
		t.Errorf("Auftragsmodell: %+v", p)
	}
	// Skills: Supervisor vor Rollenstandard, Agent und Auftrag davor.
	e.ok("agent", "set", "doc-2", "--role", "documentation", "--skill", tsDoc)
	p = resolveJSON(t, e, "--role", "documentation", "--agent", "doc-2", "--supervisor-skill", tsDoc)
	if p.ValueSources.SkillIDs != domain.SourceAgent {
		t.Errorf("Skills: Agent > Supervisor: %+v", p.ValueSources)
	}
	p = resolveJSON(t, e, "--role", "documentation", "--supervisor-skill", tsDoc)
	if p.ValueSources.SkillIDs != domain.SourceSupervisor {
		t.Errorf("Skills: Supervisor > Rolle: %+v", p.ValueSources)
	}
	p = resolveJSON(t, e, "--role", "documentation", "--agent", "doc-2", "--skill", tsDoc, "--supervisor-skill", tsDoc)
	if p.ValueSources.SkillIDs != domain.SourceOrder {
		t.Errorf("Skills: Auftrag > Agent: %+v", p.ValueSources)
	}
}

func TestProfileResolveSupervisorChoiceIsChecked(t *testing.T) {
	e := newProfileEnv(t).initialized()
	for name, tc := range map[string]struct {
		args  []string
		code  int
		parts []string
	}{
		"unbekannte Stufe":               {[]string{"--supervisor-effort", "ultra"}, ExitUsage, []string{"ultra"}},
		"leerer Skill":                   {[]string{"--supervisor-skill", " "}, ExitUsage, []string{"--supervisor-skill"}},
		"unbekanntes Modell":             {[]string{"--supervisor-model", "claude-gibts-nicht"}, ExitError, []string{"[unknown_model]", "Wahl des Supervisors"}},
		"Skill für die Rolle verboten":   {[]string{"--supervisor-skill", tsImpl}, ExitError, []string{"[skill_not_allowed_for_role]", "Wahl des Supervisors"}},
		"noch nicht freigegebener Skill": {[]string{"--supervisor-skill", tsPlan}, ExitError, []string{"[skill_not_approved]"}},
	} {
		t.Run(name, func(t *testing.T) {
			code, out, errOut := e.run("profile", append([]string{"resolve", "--role", "documentation"}, tc.args...)...)
			wantFail(t, code, errOut, tc.code, tc.parts...)
			if out != "" {
				t.Errorf("stdout %q", out)
			}
		})
	}
	// Ein vom Modell nicht unterstützter Effort ist auch als Wahl des Supervisors nur mit genehmigtem Fallback zulässig.
	e.write("models.yaml", modelsWithLimitedSonnet(false))
	code, _, errOut := e.run("profile", "resolve", "--role", "implementer", "--supervisor-effort", "xhigh")
	wantFail(t, code, errOut, ExitError, "[unsupported_effort]", "Wahl des Supervisors")
	e.write("models.yaml", modelsWithLimitedSonnet(true))
	p := resolveJSON(t, e, "--role", "implementer", "--supervisor-effort", "xhigh")
	if p.Effort != domain.EffortHigh || p.RequestedEffort != domain.EffortXHigh || p.Fallback == nil || p.ValueSources.Effort != domain.SourceSupervisor {
		t.Errorf("sichtbare Reduktion der Wahl des Supervisors fehlt: %+v", p)
	}
}

func TestProfileResolveHelpMentionsSupervisorFlags(t *testing.T) {
	e := newProfileEnv(t).initialized()
	help := e.ok("profile", "resolve", "--help")
	for _, s := range []string{"--supervisor-model", "--supervisor-effort", "--supervisor-skill", "Wahl des Supervisors", "Auftragswert > Agentenwert > Wahl des Supervisors > Rollenstandard"} {
		if !strings.Contains(help, s) {
			t.Errorf("Hilfe enthält %q nicht:\n%s", s, help)
		}
	}
	if strings.Contains(help, "noch keinen CLI-Schalter") || strings.Contains(help, "kommt mit CC-14") {
		t.Errorf("Hilfe beschreibt den Schalter noch als offen:\n%s", help)
	}
}

func TestProfileResolveSupervisorChoiceDoesNotWrite(t *testing.T) {
	e := newProfileEnv(t).initialized()
	before := e.file("agents.yaml") + e.file("models.yaml")
	e.ok("profile", "resolve", "--role", "implementer", "--supervisor-model", "claude-opus-5-5")
	if e.file("agents.yaml")+e.file("models.yaml") != before {
		t.Error("resolve hat Konfiguration geschrieben")
	}
}

// ---- Produktive Snapshot-Pfade laden geprüft (skills.LoadChecked) ---------------------------------------------

// removeRulesFile entfernt die Regeldatei eines kopierten Bestands: Load lädt weiter, LoadChecked nicht.
func removeRulesFile(t *testing.T, root string) {
	t.Helper()
	if err := os.Remove(filepath.Join(root, "rules", "roles.yaml")); err != nil {
		t.Fatal(err)
	}
}

func TestSkillsSnapshotLoadsChecked(t *testing.T) {
	t.Run("ohne Regeldatei wird kein Snapshot geschrieben", func(t *testing.T) {
		root := copyCatalog(t)
		removeRulesFile(t, root)
		out := t.TempDir()
		code, stdout, errOut := runSkills(t, root, "snapshot", "--role", "concept", "--out", out)
		wantFail(t, code, errOut, ExitError, "[missing_rules]")
		if stdout != "" {
			t.Errorf("stdout %q", stdout)
		}
		if entries, _ := os.ReadDir(out); len(entries) != 0 {
			t.Errorf("trotz Befund geschrieben: %v", entries)
		}
	})
	t.Run("ungültige Regeldatei", func(t *testing.T) {
		root := copyCatalog(t)
		editFile(t, filepath.Join(root, "rules", "roles.yaml"), "max_rounds: 3", "max_rounds: 99")
		out := t.TempDir()
		code, _, errOut := runSkills(t, root, "snapshot", "--role", "concept", "--out", out)
		wantFail(t, code, errOut, ExitError, "[invalid_rules]")
		if entries, _ := os.ReadDir(out); len(entries) != 0 {
			t.Errorf("trotz Befund geschrieben: %v", entries)
		}
	})
	t.Run("Regeln verletzen eine feste Plattformregel", func(t *testing.T) {
		root := copyCatalog(t)
		editFile(t, filepath.Join(root, "rules", "roles.yaml"), "always_forbidden: [start_agents, change_platform_limits]", "always_forbidden: [start_agents]")
		out := t.TempDir()
		code, _, errOut := runSkills(t, root, "snapshot", "--role", "concept", "--out", out)
		wantFail(t, code, errOut, ExitError, "[invalid_rules]", "change_platform_limits")
		if entries, _ := os.ReadDir(out); len(entries) != 0 {
			t.Errorf("trotz Befund geschrieben: %v", entries)
		}
	})
	t.Run("list und show bleiben bei Load und zeigen den Bestand auch ohne Regeldatei", func(t *testing.T) {
		// Begründung: Wer einen Bestand prüft, muss ihn auch ohne gültige Plattformregeln ansehen können;
		// list und show schreiben nichts und entscheiden nichts.
		root := copyCatalog(t)
		removeRulesFile(t, root)
		if code, out, errOut := runSkills(t, root, "list"); code != ExitOK || !strings.Contains(out, "ccmas-code-review") {
			t.Errorf("list: Exit-Code %d, stderr %q", code, errOut)
		}
		if code, _, errOut := runSkills(t, root, "show", "ccmas-code-review"); code != ExitOK {
			t.Errorf("show: Exit-Code %d, stderr %q", code, errOut)
		}
		// validate dagegen meldet die fehlende Regeldatei.
		code, _, errOut := runSkills(t, root, "validate")
		wantFail(t, code, errOut, ExitError)
	})
	t.Run("gültiger Bestand schreibt weiter", func(t *testing.T) {
		out := t.TempDir()
		t.Cleanup(func() { skills.RemoveSnapshotDir(out) })
		code, stdout, errOut := runSkills(t, copyCatalog(t), "snapshot", "--role", "reviewer", "--out", out)
		if code != ExitOK || errOut != "" || !strings.Contains(stdout, "snapshot_id: sha256:") {
			t.Fatalf("Exit-Code %d, stdout %q, stderr %q", code, stdout, errOut)
		}
	})
}

func TestProfileVerifyLoadsCatalogChecked(t *testing.T) {
	t.Run("ohne Regeldatei: Befund, kein Live-Lauf", func(t *testing.T) {
		e := newVerifyEnv(t)
		removeRulesFile(t, e.root)
		code, out, errOut := e.run("profile", "verify", "--live", "--json")
		if code != ExitError {
			t.Fatalf("Exit-Code %d, stderr %q", code, errOut)
		}
		var v verifyJSON
		decode(t, out, &v)
		c, ok := v.check("Skill-Katalog")
		if !ok || c.Status != statusFail || !strings.Contains(c.Detail, "missing_rules") {
			t.Fatalf("Skill-Katalog: %+v", c)
		}
		if len(v.LiveRuns) != 0 {
			t.Errorf("Live-Läufe trotz Befund: %+v", v.LiveRuns)
		}
		e.assertNothingExecuted(t)
	})
	t.Run("mit Regeldatei: in Ordnung", func(t *testing.T) {
		e := newVerifyEnv(t)
		var v verifyJSON
		decode(t, e.ok("profile", "verify", "--json"), &v)
		c, ok := v.check("Skill-Katalog")
		if !ok || c.Status != statusOK || !strings.Contains(c.Detail, "Rollenregeln") {
			t.Fatalf("Skill-Katalog: %+v", c)
		}
	})
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
