package profiles

import (
	"fmt"
	"slices"
	"sort"

	"github.com/alexwenzel/ccmas/internal/domain"
	"github.com/alexwenzel/ccmas/internal/skills"
)

// Selection sind optionale Werte je Feld. Ein leerer Wert heißt „nicht gesetzt“; bei SkillIDs
// heißt das nicht „keine Skills“ (leer ist die Standardausstattung der Rolle).
type Selection struct {
	Model    string
	Effort   domain.Effort
	SkillIDs []string
}

// Overrides sind die Werte, die nicht aus der gespeicherten Konfiguration stammen: der
// ausdrückliche Auftragswert (Order) und die Wahl des Supervisors (Supervisor).
type Overrides struct {
	Order      Selection
	Supervisor Selection
}

// Resolver löst Profile auf. Er hält eine eigene tiefe Kopie der Konfiguration und den
// Skill-Katalog und ist nach NewResolver unveränderlich; ein Resolver kann nebenläufig benutzt
// werden. Eine geänderte Konfiguration (set) braucht einen neuen Resolver; das ist gewollt: ein
// laufender Resolver und alle von ihm gelieferten Profile ändern sich nicht (neue Werte gelten
// nur für neue Ausführungen).
//
// ResolveProfile hat einen Zeiger-Empfänger, weil der Resolver den Skill-Katalog (ein
// Zeigertyp mit eigenem Zustand) hält und nicht kopiert werden soll.
type Resolver struct {
	cfg    Config
	skills *skills.Catalog
}

// NewResolver prüft die Konfiguration (Config.Validate) und kopiert sie. Der Skill-Katalog
// ist Pflicht: Skills werden nie ungeprüft übernommen.
func NewResolver(cfg Config, cat *skills.Catalog) (*Resolver, error) {
	if cat == nil {
		return nil, newErr(CodeInvalidRequest, "skill_ids", "ohne Skill-Katalog können keine Profile aufgelöst werden")
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &Resolver{cfg: cfg.Clone(), skills: cat}, nil
}

// Revision liefert die Revision der Konfiguration, mit der der Resolver arbeitet.
func (r *Resolver) Revision() int { return r.cfg.Revision }

// stage ist eine Stufe der Rangfolge mit ihren Werten.
type stage struct {
	src domain.ValueSource
	sel Selection
}

// pick wählt je Feld den Wert der höchsten Stufe, die ihn setzt.
func pick[T comparable](stages []stage, get func(Selection) T) (T, domain.ValueSource) {
	var zero T
	for _, l := range stages {
		if v := get(l.sel); v != zero {
			return v, l.src
		}
	}
	return zero, domain.SourceRole
}

// ResolveProfile löst das Profil für role (und optional einen individuellen Agenten) auf.
//
// Rangfolge je Feld einzeln: ausdrücklicher Auftragswert (overrides.Order) > individueller
// Agentenwert > Wahl des Supervisors (overrides.Supervisor) > Rollenstandard. Das Ergebnis
// liegt innerhalb der Rollenerlaubnis und ist gegen caps und den Skill-Katalog geprüft. Es ist
// ein unveränderlicher Wert; es teilt keinen Speicher mit dem Resolver.
//
// Version ist die Konfigurationsrevision: Revision(agents.yaml) + Revision(models.yaml) - 1, also
// 1 nach der Einrichtung und mit jeder Änderung einer der beiden Dateien größer. profile_id
// ist "<rolle>" beziehungsweise "<rolle>:<agent>".
//
// Fehler tragen einen stabilen Code (siehe Code); Skill-Fehler sind *skills.Error.
func (r *Resolver) ResolveProfile(role domain.Role, agentID string, overrides Overrides, caps Capabilities) (domain.AgentProfile, error) {
	if !role.Valid() {
		return domain.AgentProfile{}, newErr(CodeInvalidRequest, "role", "unbekannte Rolle %q", role)
	}
	if err := caps.Validate(); err != nil {
		return domain.AgentProfile{}, err
	}
	rp, ok := r.cfg.Roles[role]
	if !ok { // NewResolver prüft das; hier nur zur Sicherheit
		return domain.AgentProfile{}, newErr(CodeInvalidConfig, "roles", "Rolle %s hat kein Rollenprofil", role)
	}
	var agent Agent
	if agentID != "" {
		a, ok := r.cfg.Agents[agentID]
		if !ok {
			return domain.AgentProfile{}, newErr(CodeUnknownAgent, "agent", "Agent %q ist nicht angelegt (ccmas agent list zeigt die vorhandenen)", agentID)
		}
		if a.Role != role {
			return domain.AgentProfile{}, newErr(CodeAgentRoleMismatch, "agent", "Agent %q gehört zur Rolle %s, nicht zu %s", agentID, a.Role, role)
		}
		agent = a
	}

	stages := []stage{
		{domain.SourceOrder, overrides.Order},
		{domain.SourceAgent, Selection{Model: agent.Model, Effort: agent.Effort, SkillIDs: agent.SkillIDs}},
		{domain.SourceSupervisor, overrides.Supervisor},
		{domain.SourceRole, Selection{Model: rp.Model, Effort: rp.Effort, SkillIDs: rp.SkillIDs}},
	}
	model, modelSrc := pick(stages, func(s Selection) string { return s.Model })
	requested, effortSrc := pick(stages, func(s Selection) domain.Effort { return s.Effort })
	var skillIDs []string
	skillSrc := domain.SourceRole
	for _, l := range stages {
		if len(l.sel.SkillIDs) > 0 {
			skillIDs, skillSrc = l.sel.SkillIDs, l.src
			break
		}
	}

	if err := checkModelAllowed(model, modelSrc, rp, caps); err != nil {
		return domain.AgentProfile{}, err
	}
	effective, fallback, err := checkEffort(model, requested, effortSrc, rp, caps)
	if err != nil {
		return domain.AgentProfile{}, err
	}
	finalSkills, err := r.checkSkills(role, skillIDs, skillSrc)
	if err != nil {
		return domain.AgentProfile{}, err
	}
	if !slices.Contains(caps.Policies, rp.PolicyProfile) {
		return domain.AgentProfile{}, newErr(CodeInvalidConfig, "policy", "Policy-Profil %q der Rolle %s ist in models.yaml nicht bekannt (bekannt: %v)", rp.PolicyProfile, role, caps.Policies)
	}

	id := string(role)
	if agentID != "" {
		id += ":" + agentID
	}
	p := domain.AgentProfile{
		ProfileID: id, Version: r.cfg.Revision + caps.Revision - 1,
		Role: role, Runtime: rp.Runtime, Model: model, Effort: effective, SkillIDs: finalSkills,
		PolicyProfile:  rp.PolicyProfile,
		RequestedModel: model, RequestedEffort: requested, Fallback: fallback,
		ValueSources: &domain.ValueSources{Model: modelSrc, Effort: effortSrc, SkillIDs: skillSrc},
	}
	// Absicherung: das Ergebnis muss den Prüfungen des Domain-Katalogs aus denselben Quellen genügen.
	if err := BuildCatalog(caps, r.skills).CheckProfile(p); err != nil {
		return domain.AgentProfile{}, &Error{Code: CodeInvalidConfig, Message: "aufgelöstes Profil besteht die Domain-Prüfung nicht: " + err.Error(), cause: err}
	}
	return p, nil
}

func describe(src domain.ValueSource) string {
	switch src {
	case domain.SourceOrder:
		return "Auftragswert"
	case domain.SourceAgent:
		return "Agentenwert"
	case domain.SourceSupervisor:
		return "Wahl des Supervisors"
	}
	return "Rollenstandard"
}

func checkModelAllowed(model string, src domain.ValueSource, rp RoleProfile, caps Capabilities) error {
	if _, ok := caps.Models[model]; !ok {
		return newErr(CodeUnknownModel, "model", "%s: Modell %q ist in models.yaml nicht konfiguriert (konfiguriert: %v)", describe(src), model, caps.ModelIDs())
	}
	if len(rp.AllowedModels) > 0 && !slices.Contains(rp.AllowedModels, model) {
		return newErr(CodeNotAllowedForRole, "model", "%s: Modell %q ist für diese Rolle nicht erlaubt (erlaubt: %v)", describe(src), model, rp.AllowedModels)
	}
	return nil
}

// checkEffort prüft die angeforderte Stufe gegen Erlaubnis und Modell und liefert die wirksame
// Stufe. Nur ein in models.yaml genehmigter Fallback für genau dieses Modell und diese Stufe
// reduziert; er wird als EffortFallback ausgewiesen.
func checkEffort(model string, requested domain.Effort, src domain.ValueSource, rp RoleProfile, caps Capabilities) (domain.Effort, *domain.EffortFallback, error) {
	if !requested.Valid() {
		return "", nil, newErr(CodeUnsupportedEffort, "effort", "%s: unbekannte Effort-Stufe %q (erlaubt: %s)", describe(src), requested, joinEfforts(domain.Efforts()))
	}
	allowed := func(e domain.Effort) bool {
		return len(rp.AllowedEfforts) == 0 || slices.Contains(rp.AllowedEfforts, e)
	}
	if !allowed(requested) {
		return "", nil, newErr(CodeNotAllowedForRole, "effort", "%s: Effort %q ist für diese Rolle nicht erlaubt (erlaubt: %s)", describe(src), requested, joinEfforts(rp.AllowedEfforts))
	}
	if caps.Supports(model, requested) {
		return requested, nil, nil
	}
	fb, ok := caps.ApprovedFallback(model, requested)
	if !ok {
		return "", nil, newErr(CodeUnsupportedEffort, "effort", "%s: Modell %s unterstützt Effort %q nicht (unterstützt: %s) und für diese Kombination ist kein Fallback genehmigt", describe(src), model, requested, joinEfforts(caps.Models[model]))
	}
	if !allowed(fb.Effective) {
		return "", nil, newErr(CodeNotAllowedForRole, "effort", "%s: Effort %q würde auf %q reduziert (%s), das für diese Rolle nicht erlaubt ist (erlaubt: %s)", describe(src), requested, fb.Effective, fb.Source, joinEfforts(rp.AllowedEfforts))
	}
	return fb.Effective, &domain.EffortFallback{Kind: domain.FallbackEffortReduction, From: requested, To: fb.Effective, Source: fb.Source}, nil
}

// checkSkills prüft die gewählten Skills gegen den Katalog (freigegeben und für die Rolle
// erlaubt). Ohne Wahl gilt die Standardausstattung der Rolle. Das Ergebnis ist sortiert und
// eine eigene, nie nil-Liste.
func (r *Resolver) checkSkills(role domain.Role, chosen []string, src domain.ValueSource) ([]string, error) {
	ids := chosen
	if len(ids) == 0 {
		ids = r.skills.DefaultSkillIDs(role)
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			return nil, newErr(CodeInvalidRequest, "skill_ids", "Skill %s ist mehrfach angefordert", id)
		}
		seen[id] = true
		sk, ok := r.skills.Skill(id)
		switch {
		case !ok:
			return nil, &skills.Error{Code: skills.CodeUnknownSkill, Skill: id, Message: fmt.Sprintf("%s: Skill %s ist im Manifest nicht bekannt", describe(src), id)}
		case sk.Status != skills.StatusApproved:
			return nil, &skills.Error{Code: skills.CodeSkillNotApproved, Skill: id, Message: fmt.Sprintf("%s: Skill %s ist nicht freigegeben (Status %s: %s)", describe(src), id, sk.Status, sk.Adaptation)}
		case !sk.AllowsRole(role):
			return nil, &skills.Error{Code: skills.CodeSkillNotAllowedForRole, Skill: id, Message: fmt.Sprintf("%s: Skill %s ist für die Rolle %s nicht erlaubt", describe(src), id, role)}
		}
	}
	out := nonNilStrings(ids)
	sort.Strings(out)
	return out, nil
}

// BuildCatalog baut aus den Capabilities und dem Skill-Katalog den domain.Catalog für
// Decision.Check und AgentProfile-Prüfungen. Es entsteht keine zweite Modellliste: Modelle,
// Stufen und Policies sind Kopien der Capabilities; als bekannte Skills gelten nur die
// freigegebenen (approved) des Katalogs.
//
// Hinweis: Der Domain-Katalog kennt keine Fallbacks. Decision.Check weist deshalb auch dort
// einen nicht unterstützten Effort ab, wo models.yaml eine Reduktion genehmigt; ob die Wahl
// des Supervisors Fallbacks nutzen darf, ist Teil von CC-14.
func BuildCatalog(caps Capabilities, cat *skills.Catalog) domain.Catalog {
	out := domain.Catalog{
		Models:   make(map[string][]domain.Effort, len(caps.Models)),
		Skills:   map[string]bool{},
		Policies: map[string]bool{},
	}
	for id, efforts := range caps.Models {
		out.Models[id] = slices.Clone(efforts)
	}
	for _, p := range caps.Policies {
		out.Policies[p] = true
	}
	if cat != nil {
		for _, s := range cat.Skills() {
			if s.Status == skills.StatusApproved {
				out.Skills[s.ID] = true
			}
		}
	}
	return out
}
