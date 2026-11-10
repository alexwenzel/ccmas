package supervisor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexwenzel/ccmas/internal/domain"
	"github.com/alexwenzel/ccmas/internal/skills"
	"github.com/alexwenzel/ccmas/prompts"
)

// Befundrunde 2 des Reviews zu CC-14: keine Dubletten externer Aktionen, Konzeptbezug über Abhängigkeit, die feste Kennung
// für Aufträge ohne Konzeptbezug und die Pflichtangaben jeder Beauftragung.

// ---- Erneute Integration ------------------------------------------------------------------------------------------

func TestReintegrationIsRejectedWhenItWouldHaveNoNewEffect(t *testing.T) {
	e := newEnv(t)
	withPR := func(pr *PRInfo) ProjectSnapshot {
		s := baseSnapshot()
		s.order(t, "wo-ready").PR = pr // wo-ready: Kopf und freigegebenes Review gelten für commitB
		return s
	}
	t.Run("offener PR trägt bereits den freigegebenen Commit", func(t *testing.T) {
		s := withPR(&PRInfo{Number: 5, State: PROpen, CI: CIPassed, HeadCommit: commitB})
		vb, err := e.check(batch(integrationDecision("dec-1", "wo-ready")), s)
		wantRejection(t, err, CodeDuplicateStart, "dec-1", "#5", commitB)
		onlyCodes(t, err, CodeDuplicateStart)
		if vb.Decisions != nil {
			t.Fatal("eine Dublette wird nicht ausgeführt")
		}
	})
	t.Run("PR ist bereits gemergt", func(t *testing.T) {
		for _, head := range []string{commitB, commitA, ""} {
			s := withPR(&PRInfo{Number: 5, State: PRMerged, CI: CIPassed, HeadCommit: head})
			_, err := e.check(batch(integrationDecision("dec-1", "wo-ready")), s)
			wantRejection(t, err, CodeDuplicateStart, "dec-1", "gemergt")
			onlyCodes(t, err, CodeDuplicateStart)
		}
	})
	t.Run("offener PR ohne belegten Kopf-Commit: nicht belegbar, also abgelehnt", func(t *testing.T) {
		s := withPR(&PRInfo{Number: 5, State: PROpen, CI: CIPassed})
		_, err := e.check(batch(integrationDecision("dec-1", "wo-ready")), s)
		wantRejection(t, err, CodeDuplicateStart, "dec-1", "nicht belegt")
	})
	t.Run("offener PR mit älterem Stand: die Aktualisierung bleibt zulässig", func(t *testing.T) {
		// Nach Reparatur und neuem Review FREIGEGEBEN für commitB trägt der PR noch commitA.
		s := withPR(&PRInfo{Number: 5, State: PROpen, CI: CIPassed, HeadCommit: commitA})
		e.mustAccept(batch(integrationDecision("dec-1", "wo-ready")), s)
	})
	t.Run("geschlossener PR und erste Integration bleiben zulässig", func(t *testing.T) {
		e.mustAccept(batch(integrationDecision("dec-1", "wo-ready")), withPR(&PRInfo{Number: 5, State: PRClosed, CI: CIPassed, HeadCommit: commitB}))
		e.mustAccept(batch(integrationDecision("dec-1", "wo-ready")), withPR(nil))
	})
	t.Run("das Review gilt für einen anderen Commit als den des PR, nicht für den Kopf des Auftrags", func(t *testing.T) {
		// Nach dem Review hat sich der Kopf geändert: PushAllowed lehnt ab, nicht die Dublettenregel.
		s := withPR(&PRInfo{Number: 5, State: PROpen, CI: CIPassed, HeadCommit: commitB})
		s.order(t, "wo-ready").HeadCommit = commitA
		_, err := e.check(batch(integrationDecision("dec-1", "wo-ready")), s)
		wantRejection(t, err, CodeIntegrationNotApproved, "dec-1")
	})
}

func TestPRHeadCommitMustBeACompleteHash(t *testing.T) {
	s := baseSnapshot()
	s.order(t, "wo-ready").PR = &PRInfo{Number: 5, State: PROpen, CI: CIPassed, HeadCommit: "abc"}
	probs := s.Problems()
	if len(probs) != 1 || probs[0].Path != "orders[6].pr.head_commit" {
		t.Fatalf("Verstöße = %v", probs)
	}
}

// ---- Konzeptbezug nur über Abhängigkeit ---------------------------------------------------------------------------

func TestIntegrationOfAnOrderWithConceptReferenceOnlyThroughDependencyIsRejected(t *testing.T) {
	e := newEnv(t)
	for _, version := range []string{NoConceptVersion, "1.0", "irgendwas"} {
		s := baseSnapshot()
		ready := s.order(t, "wo-ready")
		ready.Concept = nil
		ready.DependsOn = []string{"wo-con"} // Konzeptauftrag: Konzeptbezug, aber kein Konzeptstand am Auftrag
		ready.LastReview.Report.ConceptVersion = version
		_, err := e.check(batch(integrationDecision("dec-1", "wo-ready")), s)
		wantRejection(t, err, CodePreconditionUnmet, "dec-1", "wo-con", "Konzeptstand")
	}
}

// ---- Feste Kennung ohne Konzeptbezug ------------------------------------------------------------------------------

func TestReviewOfAnOrderWithoutConceptReferenceCarriesTheFixedMarker(t *testing.T) {
	e := newEnv(t)
	t.Run("die feste Kennung ist zulässig, jede andere nicht", func(t *testing.T) {
		s := baseSnapshot()
		noConcept(t, &s, "wo-review")
		e.mustAccept(batch(codeReviewDecision("dec-1", "wo-review", commitA, NoConceptVersion)), s)
		for _, v := range []string{"ohne-konzept", "1.0", "Kein-Konzept"} {
			_, err := e.check(batch(codeReviewDecision("dec-1", "wo-review", commitA, v)), s)
			wantRejection(t, err, CodePreconditionUnmet, "dec-1", NoConceptVersion, v)
		}
	})
	t.Run("mit Konzeptstand ist die Kennung abgelehnt, die Version gilt", func(t *testing.T) {
		_, err := e.check(batch(codeReviewDecision("dec-1", "wo-review", commitA, NoConceptVersion)), baseSnapshot())
		wantRejection(t, err, CodePreconditionUnmet, "dec-1", "1.0")
		e.mustAccept(batch(codeReviewDecision("dec-1", "wo-review", commitA, "1.0")), baseSnapshot())
	})
	t.Run("Konzeptbezug nur über Abhängigkeit: die Kennung ersetzt den fehlenden Konzeptstand nicht", func(t *testing.T) {
		s := baseSnapshot()
		o := noConcept(t, &s, "wo-review")
		o.DependsOn = []string{"wo-con"}
		_, err := e.check(batch(codeReviewDecision("dec-1", "wo-review", commitA, NoConceptVersion)), s)
		wantRejection(t, err, CodePreconditionUnmet, "dec-1", "wo-con")
		wantRejection(t, err, CodePreconditionUnmet, "dec-1", "Konzeptbezug")
	})
}

func TestIntegrationOfAnOrderWithoutConceptReferenceBindsTheReportToTheFixedMarker(t *testing.T) {
	e := newEnv(t)
	s := baseSnapshot()
	ready := noConcept(t, &s, "wo-ready")
	ready.LastReview.Report.ConceptVersion = NoConceptVersion
	e.mustAccept(batch(integrationDecision("dec-1", "wo-ready")), s)

	for _, v := range []string{"ohne-konzept", "1.0"} { // ein erfundener Wert trägt die Integration nicht
		ready.LastReview.Report.ConceptVersion = v
		_, err := e.check(batch(integrationDecision("dec-1", "wo-ready")), s)
		wantRejection(t, err, CodeIntegrationNotApproved, "dec-1", "review_invalid", NoConceptVersion)
	}
}

func TestTheMarkerIsReservedForOrdersWithoutConcept(t *testing.T) {
	s := baseSnapshot()
	s.order(t, "wo-a").Concept.Version = NoConceptVersion
	s.order(t, "wo-a").Concept.Approval.Version = NoConceptVersion
	probs := s.Problems()
	if len(probs) != 1 || !strings.Contains(probs[0].Message, NoConceptVersion) {
		t.Fatalf("Verstöße = %v", probs)
	}
	if _, err := newEnv(t).check(batch(waitDecision("dec-1", "wo-a")), s); err == nil {
		t.Error("ein Snapshot mit der reservierten Kennung als Konzeptversion ist keine Grundlage")
	}
}

func TestSnapshotNamesTheConceptVersionEveryReviewMustCarry(t *testing.T) {
	s := baseSnapshot()
	noConcept(t, &s, "wo-b")
	dep := noConcept(t, &s, "wo-c") // hängt von wo-a ab, einem Umsetzungsauftrag: kein Konzeptbezug
	dep.DependsOn = []string{"wo-a"}
	viaDep := noConcept(t, &s, "wo-review")
	viaDep.DependsOn = []string{"wo-con"}
	got := map[string]string{}
	for _, o := range s.Canonical().Orders {
		got[o.ID] = o.ReviewConceptVersion
	}
	want := map[string]string{
		"wo-a": "1.0", "wo-b": NoConceptVersion, "wo-c": NoConceptVersion, "wo-ready": "1.0",
		"wo-review": "", // Konzeptbezug ohne Konzeptstand: kein gültiger Wert, fail-safe
		"wo-con":    "2.0", "wo-strat": "3.0",
	}
	for id, v := range want {
		if got[id] != v {
			t.Errorf("%s: ReviewConceptVersion = %q, erwartet %q", id, got[id], v)
		}
	}
	// Abgeleitet: ein vom Befüller gesetzter Wert zählt nicht.
	s2 := baseSnapshot()
	s2.order(t, "wo-a").ReviewConceptVersion = "erfunden"
	for _, o := range s2.Canonical().Orders {
		if o.ID == "wo-a" && o.ReviewConceptVersion != "1.0" {
			t.Errorf("Canonical übernimmt den gesetzten Wert: %q", o.ReviewConceptVersion)
		}
	}
	if s2.order(t, "wo-a").ReviewConceptVersion != "erfunden" {
		t.Error("Canonical verändert die Eingabe")
	}
	// Das Modell sieht den Wert im Planungszustand.
	pc, err := BuildPlanningContext(s, 0)
	if err != nil || !strings.Contains(pc.JSON, `"review_concept_version":"`+NoConceptVersion+`"`) {
		t.Errorf("Planungszustand nennt die Kennung nicht: %v", err)
	}
}

// ---- Pflichtangaben je Beauftragung -------------------------------------------------------------------------------

func TestEveryDelegationNamesExpectedOutcomeAndEstimate(t *testing.T) {
	e := newEnv(t)
	delegations := map[string]func() domain.Decision{
		"concept_work":       func() domain.Decision { return conceptDecision("dec-1", "wo-con") },
		"implementation":     func() domain.Decision { return implDecision("dec-1", "wo-a") },
		"review":             func() domain.Decision { return codeReviewDecision("dec-1", "wo-review", commitA, "1.0") },
		"documentation_work": func() domain.Decision { return docDecision("dec-1", "wo-done") },
	}
	for name, mk := range delegations {
		t.Run(name, func(t *testing.T) {
			e.mustAccept(batch(mk()), baseSnapshot())
			for what, mutate := range map[string]func(d *domain.Decision){
				"ohne erwartetes Ergebnis":      func(d *domain.Decision) { d.ExpectedOutcome = "" },
				"nur Leerraum als Ergebnis":     func(d *domain.Decision) { d.ExpectedOutcome = " \t" },
				"ohne Aufwandsschätzung":        func(d *domain.Decision) { d.Estimate = nil },
				"weder Ergebnis noch Schätzung": func(d *domain.Decision) { d.ExpectedOutcome, d.Estimate = "", nil },
			} {
				d := mk()
				mutate(&d)
				_, err := e.check(batch(d), baseSnapshot())
				r := wantRejection(t, err, CodeDelegationIncomplete, "dec-1", name)
				if !strings.Contains(r.Reason, "erwartete") && !strings.Contains(r.Reason, "Aufwand") {
					t.Errorf("%s: Begründung nennt das fehlende Feld nicht: %s", what, r.Reason)
				}
				onlyCodes(t, err, CodeDelegationIncomplete)
			}
		})
	}
	t.Run("Aktionen ohne Ausführung brauchen beides nicht", func(t *testing.T) {
		e.mustAccept(batch(waitDecision("dec-1", "wo-a"), questionDecision("dec-2", "wo-b")), baseSnapshot())
	})
	t.Run("die Ablehnung nennt, was fehlt", func(t *testing.T) {
		d := implDecision("dec-1", "wo-a")
		d.ExpectedOutcome = ""
		_, err := e.check(batch(d), baseSnapshot())
		wantRejection(t, err, CodeDelegationIncomplete, "dec-1", "erwartete Ergebnis")
		if strings.Contains(err.Error(), "Aufwandsschätzung") {
			t.Errorf("nennt eine Schätzung, die vorhanden ist: %v", err)
		}
	})
}

// TestTheMarkerIsNamedWhereTheModelAndTheReviewerReadIt: Die feste Kennung steht dort, wo Supervisor und Reviewer sie lesen, und
// die Pflichtangaben der Delegation dort, wo sie verlangt werden. Schutz vor Drift zwischen Code, Prompt, Skills und Doku.
func TestTheMarkerIsNamedWhereTheModelAndTheReviewerReadIt(t *testing.T) {
	read := func(parts ...string) string {
		raw, err := os.ReadFile(filepath.Join(append([]string{"..", ".."}, parts...)...))
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	texts := map[string]string{
		"Systemprompt":        prompts.Supervisor(),
		"Skill supervisor":    read(".claude", "skills", "ccmas-supervisor", "SKILL.md"),
		"Skill code-review":   read(".claude", "skills", "ccmas-code-review", "SKILL.md"),
		"Vertrag code-review": read(".claude", "skills", "ccmas-code-review", "contract.yaml"),
		"supervisor.md":       read("docs", "architecture", "supervisor.md"),
	}
	for name, text := range texts {
		if !strings.Contains(text, NoConceptVersion) {
			t.Errorf("%s nennt die Kennung %q nicht", name, NoConceptVersion)
		}
	}
	for _, name := range []string{"Systemprompt", "Skill supervisor", "supervisor.md"} {
		if !strings.Contains(texts[name], "review_concept_version") {
			t.Errorf("%s nennt das Feld review_concept_version nicht", name)
		}
	}
	for _, name := range []string{"Systemprompt", "Skill supervisor", "supervisor.md"} {
		if !strings.Contains(texts[name], "expected_outcome") || !strings.Contains(texts[name], "estimate") {
			t.Errorf("%s nennt die Pflichtangaben expected_outcome und estimate nicht", name)
		}
	}
}

// ---- Integration mit vorhandenem, aber nicht freigegebenem Konzeptstand -----------------------------------------------

// TestIntegrationRequiresAValidConceptApprovalWhenTheOrderCarriesAConceptState: Ein Konzeptstand am Auftrag ist Konzeptbezug.
// Ohne gültige Freigabe wird auch nicht integriert (wie bei implementation und review), selbst wenn Review und Bericht zur Version
// passen.
func TestIntegrationRequiresAValidConceptApprovalWhenTheOrderCarriesAConceptState(t *testing.T) {
	e := newEnv(t)
	e.mustAccept(batch(integrationDecision("dec-1", "wo-ready")), baseSnapshot()) // Grundfall: freigegeben, zulässig

	cases := []struct {
		name     string
		mutate   func(c *ConceptState)
		contains []string
	}{
		{"Freigabe fehlt", func(c *ConceptState) { c.Approval = nil }, []string{"1.0", "nicht freigegeben"}},
		{"Freigabe für eine andere Version", func(c *ConceptState) { c.Approval.Version = "0.9" }, []string{"Konzeptfreigabe", "gilt nicht"}},
		{"strategisches Konzept, vom Konzeptprüfer freigegeben", func(c *ConceptState) {
			c.Class, c.Criteria = skills.ConceptStrategic, []string{"scope_change"}
		}, []string{"Konzeptfreigabe", "gilt nicht", "Nutzer"}},
		{"Einstufung fehlt, vom Konzeptprüfer freigegeben", func(c *ConceptState) { c.Class, c.Criteria = "", []string{} }, []string{"Konzeptfreigabe", "gilt nicht"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := baseSnapshot()
			o := s.order(t, "wo-ready")
			c.mutate(o.Concept)
			// Bericht und Prüfziel bleiben an die (unveränderte) Version gebunden: Review und Version passen, nur die Freigabe fehlt.
			vb, err := e.check(batch(integrationDecision("dec-1", "wo-ready")), s)
			wantRejection(t, err, CodePreconditionUnmet, "dec-1", c.contains...)
			if vb.Decisions != nil {
				t.Fatal("ohne gültige Konzeptfreigabe wird nichts integriert")
			}
		})
	}
}
