package orchestrator

import (
	"bytes"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/alexwenzel/ccmas/internal/domain"
	"github.com/alexwenzel/ccmas/internal/persistence"
	"github.com/alexwenzel/ccmas/internal/skills"
	"github.com/alexwenzel/ccmas/internal/supervisor"
)

var (
	tasksWhen  = time.Date(2026, 11, 10, 12, 30, 0, 0, time.UTC)
	tasksEarly = time.Date(2026, 11, 10, 11, 0, 0, 0, time.UTC) // vor der Uhr des Stores: Ausführungen beginnen vor ihrem Ende
)

// tasksFullWorld füllt eine Datenbank ausschließlich über die Repositories. Ziel ms-1 (beauftragt) und ms-2 (fremd):
//
//	far-1  Umsetzung, ms-2, unabhängig
//	c-1    Konzept, ms-2, abgeschlossen, Stand 1.0 freigegeben (hängt von far-1 ab)
//	c-2    Konzept, ms-2, abgeschlossen, Stand 2.0 NICHT freigegeben
//	c-3    Konzept, ms-2, abgeschlossen, Stand 3.0 freigegeben
//	c-4    Konzept, ms-2, abgeschlossen, Stand 1.0 freigegeben (gleich c-1)
//	c-5    Konzept, ms-2, abgeschlossen, Stand 1.1, Freigabe nennt die Version 0.9
//	c-6    Konzept, ms-2, abgeschlossen, Stand 4.0 strategisch (scope_change), vom Nutzer freigegeben
//	u-1    Umsetzung, ms-2, von niemandem im Ziel gebraucht, mit laufender Ausführung und offener Frage
//	i-1    Umsetzung, ms-1, hängt von c-1 ab
//	i-2    Umsetzung, ms-1, hängt von i-1 ab; PR, Kopf, Ergebnis, Runden, Review, aktive Ausführung, Fragen
//	i-3    Umsetzung, ms-1, hängt von c-2 ab
//	i-4    Umsetzung, ms-1, hängt von c-1 und c-3 ab
//	i-5    Fehler, ms-1, hängt von c-1 ab, eigener Konzeptstand 9.0 (nicht freigegeben)
//	i-6    Umsetzung, ms-1, hängt von c-1 und c-4 ab
//	i-7    Umsetzung, ms-1, hängt von c-1 und i-3 ab
//	i-8    Organisation, ms-1, hängt von c-1 ab
//	i-9    Umsetzung, ms-1, hängt von c-5 ab
//	i-10   Umsetzung, ms-1, hängt von c-6 ab
func tasksFullWorld(t *testing.T, s *persistence.Store) {
	t.Helper()
	tasksBase(t, s)
	impl := tasksFixture(t, "agent-profile--implementer.json", domain.ParseAgentProfile)
	rev := tasksFixture(t, "agent-profile--effort_fallback.json", domain.ParseAgentProfile)

	conceptOrder := func(id string, c *supervisor.ConceptState, deps ...string) persistence.WorkOrderRecord {
		o := tasksOrder(t, id, "ms-2", supervisor.OrderConcept, deps...)
		o.Concept = c
		return o
	}
	tasksUpdate(t, s, func(tx *persistence.Tx) error {
		c5 := tasksApproved("1.1")
		c5.Approval.Version = "0.9"
		strategic := &supervisor.ConceptState{Version: "4.0", Class: skills.ConceptStrategic, Criteria: []string{"scope_change"},
			Approval: &supervisor.ConceptApprovalInfo{Version: "4.0", Approver: skills.ApproverUser}}
		for _, o := range []persistence.WorkOrderRecord{
			tasksOrder(t, "far-1", "ms-2", supervisor.OrderImplementation),
			conceptOrder("c-1", tasksApproved("1.0"), "far-1"),
			conceptOrder("c-2", &supervisor.ConceptState{Version: "2.0", Class: skills.ConceptRoutine, Criteria: []string{}}),
			conceptOrder("c-3", tasksApproved("3.0")),
			conceptOrder("c-4", tasksApproved("1.0")),
			conceptOrder("c-6", strategic),
			conceptOrder("c-5", c5),
			tasksOrder(t, "u-1", "ms-2", supervisor.OrderImplementation),
			tasksOrder(t, "i-1", "ms-1", supervisor.OrderImplementation, "c-1"),
			tasksOrder(t, "i-2", "ms-1", supervisor.OrderImplementation, "i-1"),
			tasksOrder(t, "i-3", "ms-1", supervisor.OrderImplementation, "c-2"),
			tasksOrder(t, "i-4", "ms-1", supervisor.OrderImplementation, "c-1", "c-3"),
			tasksOrder(t, "i-6", "ms-1", supervisor.OrderImplementation, "c-1", "c-4"),
			tasksOrder(t, "i-7", "ms-1", supervisor.OrderImplementation, "c-1", "i-3"),
			tasksOrder(t, "i-8", "ms-1", supervisor.OrderOrganisation, "c-1"),
			tasksOrder(t, "i-9", "ms-1", supervisor.OrderImplementation, "c-5"),
			tasksOrder(t, "i-10", "ms-1", supervisor.OrderImplementation, "c-6"),
		} {
			if _, err := tx.CreateWorkOrder(o); err != nil {
				return err
			}
		}
		own := tasksOrder(t, "i-5", "ms-1", supervisor.OrderBug, "c-1")
		own.Concept = &supervisor.ConceptState{Version: "9.0", Class: skills.ConceptRoutine, Criteria: []string{}}
		_, err := tx.CreateWorkOrder(own)
		return err
	})
	tasksUpdate(t, s, func(tx *persistence.Tx) error {
		for _, id := range []string{"c-1", "c-2", "c-3", "c-4", "c-5", "c-6"} {
			if err := tasksComplete(tx, id); err != nil {
				return err
			}
		}
		return nil
	})

	// i-2: alles, was der Snapshot je Auftrag zeigt.
	tasksUpdate(t, s, func(tx *persistence.Tx) error {
		if err := tx.TransitionWorkOrder(tasksProject, "i-2", domain.WOWaiting, domain.WOInProgress); err != nil {
			return err
		}
		if err := tx.SetWorkOrderHead(tasksProject, "i-2", tasksCommitA, "feature/i-2"); err != nil {
			return err
		}
		if err := tx.SetLastResult(tasksProject, "i-2", &supervisor.LastResult{Role: domain.RoleImplementer, Result: "umgesetzt"}); err != nil {
			return err
		}
		if err := tx.SetRepairRounds(tasksProject, "i-2", 1, 2); err != nil {
			return err
		}
		return tx.SetPullRequest(tasksProject, "i-2", &supervisor.PRInfo{Number: 42, State: supervisor.PROpen, CI: supervisor.CIPassed, HeadCommit: tasksCommitA})
	})
	tasksUpdate(t, s, func(tx *persistence.Tx) error {
		if err := tasksStartExecution(tx, t, "exec-impl", "i-2", "agent-1", domain.RoleImplementer, impl, tasksEarly); err != nil {
			return err
		}
		if err := tasksStartExecution(tx, t, "exec-rev", "i-2", "rev-1", domain.RoleReviewer, rev, tasksEarly); err != nil {
			return err
		}
		if err := tx.FinishAttempt(tasksProject, "exec-rev", 1, domain.ExecRunning, persistence.AttemptResult{
			Terminal: domain.TerminalResult{Attempt: 1, State: domain.ExecSucceeded, Source: domain.TerminalFromResult},
			Outcome:  domain.OutcomeSucceeded, Summary: "ok"}); err != nil {
			return err
		}
		return tx.RecordReview(tasksProject, domain.ReviewReport{ReportID: "rep-i2", WorkOrderID: "i-2", ReviewerExecutionID: "exec-rev",
			Commit: tasksCommitA, ConceptVersion: supervisor.NoConceptVersion, Verdict: domain.VerdictApproved, Findings: []domain.Finding{}, CreatedAt: tasksWhen},
			"exec-impl")
	})
	tasksUpdate(t, s, func(tx *persistence.Tx) error {
		q := func(id, order, exec string) domain.Question {
			return domain.Question{QuestionID: id, WorkOrderID: order, ExecutionID: exec, Attempt: 1, Text: "Frage " + id, Context: "Kontext", State: domain.QuestionOpen, CreatedAt: tasksWhen}
		}
		if err := tx.CreateExecution(domain.Execution{ExecutionID: "exec-u", ProjectID: tasksProject, WorkOrderID: "u-1", AgentID: "agent-1", Role: domain.RoleImplementer, CreatedAt: tasksWhen}); err != nil {
			return err
		}
		if err := tx.StartAttempt(tasksProject, domain.ExecutionAttempt{ExecutionID: "exec-u", Attempt: 1, ProfileID: impl.ProfileID, ProfileVersion: impl.Version,
			State: domain.ExecInitializing, StartedAt: tasksWhen}); err != nil {
			return err
		}
		for _, d := range []domain.Question{q("q-1", "i-2", "exec-impl"), q("q-2", "u-1", "exec-u"), q("q-3", "i-2", "exec-impl"), q("q-4", "i-2", "exec-impl")} {
			if err := tx.CreateQuestion(tasksProject, d); err != nil {
				return err
			}
		}
		for _, id := range []string{"q-3", "q-4"} {
			if err := tx.AnswerQuestion(tasksProject, domain.Answer{AnswerID: "a-" + id, QuestionID: id, Text: "ja", CreatedAt: tasksWhen}); err != nil {
				return err
			}
		}
		return tx.ConfirmQuestionDelivery(tasksProject, "q-4", tasksWhen)
	})
	tasksUpdate(t, s, func(tx *persistence.Tx) error {
		return tx.SetGoalEvidence(tasksProject, "ms-1", &supervisor.Evidence{Integration: skills.IntegrationPROpen, AcceptanceCriteriaMet: true, ReviewEvidence: true, TestEvidence: true})
	})
}

func tasksBuild(t *testing.T, s *persistence.Store, goal string, offers SnapshotOffers) supervisor.ProjectSnapshot {
	t.Helper()
	var snap supervisor.ProjectSnapshot
	tasksView(t, s, func(r *persistence.ReadTx) error {
		var err error
		snap, err = BuildSnapshot(r, tasksProject, goal, offers)
		return err
	})
	return snap
}

func tasksOrderOf(t *testing.T, snap supervisor.ProjectSnapshot, id string) supervisor.Order {
	t.Helper()
	for _, o := range snap.Orders {
		if o.ID == id {
			return o
		}
	}
	t.Fatalf("Auftrag %s fehlt im Snapshot (vorhanden: %v)", id, tasksIDs(snap))
	return supervisor.Order{}
}

func tasksIDs(snap supervisor.ProjectSnapshot) []string {
	var ids []string
	for _, o := range snap.Orders {
		ids = append(ids, o.ID)
	}
	return ids
}

// TestSnapshotMapsEveryField: Jedes Feld des Snapshots kommt aus der Datenbank, die nur über die Repositories befüllt ist,
// und der gebaute Snapshot besteht die Prüfung des Snapshots.
func TestSnapshotMapsEveryField(t *testing.T) {
	s := tasksTestStore(t)
	tasksFullWorld(t, s)
	_, offers := tasksPolicy(t)
	snap := tasksBuild(t, s, "ms-1", offers)

	if err := snap.Check(); err != nil {
		t.Fatalf("Check: %v", err)
	}
	if p := snap.Problems(); len(p) != 0 {
		t.Fatalf("Problems: %v", p)
	}
	var rev int64
	tasksView(t, s, func(r *persistence.ReadTx) error {
		var err error
		rev, err = r.Revision(tasksProject)
		return err
	})
	if snap.SchemaVersion != supervisor.SnapshotSchemaVersion || snap.ProjectID != tasksProject || snap.StateRevision != rev || rev == 0 {
		t.Errorf("Kopf: Schema %d, Projekt %q, Revision %d (Datenbank %d)", snap.SchemaVersion, snap.ProjectID, snap.StateRevision, rev)
	}

	g := snap.Goal
	if g.Kind != domain.KindMilestone || g.ID != "ms-1" || g.Title != "Meilenstein 1" ||
		!slices.Equal(g.AcceptanceCriteria, []string{"A1", "A2"}) || !slices.Equal(g.Scope, []string{"CC-1", "CC-2"}) || !slices.Equal(g.OutOfScope, []string{"CC-9"}) {
		t.Errorf("Ziel = %+v", g)
	}
	wantEv := supervisor.Evidence{Integration: skills.IntegrationPROpen, AcceptanceCriteriaMet: true, ReviewEvidence: true, TestEvidence: true}
	if g.Evidence == nil || *g.Evidence != wantEv {
		t.Errorf("Nachweise = %+v", g.Evidence)
	}
	if snap.IntegrationMode != domain.ModePR {
		t.Errorf("Modus = %s (ms-1 ist pr, das Projekt auto)", snap.IntegrationMode)
	}
	if snap.Capacity.MaxWorkers != 5 {
		t.Errorf("max_workers = %d", snap.Capacity.MaxWorkers)
	}
	// Aktiv sind auch die Ausführungen anderer Ziele (sie belegen Plätze); die beendete Reviewer-Ausführung fehlt.
	wantActive := []supervisor.ActiveExecution{
		{ExecutionID: "exec-impl", Role: domain.RoleImplementer, WorkOrderID: "i-2", State: domain.ExecRunning},
		{ExecutionID: "exec-u", Role: domain.RoleImplementer, WorkOrderID: "u-1", State: domain.ExecInitializing},
	}
	if !reflect.DeepEqual(snap.Capacity.Active, wantActive) {
		t.Errorf("active = %+v\nerwartet %+v", snap.Capacity.Active, wantActive)
	}

	o := tasksOrderOf(t, snap, "i-2")
	if o.Kind != supervisor.OrderImplementation || o.Title != "Auftrag i-2" || o.State != domain.WOInProgress || o.Mode != domain.ModePR ||
		!slices.Equal(o.DependsOn, []string{"i-1"}) {
		t.Errorf("i-2 Kopf = %+v", o)
	}
	if o.HeadCommit != tasksCommitA || o.Branch != "feature/i-2" {
		t.Errorf("i-2 Kopf-Commit %q, Branch %q", o.HeadCommit, o.Branch)
	}
	if o.LastResult == nil || *o.LastResult != (supervisor.LastResult{Role: domain.RoleImplementer, Result: "umgesetzt"}) {
		t.Errorf("last_result = %+v", o.LastResult)
	}
	if o.PR == nil || *o.PR != (supervisor.PRInfo{Number: 42, State: supervisor.PROpen, CI: supervisor.CIPassed, HeadCommit: tasksCommitA}) {
		t.Errorf("pr = %+v", o.PR)
	}
	if o.CodeRepairRounds != 1 || o.ConceptRepairRounds != 2 {
		t.Errorf("Runden = %d/%d", o.CodeRepairRounds, o.ConceptRepairRounds)
	}
	if lr := o.LastReview; lr == nil || lr.Report.ReportID != "rep-i2" || lr.ReviewerRole != domain.RoleReviewer || lr.ImplementerExecutionID != "exec-impl" ||
		lr.Report.Commit != tasksCommitA || lr.Report.Verdict != domain.VerdictApproved {
		t.Errorf("last_review = %+v", lr)
	}
	if o.ActiveExecutionID != "exec-impl" {
		t.Errorf("active_execution_id = %q", o.ActiveExecutionID)
	}
	if o.ReviewConceptVersion != supervisor.NoConceptVersion {
		t.Errorf("review_concept_version = %q (abgeleitet durch Canonical)", o.ReviewConceptVersion)
	}
	if o.Concept != nil {
		t.Errorf("i-2 hat keinen Konzeptbezug, Konzept = %+v", o.Concept)
	}
	if u := tasksOrderOf(t, snap, "far-1"); u.ActiveExecutionID != "" || u.LastReview != nil || u.PR != nil || u.LastResult != nil || u.HeadCommit != "" {
		t.Errorf("far-1 ist unberührt, ist aber %+v", u)
	}

	// Offene Fragen: offen und beantwortet, nicht die zugestellte, nicht die eines Auftrags außerhalb des Snapshots.
	var qs []string
	for _, q := range snap.OpenQuestions {
		qs = append(qs, q.QuestionID+":"+string(q.State)+":"+q.WorkOrderID)
	}
	if want := []string{"q-1:open:i-2", "q-3:answered:i-2"}; !slices.Equal(qs, want) {
		t.Errorf("offene Fragen = %v, erwartet %v", qs, want)
	}
	if snap.OpenQuestions[0].Text != "Frage q-1" {
		t.Errorf("Fragetext = %q", snap.OpenQuestions[0].Text)
	}

	// Rollen und Skills kommen von den Angeboten, nicht aus SQLite.
	if len(snap.Roles) == 0 || len(snap.Roles) != len(offers.Roles) || len(snap.Skills) == 0 || len(snap.Skills) != len(offers.Skills) {
		t.Errorf("Rollen %d (Angebot %d), Skills %d (Angebot %d)", len(snap.Roles), len(offers.Roles), len(snap.Skills), len(offers.Skills))
	}

	// Deterministisch: zweimal gebaut, byte-gleich; und kanonisch.
	a, err := json.Marshal(snap)
	tasksMust(t, err)
	b, err := json.Marshal(tasksBuild(t, s, "ms-1", offers))
	tasksMust(t, err)
	if !bytes.Equal(a, b) {
		t.Error("zwei Bauten desselben Zustands unterscheiden sich")
	}
	if !reflect.DeepEqual(snap, snap.Canonical()) {
		t.Error("der gebaute Snapshot ist nicht kanonisch")
	}
}

// TestSnapshotContainsGoalOrdersAndTheirDependencies: alle Aufträge des Ziels und, über mehrere Stufen, die Aufträge
// außerhalb des Ziels, von denen sie abhängen; sonst nichts. Das Ziel ms-2 sieht seine eigenen Aufträge und deren Abhängigkeiten.
func TestSnapshotContainsGoalOrdersAndTheirDependencies(t *testing.T) {
	s := tasksTestStore(t)
	tasksFullWorld(t, s)

	snap := tasksBuild(t, s, "ms-1", SnapshotOffers{})
	want := []string{"c-1", "c-2", "c-3", "c-4", "c-5", "c-6", "far-1", "i-1", "i-10", "i-2", "i-3", "i-4", "i-5", "i-6", "i-7", "i-8", "i-9"}
	if got := tasksIDs(snap); !slices.Equal(got, want) {
		t.Errorf("Aufträge von ms-1 = %v\nerwartet %v", got, want)
	}
	if err := snap.Check(); err != nil {
		t.Errorf("Check: %v", err)
	}
	// far-1 ist nur über c-1 (zwei Stufen) bekannt; seine depends_on sind leer, aber c-1 verweist auf ihn.
	if c1 := tasksOrderOf(t, snap, "c-1"); !slices.Equal(c1.DependsOn, []string{"far-1"}) {
		t.Errorf("c-1 depends_on = %v", c1.DependsOn)
	}

	other := tasksBuild(t, s, "ms-2", SnapshotOffers{})
	wantOther := []string{"c-1", "c-2", "c-3", "c-4", "c-5", "c-6", "far-1", "u-1"}
	if got := tasksIDs(other); !slices.Equal(got, wantOther) {
		t.Errorf("Aufträge von ms-2 = %v\nerwartet %v", got, wantOther)
	}
	if err := other.Check(); err != nil {
		t.Errorf("Check ms-2: %v", err)
	}
	if other.Goal.ID != "ms-2" || other.IntegrationMode != domain.ModeAuto {
		t.Errorf("Ziel ms-2 = %+v, Modus %s", other.Goal, other.IntegrationMode)
	}
	// Die Frage von u-1 gehört jetzt zum Snapshot, die von i-2 nicht.
	var qs []string
	for _, q := range other.OpenQuestions {
		qs = append(qs, q.QuestionID)
	}
	if !slices.Equal(qs, []string{"q-2"}) {
		t.Errorf("offene Fragen von ms-2 = %v", qs)
	}
}

// TestSnapshotConceptInheritance: der freigegebene Konzeptstand wird auf abhängige Aufträge übertragen, fail-safe.
func TestSnapshotConceptInheritance(t *testing.T) {
	s := tasksTestStore(t)
	tasksFullWorld(t, s)
	snap := tasksBuild(t, s, "ms-1", SnapshotOffers{})
	c1 := tasksOrderOf(t, snap, "c-1").Concept
	if c1 == nil || c1.Approval == nil {
		t.Fatalf("Ausgangslage: c-1 = %+v", c1)
	}

	inherits := func(t *testing.T, id string, want supervisor.ConceptState) {
		t.Helper()
		o := tasksOrderOf(t, snap, id)
		if o.Concept == nil {
			t.Fatalf("%s: kein Konzeptstand übertragen", id)
		}
		if !reflect.DeepEqual(*o.Concept, want) {
			t.Errorf("%s: Konzeptstand = %+v (Freigabe %+v)\nerwartet %+v (Freigabe %+v)", id, *o.Concept, o.Concept.Approval, want, want.Approval)
		}
		if o.ReviewConceptVersion != want.Version {
			t.Errorf("%s: review_concept_version = %q", id, o.ReviewConceptVersion)
		}
		if o.Concept == c1 || o.Concept.Approval == c1.Approval {
			t.Errorf("%s: der übertragene Stand teilt Zeiger mit dem Konzeptauftrag", id)
		}
	}
	routine := supervisor.ConceptState{Version: "1.0", Class: skills.ConceptRoutine, Criteria: []string{},
		Approval: &supervisor.ConceptApprovalInfo{Version: "1.0", Approver: skills.ApproverConceptReviewer}}
	notInherited := func(t *testing.T, id string) {
		t.Helper()
		if o := tasksOrderOf(t, snap, id); o.Concept != nil {
			t.Errorf("%s: Konzeptstand %+v übertragen, erwartet keiner", id, *o.Concept)
		}
	}

	t.Run("genau ein freigegebener Konzeptauftrag", func(t *testing.T) { inherits(t, "i-1", routine) })
	t.Run("Einstufung, Kriterien und Freigeber werden mit übertragen", func(t *testing.T) {
		inherits(t, "i-10", supervisor.ConceptState{Version: "4.0", Class: skills.ConceptStrategic, Criteria: []string{"scope_change"},
			Approval: &supervisor.ConceptApprovalInfo{Version: "4.0", Approver: skills.ApproverUser}})
	})
	t.Run("Konzeptauftrag nicht freigegeben", func(t *testing.T) { notInherited(t, "i-3") })
	t.Run("mehrere mit unterschiedlichen Ständen", func(t *testing.T) { notInherited(t, "i-4") })
	t.Run("mehrere mit gleichem Stand", func(t *testing.T) { inherits(t, "i-6", routine) })
	t.Run("andere Abhängigkeiten stören nicht", func(t *testing.T) { inherits(t, "i-7", routine) })
	t.Run("eigener Stand hat Vorrang", func(t *testing.T) {
		o := tasksOrderOf(t, snap, "i-5")
		if o.Concept == nil || o.Concept.Version != "9.0" || o.Concept.Approval != nil {
			t.Errorf("i-5 = %+v: der eigene, nicht freigegebene Stand muss bleiben", o.Concept)
		}
	})
	t.Run("Freigabe für eine andere Version", func(t *testing.T) { notInherited(t, "i-9") })
	t.Run("Organisationsauftrag erbt nicht", func(t *testing.T) { notInherited(t, "i-8") })
	t.Run("Konzeptaufträge behalten ihren Stand", func(t *testing.T) {
		if c := tasksOrderOf(t, snap, "c-2").Concept; c == nil || c.Version != "2.0" || c.Approval != nil {
			t.Errorf("c-2 = %+v", c)
		}
	})
	t.Run("Aufträge ohne Konzeptabhängigkeit", func(t *testing.T) { notInherited(t, "i-2") })

	t.Run("die Datenbank bleibt unverändert", func(t *testing.T) {
		tasksView(t, s, func(r *persistence.ReadTx) error {
			rec, err := r.WorkOrder(tasksProject, "i-1")
			if err != nil {
				return err
			}
			if rec.Concept != nil {
				t.Errorf("i-1 trägt in der Datenbank einen Konzeptstand: %+v", rec.Concept)
			}
			return nil
		})
	})
}

// TestSnapshotConceptReleaseFollowsTheDatabase: Die Übertragung wird bei jedem Bau neu entschieden. Wird die Freigabe
// zurückgenommen, erbt der abhängige Auftrag nichts mehr; erst die erneute Freigabe der neuen Version überträgt wieder.
func TestSnapshotConceptReleaseFollowsTheDatabase(t *testing.T) {
	s := tasksTestStore(t)
	tasksFullWorld(t, s)
	set := func(c *supervisor.ConceptState) {
		tasksUpdate(t, s, func(tx *persistence.Tx) error { return tx.SetConceptState(tasksProject, "c-1", c) })
	}
	inherited := func() *supervisor.ConceptState {
		return tasksOrderOf(t, tasksBuild(t, s, "ms-1", SnapshotOffers{}), "i-1").Concept
	}

	if c := inherited(); c == nil || c.Version != "1.0" {
		t.Fatalf("Ausgangslage: %+v", c)
	}
	set(&supervisor.ConceptState{Version: "1.0", Class: skills.ConceptRoutine, Criteria: []string{}}) // Freigabe zurückgenommen
	if c := inherited(); c != nil {
		t.Errorf("ohne Freigabe übertragen: %+v", c)
	}
	set(tasksApproved("2.0")) // neue Version, freigegeben
	if c := inherited(); c == nil || c.Version != "2.0" || c.Approval == nil || c.Approval.Version != "2.0" {
		t.Errorf("neue Version: %+v", c)
	}
}

func TestSnapshotRejectsForeignProjectAndGoal(t *testing.T) {
	s := tasksTestStore(t)
	tasksFullWorld(t, s)
	tasksUpdate(t, s, func(tx *persistence.Tx) error {
		cfg := tasksFixture(t, "project-config--full.json", domain.ParseProjectConfig)
		cfg.ProjectID = "other"
		if _, err := tx.CreateProject(cfg); err != nil {
			return err
		}
		_, err := tx.CreateGoal(persistence.GoalRecord{ProjectID: "other", IntegrationMode: domain.ModePR,
			Goal: supervisor.Goal{Kind: domain.KindMilestone, ID: "ms-o", AcceptanceCriteria: []string{}, Scope: []string{}, OutOfScope: []string{}}})
		return err
	})
	build := func(project, goal string) error {
		return s.View(tasksCtx(t), func(r *persistence.ReadTx) error {
			_, err := BuildSnapshot(r, project, goal, SnapshotOffers{})
			return err
		})
	}
	for name, c := range map[string][2]string{
		"unbekanntes Projekt":         {"nope", "ms-1"},
		"unbekanntes Ziel":            {tasksProject, "ms-9"},
		"Ziel eines anderen Projekts": {tasksProject, "ms-o"},
		"Projekt mit fremdem Ziel":    {"other", "ms-1"},
	} {
		err := build(c[0], c[1])
		if err == nil {
			t.Errorf("%s: kein Fehler", name)
			continue
		}
		if persistence.CodeOf(err) != persistence.CodeNotFound {
			t.Errorf("%s: %v (erwartet not_found)", name, err)
		}
	}
	// Das andere Projekt baut seinen eigenen Snapshot ohne Aufträge von ccmas.
	var snap supervisor.ProjectSnapshot
	tasksView(t, s, func(r *persistence.ReadTx) error {
		var err error
		snap, err = BuildSnapshot(r, "other", "ms-o", SnapshotOffers{})
		return err
	})
	if snap.ProjectID != "other" || len(snap.Orders) != 0 || len(snap.Capacity.Active) != 0 || len(snap.OpenQuestions) != 0 {
		t.Errorf("Snapshot des anderen Projekts = %+v", snap)
	}
	if err := snap.Check(); err != nil {
		t.Errorf("Check: %v", err)
	}
}

// TestSnapshotRevisionZero: Ein Projekt, das in derselben Transaktion angelegt und weiter geändert wird, bleibt auf Revision 0
// (so festgelegt). 0 ist ein gewöhnlicher Wert: der Snapshot ist gültig; die nächste Planungsänderung ergibt 1, und der Snapshot
// auf 0 ist dann veraltet.
func TestSnapshotRevisionZero(t *testing.T) {
	s := tasksTestStore(t)
	res := tasksUpdate(t, s, func(tx *persistence.Tx) error {
		if _, err := tx.CreateProject(tasksFixture(t, "project-config--full.json", domain.ParseProjectConfig)); err != nil {
			return err
		}
		if _, err := tx.CreateGoal(persistence.GoalRecord{ProjectID: tasksProject, IntegrationMode: domain.ModePR,
			Goal: supervisor.Goal{Kind: domain.KindMilestone, ID: "ms-1", Title: "M", AcceptanceCriteria: []string{"A"}, Scope: []string{}, OutOfScope: []string{}}}); err != nil {
			return err
		}
		_, err := tx.CreateWorkOrder(tasksOrder(t, "wo-1", "ms-1", supervisor.OrderImplementation))
		return err
	})
	if got, ok := res.Revisions[tasksProject]; !ok || got != 0 {
		t.Fatalf("Revision nach der kombinierten Anlage = %d (gemeldet: %v), erwartet 0 (so festgelegt)", got, ok)
	}

	snap0 := tasksBuild(t, s, "ms-1", SnapshotOffers{})
	if snap0.StateRevision != 0 || len(snap0.Orders) != 1 {
		t.Fatalf("Snapshot: Revision %d, Aufträge %v", snap0.StateRevision, tasksIDs(snap0))
	}
	if err := snap0.Check(); err != nil {
		t.Fatalf("Revision 0 ist ein gültiger Snapshot: %v", err)
	}

	// Der Snapshot auf 0 ist, solange nichts geschieht, aktuell: ein Batch auf Revision 0 ist nicht veraltet.
	pol, offers := tasksPolicy(t)
	waitBatch := func(rev int64) domain.DecisionBatch {
		d := domain.Decision{SchemaVersion: 1, DecisionID: "dec-1", ProjectID: tasksProject, StateRevision: rev,
			Target: domain.DecisionTarget{Kind: domain.KindWorkOrder, ID: "wo-1"}, Action: domain.ActionWait, Preconditions: []domain.Precondition{},
			Rationale: "Es gibt nichts zu tun."}
		return domain.DecisionBatch{SchemaVersion: 1, BatchID: "batch-0", ProjectID: tasksProject, StateRevision: rev,
			Goal: domain.DecisionTarget{Kind: domain.KindMilestone, ID: "ms-1"}, Rationale: "Warten.", Decisions: []domain.Decision{d}}
	}
	snap0 = tasksBuild(t, s, "ms-1", offers)
	if err := (supervisor.Validator{}).Validate(waitBatch(0), snap0, pol); err != nil {
		t.Fatalf("Batch auf Revision 0 gegen den Snapshot auf 0: %v", err)
	}

	// Die nächste Planungsänderung ergibt 1.
	res = tasksUpdate(t, s, func(tx *persistence.Tx) error {
		return tx.TransitionWorkOrder(tasksProject, "wo-1", domain.WOWaiting, domain.WOInProgress)
	})
	if res.Revisions[tasksProject] != 1 {
		t.Fatalf("Revision nach der nächsten Änderung = %d, erwartet 1", res.Revisions[tasksProject])
	}
	snap1 := tasksBuild(t, s, "ms-1", offers)
	if snap1.StateRevision != 1 {
		t.Fatalf("Snapshot danach: Revision %d", snap1.StateRevision)
	}
	err := (supervisor.Validator{}).Validate(waitBatch(0), snap1, pol)
	re, ok := supervisor.AsRejection(err)
	if !ok {
		t.Fatalf("der Batch auf Revision 0 ist gegen Revision 1 nicht abgelehnt: %v", err)
	}
	stale := false
	for _, r := range re.Rejections {
		stale = stale || r.Code == supervisor.CodeStaleRevision
	}
	if !stale {
		t.Errorf("Ablehnung ohne %s: %v", supervisor.CodeStaleRevision, re)
	}
	if err := (supervisor.Validator{}).Validate(waitBatch(1), snap1, pol); err != nil {
		t.Errorf("Batch auf Revision 1 gegen den Snapshot auf 1: %v", err)
	}
}

func TestSnapshotSourceMatchesBuildInView(t *testing.T) {
	s := tasksTestStore(t)
	tasksFullWorld(t, s)
	_, offers := tasksPolicy(t)

	src, err := NewStoreSnapshotSource(s, tasksProject, "ms-1", offers)
	tasksMust(t, err)
	var source supervisor.SnapshotSource = src
	got, err := source.CurrentSnapshot(tasksCtx(t), tasksProject)
	tasksMust(t, err)
	if want := tasksBuild(t, s, "ms-1", offers); !reflect.DeepEqual(got, want) {
		t.Errorf("SnapshotSource liefert etwas anderes als der Bau im View:\n%+v\n%+v", got, want)
	}

	// Ein fremdes Projekt ist ein Fehler, keine stille Umleitung.
	if _, err := src.CurrentSnapshot(tasksCtx(t), "other"); err == nil || !strings.Contains(err.Error(), "other") {
		t.Errorf("fremdes Projekt: %v", err)
	}
	// Die Quelle baut bei jedem Aufruf neu: sie sieht, was danach festgeschrieben wird.
	tasksUpdate(t, s, func(tx *persistence.Tx) error {
		return tx.SetRepairRounds(tasksProject, "i-2", 2, 2)
	})
	after, err := src.CurrentSnapshot(tasksCtx(t), tasksProject)
	tasksMust(t, err)
	if after.StateRevision != got.StateRevision+1 || tasksOrderOf(t, after, "i-2").CodeRepairRounds != 2 {
		t.Errorf("Revision %d -> %d, Runden %d", got.StateRevision, after.StateRevision, tasksOrderOf(t, after, "i-2").CodeRepairRounds)
	}

	// Die Angebote sind kopiert: Änderungen des Aufrufers danach wirken nicht.
	offers.Roles[0].Model = "veraendert"
	again, err := src.CurrentSnapshot(tasksCtx(t), tasksProject)
	tasksMust(t, err)
	if again.Roles[0].Model == "veraendert" {
		t.Error("die Quelle teilt die Angebote mit dem Aufrufer")
	}

	for name, c := range map[string]struct {
		store   *persistence.Store
		project string
		goal    string
	}{
		"ohne Store":         {nil, tasksProject, "ms-1"},
		"ungültiges Projekt": {s, "a b", "ms-1"},
		"ungültiges Ziel":    {s, tasksProject, ""},
	} {
		if _, err := NewStoreSnapshotSource(c.store, c.project, c.goal, SnapshotOffers{}); err == nil {
			t.Errorf("%s: kein Fehler", name)
		}
	}
	missing, err := NewStoreSnapshotSource(s, tasksProject, "ms-9", SnapshotOffers{})
	tasksMust(t, err)
	if _, err := missing.CurrentSnapshot(tasksCtx(t), tasksProject); persistence.CodeOf(err) != persistence.CodeNotFound {
		t.Errorf("unbekanntes Ziel: %v", err)
	}
}

// TestSnapshotInsideWriteTransaction: Das Leseinterface erfüllen beide Transaktionsarten. In der Schreibtransaktion sieht der
// Bau die eigenen, noch nicht festgeschriebenen Änderungen; nach dem Festschreiben ergibt der Bau im View dasselbe.
func TestSnapshotInsideWriteTransaction(t *testing.T) {
	s := tasksTestStore(t)
	tasksFullWorld(t, s)
	before := tasksBuild(t, s, "ms-1", SnapshotOffers{})

	var inside supervisor.ProjectSnapshot
	tasksUpdate(t, s, func(tx *persistence.Tx) error {
		if err := tx.SetRepairRounds(tasksProject, "i-2", 3, 3); err != nil {
			return err
		}
		var err error
		inside, err = BuildSnapshot(tx, tasksProject, "ms-1", SnapshotOffers{})
		return err
	})
	if got := tasksOrderOf(t, inside, "i-2").CodeRepairRounds; got != 3 {
		t.Errorf("der Bau in der Schreibtransaktion sieht die eigene Änderung nicht (Runden %d)", got)
	}
	if inside.StateRevision != before.StateRevision+1 {
		t.Errorf("Revision in der Transaktion %d, vorher %d", inside.StateRevision, before.StateRevision)
	}
	if err := inside.Check(); err != nil {
		t.Errorf("Check: %v", err)
	}
	if after := tasksBuild(t, s, "ms-1", SnapshotOffers{}); !reflect.DeepEqual(after, inside) {
		t.Errorf("Bau in der Schreibtransaktion und danach im View unterscheiden sich")
	}

	// Ein Rollback nimmt auch den Snapshot zurück: er wurde nur gelesen, nichts geschrieben.
	_, err := s.Update(tasksCtx(t), "tester", func(tx *persistence.Tx) error {
		if err := tx.SetRepairRounds(tasksProject, "i-2", 9, 9); err != nil {
			return err
		}
		return context_cancelled
	})
	if err == nil {
		t.Fatal("kein Fehler")
	}
	if got := tasksOrderOf(t, tasksBuild(t, s, "ms-1", SnapshotOffers{}), "i-2").CodeRepairRounds; got != 3 {
		t.Errorf("Runden nach Rollback = %d", got)
	}
}

type tasksSentinel string

func (e tasksSentinel) Error() string { return string(e) }

const context_cancelled = tasksSentinel("Abbruch im Test")

// TestSnapshotSeveralActiveExecutionsKeepOrderActive: Hat ein Auftrag mehrere aktive Ausführungen, bleibt active_execution_id
// gesetzt (die kleinste ID), damit der Validator nichts Zweites auf ihm startet; beide stehen in capacity.active.
func TestSnapshotSeveralActiveExecutionsKeepOrderActive(t *testing.T) {
	s := tasksTestStore(t)
	tasksFullWorld(t, s)
	impl := tasksFixture(t, "agent-profile--implementer.json", domain.ParseAgentProfile)
	tasksUpdate(t, s, func(tx *persistence.Tx) error {
		return tasksStartExecution(tx, t, "exec-aaa", "i-2", "agent-1", domain.RoleImplementer, impl, tasksEarly)
	})
	snap := tasksBuild(t, s, "ms-1", SnapshotOffers{})
	if got := tasksOrderOf(t, snap, "i-2").ActiveExecutionID; got != "exec-aaa" {
		t.Errorf("active_execution_id = %q", got)
	}
	if len(snap.Capacity.Active) != 3 {
		t.Errorf("active = %+v", snap.Capacity.Active)
	}
	if err := snap.Check(); err != nil {
		t.Errorf("Check: %v", err)
	}
}

// ---- Der Validator auf dem gebauten Snapshot ---------------------------------------------------------------------

func tasksImplementation(t *testing.T, snap supervisor.ProjectSnapshot, id, orderID string) domain.Decision {
	t.Helper()
	var offer *supervisor.RoleOffer
	for i := range snap.Roles {
		if snap.Roles[i].Role == domain.RoleImplementer {
			offer = &snap.Roles[i]
		}
	}
	if offer == nil || len(offer.DefaultSkillIDs) == 0 {
		t.Fatalf("kein Angebot für die Rolle implementer mit Standard-Skills: %+v", offer)
	}
	return domain.Decision{
		SchemaVersion: 1, DecisionID: id, ProjectID: tasksProject, StateRevision: snap.StateRevision,
		Target: domain.DecisionTarget{Kind: domain.KindWorkOrder, ID: orderID}, Action: domain.ActionImplementation,
		Preconditions:   []domain.Precondition{{Kind: domain.PrecConceptApproved}},
		Rationale:       "Das Konzept ist freigegeben, die Umsetzung kann beginnen.",
		ExpectedOutcome: "Der Auftrag ist umgesetzt und prüfbar.",
		Estimate:        &domain.Estimate{Size: domain.SizeM, Complexity: domain.LevelMedium, Uncertainty: domain.LevelLow},
		Selection:       &domain.Selection{Role: domain.RoleImplementer, SkillIDs: offer.DefaultSkillIDs, Model: offer.Model, Effort: offer.Effort},
	}
}

func tasksBatch(snap supervisor.ProjectSnapshot, ds ...domain.Decision) domain.DecisionBatch {
	return domain.DecisionBatch{SchemaVersion: 1, BatchID: "batch-v", ProjectID: tasksProject, StateRevision: snap.StateRevision,
		Goal: domain.DecisionTarget{Kind: domain.KindMilestone, ID: "ms-1"}, Rationale: "Umsetzung beauftragen.", Decisions: ds}
}

// TestValidatorOnBuiltSnapshot: Auf dem aus SQLite gebauten Snapshot (Policy aus der Beispielkonfiguration und dem echten
// Skill-Katalog) nimmt der Validator die Umsetzung eines Auftrags mit freigegebenem Konzept an und lehnt die eines Auftrags
// ab, dessen Konzept nicht freigegeben ist oder dessen Stand sich nicht eindeutig übertragen lässt.
func TestValidatorOnBuiltSnapshot(t *testing.T) {
	s := tasksTestStore(t)
	tasksFullWorld(t, s)
	pol, offers := tasksPolicy(t)
	snap := tasksBuild(t, s, "ms-1", offers)

	ok := tasksBatch(snap, tasksImplementation(t, snap, "dec-1", "i-1"))
	vb, err := supervisor.Validator{}.Accept(ok, snap, pol)
	if err != nil {
		t.Fatalf("zulässiger Batch abgelehnt: %v", err)
	}
	if len(vb.Delegations()) != 1 || vb.Delegations()[0].OrderID != "i-1" {
		t.Errorf("Delegationen = %+v", vb.Delegations())
	}

	for name, order := range map[string]string{
		"Konzept nicht freigegeben":        "i-3",
		"mehrere Konzeptstände":            "i-4",
		"Freigabe für eine andere Version": "i-9",
	} {
		t.Run(name, func(t *testing.T) {
			bad := tasksBatch(snap, tasksImplementation(t, snap, "dec-1", order))
			err := (supervisor.Validator{}).Validate(bad, snap, pol)
			re, isRejection := supervisor.AsRejection(err)
			if !isRejection {
				t.Fatalf("Umsetzung von %s angenommen: %v", order, err)
			}
			found := false
			for _, r := range re.Rejections {
				found = found || (r.Code == supervisor.CodePreconditionUnmet && r.DecisionID == "dec-1")
			}
			if !found {
				t.Errorf("keine Ablehnung %s: %v", supervisor.CodePreconditionUnmet, re)
			}
		})
	}

	// Ein einziger abgelehnter Eintrag lehnt den ganzen Batch ab (atomar).
	mixed := tasksBatch(snap, tasksImplementation(t, snap, "dec-1", "i-1"), tasksImplementation(t, snap, "dec-2", "i-3"))
	if err := (supervisor.Validator{}).Validate(mixed, snap, pol); err == nil {
		t.Error("Batch mit einer unzulässigen Entscheidung angenommen")
	}
}
