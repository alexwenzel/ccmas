package supervisor

import (
	"errors"
	"fmt"

	"github.com/alexwenzel/ccmas/internal/domain"
	"github.com/alexwenzel/ccmas/internal/profiles"
	"github.com/alexwenzel/ccmas/internal/skills"
)

// Policy ist, was der Validator zum Entscheiden braucht: der Domain-Katalog (Modelle, Stufen, genehmigte
// Fallbacks, Skills mit Rollen, Policies), die geprüften Rollenregeln, der Profil-Resolver und die
// Modellfähigkeiten. Sie wird mit NewPolicy aus denselben Quellen gebaut wie alle anderen Pfade; es gibt keine
// zweite Modell- oder Skillliste.
type Policy struct {
	Catalog  domain.Catalog
	Rules    *skills.Rules
	Resolver *profiles.Resolver
	Caps     profiles.Capabilities
}

// NewPolicy baut die Policy. Der Katalog entsteht über profiles.BuildCatalog aus den Capabilities (models.yaml) und
// dem Skill-Katalog. Die Regeln müssen mit skills.LoadChecked geladen sein, damit sie gegen den Katalog geprüft
// sind.
func NewPolicy(res *profiles.Resolver, caps profiles.Capabilities, cat *skills.Catalog, rules *skills.Rules) (Policy, error) {
	switch {
	case res == nil:
		return Policy{}, errors.New("policy: Profil-Resolver fehlt")
	case cat == nil:
		return Policy{}, errors.New("policy: Skill-Katalog fehlt")
	case rules == nil:
		return Policy{}, errors.New("policy: Rollenregeln fehlen (skills.LoadChecked)")
	}
	if err := caps.Validate(); err != nil {
		return Policy{}, fmt.Errorf("policy: Modellfähigkeiten ungültig: %w", err)
	}
	p := Policy{Catalog: profiles.BuildCatalog(caps, cat), Rules: rules, Resolver: res, Caps: caps.Clone()}
	return p, p.Check()
}

// Check prüft, dass die Policy vollständig ist. Der Validator lehnt bei einer unvollständigen Policy alles ab.
func (p Policy) Check() error {
	switch {
	case p.Rules == nil:
		return errors.New("policy: Rollenregeln fehlen")
	case p.Resolver == nil:
		return errors.New("policy: Profil-Resolver fehlt")
	case len(p.Catalog.Models) == 0:
		return errors.New("policy: Katalog kennt keine Modelle")
	case p.Catalog.SkillRoles == nil:
		return errors.New("policy: Katalog ohne Rolleninformation der Skills (profiles.BuildCatalog)")
	}
	return p.Caps.Validate()
}
