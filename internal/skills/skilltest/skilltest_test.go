package skilltest_test

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/alexwenzel/ccmas/internal/domain"
	"github.com/alexwenzel/ccmas/internal/skills"
	"github.com/alexwenzel/ccmas/internal/skills/skilltest"
)

// Der Ordner muss den produktiven Prüfweg bestehen: LoadChecked ohne Befund, mit Regeln und Verträgen.
func TestNewRootPassesLoadChecked(t *testing.T) {
	root := skilltest.NewRoot(t)
	cat, rules, err := skills.LoadChecked(root)
	if err != nil {
		for _, f := range skills.FindingsOf(err) {
			t.Errorf("[%s] %s", f.Code, f.Message)
		}
		t.Fatal("LoadChecked meldet Befunde")
	}
	if rules == nil || cat == nil {
		t.Fatal("Katalog oder Regeln fehlen")
	}
	var ids []string
	for _, s := range cat.Skills() {
		ids = append(ids, s.ID)
	}
	if !slices.Equal(ids, []string{"ccmas-t-doc", "ccmas-t-impl", "ccmas-t-know", "ccmas-t-plan", "ccmas-t-review"}) {
		t.Fatalf("Skills = %v", ids)
	}
	// Workflow-Skills haben einen Vertrag, Hilfsskills nicht.
	for _, id := range []string{"ccmas-t-impl", "ccmas-t-review"} {
		if _, ok := cat.Contract(id); !ok {
			t.Errorf("%s hat keinen Vertrag", id)
		}
	}
	for _, id := range []string{"ccmas-t-know", "ccmas-t-doc", "ccmas-t-plan"} {
		if _, ok := cat.Contract(id); ok {
			t.Errorf("%s hat einen Vertrag", id)
		}
	}
	if got := cat.DefaultSkillIDs(domain.RoleReviewer); !slices.Equal(got, []string{"ccmas-t-review"}) {
		t.Errorf("Standard des Reviewers = %v", got)
	}
	if s, _ := cat.Skill("ccmas-t-plan"); s.Status != skills.StatusPendingAdaptation {
		t.Errorf("ccmas-t-plan = %+v", s)
	}
}

// Ohne Rollenregeln wäre der Ordner kein gültiger Softwareordner: LoadChecked meldet das (Abgrenzung zu Load).
func TestNewRootNeedsRulesForLoadChecked(t *testing.T) {
	root := skilltest.NewRoot(t)
	if err := os.Remove(filepath.Join(root, "rules", "roles.yaml")); err != nil {
		t.Fatal(err)
	}
	if _, err := skills.Load(root); err != nil {
		t.Fatalf("Load verlangt keine Regeln: %v", err)
	}
	_, _, err := skills.LoadChecked(root)
	var se *skills.Error
	if !errors.As(err, &se) || se.Code != skills.CodeMissingRules {
		t.Fatalf("LoadChecked ohne Regeln: %v", err)
	}
}

func TestNewRootWithCustomSkills(t *testing.T) {
	root := skilltest.NewRoot(t, skilltest.Skill{
		ID: "ccmas-x-doc", Roles: []domain.Role{domain.RoleDocumentation}, DefaultFor: []domain.Role{domain.RoleDocumentation},
		Files: map[string]string{"scripts/a.py": "print('a')\n"},
	})
	cat, _, err := skills.LoadChecked(root)
	if err != nil {
		t.Fatal(err)
	}
	s, ok := cat.Skill("ccmas-x-doc")
	if !ok || len(s.Files) != 2 || s.Category != skills.CategoryKnowledge || s.Status != skills.StatusApproved {
		t.Fatalf("Skill = %+v", s)
	}
	// Ein Snapshot lässt sich schreiben: Hashes und Dateien stimmen.
	if _, err := cat.Resolve(domain.RoleDocumentation, nil); err != nil {
		t.Fatal(err)
	}
}
