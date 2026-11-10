package runner

import (
	"testing"

	"github.com/alexwenzel/ccmas/internal/domain"
	"github.com/alexwenzel/ccmas/internal/skills"
	"github.com/alexwenzel/ccmas/internal/skills/skilltest"
)

// Test-Skills des synthetischen Softwareordners: der Test hängt so nicht vom Stand des echten Bestands ab.
const (
	skillImpl = "ccmas-t-impl" // freigegeben; Rolle implementer
	skillDoc  = "ccmas-t-doc"  // freigegeben; Rolle documentation
)

// syntheticRoot legt einen gültigen Softwareordner an (skilltest: Manifest, Rollenregeln, Verträge).
func syntheticRoot(t *testing.T) string {
	t.Helper()
	return skilltest.NewRoot(t,
		skilltest.Skill{ID: skillImpl, Category: "delivery", Roles: []domain.Role{domain.RoleImplementer}},
		skilltest.Skill{ID: skillDoc, Category: "knowledge", Roles: []domain.Role{domain.RoleDocumentation}},
	)
}

// syntheticCatalog lädt den Ordner wie ein produktiver Aufrufer: geprüft (skills.LoadChecked).
func syntheticCatalog(t *testing.T) *skills.Catalog {
	t.Helper()
	cat, _, err := skills.LoadChecked(syntheticRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	return cat
}
