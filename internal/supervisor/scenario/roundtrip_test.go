package scenario_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/alexwenzel/ccmas/internal/domain"
	"github.com/alexwenzel/ccmas/internal/supervisor"
	"github.com/alexwenzel/ccmas/internal/supervisor/scenario"
	"github.com/alexwenzel/ccmas/internal/workers/runner"
)

// Test der Belegkette ohne Konto: Dieselbe Kette wie im Live-Test (runScenario, Belegformat, Schreiben, Redigierung) mit einer
// Fake-Runtime, die vorbereitete Pläne liefert; danach prüft derselbe Offline-Prüfer die geschriebenen Belege. Das ist kein Beleg
// für die Planung (die Pläne sind handgeschrieben), sondern für die Kette, damit ein echter Lauf keine Sitzung an einen Fehler im
// Beleg verliert, und für die Prüfung selbst: ein veralteter oder verfälschter Beleg muss auffallen.

// scriptedRuntime liefert je Aufruf die nächste Ausgabe (Fake-Runtime, nur hier).
type scriptedRuntime struct {
	mu      sync.Mutex
	outputs []string
	calls   int
}

func (s *scriptedRuntime) Execute(_ context.Context, req runner.Request) (runner.Execution, error) {
	s.mu.Lock()
	out := s.outputs[min(s.calls, len(s.outputs)-1)]
	s.calls++
	n := s.calls
	s.mu.Unlock()
	return runner.Execution{
		ProjectID: req.ProjectID, ExecutionID: req.ExecutionID, Attempt: 1, Profile: req.Profile,
		SessionStarted: &domain.SessionStarted{SessionID: "sess-fake-" + string(rune('a'+n)), Model: req.Profile.Model, Effort: req.Profile.Effort,
			ClaudeVersion: "2.1.311", SDKVersion: "0.2.180", SkillSnapshotID: req.SkillSnapshot.SnapshotID, Skills: req.Profile.SkillIDs},
		Events: []domain.RunEvent{{Payload: domain.Result{Outcome: domain.OutcomeSucceeded, Summary: "fertig", StructuredOutput: json.RawMessage(out)}}},
		State:  domain.ExecSucceeded, Terminal: &domain.TerminalResult{Attempt: 1, State: domain.ExecSucceeded, Source: "result"},
	}, nil
}

func (e *evEnv) sel(role domain.Role) *domain.Selection {
	for _, o := range e.offers.Roles {
		if o.Role == role {
			return &domain.Selection{Role: role, SkillIDs: []string{o.DefaultSkillIDs[0]}, Model: o.Model, Effort: o.Effort}
		}
	}
	panic("keine Rolle " + string(role))
}

func (e *evEnv) decision(id string, action domain.Action, order string, role domain.Role, pre ...domain.Precondition) domain.Decision {
	d := domain.Decision{
		SchemaVersion: 1, DecisionID: id, ProjectID: scenario.ProjectID, StateRevision: scenario.Revision,
		Target: domain.DecisionTarget{Kind: domain.KindWorkOrder, ID: order}, Action: action,
		Preconditions: append([]domain.Precondition{}, pre...), Rationale: "Begründung des Beispielplans.",
	}
	if role != "" {
		d.Selection = e.sel(role)
		d.ExpectedOutcome = "Überprüfbares Ergebnis."
		d.Estimate = &domain.Estimate{Size: domain.SizeM, Complexity: domain.LevelMedium, Uncertainty: domain.LevelLow}
	}
	return d
}

// scriptedPlans sind gute Pläne für alle Szenarien (handgeschrieben).
func (e *evEnv) scriptedPlans() map[scenario.ID]domain.DecisionBatch {
	approved := domain.Precondition{Kind: domain.PrecConceptApproved, Ref: "1.0"}
	b := func(ds ...domain.Decision) domain.DecisionBatch {
		return domain.DecisionBatch{SchemaVersion: 1, BatchID: "x", ProjectID: scenario.ProjectID, StateRevision: scenario.Revision,
			Goal: domain.DecisionTarget{Kind: domain.KindMilestone, ID: scenario.GoalID}, Rationale: "Plan.", Decisions: ds}
	}
	rev := e.decision("dec-1", domain.ActionReview, scenario.S3Fin, domain.RoleReviewer, domain.Precondition{Kind: domain.PrecWorkOrderCompleted})
	rev.ReviewTarget = &domain.ReviewTarget{WorkOrderID: scenario.S3Fin, Commit: scenario.Commit, ConceptVersion: "1.0"}
	ask := e.decision("dec-1", domain.ActionAskQuestion, scenario.S2ConStrat, "")
	ask.Question = &domain.DecisionQuestion{Text: "Soll der Umfang erweitert werden?", Context: "Strategische Entscheidung.", Recommendation: "Nein."}
	// S7: strengeres Profil für den kritischen Auftrag (Effort eine Stufe über dem Standard), Standard für den trivialen.
	critical := e.decision("dec-1", domain.ActionImplementation, scenario.S7A, domain.RoleImplementer, domain.Precondition{Kind: domain.PrecConceptApproved, Ref: "2.0"})
	critical.Selection.Effort = domain.EffortXHigh
	return map[scenario.ID]domain.DecisionBatch{
		scenario.S7: b(critical, e.decision("dec-2", domain.ActionImplementation, scenario.S7B, domain.RoleImplementer)),
		scenario.S1: b(e.decision("dec-1", domain.ActionImplementation, scenario.S1A, domain.RoleImplementer, approved),
			e.decision("dec-2", domain.ActionImplementation, scenario.S1B, domain.RoleImplementer, approved),
			e.decision("dec-3", domain.ActionWait, scenario.S1C, "", domain.Precondition{Kind: domain.PrecWorkOrderCompleted, Ref: scenario.S1A})),
		scenario.S2Routine:   b(e.decision("dec-1", domain.ActionConceptWork, scenario.S2ConRoutine, domain.RoleConcept)),
		scenario.S2Strategic: b(ask),
		scenario.S3:          b(rev),
		scenario.S4:          b(e.decision("dec-1", domain.ActionImplementation, scenario.S4A, domain.RoleImplementer, approved)),
		scenario.S5:          b(e.decision("dec-1", domain.ActionWait, scenario.S5A, "")),
		scenario.S6:          b(e.decision("dec-1", domain.ActionRequestIntegration, scenario.S6A, "", domain.Precondition{Kind: domain.PrecReviewApproved})),
	}
}

func TestEvidenceChainWritesEvidenceTheOfflineCheckAccepts(t *testing.T) {
	e := newEvEnv(t)
	dir := t.TempDir()
	plans := e.scriptedPlans()
	used := 0
	for _, id := range scenario.IDs() {
		raw, _ := json.Marshal(plans[id])
		rt := &scriptedRuntime{outputs: []string{string(raw)}}
		runScenario(t, e, id, "claude-fake", dir, evidenceFile(dir, id), "", &used, rt)
	}
	if used != len(scenario.IDs())+1 { // S6: ein Versuch mehr (stale_revision, dann Neuplanung)
		t.Errorf("%d Sitzungen gezählt, erwartet %d", used, len(scenario.IDs())+1)
	}
	rec := &recorder{}
	guard(func() { checkEvidenceDir(rec, e, dir) })
	if len(rec.problems) != 0 {
		t.Fatalf("Prüfer beanstandet die geschriebenen Belege: %v", rec.problems)
	}

	s6, err := loadEvidence(evidenceFile(dir, scenario.S6))
	if err != nil {
		t.Fatal(err)
	}
	r := s6.Runs[0]
	if r.Outcome != "accepted" || len(r.Attempts) != 2 || !slices.Contains(r.Attempts[0].Validation.Codes, supervisor.CodeStaleRevision) || !r.Attempts[0].Stale ||
		len(r.Attempts[0].ValidationSnapshot) == 0 || r.Attempts[1].SnapshotRevision != scenario.Revision+1 {
		t.Errorf("S6 = %+v", r)
	}
	for _, id := range scenario.IDs() {
		ev, _ := loadEvidence(evidenceFile(dir, id))
		if ev.Final.PredicatesMet == nil || !*ev.Final.PredicatesMet {
			t.Errorf("%s: Prädikate nicht erfüllt: %+v", id, ev.Runs[0].Predicates)
		}
	}

	// Verfälschungen und veraltete Eingaben fallen auf.
	tamper := func(name string, change func(ev *scenarioEvidence), want string) {
		t.Helper()
		d2 := t.TempDir()
		for _, id := range scenario.IDs() {
			raw, _ := os.ReadFile(evidenceFile(dir, id))
			if err := os.WriteFile(evidenceFile(d2, id), raw, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		path := evidenceFile(d2, scenario.S1)
		ev, _ := loadEvidence(path)
		change(&ev)
		raw, err := marshalEvidence(ev) // ohne Redigierungsprüfung: der Prüfer selbst soll es finden
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, raw, 0o644); err != nil {
			t.Fatal(err)
		}
		rec := &recorder{}
		guard(func() { checkEvidenceDir(rec, e, d2) })
		if !slices.ContainsFunc(rec.problems, func(p string) bool { return strings.Contains(p, want) }) {
			t.Errorf("%s: erwartete Meldung %q fehlt: %v", name, want, rec.problems)
		}
	}
	tamper("Policy-Eingabe geändert", func(ev *scenarioEvidence) { ev.PolicyInputs["rules/roles.yaml"] = strings.Repeat("0", 64) }, "Beleg veraltet: Policy-Eingabe rules/roles.yaml")
	tamper("Urteil verfälscht", func(ev *scenarioEvidence) {
		a := &ev.Runs[0].Attempts[0]
		a.Verdict, a.Validation.Accepted = "rejected", false
		a.Validation.Codes = []supervisor.Code{supervisor.CodeCapacityExceeded}
		a.Validation.Rejections = []supervisor.Rejection{{Code: supervisor.CodeCapacityExceeded, DecisionID: "dec-1", Reason: "x"}}
		ev.Runs[0].Outcome = "suspended"
	}, "Validator urteilt")
	tamper("Snapshot verändert", func(ev *scenarioEvidence) {
		ev.Runs[0].Attempts[0].Snapshot = []byte(strings.Replace(string(ev.Runs[0].Attempts[0].Snapshot), "sc1-b", "sc1-x", 1))
	}, "Hash des Snapshots")
	tamper("Plan verändert", func(ev *scenarioEvidence) {
		ev.Runs[0].AcceptedBatch.Decisions = ev.Runs[0].AcceptedBatch.Decisions[:1]
		ev.Runs[0].Attempts[0].StampedBatch.Decisions = ev.Runs[0].Attempts[0].StampedBatch.Decisions[:1]
	}, "Prädikat")
	// Rohe Ausgabe und gestempelter Batch gehören zusammen: Eine von Hand geänderte Auswahl fällt auf, auch wenn der angenommene
	// Batch gleich mitgeändert wurde und der Validator die geänderte Auswahl annimmt (Effort xhigh liegt im Angebot).
	const rawMismatch = "der gestempelte Batch ist nicht die rohe Ausgabe des Modells"
	tamper("Auswahl nur im gestempelten Batch", func(ev *scenarioEvidence) {
		ev.Runs[0].Attempts[0].StampedBatch.Decisions[0].Selection.Effort = domain.EffortXHigh
	}, rawMismatch)
	tamper("Auswahl im gestempelten und im angenommenen Batch, rohe Ausgabe unverändert", func(ev *scenarioEvidence) {
		ev.Runs[0].Attempts[0].StampedBatch.Decisions[0].Selection.Effort = domain.EffortXHigh
		ev.Runs[0].AcceptedBatch.Decisions[0].Selection.Effort = domain.EffortXHigh
	}, rawMismatch)
	tamper("Auswahl nur im angenommenen Batch", func(ev *scenarioEvidence) {
		ev.Runs[0].AcceptedBatch.Decisions[0].Selection.Effort = domain.EffortXHigh
	}, "der angenommene Batch ist nicht der gestempelte")
	tamper("rohe Ausgabe verändert, Batches unverändert", func(ev *scenarioEvidence) {
		ev.Runs[0].Attempts[0].RawOutput = []byte(strings.Replace(string(ev.Runs[0].Attempts[0].RawOutput), "Begründung des Beispielplans.", "Andere Begründung.", 1))
	}, rawMismatch)
	tamper("Batch-ID im gestempelten Batch verändert", func(ev *scenarioEvidence) {
		ev.Runs[0].Attempts[0].StampedBatch.BatchID = "plan-r1-v9"
	}, rawMismatch)
	tamper("rohe Ausgabe fehlt", func(ev *scenarioEvidence) { ev.Runs[0].Attempts[0].RawOutput = nil }, "keine rohe strukturierte Ausgabe")
	tamper("Szenario nicht mehr angenommen", func(ev *scenarioEvidence) {
		ev.Runs[0].Outcome, ev.Final.Outcome = "suspended", "suspended"
	}, "Der Nachweis M02 gilt nicht mehr")
	tamper("Prädikat nicht erfüllt", func(ev *scenarioEvidence) {
		no := false
		ev.Runs[0].PredicatesMet, ev.Final.PredicatesMet = &no, &no
	}, "Der Nachweis M02 gilt nicht mehr")
	tamper("Pfad im Beleg", func(ev *scenarioEvidence) { ev.Invocation = "/Users/jemand/x" }, "nicht redigiert")

	// Der Schreiber selbst weist Pfade ab.
	if ev, _ := loadEvidence(evidenceFile(dir, scenario.S1)); true {
		ev.Invocation = "/Users/jemand/x"
		if err := writeEvidenceFile(filepath.Join(t.TempDir(), "x.json"), ev); err == nil {
			t.Error("der Schreiber nimmt einen Pfad im Beleg an")
		}
	}

	// Ein Beleg-Verzeichnis ohne Szenario fällt ebenfalls auf.
	rec = &recorder{}
	guard(func() { checkEvidenceDir(rec, e, t.TempDir()) })
	if len(rec.problems) != len(scenario.IDs()) {
		t.Errorf("leeres Verzeichnis: %v", rec.problems)
	}
}

// TestRepeatIsBoundToAnInfrastructureFailure: eine Wiederholung ist nur nach genau einem Lauf zulässig, der an der Infrastruktur
// scheiterte; ein angenommener Plan, ein inhaltlicher Fehlschlag oder eine zweite Wiederholung sind es nicht.
func TestRepeatIsBoundToAnInfrastructureFailure(t *testing.T) {
	run := func(outcome, reason string) runEvidence {
		return runEvidence{Run: 1, Outcome: outcome, SuspendReason: reason}
	}
	cases := []struct {
		name string
		runs []runEvidence
		ok   bool
	}{
		{"Prozessfehler", []runEvidence{run("suspended", "supervisor_unavailable")}, true},
		{"Abo-Limit", []runEvidence{run("suspended", "subscription_limit")}, true},
		{"angenommener Plan", []runEvidence{run("accepted", "")}, false},
		{"Neuplanungsbudget erschöpft (inhaltlich)", []runEvidence{run("suspended", "replan_budget_exhausted")}, false},
		{"Kontext zu groß", []runEvidence{run("suspended", "context_too_large")}, false},
		{"schon eine Wiederholung", []runEvidence{run("suspended", "supervisor_unavailable"), run("suspended", "supervisor_unavailable")}, false},
		{"kein Lauf", nil, false},
	}
	for _, c := range cases {
		if err := repeatAllowed(scenarioEvidence{Runs: c.runs}); (err == nil) != c.ok {
			t.Errorf("%s: Wiederholung zulässig = %v, Fehler: %v", c.name, err == nil, err)
		}
	}
}

// TestCommittedEvidenceRejectsHandEditedSelection: derselbe Eingriff an den eingecheckten Belegen (nicht an den geschriebenen der
// Fake-Runtime). Die Entscheidung dec-1 in S1 bekommt im gestempelten und im angenommenen Batch einen anderen Effort, die rohe
// Ausgabe bleibt; der Validator nimmt den Batch weiterhin an, die Offline-Prüfung muss ihn trotzdem ablehnen. Der unveränderte
// Beleg besteht (Gegenprobe).
func TestCommittedEvidenceRejectsHandEditedSelection(t *testing.T) {
	e := newEvEnv(t)
	dir := t.TempDir()
	for _, id := range scenario.IDs() {
		raw, err := os.ReadFile(evidenceFile(evidenceDir, id))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(evidenceFile(dir, id), raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	rec := &recorder{}
	guard(func() { checkEvidenceDir(rec, e, dir) })
	if len(rec.problems) != 0 {
		t.Fatalf("die eingecheckten Belege bestehen die Prüfung nicht: %v", rec.problems)
	}

	path := evidenceFile(dir, scenario.S1)
	ev, err := loadEvidence(path)
	if err != nil {
		t.Fatal(err)
	}
	was := ev.Runs[0].Attempts[0].StampedBatch.Decisions[0].Selection.Effort
	if was == domain.EffortXHigh {
		t.Fatalf("Effort schon %s, die Probe ändert nichts", was)
	}
	ev.Runs[0].Attempts[0].StampedBatch.Decisions[0].Selection.Effort = domain.EffortXHigh
	ev.Runs[0].AcceptedBatch.Decisions[0].Selection.Effort = domain.EffortXHigh
	raw, err := marshalEvidence(ev)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	rec = &recorder{}
	guard(func() { checkEvidenceDir(rec, e, dir) })
	if !slices.ContainsFunc(rec.problems, func(p string) bool {
		return strings.Contains(p, "S1: ") && strings.Contains(p, "der gestempelte Batch ist nicht die rohe Ausgabe des Modells")
	}) {
		t.Fatalf("der verfälschte Beleg wird nicht erkannt: %v", rec.problems)
	}
	t.Logf("erkannt: %s", rec.problems[0])
}
