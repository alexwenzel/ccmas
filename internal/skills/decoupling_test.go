package skills

import (
	"testing"

	"github.com/alexwenzel/ccmas/internal/domain"
)

// syntheticCatalog ist ein Katalog, wie ihn Aufrufer in eigenen Tests bauen: Skills der Workflow-Kategorien
// (review, delivery) ohne contract.yaml und ohne Regeldatei im Softwareordner.
func syntheticCatalog(t *testing.T) *catalogFixture {
	t.Helper()
	skills := defaultTestSkills()
	for i := range skills {
		skills[i].noContract = true
	}
	f := newCatalogFixture(t, skills)
	f.removeRules()
	return f
}

// TestLoadIsDecoupledFromPlatformRules hält fest, was Load verlangt und was nicht: Manifest, Dateien und
// vorhandene Verträge streng; weder rules/roles.yaml noch das Vorhandensein von Verträgen. Die Plattformregeln
// prüft ValidateWorkflow (workflow_test.go).
func TestLoadIsDecoupledFromPlatformRules(t *testing.T) {
	t.Run("Katalog ohne Regeldatei und ohne Verträge lädt", func(t *testing.T) {
		c := syntheticCatalog(t).mustLoad()
		if len(c.Skills()) != 5 {
			t.Fatalf("%d Skills geladen, erwartet 5", len(c.Skills()))
		}
		if len(c.Contracts()) != 0 {
			t.Errorf("Verträge = %v, erwartet keine", c.Contracts())
		}
		// Auch die Auswahl für die Workflow-Rollen funktioniert ohne Regeln.
		for role, id := range map[domain.Role]string{domain.RoleReviewer: "ccmas-demo-review", domain.RoleImplementer: "ccmas-demo-impl"} {
			snap, err := c.Resolve(role, nil)
			if err != nil {
				t.Fatalf("Resolve(%s): %v", role, err)
			}
			if ids := snap.SkillIDs(); len(ids) != 1 || ids[0] != id {
				t.Errorf("Resolve(%s) = %v, erwartet [%s]", role, ids, id)
			}
		}
	})

	t.Run("Load liest die Regeldatei nicht, auch eine kaputte stört nicht", func(t *testing.T) {
		f := syntheticCatalog(t)
		f.setRulesRaw("kaputt: [unvollständig\n")
		f.mustLoad()
	})

	t.Run("ein vorhandener Vertrag wird auch ohne Regeln streng gelesen", func(t *testing.T) {
		cases := map[string]struct {
			mutate func(t *testing.T, m map[string]any)
			code   Code
			field  string
		}{
			"Plattformschlüssel oben": {func(t *testing.T, m map[string]any) { m["max_workers"] = 9 }, CodePlatformLimit, "max_workers"},
			"Plattformschlüssel tief": {func(t *testing.T, m map[string]any) {
				list(t, at(t, m, "results", "findings"), "followups")[0].(map[string]any)["integration_mode"] = "auto"
			}, CodePlatformLimit, "integration_mode"},
			"unbekannter Schlüssel": {func(t *testing.T, m map[string]any) { m["bonus"] = 1 }, CodeInvalidContract, "bonus"},
			"Aktion unbekannt": {func(t *testing.T, m map[string]any) {
				at(t, m, "results", "findings")["followups"] = []any{map[string]any{"action": "deploy"}}
			}, CodeInvalidContract, "results.findings.followups[0]"},
			"Integration ohne review_approved": {func(t *testing.T, m map[string]any) {
				at(t, m, "results", "approved")["followups"] = []any{map[string]any{"action": "request_integration"}}
			}, CodeInvalidContract, "review_approved"},
			"Skill-ID fremd": {func(t *testing.T, m map[string]any) { m["skill"] = "ccmas-anderer" }, CodeInvalidContract, "skill"},
		}
		for name, tc := range cases {
			t.Run(name, func(t *testing.T) {
				f := syntheticCatalog(t)
				m := baseContract(t, defaultTestSkills()[0])
				tc.mutate(t, m)
				f.setContract("ccmas-demo-review", m)
				_, err := f.load()
				wantFinding(t, err, tc.code, tc.field)
			})
		}
	})

	t.Run("ein gültiger Vertrag lädt ohne Regeln und ist abrufbar", func(t *testing.T) {
		f := syntheticCatalog(t)
		f.setContract("ccmas-demo-review", baseContract(t, defaultTestSkills()[0]))
		c := f.mustLoad()
		ct, ok := c.Contract("ccmas-demo-review")
		if !ok || ct.Role != domain.RoleReviewer {
			t.Fatalf("Vertrag = %+v, %v", ct, ok)
		}
	})
}
