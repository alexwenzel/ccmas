package supervisor

import (
	"slices"
	"sort"

	"github.com/alexwenzel/ccmas/internal/domain"
	"github.com/alexwenzel/ccmas/internal/profiles"
	"github.com/alexwenzel/ccmas/internal/skills"
)

// BuildRoleOffers baut die erlaubten Rollenprofile für den Snapshot: je Rolle Standardwerte und die tatsächlich
// wählbaren Modelle, Stufen und Skills. Leere Einschränkungen in agents.yaml (alle konfigurierten) werden ausgeschrieben,
// damit das Modell die Auswahl sieht. agents ordnet einer Rolle den individuellen Agenten zu, der sie ausführt
// (dessen Werte Vorrang vor der Wahl des Supervisors haben).
//
// Die Angabe ist Information für die Planung. Zulässig ist, was die Auflösung des Profils (Policy) zulässt; sie
// ist die Instanz, nicht diese Liste.
func BuildRoleOffers(cfg profiles.Config, caps profiles.Capabilities, cat *skills.Catalog, agents map[domain.Role]string) []RoleOffer {
	var out []RoleOffer
	for _, role := range domain.Roles() {
		rp, ok := cfg.Roles[role]
		if !ok {
			continue
		}
		models := slices.Clone(rp.AllowedModels)
		if len(models) == 0 {
			models = caps.ModelIDs()
		}
		sort.Strings(models)
		efforts := slices.Clone(rp.AllowedEfforts)
		if len(efforts) == 0 {
			for _, e := range domain.Efforts() {
				for _, m := range models {
					if caps.Supports(m, e) {
						efforts = append(efforts, e)
						break
					}
				}
			}
		}
		sort.SliceStable(efforts, func(i, j int) bool { return effortRank(efforts[i]) < effortRank(efforts[j]) })

		offer := RoleOffer{
			Role: role, AgentID: agents[role], Model: rp.Model, Effort: rp.Effort, Policy: rp.PolicyProfile,
			AllowedModels: models, AllowedEfforts: efforts, SkillIDs: []string{}, DefaultSkillIDs: []string{},
		}
		if cat != nil {
			for _, s := range cat.ForRole(role) {
				if s.Status == skills.StatusApproved {
					offer.SkillIDs = append(offer.SkillIDs, s.ID)
				}
			}
			offer.DefaultSkillIDs = append(offer.DefaultSkillIDs, cat.DefaultSkillIDs(role)...)
		}
		out = append(out, offer)
	}
	return out
}

// BuildSkillOffers baut die Skill-Liste des Snapshots: die freigegebenen Skills des Katalogs.
func BuildSkillOffers(cat *skills.Catalog) []SkillOffer {
	out := []SkillOffer{}
	if cat == nil {
		return out
	}
	for _, s := range cat.Skills() {
		if s.Status == skills.StatusApproved {
			out = append(out, SkillOffer{ID: s.ID, Category: string(s.Category), Description: s.Description, Roles: s.Roles})
		}
	}
	return out
}
