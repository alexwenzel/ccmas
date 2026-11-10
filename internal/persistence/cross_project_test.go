package persistence

import (
	"strings"
	"testing"

	"github.com/alexwenzel/ccmas/internal/domain"
	"github.com/alexwenzel/ccmas/internal/supervisor"
)

const otherProject = "other"

// seedOtherProject legt neben dem Projekt von seedBase ein zweites, vollständig bestücktes Projekt an: Profil, Agent,
// Ziel, Auftrag, Ausführung mit Versuch, eine beantwortete und eine offene Frage. Alle Bezüge darin sind in sich stimmig.
func seedOtherProject(t *testing.T, s *Store) {
	t.Helper()
	cfg := fxProject(t)
	cfg.ProjectID = otherProject
	mustUpdate(t, s, func(tx *Tx) error {
		if _, err := tx.CreateProject(cfg); err != nil {
			return err
		}
		if err := tx.RecordProfile(otherProject, fxProfile(t)); err != nil {
			return err
		}
		if _, err := tx.CreateAgent(otherProject, "agent-o", domain.RoleImplementer); err != nil {
			return err
		}
		_, err := tx.CreateGoal(GoalRecord{
			ProjectID: otherProject, Goal: supervisor.Goal{Kind: domain.KindMilestone, ID: "ms-o", Title: "Meilenstein o",
				AcceptanceCriteria: []string{"A1"}, Scope: []string{"CC-1"}, OutOfScope: []string{"CC-9"}},
			IntegrationMode: domain.ModePR,
		})
		return err
	})
	mustUpdate(t, s, func(tx *Tx) error {
		w := fxWorkOrder(t, "wo-o")
		w.ProjectID = otherProject
		_, err := tx.CreateWorkOrder(WorkOrderRecord{WorkOrder: w, GoalID: "ms-o", Kind: supervisor.OrderImplementation, Title: "Auftrag o"})
		return err
	})
	profile := fxProfile(t)
	mustUpdate(t, s, func(tx *Tx) error {
		if err := tx.CreateExecution(domain.Execution{ExecutionID: "exec-o", ProjectID: otherProject, WorkOrderID: "wo-o", Role: domain.RoleImplementer, CreatedAt: s.now()}); err != nil {
			return err
		}
		if err := tx.StartAttempt(otherProject, domain.ExecutionAttempt{ExecutionID: "exec-o", Attempt: 1, ProfileID: profile.ProfileID,
			ProfileVersion: profile.Version, State: domain.ExecInitializing, StartedAt: s.now()}); err != nil {
			return err
		}
		for _, id := range []string{"q-o", "q-o2"} { // q-o wird beantwortet, q-o2 bleibt offen
			if err := tx.CreateQuestion(otherProject, domain.Question{QuestionID: id, WorkOrderID: "wo-o", ExecutionID: "exec-o", Attempt: 1,
				Text: "?", Context: "c", State: domain.QuestionOpen, CreatedAt: s.now()}); err != nil {
				return err
			}
		}
		return nil
	})
	mustUpdate(t, s, func(tx *Tx) error {
		return tx.AnswerQuestion(otherProject, domain.Answer{AnswerID: "a-o", QuestionID: "q-o", Text: "ja", CreatedAt: s.now()})
	})
}

// wantFKViolation verlangt, dass die Datenbank die Anweisung wegen eines Fremdschlüssels abweist (nicht nur irgendeine
// Einschränkung): sonst könnte ein anderer Fehler des Testaufbaus die Abweisung vortäuschen.
func wantFKViolation(t *testing.T, s *Store, name, stmt string, args ...any) {
	t.Helper()
	err := execSQL(t, s, stmt, args...)
	if err == nil {
		t.Errorf("%s: Bezug über die Projektgrenze angenommen", name)
		return
	}
	if CodeOf(mapErr(err)) != CodeConstraint || !strings.Contains(err.Error(), "FOREIGN KEY") {
		t.Errorf("%s: %v (erwartet: Fremdschlüssel-Verletzung)", name, err)
	}
}

const (
	insertLease = `INSERT INTO leases (lease_id, project_id, work_order_id, execution_id, attempt, agent_id, generation, state,
		worktree_path, acquired_at, expires_at, released_at) VALUES (?, ?, ?, ?, ?, ?, 1, 'released', '/w/x', ?, ?, ?)`
	insertReview = `INSERT INTO review_reports (report_id, project_id, work_order_id, reviewer_execution_id, implementer_execution_id,
		commit_sha, concept_version, verdict, findings_json, report_json, created_at)
		VALUES (?, ?, ?, ?, NULL, 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa', '1.0.0', 'FREIGEGEBEN', '[]', '{"report_id":"' || ? || '"}', ?)`
	insertQuestion = `INSERT INTO questions (question_id, project_id, work_order_id, execution_id, attempt, text, context, state, created_at)
		VALUES (?, ?, ?, ?, 1, 't', 'c', 'open', ?)`
	insertExecution = `INSERT INTO executions (execution_id, project_id, work_order_id, agent_id, role, created_at) VALUES (?, ?, ?, ?, 'implementer', ?)`
	insertAttempt   = `INSERT INTO execution_attempts (project_id, execution_id, attempt, profile_id, profile_version, skill_snapshot_id, state, started_at)
		VALUES (?, ?, ?, 'impl-default', 3, ?, 'initializing', ?)`
)

// TestForeignKeysAcrossProjects: Bezüge über die Grenze zweier vorhandener Projekte weist die Datenbank ab (Auftrag,
// Abhängigkeit, Ausführung, Versuch, Review, Lease, Frage). Jede Zeile hat eine Gegenprobe im selben Projekt, die
// gelingt: so liegt die Abweisung am Projektbezug und nicht an der Form der Anweisung.
func TestForeignKeysAcrossProjects(t *testing.T) {
	s := newStore(t)
	seedBase(t, s)
	seedOtherProject(t, s)
	snap := fxSnapshot(t).SnapshotID

	// Auftrag: Ziel eines anderen Projekts (über das Repository und direkt).
	_, err := s.Update(ctxT(t), "tester", func(tx *Tx) error {
		_, err := tx.CreateWorkOrder(WorkOrderRecord{WorkOrder: fxWorkOrder(t, "wo-x"), GoalID: "ms-o", Kind: supervisor.OrderBug})
		return err
	})
	wantCode(t, err, CodeConstraint)
	wantFKViolation(t, s, "Auftrag: Ziel eines anderen Projekts", `INSERT INTO work_orders (work_order_id, project_id, goal_id, target_kind, target_ref, user_order, goal,
		acceptance_criteria_json, allowed_scope_json, mode, plan_version, state, order_kind, title, head_commit, branch,
		code_repair_rounds, concept_repair_rounds, revision, created_at, updated_at)
		SELECT 'wo-y', 'ccmas', 'ms-o', target_kind, target_ref, user_order, goal, acceptance_criteria_json, allowed_scope_json, mode,
		plan_version, state, order_kind, title, head_commit, branch, code_repair_rounds, concept_repair_rounds, 1, created_at, updated_at
		FROM work_orders WHERE work_order_id = 'wo-1'`)

	// Abhängigkeit.
	wantFKViolation(t, s, "Abhängigkeit auf Auftrag eines anderen Projekts",
		"INSERT INTO work_order_dependencies (project_id, work_order_id, depends_on_id) VALUES ('ccmas', 'wo-1', 'wo-o')")
	wantFKViolation(t, s, "Abhängigkeit von Auftrag eines anderen Projekts",
		"INSERT INTO work_order_dependencies (project_id, work_order_id, depends_on_id) VALUES ('other', 'wo-o', 'wo-1')")

	// Ausführung: Auftrag oder Agent eines anderen Projekts.
	wantFKViolation(t, s, "Ausführung: Auftrag eines anderen Projekts", insertExecution, "e-1", "ccmas", "wo-o", nil, ts)
	wantFKViolation(t, s, "Ausführung: Agent eines anderen Projekts", insertExecution, "e-2", "ccmas", "wo-1", "agent-o", ts)
	if err := execSQL(t, s, insertExecution, "e-ok", "ccmas", "wo-1", "agent-1", ts); err != nil {
		t.Fatalf("Gegenprobe Ausführung: %v", err)
	}

	// Versuch: Ausführung eines anderen Projekts; Skill-Snapshot eines anderen Projekts.
	wantFKViolation(t, s, "Versuch: Ausführung eines anderen Projekts", insertAttempt, "ccmas", "exec-o", 2, nil, ts)
	wantFKViolation(t, s, "Versuch: Skill-Snapshot eines anderen Projekts", insertAttempt, "other", "exec-o", 2, snap, ts)
	if err := execSQL(t, s, insertAttempt, "other", "exec-o", 2, nil, ts); err != nil {
		t.Fatalf("Gegenprobe Versuch: %v", err)
	}

	// Review: Auftrag oder Ausführung eines anderen Projekts.
	wantFKViolation(t, s, "Review: Auftrag eines anderen Projekts", insertReview, "r-1", "ccmas", "wo-o", "e-ok", "r-1", ts)
	wantFKViolation(t, s, "Review: Ausführung eines anderen Projekts", insertReview, "r-2", "ccmas", "wo-1", "exec-o", "r-2", ts)
	if err := execSQL(t, s, insertReview, "r-ok", "other", "wo-o", "exec-o", "r-ok", ts); err != nil {
		t.Fatalf("Gegenprobe Review: %v", err)
	}

	// Lease: Auftrag, Agent oder Versuch eines anderen Projekts.
	wantFKViolation(t, s, "Lease: Auftrag eines anderen Projekts", insertLease, "l-1", "ccmas", "wo-o", nil, nil, "agent-1", ts, ts, ts)
	wantFKViolation(t, s, "Lease: Agent eines anderen Projekts", insertLease, "l-2", "ccmas", "wo-1", nil, nil, "agent-o", ts, ts, ts)
	wantFKViolation(t, s, "Lease: Versuch eines anderen Projekts", insertLease, "l-3", "ccmas", "wo-1", "exec-o", 1, "agent-1", ts, ts, ts)
	if err := execSQL(t, s, insertLease, "l-ok", "other", "wo-o", "exec-o", 1, "agent-o", ts, ts, ts); err != nil {
		t.Fatalf("Gegenprobe Lease: %v", err)
	}

	// Frage: Auftrag oder Versuch eines anderen Projekts.
	wantFKViolation(t, s, "Frage: Auftrag eines anderen Projekts", insertQuestion, "q-1", "ccmas", "wo-o", "e-ok", ts)
	wantFKViolation(t, s, "Frage: Versuch eines anderen Projekts", insertQuestion, "q-2", "ccmas", "wo-1", "exec-o", ts)

	// Antwort: Frage eines anderen Projekts.
	wantFKViolation(t, s, "Antwort: Frage eines anderen Projekts",
		"INSERT INTO answers (answer_id, project_id, question_id, text, created_at, actor) VALUES ('a-x', 'ccmas', 'q-o2', 't', ?, 'tester')", ts)
}

// TestQuestionAnswerForeignKey: die Antwort einer Frage gehört zu genau dieser Frage und diesem Projekt. Die Datenbank
// weist eine Antwort einer anderen Frage, eines anderen Projekts und eine nicht vorhandene Antwort ab; die Reihenfolge
// Frage, Antwort, Aktualisierung der Frage funktioniert weiter.
func TestQuestionAnswerForeignKey(t *testing.T) {
	s := newStore(t)
	seedBase(t, s)
	seedOtherProject(t, s)
	seedExecutionFor(t, s, "wo-1", "exec-1", domain.RoleImplementer)
	mustUpdate(t, s, func(tx *Tx) error {
		for _, id := range []string{"q-1", "q-2"} {
			if err := tx.CreateQuestion(projectID, domain.Question{QuestionID: id, WorkOrderID: "wo-1", ExecutionID: "exec-1", Attempt: 1,
				Text: "?", Context: "c", State: domain.QuestionOpen, CreatedAt: s.now()}); err != nil {
				return err
			}
		}
		return nil
	})
	// Die normale Reihenfolge über das Repository (Antwort einfügen, dann die Frage aktualisieren) gelingt.
	mustUpdate(t, s, func(tx *Tx) error {
		return tx.AnswerQuestion(projectID, domain.Answer{AnswerID: "a-1", QuestionID: "q-1", Text: "ja", CreatedAt: s.now()})
	})
	mustView(t, s, func(r *ReadTx) error {
		q, err := r.Question(projectID, "q-1")
		if err == nil && (q.AnswerID != "a-1" || q.State != domain.QuestionAnswered) {
			t.Errorf("Frage nach Antwort: %+v", q)
		}
		return err
	})

	const setAnswer = "UPDATE questions SET state = 'answered', answer_id = ? WHERE project_id = 'ccmas' AND question_id = ?"
	wantFKViolation(t, s, "Antwort einer anderen Frage", setAnswer, "a-1", "q-2")
	wantFKViolation(t, s, "nicht vorhandene Antwort", setAnswer, "a-gibt-es-nicht", "q-2")
	wantFKViolation(t, s, "Antwort eines anderen Projekts", setAnswer, "a-o", "q-2")
	wantFKViolation(t, s, "Antwort eines anderen Projekts auf eigener Frage", setAnswer, "a-o", "q-1")
	// Auch beim Anlegen einer Frage mit gesetzter Antwort.
	wantFKViolation(t, s, "Frage mit nicht vorhandener Antwort", `INSERT INTO questions (question_id, project_id, work_order_id, execution_id, attempt,
		text, context, state, created_at, answer_id) VALUES ('q-3', 'ccmas', 'wo-1', 'exec-1', 1, 't', 'c', 'answered', ?, 'a-nope')`, ts)

	// Nichts davon hat die Fragen verändert: q-2 ist offen und ohne Antwort.
	mustView(t, s, func(r *ReadTx) error {
		q, err := r.Question(projectID, "q-2")
		if err == nil && (q.State != domain.QuestionOpen || q.AnswerID != "") {
			t.Errorf("q-2 verändert: %+v", q)
		}
		return err
	})

	// Die zweite Frage lässt sich danach regulär beantworten.
	mustUpdate(t, s, func(tx *Tx) error {
		return tx.AnswerQuestion(projectID, domain.Answer{AnswerID: "a-2", QuestionID: "q-2", Text: "nein", CreatedAt: s.now()})
	})
}
