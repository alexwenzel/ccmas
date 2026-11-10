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

func TestBuildCatalogCarriesApprovedFallbacksAndSkillRoles(t *testing.T) {
	caps := testCaps()
	cat := BuildCatalog(caps, newTestSkills(t))

	// Die genehmigten Reduktionen stammen aus models.yaml (Capabilities), es gibt keine zweite Liste.
	want := domain.CatalogFallback{Model: mMid, Requested: domain.EffortXHigh, Effective: domain.EffortHigh, Source: "Testdoku: xhigh läuft als high"}
	if len(cat.Fallbacks) != 1 || cat.Fallbacks[0] != want {
		t.Fatalf("Fallbacks = %+v, erwartet %+v", cat.Fallbacks, want)
	}
	// Je freigegebenem Skill stehen die erlaubten Rollen im Katalog; ein nicht freigegebener Skill fehlt.
	if got := cat.SkillRoles[skReview]; !slices.Equal(got, []domain.Role{domain.RoleReviewer, domain.RoleConceptReviewer}) {
		t.Errorf("Rollen von %s = %v", skReview, got)
	}
	if got := cat.SkillRoles[skImpl]; !slices.Equal(got, []domain.Role{domain.RoleImplementer}) {
		t.Errorf("Rollen von %s = %v", skImpl, got)
	}
	if _, ok := cat.SkillRoles[skPlan]; ok {
		t.Error("nicht freigegebener Skill hat Rollen im Katalog")
	}
	// Mit Rolleninformation, auch wenn kein Skill freigegeben ist: nie nil (sonst entfiele die Rollenprüfung).
	if empty := BuildCatalog(caps, nil); empty.SkillRoles == nil {
		t.Error("SkillRoles darf ohne Katalog nicht nil sein")
	}
	// Kopie: Änderungen am Katalog verändern die Capabilities nicht.
	cat.Fallbacks[0].Effective = domain.EffortLow
	if caps.Fallbacks[0].Effective != domain.EffortHigh {
		t.Error("der Katalog teilt Speicher mit den Capabilities")
	}
}

func TestDecisionCheckUsesApprovedFallbackFromModelsYAML(t *testing.T) {
	cat := BuildCatalog(testCaps(), newTestSkills(t))
	ctx := domain.DecisionContext{ProjectID: "proj-1", StateRevision: 3, Catalog: cat}
	decision := func(model string, effort domain.Effort) domain.Decision {
		return domain.Decision{
			Action: domain.ActionImplementation, ProjectID: "proj-1", StateRevision: 3,
			Selection: &domain.Selection{Role: domain.RoleImplementer, Model: model, Effort: effort, SkillIDs: []string{skImpl}},
		}
	}
	// mMid unterstützt xhigh nicht, models.yaml genehmigt die Reduktion auf high: zulässig und sichtbar.
	got, err := decision(mMid, domain.EffortXHigh).CheckEffective(ctx)
	if err != nil {
		t.Fatalf("genehmigter Fallback abgewiesen: %v", err)
	}
	if got.Effort != domain.EffortHigh || got.RequestedEffort != domain.EffortXHigh || got.Fallback == nil || got.Fallback.To != domain.EffortHigh {
		t.Fatalf("wirksame Auswahl = %+v", got)
	}
	// Für max ist nichts genehmigt; mSmall hat gar keinen Fallback.
	var ce *domain.ContractError
	if err := decision(mMid, domain.EffortMax).Check(ctx); !errors.As(err, &ce) || ce.Category() != domain.CatEffort {
		t.Errorf("max auf mMid: %v", err)
	}
	if err := decision(mSmall, domain.EffortXHigh).Check(ctx); !errors.As(err, &ce) || ce.Category() != domain.CatEffort {
		t.Errorf("xhigh auf mSmall: %v", err)
	}
}

func TestDecisionCheckRejectsSkillOfAnotherRole(t *testing.T) {
	cat := BuildCatalog(testCaps(), newTestSkills(t))
	ctx := domain.DecisionContext{ProjectID: "proj-1", StateRevision: 3, Catalog: cat}
	d := domain.Decision{
		Action: domain.ActionImplementation, ProjectID: "proj-1", StateRevision: 3,
		Selection: &domain.Selection{Role: domain.RoleImplementer, Model: mMid, Effort: domain.EffortHigh, SkillIDs: []string{skReview}},
	}
	var ce *domain.ContractError
	if err := d.Check(ctx); !errors.As(err, &ce) || ce.Category() != domain.CatRole {
		t.Fatalf("Review-Skill für den Implementierer: %v", err)
	}
	d.Action, d.Selection = domain.ActionDocumentationWork, &domain.Selection{Role: domain.RoleDocumentation, Model: mMid, Effort: domain.EffortHigh, SkillIDs: []string{skDoc, skExtra}}
	if err := d.Check(ctx); err != nil {
		t.Fatalf("documentation_work mit Dokumentationsskills: %v", err)
	}
	d.Selection.SkillIDs = []string{skImpl}
	if err := d.Check(ctx); !errors.As(err, &ce) || ce.Category() != domain.CatRole {
		t.Fatalf("Implementierungsskill für die Dokumentation: %v", err)
	}
}
