package profiles

import (
	"testing"

	"github.com/alexwenzel/ccmas/config"
	"github.com/alexwenzel/ccmas/internal/domain"
	"github.com/alexwenzel/ccmas/internal/skills"
)

// Startprofile laut Festlegung des Projektinhabers vom 10.11.2026.
var startProfiles = map[domain.Role]struct {
	model  string
	effort domain.Effort
	policy string
}{
	domain.RoleSupervisor:      {"claude-opus-5-5", domain.EffortHigh, "readonly"},
	domain.RoleReviewer:        {"claude-opus-5-5", domain.EffortHigh, "readonly"},
	domain.RoleConceptReviewer: {"claude-opus-5-5", domain.EffortHigh, "readonly"},
	domain.RoleImplementer:     {"claude-sonnet-5-5", domain.EffortHigh, "scoped_write"},
	domain.RoleConcept:         {"claude-sonnet-5-5", domain.EffortHigh, "readonly"},
	domain.RoleDocumentation:   {"claude-sonnet-5-5", domain.EffortMedium, "readonly"},
	domain.RoleProjectSetup:    {"claude-sonnet-5-5", domain.EffortHigh, "readonly"},
	domain.RoleCompletion:      {"claude-sonnet-5-5", domain.EffortHigh, "readonly"},
	domain.RoleQuestionHandler: {"claude-sonnet-5-5", domain.EffortHigh, "readonly"},
}

func loadExamples(t *testing.T) (Capabilities, Config) {
	t.Helper()
	caps, err := ParseCapabilities(config.ModelsExample())
	if err != nil {
		t.Fatalf("models.example.yaml: %v", err)
	}
	cfg, err := ParseConfig(config.AgentsExample())
	if err != nil {
		t.Fatalf("agents.example.yaml: %v", err)
	}
	return caps, cfg
}

func TestExampleCapabilities(t *testing.T) {
	caps, _ := loadExamples(t)
	if caps.Source == "" || caps.CheckedOn == "" {
		t.Error("Quelle und Prüfdatum müssen in models.example.yaml stehen")
	}
	// Laut Dokumentationstabelle „Effort levels by model“ (Stand 2026-11-10).
	for _, m := range []string{"claude-opus-5-5", "claude-sonnet-5-5", "claude-haiku-5-5"} {
		for _, e := range domain.Efforts() {
			if !caps.Supports(m, e) {
				t.Errorf("%s unterstützt laut Tabelle %s", m, e)
			}
		}
	}
	if len(caps.Models) != 3 {
		t.Errorf("Modelle = %v", caps.ModelIDs())
	}
	if len(caps.Fallbacks) != 0 {
		t.Errorf("das Beispiel genehmigt keine Fallbacks: %+v", caps.Fallbacks)
	}
	if len(caps.Policies) != 2 {
		t.Errorf("Policies = %v", caps.Policies)
	}
}

func TestStartProfilesFromExampleConfig(t *testing.T) {
	caps, cfg := loadExamples(t)
	if len(cfg.Agents) != 0 {
		t.Errorf("das Beispiel legt keine individuellen Agenten an: %v", cfg.Agents)
	}
	cat, err := skills.Load("../..")
	if err != nil {
		t.Fatalf("echter Skill-Bestand nicht ladbar: %v", err)
	}
	r, err := NewResolver(cfg, cat)
	if err != nil {
		t.Fatal(err)
	}
	domainCat := BuildCatalog(caps, cat)
	for role, want := range startProfiles {
		t.Run(string(role), func(t *testing.T) {
			p, err := r.ResolveProfile(role, "", Overrides{}, caps)
			if err != nil {
				t.Fatal(err)
			}
			if p.Model != want.model || p.Effort != want.effort || p.PolicyProfile != want.policy {
				t.Errorf("Profil = %s/%s/%s, erwartet %s/%s/%s", p.Model, p.Effort, p.PolicyProfile, want.model, want.effort, want.policy)
			}
			if p.Fallback != nil {
				t.Errorf("Startprofil nutzt einen Fallback: %+v", p.Fallback)
			}
			if err := domainCat.CheckProfile(p); err != nil {
				t.Errorf("Domain-Prüfung: %v", err)
			}
		})
	}
	if len(startProfiles) != len(domain.Roles()) {
		t.Errorf("Startprofile decken %d von %d Rollen ab", len(startProfiles), len(domain.Roles()))
	}
}
