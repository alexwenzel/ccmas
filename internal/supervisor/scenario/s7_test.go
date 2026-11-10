package scenario_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/alexwenzel/ccmas/internal/domain"
	"github.com/alexwenzel/ccmas/internal/supervisor"
	"github.com/alexwenzel/ccmas/internal/supervisor/scenario"
)

// Tests für S7 (Profilwahl nach Zustand). Sie nutzen die echte Beispielkonfiguration (newEvEnv): die Prädikate stufen Modelle nach
// der Familie im Namen ein, die erfundenen Modelle des Vertragstests (model-big, model-mid) sind nicht einstufbar.

const (
	opus   = "claude-opus-5-5"
	sonnet = "claude-sonnet-5-5"
	haiku  = "claude-haiku-5-5"
)

// s7Impl ist die Implementierung eines S7-Auftrags mit dem angegebenen Profil.
func (e *evEnv) s7Impl(id, order, model string, effort domain.Effort) domain.Decision {
	var pre []domain.Precondition
	if order == scenario.S7A {
		pre = append(pre, domain.Precondition{Kind: domain.PrecConceptApproved, Ref: "2.0"})
	}
	d := e.decision(id, domain.ActionImplementation, order, domain.RoleImplementer, pre...)
	d.Selection.Model, d.Selection.Effort = model, effort
	return d
}

func s7Batch(ds ...domain.Decision) domain.DecisionBatch {
	return domain.DecisionBatch{SchemaVersion: 1, BatchID: "batch-1", ProjectID: scenario.ProjectID, StateRevision: scenario.Revision,
		Goal: domain.DecisionTarget{Kind: domain.KindMilestone, ID: scenario.GoalID}, Rationale: "Plan.", Decisions: ds}
}

func TestS7SnapshotDescribesTwoIndependentStartableOrders(t *testing.T) {
	e := newEvEnv(t)
	snap, err := scenario.Snapshot(scenario.S7, e.offers)
	if err != nil {
		t.Fatal(err)
	}
	if snap.IntegrationMode != domain.ModePR || snap.Capacity.MaxWorkers < 2 || len(snap.Capacity.Active) != 0 || len(snap.Orders) != 2 {
		t.Fatalf("S7 entspricht nicht dem Szenario: Modus %s, Kapazität %+v, %d Aufträge", snap.IntegrationMode, snap.Capacity, len(snap.Orders))
	}
	for _, o := range snap.Orders {
		if len(o.DependsOn) != 0 || o.State != domain.WOWaiting || o.Mode != domain.ModePR || o.ActiveExecutionID != "" {
			t.Errorf("%s ist nicht unabhängig und startbar: %+v", o.ID, o)
		}
	}
	a, b := snap.Orders[0], snap.Orders[1]
	if a.ID != scenario.S7A || a.Kind != supervisor.OrderImplementation || a.Concept == nil || a.Concept.Approval == nil || a.Concept.Approval.Version != a.Concept.Version ||
		!slices.Contains(a.Concept.Criteria, "architecture_change") || !slices.Contains(a.Concept.Criteria, "permission_change") {
		t.Errorf("sc7-a: kein freigegebenes strategisches Konzept: %+v", a.Concept)
	}
	if b.ID != scenario.S7B || b.Concept != nil || b.Kind != supervisor.OrderBug {
		t.Errorf("sc7-b: ohne Konzeptbezug erwartet: %+v", b)
	}
	if len(snap.Goal.AcceptanceCriteria) < 5 {
		t.Errorf("zu wenige Akzeptanzkriterien: %v", snap.Goal.AcceptanceCriteria)
	}
	// Das Angebot der Rolle implementer erlaubt stärkere und schlankere Modelle und Stufen als den Standard.
	var offer supervisor.RoleOffer
	for _, o := range snap.Roles {
		if o.Role == domain.RoleImplementer {
			offer = o
		}
	}
	if !slices.Contains(offer.AllowedModels, opus) || !slices.Contains(offer.AllowedModels, haiku) || !slices.Contains(offer.AllowedModels, offer.Model) ||
		slices.Index(offer.AllowedEfforts, domain.EffortXHigh) <= slices.Index(offer.AllowedEfforts, offer.Effort) ||
		slices.Index(offer.AllowedEfforts, domain.EffortLow) >= slices.Index(offer.AllowedEfforts, offer.Effort) {
		t.Errorf("das Angebot der Rolle implementer erlaubt keine stärkeren und schlankeren Profile: %+v", offer)
	}
}

func TestS7ProfileChoiceFollowsTheState(t *testing.T) {
	e := newEvEnv(t)
	snap, err := scenario.Snapshot(scenario.S7, e.offers)
	if err != nil {
		t.Fatal(err)
	}
	def := e.sel(domain.RoleImplementer) // Rollenstandard: sonnet/high
	if def.Model != sonnet || def.Effort != domain.EffortHigh {
		t.Fatalf("Rollenstandard %s/%s, die Fälle gehen von sonnet/high aus", def.Model, def.Effort)
	}
	a := func(model string, effort domain.Effort) domain.Decision {
		return e.s7Impl("dec-1", scenario.S7A, model, effort)
	}
	b := func(model string, effort domain.Effort) domain.Decision {
		return e.s7Impl("dec-2", scenario.S7B, model, effort)
	}

	cases := []struct {
		name  string
		batch domain.DecisionBatch
		// wantViolation: Teilstring eines Verstoßes; leer: alle Prädikate erfüllt.
		wantViolation string
		// rejected: der Validator lehnt den Batch ab (die Prädikate urteilen unabhängig davon).
		rejected bool
	}{
		{name: "gut: stärkeres Modell für a, Standard für b", batch: s7Batch(a(opus, domain.EffortHigh), b(sonnet, domain.EffortHigh))},
		{name: "gut: höherer Effort für a, schlankerer Effort für b", batch: s7Batch(a(sonnet, domain.EffortXHigh), b(sonnet, domain.EffortMedium))},
		{name: "gut: stärkeres Modell und höherer Effort für a, schlankeres Modell für b", batch: s7Batch(a(opus, domain.EffortMax), b(haiku, domain.EffortLow))},
		{name: "gut: Reihenfolge der Entscheidungen ist gleichgültig", batch: s7Batch(b(sonnet, domain.EffortHigh), a(opus, domain.EffortXHigh))},
		{name: "schlecht: Standard für a (kein strengeres Profil)", batch: s7Batch(a(sonnet, domain.EffortHigh), b(sonnet, domain.EffortHigh)), wantViolation: "nicht strenger als der Rollenstandard"},
		{name: "schlecht: schlankeres Profil für a", batch: s7Batch(a(sonnet, domain.EffortLow), b(sonnet, domain.EffortLow)), wantViolation: "nicht strenger als der Rollenstandard"},
		{name: "schlecht: stärkeres Modell für b", batch: s7Batch(a(opus, domain.EffortHigh), b(opus, domain.EffortHigh)), wantViolation: "obwohl der Auftrag trivial"},
		{name: "schlecht: höherer Effort für b", batch: s7Batch(a(opus, domain.EffortHigh), b(sonnet, domain.EffortXHigh)), wantViolation: "obwohl der Auftrag trivial"},
		{name: "schlecht: stärkeres Profil für beide", batch: s7Batch(a(opus, domain.EffortMax), b(opus, domain.EffortMax)), wantViolation: "sc7-b"},
		{name: "schlecht: b fehlt", batch: s7Batch(a(opus, domain.EffortHigh)), wantViolation: "keine Implementierung für den unabhängigen, startbaren Auftrag sc7-b"},
		{name: "schlecht: a fehlt", batch: s7Batch(b(sonnet, domain.EffortHigh)), wantViolation: "keine Implementierung für den unabhängigen, startbaren Auftrag sc7-a"},
		{name: "schlecht: b wartet statt umgesetzt zu werden", batch: s7Batch(a(opus, domain.EffortHigh), e.decision("dec-2", domain.ActionWait, scenario.S7B, "")), wantViolation: "sc7-b"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			violations := scenario.Evaluate(scenario.S7, c.batch, snap)
			if c.wantViolation == "" && len(violations) != 0 {
				t.Errorf("gutes Beispiel verletzt Prädikate: %v", violations)
			}
			if c.wantViolation != "" && !slices.ContainsFunc(violations, func(v string) bool { return strings.Contains(v, c.wantViolation) }) {
				t.Errorf("erwarteter Verstoß %q fehlt: %v", c.wantViolation, violations)
			}
			// Zulässigkeit ist davon unabhängig: Alle Beispiele sind zulässige Pläne, auch die inhaltlich schlechten.
			if _, err := (supervisor.Validator{}).Accept(c.batch, snap, e.pol); err != nil != c.rejected {
				t.Errorf("Validator: Fehler %v, abgelehnt erwartet: %v", err, c.rejected)
			}
		})
	}
}

// TestS7NamesTheViolatedPredicate: Das Ergebnis je Prädikat nennt, welches Prädikat verfehlt ist.
func TestS7NamesTheViolatedPredicate(t *testing.T) {
	e := newEvEnv(t)
	snap, _ := scenario.Snapshot(scenario.S7, e.offers)
	met := func(b domain.DecisionBatch) map[string]bool {
		out := map[string]bool{}
		for _, r := range scenario.EvaluateDetailed(scenario.S7, b, snap) {
			out[r.Name] = r.Met
		}
		return out
	}
	got := met(s7Batch(e.s7Impl("dec-1", scenario.S7A, sonnet, domain.EffortHigh), e.s7Impl("dec-2", scenario.S7B, sonnet, domain.EffortHigh)))
	if got["critical_order_stricter_profile"] || !got["trivial_order_not_stronger"] || !got["both_orders_implemented"] || !got["rationales_state_profile_reason"] || !got["delegations_complete"] {
		t.Errorf("Standard für a: Ergebnis je Prädikat = %v", got)
	}
	got = met(s7Batch(e.s7Impl("dec-1", scenario.S7A, opus, domain.EffortHigh), e.s7Impl("dec-2", scenario.S7B, opus, domain.EffortHigh)))
	if !got["critical_order_stricter_profile"] || got["trivial_order_not_stronger"] {
		t.Errorf("stärkeres Profil für b: Ergebnis je Prädikat = %v", got)
	}
}

// TestS7RationalesAreCheckedForPresenceNotWording: Die Begründung zählt als vorhanden, gleich wie sie lautet, und fehlt, wenn sie leer ist.
func TestS7RationalesAreCheckedForPresenceNotWording(t *testing.T) {
	e := newEvEnv(t)
	snap, _ := scenario.Snapshot(scenario.S7, e.offers)
	a, b := e.s7Impl("dec-1", scenario.S7A, opus, domain.EffortHigh), e.s7Impl("dec-2", scenario.S7B, sonnet, domain.EffortMedium)
	a.Rationale, b.Rationale = "x", "Ganz anders formuliert, mit viel mehr Text: äöü."
	if v := scenario.Evaluate(scenario.S7, s7Batch(a, b), snap); len(v) != 0 {
		t.Errorf("Wortlaut beeinflusst das Urteil: %v", v)
	}
	b.Rationale = "  "
	v := scenario.Evaluate(scenario.S7, s7Batch(a, b), snap)
	if !slices.ContainsFunc(v, func(s string) bool { return strings.Contains(s, "keine Begründung der Profilwahl für sc7-b") }) {
		t.Errorf("fehlende Begründung nicht gefunden: %v", v)
	}
}

// TestS7ModelStrengthNeedsAKnownFamily: Ein Modell, dessen Stärke sich nicht gegen den Standard einstufen lässt, ist ein Verstoß und
// gilt nicht stillschweigend als erfüllt.
func TestS7ModelStrengthNeedsAKnownFamily(t *testing.T) {
	e := newEvEnv(t)
	snap, _ := scenario.Snapshot(scenario.S7, e.offers)
	b := s7Batch(e.s7Impl("dec-1", scenario.S7A, "claude-neu-1", domain.EffortHigh), e.s7Impl("dec-2", scenario.S7B, sonnet, domain.EffortHigh))
	v := scenario.Check(scenario.S7, b, snap)
	if !slices.ContainsFunc(v, func(s string) bool { return strings.Contains(s, "nicht einstufbar") }) {
		t.Errorf("unbekannte Familie nicht beanstandet: %v", v)
	}
}
