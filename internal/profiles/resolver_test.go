package profiles

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/alexwenzel/ccmas/internal/domain"
	"github.com/alexwenzel/ccmas/internal/skills"
)

// precedenceConfig: Rolle documentation mit eigenem Standard je Feld, ein Agent mit
// individuellen Werten. Die Werte jeder Stufe sind je Feld verschieden, damit die Herkunft
// eindeutig am Ergebnis ablesbar ist.
func precedenceConfig() Config {
	cfg := testConfig()
	cfg.Roles[domain.RoleDocumentation] = RoleProfile{
		Runtime: domain.RuntimeClaude, Model: "model-role", Effort: domain.EffortLow,
		SkillIDs: []string{skDoc}, PolicyProfile: "readonly",
	}
	return cfg
}

func precedenceCaps() Capabilities {
	caps := testCaps()
	for _, m := range []string{"model-order", "model-agent", "model-supervisor", "model-role"} {
		caps.Models[m] = slices.Clone(caps.Models[mBig])
	}
	return caps
}

// level beschreibt die Werte einer Stufe der Rangfolge.
type level struct {
	src    domain.ValueSource
	model  string
	effort domain.Effort
	skills []string
}

var (
	lvOrder      = level{domain.SourceOrder, "model-order", domain.EffortMax, []string{skKnow}}
	lvAgent      = level{domain.SourceAgent, "model-agent", domain.EffortXHigh, []string{skExtra}}
	lvSupervisor = level{domain.SourceSupervisor, "model-supervisor", domain.EffortMedium, []string{skKnow, skExtra}}
	lvRole       = level{domain.SourceRole, "model-role", domain.EffortLow, []string{skDoc}}
)

// TestOverridePrecedence prüft die Rangfolge ausdrücklicher Auftragswert > individueller
// Agentenwert > Wahl des Supervisors > Rollenstandard für jedes Feld einzeln über alle
// Kombinationen der drei überschreibenden Stufen, einschließlich value_sources.
func TestOverridePrecedence(t *testing.T) {
	for _, field := range []string{"model", "effort", "skill_ids"} {
		for mask := 0; mask < 8; mask++ {
			set := [3]bool{mask&1 != 0, mask&2 != 0, mask&4 != 0} // order, agent, supervisor
			name := fmt.Sprintf("%s/order=%v,agent=%v,supervisor=%v", field, set[0], set[1], set[2])
			t.Run(name, func(t *testing.T) {
				cfg := precedenceConfig()
				var ov Overrides
				want := lvRole
				apply := func(on bool, lv level, sel *Selection, agent bool) {
					if !on {
						return
					}
					want = lv
					var s Selection
					switch field {
					case "model":
						s.Model = lv.model
					case "effort":
						s.Effort = lv.effort
					case "skill_ids":
						s.SkillIDs = lv.skills
					}
					if agent {
						a := cfg.Agents["doc-1"]
						a.Role, a.Model, a.Effort, a.SkillIDs = domain.RoleDocumentation, s.Model, s.Effort, s.SkillIDs
						cfg.Agents["doc-1"] = a
						return
					}
					*sel = s
				}
				// Von niedrig nach hoch anwenden: die zuletzt gesetzte Stufe ist die höchste.
				apply(set[2], lvSupervisor, &ov.Supervisor, false)
				apply(set[1], lvAgent, nil, true)
				apply(set[0], lvOrder, &ov.Order, false)
				if !set[1] {
					cfg.Agents["doc-1"] = Agent{Role: domain.RoleDocumentation} // Agent ohne eigene Werte
				}

				p, err := newResolver(t, cfg).ResolveProfile(domain.RoleDocumentation, "doc-1", ov, precedenceCaps())
				if err != nil {
					t.Fatal(err)
				}
				if p.ValueSources == nil {
					t.Fatal("value_sources fehlt")
				}
				// Das untersuchte Feld folgt der Rangfolge.
				switch field {
				case "model":
					if p.Model != want.model || p.RequestedModel != want.model || p.ValueSources.Model != want.src {
						t.Errorf("Modell = %q (angefordert %q, Herkunft %q), erwartet %q (%q)", p.Model, p.RequestedModel, p.ValueSources.Model, want.model, want.src)
					}
				case "effort":
					if p.Effort != want.effort || p.RequestedEffort != want.effort || p.ValueSources.Effort != want.src {
						t.Errorf("Effort = %q (angefordert %q, Herkunft %q), erwartet %q (%q)", p.Effort, p.RequestedEffort, p.ValueSources.Effort, want.effort, want.src)
					}
				case "skill_ids":
					if !slices.Equal(p.SkillIDs, sortedCopy(want.skills)) || p.ValueSources.SkillIDs != want.src {
						t.Errorf("Skills = %v (Herkunft %q), erwartet %v (%q)", p.SkillIDs, p.ValueSources.SkillIDs, sortedCopy(want.skills), want.src)
					}
				}
				// Die übrigen Felder bleiben beim Rollenstandard: Felder sind unabhängig.
				if field != "model" && (p.Model != lvRole.model || p.ValueSources.Model != domain.SourceRole) {
					t.Errorf("Modell = %q (%q), erwartet Rollenstandard", p.Model, p.ValueSources.Model)
				}
				if field != "effort" && (p.Effort != lvRole.effort || p.ValueSources.Effort != domain.SourceRole) {
					t.Errorf("Effort = %q (%q), erwartet Rollenstandard", p.Effort, p.ValueSources.Effort)
				}
				if field != "skill_ids" && (!slices.Equal(p.SkillIDs, []string{skDoc}) || p.ValueSources.SkillIDs != domain.SourceRole) {
					t.Errorf("Skills = %v (%q), erwartet Rollenstandard", p.SkillIDs, p.ValueSources.SkillIDs)
				}
			})
		}
	}
}

func sortedCopy(in []string) []string {
	out := slices.Clone(in)
	slices.Sort(out)
	return out
}

func TestOverridePrecedenceFieldsComeFromDifferentLevels(t *testing.T) {
	cfg := precedenceConfig()
	cfg.Agents["doc-1"] = Agent{Role: domain.RoleDocumentation, Effort: domain.EffortXHigh}
	ov := Overrides{
		Order:      Selection{Model: "model-order"},
		Supervisor: Selection{Model: "model-supervisor", Effort: domain.EffortMedium, SkillIDs: []string{skExtra}},
	}
	p, err := newResolver(t, cfg).ResolveProfile(domain.RoleDocumentation, "doc-1", ov, precedenceCaps())
	if err != nil {
		t.Fatal(err)
	}
	want := domain.ValueSources{Model: domain.SourceOrder, Effort: domain.SourceAgent, SkillIDs: domain.SourceSupervisor}
	if p.ValueSources == nil || *p.ValueSources != want {
		t.Errorf("value_sources = %+v, erwartet %+v", p.ValueSources, want)
	}
	if p.Model != "model-order" || p.Effort != domain.EffortXHigh || !slices.Equal(p.SkillIDs, []string{skExtra}) {
		t.Errorf("Profil = %+v", p)
	}
}

func TestResolveWithoutAgentUsesRoleOnly(t *testing.T) {
	cfg := precedenceConfig()
	cfg.Agents["doc-1"] = Agent{Role: domain.RoleDocumentation, Model: "model-agent", Effort: domain.EffortXHigh}
	p, err := newResolver(t, cfg).ResolveProfile(domain.RoleDocumentation, "", Overrides{}, precedenceCaps())
	if err != nil {
		t.Fatal(err)
	}
	if p.Model != "model-role" || p.Effort != domain.EffortLow {
		t.Errorf("Agentenwerte wirken ohne Agent: %+v", p)
	}
	if p.ProfileID != "documentation" {
		t.Errorf("profile_id = %q, erwartet documentation", p.ProfileID)
	}
	if p.Role != domain.RoleDocumentation || p.Runtime != domain.RuntimeClaude || p.PolicyProfile != "readonly" {
		t.Errorf("Profil = %+v", p)
	}
}

func TestResolveProfileIDAndVersion(t *testing.T) {
	cfg := testConfig()
	cfg.Revision = 4
	cfg.Agents["impl-1"] = Agent{Role: domain.RoleImplementer}
	caps := testCaps()
	caps.Revision = 3
	r := newResolver(t, cfg)
	p, err := r.ResolveProfile(domain.RoleImplementer, "impl-1", Overrides{}, caps)
	if err != nil {
		t.Fatal(err)
	}
	if p.ProfileID != "implementer:impl-1" {
		t.Errorf("profile_id = %q", p.ProfileID)
	}
	// Version = Revision(agents) + Revision(models) - 1: wächst mit jeder Änderung einer Datei.
	if p.Version != 6 {
		t.Errorf("version = %d, erwartet 6", p.Version)
	}
	caps.Revision = 4
	p2, _ := r.ResolveProfile(domain.RoleImplementer, "impl-1", Overrides{}, caps)
	if p2.Version != 7 {
		t.Errorf("version nach Änderung von models.yaml = %d, erwartet 7", p2.Version)
	}
	if err := (domain.Catalog{}).CheckProfile(p); err == nil {
		t.Error("leerer Katalog akzeptiert das Profil")
	}
}

func TestUnsupportedEffort(t *testing.T) {
	r := newResolver(t, testConfig())
	resolve := func(model string, effort domain.Effort, caps Capabilities) (domain.AgentProfile, error) {
		return r.ResolveProfile(domain.RoleReviewer, "", Overrides{Order: Selection{Model: model, Effort: effort}}, caps)
	}

	t.Run("ohne genehmigten Fallback abgewiesen", func(t *testing.T) {
		caps := testCaps()
		caps.Fallbacks = nil
		_, err := resolve(mMid, domain.EffortXHigh, caps)
		wantCode(t, err, CodeUnsupportedEffort)
		for _, part := range []string{mMid, "xhigh"} {
			if !containsStr(err.Error(), part) {
				t.Errorf("Meldung %q nennt %q nicht", err, part)
			}
		}
	})

	t.Run("mit genehmigtem Fallback sichtbar reduziert", func(t *testing.T) {
		p, err := resolve(mMid, domain.EffortXHigh, testCaps())
		if err != nil {
			t.Fatal(err)
		}
		if p.Effort != domain.EffortHigh || p.RequestedEffort != domain.EffortXHigh {
			t.Errorf("effort = %q, requested_effort = %q", p.Effort, p.RequestedEffort)
		}
		want := &domain.EffortFallback{Kind: "effort_reduction", From: domain.EffortXHigh, To: domain.EffortHigh, Source: "Testdoku: xhigh läuft als high"}
		if !reflect.DeepEqual(p.Fallback, want) {
			t.Errorf("fallback = %+v, erwartet %+v", p.Fallback, want)
		}
		if p.Model != mMid || p.RequestedModel != mMid {
			t.Errorf("Modell = %q, angefordert %q", p.Model, p.RequestedModel)
		}
		if p.ValueSources.Effort != domain.SourceOrder {
			t.Errorf("Herkunft des Efforts = %q, erwartet order (die Anforderung)", p.ValueSources.Effort)
		}
		// Das Ergebnis ist mit dem Domain-Katalog konsistent (Reduktion sichtbar, nicht verdeckt).
		if err := BuildCatalog(testCaps(), newTestSkills(t)).CheckProfile(p); err != nil {
			t.Errorf("Domain-Prüfung: %v", err)
		}
	})

	t.Run("ohne Reduktion kein Fallback-Eintrag", func(t *testing.T) {
		p, err := resolve(mMid, domain.EffortHigh, testCaps())
		if err != nil {
			t.Fatal(err)
		}
		if p.Fallback != nil || p.RequestedEffort != domain.EffortHigh || p.Effort != domain.EffortHigh {
			t.Errorf("Profil = %+v", p)
		}
	})

	t.Run("Fallback einer anderen Stufe wird nicht angewendet", func(t *testing.T) {
		// genehmigt ist nur xhigh -> high; max wird nicht stillschweigend ebenfalls reduziert
		_, err := resolve(mMid, domain.EffortMax, testCaps())
		wantCode(t, err, CodeUnsupportedEffort)
	})

	t.Run("Fallback eines anderen Modells wird nicht angewendet", func(t *testing.T) {
		_, err := resolve(mSmall, domain.EffortXHigh, testCaps())
		wantCode(t, err, CodeUnsupportedEffort)
	})

	t.Run("unbekanntes Modell", func(t *testing.T) {
		_, err := resolve("model-fremd", domain.EffortHigh, testCaps())
		wantCode(t, err, CodeUnknownModel)
		if !containsStr(err.Error(), "model-fremd") {
			t.Errorf("Meldung nennt das Modell nicht: %v", err)
		}
	})

	t.Run("unbekannte Stufe", func(t *testing.T) {
		_, err := resolve(mBig, domain.Effort("ultra"), testCaps())
		wantCode(t, err, CodeUnsupportedEffort)
	})

	t.Run("Fallback gilt auch für Rollenstandard und Agentenwert", func(t *testing.T) {
		cfg := testConfig()
		rp := cfg.Roles[domain.RoleReviewer]
		rp.Model, rp.Effort = mMid, domain.EffortXHigh
		cfg.Roles[domain.RoleReviewer] = rp
		p, err := newResolver(t, cfg).ResolveProfile(domain.RoleReviewer, "", Overrides{}, testCaps())
		if err != nil {
			t.Fatal(err)
		}
		if p.Fallback == nil || p.Effort != domain.EffortHigh || p.RequestedEffort != domain.EffortXHigh || p.ValueSources.Effort != domain.SourceRole {
			t.Errorf("Profil = %+v", p)
		}
	})
}

func TestUnsupportedEffortLeavesNoPartialResult(t *testing.T) {
	r := newResolver(t, testConfig())
	p, err := r.ResolveProfile(domain.RoleReviewer, "", Overrides{Order: Selection{Model: mSmall, Effort: domain.EffortMax}}, testCaps())
	wantCode(t, err, CodeUnsupportedEffort)
	if !reflect.DeepEqual(p, domain.AgentProfile{}) {
		t.Errorf("bei Fehler muss das Profil leer sein, war %+v", p)
	}
}

// TestProfileSnapshot: Ein aufgelöstes Profil ist ein unveränderlicher Wert. Eine spätere
// Änderung der Konfiguration (set) wirkt nur auf neue Auflösungen.
func TestProfileSnapshot(t *testing.T) {
	cfg := testConfig()
	rp := cfg.Roles[domain.RoleDocumentation]
	rp.SkillIDs = []string{skDoc, skExtra}
	cfg.Roles[domain.RoleDocumentation] = rp
	cat := newTestSkills(t)

	r1, err := NewResolver(cfg, cat)
	if err != nil {
		t.Fatal(err)
	}
	old, err := r1.ResolveProfile(domain.RoleDocumentation, "", Overrides{}, testCaps())
	if err != nil {
		t.Fatal(err)
	}
	frozen := cloneProfile(old)

	// 1. Das Verändern der übergebenen Konfiguration (auch ihrer Slices) wirkt nicht zurück.
	cfg.Roles[domain.RoleDocumentation].SkillIDs[0] = "ccmas-geaendert"
	cfg.Roles[domain.RoleDocumentation] = RoleProfile{Model: "x"}
	// 2. Das Verändern des ausgegebenen Profils wirkt nicht auf spätere Auflösungen.
	old.SkillIDs[0] = "ccmas-manipuliert"
	again, err := r1.ResolveProfile(domain.RoleDocumentation, "", Overrides{}, testCaps())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(again.SkillIDs, frozen.SkillIDs) {
		t.Errorf("Skill-Slice teilt Speicher mit dem früheren Profil: %v", again.SkillIDs)
	}
	old.SkillIDs[0] = frozen.SkillIDs[0]
	if !reflect.DeepEqual(old, frozen) {
		t.Errorf("altes Profil verändert: %+v, erwartet %+v", old, frozen)
	}

	// 3. Eine neue Konfigurationsrevision (set) ergibt bei neuer Auflösung neue Werte und
	// eine höhere Version; das alte Profil bleibt unverändert.
	next := testConfig()
	next.Revision = 2
	nrp := next.Roles[domain.RoleDocumentation]
	nrp.Effort, nrp.SkillIDs = domain.EffortMedium, []string{skExtra}
	next.Roles[domain.RoleDocumentation] = nrp
	r2, err := NewResolver(next, cat)
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := r2.ResolveProfile(domain.RoleDocumentation, "", Overrides{}, testCaps())
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Version <= frozen.Version || fresh.Effort != domain.EffortMedium || !slices.Equal(fresh.SkillIDs, []string{skExtra}) {
		t.Errorf("neue Auflösung = %+v (alte Version %d)", fresh, frozen.Version)
	}
	if !reflect.DeepEqual(old, frozen) {
		t.Errorf("altes Profil nach neuer Konfiguration verändert: %+v", old)
	}
}

func cloneProfile(p domain.AgentProfile) domain.AgentProfile {
	p.SkillIDs = slices.Clone(p.SkillIDs)
	if p.Fallback != nil {
		f := *p.Fallback
		p.Fallback = &f
	}
	if p.ValueSources != nil {
		v := *p.ValueSources
		p.ValueSources = &v
	}
	return p
}

func TestRoleAllowance(t *testing.T) {
	cfg := testConfig()
	rp := cfg.Roles[domain.RoleReviewer]
	rp.AllowedModels = []string{mBig, mMid}
	rp.AllowedEfforts = []domain.Effort{domain.EffortHigh, domain.EffortXHigh}
	cfg.Roles[domain.RoleReviewer] = rp
	cfg.Agents["rev-1"] = Agent{Role: domain.RoleReviewer, Model: mSmall}
	r := newResolver(t, cfg)

	cases := map[string]struct {
		agent string
		ov    Overrides
		code  Code
		field string
	}{
		"Rollenstandard erlaubt":            {"", Overrides{}, "", ""},
		"Auftragsmodell erlaubt":            {"", Overrides{Order: Selection{Model: mMid}}, "", ""},
		"Auftragsmodell nicht erlaubt":      {"", Overrides{Order: Selection{Model: mSmall}}, CodeNotAllowedForRole, "model"},
		"Supervisor-Modell nicht erlaubt":   {"", Overrides{Supervisor: Selection{Model: mSmall}}, CodeNotAllowedForRole, "model"},
		"Agentenmodell nicht erlaubt":       {"rev-1", Overrides{}, CodeNotAllowedForRole, "model"},
		"Auftrag schlägt Agent, erlaubt":    {"rev-1", Overrides{Order: Selection{Model: mBig}}, "", ""},
		"Effort nicht erlaubt":              {"", Overrides{Order: Selection{Effort: domain.EffortLow}}, CodeNotAllowedForRole, "effort"},
		"Effort erlaubt":                    {"", Overrides{Order: Selection{Effort: domain.EffortXHigh}}, "", ""},
		"unbekanntes Modell vor Erlaubnis":  {"", Overrides{Order: Selection{Model: "model-fremd"}}, CodeUnknownModel, ""},
		"Reduktion innerhalb der Erlaubnis": {"", Overrides{Order: Selection{Model: mMid, Effort: domain.EffortXHigh}}, "", ""},
		"unterstützt aber nicht erlaubt":    {"", Overrides{Order: Selection{Model: mBig, Effort: domain.EffortMax}}, CodeNotAllowedForRole, "effort"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := r.ResolveProfile(domain.RoleReviewer, tc.agent, tc.ov, testCaps())
			if tc.code == "" {
				if err != nil {
					t.Fatalf("unerwarteter Fehler: %v", err)
				}
				return
			}
			wantCode(t, err, tc.code)
			if tc.field != "" && !errors.Is(err, &Error{Code: tc.code, Field: tc.field}) {
				t.Errorf("Feld = %v, erwartet %q", err, tc.field)
			}
		})
	}

	t.Run("wirksamer Effort außerhalb der Erlaubnis", func(t *testing.T) {
		cfg := testConfig()
		rp := cfg.Roles[domain.RoleReviewer]
		rp.AllowedEfforts = []domain.Effort{domain.EffortXHigh, domain.EffortMax}
		cfg.Roles[domain.RoleReviewer] = rp
		_, err := newResolver(t, cfg).ResolveProfile(domain.RoleReviewer, "", Overrides{Order: Selection{Model: mMid, Effort: domain.EffortXHigh}}, testCaps())
		wantCode(t, err, CodeNotAllowedForRole) // xhigh ist erlaubt, die Reduktion auf high nicht
	})

	t.Run("ohne Angabe sind alle konfigurierten erlaubt", func(t *testing.T) {
		p, err := newResolver(t, testConfig()).ResolveProfile(domain.RoleReviewer, "", Overrides{Order: Selection{Model: mSmall, Effort: domain.EffortLow}}, testCaps())
		if err != nil || p.Model != mSmall {
			t.Fatalf("Profil = %+v, Fehler = %v", p, err)
		}
	})
}

func TestAgentErrors(t *testing.T) {
	cfg := testConfig()
	cfg.Agents["impl-1"] = Agent{Role: domain.RoleImplementer}
	r := newResolver(t, cfg)
	_, err := r.ResolveProfile(domain.RoleImplementer, "gibts-nicht", Overrides{}, testCaps())
	wantCode(t, err, CodeUnknownAgent)
	_, err = r.ResolveProfile(domain.RoleReviewer, "impl-1", Overrides{}, testCaps())
	wantCode(t, err, CodeAgentRoleMismatch)
	_, err = r.ResolveProfile(domain.Role("wizard"), "", Overrides{}, testCaps())
	wantCode(t, err, CodeInvalidRequest)
}

func TestResolveSkillsThroughCatalog(t *testing.T) {
	r := newResolver(t, testConfig())
	resolveSkills := func(role domain.Role, ids ...string) (domain.AgentProfile, error) {
		return r.ResolveProfile(role, "", Overrides{Order: Selection{SkillIDs: ids}}, testCaps())
	}

	t.Run("leer ist die Standardausstattung der Rolle", func(t *testing.T) {
		p, err := r.ResolveProfile(domain.RoleReviewer, "", Overrides{}, testCaps())
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(p.SkillIDs, []string{skReview}) || p.ValueSources.SkillIDs != domain.SourceRole {
			t.Errorf("Skills = %v (%q)", p.SkillIDs, p.ValueSources.SkillIDs)
		}
	})
	t.Run("Rolle ohne Standardausstattung ergibt eine leere Liste, nicht null", func(t *testing.T) {
		p, err := r.ResolveProfile(domain.RoleCompletion, "", Overrides{}, testCaps())
		if err != nil {
			t.Fatal(err)
		}
		if p.SkillIDs == nil || len(p.SkillIDs) != 0 {
			t.Errorf("Skills = %#v, erwartet leere, nicht nil-Liste", p.SkillIDs)
		}
	})
	t.Run("ausdrücklich gewählt und sortiert", func(t *testing.T) {
		p, err := resolveSkills(domain.RoleDocumentation, skExtra, skDoc)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(p.SkillIDs, []string{skDoc, skExtra}) {
			t.Errorf("Skills = %v", p.SkillIDs)
		}
	})
	t.Run("noch nicht freigegeben", func(t *testing.T) {
		_, err := resolveSkills(domain.RoleSupervisor, skPlan)
		if !errors.Is(err, &skills.Error{Code: skills.CodeSkillNotApproved}) {
			t.Errorf("Fehler = %v, erwartet skill_not_approved", err)
		}
	})
	t.Run("für die Rolle nicht erlaubt", func(t *testing.T) {
		_, err := resolveSkills(domain.RoleImplementer, skDoc)
		if !errors.Is(err, &skills.Error{Code: skills.CodeSkillNotAllowedForRole, Skill: skDoc}) {
			t.Errorf("Fehler = %v, erwartet skill_not_allowed_for_role", err)
		}
	})
	t.Run("unbekannt", func(t *testing.T) {
		_, err := resolveSkills(domain.RoleDocumentation, "ccmas-gibts-nicht")
		if !errors.Is(err, &skills.Error{Code: skills.CodeUnknownSkill}) {
			t.Errorf("Fehler = %v, erwartet unknown_skill", err)
		}
	})
	t.Run("doppelt", func(t *testing.T) {
		_, err := resolveSkills(domain.RoleDocumentation, skDoc, skDoc)
		wantCode(t, err, CodeInvalidRequest)
	})
	t.Run("ungültiger Rollenstandard wird beim Auflösen erkannt", func(t *testing.T) {
		cfg := testConfig()
		rp := cfg.Roles[domain.RoleImplementer]
		rp.SkillIDs = []string{skDoc} // erlaubt nur für documentation
		cfg.Roles[domain.RoleImplementer] = rp
		_, err := newResolver(t, cfg).ResolveProfile(domain.RoleImplementer, "", Overrides{}, testCaps())
		if !errors.Is(err, &skills.Error{Code: skills.CodeSkillNotAllowedForRole}) {
			t.Errorf("Fehler = %v", err)
		}
	})
}

func TestResolveRejectsBrokenPolicyOrCapabilities(t *testing.T) {
	cfg := testConfig()
	rp := cfg.Roles[domain.RoleReviewer]
	rp.PolicyProfile = "wild"
	cfg.Roles[domain.RoleReviewer] = rp
	_, err := newResolver(t, cfg).ResolveProfile(domain.RoleReviewer, "", Overrides{}, testCaps())
	wantCode(t, err, CodeInvalidConfig)

	broken := testCaps()
	broken.Fallbacks = []ApprovedFallback{{Model: mMid, Requested: domain.EffortXHigh, Effective: domain.EffortLow, Source: "falsch"}}
	_, err = newResolver(t, testConfig()).ResolveProfile(domain.RoleReviewer, "", Overrides{}, broken)
	wantCode(t, err, CodeInvalidConfig)
}

func TestNewResolverValidates(t *testing.T) {
	cat := newTestSkills(t)
	if _, err := NewResolver(testConfig(), nil); err == nil {
		t.Error("ohne Skill-Katalog erwartet ein Fehler")
	}
	cfg := testConfig()
	delete(cfg.Roles, domain.RoleReviewer)
	_, err := NewResolver(cfg, cat)
	wantCode(t, err, CodeInvalidConfig)
}

func TestResolverConcurrentUse(t *testing.T) {
	cfg := testConfig()
	cfg.Agents["impl-1"] = Agent{Role: domain.RoleImplementer}
	r := newResolver(t, cfg)
	done := make(chan error, 8)
	for i := 0; i < 8; i++ {
		go func() {
			_, err := r.ResolveProfile(domain.RoleImplementer, "impl-1", Overrides{}, testCaps())
			done <- err
		}()
	}
	for i := 0; i < 8; i++ {
		if err := <-done; err != nil {
			t.Error(err)
		}
	}
}

func containsStr(s, sub string) bool { return strings.Contains(s, sub) }
