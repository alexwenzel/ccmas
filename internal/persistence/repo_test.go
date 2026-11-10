package persistence

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/alexwenzel/ccmas/internal/domain"
	"github.com/alexwenzel/ccmas/internal/supervisor"
)

// TestRestartPersistence: nach Close und erneutem Open ist alles inhaltlich identisch, gelesen als Domain-Werte.
func TestRestartPersistence(t *testing.T) {
	s := newStore(t)
	ex := buildWorld(t, s)
	before := readWorld(t, s)

	s2 := closeAndReopen(t, s)
	after := readWorld(t, s2)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("Zustand nach Neustart weicht ab:\nvorher  %+v\nnachher %+v", before, after)
	}

	// Und die gelesenen Werte sind die geschriebenen (nicht nur untereinander gleich).
	if after.Project.Config.ProjectID != projectID || !reflect.DeepEqual(after.Project.Config, fxProject(t)) {
		t.Errorf("Projektkonfiguration = %+v", after.Project.Config)
	}
	wantProfiles := []domain.AgentProfile{fxProfile(t), fxReviewerProfile(t)}
	if !reflect.DeepEqual(after.Profiles, wantProfiles) {
		t.Errorf("Profile = %+v", after.Profiles)
	}
	if !reflect.DeepEqual(after.Snapshot.Snapshot, fxSnapshot(t)) || after.Snapshot.Path != snapshotPath {
		t.Errorf("Skill-Snapshot = %+v", after.Snapshot)
	}

	var o2 WorkOrderRecord
	for _, o := range after.Orders {
		if o.WorkOrderID == "wo-2" {
			o2 = o
		}
	}
	if o2.Revision != ex.order2Rev {
		t.Errorf("Zeilenrevision = %d, erwartet %d", o2.Revision, ex.order2Rev)
	}
	o2.Revision, o2.CreatedAt, o2.UpdatedAt = 0, time.Time{}, time.Time{}
	if !reflect.DeepEqual(o2, ex.order2) {
		t.Errorf("Auftrag wo-2:\ngelesen    %+v\ngeschrieben %+v", o2, ex.order2)
	}

	var task TaskRecord
	for _, tk := range after.Tasks {
		task = tk
	}
	if !reflect.DeepEqual(task, ex.task) {
		t.Errorf("Task:\ngelesen     %+v\ngeschrieben %+v", task, ex.task)
	}

	var q1 domain.Question
	for _, q := range after.Questions {
		if q.QuestionID == "q-1" {
			q1 = q
		}
	}
	if !reflect.DeepEqual(q1, ex.question1) {
		t.Errorf("Frage:\ngelesen     %+v\ngeschrieben %+v", q1, ex.question1)
	}
	if len(after.Answers) != 1 || !reflect.DeepEqual(after.Answers[0], ex.answer1) {
		t.Errorf("Antwort = %+v, erwartet %+v", after.Answers, ex.answer1)
	}

	if len(after.Plans) != 1 || !reflect.DeepEqual(after.Plans[0].Batch, ex.batch) ||
		after.Plans[0].Verdict != supervisor.VerdictRejected || !reflect.DeepEqual(after.Plans[0].Rejections, ex.rejections) {
		t.Errorf("Plan = %+v", after.Plans)
	}
	if len(after.Decisions) != 3 {
		t.Fatalf("%d Entscheidungen, erwartet 3", len(after.Decisions))
	}
	d1 := after.Decisions[0]
	if d1.Decision.DecisionID != "dec-1" || d1.Validation != supervisor.VerdictRejected || d1.Delegation == nil || !reflect.DeepEqual(*d1.Delegation, fxProfile(t)) {
		t.Errorf("Entscheidung dec-1 = %+v", d1)
	}
	if d3 := after.Decisions[2]; d3.Delegation != nil || d3.Validation != supervisor.VerdictRejected {
		// Der Batch-Eintrag (ohne decision_id) lehnt alle ab; dec-3 hat kein Delegationsprofil.
		t.Errorf("Entscheidung dec-3 = %+v", d3)
	}

	if len(after.Reviews) != 1 || !reflect.DeepEqual(after.Reviews[0].Report, ex.review) {
		t.Errorf("Review = %+v", after.Reviews)
	}

	var exec domain.ExecutionAttempt
	for _, a := range after.Attempts {
		if a.ExecutionID == "exec-impl" {
			exec = a
		}
	}
	if exec.State != domain.ExecSucceeded || exec.SessionID != "sess-42" || exec.Terminal == nil || exec.Terminal.Source != domain.TerminalFromResult ||
		exec.Process == nil || exec.Process.PID != 4711 || exec.OSProcessState != domain.ProcSleeping || string(exec.Spec) != ex.attemptSpec ||
		exec.Summary != "Umsetzung abgeschlossen." || exec.SkillSnapshotID != fxSnapshot(t).SnapshotID || exec.Worktree != "/work/exec/ccmas/wo-1" {
		t.Errorf("Versuch exec-impl = %+v", exec)
	}
}

// TestSnapshotFieldsPersisted: jedes Feld, das der ProjectSnapshot aus dem führenden Zustand braucht (Übergaben aus
// CC-14), wird gespeichert und nach Neustart unverändert gelesen.
func TestSnapshotFieldsPersisted(t *testing.T) {
	s := newStore(t)
	ex := buildWorld(t, s)
	var lastRev int64
	mustView(t, s, func(r *ReadTx) error { var err error; lastRev, err = r.Revision(projectID); return err })
	s = closeAndReopen(t, s)

	mustView(t, s, func(r *ReadTx) error {
		// state_revision.
		if rev, err := r.Revision(projectID); err != nil || rev != lastRev {
			t.Errorf("Revision = %d (%v), erwartet %d", rev, err, lastRev)
		}
		o2, err := r.WorkOrder(projectID, "wo-2")
		if err != nil {
			return err
		}
		// Auftragsart, Titel, Kopf-Commit, Branch, Modus, Abhängigkeiten.
		if o2.Kind != supervisor.OrderBug || o2.Title != "Auftrag 2" || o2.HeadCommit != commitA || o2.Branch != "feature/wo-2" ||
			o2.Mode != domain.ModeAuto || !reflect.DeepEqual(o2.DependsOn, []string{"wo-1"}) {
			t.Errorf("Auftragsfelder = %+v", o2)
		}
		// pr.head_commit, last_result, Reparaturrunden, Konzeptstand mit Freigabe.
		if o2.PR == nil || *o2.PR != *ex.order2.PR || o2.PR.HeadCommit != commitA {
			t.Errorf("PR = %+v", o2.PR)
		}
		if o2.LastResult == nil || *o2.LastResult != *ex.order2.LastResult {
			t.Errorf("last_result = %+v", o2.LastResult)
		}
		if o2.CodeRepairRounds != 1 || o2.ConceptRepairRounds != 2 {
			t.Errorf("Reparaturrunden = %d/%d", o2.CodeRepairRounds, o2.ConceptRepairRounds)
		}
		if !reflect.DeepEqual(o2.Concept, ex.order2.Concept) || o2.Concept.Approval == nil || o2.Concept.Approval.Approver != "concept_reviewer" {
			t.Errorf("Konzeptstand = %+v", o2.Concept)
		}
		// Abschlussnachweise und Zielfelder.
		g, err := r.Goal(projectID, "ms-1")
		if err != nil {
			return err
		}
		if g.Goal.Evidence == nil || *g.Goal.Evidence != ex.evidence || g.Goal.Title != "Meilenstein 1" ||
			!reflect.DeepEqual(g.Goal.Scope, []string{"CC-1"}) || !reflect.DeepEqual(g.Goal.OutOfScope, []string{"CC-9"}) ||
			!reflect.DeepEqual(g.Goal.AcceptanceCriteria, []string{"A1"}) || g.IntegrationMode != domain.ModePR {
			t.Errorf("Ziel = %+v", g)
		}
		// Ausführungsdatensätze mit Rolle: nur die laufende Reviewer-Ausführung ist aktiv.
		active, err := r.ActiveExecutions(projectID)
		if err != nil {
			return err
		}
		want := []supervisor.ActiveExecution{{ExecutionID: "exec-rev", Role: domain.RoleReviewer, WorkOrderID: "wo-1", State: domain.ExecRunning}}
		if !reflect.DeepEqual(active, want) {
			t.Errorf("aktive Ausführungen = %+v, erwartet %+v", active, want)
		}
		// Review-Datensatz: Rolle der Reviewer-Ausführung (per Join) und Implementierer-Ausführung.
		rec, err := r.LastReview(projectID, "wo-1")
		if err != nil {
			return err
		}
		if rec == nil || rec.ReviewerRole != domain.RoleReviewer || rec.ImplementerExecutionID != "exec-impl" || !reflect.DeepEqual(rec.Report, ex.review) {
			t.Errorf("letztes Review = %+v", rec)
		}
		if none, err := r.LastReview(projectID, "wo-2"); err != nil || none != nil {
			t.Errorf("Review ohne Bericht = %+v (%v)", none, err)
		}
		// Offene Fragen: q-2 offen; q-1 ist zugestellt und fällt heraus.
		open, err := r.OpenQuestions(projectID)
		if err != nil {
			return err
		}
		wantQ := []supervisor.OpenQuestion{{QuestionID: "q-2", WorkOrderID: "wo-1", State: domain.QuestionOpen, Text: "Darf ich nachfragen?"}}
		if !reflect.DeepEqual(open, wantQ) {
			t.Errorf("offene Fragen = %+v, erwartet %+v", open, wantQ)
		}
		return nil
	})
}

// TestAuditCoupling: jede fachliche Änderung erzeugt genau ein Ereignis in derselben Transaktion; ein Fehler im
// Callback hinterlässt weder Änderung noch Ereignis noch Revisionsänderung.
func TestAuditCoupling(t *testing.T) {
	s := newStore(t)
	seedBase(t, s)
	errBoom := errors.New("boom")

	other := fxProject(t)
	other.ProjectID = "other"
	second := fxSnapshot(t)
	second.SnapshotID = "sha256:" + hex64("d")
	profile4 := fxProfile(t)
	profile4.Version = 4
	updatedCfg := fxProject(t)
	updatedCfg.MaxWorkers = 7

	type step struct {
		name  string
		kind  string
		level domain.AuditLevel
		bump  bool
		fn    func(tx *Tx) error
	}
	steps := []step{
		{"Projekt anlegen", domain.AuditProjectCreated, domain.AuditLevelConfig, false, func(tx *Tx) error { _, err := tx.CreateProject(other); return err }},
		{"Konfiguration ändern", domain.AuditProjectConfigUpdated, domain.AuditLevelConfig, true, func(tx *Tx) error { return tx.UpdateProjectConfig(updatedCfg) }},
		{"Profil", domain.AuditProfileRecorded, domain.AuditLevelConfig, false, func(tx *Tx) error { return tx.RecordProfile(projectID, profile4) }},
		{"Skill-Snapshot", domain.AuditSkillSnapshotRecorded, domain.AuditLevelConfig, false, func(tx *Tx) error {
			return tx.RecordSkillSnapshot(projectID, second, "/work/exec/ccmas/skills/zweiter")
		}},
		{"Agent anlegen", domain.AuditAgentCreated, domain.AuditLevelAgent, true, func(tx *Tx) error { _, err := tx.CreateAgent(projectID, "agent-2", domain.RoleReviewer); return err }},
		{"Verfügbarkeit", domain.AuditAgentAvailability, domain.AuditLevelAgent, true, func(tx *Tx) error {
			return tx.SetAgentAvailability(projectID, "agent-2", domain.AgentAvailable, domain.AgentReserved)
		}},
		{"Ziel anlegen", domain.AuditGoalCreated, domain.AuditLevelGoal, true, func(tx *Tx) error {
			_, err := tx.CreateGoal(GoalRecord{ProjectID: projectID, Goal: goalOf("tk-1"), IntegrationMode: domain.ModeAuto, ParentGoalID: "ms-1"})
			return err
		}},
		{"Nachweise", domain.AuditGoalEvidenceSet, domain.AuditLevelGoal, true, func(tx *Tx) error {
			return tx.SetGoalEvidence(projectID, "tk-1", &supervisor.Evidence{TestEvidence: true})
		}},
		{"Auftrag anlegen", domain.AuditWorkOrderCreated, domain.AuditLevelWorkOrder, true, func(tx *Tx) error {
			_, err := tx.CreateWorkOrder(WorkOrderRecord{WorkOrder: fxWorkOrder(t, "wo-2"), GoalID: "ms-1", Kind: supervisor.OrderImplementation, DependsOn: []string{"wo-1"}})
			return err
		}},
		{"Auftragszustand", domain.AuditWorkOrderStateChanged, domain.AuditLevelWorkOrder, true, func(tx *Tx) error {
			return tx.TransitionWorkOrder(projectID, "wo-2", domain.WOWaiting, domain.WOInProgress)
		}},
		{"Kopf-Commit", domain.AuditWorkOrderHeadSet, domain.AuditLevelWorkOrder, true, func(tx *Tx) error {
			return tx.SetWorkOrderHead(projectID, "wo-2", commitB, "feature/wo-2")
		}},
		{"Rollenergebnis", domain.AuditWorkOrderResultSet, domain.AuditLevelWorkOrder, true, func(tx *Tx) error {
			return tx.SetLastResult(projectID, "wo-2", &supervisor.LastResult{Role: domain.RoleImplementer, Result: "umgesetzt"})
		}},
		{"Reparaturrunden", domain.AuditWorkOrderRepairRounds, domain.AuditLevelWorkOrder, true, func(tx *Tx) error {
			return tx.SetRepairRounds(projectID, "wo-2", 2, 0)
		}},
		{"Konzeptstand", domain.AuditWorkOrderConceptSet, domain.AuditLevelWorkOrder, true, func(tx *Tx) error {
			return tx.SetConceptState(projectID, "wo-2", &supervisor.ConceptState{Version: "2.0.0", Criteria: []string{}})
		}},
		{"Pull Request", domain.AuditWorkOrderPRSet, domain.AuditLevelWorkOrder, true, func(tx *Tx) error {
			return tx.SetPullRequest(projectID, "wo-2", &supervisor.PRInfo{Number: 7, State: supervisor.PROpen, CI: supervisor.CIPending})
		}},
		{"zweiter Auftrag", domain.AuditWorkOrderCreated, domain.AuditLevelWorkOrder, true, func(tx *Tx) error {
			if _, err := tx.CreateWorkOrder(WorkOrderRecord{WorkOrder: fxWorkOrder(t, "wo-3"), GoalID: "ms-1", Kind: supervisor.OrderBug}); err != nil {
				return err
			}
			return nil
		}},
		{"Abhängigkeit hinzufügen", domain.AuditWorkOrderDependency, domain.AuditLevelWorkOrder, true, func(tx *Tx) error {
			return tx.AddDependency(projectID, "wo-3", "wo-2")
		}},
		{"Task", domain.AuditTaskSynced, domain.AuditLevelTask, false, func(tx *Tx) error {
			_, err := tx.SyncTask(TaskRecord{TaskID: "task-1", ProjectID: projectID, AsanaGID: "111", WorkOrderID: "wo-2", Title: "T", SyncedAt: s.now()})
			return err
		}},
		{"Ausführung", domain.AuditExecutionCreated, domain.AuditLevelExecution, true, func(tx *Tx) error {
			return tx.CreateExecution(domain.Execution{ExecutionID: "exec-impl", ProjectID: projectID, WorkOrderID: "wo-1", AgentID: "agent-1", Role: domain.RoleImplementer, CreatedAt: s.now()})
		}},
		{"Versuch starten", domain.AuditAttemptStarted, domain.AuditLevelExecution, true, func(tx *Tx) error {
			return tx.StartAttempt(projectID, domain.ExecutionAttempt{ExecutionID: "exec-impl", Attempt: 1, ProfileID: "impl-default", ProfileVersion: 3,
				State: domain.ExecInitializing, StartedAt: s.now()})
		}},
		{"Ausführungszustand", domain.AuditExecutionStateChanged, domain.AuditLevelExecution, true, func(tx *Tx) error {
			return tx.TransitionExecution(projectID, "exec-impl", 1, domain.ExecInitializing, domain.ExecRunning)
		}},
		{"Session", domain.AuditSessionSet, domain.AuditLevelExecution, false, func(tx *Tx) error {
			return tx.SetSession(projectID, "exec-impl", 1, "sess-1")
		}},
		{"Prozess", domain.AuditProcessObserved, domain.AuditLevelProcess, false, func(tx *Tx) error {
			return tx.RecordProcess(projectID, "exec-impl", 1, domain.ProcessIdentity{PID: 99, StartTime: s.now()}, domain.ProcRunning, s.now())
		}},
		{"Frage", domain.AuditQuestionCreated, domain.AuditLevelQuestion, true, func(tx *Tx) error {
			return tx.CreateQuestion(projectID, domain.Question{QuestionID: "q-1", WorkOrderID: "wo-1", ExecutionID: "exec-impl", Attempt: 1, Text: "?", Context: "c", State: domain.QuestionOpen, CreatedAt: s.now()})
		}},
		{"Antwort", domain.AuditQuestionAnswered, domain.AuditLevelQuestion, true, func(tx *Tx) error {
			return tx.AnswerQuestion(projectID, domain.Answer{AnswerID: "a-1", QuestionID: "q-1", Text: "!", CreatedAt: s.now()})
		}},
		{"Zustellung", domain.AuditQuestionDelivered, domain.AuditLevelQuestion, true, func(tx *Tx) error {
			return tx.ConfirmQuestionDelivery(projectID, "q-1", s.now())
		}},
		{"zweite Frage", domain.AuditQuestionCreated, domain.AuditLevelQuestion, true, func(tx *Tx) error {
			return tx.CreateQuestion(projectID, domain.Question{QuestionID: "q-2", WorkOrderID: "wo-1", ExecutionID: "exec-impl", Attempt: 1, Text: "?", Context: "c", State: domain.QuestionOpen, CreatedAt: s.now()})
		}},
		{"Frage schließen", domain.AuditQuestionStateChanged, domain.AuditLevelQuestion, true, func(tx *Tx) error {
			return tx.CloseQuestion(projectID, "q-2", domain.QuestionOpen, domain.QuestionCancelled)
		}},
		{"Versuch beenden", domain.AuditAttemptFinished, domain.AuditLevelExecution, true, func(tx *Tx) error {
			return tx.FinishAttempt(projectID, "exec-impl", 1, domain.ExecRunning, AttemptResult{
				Terminal: domain.TerminalResult{Attempt: 1, State: domain.ExecCrashed, Source: domain.TerminalFromProcessExit, ExitCode: 0},
				EndedAt:  s.now()})
		}},
		{"Plan", domain.AuditPlanRecorded, domain.AuditLevelDecision, false, func(tx *Tx) error {
			return tx.RecordPlan(PlanRecord{Batch: fxBatch(t), Verdict: supervisor.VerdictAccepted, PlanningSnapshotSHA256: hex64("1")})
		}},
		{"Review", domain.AuditReviewRecorded, domain.AuditLevelReview, true, func(tx *Tx) error {
			rep := domain.ReviewReport{ReportID: "rep-1", WorkOrderID: "wo-1", ReviewerExecutionID: "exec-impl", Commit: commitA,
				ConceptVersion: "1.0.0", Verdict: domain.VerdictApproved, Findings: []domain.Finding{}, CreatedAt: s.now()}
			return tx.RecordReview(projectID, rep, "")
		}},
	}

	total := func() (events int, revs map[string]int64) {
		revs = map[string]int64{}
		mustView(t, s, func(r *ReadTx) error {
			for _, p := range []string{projectID, "other"} {
				evs, err := r.Events(p, EventFilter{})
				if err != nil {
					return err
				}
				events += len(evs)
				if rev, err := r.Revision(p); err == nil {
					revs[p] = rev
				}
			}
			return nil
		})
		return
	}

	for _, st := range steps {
		t.Run(st.name, func(t *testing.T) {
			evBefore, revBefore := total()

			// 1. Fehler im Callback nach der Änderung: nichts bleibt zurück.
			_, err := s.Update(ctxT(t), "tester", func(tx *Tx) error {
				if err := st.fn(tx); err != nil {
					return err
				}
				return errBoom
			})
			if !errors.Is(err, errBoom) {
				t.Fatalf("Update = %v, erwartet den Fehler des Callbacks", err)
			}
			if ev, rev := total(); ev != evBefore || !reflect.DeepEqual(rev, revBefore) {
				t.Fatalf("Rollback hinterließ Spuren: Ereignisse %d -> %d, Revisionen %v -> %v", evBefore, ev, revBefore, rev)
			}

			// 2. Ohne Fehler: genau ein Ereignis, in derselben Transaktion geschrieben.
			res, err := s.Update(ctxT(t), "tester", st.fn)
			if err != nil {
				t.Fatalf("Update: %v", err)
			}
			evAfter, revAfter := total()
			if evAfter != evBefore+1 {
				t.Fatalf("%d neue Ereignisse, erwartet genau 1", evAfter-evBefore)
			}
			if len(res.EventIDs) != 1 {
				t.Fatalf("UpdateResult.EventIDs = %v", res.EventIDs)
			}
			var ev domain.AuditEvent
			mustView(t, s, func(r *ReadTx) error {
				for _, p := range []string{projectID, "other"} {
					evs, _ := r.Events(p, EventFilter{AfterID: res.EventIDs[0] - 1, Limit: 1})
					if len(evs) == 1 {
						ev = evs[0]
					}
				}
				return nil
			})
			if ev.EventID != res.EventIDs[0] || ev.Kind != st.kind || ev.Level != st.level || ev.Actor != "tester" {
				t.Fatalf("Ereignis = %+v, erwartet Art %q Ebene %q", ev, st.kind, st.level)
			}
			if err := ev.Check(); err != nil {
				t.Fatalf("Ereignis verletzt den Vertrag: %v", err)
			}
			// Revision: Änderungen am Planungszustand erhöhen um 1, Journaleinträge nicht.
			wantRev := revBefore[ev.ProjectID]
			if st.bump {
				wantRev++
			}
			if revAfter[ev.ProjectID] != wantRev || ev.StateRevision != wantRev {
				t.Fatalf("Revision %d (Ereignis %d), erwartet %d", revAfter[ev.ProjectID], ev.StateRevision, wantRev)
			}
		})
	}
}

// TestFailedCallPoisonsTransaction: scheitert ein Repository-Aufruf, wird die Transaktion nicht festgeschrieben, auch
// wenn der Callback den Fehler verschluckt. Es bleibt kein halbes Ergebnis zurück.
func TestFailedCallPoisonsTransaction(t *testing.T) {
	s := newStore(t)
	seedBase(t, s)
	evBefore, revBefore := countEvents(t, s), revisionOf(t, s, projectID)

	_, err := s.Update(ctxT(t), "tester", func(tx *Tx) error {
		if _, err := tx.CreateGoal(GoalRecord{ProjectID: projectID, Goal: goalOf("g-new"), IntegrationMode: domain.ModePR}); err != nil {
			return err
		}
		// Doppelte ID scheitert; der Callback ignoriert den Fehler.
		_, _ = tx.CreateGoal(GoalRecord{ProjectID: projectID, Goal: goalOf("ms-1"), IntegrationMode: domain.ModePR})
		return nil
	})
	wantCode(t, err, CodeConflict)
	if countEvents(t, s) != evBefore || revisionOf(t, s, projectID) != revBefore {
		t.Fatal("verschluckter Fehler hinterließ eine teilweise Änderung")
	}
	mustView(t, s, func(r *ReadTx) error {
		if _, err := r.Goal(projectID, "g-new"); CodeOf(err) != CodeNotFound {
			t.Errorf("Ziel g-new existiert: %v", err)
		}
		return nil
	})
}

// TestPanicRollsBack: auch ein Panic im Callback hinterlässt nichts und gibt die Schreibsperre frei.
func TestPanicRollsBack(t *testing.T) {
	s := newStore(t)
	seedBase(t, s)
	evBefore := countEvents(t, s)
	func() {
		defer func() {
			if recover() == nil {
				t.Error("Panic wurde verschluckt")
			}
		}()
		_, _ = s.Update(ctxT(t), "tester", func(tx *Tx) error {
			_, _ = tx.CreateGoal(GoalRecord{ProjectID: projectID, Goal: goalOf("g-panic"), IntegrationMode: domain.ModePR})
			panic("boom")
		})
	}()
	if countEvents(t, s) != evBefore {
		t.Fatal("Panic hinterließ ein Ereignis")
	}
	mustUpdate(t, s, func(tx *Tx) error { return nil }) // Sperre ist frei
}

// TestStateRevision: genau +1 je festgeschriebener Änderungstransaktion; ohne Änderung keine Erhöhung; mehrere
// Änderungen in einer Transaktion zählen einmal; Journaleinträge verändern die Revision nicht.
func TestStateRevision(t *testing.T) {
	s := newStore(t)
	mustUpdate(t, s, func(tx *Tx) error { _, err := tx.CreateProject(fxProject(t)); return err })
	other := fxProject(t)
	other.ProjectID = "other"
	mustUpdate(t, s, func(tx *Tx) error { _, err := tx.CreateProject(other); return err })

	if got := revisionOf(t, s, projectID); got != 0 {
		t.Fatalf("Startrevision = %d, erwartet 0", got)
	}
	rev := func() int64 { return revisionOf(t, s, projectID) }
	goal := func(id string) func(tx *Tx) error {
		return func(tx *Tx) error {
			_, err := tx.CreateGoal(GoalRecord{ProjectID: projectID, Goal: goalOf(id), IntegrationMode: domain.ModePR})
			return err
		}
	}

	// Eine Änderung: +1.
	res := mustUpdate(t, s, goal("g1"))
	if rev() != 1 || res.Revisions[projectID] != 1 {
		t.Fatalf("nach einer Änderung: Revision %d, Ergebnis %v", rev(), res.Revisions)
	}
	// Mehrere Änderungen in einer Transaktion: einmal +1.
	res = mustUpdate(t, s, func(tx *Tx) error {
		for _, id := range []string{"g2", "g3", "g4"} {
			if err := goal(id)(tx); err != nil {
				return err
			}
		}
		return nil
	})
	if rev() != 2 || res.Revisions[projectID] != 2 {
		t.Fatalf("nach drei Änderungen in einer Transaktion: Revision %d", rev())
	}
	// Transaktion ohne Änderung, Nur-Lesen-Zugriff und Fehler: keine Erhöhung.
	res = mustUpdate(t, s, func(tx *Tx) error { _, err := tx.Goal(projectID, "g1"); return err })
	if rev() != 2 || len(res.Revisions) != 0 || len(res.EventIDs) != 0 {
		t.Fatalf("leere Transaktion: Revision %d, Ergebnis %+v", rev(), res)
	}
	mustView(t, s, func(r *ReadTx) error { _, err := r.Goals(projectID); return err })
	_, err := s.Update(ctxT(t), "tester", func(tx *Tx) error {
		if err := goal("g5")(tx); err != nil {
			return err
		}
		return errors.New("abgebrochen")
	})
	if err == nil || rev() != 2 {
		t.Fatalf("abgebrochene Transaktion: Revision %d (%v)", rev(), err)
	}
	// Eine Änderung ohne Wirkung (gleicher Wert) ist keine Änderung.
	mustUpdate(t, s, func(tx *Tx) error {
		return tx.SetGoalEvidence(projectID, "g1", &supervisor.Evidence{TestEvidence: true})
	})
	if rev() != 3 {
		t.Fatalf("Nachweis gesetzt: Revision %d, erwartet 3", rev())
	}
	evs := countEvents(t, s)
	mustUpdate(t, s, func(tx *Tx) error {
		return tx.SetGoalEvidence(projectID, "g1", &supervisor.Evidence{TestEvidence: true})
	})
	if rev() != 3 || countEvents(t, s) != evs {
		t.Fatalf("gleicher Nachweis erneut: Revision %d, Ereignisse %d -> %d", rev(), evs, countEvents(t, s))
	}
	// Journal: ein Plan wird für die Revision aufgezeichnet, auf der er beruht; er ändert sie nicht.
	mustUpdate(t, s, func(tx *Tx) error {
		b := fxBatch(t)
		b.StateRevision = 3
		for i := range b.Decisions {
			b.Decisions[i].StateRevision = 3
		}
		return tx.RecordPlan(PlanRecord{Batch: b, Verdict: supervisor.VerdictAccepted, PlanningSnapshotSHA256: hex64("1")})
	})
	if rev() != 3 {
		t.Fatalf("Plan aufgezeichnet: Revision %d, erwartet 3", rev())
	}
	// Ein anderes Projekt hat seine eigene Revision.
	if got := revisionOf(t, s, "other"); got != 0 {
		t.Fatalf("Revision des anderen Projekts = %d", got)
	}
	// Projekt anlegen und im selben Zug ändern: Revision 0 bleibt, danach +1.
	third := fxProject(t)
	third.ProjectID = "third"
	res = mustUpdate(t, s, func(tx *Tx) error {
		_, err := tx.CreateProject(third)
		return err
	})
	if res.Revisions["third"] != 0 || revisionOf(t, s, "third") != 0 {
		t.Fatalf("neues Projekt: Revision %d", revisionOf(t, s, "third"))
	}
	// Das Ereignis trägt die Revision nach der Änderung.
	if ev := lastEvent(t, s); ev.StateRevision != 3 {
		t.Fatalf("letztes Ereignis des Projekts hat Revision %d", ev.StateRevision)
	}
}

// TestStateLevelsSeparate: Auftrag, Ausführung, Prozess und Agent sind getrennte Ebenen; eine Änderung berührt die
// anderen nicht, jede erzeugt ihr eigenes Ereignis, und ein unerlaubter Übergang ändert nichts.
func TestStateLevelsSeparate(t *testing.T) {
	s := newStore(t)
	seedBase(t, s)
	seedExecutionFor(t, s, "wo-1", "exec-1", domain.RoleImplementer)
	mustUpdate(t, s, func(tx *Tx) error {
		if err := tx.TransitionExecution(projectID, "exec-1", 1, domain.ExecInitializing, domain.ExecRunning); err != nil {
			return err
		}
		return tx.RecordProcess(projectID, "exec-1", 1, domain.ProcessIdentity{PID: 10, StartTime: s.now()}, domain.ProcRunning, s.now())
	})

	type levels struct {
		Order   domain.WorkOrderState
		Exec    domain.ExecutionState
		OS      domain.OSProcessState
		Process *domain.ProcessIdentity
		Agent   domain.AgentAvailability
	}
	read := func() levels {
		var l levels
		mustView(t, s, func(r *ReadTx) error {
			o, err := r.WorkOrder(projectID, "wo-1")
			if err != nil {
				return err
			}
			a, err := r.Attempt(projectID, "exec-1", 1)
			if err != nil {
				return err
			}
			ag, err := r.Agent(projectID, "agent-1")
			if err != nil {
				return err
			}
			l = levels{o.State, a.State, a.OSProcessState, a.Process, ag.Availability}
			return nil
		})
		return l
	}
	base := read()
	if base.Order != domain.WOWaiting || base.Exec != domain.ExecRunning || base.OS != domain.ProcRunning || base.Agent != domain.AgentAvailable {
		t.Fatalf("Ausgangslage = %+v", base)
	}

	change := func(name string, fn func(tx *Tx) error, level domain.AuditLevel, from, to string, mutate func(l *levels)) {
		t.Helper()
		evBefore := countEvents(t, s)
		mustUpdate(t, s, fn)
		want := base
		mutate(&want)
		if got := read(); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: Ebenen = %+v, erwartet %+v (nur eine Ebene ändert sich)", name, got, want)
		}
		base = want
		if got := countEvents(t, s); got != evBefore+1 {
			t.Errorf("%s: %d Ereignisse, erwartet 1", name, got-evBefore)
		}
		ev := lastEvent(t, s)
		if ev.Level != level || ev.FromState != from || ev.ToState != to {
			t.Errorf("%s: Ereignis = %+v, erwartet Ebene %s %s -> %s", name, ev, level, from, to)
		}
	}
	change("Auftrag", func(tx *Tx) error {
		return tx.TransitionWorkOrder(projectID, "wo-1", domain.WOWaiting, domain.WOInProgress)
	}, domain.AuditLevelWorkOrder, "waiting", "in_progress", func(l *levels) { l.Order = domain.WOInProgress })
	change("Ausführung", func(tx *Tx) error {
		return tx.TransitionExecution(projectID, "exec-1", 1, domain.ExecRunning, domain.ExecAwaitingAnswer)
	}, domain.AuditLevelExecution, "running", "awaiting_answer", func(l *levels) { l.Exec = domain.ExecAwaitingAnswer })
	// Ein Prozessende mit Exit 0 beendet weder den Versuch noch den Auftrag.
	change("Prozess", func(tx *Tx) error {
		return tx.RecordProcess(projectID, "exec-1", 1, *base.Process, domain.ProcExited, s.now())
	}, domain.AuditLevelProcess, "running", "exited", func(l *levels) { l.OS = domain.ProcExited })
	change("Agent", func(tx *Tx) error {
		return tx.SetAgentAvailability(projectID, "agent-1", domain.AgentAvailable, domain.AgentReserved)
	}, domain.AuditLevelAgent, "available", "reserved", func(l *levels) { l.Agent = domain.AgentReserved })

	// Unerlaubte Übergänge: Fehlerklasse state, nichts geändert, kein Ereignis, keine Revision.
	frozen := func(name string, fn func(tx *Tx) error, want Code) {
		t.Helper()
		evBefore, revBefore := countEvents(t, s), revisionOf(t, s, projectID)
		_, err := s.Update(ctxT(t), "tester", fn)
		wantCode(t, err, want)
		if want == CodeState {
			var ce *domain.ContractError
			if !errors.As(err, &ce) || ce.Category() != domain.CatState {
				t.Errorf("%s: Fehler %v ist kein ContractError der Klasse state", name, err)
			}
		}
		if got := read(); !reflect.DeepEqual(got, base) {
			t.Errorf("%s: Ebenen verändert: %+v", name, got)
		}
		if countEvents(t, s) != evBefore || revisionOf(t, s, projectID) != revBefore {
			t.Errorf("%s: Ereignis oder Revision trotz Fehler", name)
		}
	}
	frozen("Auftrag in_progress -> completed", func(tx *Tx) error {
		return tx.TransitionWorkOrder(projectID, "wo-1", domain.WOInProgress, domain.WOCompleted)
	}, CodeState)
	frozen("Agent reserved -> reserved", func(tx *Tx) error {
		return tx.SetAgentAvailability(projectID, "agent-1", domain.AgentReserved, domain.AgentReserved)
	}, CodeState)
	frozen("Ausführung awaiting_answer -> initializing", func(tx *Tx) error {
		return tx.TransitionExecution(projectID, "exec-1", 1, domain.ExecAwaitingAnswer, domain.ExecInitializing)
	}, CodeState)
	// Erwarteter Ist-Zustand stimmt nicht: Konflikt.
	frozen("Auftrag mit falschem Ist-Zustand", func(tx *Tx) error {
		return tx.TransitionWorkOrder(projectID, "wo-1", domain.WOWaiting, domain.WOInProgress)
	}, CodeConflict)
	frozen("Auftrag review mit falschem Ist-Zustand", func(tx *Tx) error {
		return tx.TransitionWorkOrder(projectID, "wo-1", domain.WOReview, domain.WOWaitingIntegration)
	}, CodeConflict)
	frozen("Agent mit falschem Ist-Zustand", func(tx *Tx) error {
		return tx.SetAgentAvailability(projectID, "agent-1", domain.AgentAvailable, domain.AgentReserved)
	}, CodeConflict)
	frozen("Ausführung mit falschem Ist-Zustand", func(tx *Tx) error {
		return tx.TransitionExecution(projectID, "exec-1", 1, domain.ExecRunning, domain.ExecStopping)
	}, CodeConflict)
	// Endzustände erreicht man nur über FinishAttempt, mit zum Zustand passendem terminalem Ergebnis.
	frozen("terminaler Zustand über TransitionExecution", func(tx *Tx) error {
		return tx.TransitionExecution(projectID, "exec-1", 1, domain.ExecAwaitingAnswer, domain.ExecSucceeded)
	}, CodeInvalid)
	frozen("Ergebnis passt nicht zum Zustand", func(tx *Tx) error {
		return tx.FinishAttempt(projectID, "exec-1", 1, domain.ExecAwaitingAnswer, AttemptResult{
			Terminal: domain.TerminalResult{Attempt: 1, State: domain.ExecSucceeded, Source: domain.TerminalFromProcessExit}, EndedAt: s.now()})
	}, CodeInvalid)
	frozen("Beenden aus awaiting_answer mit Abbruch", func(tx *Tx) error {
		return tx.FinishAttempt(projectID, "exec-1", 1, domain.ExecAwaitingAnswer, AttemptResult{
			Terminal: domain.TerminalResult{Attempt: 1, State: domain.ExecCancelled, Source: domain.TerminalFromResult},
			Outcome:  domain.OutcomeCancelled, EndedAt: s.now()})
	}, CodeState)
	// Prozessidentität gehört zum Versuch: eine andere PID ist kein Update derselben Identität.
	frozen("andere Prozessidentität", func(tx *Tx) error {
		return tx.RecordProcess(projectID, "exec-1", 1, domain.ProcessIdentity{PID: 11, StartTime: s.now()}, domain.ProcRunning, s.now())
	}, CodeConflict)
}
