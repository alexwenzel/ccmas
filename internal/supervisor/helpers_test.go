package supervisor

import (
	"strings"
	"testing"
	"time"

	"github.com/alexwenzel/ccmas/internal/domain"
	"github.com/alexwenzel/ccmas/internal/profiles"
	"github.com/alexwenzel/ccmas/internal/skills"
	"github.com/alexwenzel/ccmas/internal/skills/skilltest"
)

// Erfundene Modell-IDs: produktive IDs stehen nur in config/*.example.yaml.
const (
	mBig = "model-big" // alle fünf Stufen
	mMid = "model-mid" // low, medium, high; xhigh ist als high genehmigt
)

// Test-Skills aus skilltest.DefaultSkills.
const (
	skImpl   = "ccmas-t-impl"
	skReview = "ccmas-t-review"
	skKnow   = "ccmas-t-know"
	skDoc    = "ccmas-t-doc"
	skPlan   = "ccmas-t-plan" // pending_adaptation
)

const (
	commitA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	commitB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func testCaps() profiles.Capabilities {
	return profiles.Capabilities{
		Revision: 1, Source: "Testquelle", CheckedOn: "2026-11-10",
		Models: map[string][]domain.Effort{
			mBig: {domain.EffortLow, domain.EffortMedium, domain.EffortHigh, domain.EffortXHigh, domain.EffortMax},
			mMid: {domain.EffortLow, domain.EffortMedium, domain.EffortHigh},
		},
		Fallbacks: []profiles.ApprovedFallback{
			{Model: mMid, Requested: domain.EffortXHigh, Effective: domain.EffortHigh, Source: "Testdoku: xhigh läuft als high"},
		},
		Policies: []string{"readonly", "scoped_write"},
	}
}

// testConfig: alle Rollen model-big/high; Implementierung model-mid (nur mid oder big); Reviewer nur model-big.
func testConfig() profiles.Config {
	cfg := profiles.Config{Revision: 1, Roles: map[domain.Role]profiles.RoleProfile{}, Agents: map[string]profiles.Agent{}}
	for _, r := range domain.Roles() {
		cfg.Roles[r] = profiles.RoleProfile{Runtime: domain.RuntimeClaude, Model: mBig, Effort: domain.EffortHigh, PolicyProfile: "readonly"}
	}
	impl := cfg.Roles[domain.RoleImplementer]
	impl.Model, impl.PolicyProfile, impl.AllowedModels = mMid, "scoped_write", []string{mMid, mBig}
	cfg.Roles[domain.RoleImplementer] = impl
	for _, r := range []domain.Role{domain.RoleReviewer, domain.RoleConceptReviewer} {
		rp := cfg.Roles[r]
		rp.AllowedModels = []string{mBig}
		cfg.Roles[r] = rp
	}
	return cfg
}

type testEnv struct {
	t     *testing.T
	root  string
	cat   *skills.Catalog
	rules *skills.Rules
	caps  profiles.Capabilities
	cfg   profiles.Config
	pol   Policy
}

// newEnv lädt einen gültigen synthetischen Softwareordner über den produktiven Weg (LoadChecked).
func newEnv(t *testing.T) *testEnv {
	t.Helper()
	root := skilltest.NewRoot(t)
	cat, rules, err := skills.LoadChecked(root)
	if err != nil {
		t.Fatalf("LoadChecked: %v", err)
	}
	e := &testEnv{t: t, root: root, cat: cat, rules: rules, caps: testCaps(), cfg: testConfig()}
	e.pol = e.policy(e.cfg)
	return e
}

func (e *testEnv) policy(cfg profiles.Config) Policy {
	e.t.Helper()
	res, err := profiles.NewResolver(cfg, e.cat)
	if err != nil {
		e.t.Fatal(err)
	}
	pol, err := NewPolicy(res, e.caps, e.cat, e.rules)
	if err != nil {
		e.t.Fatal(err)
	}
	return pol
}

func approvedConcept(version string) *ConceptState {
	return &ConceptState{
		Version: version, Class: skills.ConceptRoutine, Criteria: []string{},
		Approval: &ConceptApprovalInfo{Version: version, Approver: skills.ApproverConceptReviewer},
	}
}

func report(order, commit, concept string, verdict domain.Verdict, reviewerExec string) domain.ReviewReport {
	r := domain.ReviewReport{
		ReportID: "rep-" + order, WorkOrderID: order, ReviewerExecutionID: reviewerExec, Commit: commit, ConceptVersion: concept,
		Verdict: verdict, Findings: []domain.Finding{}, CreatedAt: time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC),
	}
	if verdict == domain.VerdictFindings {
		r.Findings = []domain.Finding{{FindingID: "f-1", Severity: domain.SeverityMajor, Summary: "Befund"}}
	}
	return r
}

// baseSnapshot ist ein gültiger Snapshot mit Aufträgen für alle Fälle. Tests verändern Kopien davon.
//
//	wo-a      Umsetzung, wartet, freigegebenes Konzept 1.0, unabhängig
//	wo-b      Umsetzung, wartet, freigegebenes Konzept 1.0, unabhängig
//	wo-c      Umsetzung, wartet, hängt von wo-a ab
//	wo-con    Konzeptarbeit, wartet, Konzept 2.0 nicht freigegeben (Routine)
//	wo-strat  Konzeptarbeit, wartet, Konzept 3.0 strategisch, nicht freigegeben
//	wo-review Umsetzung in Validierung, Kopf commitA, noch kein Review
//	wo-ready  Umsetzung im Review-Zustand, Kopf commitB, Review FREIGEGEBEN für commitB
//	wo-block  Umsetzung, blockiert
//	wo-done   Umsetzung, abgeschlossen
func baseSnapshot() ProjectSnapshot {
	order := func(id string, kind OrderKind, state domain.WorkOrderState, deps ...string) Order {
		return Order{ID: id, Kind: kind, State: state, Mode: domain.ModePR, DependsOn: deps}
	}
	impl := func(id string, state domain.WorkOrderState, deps ...string) Order {
		o := order(id, OrderImplementation, state, deps...)
		o.Concept = approvedConcept("1.0")
		return o
	}
	review := impl("wo-review", domain.WOValidation)
	review.HeadCommit, review.Branch = commitA, "feature/CC-1-review"
	ready := impl("wo-ready", domain.WOReview)
	ready.HeadCommit, ready.Branch = commitB, "feature/CC-2-ready"
	ready.LastReview = &ReviewRecord{
		Report:       report("wo-ready", commitB, "1.0", domain.VerdictApproved, "exec-rev"),
		ReviewerRole: domain.RoleReviewer, ImplementerExecutionID: "exec-impl",
	}
	con := order("wo-con", OrderConcept, domain.WOWaiting)
	con.Concept = &ConceptState{Version: "2.0", Class: skills.ConceptRoutine, Criteria: []string{}}
	strat := order("wo-strat", OrderConcept, domain.WOWaiting)
	strat.Concept = &ConceptState{Version: "3.0", Class: skills.ConceptStrategic, Criteria: []string{"scope_change"}}

	return ProjectSnapshot{
		SchemaVersion: SnapshotSchemaVersion, ProjectID: "ccmas", StateRevision: 7,
		Goal: Goal{
			Kind: domain.KindMilestone, ID: "ms-1", Title: "Meilenstein", AcceptanceCriteria: []string{"Alles funktioniert"},
			Scope: []string{"tk-1", "tk-2"}, OutOfScope: []string{"tk-99"},
		},
		IntegrationMode: domain.ModePR,
		Capacity:        Capacity{MaxWorkers: 3, Active: []ActiveExecution{}},
		Orders: []Order{
			impl("wo-a", domain.WOWaiting), impl("wo-b", domain.WOWaiting), impl("wo-c", domain.WOWaiting, "wo-a"),
			con, strat, review, ready, impl("wo-block", domain.WOBlocked), impl("wo-done", domain.WOCompleted),
		},
		OpenQuestions: []OpenQuestion{},
		Roles:         []RoleOffer{},
		Skills:        []SkillOffer{},
	}
}

func (s *ProjectSnapshot) order(t *testing.T, id string) *Order {
	for i := range s.Orders {
		if s.Orders[i].ID == id {
			return &s.Orders[i]
		}
	}
	panic("Auftrag " + id + " nicht im Snapshot") // Fehler im Test, nicht im Produktcode
}

func (s *ProjectSnapshot) activate(role domain.Role, execID, orderID string) {
	s.Capacity.Active = append(s.Capacity.Active, ActiveExecution{ExecutionID: execID, Role: role, WorkOrderID: orderID, State: domain.ExecRunning})
}

// ---- Entscheidungen und Batches ----------------------------------------------------

func target(id string) domain.DecisionTarget {
	return domain.DecisionTarget{Kind: domain.KindWorkOrder, ID: id}
}

func baseDecision(id string, action domain.Action, orderID string) domain.Decision {
	return domain.Decision{
		SchemaVersion: 1, DecisionID: id, ProjectID: "ccmas", StateRevision: 7, Target: target(orderID), Action: action,
		Preconditions: []domain.Precondition{}, Rationale: "Kurze, überprüfbare Begründung.",
	}
}

func selection(role domain.Role, model string, effort domain.Effort, skillIDs ...string) *domain.Selection {
	if skillIDs == nil {
		skillIDs = []string{}
	}
	return &domain.Selection{Role: role, SkillIDs: skillIDs, Model: model, Effort: effort}
}

func implDecision(id, orderID string) domain.Decision {
	d := baseDecision(id, domain.ActionImplementation, orderID)
	d.Preconditions = []domain.Precondition{{Kind: domain.PrecConceptApproved}}
	d.Selection = selection(domain.RoleImplementer, mMid, domain.EffortHigh, skImpl)
	return d
}

func conceptDecision(id, orderID string) domain.Decision {
	d := baseDecision(id, domain.ActionConceptWork, orderID)
	d.Selection = selection(domain.RoleConcept, mBig, domain.EffortHigh, skKnow)
	return d
}

func codeReviewDecision(id, orderID, commit, concept string) domain.Decision {
	d := baseDecision(id, domain.ActionReview, orderID)
	d.Preconditions = []domain.Precondition{{Kind: domain.PrecWorkOrderCompleted}}
	d.Selection = selection(domain.RoleReviewer, mBig, domain.EffortHigh, skReview)
	d.ReviewTarget = &domain.ReviewTarget{WorkOrderID: orderID, Commit: commit, ConceptVersion: concept}
	return d
}

func docDecision(id, orderID string) domain.Decision {
	d := baseDecision(id, domain.ActionDocumentationWork, orderID)
	d.Selection = selection(domain.RoleDocumentation, mMid, domain.EffortMedium, skDoc)
	return d
}

func integrationDecision(id, orderID string) domain.Decision {
	d := baseDecision(id, domain.ActionRequestIntegration, orderID)
	d.Preconditions = []domain.Precondition{{Kind: domain.PrecReviewApproved}}
	return d
}

func waitDecision(id, orderID string) domain.Decision {
	return baseDecision(id, domain.ActionWait, orderID)
}

func questionDecision(id, orderID string) domain.Decision {
	d := baseDecision(id, domain.ActionAskQuestion, orderID)
	d.Question = &domain.DecisionQuestion{Text: "Welche Variante?", Context: "Kontext"}
	return d
}

func planDecision(id string, items ...domain.PlannedWorkOrder) domain.Decision {
	d := baseDecision(id, domain.ActionPlanExtend, "ms-1")
	d.Target = domain.DecisionTarget{Kind: domain.KindMilestone, ID: "ms-1"}
	if items != nil {
		d.PlanExtension = &domain.PlanExtension{WorkOrders: items}
	}
	return d
}

func planned(ref string, deps ...string) domain.PlannedWorkOrder {
	if deps == nil {
		deps = []string{}
	}
	return domain.PlannedWorkOrder{
		Ref: ref, Kind: domain.PlanPartial, Title: "Teilauftrag " + ref, Goal: "Ziel von " + ref,
		AcceptanceCriteria: []string{"erfüllt"}, DependsOn: deps,
	}
}

func completionDecision(id string) domain.Decision {
	d := baseDecision(id, domain.ActionRequestCompletion, "ms-1")
	d.Target = domain.DecisionTarget{Kind: domain.KindMilestone, ID: "ms-1"}
	return d
}

func batch(ds ...domain.Decision) domain.DecisionBatch {
	return domain.DecisionBatch{
		SchemaVersion: 1, BatchID: "batch-1", ProjectID: "ccmas", StateRevision: 7,
		Goal: domain.DecisionTarget{Kind: domain.KindMilestone, ID: "ms-1"}, Rationale: "Kurze Begründung des Plans.", Decisions: ds,
	}
}

// ---- Auswertung --------------------------------------------------------------------

// rejected verlangt eine Ablehnung und liefert ihre Liste.
func rejected(t *testing.T, err error) *RejectionError {
	t.Helper()
	re, ok := AsRejection(err)
	if !ok {
		t.Fatalf("keine Ablehnung: %v", err)
	}
	return re
}

// wantRejection verlangt, dass die Liste mindestens eine Ablehnung mit Code und Entscheidungs-ID enthält,
// und liefert sie. Die Begründung muss etwas sagen (nicht leer) und, wenn angegeben, den Text enthalten.
func wantRejection(t *testing.T, err error, code Code, decisionID string, contains ...string) Rejection {
	t.Helper()
	re := rejected(t, err)
	for _, r := range re.Rejections {
		if r.Code == code && r.DecisionID == decisionID {
			if strings.TrimSpace(r.Reason) == "" {
				t.Errorf("Ablehnung %s/%s ohne Begründung", code, decisionID)
			}
			for _, c := range contains {
				if !strings.Contains(r.Reason, c) {
					t.Errorf("Begründung %q enthält %q nicht", r.Reason, c)
				}
			}
			return r
		}
	}
	t.Fatalf("keine Ablehnung %s für %q in:\n%s", code, decisionID, re.Error())
	return Rejection{}
}

// onlyCodes verlangt, dass die Ablehnungen genau diese Codes (als Menge) tragen.
func onlyCodes(t *testing.T, err error, codes ...Code) {
	t.Helper()
	re := rejected(t, err)
	got := map[Code]bool{}
	for _, r := range re.Rejections {
		got[r.Code] = true
	}
	want := map[Code]bool{}
	for _, c := range codes {
		want[c] = true
	}
	for c := range got {
		if !want[c] {
			t.Errorf("unerwarteter Code %s in:\n%s", c, re.Error())
		}
	}
	for c := range want {
		if !got[c] {
			t.Errorf("fehlender Code %s in:\n%s", c, re.Error())
		}
	}
}

func (e *testEnv) check(b domain.DecisionBatch, s ProjectSnapshot) (ValidatedBatch, error) {
	e.t.Helper()
	return Validator{}.Accept(b, s, e.pol)
}

func (e *testEnv) mustAccept(b domain.DecisionBatch, s ProjectSnapshot) ValidatedBatch {
	e.t.Helper()
	vb, err := e.check(b, s)
	if err != nil {
		e.t.Fatalf("Batch abgelehnt: %v", err)
	}
	return vb
}
