package supervisor

import (
	"slices"
	"testing"

	"github.com/alexwenzel/ccmas/internal/domain"
	"github.com/alexwenzel/ccmas/internal/profiles"
)

func TestBuildRoleOffers(t *testing.T) {
	e := newEnv(t)
	cfg := testConfig()
	cfg.Agents["fast"] = profiles.Agent{Role: domain.RoleImplementer, Effort: domain.EffortLow}
	offers := BuildRoleOffers(cfg, e.caps, e.cat, map[domain.Role]string{domain.RoleImplementer: "fast"})
	if len(offers) != len(domain.Roles()) {
		t.Fatalf("%d Angebote für %d Rollen", len(offers), len(domain.Roles()))
	}
	by := map[domain.Role]RoleOffer{}
	for i, o := range offers {
		if o.Role != domain.Roles()[i] {
			t.Errorf("Angebot %d gehört zu %s, erwartet %s", i, o.Role, domain.Roles()[i])
		}
		by[o.Role] = o
	}
	impl := by[domain.RoleImplementer]
	if impl.AgentID != "fast" || impl.Model != mMid || impl.Effort != domain.EffortHigh || impl.Policy != "scoped_write" {
		t.Errorf("Implementierer = %+v", impl)
	}
	if !slices.Equal(impl.AllowedModels, []string{mBig, mMid}) {
		t.Errorf("erlaubte Modelle = %v", impl.AllowedModels)
	}
	if !slices.Equal(impl.SkillIDs, []string{skImpl}) || !slices.Equal(impl.DefaultSkillIDs, []string{skImpl}) {
		t.Errorf("Skills = %v / %v", impl.SkillIDs, impl.DefaultSkillIDs)
	}
	// Ohne Einschränkung gelten alle konfigurierten Modelle; die Stufen sind die von mindestens einem Modell unterstützten.
	sup := by[domain.RoleSupervisor]
	if !slices.Equal(sup.AllowedModels, []string{mBig, mMid}) || sup.AgentID != "" {
		t.Errorf("Supervisor = %+v", sup)
	}
	if !slices.Equal(sup.AllowedEfforts, domain.Efforts()) {
		t.Errorf("Stufen = %v", sup.AllowedEfforts)
	}
	// Reviewer: nur model-big; nur die für die Rolle erlaubten Skills; der noch nicht freigegebene fehlt.
	rev := by[domain.RoleReviewer]
	if !slices.Equal(rev.AllowedModels, []string{mBig}) || !slices.Equal(rev.SkillIDs, []string{skReview}) {
		t.Errorf("Reviewer = %+v", rev)
	}
	if got := by[domain.RoleSupervisor].SkillIDs; len(got) != 0 {
		t.Errorf("Supervisor erhält den nicht freigegebenen Skill: %v", got)
	}
	// Ausdrücklich eingeschränkte Stufen bleiben erhalten.
	cfg2 := testConfig()
	rp := cfg2.Roles[domain.RoleConcept]
	rp.AllowedEfforts = []domain.Effort{domain.EffortHigh, domain.EffortLow}
	cfg2.Roles[domain.RoleConcept] = rp
	got := BuildRoleOffers(cfg2, e.caps, e.cat, nil)
	for _, o := range got {
		if o.Role == domain.RoleConcept && !slices.Equal(o.AllowedEfforts, []domain.Effort{domain.EffortLow, domain.EffortHigh}) {
			t.Errorf("Stufen des Konzept-Agenten = %v", o.AllowedEfforts)
		}
	}
	// Alle Angebote sind im Snapshot gültig.
	s := baseSnapshot()
	s.Roles = offers
	if p := s.Problems(); len(p) != 0 {
		t.Errorf("Snapshot mit Angeboten ungültig: %v", p)
	}
}

func TestBuildSkillOffers(t *testing.T) {
	e := newEnv(t)
	got := BuildSkillOffers(e.cat)
	var ids []string
	for _, o := range got {
		ids = append(ids, o.ID)
		if o.Description == "" || o.Category == "" || len(o.Roles) == 0 {
			t.Errorf("Angebot unvollständig: %+v", o)
		}
	}
	// Nur freigegebene Skills; ccmas-t-plan ist pending_adaptation.
	if !slices.Equal(ids, []string{skDoc, skImpl, skKnow, skReview}) {
		t.Fatalf("Skills = %v", ids)
	}
}
