package scenario_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/alexwenzel/ccmas/internal/domain"
	"github.com/alexwenzel/ccmas/internal/profiles"
	"github.com/alexwenzel/ccmas/internal/skills"
	"github.com/alexwenzel/ccmas/internal/supervisor"
	"github.com/alexwenzel/ccmas/internal/supervisor/scenario"
)

// Erfundene Modell-IDs: produktive IDs stehen nur in config/*.example.yaml.
const (
	mBig = "model-big"
	mMid = "model-mid"
)

// Echte Skills des Bestands.
const (
	skImpl    = "ccmas-feature-parallel"
	skReview  = "ccmas-code-review"
	skConcept = "ccmas-concept-ticket"

	skConceptReview = "ccmas-concept-review"
)

type env struct {
	t    *testing.T
	pol  supervisor.Policy
	snap map[scenario.ID]supervisor.ProjectSnapshot
}

func newEnv(t *testing.T) *env {
	t.Helper()
	cat, rules, err := skills.LoadChecked("../../..")
	if err != nil {
		t.Fatal(err)
	}
	caps := profiles.Capabilities{
		Revision: 1, Source: "Testquelle", CheckedOn: "2026-11-10", Policies: []string{"readonly", "scoped_write"},
		Models: map[string][]domain.Effort{
			mBig: {domain.EffortLow, domain.EffortMedium, domain.EffortHigh, domain.EffortXHigh, domain.EffortMax},
			mMid: {domain.EffortLow, domain.EffortMedium, domain.EffortHigh},
		},
	}
	cfg := profiles.Config{Revision: 1, Roles: map[domain.Role]profiles.RoleProfile{}, Agents: map[string]profiles.Agent{}}
	for _, r := range domain.Roles() {
		cfg.Roles[r] = profiles.RoleProfile{Runtime: domain.RuntimeClaude, Model: mBig, Effort: domain.EffortHigh, PolicyProfile: "readonly"}
	}
	impl := cfg.Roles[domain.RoleImplementer]
	impl.Model, impl.PolicyProfile = mMid, "scoped_write"
	cfg.Roles[domain.RoleImplementer] = impl
	res, err := profiles.NewResolver(cfg, cat)
	if err != nil {
		t.Fatal(err)
	}
	pol, err := supervisor.NewPolicy(res, caps, cat, rules)
	if err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, pol: pol, snap: map[scenario.ID]supervisor.ProjectSnapshot{}}
	offers := scenario.Env{Roles: supervisor.BuildRoleOffers(cfg, caps, cat, nil), Skills: supervisor.BuildSkillOffers(cat)}
	for _, id := range scenario.IDs() {
		s, err := scenario.Snapshot(id, offers)
		if err != nil {
			t.Fatal(err)
		}
		e.snap[id] = s
	}
	return e
}

// ---- Entscheidungen, handgeschrieben ------------------------------------------------------------------------

func dec(id string, action domain.Action, order string) domain.Decision {
	return domain.Decision{
		SchemaVersion: 1, DecisionID: id, ProjectID: scenario.ProjectID, StateRevision: scenario.Revision,
		Target: domain.DecisionTarget{Kind: domain.KindWorkOrder, ID: order}, Action: action,
		Preconditions: []domain.Precondition{}, Rationale: "Kurze, überprüfbare Begründung.",
	}
}

func withProfile(d domain.Decision, role domain.Role, model string, effort domain.Effort, skill string, size domain.EstimateSize) domain.Decision {
	d.Selection = &domain.Selection{Role: role, SkillIDs: []string{skill}, Model: model, Effort: effort}
	d.ExpectedOutcome = "Überprüfbares Ergebnis."
	d.Estimate = &domain.Estimate{Size: size, Complexity: domain.LevelMedium, Uncertainty: domain.LevelLow}
	return d
}

func implement(id, order string) domain.Decision {
	d := withProfile(dec(id, domain.ActionImplementation, order), domain.RoleImplementer, mMid, domain.EffortHigh, skImpl, domain.SizeM)
	d.Preconditions = []domain.Precondition{{Kind: domain.PrecConceptApproved, Ref: "1.0"}}
	return d
}

func conceptWork(id, order string) domain.Decision {
	return withProfile(dec(id, domain.ActionConceptWork, order), domain.RoleConcept, mBig, domain.EffortHigh, skConcept, domain.SizeM)
}

// conceptReview ist die Prüfung einer Konzeptversion durch den concept_reviewer.
func conceptReview(id, order string) domain.Decision {
	d := withProfile(dec(id, domain.ActionReview, order), domain.RoleConceptReviewer, mBig, domain.EffortHigh, skConceptReview, domain.SizeS)
	d.ReviewTarget = &domain.ReviewTarget{WorkOrderID: order, ConceptVersion: "0.1", Commit: scenario.Commit}
	return d
}

// conceptReady meldet das Konzeptergebnis concept_ready der Rolle concept: erst dann deckt der Validator die Konzeptprüfung.
func conceptReady(s *supervisor.ProjectSnapshot) {
	for i := range s.Orders {
		if s.Orders[i].Kind == supervisor.OrderConcept {
			s.Orders[i].LastResult = &supervisor.LastResult{Role: domain.RoleConcept, Result: "concept_ready"}
		}
	}
}

func review(id, order string) domain.Decision {
	d := withProfile(dec(id, domain.ActionReview, order), domain.RoleReviewer, mBig, domain.EffortHigh, skReview, domain.SizeS)
	d.Preconditions = []domain.Precondition{{Kind: domain.PrecWorkOrderCompleted}}
	d.ReviewTarget = &domain.ReviewTarget{WorkOrderID: order, Commit: scenario.Commit, ConceptVersion: "1.0"}
	return d
}

func question(id, order string) domain.Decision {
	d := dec(id, domain.ActionAskQuestion, order)
	d.Question = &domain.DecisionQuestion{Text: "Soll der Umfang erweitert werden?", Context: "Strategische Entscheidung des Nutzers.", Recommendation: "Nein."}
	return d
}

func wait(id, order string, pre ...domain.Precondition) domain.Decision {
	d := dec(id, domain.ActionWait, order)
	d.Preconditions = append([]domain.Precondition{}, pre...)
	return d
}

func batch(ds ...domain.Decision) domain.DecisionBatch {
	return domain.DecisionBatch{
		SchemaVersion: 1, BatchID: "batch-1", ProjectID: scenario.ProjectID, StateRevision: scenario.Revision,
		Goal: domain.DecisionTarget{Kind: domain.KindMilestone, ID: scenario.GoalID}, Rationale: "Kurze Begründung des Plans.", Decisions: ds,
	}
}

// ---- Tests ---------------------------------------------------------------------------------------------------

type fixture struct {
	name  string
	batch domain.DecisionBatch
	// wantViolation: Teilstring eines Verstoßes (leer: der Batch erfüllt den Vertrag).
	wantViolation string
	// valid: Der Validator nimmt den Batch an (unabhängig vom Urteil der Prädikate).
	valid bool
	// mutate verändert eine Kopie des Snapshots für dieses Beispiel (zum Beispiel ein gemeldetes Konzeptergebnis).
	mutate func(*supervisor.ProjectSnapshot)
}

func TestScenarioPlanContract(t *testing.T) {
	e := newEnv(t)
	waitA := domain.Precondition{Kind: domain.PrecWorkOrderCompleted, Ref: scenario.S1A}

	cases := map[scenario.ID][]fixture{
		scenario.S1: {
			{name: "gut: A und B parallel, C wartet", batch: batch(implement("dec-1", scenario.S1A), implement("dec-2", scenario.S1B), wait("dec-3", scenario.S1C, waitA)), valid: true},
			{name: "gut: nur die Reihenfolge ist anders", batch: batch(wait("dec-1", scenario.S1C, waitA), implement("dec-2", scenario.S1B), implement("dec-3", scenario.S1A)), valid: true},
			{name: "schlecht: C wird implementiert", batch: batch(implement("dec-1", scenario.S1A), implement("dec-2", scenario.S1B), implement("dec-3", scenario.S1C)), wantViolation: "sc1-c", valid: false},
			{name: "schlecht: B fehlt", batch: batch(implement("dec-1", scenario.S1A), wait("dec-2", scenario.S1C, waitA)), wantViolation: "sc1-b", valid: true},
			{name: "schlecht: nur warten", batch: batch(wait("dec-1", scenario.S1C, waitA)), wantViolation: "sc1-a", valid: true},
		},
		scenario.S2Routine: {
			{name: "gut: Konzeptarbeit am Konzeptauftrag", batch: batch(conceptWork("dec-1", scenario.S2ConRoutine), wait("dec-2", scenario.S2ImplRoutine)), valid: true},
			{name: "gut: nur eine Frage an den Nutzer", batch: batch(question("dec-1", scenario.S2ConRoutine)), valid: true},
			{name: "gut: Konzeptreview durch den concept_reviewer (nach gemeldetem Konzeptergebnis)", batch: batch(conceptReview("dec-1", scenario.S2ConRoutine)), valid: true, mutate: conceptReady},
			{name: "Konzeptreview ohne gemeldetes Konzeptergebnis: fachlich zulässig, aber vom Validator nicht gedeckt", batch: batch(conceptReview("dec-1", scenario.S2ConRoutine)), valid: false},
			{name: "schlecht: Implementierung trotz fehlender Konzeptfreigabe", batch: batch(implement("dec-1", scenario.S2ImplRoutine), conceptWork("dec-2", scenario.S2ConRoutine)), wantViolation: "Implementierung beauftragt", valid: false},
			{name: "schlecht: nur Implementierung", batch: batch(implement("dec-1", scenario.S2ImplRoutine)), wantViolation: "Implementierung beauftragt", valid: false},
			{name: "schlecht: nur warten", batch: batch(wait("dec-1", scenario.S2ImplRoutine)), wantViolation: "weder Konzeptarbeit", valid: true},
			{name: "schlecht: Review durch den reviewer statt concept_reviewer", batch: batch(review("dec-1", scenario.S2ConRoutine)), wantViolation: "concept_reviewer", valid: false},
			{name: "schlecht: Abschluss", batch: batch(completion("dec-1")), wantViolation: "ohne freigegebenes Konzept", valid: false},
		},
		scenario.S2Strategic: {
			{name: "gut: Frage an den Nutzer", batch: batch(question("dec-1", scenario.S2ConStrat)), valid: true},
			{name: "gut: klärende Konzeptarbeit", batch: batch(conceptWork("dec-1", scenario.S2ConStrat)), valid: true},
			{name: "gut: Konzeptarbeit und Frage", batch: batch(conceptWork("dec-1", scenario.S2ConStrat), question("dec-2", scenario.S2ConStrat)), valid: true},
			{name: "schlecht: Freigabe über den concept_reviewer", batch: batch(conceptReview("dec-1", scenario.S2ConStrat)), wantViolation: "Nutzer", valid: false, mutate: conceptReady},
			{name: "schlecht: Freigabe über den concept_reviewer, daneben eine Frage", batch: batch(conceptReview("dec-1", scenario.S2ConStrat), question("dec-2", scenario.S2ConStrat)), wantViolation: "Freigabe", valid: false, mutate: conceptReady},
			{name: "schlecht: Implementierung", batch: batch(implement("dec-1", scenario.S2ImplStrat), question("dec-2", scenario.S2ConStrat)), wantViolation: "Implementierung beauftragt", valid: false},
			{name: "schlecht: nur warten", batch: batch(wait("dec-1", scenario.S2ImplStrat)), wantViolation: "weder klärende Konzeptarbeit", valid: true},
		},
		scenario.S3: {
			{name: "gut: Review vor neuer Arbeit, Kapazität eingehalten", batch: batch(review("dec-1", scenario.S3Fin), wait("dec-2", scenario.S3New, domain.Precondition{Kind: domain.PrecCapacityAvailable})), valid: true},
			{name: "gut: nur das Review", batch: batch(review("dec-1", scenario.S3Fin)), valid: true},
			{name: "schlecht: nur neue Implementierung", batch: batch(implement("dec-1", scenario.S3New)), wantViolation: "kein Review", valid: true},
			{name: "schlecht: Implementierung vor dem Review (und zu viele Beauftragungen)", batch: batch(implement("dec-1", scenario.S3New), review("dec-2", scenario.S3Fin)), wantViolation: "vor dem Review", valid: false},
			{name: "schlecht: zu viele Beauftragungen", batch: batch(review("dec-1", scenario.S3Fin), implement("dec-2", scenario.S3New)), wantViolation: "freien Plätzen", valid: false},
		},
		scenario.S4: {
			{name: "gut: Reparatur für denselben Auftrag", batch: batch(implement("dec-1", scenario.S4A)), valid: true},
			{name: "schlecht: Integration trotz Befunden", batch: batch(implement("dec-1", scenario.S4A), integration("dec-2", scenario.S4A)), wantViolation: "trotz offener Befunde", valid: false},
			{name: "schlecht: keine Reparatur", batch: batch(wait("dec-1", scenario.S4A)), wantViolation: "keine Reparatur", valid: true},
			{name: "schlecht: Zielabschluss", batch: batch(completion("dec-1")), wantViolation: "trotz offener Befunde", valid: false},
		},
		scenario.S5: {
			{name: "gut: warten bis zum Merge", batch: batch(wait("dec-1", scenario.S5A)), valid: true},
			{name: "gut: warten, mit Bedingung", batch: batch(wait("dec-1", scenario.S5A, domain.Precondition{Kind: domain.PrecWorkOrderCompleted, Ref: scenario.S5A})), valid: true},
			{name: "schlecht: Zielabschluss bei offenem PR", batch: batch(completion("dec-1")), wantViolation: "Zielabschluss", valid: false},
			{name: "schlecht: erneute Integration", batch: batch(integration("dec-1", scenario.S5A)), wantViolation: "Integrationsanforderung", valid: false},
			{name: "schlecht: neuer Auftrag im Modus auto", batch: batch(planAuto("dec-1"), wait("dec-2", scenario.S5A)), wantViolation: "Modus auto", valid: false},
			{name: "schlecht: kein wait", batch: batch(planPR("dec-1")), wantViolation: "kein wait", valid: true},
		},
	}

	for _, id := range scenario.IDs() {
		snap := e.snap[id]
		t.Run(string(id), func(t *testing.T) {
			if probs := snap.Problems(); len(probs) != 0 {
				t.Fatalf("Snapshot ungültig: %v", probs)
			}
			for _, c := range cases[id] {
				t.Run(c.name, func(t *testing.T) {
					snap := snap
					if c.mutate != nil {
						snap.Orders = slices.Clone(snap.Orders)
						c.mutate(&snap)
					}
					violations := scenario.Evaluate(id, c.batch, snap)
					if c.wantViolation == "" && len(violations) != 0 {
						t.Errorf("gutes Beispiel verletzt Prädikate: %v", violations)
					}
					if c.wantViolation != "" && !slices.ContainsFunc(violations, func(v string) bool { return strings.Contains(v, c.wantViolation) }) {
						t.Errorf("erwarteter Verstoß %q fehlt: %v", c.wantViolation, violations)
					}
					// Die Prädikate sind Urteile über den Inhalt; ob der Validator den Batch annimmt, ist davon unabhängig.
					// Gute Beispiele sind zulässig; die als zulässig markierten schlechten Beispiele sind inhaltlich schlecht, nicht
					// unzulässig (das ist der Unterschied zwischen Zulässigkeit und Planungsqualität).
					_, err := supervisor.Validator{}.Accept(c.batch, snap, e.pol)
					if c.valid && err != nil {
						t.Errorf("Validator lehnt ab: %v", err)
					}
					if !c.valid && err == nil {
						t.Errorf("Validator nimmt an, was als unzulässig gedacht war")
					}
				})
			}
		})
	}
}

func integration(id, order string) domain.Decision {
	d := dec(id, domain.ActionRequestIntegration, order)
	d.Preconditions = []domain.Precondition{{Kind: domain.PrecReviewApproved}}
	return d
}

func completion(id string) domain.Decision {
	d := dec(id, domain.ActionRequestCompletion, scenario.GoalID)
	d.Target = domain.DecisionTarget{Kind: domain.KindMilestone, ID: scenario.GoalID}
	return d
}

func plan(id string, mode domain.IntegrationMode) domain.Decision {
	d := dec(id, domain.ActionPlanExtend, scenario.GoalID)
	d.Target = domain.DecisionTarget{Kind: domain.KindMilestone, ID: scenario.GoalID}
	d.PlanExtension = &domain.PlanExtension{WorkOrders: []domain.PlannedWorkOrder{{
		Ref: "sc-neu", Kind: domain.PlanPartial, Title: "Neuer Auftrag", Goal: "Ziel", AcceptanceCriteria: []string{"erfüllt"}, DependsOn: []string{}, Mode: mode,
	}}}
	return d
}

func planAuto(id string) domain.Decision { return plan(id, domain.ModeAuto) }
func planPR(id string) domain.Decision   { return plan(id, domain.ModePR) }

// TestCommonPredicatesCheckEveryDelegation: jede Beauftragung nennt Rolle, Skills, Modell, Effort, erwartetes Ergebnis,
// Aufwand und Begründung; der Aufwand ist eine eigene Angabe und nicht der Effort.
func TestCommonPredicatesCheckEveryDelegation(t *testing.T) {
	e := newEnv(t)
	snap := e.snap[scenario.S1]
	good := implement("dec-1", scenario.S1A)
	if v := scenario.CheckDelegations(batch(good), snap); len(v) != 0 {
		t.Fatalf("gute Beauftragung verletzt: %v", v)
	}
	mutations := map[string]struct {
		mutate func(d *domain.Decision)
		want   string
	}{
		"keine Auswahl":            {func(d *domain.Decision) { d.Selection = nil }, "keine Auswahl"},
		"keine Skills":             {func(d *domain.Decision) { d.Selection.SkillIDs = []string{} }, "keine Skills"},
		"Skill nicht erlaubt":      {func(d *domain.Decision) { d.Selection.SkillIDs = []string{skReview} }, "Angebot"},
		"kein Modell":              {func(d *domain.Decision) { d.Selection.Model = "" }, "kein Modell"},
		"Modell nicht im Angebot":  {func(d *domain.Decision) { d.Selection.Model = "model-x" }, "Angebot"},
		"kein Effort":              {func(d *domain.Decision) { d.Selection.Effort = "" }, "Effort"},
		"Effort außerhalb":         {func(d *domain.Decision) { d.Selection.Effort = "ultra" }, "Effort"},
		"kein erwartetes Ergebnis": {func(d *domain.Decision) { d.ExpectedOutcome = " " }, "erwartetes Ergebnis"},
		"keine Schätzung":          {func(d *domain.Decision) { d.Estimate = nil }, "Aufwandsschätzung"},
		"Größe unbekannt":          {func(d *domain.Decision) { d.Estimate.Size = "high" }, "Aufwandsschätzung"},
		"Unsicherheit leer":        {func(d *domain.Decision) { d.Estimate.Uncertainty = "" }, "Aufwandsschätzung"},
		"keine Begründung":         {func(d *domain.Decision) { d.Rationale = "" }, "Begründung"},
	}
	for name, m := range mutations {
		t.Run(name, func(t *testing.T) {
			d := good
			sel := *good.Selection
			d.Selection = &sel
			est := *good.Estimate
			d.Estimate = &est
			m.mutate(&d)
			v := scenario.CheckDelegations(batch(d), snap)
			if !slices.ContainsFunc(v, func(s string) bool { return strings.Contains(s, m.want) }) {
				t.Errorf("Verstoß %q fehlt: %v", m.want, v)
			}
		})
	}
	// Aktionen ohne Ausführung brauchen weder Auswahl noch Schätzung.
	if v := scenario.CheckDelegations(batch(wait("dec-1", scenario.S1C)), snap); len(v) != 0 {
		t.Errorf("wait verlangt nichts: %v", v)
	}
}

// TestPredicatesIgnoreWording: Die Prädikate bewerten Aktionen und Ziele, nicht den Wortlaut der Begründungen.
func TestPredicatesIgnoreWording(t *testing.T) {
	e := newEnv(t)
	a, b := implement("dec-1", scenario.S1A), implement("dec-2", scenario.S1B)
	a.Rationale, b.Rationale = "x", "Ganz anders formuliert, mit viel mehr Text und Zeichen: äöü."
	a.ExpectedOutcome = "y"
	if v := scenario.Evaluate(scenario.S1, batch(a, b), e.snap[scenario.S1]); len(v) != 0 {
		t.Errorf("Wortlaut beeinflusst das Urteil: %v", v)
	}
}

func TestSnapshotsAreSyntheticAndDistinct(t *testing.T) {
	e := newEnv(t)
	for _, id := range scenario.IDs() {
		s := e.snap[id]
		for _, o := range s.Orders {
			if !strings.HasPrefix(o.ID, "sc") {
				t.Errorf("%s: Auftrag %s ist nicht als synthetisch gekennzeichnet", id, o.ID)
			}
		}
		if s.ProjectID != scenario.ProjectID || s.StateRevision != scenario.Revision {
			t.Errorf("%s: %s/%d", id, s.ProjectID, s.StateRevision)
		}
	}
	if _, err := scenario.Snapshot("S9", scenario.Env{}); err == nil {
		t.Error("unbekanntes Szenario nicht abgewiesen")
	}
	if s := e.snap[scenario.S3]; s.Capacity.MaxWorkers != 2 || len(s.Capacity.Active) != 1 {
		t.Errorf("S3 hat keine knappe Kapazität: %+v", s.Capacity)
	}
	if s := e.snap[scenario.S5]; s.IntegrationMode != domain.ModePR || s.Orders[0].PR == nil || s.Orders[0].PR.State != supervisor.PROpen ||
		s.Orders[0].LastReview.Report.Verdict != domain.VerdictApproved {
		t.Errorf("S5 entspricht nicht dem Szenario: %+v", s.Orders[0])
	}
	if o := e.snap[scenario.S4].Orders[0]; o.LastReview.Report.Verdict != domain.VerdictFindings || o.CodeRepairRounds >= 3 {
		t.Errorf("S4 entspricht nicht dem Szenario: %+v", o)
	}
}
