package domain

import "testing"

// resolvedProfile ist ein aufgelöstes Profil mit genehmigter Reduktion von xhigh auf high
// (model-a unterstützt xhigh nicht, siehe testCatalog).
func resolvedProfile() AgentProfile {
	return AgentProfile{
		ProfileID: "reviewer:rev-1", Version: 7, Role: RoleReviewer, Runtime: RuntimeClaude,
		Model: "model-a", Effort: EffortHigh, SkillIDs: []string{}, PolicyProfile: "default",
		RequestedModel: "model-a", RequestedEffort: EffortXHigh,
		Fallback:     &EffortFallback{Kind: FallbackEffortReduction, From: EffortXHigh, To: EffortHigh, Source: "models.yaml"},
		ValueSources: &ValueSources{Model: SourceRole, Effort: SourceOrder, SkillIDs: SourceRole},
	}
}

func TestCheckProfileRequestedAndFallback(t *testing.T) {
	c := testCatalog()
	if err := c.CheckProfile(resolvedProfile()); err != nil {
		t.Fatalf("konsistentes Profil abgewiesen: %v", err)
	}

	// Ohne die neuen Felder bleibt jedes bisherige Profil gültig (v1-kompatibel).
	old := resolvedProfile()
	old.RequestedModel, old.RequestedEffort, old.Fallback, old.ValueSources = "", "", nil, nil
	if err := c.CheckProfile(old); err != nil {
		t.Fatalf("Profil ohne neue Felder abgewiesen: %v", err)
	}

	for name, tc := range map[string]struct {
		mutate func(p *AgentProfile)
		want   Category
	}{
		"Modell wird nie ersetzt":        {func(p *AgentProfile) { p.RequestedModel = "model-b" }, CatModel},
		"angefordert ungleich wirksam":   {func(p *AgentProfile) { p.Fallback = nil }, CatEffort},
		"Fallback ohne angeforderten":    {func(p *AgentProfile) { p.RequestedEffort = "" }, CatEffort},
		"from weicht von angefordert ab": {func(p *AgentProfile) { p.Fallback.From = EffortMax }, CatEffort},
		"to weicht von wirksam ab":       {func(p *AgentProfile) { p.Fallback.To = EffortMedium }, CatEffort},
		"Anhebung statt Reduktion":       {func(p *AgentProfile) { p.RequestedEffort, p.Fallback.From = EffortLow, EffortLow }, CatEffort},
		"angeforderte Stufe ist unterstützt": {func(p *AgentProfile) {
			p.Effort, p.Fallback.To, p.RequestedEffort, p.Fallback.From = EffortHigh, EffortHigh, EffortHigh, EffortHigh
		}, CatEffort},
		"unbekannte Fallback-Art":          {func(p *AgentProfile) { p.Fallback.Kind = "model_swap" }, CatValue},
		"unbekannte Herkunft":              {func(p *AgentProfile) { p.ValueSources.Effort = "default" }, CatValue},
		"fehlende Herkunft eines Felds":    {func(p *AgentProfile) { p.ValueSources.SkillIDs = "" }, CatValue},
		"wirksame Stufe nicht unterstützt": {func(p *AgentProfile) { p.Effort, p.Fallback.To = EffortMax, EffortMax }, CatEffort},
		"unbekanntes Modell":               {func(p *AgentProfile) { p.Model, p.RequestedModel = "model-x", "model-x" }, CatModel},
	} {
		t.Run(name, func(t *testing.T) {
			bad := resolvedProfile()
			bad.Fallback = &EffortFallback{Kind: FallbackEffortReduction, From: EffortXHigh, To: EffortHigh, Source: "models.yaml"}
			bad.ValueSources = &ValueSources{Model: SourceRole, Effort: SourceOrder, SkillIDs: SourceRole}
			tc.mutate(&bad)
			wantCategory(t, c.CheckProfile(bad), tc.want)
		})
	}
}

func TestValueSourcesAreTheDocumentedRanking(t *testing.T) {
	got := ValueSourceLevels()
	want := []ValueSource{"order", "agent", "supervisor", "role"}
	if len(got) != len(want) {
		t.Fatalf("Stufen = %v, erwartet %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] || !got[i].Valid() {
			t.Errorf("Stufe %d = %q, erwartet %q", i, got[i], want[i])
		}
	}
	if ValueSource("default").Valid() {
		t.Error("unbekannte Stufe gilt als gültig")
	}
}
