package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexwenzel/ccmas/internal/domain"
)

// Test-Skills des synthetischen Softwareordners: so hängen die Tests nicht vom Stand des echten
// Skill-Bestands ab.
const (
	tsDoc  = "ccmas-t-doc"  // freigegeben; documentation; Standard für documentation
	tsImpl = "ccmas-t-impl" // freigegeben; implementer; Standard für implementer
	tsPlan = "ccmas-t-plan" // pending_adaptation; supervisor
)

// syntheticRoot legt einen Softwareordner mit drei Test-Skills an.
func syntheticRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	type sk struct{ id, status, adapt, cat, roles, def string }
	list := []sk{
		{tsDoc, "approved", "", "knowledge", "[documentation]", "[documentation]"},
		{tsImpl, "approved", "", "delivery", "[implementer]", "[implementer]"},
		{tsPlan, "pending_adaptation", "wird angepasst", "planning", "[supervisor]", "[]"},
	}
	var b strings.Builder
	b.WriteString("schema_version: 1\nskills:\n")
	for _, s := range list {
		content := fmt.Sprintf("---\nname: %s\ndescription: Beschreibung von %s\n---\n\n# %s\n", s.id, s.id, s.id)
		dir := filepath.Join(root, ".claude", "skills", s.id)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256([]byte(content))
		fh := hex.EncodeToString(sum[:])
		tree := sha256.Sum256([]byte(fh + "  SKILL.md\n"))
		fmt.Fprintf(&b, "  - id: %s\n    version: 1\n    status: %s\n    adaptation: %q\n    category: %s\n    roles: %s\n    default_for: %s\n",
			s.id, s.status, s.adapt, s.cat, s.roles, s.def)
		fmt.Fprintf(&b, "    external_writes: []\n    tree_sha256: %s\n    files:\n      - path: SKILL.md\n        sha256: %s\n", hex.EncodeToString(tree[:]), fh)
		fmt.Fprintf(&b, "    source:\n      package: Skills/x.skill\n      package_sha256: %s\n      derived: \"2026-11-09\"\n      changes: Test\n", strings.Repeat("ab", 32))
	}
	if err := os.WriteFile(filepath.Join(root, ".claude", "skills", "manifest.yaml"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// profileEnv ist eine isolierte Umgebung: eigener Softwareordner, eigenes Konfigurationsverzeichnis.
type profileEnv struct {
	t    *testing.T
	root string
	dir  string
}

func newProfileEnv(t *testing.T) *profileEnv {
	t.Helper()
	t.Setenv(EnvHome, "")
	t.Setenv(EnvConfigDir, "")
	oldExe, oldEnv := executablePath, lookupEnv
	executablePath = func() (string, error) { return filepath.Join(t.TempDir(), "bin", "ccmas"), nil }
	lookupEnv = os.Getenv
	t.Cleanup(func() { executablePath, lookupEnv = oldExe, oldEnv })
	return &profileEnv{t: t, root: syntheticRoot(t), dir: filepath.Join(t.TempDir(), "config")}
}

// run führt ccmas mit --root und --config-dir aus (vor den Unterbefehlen verankert über die Gruppe).
func (e *profileEnv) run(group string, args ...string) (int, string, string) {
	e.t.Helper()
	full := append([]string{group}, args...)
	full = append(full, "--root", e.root, "--config-dir", e.dir)
	return run(e.t, full...)
}

func (e *profileEnv) ok(group string, args ...string) string {
	e.t.Helper()
	code, out, errOut := e.run(group, args...)
	if code != ExitOK {
		e.t.Fatalf("ccmas %s %v: Exit-Code %d, stderr %q", group, args, code, errOut)
	}
	return out
}

func (e *profileEnv) initialized() *profileEnv {
	e.t.Helper()
	e.ok("profile", "init")
	return e
}

func (e *profileEnv) file(name string) string {
	e.t.Helper()
	raw, err := os.ReadFile(filepath.Join(e.dir, name))
	if err != nil {
		e.t.Fatal(err)
	}
	return string(raw)
}

func (e *profileEnv) write(name, content string) {
	e.t.Helper()
	if err := os.WriteFile(filepath.Join(e.dir, name), []byte(content), 0o600); err != nil {
		e.t.Fatal(err)
	}
}

func decode(t *testing.T, out string, into any) {
	t.Helper()
	if err := json.Unmarshal([]byte(out), into); err != nil {
		t.Fatalf("keine gültige JSON-Ausgabe: %v\n%s", err, out)
	}
}

func wantFail(t *testing.T, code int, errOut string, wantCode int, parts ...string) {
	t.Helper()
	if code != wantCode {
		t.Fatalf("Exit-Code %d, erwartet %d (stderr %q)", code, wantCode, errOut)
	}
	for _, p := range parts {
		if !strings.Contains(errOut, p) {
			t.Errorf("stderr %q enthält %q nicht", errOut, p)
		}
	}
}

// ---- Konfigurationsverzeichnis ------------------------------------------------

func TestResolveConfigDirPrecedence(t *testing.T) {
	e := newProfileEnv(t)
	flagDir, envDir := filepath.Join(t.TempDir(), "flag"), filepath.Join(t.TempDir(), "env")

	got, err := resolveConfigDir("", e.root)
	if err != nil || got != filepath.Join(e.root, ".ccmas", "config") {
		t.Errorf("Standard = %q (%v)", got, err)
	}
	t.Setenv(EnvConfigDir, envDir)
	if got, _ := resolveConfigDir("", e.root); got != envDir {
		t.Errorf("Umgebung = %q, erwartet %q", got, envDir)
	}
	if got, _ := resolveConfigDir(flagDir, e.root); got != flagDir {
		t.Errorf("Flag = %q, erwartet %q", got, flagDir)
	}
	// Ohne Softwareordner, Flag und Umgebung gibt es keinen stillen Ersatz durch das Arbeitsverzeichnis.
	t.Setenv(EnvConfigDir, "")
	if _, err := resolveConfigDir("", ""); err == nil || !strings.Contains(err.Error(), "--config-dir") {
		t.Errorf("Fehler = %v, erwartet Hinweis auf --config-dir", err)
	}
}

func TestProfileUsesEnvConfigDir(t *testing.T) {
	e := newProfileEnv(t)
	envDir := filepath.Join(t.TempDir(), "envcfg")
	t.Setenv(EnvConfigDir, envDir)
	code, out, errOut := run(t, "profile", "init", "--root", e.root)
	if code != ExitOK {
		t.Fatalf("Exit-Code %d: %s", code, errOut)
	}
	if !strings.Contains(out, envDir) {
		t.Errorf("Ausgabe nennt %s nicht:\n%s", envDir, out)
	}
	if _, err := os.Stat(filepath.Join(envDir, "agents.yaml")); err != nil {
		t.Error(err)
	}
}

func TestProfileDefaultsToSoftwareRootConfigDir(t *testing.T) {
	e := newProfileEnv(t)
	code, _, errOut := run(t, "profile", "init", "--root", e.root)
	if code != ExitOK {
		t.Fatalf("Exit-Code %d: %s", code, errOut)
	}
	if _, err := os.Stat(filepath.Join(e.root, ".ccmas", "config", "models.yaml")); err != nil {
		t.Error(err)
	}
}

// ---- profile init -------------------------------------------------------------

func TestProfileInit(t *testing.T) {
	e := newProfileEnv(t)
	out := e.ok("profile", "init")
	for _, want := range []string{"models.yaml: angelegt", "agents.yaml: angelegt", e.dir} {
		if !strings.Contains(out, want) {
			t.Errorf("Ausgabe enthält %q nicht:\n%s", want, out)
		}
	}
	// Zweiter Aufruf überschreibt nie, auch keine veränderte Datei.
	e.write("agents.yaml", "# von Hand\n"+e.file("agents.yaml"))
	before := e.file("agents.yaml")
	out = e.ok("profile", "init")
	if strings.Count(out, "bereits vorhanden, nicht überschrieben") != 2 {
		t.Errorf("Ausgabe:\n%s", out)
	}
	if e.file("agents.yaml") != before {
		t.Error("init hat agents.yaml überschrieben")
	}
}

func TestProfileCommandsNeedInit(t *testing.T) {
	e := newProfileEnv(t)
	for _, args := range [][]string{{"profile", "list"}, {"profile", "show", "reviewer"}, {"profile", "set", "reviewer", "--effort", "low"},
		{"profile", "resolve", "--role", "reviewer"}, {"agent", "list"}, {"agent", "set", "a1", "--role", "reviewer"}} {
		code, out, errOut := e.run(args[0], args[1:]...)
		wantFail(t, code, errOut, ExitError, "profile init", "[not_initialized]")
		if out != "" {
			t.Errorf("%v: stdout %q", args, out)
		}
	}
	if _, err := os.Stat(e.dir); !os.IsNotExist(err) {
		t.Error("lesende und prüfende Befehle haben das Konfigurationsverzeichnis angelegt")
	}
}

// ---- profile list / show ------------------------------------------------------

func TestProfileListStartProfiles(t *testing.T) {
	e := newProfileEnv(t).initialized()
	var got struct {
		ConfigDir string `json:"config_dir"`
		Revision  int    `json:"revision"`
		Roles     []struct {
			Role   string   `json:"role"`
			Model  string   `json:"model"`
			Effort string   `json:"effort"`
			Policy string   `json:"policy"`
			Skills []string `json:"skills"`
		} `json:"roles"`
	}
	decode(t, e.ok("profile", "list", "--json"), &got)
	if got.ConfigDir != e.dir || got.Revision != 1 || len(got.Roles) != len(domain.Roles()) {
		t.Fatalf("Ausgabe = %+v", got)
	}
	want := map[string][3]string{
		"supervisor": {"claude-opus-5-5", "high", "readonly"}, "reviewer": {"claude-opus-5-5", "high", "readonly"},
		"concept_reviewer": {"claude-opus-5-5", "high", "readonly"}, "implementer": {"claude-sonnet-5-5", "high", "scoped_write"},
		"concept": {"claude-sonnet-5-5", "high", "readonly"}, "documentation": {"claude-sonnet-5-5", "medium", "readonly"},
		"project_setup": {"claude-sonnet-5-5", "high", "readonly"}, "completion": {"claude-sonnet-5-5", "high", "readonly"},
		"question_handler": {"claude-sonnet-5-5", "high", "readonly"},
	}
	for _, r := range got.Roles {
		w := want[r.Role]
		if r.Model != w[0] || r.Effort != w[1] || r.Policy != w[2] {
			t.Errorf("%s = %s/%s/%s, erwartet %v", r.Role, r.Model, r.Effort, r.Policy, w)
		}
		if r.Skills == nil {
			t.Errorf("%s: skills ist null statt []", r.Role)
		}
	}
	text := e.ok("profile", "list")
	for _, s := range []string{"ROLLE", "claude-opus-5-5", "scoped_write", "question_handler", "Revision 1"} {
		if !strings.Contains(text, s) {
			t.Errorf("Text enthält %q nicht:\n%s", s, text)
		}
	}
}

func TestProfileShow(t *testing.T) {
	e := newProfileEnv(t).initialized()
	e.ok("agent", "set", "doc-1", "--role", "documentation")
	text := e.ok("profile", "show", "documentation")
	for _, s := range []string{"claude-sonnet-5-5", "medium", "readonly", "Standardausstattung der Rolle", "alle konfigurierten", "doc-1"} {
		if !strings.Contains(text, s) {
			t.Errorf("Text enthält %q nicht:\n%s", s, text)
		}
	}
	var got struct {
		Profile struct {
			Role  string `json:"role"`
			Model string `json:"model"`
		} `json:"profile"`
		Agents []string `json:"agents"`
	}
	decode(t, e.ok("profile", "show", "documentation", "--json"), &got)
	if got.Profile.Role != "documentation" || got.Profile.Model != "claude-sonnet-5-5" || len(got.Agents) != 1 || got.Agents[0] != "doc-1" {
		t.Errorf("Ausgabe = %+v", got)
	}
	code, _, errOut := e.run("profile", "show", "wizard")
	wantFail(t, code, errOut, ExitUsage, "wizard")
	code, _, errOut = e.run("profile", "show")
	wantFail(t, code, errOut, ExitUsage)
}

// ---- profile set --------------------------------------------------------------

func TestProfileSet(t *testing.T) {
	e := newProfileEnv(t).initialized()
	out := e.ok("profile", "set", "reviewer", "--model", "claude-sonnet-5-5", "--effort", "xhigh")
	for _, s := range []string{"Rolle reviewer gespeichert", "Revision 2", "claude-sonnet-5-5", "xhigh", "nur für neue Ausführungen"} {
		if !strings.Contains(out, s) {
			t.Errorf("Ausgabe enthält %q nicht:\n%s", s, out)
		}
	}
	var got struct {
		Revision int `json:"revision"`
		Roles    []struct {
			Role   string `json:"role"`
			Model  string `json:"model"`
			Effort string `json:"effort"`
		} `json:"roles"`
	}
	decode(t, e.ok("profile", "list", "--json"), &got)
	if got.Revision != 2 {
		t.Errorf("Revision = %d", got.Revision)
	}
	for _, r := range got.Roles {
		switch r.Role {
		case "reviewer":
			if r.Model != "claude-sonnet-5-5" || r.Effort != "xhigh" {
				t.Errorf("reviewer = %+v", r)
			}
		case "supervisor":
			if r.Model != "claude-opus-5-5" || r.Effort != "high" {
				t.Errorf("andere Rolle verändert: %+v", r)
			}
		}
	}
}

func TestProfileSetRejectsInvalidWithoutWriting(t *testing.T) {
	e := newProfileEnv(t).initialized()
	before := e.file("agents.yaml")
	cases := map[string]struct {
		args  []string
		parts []string
	}{
		"unbekanntes Modell":            {[]string{"--model", "claude-gibts-nicht"}, []string{"[unknown_model]", "claude-gibts-nicht"}},
		"unbekanntes Policy-Profil":     {[]string{"--policy", "wild"}, []string{"[invalid_config]", "wild"}},
		"Skill noch nicht freigegeben":  {[]string{"--skill", tsPlan}, []string{"[skill_not_approved]", tsPlan}},
		"Skill für Rolle nicht erlaubt": {[]string{"--skill", tsImpl}, []string{"[skill_not_allowed_for_role]", tsImpl}},
		"Skill unbekannt":               {[]string{"--skill", "ccmas-gibts-nicht"}, []string{"[unknown_skill]"}},
		"Skill doppelt":                 {[]string{"--skill", tsDoc, "--skill", tsDoc}, []string{"[invalid_config]", tsDoc}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			role := "documentation"
			code, out, errOut := e.run("profile", append([]string{"set", role}, tc.args...)...)
			wantFail(t, code, errOut, ExitError, tc.parts...)
			if out != "" {
				t.Errorf("stdout %q", out)
			}
			if e.file("agents.yaml") != before {
				t.Error("agents.yaml wurde trotz Fehler verändert")
			}
		})
	}
}

func TestProfileSetUsageErrors(t *testing.T) {
	e := newProfileEnv(t).initialized()
	before := e.file("agents.yaml")
	cases := map[string][]string{
		"keine Änderung":          {"set", "reviewer"},
		"unbekannte Rolle":        {"set", "wizard", "--effort", "low"},
		"keine Rolle":             {"set", "--effort", "low"},
		"unbekannte Effort-Stufe": {"set", "reviewer", "--effort", "ultra"},
		"skill und clear-skills":  {"set", "reviewer", "--skill", tsDoc, "--clear-skills"},
		"leere Modell-ID":         {"set", "reviewer", "--model", ""},
		"leere Skill-ID":          {"set", "reviewer", "--skill", ""},
		"unbekanntes Flag":        {"set", "reviewer", "--gibtsnicht"},
		"zusätzliches Argument":   {"set", "reviewer", "extra", "--effort", "low"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			code, out, errOut := e.run("profile", args...)
			wantFail(t, code, errOut, ExitUsage)
			if out != "" {
				t.Errorf("stdout %q", out)
			}
			if e.file("agents.yaml") != before {
				t.Error("agents.yaml wurde verändert")
			}
		})
	}
}

func TestProfileSetSkills(t *testing.T) {
	e := newProfileEnv(t).initialized()
	e.ok("profile", "set", "documentation", "--skill", tsDoc)
	var got struct {
		Profile struct {
			Skills []string `json:"skills"`
		} `json:"profile"`
	}
	decode(t, e.ok("profile", "show", "documentation", "--json"), &got)
	if len(got.Profile.Skills) != 1 || got.Profile.Skills[0] != tsDoc {
		t.Errorf("Skills = %v", got.Profile.Skills)
	}
	e.ok("profile", "set", "documentation", "--clear-skills")
	decode(t, e.ok("profile", "show", "documentation", "--json"), &got)
	if len(got.Profile.Skills) != 0 {
		t.Errorf("Skills nach --clear-skills = %v", got.Profile.Skills)
	}
}

// modelsWithLimitedSonnet beschränkt Sonnet auf low bis high; auf Wunsch mit genehmigtem Fallback.
func modelsWithLimitedSonnet(withFallback bool) string {
	s := `schema_version: 1
revision: 1
source: "Testquelle"
checked_on: "2026-11-10"
policies: [readonly, scoped_write]
models:
  claude-opus-5-5:
    efforts: [low, medium, high, xhigh, max]
  claude-sonnet-5-5:
    efforts: [low, medium, high]
  claude-haiku-5-5:
    efforts: [low, medium, high, xhigh, max]
`
	if withFallback {
		s += `fallbacks:
  - model: claude-sonnet-5-5
    requested: xhigh
    effective: high
    source: "Testdoku"
`
	}
	return s
}

func TestEffortFallbackThroughCLI(t *testing.T) {
	t.Run("ohne genehmigten Fallback abgelehnt", func(t *testing.T) {
		e := newProfileEnv(t).initialized()
		e.write("models.yaml", modelsWithLimitedSonnet(false))
		before := e.file("agents.yaml")
		code, _, errOut := e.run("profile", "set", "implementer", "--effort", "xhigh")
		wantFail(t, code, errOut, ExitError, "[unsupported_effort]", "claude-sonnet-5-5", "xhigh")
		if e.file("agents.yaml") != before {
			t.Error("agents.yaml verändert")
		}
		code, _, errOut = e.run("profile", "resolve", "--role", "implementer", "--effort", "xhigh")
		wantFail(t, code, errOut, ExitError, "[unsupported_effort]")
	})
	t.Run("mit genehmigtem Fallback sichtbar reduziert", func(t *testing.T) {
		e := newProfileEnv(t).initialized()
		e.write("models.yaml", modelsWithLimitedSonnet(true))
		out := e.ok("profile", "set", "implementer", "--effort", "xhigh")
		if !strings.Contains(out, "reduziert") || !strings.Contains(out, "Testdoku") {
			t.Errorf("Ausgabe zeigt die Reduktion nicht:\n%s", out)
		}
		var p domain.AgentProfile
		decode(t, e.ok("profile", "resolve", "--role", "implementer", "--json"), &p)
		if p.Effort != domain.EffortHigh || p.RequestedEffort != domain.EffortXHigh || p.Fallback == nil || p.Fallback.Source != "Testdoku" {
			t.Errorf("Profil = %+v", p)
		}
		// Fallback gilt nur für diese Stufe: max bleibt abgelehnt.
		code, _, errOut := e.run("profile", "resolve", "--role", "implementer", "--effort", "max")
		wantFail(t, code, errOut, ExitError, "[unsupported_effort]")
		text := e.ok("profile", "resolve", "--role", "implementer")
		for _, s := range []string{"Effort angefordert: xhigh", "Effort wirksam:     high (abgeleitet, nicht gemessen)", "Fallback:", "effort_reduction"} {
			if !strings.Contains(text, s) {
				t.Errorf("Text enthält %q nicht:\n%s", s, text)
			}
		}
	})
}

// ---- profile resolve ----------------------------------------------------------

func TestProfileResolveJSONIsAValidAgentProfile(t *testing.T) {
	e := newProfileEnv(t).initialized()
	e.ok("agent", "set", "impl-1", "--role", "implementer", "--effort", "xhigh")
	out := e.ok("profile", "resolve", "--role", "implementer", "--agent", "impl-1", "--model", "claude-opus-5-5", "--json")
	p, err := domain.ParseAgentProfile([]byte(out)) // Schema-Prüfung des Vertrags
	if err != nil {
		t.Fatalf("Ausgabe verletzt den Vertrag agent-profile: %v\n%s", err, out)
	}
	if p.ProfileID != "implementer:impl-1" || p.Model != "claude-opus-5-5" || p.Effort != domain.EffortXHigh || p.SkillIDs[0] != tsImpl {
		t.Errorf("Profil = %+v", p)
	}
	want := domain.ValueSources{Model: domain.SourceOrder, Effort: domain.SourceAgent, SkillIDs: domain.SourceRole}
	if p.ValueSources == nil || *p.ValueSources != want {
		t.Errorf("value_sources = %+v, erwartet %+v", p.ValueSources, want)
	}
	if p.Version != 2 { // agents.yaml: Revision 2 (init, agent set) + models.yaml: Revision 1 - 1
		t.Errorf("version = %d, erwartet 2", p.Version)
	}
}

func TestProfileResolveOrderValuesWinOverAgent(t *testing.T) {
	e := newProfileEnv(t).initialized()
	e.ok("agent", "set", "doc-1", "--role", "documentation", "--effort", "low", "--skill", tsDoc)
	var p domain.AgentProfile
	decode(t, e.ok("profile", "resolve", "--role", "documentation", "--agent", "doc-1", "--effort", "max", "--json"), &p)
	if p.Effort != domain.EffortMax || p.ValueSources.Effort != domain.SourceOrder {
		t.Errorf("Effort = %s (%s)", p.Effort, p.ValueSources.Effort)
	}
	decode(t, e.ok("profile", "resolve", "--role", "documentation", "--agent", "doc-1", "--json"), &p)
	if p.Effort != domain.EffortLow || p.ValueSources.Effort != domain.SourceAgent || p.ValueSources.SkillIDs != domain.SourceAgent {
		t.Errorf("Agentenwerte wirken nicht: %+v", p)
	}
}

func TestProfileResolveErrors(t *testing.T) {
	e := newProfileEnv(t).initialized()
	e.ok("agent", "set", "doc-1", "--role", "documentation")
	for name, tc := range map[string]struct {
		args  []string
		code  int
		parts []string
	}{
		"ohne Rolle":                    {[]string{"resolve"}, ExitUsage, []string{"--role"}},
		"unbekannte Rolle":              {[]string{"resolve", "--role", "wizard"}, ExitUsage, []string{"wizard"}},
		"unbekannte Effort-Stufe":       {[]string{"resolve", "--role", "reviewer", "--effort", "ultra"}, ExitUsage, []string{"ultra"}},
		"unbekannter Agent":             {[]string{"resolve", "--role", "reviewer", "--agent", "gibts-nicht"}, ExitError, []string{"[unknown_agent]"}},
		"Agent mit falscher Rolle":      {[]string{"resolve", "--role", "reviewer", "--agent", "doc-1"}, ExitError, []string{"[agent_role_mismatch]"}},
		"unbekanntes Modell":            {[]string{"resolve", "--role", "reviewer", "--model", "claude-gibts-nicht"}, ExitError, []string{"[unknown_model]"}},
		"Skill für Rolle nicht erlaubt": {[]string{"resolve", "--role", "reviewer", "--skill", tsDoc}, ExitError, []string{"[skill_not_allowed_for_role]"}},
	} {
		t.Run(name, func(t *testing.T) {
			code, out, errOut := e.run("profile", tc.args...)
			wantFail(t, code, errOut, tc.code, tc.parts...)
			if out != "" {
				t.Errorf("stdout %q", out)
			}
		})
	}
}

func TestProfileResolveDoesNotWrite(t *testing.T) {
	e := newProfileEnv(t).initialized()
	before := e.file("agents.yaml") + e.file("models.yaml")
	e.ok("profile", "resolve", "--role", "supervisor", "--effort", "max")
	if e.file("agents.yaml")+e.file("models.yaml") != before {
		t.Error("resolve hat Konfiguration geschrieben")
	}
}

// ---- agent --------------------------------------------------------------------

func TestAgentSetListShowUnset(t *testing.T) {
	e := newProfileEnv(t).initialized()
	if out := e.ok("agent", "list"); !strings.Contains(out, "keine individuellen Agenten") {
		t.Errorf("leere Liste:\n%s", out)
	}
	out := e.ok("agent", "set", "impl-schwer", "--role", "implementer", "--model", "claude-opus-5-5", "--effort", "xhigh")
	for _, s := range []string{"Agent impl-schwer gespeichert", "Rolle implementer", "Revision 2", "claude-opus-5-5", "xhigh"} {
		if !strings.Contains(out, s) {
			t.Errorf("Ausgabe enthält %q nicht:\n%s", s, out)
		}
	}
	// Ein weiteres set ändert nur die genannten Felder.
	e.ok("agent", "set", "impl-schwer", "--role", "implementer", "--effort", "max")
	var show struct {
		Agent struct {
			ID     string   `json:"id"`
			Role   string   `json:"role"`
			Model  string   `json:"model"`
			Effort string   `json:"effort"`
			Skills []string `json:"skills"`
		} `json:"agent"`
	}
	decode(t, e.ok("agent", "show", "impl-schwer", "--json"), &show)
	if a := show.Agent; a.ID != "impl-schwer" || a.Role != "implementer" || a.Model != "claude-opus-5-5" || a.Effort != "max" || a.Skills == nil || len(a.Skills) != 0 {
		t.Errorf("Agent = %+v", a)
	}
	text := e.ok("agent", "show", "impl-schwer")
	if !strings.Contains(text, "(erbt vom Rollenprofil)") || !strings.Contains(text, "max") {
		t.Errorf("Text:\n%s", text)
	}
	var list struct {
		Agents []struct {
			ID string `json:"id"`
		} `json:"agents"`
	}
	decode(t, e.ok("agent", "list", "--json"), &list)
	if len(list.Agents) != 1 || list.Agents[0].ID != "impl-schwer" {
		t.Errorf("Liste = %+v", list)
	}
	if out := e.ok("agent", "list"); !strings.Contains(out, "impl-schwer") || !strings.Contains(out, "claude-opus-5-5") {
		t.Errorf("Text-Liste:\n%s", out)
	}

	// unset: einzelne Werte entfernen.
	out = e.ok("agent", "unset", "impl-schwer", "--effort")
	if !strings.Contains(out, "aktualisiert") {
		t.Errorf("Ausgabe:\n%s", out)
	}
	decode(t, e.ok("agent", "show", "impl-schwer", "--json"), &show)
	if show.Agent.Effort != "" || show.Agent.Model != "claude-opus-5-5" {
		t.Errorf("nach unset --effort: %+v", show.Agent)
	}
	e.ok("agent", "unset", "impl-schwer", "--model", "--skills")
	decode(t, e.ok("agent", "show", "impl-schwer", "--json"), &show)
	if show.Agent.Model != "" || show.Agent.Role != "implementer" {
		t.Errorf("nach unset --model --skills: %+v", show.Agent)
	}
	out = e.ok("agent", "unset", "impl-schwer", "--all")
	if !strings.Contains(out, "gelöscht") {
		t.Errorf("Ausgabe:\n%s", out)
	}
	code, _, errOut := e.run("agent", "show", "impl-schwer")
	wantFail(t, code, errOut, ExitError, "[unknown_agent]")
}

func TestAgentSetSkills(t *testing.T) {
	e := newProfileEnv(t).initialized()
	e.ok("agent", "set", "doc-1", "--role", "documentation", "--skill", tsDoc)
	var show struct {
		Agent struct {
			Skills []string `json:"skills"`
		} `json:"agent"`
	}
	decode(t, e.ok("agent", "show", "doc-1", "--json"), &show)
	if len(show.Agent.Skills) != 1 || show.Agent.Skills[0] != tsDoc {
		t.Errorf("Skills = %v", show.Agent.Skills)
	}
	e.ok("agent", "unset", "doc-1", "--skills")
	decode(t, e.ok("agent", "show", "doc-1", "--json"), &show)
	if len(show.Agent.Skills) != 0 {
		t.Errorf("Skills nach unset = %v", show.Agent.Skills)
	}
}

func TestAgentSetRejectsInvalidWithoutWriting(t *testing.T) {
	e := newProfileEnv(t).initialized()
	before := e.file("agents.yaml")
	for name, tc := range map[string]struct {
		args  []string
		parts []string
	}{
		"unbekanntes Modell":      {[]string{"set", "a1", "--role", "reviewer", "--model", "claude-gibts-nicht"}, []string{"[unknown_model]"}},
		"Skill nicht erlaubt":     {[]string{"set", "a1", "--role", "reviewer", "--skill", tsDoc}, []string{"[skill_not_allowed_for_role]"}},
		"Skill nicht freigegeben": {[]string{"set", "a1", "--role", "supervisor", "--skill", tsPlan}, []string{"[skill_not_approved]"}},
		"ungültige Agenten-ID":    {[]string{"set", "a b", "--role", "reviewer"}, []string{"[invalid_config]"}},
	} {
		t.Run(name, func(t *testing.T) {
			code, out, errOut := e.run("agent", tc.args...)
			wantFail(t, code, errOut, ExitError, tc.parts...)
			if out != "" {
				t.Errorf("stdout %q", out)
			}
			if e.file("agents.yaml") != before {
				t.Error("agents.yaml wurde trotz Fehler verändert")
			}
		})
	}
}

func TestAgentRoleAllowanceIsEnforced(t *testing.T) {
	e := newProfileEnv(t).initialized()
	cfg := strings.Replace(e.file("agents.yaml"), "  reviewer:\n    runtime: claude\n", "  reviewer:\n    runtime: claude\n    allowed_models: [claude-opus-5-5]\n", 1)
	if cfg == e.file("agents.yaml") {
		t.Fatal("Testdatei konnte nicht angepasst werden")
	}
	e.write("agents.yaml", cfg)
	code, _, errOut := e.run("agent", "set", "rev-1", "--role", "reviewer", "--model", "claude-haiku-5-5")
	wantFail(t, code, errOut, ExitError, "[not_allowed_for_role]", "claude-haiku-5-5")
	code, _, errOut = e.run("profile", "set", "reviewer", "--model", "claude-sonnet-5-5")
	wantFail(t, code, errOut, ExitError, "[not_allowed_for_role]")
	e.ok("agent", "set", "rev-1", "--role", "reviewer", "--model", "claude-opus-5-5")
}

func TestAgentUsageErrors(t *testing.T) {
	e := newProfileEnv(t).initialized()
	e.ok("agent", "set", "a1", "--role", "reviewer", "--effort", "low")
	before := e.file("agents.yaml")
	for name, args := range map[string][]string{
		"set ohne Rolle":             {"set", "a1"},
		"set mit unbekannter Rolle":  {"set", "a1", "--role", "wizard"},
		"set mit unbekanntem Effort": {"set", "a1", "--role", "reviewer", "--effort", "ultra"},
		"set ohne ID":                {"set", "--role", "reviewer"},
		"unset ohne Option":          {"unset", "a1"},
		"unset --all mit anderem":    {"unset", "a1", "--all", "--model"},
		"unset ohne ID":              {"unset", "--all"},
		"show ohne ID":               {"show"},
		"list mit Argument":          {"list", "extra"},
		"unbekannter Unterbefehl":    {"gibtsnicht"},
	} {
		t.Run(name, func(t *testing.T) {
			code, out, errOut := e.run("agent", args...)
			wantFail(t, code, errOut, ExitUsage)
			if out != "" {
				t.Errorf("stdout %q", out)
			}
			if e.file("agents.yaml") != before {
				t.Error("agents.yaml wurde verändert")
			}
		})
	}
	code, _, errOut := e.run("agent", "unset", "gibts-nicht", "--all")
	wantFail(t, code, errOut, ExitError, "[unknown_agent]")
}

func TestAgentChangeDoesNotAffectResolvedProfile(t *testing.T) {
	// Neue Werte gelten nur für neue Auflösungen: das früher ausgegebene Profil bleibt ein Wert.
	e := newProfileEnv(t).initialized()
	e.ok("agent", "set", "a1", "--role", "reviewer", "--effort", "low")
	var first domain.AgentProfile
	decode(t, e.ok("profile", "resolve", "--role", "reviewer", "--agent", "a1", "--json"), &first)
	e.ok("agent", "set", "a1", "--role", "reviewer", "--effort", "max")
	var second domain.AgentProfile
	decode(t, e.ok("profile", "resolve", "--role", "reviewer", "--agent", "a1", "--json"), &second)
	if first.Effort != domain.EffortLow || second.Effort != domain.EffortMax || second.Version <= first.Version {
		t.Errorf("erste Auflösung %+v, zweite %+v", first, second)
	}
}

func TestProfileAndAgentHelp(t *testing.T) {
	for _, g := range []string{"profile", "agent"} {
		code, out, _ := run(t, g)
		if code != ExitOK || !strings.Contains(out, "--config-dir") {
			t.Errorf("ccmas %s: Exit-Code %d, Ausgabe:\n%s", g, code, out)
		}
		code, _, errOut := run(t, g, "gibtsnicht")
		wantFail(t, code, errOut, ExitUsage, "gibtsnicht")
	}
}
