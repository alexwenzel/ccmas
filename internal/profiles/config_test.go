package profiles

import (
	"strings"
	"testing"

	"github.com/alexwenzel/ccmas/internal/domain"
)

func TestConfigRoundTrip(t *testing.T) {
	cfg := testConfig()
	cfg.Revision = 5
	rp := cfg.Roles[domain.RoleReviewer]
	rp.SkillIDs = []string{skReview}
	rp.AllowedModels = []string{mBig}
	rp.AllowedEfforts = []domain.Effort{domain.EffortHigh, domain.EffortMax}
	cfg.Roles[domain.RoleReviewer] = rp
	cfg.Agents["rev-1"] = Agent{Role: domain.RoleReviewer, Model: mBig, Effort: domain.EffortMax, SkillIDs: []string{skReview}}
	cfg.Agents["impl-1"] = Agent{Role: domain.RoleImplementer}

	raw, err := cfg.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseConfig(raw)
	if err != nil {
		t.Fatalf("eigene Ausgabe nicht lesbar: %v\n%s", err, raw)
	}
	if got.Revision != 5 || len(got.Roles) != len(domain.Roles()) || len(got.Agents) != 2 {
		t.Fatalf("Config = %+v", got)
	}
	again, _ := got.Marshal()
	if string(again) != string(raw) {
		t.Errorf("Marshal ist nicht stabil:\n%s\n---\n%s", raw, again)
	}
	r := got.Roles[domain.RoleReviewer]
	if r.Model != mBig || r.PolicyProfile != "readonly" || len(r.AllowedEfforts) != 2 || r.SkillIDs[0] != skReview {
		t.Errorf("Rollenprofil = %+v", r)
	}
	if a := got.Agents["rev-1"]; a.Role != domain.RoleReviewer || a.Effort != domain.EffortMax {
		t.Errorf("Agent = %+v", a)
	}
	if a := got.Agents["impl-1"]; a.Model != "" || a.Effort != "" || len(a.SkillIDs) != 0 {
		t.Errorf("Agent ohne Werte erhielt Werte: %+v", a)
	}
	if strings.Contains(string(raw), "secret") || strings.Contains(string(raw), "token") {
		t.Error("Konfiguration enthält verdächtige Schlüssel")
	}
}

func validConfigYAML(t *testing.T) string {
	t.Helper()
	cfg := testConfig()
	cfg.Agents["rev-1"] = Agent{Role: domain.RoleReviewer, Model: mBig}
	raw, err := cfg.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestParseConfigRejects(t *testing.T) {
	base := validConfigYAML(t)
	if _, err := ParseConfig([]byte(base)); err != nil {
		t.Fatal(err)
	}
	mut := func(old, new string) string {
		t.Helper()
		if !strings.Contains(base, old) {
			t.Fatalf("Ausgangsdatei enthält %q nicht:\n%s", old, base)
		}
		return strings.Replace(base, old, new, 1)
	}
	cases := map[string]string{
		"unbekannter Schlüssel":      mut("revision: 1", "revision: 1\nzusatz: 1"),
		"falsche Schema-Version":     mut("schema_version: 1", "schema_version: 7"),
		"Revision null":              mut("revision: 1", "revision: 0"),
		"unbekannte Rolle":           mut("  reviewer:\n", "  wizard:\n"),
		"Rolle fehlt":                strings.Replace(base, "  completion:\n    runtime: claude\n    model: "+mBig+"\n    effort: high\n    policy: readonly\n", "", 1),
		"unbekannte Runtime":         mut("runtime: claude", "runtime: gpt"),
		"unbekannter Effort":         mut("effort: high", "effort: ultra"),
		"Modell leer":                mut("model: "+mBig, "model: \"\""),
		"Modell mit Leerzeichen":     mut("model: "+mBig, "model: \"a b\""),
		"Policy fehlt":               mut("policy: scoped_write", "policy: \"\""),
		"Agent: unbekannte Rolle":    mut("role: reviewer", "role: wizard"),
		"Agent: ungültige ID":        mut("  rev-1:", "  \"rev 1\":"),
		"Agent: Effort unbekannt":    mut("role: reviewer", "role: reviewer\n    effort: mittel"),
		"Skill doppelt":              mut("role: reviewer", "role: reviewer\n    skills: [a-b, a-b]"),
		"Skill-ID ungültig":          mut("role: reviewer", "role: reviewer\n    skills: [\"a b\"]"),
		"erlaubte Modelle doppelt":   mut("runtime: claude", "runtime: claude\n    allowed_models: [a, a]"),
		"erlaubte Efforts unbekannt": mut("runtime: claude", "runtime: claude\n    allowed_efforts: [mittel]"),
		"zwei Dokumente":             base + "---\nrevision: 2\n",
		"Geheimnis als Schlüssel":    mut("revision: 1", "revision: 1\napi_key: sk-123"),
		"leer":                       "",
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ParseConfig([]byte(raw))
			wantCode(t, err, CodeInvalidConfig)
		})
	}
}

func TestConfigCloneIsDeep(t *testing.T) {
	cfg := testConfig()
	rp := cfg.Roles[domain.RoleReviewer]
	rp.SkillIDs, rp.AllowedModels, rp.AllowedEfforts = []string{skReview}, []string{mBig}, []domain.Effort{domain.EffortHigh}
	cfg.Roles[domain.RoleReviewer] = rp
	cfg.Agents["a"] = Agent{Role: domain.RoleReviewer, SkillIDs: []string{skReview}}
	c := cfg.Clone()
	c.Roles[domain.RoleReviewer].SkillIDs[0] = "x"
	c.Roles[domain.RoleReviewer].AllowedModels[0] = "x"
	c.Roles[domain.RoleReviewer].AllowedEfforts[0] = domain.EffortMax
	c.Agents["a"].SkillIDs[0] = "x"
	delete(c.Agents, "a")
	if cfg.Roles[domain.RoleReviewer].SkillIDs[0] != skReview || cfg.Roles[domain.RoleReviewer].AllowedModels[0] != mBig ||
		cfg.Roles[domain.RoleReviewer].AllowedEfforts[0] != domain.EffortHigh || cfg.Agents["a"].SkillIDs[0] != skReview {
		t.Error("Clone teilt Speicher")
	}
}
