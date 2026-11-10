package persistence

import (
	"testing"
	"time"

	"github.com/alexwenzel/ccmas/internal/domain"
	"github.com/alexwenzel/ccmas/internal/supervisor"
)

// world ist alles, was ein Neustart unverändert erhalten muss, gelesen über die Lese-API.
type world struct {
	Project    ProjectRecord
	Profiles   []domain.AgentProfile
	Snapshot   SkillSnapshotRecord
	Agents     []domain.Agent
	Goals      []GoalRecord
	Orders     []WorkOrderRecord
	Tasks      []TaskRecord
	Executions []domain.Execution
	Attempts   []domain.ExecutionAttempt
	Questions  []domain.Question
	Answers    []domain.Answer
	Reviews    []supervisor.ReviewRecord
	Plans      []PlanRecord
	Decisions  []DecisionRecord
	Active     []supervisor.ActiveExecution
	Open       []supervisor.OpenQuestion
	Events     []domain.AuditEvent
	Revision   int64
}

func readWorld(t *testing.T, s *Store) world {
	t.Helper()
	var w world
	mustView(t, s, func(r *ReadTx) error {
		var err error
		if w.Project, err = r.Project(projectID); err != nil {
			return err
		}
		if w.Profiles, err = r.Profiles(projectID); err != nil {
			return err
		}
		if w.Snapshot, err = r.SkillSnapshot(projectID, fxSnapshot(t).SnapshotID); err != nil {
			return err
		}
		if w.Agents, err = r.Agents(projectID); err != nil {
			return err
		}
		if w.Goals, err = r.Goals(projectID); err != nil {
			return err
		}
		if w.Orders, err = r.WorkOrders(projectID); err != nil {
			return err
		}
		if w.Tasks, err = r.Tasks(projectID); err != nil {
			return err
		}
		if w.Executions, err = r.Executions(projectID, ""); err != nil {
			return err
		}
		for _, e := range w.Executions {
			as, err := r.Attempts(projectID, e.ExecutionID)
			if err != nil {
				return err
			}
			w.Attempts = append(w.Attempts, as...)
		}
		if w.Questions, err = r.Questions(projectID); err != nil {
			return err
		}
		for _, q := range w.Questions {
			if q.AnswerID == "" {
				continue
			}
			a, err := r.Answer(projectID, q.QuestionID)
			if err != nil {
				return err
			}
			w.Answers = append(w.Answers, a)
		}
		for _, o := range w.Orders {
			rs, err := r.Reviews(projectID, o.WorkOrderID)
			if err != nil {
				return err
			}
			w.Reviews = append(w.Reviews, rs...)
		}
		if w.Plans, err = r.Plans(projectID); err != nil {
			return err
		}
		for _, p := range w.Plans {
			ds, err := r.Decisions(projectID, p.Batch.BatchID)
			if err != nil {
				return err
			}
			w.Decisions = append(w.Decisions, ds...)
		}
		if w.Active, err = r.ActiveExecutions(projectID); err != nil {
			return err
		}
		if w.Open, err = r.OpenQuestions(projectID); err != nil {
			return err
		}
		if w.Events, err = r.Events(projectID, EventFilter{}); err != nil {
			return err
		}
		w.Revision, err = r.Revision(projectID)
		return err
	})
	return w
}

// expectations sind die Werte, die buildWorld hineingeschrieben hat und die ein Neustart unverändert liefern muss.
type expectations struct {
	order2      WorkOrderRecord // ohne Revision und Zeiten
	order2Rev   int64
	task        TaskRecord
	batch       domain.DecisionBatch
	rejections  []supervisor.Rejection
	review      domain.ReviewReport
	evidence    supervisor.Evidence
	question1   domain.Question
	answer1     domain.Answer
	attemptSpec string
}

var (
	rejectionList = []supervisor.Rejection{
		{Code: supervisor.CodeStaleRevision, Reason: "Zustandsrevision 7 ist veraltet, aktuell ist 9."},
		{Code: supervisor.CodePreconditionUnmet, DecisionID: "dec-1", Reason: "Konzept nicht freigegeben."},
	}
	evidenceFull = supervisor.Evidence{
		Integration: "merged", MergedBy: "human", AcceptanceCriteriaMet: true, ReviewEvidence: true, TestEvidence: true,
		CIEvidence: true, TaskStateUpdated: true, KnowledgeUpdated: false, ChildrenComplete: true, OwnerAcceptance: false,
	}
)

// buildWorld legt einen Zustand an, der jede Entität berührt: Projekt, Profil, Snapshot, Agent, Ziel mit Nachweisen, zwei
// Aufträge (Abhängigkeit, Konzeptstand mit Freigabe, PR, Ergebnis, Reparaturrunden), Asana-Abbild, zwei Ausführungen,
// eine beantwortete und zugestellte sowie eine offene Frage, einen abgelehnten Plan mit Delegationsprofilen und einen
// Review-Bericht mit Implementierer-Ausführung.
func buildWorld(t *testing.T, s *Store) expectations {
	t.Helper()
	seedBase(t, s)
	var ex expectations

	ex.order2 = WorkOrderRecord{
		WorkOrder: fxWorkOrder(t, "wo-2"), GoalID: "ms-1", Kind: supervisor.OrderBug, Title: "Auftrag 2",
		DependsOn: []string{"wo-1"},
	}
	ex.order2.Target = domain.WorkOrderTarget{Kind: domain.KindFeature, Ref: "feat-1"}
	ex.order2.Mode = domain.ModeAuto
	ex.order2.HeadCommit, ex.order2.Branch = commitA, "feature/wo-2"
	ex.order2.LastResult = &supervisor.LastResult{Role: domain.RoleImplementer, Result: "umgesetzt"}
	ex.order2.CodeRepairRounds, ex.order2.ConceptRepairRounds = 1, 2
	ex.order2.Concept = &supervisor.ConceptState{
		Version: "1.2.0", Class: "routine", Criteria: []string{"k1", "k2"},
		Approval: &supervisor.ConceptApprovalInfo{Version: "1.2.0", Approver: "concept_reviewer"},
	}
	ex.order2.PR = &supervisor.PRInfo{Number: 42, State: supervisor.PROpen, CI: supervisor.CIPassed, HeadCommit: commitA}
	ex.order2.State = domain.WOWaiting

	mustUpdate(t, s, func(tx *Tx) error {
		o := ex.order2
		o.HeadCommit, o.Branch, o.LastResult, o.CodeRepairRounds, o.ConceptRepairRounds, o.Concept, o.PR = "", "", nil, 0, 0, nil, nil
		if _, err := tx.CreateWorkOrder(o); err != nil {
			return err
		}
		if err := tx.SetWorkOrderHead(projectID, "wo-2", commitA, "feature/wo-2"); err != nil {
			return err
		}
		return nil
	})
	mustUpdate(t, s, func(tx *Tx) error {
		if err := tx.SetLastResult(projectID, "wo-2", ex.order2.LastResult); err != nil {
			return err
		}
		if err := tx.SetRepairRounds(projectID, "wo-2", 1, 2); err != nil {
			return err
		}
		if err := tx.SetConceptState(projectID, "wo-2", ex.order2.Concept); err != nil {
			return err
		}
		return tx.SetPullRequest(projectID, "wo-2", ex.order2.PR)
	})
	mustUpdate(t, s, func(tx *Tx) error {
		if err := tx.TransitionWorkOrder(projectID, "wo-2", domain.WOWaiting, domain.WOInProgress); err != nil {
			return err
		}
		return tx.TransitionWorkOrder(projectID, "wo-2", domain.WOInProgress, domain.WOWaiting)
	})
	ex.order2Rev = 1 + 1 + 4 + 2 // anlegen, Kopf, Ergebnis/Runden/Konzept/PR, zwei Übergänge

	ex.task = TaskRecord{TaskID: "task-1", ProjectID: projectID, AsanaGID: "1220366216593996", WorkOrderID: "wo-2",
		Title: "CC-27 SQLite", Priority: "hoch", Section: "In Progress", SyncedAt: time.Date(2026, 11, 10, 11, 0, 0, 5, time.UTC)}
	mustUpdate(t, s, func(tx *Tx) error {
		got, err := tx.SyncTask(ex.task)
		ex.task = got
		return err
	})

	// Ausführung des Implementierers: Versuch 1 läuft, stellt eine Frage, die beantwortet und zugestellt wird, und endet erfolgreich.
	ex.attemptSpec = `{"role":"implementer","prompt_bytes":120,"prompt_sha256":"` + hex64("c") + `","model":"model-a"}`
	mustUpdate(t, s, func(tx *Tx) error {
		if err := tx.CreateExecution(domain.Execution{ExecutionID: "exec-impl", ProjectID: projectID, WorkOrderID: "wo-1",
			AgentID: "agent-1", Role: domain.RoleImplementer, CreatedAt: s.now()}); err != nil {
			return err
		}
		return tx.StartAttempt(projectID, domain.ExecutionAttempt{ExecutionID: "exec-impl", Attempt: 1, ProfileID: "impl-default",
			ProfileVersion: 3, SkillSnapshotID: fxSnapshot(t).SnapshotID, State: domain.ExecInitializing,
			Worktree: "/work/exec/ccmas/wo-1", Branch: "feature/wo-1", StartedAt: s.now(), Spec: []byte(ex.attemptSpec)})
	})
	mustUpdate(t, s, func(tx *Tx) error {
		if err := tx.TransitionExecution(projectID, "exec-impl", 1, domain.ExecInitializing, domain.ExecRunning); err != nil {
			return err
		}
		if err := tx.SetSession(projectID, "exec-impl", 1, "sess-42"); err != nil {
			return err
		}
		return tx.RecordProcess(projectID, "exec-impl", 1, domain.ProcessIdentity{PID: 4711, StartTime: time.Date(2026, 11, 10, 12, 0, 1, 7, time.UTC)},
			domain.ProcSleeping, time.Date(2026, 11, 10, 12, 0, 2, 9, time.UTC))
	})
	ex.question1 = domain.Question{QuestionID: "q-1", WorkOrderID: "wo-1", ExecutionID: "exec-impl", Attempt: 1, Text: "Welche Variante?",
		Context: "Zwei Möglichkeiten.", Recommendation: "Variante A.", State: domain.QuestionOpen, CreatedAt: time.Date(2026, 11, 10, 12, 5, 0, 1, time.UTC)}
	mustUpdate(t, s, func(tx *Tx) error {
		if err := tx.CreateQuestion(projectID, ex.question1); err != nil {
			return err
		}
		return tx.TransitionExecution(projectID, "exec-impl", 1, domain.ExecRunning, domain.ExecAwaitingAnswer)
	})
	ex.answer1 = domain.Answer{AnswerID: "a-1", QuestionID: "q-1", Text: "Variante A.", CreatedAt: time.Date(2026, 11, 10, 12, 6, 0, 2, time.UTC)}
	mustUpdate(t, s, func(tx *Tx) error { return tx.AnswerQuestion(projectID, ex.answer1) })
	confirm := time.Date(2026, 11, 10, 12, 7, 0, 3, time.UTC)
	mustUpdate(t, s, func(tx *Tx) error {
		if err := tx.ConfirmQuestionDelivery(projectID, "q-1", confirm); err != nil {
			return err
		}
		return tx.TransitionExecution(projectID, "exec-impl", 1, domain.ExecAwaitingAnswer, domain.ExecRunning)
	})
	ex.question1.State, ex.question1.AnswerID, ex.question1.DeliveryConfirmedAt = domain.QuestionDelivered, "a-1", confirm
	ex.answer1.DeliveryConfirmedAt = &confirm
	mustUpdate(t, s, func(tx *Tx) error {
		return tx.FinishAttempt(projectID, "exec-impl", 1, domain.ExecRunning, AttemptResult{
			Terminal: domain.TerminalResult{Attempt: 1, State: domain.ExecSucceeded, Source: domain.TerminalFromResult},
			Outcome:  domain.OutcomeSucceeded, Summary: "Umsetzung abgeschlossen.", EndedAt: time.Date(2026, 11, 10, 12, 30, 0, 4, time.UTC)})
	})

	// Reviewer-Ausführung: läuft noch, mit einer offenen Frage.
	mustUpdate(t, s, func(tx *Tx) error {
		if err := tx.CreateExecution(domain.Execution{ExecutionID: "exec-rev", ProjectID: projectID, WorkOrderID: "wo-1",
			Role: domain.RoleReviewer, CreatedAt: s.now()}); err != nil {
			return err
		}
		if err := tx.StartAttempt(projectID, domain.ExecutionAttempt{ExecutionID: "exec-rev", Attempt: 1, ProfileID: "reviewer:rev-1",
			ProfileVersion: 7, State: domain.ExecInitializing, StartedAt: s.now()}); err != nil {
			return err
		}
		if err := tx.TransitionExecution(projectID, "exec-rev", 1, domain.ExecInitializing, domain.ExecRunning); err != nil {
			return err
		}
		return tx.CreateQuestion(projectID, domain.Question{QuestionID: "q-2", WorkOrderID: "wo-1", ExecutionID: "exec-rev", Attempt: 1,
			Text: "Darf ich nachfragen?", Context: "Kontext.", State: domain.QuestionOpen, CreatedAt: time.Date(2026, 11, 10, 12, 40, 0, 0, time.UTC)})
	})

	// Review-Bericht für wo-1: Reviewer exec-rev, Implementierer exec-impl.
	rep, err := domain.ParseReviewReport(fixture(t, "review-report--findings.json"))
	must(t, err)
	rep.ReviewerExecutionID = "exec-rev"
	ex.review = rep
	mustUpdate(t, s, func(tx *Tx) error { return tx.RecordReview(projectID, rep, "exec-impl") })

	// Abgelehnter Plan mit Delegationsprofilen.
	ex.batch = fxBatch(t)
	ex.rejections = rejectionList
	mustUpdate(t, s, func(tx *Tx) error {
		return tx.RecordPlan(PlanRecord{
			Batch: ex.batch, Verdict: supervisor.VerdictRejected, Rejections: ex.rejections,
			PlanningSnapshotSHA256: hex64("1"), CurrentSnapshotSHA256: hex64("2"),
			Delegations: map[string]domain.AgentProfile{"dec-1": fxProfile(t), "dec-2": fxConceptProfile(t)},
		})
	})

	ex.evidence = evidenceFull
	mustUpdate(t, s, func(tx *Tx) error { return tx.SetGoalEvidence(projectID, "ms-1", &ex.evidence) })
	return ex
}

func hex64(c string) string {
	out := ""
	for len(out) < 64 {
		out += c
	}
	return out[:64]
}
