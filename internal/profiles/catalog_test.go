package profiles

import (
	"errors"
	"slices"
	"testing"

	"github.com/alexwenzel/ccmas/internal/domain"
)

func TestBuildCatalogBridgesCapabilitiesAndSkills(t *testing.T) {
	caps := testCaps()
	cat := BuildCatalog(caps, newTestSkills(t))

	// Keine zweite Modellliste: genau die Modelle und Stufen der Capabilities.
	if len(cat.Models) != len(caps.Models) {
		t.Fatalf("Modelle = %v", cat.Models)
	}
	for m, efforts := range caps.Models {
		if !slices.Equal(cat.Models[m], efforts) {
			t.Errorf("Modell %s: %v, erwartet %v", m, cat.Models[m], efforts)
		}
	}
	// Nur freigegebene Skills sind bekannt.
	for _, id := range []string{skKnow, skDoc, skExtra, skImpl, skReview} {
		if !cat.Skills[id] {
			t.Errorf("freigegebener Skill %s fehlt", id)
		}
	}
	if cat.Skills[skPlan] {
		t.Error("noch nicht freigegebener Skill ist bekannt")
	}
	if !cat.Policies["readonly"] || !cat.Policies["scoped_write"] || len(cat.Policies) != 2 {
		t.Errorf("Policies = %v", cat.Policies)
	}
	// Der Katalog ist eine Kopie.
	cat.Models[mBig][0] = domain.EffortMax
	if caps.Models[mBig][0] != domain.EffortLow {
		t.Error("der Katalog teilt Speicher mit den Capabilities")
	}
}

func TestBuildCatalogDrivesDecisionCheck(t *testing.T) {
	cat := BuildCatalog(testCaps(), newTestSkills(t))
	ctx := domain.DecisionContext{ProjectID: "proj-1", StateRevision: 3, Catalog: cat}
	decision := func(model string, effort domain.Effort, skillIDs ...string) domain.Decision {
		return domain.Decision{
			Action: domain.ActionImplementation, ProjectID: "proj-1", StateRevision: 3,
			Selection: &domain.Selection{Role: domain.RoleImplementer, Model: model, Effort: effort, SkillIDs: skillIDs},
		}
	}
	if err := decision(mMid, domain.EffortHigh, skImpl).Check(ctx); err != nil {
		t.Fatalf("zulässige Auswahl abgewiesen: %v", err)
	}
	wantDomain := func(err error, want domain.Category) {
		t.Helper()
		var ce *domain.ContractError
		if !errors.As(err, &ce) || ce.Category() != want {
			t.Errorf("Fehler = %v, erwartet Klasse %s", err, want)
		}
	}
	wantDomain(decision("model-fremd", domain.EffortHigh).Check(ctx), domain.CatModel)
	wantDomain(decision(mMid, domain.EffortMax).Check(ctx), domain.CatEffort)
	wantDomain(decision(mMid, domain.EffortHigh, skPlan).Check(ctx), domain.CatValue)
}

func TestResolvedProfilesPassTheCatalog(t *testing.T) {
	// Was der Resolver liefert, besteht die Prüfung des Domain-Katalogs aus denselben Quellen.
	caps := testCaps()
	cat := BuildCatalog(caps, newTestSkills(t))
	r := newResolver(t, testConfig())
	for _, role := range domain.Roles() {
		p, err := r.ResolveProfile(role, "", Overrides{}, caps)
		if err != nil {
			t.Fatalf("%s: %v", role, err)
		}
		if err := cat.CheckProfile(p); err != nil {
			t.Errorf("%s: %v", role, err)
		}
	}
}
