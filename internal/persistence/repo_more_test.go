package persistence

import (
	"errors"
	"strings"
	"testing"

	"github.com/alexwenzel/ccmas/internal/domain"
	"github.com/alexwenzel/ccmas/internal/supervisor"
)

// TestDecisionIDScopedByBatch: decision_id stammt vom Modell und ist nur innerhalb eines Batches eindeutig.
func TestDecisionIDScopedByBatch(t *testing.T) {
	s := newStore(t)
	seedBase(t, s)
	b1, b2 := fxBatch(t), fxBatch(t)
	b2.BatchID = "batch-2" // dieselben decision_ids dec-1..dec-3
	mustUpdate(t, s, func(tx *Tx) error {
		if err := tx.RecordPlan(PlanRecord{Batch: b1, Verdict: supervisor.VerdictAccepted, PlanningSnapshotSHA256: hex64("1")}); err != nil {
			return err
		}
		return tx.RecordPlan(PlanRecord{Batch: b2, Verdict: supervisor.VerdictAccepted, PlanningSnapshotSHA256: hex64("1")})
	})
	mustView(t, s, func(r *ReadTx) error {
		for _, id := range []string{"batch-1", "batch-2"} {
			ds, err := r.Decisions(projectID, id)
			if err != nil {
				return err
			}
			if len(ds) != 3 || ds[0].Decision.DecisionID != "dec-1" || ds[0].Validation != supervisor.VerdictAccepted {
				t.Errorf("Batch %s: Entscheidungen = %+v", id, ds)
			}
		}
		return nil
	})
	// Derselbe Batch zweimal ist ein Konflikt, nicht ein stilles Überschreiben.
	_, err := s.Update(ctxT(t), "tester", func(tx *Tx) error {
		return tx.RecordPlan(PlanRecord{Batch: b1, Verdict: supervisor.VerdictAccepted, PlanningSnapshotSHA256: hex64("1")})
	})
	wantCode(t, err, CodeConflict)

	// Doppelte decision_id im selben Batch: nur als abgelehnter Plan mit dem passenden Code festzuhalten.
	dup := fxBatch(t)
	dup.BatchID = "batch-dup"
	dup.Decisions[1].DecisionID = "dec-1"
	_, err = s.Update(ctxT(t), "tester", func(tx *Tx) error {
		return tx.RecordPlan(PlanRecord{Batch: dup, Verdict: supervisor.VerdictAccepted, PlanningSnapshotSHA256: hex64("1")})
	})
	wantCode(t, err, CodeInvalid)
	mustUpdate(t, s, func(tx *Tx) error {
		return tx.RecordPlan(PlanRecord{Batch: dup, Verdict: supervisor.VerdictRejected, PlanningSnapshotSHA256: hex64("1"),
			Rejections: []supervisor.Rejection{{Code: supervisor.CodeDuplicateDecisionID, DecisionID: "dec-1", Reason: "doppelt"}}})
	})
	mustView(t, s, func(r *ReadTx) error {
		p, err := r.Plan(projectID, "batch-dup")
		if err != nil || p.Verdict != supervisor.VerdictRejected || len(p.Batch.Decisions) != 3 {
			t.Errorf("Plan = %+v (%v)", p, err)
		}
		if ds, _ := r.Decisions(projectID, "batch-dup"); len(ds) != 0 {
			t.Errorf("%d Entscheidungszeilen für einen Batch mit doppelter decision_id", len(ds))
		}
		return nil
	})
}

// TestPlanValidation: Pläne werden vor dem Speichern gegen das Schema und die Konsistenz von Urteil und Ablehnungen geprüft.
func TestPlanValidation(t *testing.T) {
	s := newStore(t)
	seedBase(t, s)
	good := func() PlanRecord {
		return PlanRecord{Batch: fxBatch(t), Verdict: supervisor.VerdictAccepted, PlanningSnapshotSHA256: hex64("1")}
	}
	cases := map[string]func(p *PlanRecord){
		"Schemaverstoß":                       func(p *PlanRecord) { p.Batch.Decisions[0].Action = "teleport" },
		"fremdes Projekt":                     func(p *PlanRecord) { p.Batch.ProjectID = "fremd" },
		"angenommen mit Ablehnung":            func(p *PlanRecord) { p.Rejections = []supervisor.Rejection{{Code: supervisor.CodeSchema, Reason: "x"}} },
		"abgelehnt ohne Ablehnung":            func(p *PlanRecord) { p.Verdict = supervisor.VerdictRejected },
		"unbekanntes Urteil":                  func(p *PlanRecord) { p.Verdict = supervisor.VerdictNoOutput },
		"Revision der Entscheidung weicht ab": func(p *PlanRecord) { p.Batch.Decisions[1].StateRevision = 99 },
		"Hash fehlt":                          func(p *PlanRecord) { p.PlanningSnapshotSHA256 = "" },
		"Hash kein Hex":                       func(p *PlanRecord) { p.CurrentSnapshotSHA256 = strings.Repeat("z", 64) },
		"Delegation ohne Entscheidung": func(p *PlanRecord) {
			p.Delegations = map[string]domain.AgentProfile{"dec-99": fxProfile(t)}
		},
		"Delegation mit falscher Rolle": func(p *PlanRecord) {
			p.Delegations = map[string]domain.AgentProfile{"dec-1": fxReviewerProfile(t)}
		},
		"Delegation mit anderem Modell": func(p *PlanRecord) {
			pr := fxProfile(t)
			pr.Model = "model-z"
			p.Delegations = map[string]domain.AgentProfile{"dec-1": pr}
		},
		"Delegation ohne Auswahl": func(p *PlanRecord) {
			p.Delegations = map[string]domain.AgentProfile{"dec-3": fxProfile(t)}
		},
		"Ablehnung einer unbekannten Entscheidung": func(p *PlanRecord) {
			p.Verdict = supervisor.VerdictRejected
			p.Rejections = []supervisor.Rejection{{Code: supervisor.CodeInvalidTarget, DecisionID: "dec-99", Reason: "x"}}
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			p := good()
			mutate(&p)
			_, err := s.Update(ctxT(t), "tester", func(tx *Tx) error { return tx.RecordPlan(p) })
			wantCode(t, err, CodeInvalid)
		})
	}
	// Validierung je Entscheidung: nur die abgelehnte Entscheidung gilt als abgelehnt, der Rest als für sich zulässig.
	p := good()
	p.Verdict = supervisor.VerdictRejected
	p.Rejections = []supervisor.Rejection{{Code: supervisor.CodePreconditionUnmet, DecisionID: "dec-2", Reason: "x"}}
	mustUpdate(t, s, func(tx *Tx) error { return tx.RecordPlan(p) })
	mustView(t, s, func(r *ReadTx) error {
		ds, err := r.Decisions(projectID, "batch-1")
		if err != nil {
			return err
		}
		got := []supervisor.Verdict{ds[0].Validation, ds[1].Validation, ds[2].Validation}
		want := []supervisor.Verdict{supervisor.VerdictAccepted, supervisor.VerdictRejected, supervisor.VerdictAccepted}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("Validierung = %v, erwartet %v", got, want)
				break
			}
		}
		return nil
	})
}

// TestPlanAttemptRoundTrip: das Protokoll des Planungsversuchs bleibt erhalten.
func TestPlanAttemptRoundTrip(t *testing.T) {
	s := newStore(t)
	seedBase(t, s)
	att := &supervisor.Attempt{Number: 2, ExecutionID: "exec-plan", StartedAt: s.now(), SnapshotRevision: 7, SnapshotSHA256: hex64("1"),
		ContextReduced: []string{}, Skills: []supervisor.SkillRecord{{ID: "ccmas-x", Version: 1, TreeSHA256: hex64("3")}},
		Verdict: supervisor.VerdictAccepted, Replan: true}
	mustUpdate(t, s, func(tx *Tx) error {
		return tx.RecordPlan(PlanRecord{Batch: fxBatch(t), Verdict: supervisor.VerdictAccepted, PlanningSnapshotSHA256: hex64("1"), Attempt: att})
	})
	mustView(t, s, func(r *ReadTx) error {
		p, err := r.Plan(projectID, "batch-1")
		if err != nil {
			return err
		}
		if p.Attempt == nil || p.Attempt.Number != 2 || p.Attempt.ExecutionID != "exec-plan" || !p.Attempt.Replan || len(p.Attempt.Skills) != 1 {
			t.Errorf("Versuch = %+v", p.Attempt)
		}
		return nil
	})
}

// TestInputValidation: ungültige Eingaben werden vor dem Schreiben abgewiesen (fail-closed).
func TestInputValidation(t *testing.T) {
	s := newStore(t)
	seedBase(t, s)
	exec := func(name string, fn func(tx *Tx) error, want Code) {
		t.Run(name, func(t *testing.T) {
			evBefore := countEvents(t, s)
			_, err := s.Update(ctxT(t), "tester", fn)
			wantCode(t, err, want)
			if countEvents(t, s) != evBefore {
				t.Error("Ereignis trotz abgewiesener Eingabe")
			}
		})
	}
	badCfg := fxProject(t)
	badCfg.ProjectID = "neu"
	badCfg.BaseBranch = "main"
	exec("Projekt mit Basisbranch main", func(tx *Tx) error { _, err := tx.CreateProject(badCfg); return err }, CodeInvalid)
	exec("Projekt doppelt", func(tx *Tx) error { _, err := tx.CreateProject(fxProject(t)); return err }, CodeConflict)
	exec("Projekt unbekannt", func(tx *Tx) error { _, err := tx.CreateAgent("unbekannt", "a", domain.RoleReviewer); return err }, CodeNotFound)
	exec("ungültige ID", func(tx *Tx) error { _, err := tx.CreateAgent(projectID, "a b", domain.RoleReviewer); return err }, CodeInvalid)
	exec("Profil verletzt Schema", func(tx *Tx) error {
		p := fxProfile(t)
		p.Version = 0
		return tx.RecordProfile(projectID, p)
	}, CodeInvalid)
	exec("Auftrag verletzt Schema", func(tx *Tx) error {
		w := fxWorkOrder(t, "wo-bad")
		w.AllowedScope = []string{"../x"}
		_, err := tx.CreateWorkOrder(WorkOrderRecord{WorkOrder: w, GoalID: "ms-1", Kind: supervisor.OrderBug})
		return err
	}, CodeInvalid)
	exec("Auftrag ohne Kriterien (nil)", func(tx *Tx) error {
		w := fxWorkOrder(t, "wo-bad")
		w.AcceptanceCriteria = nil
		_, err := tx.CreateWorkOrder(WorkOrderRecord{WorkOrder: w, GoalID: "ms-1", Kind: supervisor.OrderBug})
		return err
	}, CodeInvalid)
	exec("Auftrag in unbekanntem Projekt", func(tx *Tx) error {
		w := fxWorkOrder(t, "wo-bad")
		w.ProjectID = "fremd"
		_, err := tx.CreateWorkOrder(WorkOrderRecord{WorkOrder: w, GoalID: "ms-1", Kind: supervisor.OrderBug})
		return err
	}, CodeNotFound)
	exec("Auftrag zu Ziel eines anderen Projekts", func(tx *Tx) error {
		other := fxProject(t)
		other.ProjectID = "other"
		if _, err := tx.CreateProject(other); err != nil {
			return err
		}
		w := fxWorkOrder(t, "wo-bad")
		w.ProjectID = "other"
		_, err := tx.CreateWorkOrder(WorkOrderRecord{WorkOrder: w, GoalID: "ms-1", Kind: supervisor.OrderBug})
		return err
	}, CodeConstraint)
	exec("Auftragsart unbekannt", func(tx *Tx) error {
		_, err := tx.CreateWorkOrder(WorkOrderRecord{WorkOrder: fxWorkOrder(t, "wo-bad"), GoalID: "ms-1", Kind: "epic"})
		return err
	}, CodeInvalid)
	exec("Selbstabhängigkeit", func(tx *Tx) error {
		_, err := tx.CreateWorkOrder(WorkOrderRecord{WorkOrder: fxWorkOrder(t, "wo-self"), GoalID: "ms-1", Kind: supervisor.OrderBug, DependsOn: []string{"wo-self"}})
		return err
	}, CodeInvalid)
	exec("Selbstabhängigkeit nachträglich", func(tx *Tx) error { return tx.AddDependency(projectID, "wo-1", "wo-1") }, CodeInvalid)
	exec("doppelte Abhängigkeit", func(tx *Tx) error {
		_, err := tx.CreateWorkOrder(WorkOrderRecord{WorkOrder: fxWorkOrder(t, "wo-d"), GoalID: "ms-1", Kind: supervisor.OrderBug, DependsOn: []string{"wo-1", "wo-1"}})
		return err
	}, CodeInvalid)
	exec("Commit kein Hash", func(tx *Tx) error { return tx.SetWorkOrderHead(projectID, "wo-1", "abc123", "b") }, CodeInvalid)
	exec("Auftrag unbekannt", func(tx *Tx) error { return tx.SetWorkOrderHead(projectID, "wo-gibt-es-nicht", commitA, "b") }, CodeNotFound)
	exec("PR-Nummer 0", func(tx *Tx) error {
		return tx.SetPullRequest(projectID, "wo-1", &supervisor.PRInfo{Number: 0, State: supervisor.PROpen, CI: supervisor.CIPassed})
	}, CodeInvalid)
	exec("PR-Stand unbekannt", func(tx *Tx) error {
		return tx.SetPullRequest(projectID, "wo-1", &supervisor.PRInfo{Number: 1, State: "draft", CI: supervisor.CIPassed})
	}, CodeInvalid)
	exec("Konzeptklasse unbekannt", func(tx *Tx) error {
		return tx.SetConceptState(projectID, "wo-1", &supervisor.ConceptState{Version: "1", Class: "wichtig", Criteria: []string{}})
	}, CodeInvalid)
	exec("Freigabe ohne Freigeber", func(tx *Tx) error {
		return tx.SetConceptState(projectID, "wo-1", &supervisor.ConceptState{Version: "1", Criteria: []string{}, Approval: &supervisor.ConceptApprovalInfo{Version: "1"}})
	}, CodeInvalid)
	exec("Reparaturrunden negativ", func(tx *Tx) error { return tx.SetRepairRounds(projectID, "wo-1", -1, 0) }, CodeInvalid)
	exec("Rollenergebnis mit unbekannter Rolle", func(tx *Tx) error {
		return tx.SetLastResult(projectID, "wo-1", &supervisor.LastResult{Role: "boss", Result: "x"})
	}, CodeInvalid)
	exec("Ziel mit unbekannter Art", func(tx *Tx) error {
		g := goalOf("g-x")
		g.Kind = domain.KindWorkOrder
		_, err := tx.CreateGoal(GoalRecord{ProjectID: projectID, Goal: g, IntegrationMode: domain.ModePR})
		return err
	}, CodeInvalid)
	exec("Ziel mit sich selbst als Elternteil", func(tx *Tx) error {
		_, err := tx.CreateGoal(GoalRecord{ProjectID: projectID, Goal: goalOf("g-x"), IntegrationMode: domain.ModePR, ParentGoalID: "g-x"})
		return err
	}, CodeInvalid)
	exec("Frage ohne Ausführung", func(tx *Tx) error {
		return tx.CreateQuestion(projectID, domain.Question{QuestionID: "q", WorkOrderID: "wo-1", ExecutionID: "exec-none", Attempt: 1, Text: "t", Context: "c", State: domain.QuestionOpen, CreatedAt: s.now()})
	}, CodeNotFound)
	exec("Frage nicht offen", func(tx *Tx) error {
		return tx.CreateQuestion(projectID, domain.Question{QuestionID: "q", WorkOrderID: "wo-1", ExecutionID: "exec-none", Attempt: 1, Text: "t", Context: "c", State: domain.QuestionAnswered, CreatedAt: s.now()})
	}, CodeInvalid)
	exec("Antwort auf unbekannte Frage", func(tx *Tx) error {
		return tx.AnswerQuestion(projectID, domain.Answer{AnswerID: "a", QuestionID: "q-none", Text: "t", CreatedAt: s.now()})
	}, CodeNotFound)
	exec("Review ohne Reviewer-Ausführung", func(tx *Tx) error {
		return tx.RecordReview(projectID, domain.ReviewReport{ReportID: "r", WorkOrderID: "wo-1", ReviewerExecutionID: "exec-none", Commit: commitA,
			ConceptVersion: "1", Verdict: domain.VerdictApproved, Findings: []domain.Finding{}, CreatedAt: s.now()}, "")
	}, CodeNotFound)
	exec("Task mit unbekanntem Auftrag", func(tx *Tx) error {
		_, err := tx.SyncTask(TaskRecord{TaskID: "t", ProjectID: projectID, AsanaGID: "1", WorkOrderID: "wo-none", SyncedAt: s.now()})
		return err
	}, CodeConstraint)
}

// TestQuestionFlow: offen -> beantwortet -> zugestellt; keine Abkürzung, keine zweite Antwort, Antwort nur einer Frage.
func TestQuestionFlow(t *testing.T) {
	s := newStore(t)
	seedBase(t, s)
	seedExecutionFor(t, s, "wo-1", "exec-1", domain.RoleImplementer)
	q := domain.Question{QuestionID: "q-1", WorkOrderID: "wo-1", ExecutionID: "exec-1", Attempt: 1, Text: "t", Context: "c", State: domain.QuestionOpen, CreatedAt: s.now()}
	mustUpdate(t, s, func(tx *Tx) error { return tx.CreateQuestion(projectID, q) })

	try := func(name string, fn func(tx *Tx) error, want Code) {
		t.Helper()
		_, err := s.Update(ctxT(t), "tester", fn)
		if want == "" {
			if err != nil {
				t.Errorf("%s: %v", name, err)
			}
			return
		}
		if CodeOf(err) != want {
			t.Errorf("%s: Code %q (%v), erwartet %q", name, CodeOf(err), err, want)
		}
	}
	try("Zustellung ohne Antwort", func(tx *Tx) error { return tx.ConfirmQuestionDelivery(projectID, "q-1", s.now()) }, CodeState)
	try("Antwort mit Zustellbestätigung", func(tx *Tx) error {
		at := s.now()
		return tx.AnswerQuestion(projectID, domain.Answer{AnswerID: "a-1", QuestionID: "q-1", Text: "x", CreatedAt: s.now(), DeliveryConfirmedAt: &at})
	}, CodeInvalid)
	try("Antwort", func(tx *Tx) error {
		return tx.AnswerQuestion(projectID, domain.Answer{AnswerID: "a-1", QuestionID: "q-1", Text: "x", CreatedAt: s.now()})
	}, "")
	try("zweite Antwort", func(tx *Tx) error {
		return tx.AnswerQuestion(projectID, domain.Answer{AnswerID: "a-2", QuestionID: "q-1", Text: "y", CreatedAt: s.now()})
	}, CodeState)
	try("Schließen einer beantworteten Frage mit falschem Ist-Zustand", func(tx *Tx) error {
		return tx.CloseQuestion(projectID, "q-1", domain.QuestionOpen, domain.QuestionCancelled)
	}, CodeConflict)
	try("Zustellung", func(tx *Tx) error { return tx.ConfirmQuestionDelivery(projectID, "q-1", s.now()) }, "")
	try("zweite Zustellung", func(tx *Tx) error { return tx.ConfirmQuestionDelivery(projectID, "q-1", s.now()) }, CodeState)
	try("zugestellte Frage schließen", func(tx *Tx) error {
		return tx.CloseQuestion(projectID, "q-1", domain.QuestionDelivered, domain.QuestionCancelled)
	}, CodeState)
	try("Schließen zu beantwortet", func(tx *Tx) error {
		return tx.CloseQuestion(projectID, "q-1", domain.QuestionOpen, domain.QuestionAnswered)
	}, CodeInvalid)

	// Frage und Ausführung gehören zum selben Auftrag.
	mustUpdate(t, s, func(tx *Tx) error {
		_, err := tx.CreateWorkOrder(WorkOrderRecord{WorkOrder: fxWorkOrder(t, "wo-2"), GoalID: "ms-1", Kind: supervisor.OrderBug})
		return err
	})
	try("Frage mit Auftrag einer fremden Ausführung", func(tx *Tx) error {
		return tx.CreateQuestion(projectID, domain.Question{QuestionID: "q-9", WorkOrderID: "wo-2", ExecutionID: "exec-1", Attempt: 1, Text: "t", Context: "c", State: domain.QuestionOpen, CreatedAt: s.now()})
	}, CodeInvalid)
}

// TestAttemptLifecycle: Versuche zählen lückenlos, ein neuer Versuch folgt erst auf einen beendeten, das terminale
// Ergebnis ist endgültig, und das Profil muss zur Rolle der Ausführung passen.
func TestAttemptLifecycle(t *testing.T) {
	s := newStore(t)
	seedBase(t, s)
	seedExecutionFor(t, s, "wo-1", "exec-1", domain.RoleImplementer)
	start := func(n int) func(tx *Tx) error {
		return func(tx *Tx) error {
			return tx.StartAttempt(projectID, domain.ExecutionAttempt{ExecutionID: "exec-1", Attempt: n, ProfileID: "impl-default", ProfileVersion: 3,
				State: domain.ExecInitializing, StartedAt: s.now()})
		}
	}
	_, err := s.Update(ctxT(t), "tester", start(2))
	wantCode(t, err, CodeInvalid) // Versuch 1 läuft noch
	_, err = s.Update(ctxT(t), "tester", start(1))
	wantCode(t, err, CodeConflict) // Versuch 1 gibt es schon
	_, err = s.Update(ctxT(t), "tester", func(tx *Tx) error {
		return tx.StartAttempt(projectID, domain.ExecutionAttempt{ExecutionID: "exec-1", Attempt: 2, ProfileID: "reviewer:rev-1", ProfileVersion: 7,
			State: domain.ExecInitializing, StartedAt: s.now()})
	})
	wantCode(t, err, CodeInvalid)

	mustUpdate(t, s, func(tx *Tx) error {
		if err := tx.TransitionExecution(projectID, "exec-1", 1, domain.ExecInitializing, domain.ExecRunning); err != nil {
			return err
		}
		return tx.FinishAttempt(projectID, "exec-1", 1, domain.ExecRunning, AttemptResult{
			Terminal: domain.TerminalResult{Attempt: 1, State: domain.ExecControlledFailure, Source: domain.TerminalFromError},
			Summary:  "Fehler", EndedAt: s.now()})
	})
	// Endgültig: kein Übergang, kein zweites Ergebnis.
	_, err = s.Update(ctxT(t), "tester", func(tx *Tx) error {
		return tx.TransitionExecution(projectID, "exec-1", 1, domain.ExecControlledFailure, domain.ExecRunning)
	})
	wantCode(t, err, CodeState)
	_, err = s.Update(ctxT(t), "tester", func(tx *Tx) error {
		return tx.FinishAttempt(projectID, "exec-1", 1, domain.ExecControlledFailure, AttemptResult{
			Terminal: domain.TerminalResult{Attempt: 1, State: domain.ExecCrashed, Source: domain.TerminalFromProcessExit}, EndedAt: s.now()})
	})
	wantCode(t, err, CodeState)
	_, err = s.w.ExecContext(ctxT(t), "UPDATE execution_attempts SET state = 'succeeded' WHERE execution_id = 'exec-1'")
	wantCode(t, mapErr(err), CodeConstraint) // auch die Datenbank hält das Ergebnis fest

	// Der Resume ist ein neuer Versuch, die alten Versuche bleiben unverändert.
	mustUpdate(t, s, start(2))
	mustView(t, s, func(r *ReadTx) error {
		as, err := r.Attempts(projectID, "exec-1")
		if err != nil || len(as) != 2 || as[0].State != domain.ExecControlledFailure || as[1].State != domain.ExecInitializing || as[0].Summary != "Fehler" {
			t.Errorf("Versuche = %+v (%v)", as, err)
		}
		return nil
	})
	// Session nur einmal; Prozess nach dem Ende weiter beobachtbar (Exit), ohne den Versuch zu ändern.
	mustUpdate(t, s, func(tx *Tx) error { return tx.SetSession(projectID, "exec-1", 2, "sess-a") })
	_, err = s.Update(ctxT(t), "tester", func(tx *Tx) error { return tx.SetSession(projectID, "exec-1", 2, "sess-b") })
	wantCode(t, err, CodeConflict)
	mustUpdate(t, s, func(tx *Tx) error {
		return tx.RecordProcess(projectID, "exec-1", 1, domain.ProcessIdentity{PID: 5, StartTime: s.now()}, domain.ProcExited, s.now())
	})
	mustView(t, s, func(r *ReadTx) error {
		a, err := r.Attempt(projectID, "exec-1", 1)
		if err != nil || a.State != domain.ExecControlledFailure || a.OSProcessState != domain.ProcExited {
			t.Errorf("Versuch 1 = %+v (%v)", a, err)
		}
		return nil
	})
}

// TestSpecWithoutPrompt: der Spec-Datensatz trägt keinen Prompt-Text.
func TestSpecWithoutPrompt(t *testing.T) {
	s := newStore(t)
	seedBase(t, s)
	mustUpdate(t, s, func(tx *Tx) error {
		return tx.CreateExecution(domain.Execution{ExecutionID: "exec-1", ProjectID: projectID, WorkOrderID: "wo-1", Role: domain.RoleImplementer, CreatedAt: s.now()})
	})
	_, err := s.Update(ctxT(t), "tester", func(tx *Tx) error {
		return tx.StartAttempt(projectID, domain.ExecutionAttempt{ExecutionID: "exec-1", Attempt: 1, ProfileID: "impl-default", ProfileVersion: 3,
			State: domain.ExecInitializing, StartedAt: s.now(), Spec: []byte(`{"prompt":"vertraulich"}`)})
	})
	wantCode(t, err, CodeInvalid)
	var ce *domain.ContractError
	if !errors.As(err, &ce) {
		t.Fatalf("Fehler %v enthält keinen ContractError", err)
	}
}

// TestAuditDetailsHaveNoFreeText: die Details der Ereignisse sind kurz und aus typisierten Feldern gebaut; ein langer
// Auftragstext oder eine Frage erscheint nicht darin.
func TestAuditDetailsHaveNoFreeText(t *testing.T) {
	s := newStore(t)
	seedBase(t, s)
	long := strings.Repeat("GEHEIMER-TEXT ", 50)
	mustUpdate(t, s, func(tx *Tx) error {
		w := fxWorkOrder(t, "wo-long")
		w.UserOrder, w.Goal = long, long
		_, err := tx.CreateWorkOrder(WorkOrderRecord{WorkOrder: w, GoalID: "ms-1", Kind: supervisor.OrderBug, Title: long})
		return err
	})
	seedExecutionFor(t, s, "wo-1", "exec-1", domain.RoleImplementer)
	mustUpdate(t, s, func(tx *Tx) error {
		return tx.CreateQuestion(projectID, domain.Question{QuestionID: "q-1", WorkOrderID: "wo-1", ExecutionID: "exec-1", Attempt: 1,
			Text: long, Context: long, State: domain.QuestionOpen, CreatedAt: s.now()})
	})
	mustView(t, s, func(r *ReadTx) error {
		evs, err := r.Events(projectID, EventFilter{})
		if err != nil {
			return err
		}
		for _, e := range evs {
			raw, _ := e.DetailsJSON()
			if len(raw) > domain.MaxAuditDetailsBytes || strings.Contains(string(raw), "GEHEIMER") || strings.Contains(e.EntityID, "GEHEIMER") {
				t.Errorf("Ereignis %s enthält Freitext: %s", e.Kind, raw)
			}
		}
		return nil
	})
}

// TestCreateWorkOrderOnlyInWaiting: ein Auftrag beginnt im Zustand „wartet“; jeder andere Anfangszustand liefert den
// Code state, und nichts wird geschrieben (kein Auftrag, kein Ereignis, keine Revision). Fortschritt gibt es nur über
// TransitionWorkOrder.
func TestCreateWorkOrderOnlyInWaiting(t *testing.T) {
	s := newStore(t)
	seedBase(t, s)
	for _, st := range domain.WorkOrderStates() {
		if st == domain.WOWaiting {
			continue
		}
		t.Run(string(st), func(t *testing.T) {
			events, rev := countEvents(t, s), revisionOf(t, s, projectID)
			w := fxWorkOrder(t, "wo-start-"+string(st))
			w.State = st
			_, err := s.Update(ctxT(t), "tester", func(tx *Tx) error {
				_, err := tx.CreateWorkOrder(WorkOrderRecord{WorkOrder: w, GoalID: "ms-1", Kind: supervisor.OrderBug})
				return err
			})
			wantCode(t, err, CodeState)
			if got := countEvents(t, s); got != events {
				t.Errorf("%d Ereignisse geschrieben", got-events)
			}
			if got := revisionOf(t, s, projectID); got != rev {
				t.Errorf("Revision %d -> %d", rev, got)
			}
			mustView(t, s, func(r *ReadTx) error {
				_, err := r.WorkOrder(projectID, w.WorkOrderID)
				wantCode(t, err, CodeNotFound)
				return nil
			})
		})
	}

	// „wartet“ wird angenommen, und der Fortschritt läuft über den Übergang (mit Ereignis).
	w := fxWorkOrder(t, "wo-start-waiting")
	if w.State != domain.WOWaiting {
		t.Fatalf("Fixture beginnt in %q", w.State)
	}
	mustUpdate(t, s, func(tx *Tx) error {
		_, err := tx.CreateWorkOrder(WorkOrderRecord{WorkOrder: w, GoalID: "ms-1", Kind: supervisor.OrderBug})
		return err
	})
	before := countEvents(t, s)
	mustUpdate(t, s, func(tx *Tx) error {
		return tx.TransitionWorkOrder(projectID, w.WorkOrderID, domain.WOWaiting, domain.WOInProgress)
	})
	if got := countEvents(t, s); got != before+1 {
		t.Errorf("Übergang schrieb %d Ereignisse", got-before)
	}
}
