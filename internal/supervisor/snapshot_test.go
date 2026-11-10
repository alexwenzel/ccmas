package supervisor

import (
	"bytes"
	"encoding/json"
	"math/rand"
	"slices"
	"strings"
	"testing"

	"github.com/alexwenzel/ccmas/internal/domain"
	"github.com/alexwenzel/ccmas/internal/skills"
)

// fullSnapshot trägt jede Angabe, die der Ticketauftrag nennt: Projekt, Revision, Ziel mit Kriterien und Umfang,
// Modus, Kapazität mit aktiven Ausführungen, Aufträge mit Art, Zustand, Abhängigkeiten, Konzeptstand, letztem
// Review, PR-Stand, Reparaturrunden und aktiver Ausführung, offene Fragen sowie Rollenprofile und Skills.
func fullSnapshot() ProjectSnapshot {
	s := baseSnapshot()
	s.Goal.Evidence = &Evidence{Integration: skills.IntegrationPROpen, AcceptanceCriteriaMet: true}
	s.activate(domain.RoleSupervisor, "exec-sup", "")
	s.activate(domain.RoleReviewer, "exec-rev-2", "wo-review")
	o := s.order(nil, "wo-ready")
	o.PR = &PRInfo{Number: 12, State: PROpen, CI: CIPassed}
	o.CodeRepairRounds, o.ConceptRepairRounds = 1, 2
	o.LastResult = &LastResult{Role: domain.RoleReviewer, Result: "approved"}
	r := s.order(nil, "wo-review")
	r.ActiveExecutionID = "exec-rev-2"
	s.OpenQuestions = []OpenQuestion{{QuestionID: "q-1", WorkOrderID: "wo-a", State: domain.QuestionOpen, Text: "Welche Variante?"}}
	s.Roles = []RoleOffer{{
		Role: domain.RoleImplementer, AgentID: "fast", Model: mMid, Effort: domain.EffortHigh, Policy: "scoped_write",
		AllowedModels: []string{mMid, mBig}, AllowedEfforts: []domain.Effort{domain.EffortLow, domain.EffortHigh},
		SkillIDs: []string{skImpl}, DefaultSkillIDs: []string{skImpl},
	}}
	s.Skills = []SkillOffer{{ID: skImpl, Category: "delivery", Description: "Beschreibung", Roles: []domain.Role{domain.RoleImplementer}}}
	return s
}

func TestSnapshotCarriesAllRequiredInformation(t *testing.T) {
	raw, err := json.Marshal(fullSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	has := func(obj map[string]any, keys ...string) {
		t.Helper()
		for _, k := range keys {
			if _, ok := obj[k]; !ok {
				t.Errorf("Feld %q fehlt in %v", k, obj)
			}
		}
	}
	has(m, "schema_version", "project_id", "state_revision", "goal", "integration_mode", "capacity", "orders", "open_questions", "roles", "skills")
	goal := m["goal"].(map[string]any)
	has(goal, "kind", "id", "acceptance_criteria", "scope", "out_of_scope", "evidence")
	capacity := m["capacity"].(map[string]any)
	has(capacity, "max_workers", "active")
	has(capacity["active"].([]any)[0].(map[string]any), "execution_id", "role", "state")

	byID := map[string]map[string]any{}
	for _, o := range m["orders"].([]any) {
		om := o.(map[string]any)
		has(om, "id", "kind", "state", "mode", "depends_on", "code_repair_rounds", "concept_repair_rounds")
		byID[om["id"].(string)] = om
	}
	concept := byID["wo-a"]["concept"].(map[string]any)
	has(concept, "version", "class", "criteria", "approval")
	has(concept["approval"].(map[string]any), "version", "approver")
	review := byID["wo-ready"]["last_review"].(map[string]any)
	has(review, "report", "reviewer_role", "implementer_execution_id")
	has(review["report"].(map[string]any), "report_id", "verdict", "commit", "concept_version", "reviewer_execution_id")
	has(byID["wo-ready"], "pr", "last_result", "head_commit", "branch")
	has(byID["wo-ready"]["pr"].(map[string]any), "number", "state", "ci")
	has(byID["wo-review"], "active_execution_id")
	has(m["open_questions"].([]any)[0].(map[string]any), "question_id", "work_order_id", "state")
	has(m["roles"].([]any)[0].(map[string]any), "role", "agent_id", "model", "allowed_models", "allowed_efforts", "skill_ids")
	has(m["skills"].([]any)[0].(map[string]any), "id", "category", "roles")
}

func TestSnapshotStrictDecoding(t *testing.T) {
	raw, err := json.Marshal(fullSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeSnapshot(raw)
	if err != nil {
		t.Fatal(err)
	}
	again, _ := json.Marshal(got)
	if !bytes.Equal(raw, again) {
		t.Fatalf("Round-Trip verändert den Snapshot:\n%s\n%s", raw, again)
	}
	if problems := got.Problems(); len(problems) != 0 {
		t.Fatalf("Round-Trip-Snapshot ungültig: %v", problems)
	}

	withExtra := strings.Replace(string(raw), `{"schema_version"`, `{"unbekannt":1,"schema_version"`, 1)
	nested := strings.Replace(string(raw), `"max_workers":3`, `"max_workers":3,"extra":true`, 1)
	for name, in := range map[string]string{
		"unbekanntes Feld":            withExtra,
		"unbekanntes verschachteltes": nested,
		"Folgedaten":                  string(raw) + `{}`,
		"kein Objekt":                 `[]`,
		"leer":                        ``,
		"falscher Typ":                strings.Replace(string(raw), `"state_revision":7`, `"state_revision":"sieben"`, 1),
	} {
		if _, err := DecodeSnapshot([]byte(in)); err == nil {
			t.Errorf("%s: nicht abgewiesen", name)
		}
	}
}

func TestSnapshotSerializationIsDeterministic(t *testing.T) {
	want, err := json.Marshal(fullSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(want, []byte("null")) {
		t.Fatalf("Snapshot enthält null:\n%s", want)
	}
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 20; i++ {
		s := fullSnapshot()
		rng.Shuffle(len(s.Orders), func(a, b int) { s.Orders[a], s.Orders[b] = s.Orders[b], s.Orders[a] })
		rng.Shuffle(len(s.Capacity.Active), func(a, b int) {
			s.Capacity.Active[a], s.Capacity.Active[b] = s.Capacity.Active[b], s.Capacity.Active[a]
		})
		for j := range s.Orders {
			d := s.Orders[j].DependsOn
			rng.Shuffle(len(d), func(a, b int) { d[a], d[b] = d[b], d[a] })
		}
		rng.Shuffle(len(s.Goal.Scope), func(a, b int) { s.Goal.Scope[a], s.Goal.Scope[b] = s.Goal.Scope[b], s.Goal.Scope[a] })
		got, err := json.Marshal(s)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("Lauf %d: Serialisierung hängt von der Reihenfolge ab", i)
		}
	}

	// nil-Listen werden zu leeren Listen, nicht zu null.
	empty := ProjectSnapshot{SchemaVersion: 1, ProjectID: "p", Goal: Goal{Kind: domain.KindTicket, ID: "t"}}
	raw, err := json.Marshal(empty)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("null")) {
		t.Fatalf("nil-Liste als null ausgegeben: %s", raw)
	}

	// Die Sortierung arbeitet auf einer Kopie.
	s := fullSnapshot()
	s.Orders[0], s.Orders[len(s.Orders)-1] = s.Orders[len(s.Orders)-1], s.Orders[0]
	first := s.Orders[0].ID
	if _, err := json.Marshal(s); err != nil {
		t.Fatal(err)
	}
	if s.Orders[0].ID != first {
		t.Error("Marshal sortiert den Snapshot des Aufrufers um")
	}
}

func TestSnapshotProblems(t *testing.T) {
	if p := baseSnapshot().Problems(); len(p) != 0 {
		t.Fatalf("Grundsnapshot ungültig: %v", p)
	}
	if err := baseSnapshot().Check(); err != nil {
		t.Fatal(err)
	}
	cases := map[string]struct {
		mutate func(s *ProjectSnapshot)
		path   string
	}{
		"Schema-Version":       {func(s *ProjectSnapshot) { s.SchemaVersion = 0 }, "schema_version"},
		"Projekt-ID":           {func(s *ProjectSnapshot) { s.ProjectID = "a b" }, "project_id"},
		"Revision negativ":     {func(s *ProjectSnapshot) { s.StateRevision = -1 }, "state_revision"},
		"Modus":                {func(s *ProjectSnapshot) { s.IntegrationMode = "" }, "integration_mode"},
		"Zielart":              {func(s *ProjectSnapshot) { s.Goal.Kind = domain.KindWorkOrder }, "goal.kind"},
		"Ziel-ID":              {func(s *ProjectSnapshot) { s.Goal.ID = "" }, "goal.id"},
		"ID im Umfang doppelt": {func(s *ProjectSnapshot) { s.Goal.OutOfScope = []string{"tk-1"} }, "goal.out_of_scope[0]"},
		"Integrationsstand":    {func(s *ProjectSnapshot) { s.Goal.Evidence = &Evidence{Integration: "halb"} }, "goal.evidence.integration"},
		"max_workers zu groß":  {func(s *ProjectSnapshot) { s.Capacity.MaxWorkers = 17 }, "capacity.max_workers"},
		"aktive Ausführung doppelt": {func(s *ProjectSnapshot) {
			s.activate(domain.RoleConcept, "e", "")
			s.activate(domain.RoleConcept, "e", "")
		}, "capacity.active[1]"},
		"aktive Ausführung beendet": {func(s *ProjectSnapshot) {
			s.Capacity.Active = []ActiveExecution{{ExecutionID: "e", Role: domain.RoleConcept, State: domain.ExecCrashed}}
		}, "capacity.active[0].state"},
		"aktive Ausführung unbekannte Rolle": {func(s *ProjectSnapshot) {
			s.Capacity.Active = []ActiveExecution{{ExecutionID: "e", Role: "x", State: domain.ExecRunning}}
		}, "capacity.active[0].role"},
		"Auftrags-ID doppelt":             {func(s *ProjectSnapshot) { s.Orders[1].ID = s.Orders[0].ID }, "orders[1].id"},
		"Auftrags-ID wie Ziel":            {func(s *ProjectSnapshot) { s.Orders[0].ID = "ms-1" }, "orders[0].id"},
		"Auftragsart":                     {func(s *ProjectSnapshot) { s.Orders[0].Kind = "x" }, "orders[0].kind"},
		"Auftragszustand":                 {func(s *ProjectSnapshot) { s.Orders[0].State = "x" }, "orders[0].state"},
		"Auftragsmodus":                   {func(s *ProjectSnapshot) { s.Orders[0].Mode = "" }, "orders[0].mode"},
		"Selbstabhängigkeit":              {func(s *ProjectSnapshot) { s.Orders[0].DependsOn = []string{s.Orders[0].ID} }, "orders[0].depends_on[0]"},
		"unbekannte Abhängigkeit":         {func(s *ProjectSnapshot) { s.Orders[0].DependsOn = []string{"nirgends"} }, "orders[0].depends_on[0]"},
		"Konzeptversion fehlt":            {func(s *ProjectSnapshot) { s.Orders[0].Concept.Version = "" }, "orders[0].concept.version"},
		"Einstufung":                      {func(s *ProjectSnapshot) { s.Orders[0].Concept.Class = "x" }, "orders[0].concept.class"},
		"Freigeber":                       {func(s *ProjectSnapshot) { s.Orders[0].Concept.Approval.Approver = "x" }, "orders[0].concept.approval.approver"},
		"Kopf-Commit":                     {func(s *ProjectSnapshot) { s.Orders[0].HeadCommit = "abc" }, "orders[0].head_commit"},
		"PR-Nummer":                       {func(s *ProjectSnapshot) { s.Orders[0].PR = &PRInfo{Number: 0, State: PROpen, CI: CIPassed} }, "orders[0].pr.number"},
		"PR-Stand":                        {func(s *ProjectSnapshot) { s.Orders[0].PR = &PRInfo{Number: 1, State: "x", CI: CIPassed} }, "orders[0].pr.state"},
		"CI-Stand":                        {func(s *ProjectSnapshot) { s.Orders[0].PR = &PRInfo{Number: 1, State: PROpen, CI: "x"} }, "orders[0].pr.ci"},
		"Reparaturrunden negativ":         {func(s *ProjectSnapshot) { s.Orders[0].CodeRepairRounds = -1 }, "orders[0]"},
		"aktive Ausführung nicht geführt": {func(s *ProjectSnapshot) { s.Orders[0].ActiveExecutionID = "e-nirgends" }, "orders[0].active_execution_id"},
		"letztes Ergebnis":                {func(s *ProjectSnapshot) { s.Orders[0].LastResult = &LastResult{Role: "x", Result: "y"} }, "orders[0].last_result"},
		"Frage doppelt": {func(s *ProjectSnapshot) {
			s.OpenQuestions = []OpenQuestion{{QuestionID: "q", State: domain.QuestionOpen}, {QuestionID: "q", State: domain.QuestionOpen}}
		}, "open_questions[1]"},
		"Frage zu unbekanntem Auftrag": {func(s *ProjectSnapshot) {
			s.OpenQuestions = []OpenQuestion{{QuestionID: "q", WorkOrderID: "nirgends", State: domain.QuestionOpen}}
		}, "open_questions[0].work_order_id"},
		"Fragezustand":  {func(s *ProjectSnapshot) { s.OpenQuestions = []OpenQuestion{{QuestionID: "q", State: "x"}} }, "open_questions[0].state"},
		"Rollenangebot": {func(s *ProjectSnapshot) { s.Roles = []RoleOffer{{Role: "x"}} }, "roles[0].role"},
		"Skill-Angebot": {func(s *ProjectSnapshot) { s.Skills = []SkillOffer{{ID: ""}} }, "skills[0].id"},
		"zwei Agenten derselben Rolle": {func(s *ProjectSnapshot) {
			s.Roles = []RoleOffer{{Role: domain.RoleImplementer, AgentID: "a"}, {Role: domain.RoleImplementer, AgentID: "b"}}
		}, "roles[1].role"},
		"Rollenstandard und Agent derselben Rolle": {func(s *ProjectSnapshot) {
			s.Roles = []RoleOffer{{Role: domain.RoleReviewer}, {Role: domain.RoleReviewer, AgentID: "r"}}
		}, "roles[1].role"},
		"Bericht ohne Urteil": {func(s *ProjectSnapshot) {
			s.order(t, "wo-ready").LastReview.Report.Verdict = "x"
		}, "orders[6].last_review.report"},
		"Bericht eines anderen Auftrags": {func(s *ProjectSnapshot) {
			s.order(t, "wo-ready").LastReview.Report.WorkOrderID = "wo-a"
		}, "orders[6].last_review.report.work_order_id"},
		"Reviewer-Rolle unbekannt": {func(s *ProjectSnapshot) { s.order(t, "wo-ready").LastReview.ReviewerRole = "x" }, "orders[6].last_review.reviewer_role"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			s := baseSnapshot()
			c.mutate(&s)
			problems := s.Problems()
			if !slices.ContainsFunc(problems, func(p SnapshotProblem) bool { return p.Path == c.path }) {
				t.Fatalf("kein Befund bei %q in %v", c.path, problems)
			}
			if s.Check() == nil {
				t.Fatal("Check meldet keinen Fehler")
			}
		})
	}
	// Alle Verstöße zusammen.
	s := baseSnapshot()
	s.ProjectID, s.Capacity.MaxWorkers = "", 0
	if n := len(s.Problems()); n < 2 {
		t.Errorf("nur %d Verstöße gesammelt", n)
	}
}
