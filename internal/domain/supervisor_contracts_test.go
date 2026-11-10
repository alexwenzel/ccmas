package domain

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
)

// Tests für die Erweiterungen von CC-14 (v1-kompatibel): documentation_work, expected_outcome, estimate,
// plan_extension, DecisionBatch sowie die erweiterte Prüfung von Decision.Check (Effort-Fallback,
// Skills je Rolle).

func TestDocumentationWorkAction(t *testing.T) {
	if !ActionDocumentationWork.Valid() {
		t.Fatal("documentation_work ist keine gültige Aktion")
	}
	if !slices.Equal(ActionDocumentationWork.Roles(), []Role{RoleDocumentation}) {
		t.Fatalf("Rollen der Aktion = %v", ActionDocumentationWork.Roles())
	}
	if !ActionDocumentationWork.RequiresSelection() || ActionWait.RequiresSelection() || ActionPlanExtend.RequiresSelection() {
		t.Fatal("Auswahl ist Pflicht bei concept_work, implementation, review und documentation_work, sonst nicht")
	}

	ctx := DecisionContext{ProjectID: "proj-1", StateRevision: 7, Catalog: testCatalog()}
	mk := func(role string) map[string]any {
		m := decisionFor("documentation_work")
		m["selection"] = map[string]any{"role": role, "skill_ids": []any{}, "model": "model-a", "effort": "high"}
		return m
	}
	d, err := ParseDecision(mustJSON(t, mk("documentation")))
	if err != nil {
		t.Fatalf("documentation_work mit Auswahl abgewiesen: %v", err)
	}
	if err := d.Check(ctx); err != nil {
		t.Fatalf("Rolle documentation muss erreichbar sein: %v", err)
	}
	for _, role := range []string{"implementer", "reviewer", "concept", "supervisor"} {
		d, err := ParseDecision(mustJSON(t, mk(role)))
		if err != nil {
			t.Fatal(err)
		}
		wantCategory(t, d.Check(ctx), CatRole)
	}
	// Ohne Auswahl ist die Entscheidung formal ungültig.
	m := mk("documentation")
	delete(m, "selection")
	_, err = ParseDecision(mustJSON(t, m))
	wantCategory(t, err, CatField)
}

func TestDecisionExpectedOutcomeEstimateAndPlanExtension(t *testing.T) {
	raw := readFixture(t, "valid", "decision--expected_outcome_and_estimate.json")
	d, err := ParseDecision(raw)
	if err != nil {
		t.Fatal(err)
	}
	if d.ExpectedOutcome == "" || d.Estimate == nil || d.Estimate.Size != SizeM || d.Estimate.Complexity != LevelMedium || d.Estimate.Uncertainty != LevelLow {
		t.Fatalf("Felder nicht übernommen: %+v", d)
	}
	if d.PlanExtension != nil {
		t.Fatal("plan_extension darf ohne Angabe nicht gesetzt sein")
	}
	// Der Aufwand ist vom Modell-Effort getrennt: beide Felder existieren nebeneinander.
	if d.Selection == nil || d.Selection.Effort != EffortHigh {
		t.Fatalf("Selection = %+v", d.Selection)
	}

	raw = readFixture(t, "valid", "decision--plan_extend_with_extension.json")
	d, err = ParseDecision(raw)
	if err != nil {
		t.Fatal(err)
	}
	if d.PlanExtension == nil || len(d.PlanExtension.WorkOrders) != 2 {
		t.Fatalf("plan_extension = %+v", d.PlanExtension)
	}
	first := d.PlanExtension.WorkOrders[0]
	if first.Ref != "wo-new-1" || first.Kind != PlanPartial || first.Title == "" || first.Goal == "" ||
		len(first.AcceptanceCriteria) != 1 || !slices.Equal(first.DependsOn, []string{"wo-1"}) || first.Mode != "" {
		t.Fatalf("erster Auftrag = %+v", first)
	}
	second := d.PlanExtension.WorkOrders[1]
	if second.Kind != PlanRepair || second.Mode != ModePR || len(second.AcceptanceCriteria) != 2 {
		t.Fatalf("zweiter Auftrag = %+v", second)
	}
	if d.Estimate == nil || d.Estimate.Size != SizeXL {
		t.Fatalf("Estimate = %+v", d.Estimate)
	}
	// Round-Trip: Serialisieren und erneutes Prüfen ergibt dieselbe Entscheidung.
	again, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	d2, err := ParseDecision(again)
	if err != nil {
		t.Fatalf("Round-Trip: %v", err)
	}
	if !equalJSON(t, d, d2) {
		t.Fatal("Round-Trip verändert die Entscheidung")
	}

	// Bisherige Entscheidungen bleiben gültig und tragen keine neuen Felder (v1-kompatibel).
	old, err := ParseDecision(readFixture(t, "valid", "decision--implementation.json"))
	if err != nil {
		t.Fatal(err)
	}
	if old.ExpectedOutcome != "" || old.Estimate != nil || old.PlanExtension != nil {
		t.Fatalf("alte Entscheidung trägt neue Felder: %+v", old)
	}
	out, _ := json.Marshal(old)
	for _, k := range []string{"expected_outcome", "estimate", "plan_extension"} {
		if m := map[string]any{}; json.Unmarshal(out, &m) != nil || m[k] != nil {
			t.Errorf("nicht gesetztes Feld %s wird serialisiert", k)
		}
	}
}

func equalJSON(t *testing.T, a, b any) bool {
	t.Helper()
	ja, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	jb, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	return string(ja) == string(jb)
}

func TestPlanExtensionOnlyWithPlanExtend(t *testing.T) {
	for _, action := range DecisionActions() {
		m := decisionFor(string(action))
		m["plan_extension"] = map[string]any{"work_orders": []any{map[string]any{
			"ref": "wo-new", "kind": "partial", "title": "t", "goal": "g",
			"acceptance_criteria": []any{"k"}, "depends_on": []any{},
		}}}
		if action == ActionDocumentationWork {
			m["selection"] = map[string]any{"role": "documentation", "skill_ids": []any{}, "model": "model-a", "effort": "high"}
		}
		_, err := ParseDecision(mustJSON(t, m))
		if action == ActionPlanExtend {
			if err != nil {
				t.Errorf("plan_extend mit plan_extension abgewiesen: %v", err)
			}
			continue
		}
		wantCategory(t, err, CatValue)
	}
}

func TestEnumsOfCC14MatchSchema(t *testing.T) {
	node := func(prop string) []string {
		d := schemaDoc(t, "decision-v1.schema.json")["$defs"].(map[string]any)["decision"].(map[string]any)["properties"].(map[string]any)
		return enumOf(t, d["estimate"].(map[string]any)["properties"].(map[string]any)[prop].(map[string]any))
	}
	strs := func(in ...string) []string { return in }
	if got := node("size"); !slices.Equal(sorted(got), sorted(strs(string(SizeXS), string(SizeS), string(SizeM), string(SizeL), string(SizeXL)))) {
		t.Errorf("estimate.size: Schema %v", got)
	}
	levels := strs(string(LevelLow), string(LevelMedium), string(LevelHigh))
	for _, p := range []string{"complexity", "uncertainty"} {
		if got := node(p); !slices.Equal(sorted(got), sorted(levels)) {
			t.Errorf("estimate.%s: Schema %v", p, got)
		}
	}
	d := schemaDoc(t, "decision-v1.schema.json")["$defs"].(map[string]any)["decision"].(map[string]any)["properties"].(map[string]any)
	item := d["plan_extension"].(map[string]any)["properties"].(map[string]any)["work_orders"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)
	if got := enumOf(t, item["kind"].(map[string]any)); !slices.Equal(sorted(got), sorted(strs(string(PlanPartial), string(PlanCorrection), string(PlanRepair)))) {
		t.Errorf("plan_extension.kind: Schema %v", got)
	}
}

// ---- DecisionBatch -------------------------------------------------------------

func TestDecisionBatchParse(t *testing.T) {
	b, err := ParseDecisionBatch(readFixture(t, "valid", "decision-batch--three_decisions.json"))
	if err != nil {
		t.Fatal(err)
	}
	if b.SchemaVersion != 1 || b.BatchID != "batch-1" || b.ProjectID != "ccmas" || b.StateRevision != 7 ||
		b.Goal.Kind != KindMilestone || b.Goal.ID != "ms-1" || b.Rationale == "" || len(b.Decisions) != 3 {
		t.Fatalf("Batch = %+v", b)
	}
	if b.Decisions[0].Action != ActionImplementation || b.Decisions[2].PlanExtension == nil {
		t.Fatalf("Entscheidungen = %+v", b.Decisions)
	}
	v, err := Parse(TargetDecisionBatch, readFixture(t, "valid", "decision-batch--minimal.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := v.(DecisionBatch); !ok {
		t.Fatalf("Parse liefert %T", v)
	}
	if !slices.Contains(Targets(), TargetDecisionBatch) {
		t.Fatal("decision-batch fehlt in Targets()")
	}
}

func TestDecisionBatchRejections(t *testing.T) {
	good := func() map[string]any {
		return map[string]any{
			"schema_version": 1, "batch_id": "b-1", "project_id": "proj-1", "state_revision": 7,
			"goal": map[string]any{"kind": "milestone", "id": "ms-1"}, "rationale": "Kurz.",
			"decisions": []any{decisionFor("wait")},
		}
	}
	if _, err := ParseDecisionBatch(mustJSON(t, good())); err != nil {
		t.Fatalf("Grundbatch muss gültig sein: %v", err)
	}
	for name, tc := range map[string]struct {
		mutate func(m map[string]any)
		want   Category
	}{
		"Version":                   {func(m map[string]any) { m["schema_version"] = 2 }, CatVersion},
		"batch_id fehlt":            {func(m map[string]any) { delete(m, "batch_id") }, CatField},
		"Zielbezug fehlt":           {func(m map[string]any) { delete(m, "goal") }, CatField},
		"Begründung fehlt":          {func(m map[string]any) { delete(m, "rationale") }, CatField},
		"Begründung leer":           {func(m map[string]any) { m["rationale"] = "" }, CatValue},
		"Begründung zu lang":        {func(m map[string]any) { m["rationale"] = strings.Repeat("x", 1001) }, CatSize},
		"Zielart work_order":        {func(m map[string]any) { m["goal"] = map[string]any{"kind": "work_order", "id": "wo-1"} }, CatValue},
		"keine Entscheidungen":      {func(m map[string]any) { m["decisions"] = []any{} }, CatValue},
		"unbekanntes Feld":          {func(m map[string]any) { m["extra"] = 1 }, CatUnknownField},
		"Entscheidung ungültig":     {func(m map[string]any) { m["decisions"] = []any{decisionFor("deploy")} }, CatAction},
		"Revision negativ":          {func(m map[string]any) { m["state_revision"] = -1 }, CatValue},
		"Entscheidungen kein Array": {func(m map[string]any) { m["decisions"] = decisionFor("wait") }, CatValue},
	} {
		t.Run(name, func(t *testing.T) {
			m := good()
			tc.mutate(m)
			_, err := ParseDecisionBatch(mustJSON(t, m))
			var ce *ContractError
			if !errors.As(err, &ce) {
				t.Fatalf("kein ContractError: %v", err)
			}
			if !slices.Contains(ce.Categories, tc.want) {
				t.Fatalf("Klassen %v enthalten %s nicht: %v", ce.Categories, tc.want, ce)
			}
		})
	}
	// Höchstens 32 Entscheidungen.
	m := good()
	var many []any
	for i := 0; i < 33; i++ {
		many = append(many, decisionFor("wait"))
	}
	m["decisions"] = many
	_, err := ParseDecisionBatch(mustJSON(t, m))
	wantCategory(t, err, CatSize)
}

// ---- Decision.Check: genehmigte Effort-Fallbacks ---------------------------------------------

// fallbackCatalog kennt model-a ohne xhigh (wie testCatalog) und genehmigt dort die Reduktion xhigh -> high.
func fallbackCatalog() Catalog {
	c := testCatalog()
	c.Fallbacks = []CatalogFallback{{Model: "model-a", Requested: EffortXHigh, Effective: EffortHigh, Source: "models.yaml"}}
	return c
}

func selectionDecision(t *testing.T, model string, effort Effort, mutate ...func(m map[string]any)) Decision {
	t.Helper()
	m := decisionFor("implementation")
	sel := m["selection"].(map[string]any)
	sel["model"], sel["effort"] = model, string(effort)
	for _, f := range mutate {
		f(m)
	}
	d, err := ParseDecision(mustJSON(t, m))
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestDecisionCheckRecognisesApprovedEffortFallback(t *testing.T) {
	ctx := DecisionContext{ProjectID: "proj-1", StateRevision: 7, Catalog: fallbackCatalog()}

	d := selectionDecision(t, "model-a", EffortXHigh)
	if err := d.Check(ctx); err != nil {
		t.Fatalf("genehmigter Fallback wird nicht anerkannt: %v", err)
	}
	// Die Reduktion bleibt sichtbar: der wirksame Wert und der Fallback werden geliefert.
	got, err := d.CheckEffective(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Model != "model-a" || got.RequestedEffort != EffortXHigh || got.Effort != EffortHigh {
		t.Fatalf("wirksame Auswahl = %+v", got)
	}
	want := &EffortFallback{Kind: FallbackEffortReduction, From: EffortXHigh, To: EffortHigh, Source: "models.yaml"}
	if got.Fallback == nil || *got.Fallback != *want {
		t.Fatalf("Fallback = %+v, erwartet %+v", got.Fallback, want)
	}
	if d.Selection.Effort != EffortXHigh {
		t.Fatal("Check darf die Entscheidung nicht verändern")
	}

	// Ohne Fallback-Eintrag bleibt die Ablehnung (Verhalten unverändert).
	wantCategory(t, selectionDecision(t, "model-a", EffortXHigh).Check(DecisionContext{ProjectID: "proj-1", StateRevision: 7, Catalog: testCatalog()}), CatEffort)
	// Die Genehmigung gilt nur für genau dieses Paar aus Modell und angeforderter Stufe.
	wantCategory(t, selectionDecision(t, "model-a", EffortMax).Check(ctx), CatEffort)
	other := fallbackCatalog()
	other.Models["model-c"] = []Effort{EffortLow, EffortMedium, EffortHigh}
	wantCategory(t, selectionDecision(t, "model-c", EffortXHigh).Check(DecisionContext{ProjectID: "proj-1", StateRevision: 7, Catalog: other}), CatEffort)
	// Ein unbekanntes Modell wird nie ersetzt.
	wantCategory(t, selectionDecision(t, "model-x", EffortXHigh).Check(ctx), CatModel)
	// Eine unterstützte Stufe wird nicht reduziert.
	plain, err := selectionDecision(t, "model-a", EffortHigh).CheckEffective(ctx)
	if err != nil || plain.Fallback != nil || plain.Effort != EffortHigh || plain.RequestedEffort != EffortHigh {
		t.Fatalf("unterstützte Stufe: %+v, %v", plain, err)
	}
	// Ein inkonsistenter Katalog (Ziel nicht unterstützt, Ziel nicht niedriger) genehmigt nichts.
	broken := fallbackCatalog()
	broken.Fallbacks = []CatalogFallback{{Model: "model-a", Requested: EffortXHigh, Effective: EffortMax, Source: "x"}}
	wantCategory(t, selectionDecision(t, "model-a", EffortXHigh).Check(DecisionContext{ProjectID: "proj-1", StateRevision: 7, Catalog: broken}), CatEffort)
	broken.Fallbacks = []CatalogFallback{{Model: "model-a", Requested: EffortXHigh, Effective: EffortXHigh, Source: "x"}}
	wantCategory(t, selectionDecision(t, "model-a", EffortXHigh).Check(DecisionContext{ProjectID: "proj-1", StateRevision: 7, Catalog: broken}), CatEffort)

	// Aktionen ohne Auswahl liefern keine wirksame Auswahl.
	wait, err := ParseDecision(mustJSON(t, decisionFor("wait")))
	if err != nil {
		t.Fatal(err)
	}
	if eff, err := wait.CheckEffective(ctx); err != nil || eff != nil {
		t.Fatalf("wait: %+v, %v", eff, err)
	}
}

func TestCatalogEffectiveEffort(t *testing.T) {
	c := fallbackCatalog()
	eff, fb, err := c.EffectiveEffort("model-b", EffortMax)
	if err != nil || eff != EffortMax || fb != nil {
		t.Fatalf("unterstützt: %v %v %v", eff, fb, err)
	}
	eff, fb, err = c.EffectiveEffort("model-a", EffortXHigh)
	if err != nil || eff != EffortHigh || fb == nil || fb.From != EffortXHigh || fb.To != EffortHigh {
		t.Fatalf("Fallback: %v %v %v", eff, fb, err)
	}
	_, _, err = c.EffectiveEffort("model-a", Effort("ultra"))
	wantCategory(t, err, CatEffort)
	_, _, err = c.EffectiveEffort("nirgends", EffortLow)
	wantCategory(t, err, CatModel)
}

// ---- Decision.Check: Skills gegen die Rolle der Auswahl ---------------------------------------

func roleCatalog() Catalog {
	c := testCatalog()
	c.Skills["ccmas-obsidian-seite"] = true
	c.Skills["ccmas-code-review"] = true
	c.SkillRoles = map[string][]Role{
		"ccmas-feature-parallel": {RoleImplementer},
		"ccmas-obsidian-seite":   {RoleConcept, RoleDocumentation},
		"ccmas-code-review":      {RoleReviewer, RoleConceptReviewer},
	}
	return c
}

func TestDecisionCheckSkillsAgainstSelectedRole(t *testing.T) {
	ctx := DecisionContext{ProjectID: "proj-1", StateRevision: 7, Catalog: roleCatalog()}
	withSkills := func(role string, ids ...string) func(m map[string]any) {
		return func(m map[string]any) {
			sel := m["selection"].(map[string]any)
			sel["role"] = role
			list := []any{}
			for _, id := range ids {
				list = append(list, id)
			}
			sel["skill_ids"] = list
		}
	}
	mk := func(action, role string, ids ...string) Decision {
		m := decisionFor(action)
		withSkills(role, ids...)(m)
		d, err := ParseDecision(mustJSON(t, m))
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	for name, d := range map[string]Decision{
		"Skill für die Rolle erlaubt":        mk("implementation", "implementer", "ccmas-feature-parallel"),
		"Skill für mehrere Rollen":           mk("concept_work", "concept", "ccmas-obsidian-seite"),
		"Review mit Reviewer-Skill":          mk("review", "reviewer", "ccmas-code-review"),
		"Review der Konzeptprüfung":          mk("review", "concept_reviewer", "ccmas-code-review"),
		"keine Skills (Standardausstattung)": mk("implementation", "implementer"),
	} {
		if err := d.Check(ctx); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	// Bekannt und freigegeben, aber für die Rolle nicht erlaubt: Rollenfehler, kein Skill-Unbekannt-Fehler.
	for name, d := range map[string]Decision{
		"Implementierer mit Review-Skill":   mk("implementation", "implementer", "ccmas-code-review"),
		"Konzept mit Implementierungsskill": mk("concept_work", "concept", "ccmas-feature-parallel"),
		"ein falscher von zwei Skills":      mk("implementation", "implementer", "ccmas-feature-parallel", "ccmas-obsidian-seite"),
	} {
		t.Run(name, func(t *testing.T) { wantCategory(t, d.Check(ctx), CatRole) })
	}
	// Unbekannte Skills bleiben ein Wertfehler.
	wantCategory(t, mk("implementation", "implementer", "ccmas-gibtsnicht").Check(ctx), CatValue)
	// Ein Skill ohne Eintrag in SkillRoles ist bei vorhandener Rolleninformation nicht erlaubt (fail-closed).
	partial := roleCatalog()
	delete(partial.SkillRoles, "ccmas-feature-parallel")
	wantCategory(t, mk("implementation", "implementer", "ccmas-feature-parallel").Check(DecisionContext{ProjectID: "proj-1", StateRevision: 7, Catalog: partial}), CatRole)
	// Ohne Rolleninformation im Katalog (SkillRoles nil) bleibt das bisherige Verhalten.
	if err := mk("implementation", "implementer", "ccmas-feature-parallel").Check(DecisionContext{ProjectID: "proj-1", StateRevision: 7, Catalog: testCatalog()}); err != nil {
		t.Fatalf("Katalog ohne Rolleninformation: %v", err)
	}

	// Dasselbe gilt für AgentProfile.
	p := AgentProfile{
		ProfileID: "impl", Version: 1, Role: RoleImplementer, Runtime: RuntimeClaude, Model: "model-a",
		Effort: EffortHigh, SkillIDs: []string{"ccmas-feature-parallel"}, PolicyProfile: "default",
	}
	if err := roleCatalog().CheckProfile(p); err != nil {
		t.Fatal(err)
	}
	p.SkillIDs = []string{"ccmas-code-review"}
	wantCategory(t, roleCatalog().CheckProfile(p), CatRole)
}
