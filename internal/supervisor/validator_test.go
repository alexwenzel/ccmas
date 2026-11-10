package supervisor

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexwenzel/ccmas/internal/domain"
	"github.com/alexwenzel/ccmas/internal/profiles"
	"github.com/alexwenzel/ccmas/internal/skills"
)

// ---- Positivfälle: Go lässt zu, was die Regeln erlauben ---------------------------------------

func TestIndependentOrdersMayRunInParallel(t *testing.T) {
	e := newEnv(t)
	vb := e.mustAccept(batch(implDecision("dec-1", "wo-a"), implDecision("dec-2", "wo-b"), conceptDecision("dec-3", "wo-con")), baseSnapshot())
	if len(vb.Decisions) != 3 || len(vb.Delegations()) != 3 {
		t.Fatalf("validierter Batch = %+v", vb)
	}
	for _, d := range vb.Delegations() {
		if d.Profile.Model == "" || d.Profile.PolicyProfile == "" || d.OrderID == "" {
			t.Errorf("Delegation unvollständig: %+v", d)
		}
	}
	if vb.BatchID != "batch-1" || vb.StateRevision != 7 {
		t.Errorf("Kopf = %+v", vb)
	}
}

func TestDependentOrderMayWaitForItsPrerequisite(t *testing.T) {
	e := newEnv(t)
	wait := waitDecision("dec-2", "wo-c")
	wait.Preconditions = []domain.Precondition{{Kind: domain.PrecWorkOrderCompleted, Ref: "wo-a"}}
	// wo-a läuft, wo-c wartet auf wo-a: zulässig, die Voraussetzung ist die Bedingung des Wartens.
	vb := e.mustAccept(batch(implDecision("dec-1", "wo-a"), wait), baseSnapshot())
	if len(vb.Decisions) != 2 || len(vb.Delegations()) != 1 || vb.Decisions[1].Delegation != nil {
		t.Fatalf("validierter Batch = %+v", vb)
	}
}

func TestIndependentWorkMayBePulledForward(t *testing.T) {
	e := newEnv(t)
	s := baseSnapshot()
	s.order(t, "wo-a").State = domain.WOInProgress
	s.order(t, "wo-a").ActiveExecutionID = "exec-a"
	s.activate(domain.RoleImplementer, "exec-a", "wo-a")
	// wo-a läuft, wo-c hängt von wo-a ab: wo-b ist unabhängig und darf sofort starten.
	e.mustAccept(batch(implDecision("dec-1", "wo-b"), waitDecision("dec-2", "wo-c")), s)
}

func TestDocumentationWorkIsAccepted(t *testing.T) {
	e := newEnv(t)
	vb := e.mustAccept(batch(docDecision("dec-1", "wo-done")), baseSnapshot())
	p := vb.Delegations()[0].Profile
	if p.Role != domain.RoleDocumentation || p.Model != mMid || p.Effort != domain.EffortMedium {
		t.Fatalf("Profil = %+v", p)
	}
}

func TestEffectiveProfileCarriesSourcesAndVisibleFallback(t *testing.T) {
	e := newEnv(t)
	d := implDecision("dec-1", "wo-a")
	d.Selection = selection(domain.RoleImplementer, mMid, domain.EffortXHigh, skImpl)
	vb := e.mustAccept(batch(d), baseSnapshot())
	p := vb.Delegations()[0].Profile
	// model-mid unterstützt xhigh nicht; models.yaml genehmigt die Reduktion auf high. Sie bleibt sichtbar.
	if p.Effort != domain.EffortHigh || p.RequestedEffort != domain.EffortXHigh || p.Fallback == nil || p.Fallback.To != domain.EffortHigh {
		t.Fatalf("Reduktion nicht sichtbar: %+v", p)
	}
	if p.ValueSources == nil || p.ValueSources.Effort != domain.SourceSupervisor || p.ValueSources.SkillIDs != domain.SourceSupervisor {
		t.Fatalf("Herkunft = %+v", p.ValueSources)
	}
	// Die Entscheidung bleibt unverändert; angefordert war xhigh.
	if d.Selection.Effort != domain.EffortXHigh {
		t.Fatal("die Entscheidung wurde verändert")
	}
}

func TestSupervisorChoiceOverridesRoleDefaultButNotAgent(t *testing.T) {
	e := newEnv(t)
	// Ohne Agenten gilt die Wahl des Supervisors vor dem Rollenstandard (model-mid).
	d := implDecision("dec-1", "wo-a")
	d.Selection = selection(domain.RoleImplementer, mBig, domain.EffortMax, skImpl)
	p := e.mustAccept(batch(d), baseSnapshot()).Delegations()[0].Profile
	if p.Model != mBig || p.Effort != domain.EffortMax || p.ValueSources.Model != domain.SourceSupervisor || p.ValueSources.Effort != domain.SourceSupervisor {
		t.Fatalf("Wahl des Supervisors nicht wirksam: %+v", p)
	}

	// Ein individueller Agentenwert hat Vorrang vor der Wahl des Supervisors; die Herkunft zeigt es.
	cfg := testConfig()
	cfg.Agents["fast"] = profiles.Agent{Role: domain.RoleImplementer, Model: mMid, Effort: domain.EffortLow}
	pol := e.policy(cfg)
	s := baseSnapshot()
	s.Roles = []RoleOffer{{Role: domain.RoleImplementer, AgentID: "fast"}}
	vb, err := Validator{}.Accept(batch(d), s, pol)
	if err != nil {
		t.Fatal(err)
	}
	p = vb.Delegations()[0].Profile
	if p.ProfileID != "implementer:fast" || p.Model != mMid || p.Effort != domain.EffortLow {
		t.Fatalf("Agentenwert hat keinen Vorrang: %+v", p)
	}
	if p.ValueSources.Model != domain.SourceAgent || p.ValueSources.Effort != domain.SourceAgent || p.ValueSources.SkillIDs != domain.SourceSupervisor {
		t.Fatalf("Herkunft = %+v", p.ValueSources)
	}
}

func TestOtherActionsAreAccepted(t *testing.T) {
	e := newEnv(t)
	s := baseSnapshot()
	// Review (Code), Integration, Frage, Plan-Ergänzung und Warten in einem Batch.
	vb := e.mustAccept(batch(
		codeReviewDecision("dec-1", "wo-review", commitA, "1.0"),
		integrationDecision("dec-2", "wo-ready"),
		questionDecision("dec-3", "wo-a"),
		planDecision("dec-4", planned("wo-new-1", "wo-a"), planned("wo-new-2", "wo-new-1")),
		waitDecision("dec-5", "wo-c"),
	), s)
	if len(vb.Decisions) != 5 || len(vb.Delegations()) != 1 {
		t.Fatalf("validierter Batch = %+v", vb)
	}
	if got := vb.Delegations()[0].Profile; got.Role != domain.RoleReviewer || got.Model != mBig {
		t.Fatalf("Reviewer-Profil = %+v", got)
	}
}

func TestValidateIsTheTicketContractAndDoesNotMutate(t *testing.T) {
	e := newEnv(t)
	b := batch(implDecision("dec-1", "wo-a"))
	s := baseSnapshot()
	before, _ := json.Marshal(b)
	beforeSnap, _ := json.Marshal(s)
	if err := (Validator{}).Validate(b, s, e.pol); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	after, _ := json.Marshal(b)
	afterSnap, _ := json.Marshal(s)
	if string(before) != string(after) || string(beforeSnap) != string(afterSnap) {
		t.Fatal("Validate verändert Batch oder Snapshot")
	}
	// Dieselbe Ablehnung wie Accept.
	bad := batch(implDecision("dec-1", "wo-c"))
	err := (Validator{}).Validate(bad, s, e.pol)
	_, err2 := e.check(bad, s)
	if err == nil || err2 == nil || err.Error() != err2.Error() {
		t.Fatalf("Validate %v, Accept %v", err, err2)
	}
}

// ---- Schemafehler -------------------------------------------------------------------------------

func TestRejectSchemaViolation(t *testing.T) {
	e := newEnv(t)
	t.Run("Implementierung ohne Auswahl", func(t *testing.T) {
		d := implDecision("dec-1", "wo-a")
		d.Selection = nil
		vb, err := e.check(batch(d), baseSnapshot())
		wantRejection(t, err, CodeSchema, "dec-1", "/decisions/0")
		if vb.Decisions != nil {
			t.Fatal("bei einer Ablehnung darf nichts zur Ausführung freigegeben werden")
		}
	})
	t.Run("unbekannte Rolle und doppelte Skills der Auswahl", func(t *testing.T) {
		d := implDecision("dec-1", "wo-a")
		d.Selection.Role = "wizard"
		_, err := e.check(batch(d), baseSnapshot())
		wantRejection(t, err, CodeSchema, "dec-1", "role")
		d = implDecision("dec-1", "wo-a")
		d.Selection.SkillIDs = []string{skImpl, skImpl}
		_, err = e.check(batch(d), baseSnapshot())
		wantRejection(t, err, CodeSchema, "dec-1", "/decisions/0")
	})
	t.Run("leerer Batch", func(t *testing.T) {
		_, err := e.check(batch(), baseSnapshot())
		wantRejection(t, err, CodeSchema, "")
	})
	t.Run("ungültige Aktion", func(t *testing.T) {
		d := waitDecision("dec-1", "wo-a")
		d.Action = "deploy"
		_, err := e.check(batch(d), baseSnapshot())
		wantRejection(t, err, CodeSchema, "dec-1", "action")
	})
	t.Run("plan_extension bei einer anderen Aktion", func(t *testing.T) {
		d := waitDecision("dec-1", "wo-a")
		d.PlanExtension = &domain.PlanExtension{WorkOrders: []domain.PlannedWorkOrder{planned("wo-new")}}
		_, err := e.check(batch(d), baseSnapshot())
		wantRejection(t, err, CodeSchema, "dec-1")
	})
	t.Run("nil-Listen werden wie leere Listen behandelt", func(t *testing.T) {
		d := implDecision("dec-1", "wo-a")
		d.Preconditions = nil
		d.Selection.SkillIDs = nil
		d.Preconditions = []domain.Precondition{{Kind: domain.PrecConceptApproved}}
		e.mustAccept(batch(d), baseSnapshot())
	})
	t.Run("JSON: kein Objekt, unbekanntes Feld, doppelter Schlüssel", func(t *testing.T) {
		for name, raw := range map[string]string{
			"kein JSON":           `{"schema_version":`,
			"kein Objekt":         `[]`,
			"unbekanntes Feld":    `{"schema_version":1,"batch_id":"b","project_id":"ccmas","state_revision":7,"goal":{"kind":"milestone","id":"ms-1"},"rationale":"x","decisions":[],"extra":1}`,
			"doppelter Schlüssel": `{"schema_version":1,"schema_version":1}`,
		} {
			_, err := Validator{}.AcceptJSON([]byte(raw), baseSnapshot(), e.pol)
			if r := rejected(t, err); len(r.Rejections) == 0 || r.Rejections[0].Code != CodeSchema {
				t.Errorf("%s: %v", name, err)
			}
		}
	})
	t.Run("JSON: formal gültig, aber inhaltlich geprüft", func(t *testing.T) {
		raw, err := os.ReadFile(filepath.Join("..", "..", "contracts", "fixtures", "valid", "decision-batch--minimal.json"))
		if err != nil {
			t.Fatal(err)
		}
		// Das Schema nimmt die Fixture an; der Validator prüft den Bezug (wo-1 gibt es im Snapshot nicht).
		_, err = Validator{}.AcceptJSON(raw, baseSnapshot(), e.pol)
		wantRejection(t, err, CodeUnknownReference, "dec-1", "wo-1")
	})
}

// ---- Projekt und Revision -----------------------------------------------------------------------------

func TestRejectForeignProject(t *testing.T) {
	e := newEnv(t)
	b := batch(implDecision("dec-1", "wo-a"), implDecision("dec-2", "wo-b"))
	b.ProjectID = "anderes"
	b.Decisions[1].ProjectID = "anderes"
	vb, err := e.check(b, baseSnapshot())
	wantRejection(t, err, CodeForeignProject, "")
	wantRejection(t, err, CodeForeignProject, "dec-2")
	if vb.Decisions != nil {
		t.Fatal("fremdes Projekt: nichts darf ausgeführt werden")
	}
	// Nur die einzelne Entscheidung gehört zu einem anderen Projekt.
	b = batch(implDecision("dec-1", "wo-a"), implDecision("dec-2", "wo-b"))
	b.Decisions[0].ProjectID = "anderes"
	_, err = e.check(b, baseSnapshot())
	wantRejection(t, err, CodeForeignProject, "dec-1")
	onlyCodes(t, err, CodeForeignProject)
}

func TestRejectStaleDecision(t *testing.T) {
	e := newEnv(t)
	t.Run("Entscheidung veraltet", func(t *testing.T) {
		b := batch(implDecision("dec-1", "wo-a"), implDecision("dec-2", "wo-b"))
		b.Decisions[0].StateRevision = 6
		vb, err := e.check(b, baseSnapshot())
		r := wantRejection(t, err, CodeStaleRevision, "dec-1", "6", "7")
		if !strings.Contains(r.Reason, "neu") {
			t.Errorf("Begründung weist nicht auf die Neuplanung hin: %q", r.Reason)
		}
		if vb.Decisions != nil || len(vb.Delegations()) != 0 {
			t.Fatal("Revisionskonflikt: keine Ausführung")
		}
		onlyCodes(t, err, CodeStaleRevision)
	})
	t.Run("Batch veraltet", func(t *testing.T) {
		b := batch(implDecision("dec-1", "wo-a"))
		b.StateRevision = 8 // auch eine neuere Revision als der Snapshot ist nicht der aktuelle Stand
		vb, err := e.check(b, baseSnapshot())
		wantRejection(t, err, CodeStaleRevision, "")
		if vb.Decisions != nil {
			t.Fatal("Revisionskonflikt: keine Ausführung")
		}
	})
	t.Run("Snapshot ist weitergelaufen", func(t *testing.T) {
		s := baseSnapshot()
		s.StateRevision = 8
		_, err := e.check(batch(implDecision("dec-1", "wo-a")), s)
		wantRejection(t, err, CodeStaleRevision, "")
		wantRejection(t, err, CodeStaleRevision, "dec-1")
	})
}

// ---- Bezüge --------------------------------------------------------------------------------------------

func TestRejectUnknownReferences(t *testing.T) {
	e := newEnv(t)
	t.Run("unbekannter Auftrag", func(t *testing.T) {
		_, err := e.check(batch(implDecision("dec-1", "wo-nope")), baseSnapshot())
		wantRejection(t, err, CodeUnknownReference, "dec-1", "wo-nope")
	})
	t.Run("unbekanntes Ticket als Ziel", func(t *testing.T) {
		d := waitDecision("dec-1", "tk-unbekannt")
		d.Target.Kind = domain.KindTicket
		_, err := e.check(batch(d), baseSnapshot())
		wantRejection(t, err, CodeUnknownReference, "dec-1", "tk-unbekannt")
	})
	t.Run("unbekanntes Ziel des Batches", func(t *testing.T) {
		b := batch(waitDecision("dec-1", "wo-a"))
		b.Goal.ID = "ms-fremd"
		_, err := e.check(b, baseSnapshot())
		wantRejection(t, err, CodeUnknownReference, "", "ms-fremd")
	})
	t.Run("unbekannter Auftrag in einer Voraussetzung", func(t *testing.T) {
		d := implDecision("dec-1", "wo-a")
		d.Preconditions = append(d.Preconditions, domain.Precondition{Kind: domain.PrecWorkOrderCompleted, Ref: "wo-nope"})
		_, err := e.check(batch(d), baseSnapshot())
		wantRejection(t, err, CodeUnknownReference, "dec-1", "wo-nope")
	})
	t.Run("unbekannte Abhängigkeit einer neuen Aufgabe", func(t *testing.T) {
		_, err := e.check(batch(planDecision("dec-1", planned("wo-new", "wo-nope"))), baseSnapshot())
		wantRejection(t, err, CodeUnknownReference, "dec-1", "wo-nope")
	})
	t.Run("neue Aufgabe ist erst im nächsten Zyklus ansprechbar", func(t *testing.T) {
		_, err := e.check(batch(planDecision("dec-1", planned("wo-new")), implDecision("dec-2", "wo-new")), baseSnapshot())
		wantRejection(t, err, CodeUnknownReference, "dec-2", "wo-new")
	})
	t.Run("Auftrag und Ziel haben je Aktion die richtige Art", func(t *testing.T) {
		d := implDecision("dec-1", "wo-a")
		d.Target = domain.DecisionTarget{Kind: domain.KindTicket, ID: "tk-1"}
		_, err := e.check(batch(d), baseSnapshot())
		wantRejection(t, err, CodeInvalidTarget, "dec-1", "work_order")
		_, err = e.check(batch(completionDecision("dec-1"), func() domain.Decision {
			c := completionDecision("dec-2")
			c.Target = target("wo-a")
			return c
		}()), baseSnapshot())
		wantRejection(t, err, CodeInvalidTarget, "dec-2", "Ziel")
		rv := codeReviewDecision("dec-3", "wo-review", commitA, "1.0")
		rv.ReviewTarget.WorkOrderID = "wo-a"
		_, err = e.check(batch(rv), baseSnapshot())
		wantRejection(t, err, CodeInvalidTarget, "dec-3", "wo-a")
		// Art des Auftrags: Konzeptarbeit nur für Konzeptaufträge, Umsetzung nicht für Konzeptaufträge.
		_, err = e.check(batch(implDecision("dec-4", "wo-con")), baseSnapshot())
		wantRejection(t, err, CodeInvalidTarget, "dec-4", "concept")
		_, err = e.check(batch(conceptDecision("dec-5", "wo-a")), baseSnapshot())
		wantRejection(t, err, CodeInvalidTarget, "dec-5", "implementation")
	})
}

func TestRejectDependencyCycle(t *testing.T) {
	e := newEnv(t)
	t.Run("zwei neue Aufträge hängen voneinander ab", func(t *testing.T) {
		_, err := e.check(batch(planDecision("dec-1", planned("wo-n1", "wo-n2"), planned("wo-n2", "wo-n1"))), baseSnapshot())
		r := wantRejection(t, err, CodeDependencyCycle, "dec-1", "wo-n1", "wo-n2")
		if !strings.Contains(r.Reason, "->") {
			t.Errorf("Begründung nennt den Zyklus nicht als Weg: %q", r.Reason)
		}
	})
	t.Run("Selbstabhängigkeit", func(t *testing.T) {
		_, err := e.check(batch(planDecision("dec-1", planned("wo-n1", "wo-n1"))), baseSnapshot())
		wantRejection(t, err, CodeDependencyCycle, "dec-1", "wo-n1")
	})
	t.Run("Zyklus über drei Aufträge und mehrere Entscheidungen", func(t *testing.T) {
		b := batch(
			planDecision("dec-1", planned("wo-n1", "wo-n3")),
			planDecision("dec-2", planned("wo-n2", "wo-n1")),
			planDecision("dec-3", planned("wo-n3", "wo-n2")),
		)
		_, err := e.check(b, baseSnapshot())
		re := rejected(t, err)
		n := 0
		for _, r := range re.Rejections {
			if r.Code == CodeDependencyCycle {
				n++
			}
		}
		if n == 0 {
			t.Fatalf("kein Zyklus gemeldet:\n%s", re.Error())
		}
	})
	t.Run("vorhandene Abhängigkeiten plus Ergänzung: ein Zyklus im Bestand betrifft die Entscheidung", func(t *testing.T) {
		s := baseSnapshot()
		s.order(t, "wo-a").DependsOn = []string{"wo-b"}
		s.order(t, "wo-b").DependsOn = []string{"wo-a"}
		_, err := e.check(batch(implDecision("dec-1", "wo-a"), implDecision("dec-2", "wo-done")), s)
		wantRejection(t, err, CodeDependencyCycle, "dec-1", "wo-a", "wo-b")
		for _, r := range rejected(t, err).Rejections {
			if r.DecisionID == "dec-2" && r.Code == CodeDependencyCycle {
				t.Errorf("unbeteiligte Entscheidung abgelehnt: %+v", r)
			}
		}
	})
	t.Run("azyklische Abhängigkeiten sind zulässig", func(t *testing.T) {
		e.mustAccept(batch(planDecision("dec-1", planned("wo-n1", "wo-a"), planned("wo-n2", "wo-n1", "wo-b"), planned("wo-n3", "wo-n1"))), baseSnapshot())
	})
	t.Run("doppelte Ref", func(t *testing.T) {
		_, err := e.check(batch(planDecision("dec-1", planned("wo-n1"), planned("wo-n1"))), baseSnapshot())
		wantRejection(t, err, CodeDuplicateReference, "dec-1", "wo-n1")
		_, err = e.check(batch(planDecision("dec-1", planned("wo-a"))), baseSnapshot())
		wantRejection(t, err, CodeDuplicateReference, "dec-1", "wo-a")
	})
}

// ---- Voraussetzungen ----------------------------------------------------------------------------------

func TestRejectUnmetPreconditions(t *testing.T) {
	e := newEnv(t)
	type tc struct {
		name     string
		mutate   func(s *ProjectSnapshot)
		decision domain.Decision
		contains []string
	}
	cases := []tc{
		{"offene Abhängigkeit", func(s *ProjectSnapshot) {}, implDecision("dec-1", "wo-c"), []string{"wo-a"}},
		{"Abhängigkeit im PR-Modus noch nicht gemergt", func(s *ProjectSnapshot) {
			s.order(t, "wo-a").State = domain.WOWaitingIntegration
		}, implDecision("dec-1", "wo-c"), []string{"wo-a", "waiting_integration"}},
		{"Abhängigkeit ist kein Auftrag und nicht erfüllt", func(s *ProjectSnapshot) {
			s.order(t, "wo-a").DependsOn = []string{"tk-1"}
		}, implDecision("dec-1", "wo-a"), []string{"tk-1"}},
		{"blockierter Auftrag", func(s *ProjectSnapshot) {}, implDecision("dec-1", "wo-block"), []string{"blocked"}},
		{"abgeschlossener Auftrag", func(s *ProjectSnapshot) {}, implDecision("dec-1", "wo-done"), []string{"completed"}},
		{"abgebrochener Auftrag", func(s *ProjectSnapshot) {
			s.order(t, "wo-a").State = domain.WOCancelled
		}, implDecision("dec-1", "wo-a"), []string{"cancelled"}},
		{"Konzeptbezug über eine Abhängigkeit, aber kein Konzeptstand am Auftrag", func(s *ProjectSnapshot) {
			s.order(t, "wo-con").State = domain.WOCompleted
			a := s.order(t, "wo-a")
			a.Concept, a.DependsOn = nil, []string{"wo-con"}
		}, implDecision("dec-1", "wo-a"), []string{"wo-con", "Konzeptstand"}},
		{"Konzept nicht freigegeben", func(s *ProjectSnapshot) { s.order(t, "wo-a").Concept.Approval = nil }, implDecision("dec-1", "wo-a"), []string{"Freigabe"}},
		{"Freigabe gilt für eine ältere Version", func(s *ProjectSnapshot) {
			s.order(t, "wo-a").Concept.Version = "1.1"
		}, implDecision("dec-1", "wo-a"), []string{"1.0", "1.1"}},
		{"Konzeptprüfer gibt ein strategisches Konzept frei", func(s *ProjectSnapshot) {
			s.order(t, "wo-a").Concept.Class = skills.ConceptStrategic
			s.order(t, "wo-a").Concept.Criteria = []string{"scope_change"}
		}, implDecision("dec-1", "wo-a"), []string{"Nutzer"}},
		{"nicht eingestuftes Konzept ohne Nutzerfreigabe", func(s *ProjectSnapshot) {
			s.order(t, "wo-a").Concept.Class = ""
		}, implDecision("dec-1", "wo-a"), []string{"Nutzer"}},
		{"strategisches Konzept: der Nutzer entscheidet, nicht der Konzeptprüfer", func(s *ProjectSnapshot) {
			s.order(t, "wo-strat").Kind = OrderConcept
		}, func() domain.Decision {
			d := codeReviewDecision("dec-1", "wo-strat", commitA, "3.0")
			d.Selection = selection(domain.RoleConceptReviewer, mBig, domain.EffortHigh, skReview)
			d.Preconditions = []domain.Precondition{}
			return d
		}(), []string{"ask_question"}},
		{"offene Frage zum Auftrag", func(s *ProjectSnapshot) {
			s.OpenQuestions = []OpenQuestion{{QuestionID: "q-1", WorkOrderID: "wo-a", State: domain.QuestionOpen}}
		}, implDecision("dec-1", "wo-a"), []string{"q-1"}},
		{"beantwortet, aber noch nicht zugestellt", func(s *ProjectSnapshot) {
			s.OpenQuestions = []OpenQuestion{{QuestionID: "q-1", WorkOrderID: "wo-a", State: domain.QuestionAnswered}}
		}, implDecision("dec-1", "wo-a"), []string{"q-1"}},
		{"deklarierte Voraussetzung gilt nicht (review_approved)", func(s *ProjectSnapshot) {}, func() domain.Decision {
			d := codeReviewDecision("dec-1", "wo-review", commitA, "1.0")
			d.Preconditions = append(d.Preconditions, domain.Precondition{Kind: domain.PrecReviewApproved})
			return d
		}(), []string{"review_approved"}},
		{"deklarierte Voraussetzung gilt nicht (ci_passed)", func(s *ProjectSnapshot) {}, func() domain.Decision {
			d := implDecision("dec-1", "wo-a")
			d.Preconditions = append(d.Preconditions, domain.Precondition{Kind: domain.PrecCIPassed})
			return d
		}(), []string{"ci_passed"}},
		{"Konzeptfreigabe für eine andere Version deklariert", func(s *ProjectSnapshot) {}, func() domain.Decision {
			d := implDecision("dec-1", "wo-a")
			d.Preconditions = []domain.Precondition{{Kind: domain.PrecConceptApproved, Ref: "v9"}}
			return d
		}(), []string{"v9", "1.0"}},
		{"Review: Implementierung ist noch nicht fertig", func(s *ProjectSnapshot) {
			s.order(t, "wo-review").State = domain.WOInProgress
		}, codeReviewDecision("dec-1", "wo-review", commitA, "1.0"), []string{"in_progress"}},
		{"Review: Prüfziel ist nicht der aktuelle Stand", func(s *ProjectSnapshot) {}, codeReviewDecision("dec-1", "wo-review", commitB, "1.0"), []string{commitA}},
		{"Review: andere Konzeptversion", func(s *ProjectSnapshot) {}, codeReviewDecision("dec-1", "wo-review", commitA, "0.9"), []string{"1.0", "0.9"}},
		{"Review ohne Stand der Implementierung", func(s *ProjectSnapshot) { s.order(t, "wo-review").HeadCommit = "" }, codeReviewDecision("dec-1", "wo-review", commitA, "1.0"), []string{"Stand"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := baseSnapshot()
			c.mutate(&s)
			vb, err := e.check(batch(c.decision), s)
			wantRejection(t, err, CodePreconditionUnmet, "dec-1", c.contains...)
			if vb.Decisions != nil {
				t.Fatal("unerfüllte Voraussetzung: nichts darf ausgeführt werden")
			}
		})
	}

	t.Run("eine Ursache, eine Ablehnung", func(t *testing.T) {
		// Regressionstest: Zustand und deklarierte Voraussetzung dürfen dieselbe Ursache nicht doppelt melden.
		s := baseSnapshot()
		s.order(t, "wo-review").State = domain.WOInProgress
		_, err := e.check(batch(codeReviewDecision("dec-1", "wo-review", commitA, "1.0")), s)
		n := 0
		for _, r := range rejected(t, err).Rejections {
			if r.Code == CodePreconditionUnmet {
				n++
			}
		}
		if n != 1 {
			t.Errorf("%d Ablehnungen statt einer:\n%v", n, err)
		}
	})
	t.Run("Wait mit Voraussetzung ist die Bedingung des Wartens und keine Behauptung", func(t *testing.T) {
		w := waitDecision("dec-1", "wo-c")
		w.Preconditions = []domain.Precondition{{Kind: domain.PrecWorkOrderCompleted, Ref: "wo-a"}, {Kind: domain.PrecCIPassed}}
		e.mustAccept(batch(w), baseSnapshot())
	})
	t.Run("freigegebenes Konzept durch den Nutzer (auch strategisch)", func(t *testing.T) {
		s := baseSnapshot()
		c := s.order(t, "wo-a").Concept
		c.Class, c.Criteria = skills.ConceptStrategic, []string{"scope_change"}
		c.Approval = &ConceptApprovalInfo{Version: "1.0", Approver: skills.ApproverUser}
		e.mustAccept(batch(implDecision("dec-1", "wo-a")), s)
	})
	t.Run("erfüllte Voraussetzungen", func(t *testing.T) {
		s := baseSnapshot()
		s.order(t, "wo-a").State = domain.WOCompleted // wo-c darf nun starten
		d := implDecision("dec-1", "wo-c")
		d.Preconditions = append(d.Preconditions, domain.Precondition{Kind: domain.PrecWorkOrderCompleted, Ref: "wo-a"}, domain.Precondition{Kind: domain.PrecCapacityAvailable})
		e.mustAccept(batch(d), s)
	})
}

// ---- Kapazität -----------------------------------------------------------------------------------------

func TestRejectCapacityExceeded(t *testing.T) {
	e := newEnv(t)
	t.Run("mehr neue Ausführungen als freie Plätze", func(t *testing.T) {
		s := baseSnapshot()
		s.activate(domain.RoleImplementer, "exec-1", "wo-block")
		s.activate(domain.RoleImplementer, "exec-2", "wo-done")
		vb, err := e.check(batch(implDecision("dec-1", "wo-a"), implDecision("dec-2", "wo-b")), s)
		r := wantRejection(t, err, CodeCapacityExceeded, "dec-2", "3", "2")
		_ = r
		for _, rj := range rejected(t, err).Rejections {
			if rj.Code == CodeCapacityExceeded && rj.DecisionID == "dec-1" {
				t.Error("die erste Entscheidung passt noch in die Kapazität")
			}
		}
		if vb.Decisions != nil {
			t.Fatal("Kapazität überschritten: nichts darf ausgeführt werden")
		}
	})
	t.Run("alle vier Arten zählen, auch Reviewer", func(t *testing.T) {
		s := baseSnapshot()
		s.Capacity.MaxWorkers = 1
		for name, d := range map[string]domain.Decision{
			"concept_work":       conceptDecision("dec-2", "wo-con"),
			"implementation":     implDecision("dec-2", "wo-b"),
			"review":             codeReviewDecision("dec-2", "wo-review", commitA, "1.0"),
			"documentation_work": docDecision("dec-2", "wo-done"),
		} {
			_, err := e.check(batch(implDecision("dec-1", "wo-a"), d), s)
			if r := rejected(t, err); len(r.Rejections) == 0 {
				t.Errorf("%s: keine Ablehnung", name)
			}
			wantRejection(t, err, CodeCapacityExceeded, "dec-2")
		}
	})
	t.Run("ein Reviewer verbraucht den letzten freien Platz", func(t *testing.T) {
		s := baseSnapshot()
		s.activate(domain.RoleImplementer, "exec-1", "wo-block")
		s.activate(domain.RoleConcept, "exec-2", "wo-con")
		e.mustAccept(batch(codeReviewDecision("dec-1", "wo-review", commitA, "1.0")), s)
		_, err := e.check(batch(codeReviewDecision("dec-1", "wo-review", commitA, "1.0"), implDecision("dec-2", "wo-a")), s)
		wantRejection(t, err, CodeCapacityExceeded, "dec-2")
	})
	t.Run("der Supervisor zählt nicht mit", func(t *testing.T) {
		s := baseSnapshot()
		s.Capacity.MaxWorkers = 2
		s.activate(domain.RoleSupervisor, "exec-sup", "")
		s.activate(domain.RoleImplementer, "exec-1", "wo-block")
		e.mustAccept(batch(implDecision("dec-1", "wo-a")), s)
		_, err := e.check(batch(implDecision("dec-1", "wo-a"), implDecision("dec-2", "wo-b")), s)
		wantRejection(t, err, CodeCapacityExceeded, "dec-2")
	})
	t.Run("Aktionen ohne Ausführung brauchen keinen Platz", func(t *testing.T) {
		s := baseSnapshot()
		s.Capacity.MaxWorkers = 1
		s.activate(domain.RoleImplementer, "exec-1", "wo-block")
		e.mustAccept(batch(waitDecision("dec-1", "wo-a"), questionDecision("dec-2", "wo-b"), planDecision("dec-3", planned("wo-n1")), integrationDecision("dec-4", "wo-ready")), s)
	})
	t.Run("deklarierte Voraussetzung capacity_available ohne freien Platz", func(t *testing.T) {
		s := baseSnapshot()
		s.Capacity.MaxWorkers = 1
		s.activate(domain.RoleImplementer, "exec-1", "wo-block")
		w := waitDecision("dec-1", "wo-a")
		w.Preconditions = []domain.Precondition{{Kind: domain.PrecCapacityAvailable}}
		e.mustAccept(batch(w), s) // Bedingung des Wartens
		d := implDecision("dec-1", "wo-a")
		d.Preconditions = append(d.Preconditions, domain.Precondition{Kind: domain.PrecCapacityAvailable})
		_, err := e.check(batch(d), s)
		wantRejection(t, err, CodeCapacityExceeded, "dec-1")
		onlyCodes(t, err, CodeCapacityExceeded)
	})
}

// ---- Doppelstart ----------------------------------------------------------------------------------------

func TestRejectDoubleStart(t *testing.T) {
	e := newEnv(t)
	t.Run("zwei Entscheidungen für denselben Auftrag", func(t *testing.T) {
		vb, err := e.check(batch(implDecision("dec-1", "wo-a"), docDecision("dec-2", "wo-a")), baseSnapshot())
		wantRejection(t, err, CodeDuplicateStart, "dec-2", "dec-1")
		if vb.Decisions != nil {
			t.Fatal("Doppelstart: nichts darf ausgeführt werden")
		}
		for _, r := range rejected(t, err).Rejections {
			if r.DecisionID == "dec-1" && r.Code == CodeDuplicateStart {
				t.Error("die erste Entscheidung bleibt zulässig")
			}
		}
	})
	t.Run("Auftrag mit aktiver Ausführung", func(t *testing.T) {
		s := baseSnapshot()
		s.order(t, "wo-a").State = domain.WOInProgress
		s.activate(domain.RoleImplementer, "exec-a", "wo-a")
		_, err := e.check(batch(implDecision("dec-1", "wo-a")), s)
		wantRejection(t, err, CodeDuplicateStart, "dec-1", "exec-a")
	})
	t.Run("aktive Ausführung nur im Auftrag geführt", func(t *testing.T) {
		s := baseSnapshot()
		s.order(t, "wo-a").ActiveExecutionID = "exec-a"
		s.activate(domain.RoleImplementer, "exec-a", "wo-a")
		_, err := e.check(batch(conceptDecision("dec-1", "wo-con"), docDecision("dec-2", "wo-a")), s)
		wantRejection(t, err, CodeDuplicateStart, "dec-2", "exec-a")
	})
	t.Run("Entscheidungs-ID doppelt", func(t *testing.T) {
		_, err := e.check(batch(implDecision("dec-1", "wo-a"), implDecision("dec-1", "wo-b")), baseSnapshot())
		wantRejection(t, err, CodeDuplicateDecisionID, "dec-1")
	})
	t.Run("verschiedene Aufträge sind kein Doppelstart", func(t *testing.T) {
		e.mustAccept(batch(implDecision("dec-1", "wo-a"), implDecision("dec-2", "wo-b")), baseSnapshot())
	})
}

// ---- Umfang und Modus ----------------------------------------------------------------------------------

func TestRejectScopeExtension(t *testing.T) {
	e := newEnv(t)
	t.Run("Ziel außerhalb des beauftragten Umfangs", func(t *testing.T) {
		d := waitDecision("dec-1", "tk-99")
		d.Target.Kind = domain.KindTicket
		_, err := e.check(batch(d), baseSnapshot())
		wantRejection(t, err, CodeScopeExtension, "dec-1", "tk-99")
	})
	t.Run("Ziel im Umfang ist zulässig", func(t *testing.T) {
		d := waitDecision("dec-1", "tk-1")
		d.Target.Kind = domain.KindTicket
		e.mustAccept(batch(d), baseSnapshot())
	})
	t.Run("Batch für ein nicht beauftragtes Ziel", func(t *testing.T) {
		b := batch(waitDecision("dec-1", "wo-a"))
		b.Goal = domain.DecisionTarget{Kind: domain.KindTicket, ID: "tk-99"}
		_, err := e.check(b, baseSnapshot())
		wantRejection(t, err, CodeScopeExtension, "", "tk-99")
	})
	t.Run("Plan-Ergänzung für ein Ziel außerhalb des Umfangs", func(t *testing.T) {
		d := planDecision("dec-1", planned("wo-n1"))
		d.Target = domain.DecisionTarget{Kind: domain.KindTicket, ID: "tk-99"}
		_, err := e.check(batch(d), baseSnapshot())
		wantRejection(t, err, CodeScopeExtension, "dec-1", "tk-99")
	})
	t.Run("neue Aufgabe hängt von einem Ticket außerhalb des Umfangs ab", func(t *testing.T) {
		_, err := e.check(batch(planDecision("dec-1", planned("wo-n1", "tk-99"))), baseSnapshot())
		wantRejection(t, err, CodeScopeExtension, "dec-1", "tk-99")
	})
	t.Run("neue Aufgabe übernimmt ein nicht beauftragtes Ticket als Ref", func(t *testing.T) {
		_, err := e.check(batch(planDecision("dec-1", planned("tk-99"))), baseSnapshot())
		wantRejection(t, err, CodeScopeExtension, "dec-1", "tk-99")
	})
	t.Run("Ergänzung innerhalb des Ziels ist zulässig", func(t *testing.T) {
		d := planDecision("dec-1", planned("wo-n1", "wo-a"))
		d.Target = domain.DecisionTarget{Kind: domain.KindWorkOrder, ID: "wo-a"} // Korrektur zu einem Auftrag
		e.mustAccept(batch(d), baseSnapshot())
		e.mustAccept(batch(planDecision("dec-2", planned("wo-n2"))), baseSnapshot())
	})
}

func TestRejectModeExtension(t *testing.T) {
	e := newEnv(t)
	t.Run("neuer Auftrag im Auto-Modus im PR-Ziel", func(t *testing.T) {
		item := planned("wo-n1")
		item.Mode = domain.ModeAuto
		_, err := e.check(batch(planDecision("dec-1", item)), baseSnapshot())
		wantRejection(t, err, CodeModeExtension, "dec-1", "auto")
	})
	t.Run("restriktiverer Modus ist zulässig", func(t *testing.T) {
		s := baseSnapshot()
		s.IntegrationMode = domain.ModeAuto
		item := planned("wo-n1")
		item.Mode = domain.ModePR
		e.mustAccept(batch(planDecision("dec-1", item)), s)
		item.Mode = domain.ModeAuto
		e.mustAccept(batch(planDecision("dec-1", item)), s)
	})
	t.Run("Auftrag im Auto-Modus in einem PR-Ziel", func(t *testing.T) {
		s := baseSnapshot()
		s.order(t, "wo-ready").Mode = domain.ModeAuto
		_, err := e.check(batch(integrationDecision("dec-1", "wo-ready")), s)
		wantRejection(t, err, CodeModeExtension, "dec-1", "auto")
	})
	t.Run("Auto-Auftrag in einem Auto-Ziel wird integriert", func(t *testing.T) {
		s := baseSnapshot()
		s.IntegrationMode = domain.ModeAuto
		s.order(t, "wo-ready").Mode = domain.ModeAuto
		e.mustAccept(batch(integrationDecision("dec-1", "wo-ready")), s)
	})
}

// ---- Integration ohne gültige Freigabe -------------------------------------------------------------------

func TestRejectIntegrationWithoutValidApproval(t *testing.T) {
	e := newEnv(t)
	e.mustAccept(batch(integrationDecision("dec-1", "wo-ready")), baseSnapshot()) // Grundfall: zulässig

	cases := []struct {
		name     string
		mutate   func(o *Order)
		contains string
	}{
		{"kein Review", func(o *Order) { o.LastReview = nil }, "review_missing"},
		{"Urteil BEFUNDE", func(o *Order) {
			o.LastReview.Report = report("wo-ready", commitB, "1.0", domain.VerdictFindings, "exec-rev")
		}, "review_invalid"},
		{"Stand hat sich nach dem Review geändert", func(o *Order) { o.HeadCommit = commitA }, "review_invalid"},
		{"Konzeptversion hat sich geändert", func(o *Order) {
			o.Concept.Version = "1.1"
			o.Concept.Approval.Version = "1.1"
		}, "review_invalid"},
		{"Bericht eines Konzeptprüfers", func(o *Order) { o.LastReview.ReviewerRole = domain.RoleConceptReviewer }, "review_invalid"},
		{"Rolle der Reviewer-Ausführung unbekannt", func(o *Order) { o.LastReview.ReviewerRole = "" }, "review_invalid"},
		{"Review und Implementierung aus derselben Ausführung", func(o *Order) { o.LastReview.ImplementerExecutionID = "exec-rev" }, "review_not_independent"},
		{"Ausführung der Implementierung unbekannt", func(o *Order) { o.LastReview.ImplementerExecutionID = "" }, "review_not_independent"},
		{"Ausführung des Reviews unbekannt", func(o *Order) { o.LastReview.Report.ReviewerExecutionID = "" }, "review_not_independent"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := baseSnapshot()
			c.mutate(s.order(t, "wo-ready"))
			vb, err := e.check(batch(integrationDecision("dec-1", "wo-ready")), s)
			if re := rejected(t, err); len(re.Rejections) == 0 {
				t.Fatal("keine Ablehnung")
			}
			wantRejection(t, err, CodeIntegrationNotApproved, "dec-1", c.contains)
			if vb.Decisions != nil {
				t.Fatal("ohne gültige Freigabe wird nichts integriert")
			}
		})
	}
}

// ---- Zielabschluss ------------------------------------------------------------------------------------------------

func allDone(s *ProjectSnapshot) {
	for i := range s.Orders {
		s.Orders[i].State = domain.WOCompleted
	}
	s.Goal.Evidence = &Evidence{
		Integration: skills.IntegrationMerged, MergedBy: skills.MergedByHuman,
		AcceptanceCriteriaMet: true, ReviewEvidence: true, TestEvidence: true, CIEvidence: true,
		TaskStateUpdated: true, KnowledgeUpdated: true, ChildrenComplete: true, OwnerAcceptance: true,
	}
}

func TestRejectPrematureCompletion(t *testing.T) {
	e := newEnv(t)
	t.Run("ohne Nachweise", func(t *testing.T) {
		vb, err := e.check(batch(completionDecision("dec-1")), baseSnapshot())
		wantRejection(t, err, CodePrematureCompletion, "dec-1", "merge_confirmed", "review_evidence")
		if vb.Decisions != nil {
			t.Fatal("vorzeitiger Abschluss: nichts darf ausgeführt werden")
		}
	})
	t.Run("offener PR ist ein Wartezustand, kein Abschluss", func(t *testing.T) {
		s := baseSnapshot()
		allDone(&s)
		s.Goal.Evidence.Integration, s.Goal.Evidence.MergedBy = skills.IntegrationPROpen, ""
		r := wantRejection(t, s2err(e, s, completionDecision("dec-1")), CodePrematureCompletion, "dec-1", "merge_confirmed", "waiting_integration")
		if !strings.Contains(r.Reason, "wait") {
			t.Errorf("Begründung nennt den Wartezustand nicht: %q", r.Reason)
		}
	})
	t.Run("im PR-Modus zählt nur ein menschlicher Merge", func(t *testing.T) {
		s := baseSnapshot()
		allDone(&s)
		s.Goal.Evidence.MergedBy = skills.MergedByQueue
		wantRejection(t, s2err(e, s, completionDecision("dec-1")), CodePrematureCompletion, "dec-1", "merge_confirmed")
	})
	t.Run("Meilenstein ohne Abnahme", func(t *testing.T) {
		s := baseSnapshot()
		allDone(&s)
		s.Goal.Evidence.OwnerAcceptance = false
		wantRejection(t, s2err(e, s, completionDecision("dec-1")), CodePrematureCompletion, "dec-1", "owner_acceptance")
	})
	t.Run("Nachweise vollständig, aber Aufträge offen", func(t *testing.T) {
		s := baseSnapshot()
		allDone(&s)
		s.order(t, "wo-b").State = domain.WOInProgress
		wantRejection(t, s2err(e, s, completionDecision("dec-1")), CodePrematureCompletion, "dec-1", "wo-b")
	})
	t.Run("leere Warteschlange reicht nicht", func(t *testing.T) {
		s := baseSnapshot()
		s.Orders = nil
		wantRejection(t, s2err(e, s, completionDecision("dec-1")), CodePrematureCompletion, "dec-1")
	})
	t.Run("belegter Abschluss", func(t *testing.T) {
		s := baseSnapshot()
		allDone(&s)
		s.order(t, "wo-block").State = domain.WOCancelled
		e.mustAccept(batch(completionDecision("dec-1")), s)
	})
	t.Run("Feature braucht keine Abnahme des Meilensteins", func(t *testing.T) {
		s := baseSnapshot()
		allDone(&s)
		s.Goal.Kind = domain.KindFeature
		s.Goal.Evidence.OwnerAcceptance, s.Goal.Evidence.ChildrenComplete = false, false
		d := completionDecision("dec-1")
		d.Target.Kind = domain.KindFeature
		b := batch(d)
		b.Goal.Kind = domain.KindFeature
		e.mustAccept(b, s)
	})
	t.Run("im Auto-Modus zählt auch die Merge Queue", func(t *testing.T) {
		s := baseSnapshot()
		allDone(&s)
		s.IntegrationMode = domain.ModeAuto
		s.Goal.Evidence.MergedBy = skills.MergedByQueue
		e.mustAccept(batch(completionDecision("dec-1")), s)
	})
}

func s2err(e *testEnv, s ProjectSnapshot, ds ...domain.Decision) error {
	_, err := e.check(batch(ds...), s)
	return err
}

// ---- Reparaturbudget ----------------------------------------------------------------------------------------------

func TestRejectRepairBudgetExceeded(t *testing.T) {
	e := newEnv(t)
	withFindings := func(rounds int) ProjectSnapshot {
		s := baseSnapshot()
		o := s.order(t, "wo-a")
		o.State = domain.WOInProgress
		o.HeadCommit = commitA
		o.CodeRepairRounds = rounds
		o.LastReview = &ReviewRecord{
			Report:       report("wo-a", commitA, "1.0", domain.VerdictFindings, "exec-rev"),
			ReviewerRole: domain.RoleReviewer, ImplementerExecutionID: "exec-impl",
		}
		return s
	}
	t.Run("Code-Schleife: Budget erschöpft", func(t *testing.T) {
		vb, err := e.check(batch(implDecision("dec-1", "wo-a")), withFindings(3))
		wantRejection(t, err, CodeRepairBudgetExhausted, "dec-1", "ask_question", "3")
		if vb.Decisions != nil {
			t.Fatal("Budget erschöpft: nichts darf ausgeführt werden")
		}
	})
	t.Run("Code-Schleife: letzte Runde ist noch zulässig", func(t *testing.T) {
		e.mustAccept(batch(implDecision("dec-1", "wo-a")), withFindings(2))
	})
	t.Run("nur noch die Frage an den Nutzer", func(t *testing.T) {
		e.mustAccept(batch(questionDecision("dec-1", "wo-a")), withFindings(3))
	})
	t.Run("ohne offene Befunde ist es keine Reparaturrunde", func(t *testing.T) {
		s := baseSnapshot()
		s.order(t, "wo-a").CodeRepairRounds = 3
		e.mustAccept(batch(implDecision("dec-1", "wo-a")), s)
	})
	t.Run("Konzeptschleife wird getrennt gezählt", func(t *testing.T) {
		mk := func(codeRounds, conceptRounds int) ProjectSnapshot {
			s := baseSnapshot()
			o := s.order(t, "wo-con")
			o.ConceptRepairRounds, o.CodeRepairRounds = conceptRounds, codeRounds
			o.LastReview = &ReviewRecord{
				Report:       report("wo-con", commitA, "2.0", domain.VerdictFindings, "exec-crev"),
				ReviewerRole: domain.RoleConceptReviewer,
			}
			return s
		}
		_, err := e.check(batch(conceptDecision("dec-1", "wo-con")), mk(0, 3))
		wantRejection(t, err, CodeRepairBudgetExhausted, "dec-1", "ask_question")
		e.mustAccept(batch(conceptDecision("dec-1", "wo-con")), mk(3, 2)) // Codeschleife erschöpft, Konzeptschleife nicht
	})
}

// ---- Profil -------------------------------------------------------------------------------------------------------

func TestRejectInadmissibleProfile(t *testing.T) {
	e := newEnv(t)
	mk := func(mutate func(d *domain.Decision)) domain.Decision {
		d := implDecision("dec-1", "wo-a")
		mutate(&d)
		return d
	}
	cases := []struct {
		name     string
		decision domain.Decision
		contains []string
	}{
		{"unbekanntes Modell", mk(func(d *domain.Decision) { d.Selection.Model = "model-x" }), []string{"model-x"}},
		{"Effort vom Modell nicht unterstützt, kein Fallback", mk(func(d *domain.Decision) { d.Selection.Effort = domain.EffortMax }), []string{"max"}},
		{"Modell für die Rolle nicht erlaubt", func() domain.Decision {
			d := codeReviewDecision("dec-1", "wo-review", commitA, "1.0")
			d.Selection.Model = mMid
			return d
		}(), []string{mMid}},
		{"Rolle passt nicht zur Aktion", mk(func(d *domain.Decision) { d.Selection.Role = domain.RoleReviewer }), []string{"reviewer"}},
		{"unbekannter Skill", mk(func(d *domain.Decision) { d.Selection.SkillIDs = []string{"ccmas-gibtsnicht"} }), []string{"ccmas-gibtsnicht"}},
		{"Skill noch nicht freigegeben", mk(func(d *domain.Decision) { d.Selection.SkillIDs = []string{skPlan} }), []string{skPlan}},
		{"Skill für diese Rolle nicht erlaubt", mk(func(d *domain.Decision) { d.Selection.SkillIDs = []string{skReview} }), []string{skReview, "implementer"}},
		{"Dokumentation mit Implementierungsskill", func() domain.Decision {
			d := docDecision("dec-1", "wo-done")
			d.Selection.SkillIDs = []string{skImpl}
			return d
		}(), []string{skImpl}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			vb, err := e.check(batch(c.decision), baseSnapshot())
			wantRejection(t, err, CodeProfileNotAllowed, "dec-1", c.contains...)
			if vb.Decisions != nil {
				t.Fatal("unzulässiges Profil: nichts darf ausgeführt werden")
			}
		})
	}
	t.Run("Agent der Rolle ist nicht angelegt", func(t *testing.T) {
		s := baseSnapshot()
		s.Roles = []RoleOffer{{Role: domain.RoleImplementer, AgentID: "ghost"}}
		_, err := e.check(batch(implDecision("dec-1", "wo-a")), s)
		wantRejection(t, err, CodeProfileNotAllowed, "dec-1", "ghost")
	})
	t.Run("Fehler beim Profil stehen neben anderen Verstößen", func(t *testing.T) {
		d := implDecision("dec-1", "wo-c")
		d.Selection.Model = "model-x"
		_, err := e.check(batch(d), baseSnapshot())
		onlyCodes(t, err, CodeProfileNotAllowed, CodePreconditionUnmet)
	})
}

// ---- Folgeschritt ------------------------------------------------------------------------------------------------

func TestRejectStepNotCoveredByRoleRules(t *testing.T) {
	e := newEnv(t)
	t.Run("geforderte Voraussetzung fehlt in der Entscheidung (implementation)", func(t *testing.T) {
		d := implDecision("dec-1", "wo-a")
		d.Preconditions = []domain.Precondition{}
		_, err := e.check(batch(d), baseSnapshot())
		wantRejection(t, err, CodeStepNotCovered, "dec-1", "concept_version_approved")
	})
	t.Run("geforderte Voraussetzung fehlt in der Entscheidung (Integration)", func(t *testing.T) {
		d := integrationDecision("dec-1", "wo-ready")
		d.Preconditions = []domain.Precondition{}
		_, err := e.check(batch(d), baseSnapshot())
		wantRejection(t, err, CodeStepNotCovered, "dec-1", "review_approved")
	})
	t.Run("geforderte Voraussetzung fehlt in der Entscheidung (Review)", func(t *testing.T) {
		d := codeReviewDecision("dec-1", "wo-review", commitA, "1.0")
		d.Preconditions = []domain.Precondition{}
		_, err := e.check(batch(d), baseSnapshot())
		wantRejection(t, err, CodeStepNotCovered, "dec-1", "work_order_completed")
	})
	t.Run("Konzeptprüfung ist nur nach einem Konzeptergebnis ein Folgeschritt", func(t *testing.T) {
		mk := func(last *LastResult) ProjectSnapshot {
			s := baseSnapshot()
			s.order(t, "wo-con").LastResult = last
			return s
		}
		d := codeReviewDecision("dec-1", "wo-con", commitA, "2.0")
		d.Selection = selection(domain.RoleConceptReviewer, mBig, domain.EffortHigh, skReview)
		d.Preconditions = []domain.Precondition{}
		_, err := e.check(batch(d), mk(nil))
		wantRejection(t, err, CodeStepNotCovered, "dec-1", "concept_reviewer")
		_, err = e.check(batch(d), mk(&LastResult{Role: domain.RoleImplementer, Result: "implemented"}))
		wantRejection(t, err, CodeStepNotCovered, "dec-1", "concept_reviewer")
		// Nach concept_ready folgt die Prüfung durch den Konzeptprüfer (Routine).
		e.mustAccept(batch(d), mk(&LastResult{Role: domain.RoleConcept, Result: "concept_ready"}))
	})
	t.Run("unbekanntes letztes Ergebnis deckt nichts", func(t *testing.T) {
		s := baseSnapshot()
		s.order(t, "wo-con").LastResult = &LastResult{Role: domain.RoleConcept, Result: "erfunden"}
		d := codeReviewDecision("dec-1", "wo-con", commitA, "2.0")
		d.Selection = selection(domain.RoleConceptReviewer, mBig, domain.EffortHigh, skReview)
		d.Preconditions = []domain.Precondition{}
		_, err := e.check(batch(d), s)
		wantRejection(t, err, CodeStepNotCovered, "dec-1")
	})
	t.Run("Regeln ohne den Folgeschritt documentation_work", func(t *testing.T) {
		// Dieselbe Entscheidung ist mit den Regeln des Repositorys zulässig und mit Regeln ohne den Schritt nicht.
		e.mustAccept(batch(docDecision("dec-1", "wo-done")), baseSnapshot())
		raw, err := os.ReadFile(filepath.Join(e.root, "rules", "roles.yaml"))
		if err != nil {
			t.Fatal(err)
		}
		line := "          - {action: documentation_work, role: documentation}\n"
		if !strings.Contains(string(raw), line) {
			t.Fatal("roles.yaml enthält den Folgeschritt documentation_work nicht in der erwarteten Zeile")
		}
		if err := os.WriteFile(filepath.Join(e.root, "rules", "roles.yaml"), []byte(strings.Replace(string(raw), line, "", 1)), 0o644); err != nil {
			t.Fatal(err)
		}
		cat, rules, err := skills.LoadChecked(e.root)
		if err != nil {
			t.Fatalf("Regeln ohne den Schritt sind in sich gültig: %v", err)
		}
		e2 := &testEnv{t: t, root: e.root, cat: cat, rules: rules, caps: e.caps, cfg: e.cfg}
		e2.pol = e2.policy(e.cfg)
		_, err = e2.check(batch(docDecision("dec-1", "wo-done")), baseSnapshot())
		wantRejection(t, err, CodeStepNotCovered, "dec-1", "documentation_work")
	})
}

// ---- Nachbesserung CC-14 (Review zu W14.1) -----------------------------------------------------------

// noConcept macht wo-a zum Auftrag ohne jeden Konzeptbezug (kein Konzeptstand, keine Abhängigkeit auf einen Konzeptauftrag).
func noConcept(t *testing.T, s *ProjectSnapshot, id string) *Order {
	o := s.order(t, id)
	o.Concept = nil
	return o
}

func TestConceptApprovalIsRequiredOnlyWhereTheOrderHasAConceptReference(t *testing.T) {
	e := newEnv(t)
	t.Run("Fehlerauftrag ohne Konzeptbezug braucht keine Konzeptfreigabe", func(t *testing.T) {
		s := baseSnapshot()
		o := noConcept(t, &s, "wo-a")
		o.Kind = OrderBug
		d := implDecision("dec-1", "wo-a")
		d.Preconditions = []domain.Precondition{} // concept_version_approved entfällt: es gibt nichts freizugeben
		vb := e.mustAccept(batch(d), s)
		if len(vb.Delegations()) != 1 {
			t.Fatalf("validierter Batch = %+v", vb)
		}
	})
	t.Run("auch Umsetzung ohne Konzeptbezug, mit und ohne deklarierte Voraussetzung", func(t *testing.T) {
		s := baseSnapshot()
		noConcept(t, &s, "wo-a")
		d := implDecision("dec-1", "wo-a")
		d.Preconditions = []domain.Precondition{}
		e.mustAccept(batch(d), s)
		e.mustAccept(batch(implDecision("dec-1", "wo-a")), s) // deklariert ohne Version: leere Behauptung, kein Verstoß
	})
	t.Run("eine genannte Konzeptversion ohne Konzeptstand ist falsch", func(t *testing.T) {
		s := baseSnapshot()
		noConcept(t, &s, "wo-a")
		d := implDecision("dec-1", "wo-a")
		d.Preconditions = []domain.Precondition{{Kind: domain.PrecConceptApproved, Ref: "1.0"}}
		_, err := e.check(batch(d), s)
		wantRejection(t, err, CodePreconditionUnmet, "dec-1", "1.0", "Konzeptstand")
	})
	t.Run("Konzeptstand am Auftrag: die Freigabe bleibt Pflicht", func(t *testing.T) {
		s := baseSnapshot()
		s.order(t, "wo-a").Concept.Approval = nil
		_, err := e.check(batch(implDecision("dec-1", "wo-a")), s)
		wantRejection(t, err, CodePreconditionUnmet, "dec-1", "Freigabe")
		// Auch die Deckung durch die Rollenregeln verlangt dort die deklarierte Voraussetzung.
		d := implDecision("dec-1", "wo-b")
		d.Preconditions = []domain.Precondition{}
		_, err = e.check(batch(d), baseSnapshot())
		wantRejection(t, err, CodeStepNotCovered, "dec-1", "concept_version_approved")
	})
	t.Run("Abhängigkeit auf einen Konzeptauftrag: Konzeptstand und Freigabe bleiben Pflicht", func(t *testing.T) {
		s := baseSnapshot()
		s.order(t, "wo-con").State = domain.WOCompleted
		a := noConcept(t, &s, "wo-a")
		a.DependsOn = []string{"wo-con"}
		d := implDecision("dec-1", "wo-a")
		d.Preconditions = []domain.Precondition{}
		_, err := e.check(batch(d), s)
		wantRejection(t, err, CodePreconditionUnmet, "dec-1", "wo-con")
		wantRejection(t, err, CodeStepNotCovered, "dec-1", "concept_version_approved")
		// Mit freigegebenem Konzeptstand und deklarierter Voraussetzung ist es zulässig.
		a.Concept = approvedConcept("1.0")
		e.mustAccept(batch(implDecision("dec-1", "wo-a")), s)
		// Strategisch bleibt Sache des Nutzers.
		a.Concept.Class, a.Concept.Criteria = skills.ConceptStrategic, []string{"scope_change"}
		_, err = e.check(batch(implDecision("dec-1", "wo-a")), s)
		wantRejection(t, err, CodePreconditionUnmet, "dec-1", "Nutzer")
	})
	t.Run("Review und Integration ohne Konzeptbezug", func(t *testing.T) {
		s := baseSnapshot()
		noConcept(t, &s, "wo-review")
		ready := noConcept(t, &s, "wo-ready")
		// Ohne Konzeptversion ist die Bindung an den Commit die einzige; der Bericht nennt irgendeine Bezeichnung.
		ready.LastReview.Report.ConceptVersion = "ohne-konzept"
		e.mustAccept(batch(codeReviewDecision("dec-1", "wo-review", commitA, "ohne-konzept"), integrationDecision("dec-2", "wo-ready")), s)
		// Die Bindung an den Commit bleibt: ein Bericht für einen anderen Commit trägt die Integration nicht.
		ready.LastReview.Report.Commit = commitA
		_, err := e.check(batch(integrationDecision("dec-1", "wo-ready")), s)
		wantRejection(t, err, CodeIntegrationNotApproved, "dec-1")
	})
}

func TestDuplicateStartAppliesOnlyToStartingActions(t *testing.T) {
	e := newEnv(t)
	t.Run("wait und ask_question für denselben Auftrag sind zusammen zulässig", func(t *testing.T) {
		vb := e.mustAccept(batch(waitDecision("dec-1", "wo-c"), questionDecision("dec-2", "wo-c")), baseSnapshot())
		if len(vb.Decisions) != 2 || len(vb.Delegations()) != 0 {
			t.Fatalf("validierter Batch = %+v", vb)
		}
	})
	t.Run("wait neben einer Beauftragung desselben Auftrags ist kein Doppelstart", func(t *testing.T) {
		_, err := e.check(batch(waitDecision("dec-1", "wo-a"), implDecision("dec-2", "wo-a")), baseSnapshot())
		if err != nil {
			if re, ok := AsRejection(err); ok && re.Has(CodeDuplicateStart) {
				t.Fatalf("wait zählt nicht als Start:\n%v", err)
			}
		}
	})
	t.Run("Beauftragung und Integrationsanforderung desselben Auftrags bleiben ein Doppelstart", func(t *testing.T) {
		_, err := e.check(batch(integrationDecision("dec-1", "wo-ready"), docDecision("dec-2", "wo-ready")), baseSnapshot())
		wantRejection(t, err, CodeDuplicateStart, "dec-2", "dec-1")
		_, err = e.check(batch(docDecision("dec-1", "wo-ready"), integrationDecision("dec-2", "wo-ready")), baseSnapshot())
		wantRejection(t, err, CodeDuplicateStart, "dec-2", "dec-1")
	})
	t.Run("zwei Beauftragungen bleiben ein Doppelstart", func(t *testing.T) {
		_, err := e.check(batch(implDecision("dec-1", "wo-a"), implDecision("dec-2", "wo-a")), baseSnapshot())
		wantRejection(t, err, CodeDuplicateStart, "dec-2", "dec-1")
	})
}

func TestCapacityCountsOnlyDelegationsThatCouldStart(t *testing.T) {
	e := newEnv(t)
	free := func() ProjectSnapshot { // max_workers 3, zwei aktiv: ein Platz frei
		s := baseSnapshot()
		s.activate(domain.RoleImplementer, "exec-1", "wo-block")
		s.activate(domain.RoleImplementer, "exec-2", "wo-done")
		return s
	}
	t.Run("aus anderem Grund abgelehnte Delegation belegt keinen Platz (Sondenfall)", func(t *testing.T) {
		// dec-1: offene Abhängigkeit (wo-c hängt an wo-a). dec-2 passt in den freien Platz.
		_, err := e.check(batch(implDecision("dec-1", "wo-c"), implDecision("dec-2", "wo-b")), free())
		wantRejection(t, err, CodePreconditionUnmet, "dec-1", "wo-a")
		onlyCodes(t, err, CodePreconditionUnmet)
	})
	t.Run("zwei startbare Delegationen: die zweite passt nicht", func(t *testing.T) {
		_, err := e.check(batch(implDecision("dec-1", "wo-a"), implDecision("dec-2", "wo-b")), free())
		wantRejection(t, err, CodeCapacityExceeded, "dec-2")
		onlyCodes(t, err, CodeCapacityExceeded)
	})
	t.Run("drei Delegationen, die abgelehnte dazwischen zählt nicht", func(t *testing.T) {
		_, err := e.check(batch(implDecision("dec-1", "wo-a"), implDecision("dec-2", "wo-c"), implDecision("dec-3", "wo-b")), free())
		wantRejection(t, err, CodePreconditionUnmet, "dec-2")
		wantRejection(t, err, CodeCapacityExceeded, "dec-3")
		for _, r := range rejected(t, err).Rejections {
			if r.Code == CodeCapacityExceeded && r.DecisionID != "dec-3" {
				t.Errorf("capacity_exceeded bei %s", r.DecisionID)
			}
		}
	})
	t.Run("ein Doppelstart belegt keinen zweiten Platz", func(t *testing.T) {
		s := free()
		_, err := e.check(batch(implDecision("dec-1", "wo-a"), docDecision("dec-2", "wo-a")), s)
		wantRejection(t, err, CodeDuplicateStart, "dec-2")
		onlyCodes(t, err, CodeDuplicateStart)
	})
}

func TestNewOrderWithoutModeInheritsTheOrderedMode(t *testing.T) {
	e := newEnv(t)
	auto := planned("wo-n2")
	auto.Mode = domain.ModeAuto
	pr := planned("wo-n3")
	pr.Mode = domain.ModePR

	s := baseSnapshot() // Ziel im Modus pr
	vb := e.mustAccept(batch(planDecision("dec-1", planned("wo-n1"), pr)), s)
	modes := vb.Decisions[0].NewOrderModes
	if modes["wo-n1"] != domain.ModePR || modes["wo-n3"] != domain.ModePR || len(modes) != 2 {
		t.Fatalf("Modi der neuen Aufträge = %v", modes)
	}

	s.IntegrationMode = domain.ModeAuto // Ziel ausdrücklich im Modus auto: erben heißt auto, enger (pr) ist zulässig
	vb = e.mustAccept(batch(planDecision("dec-1", planned("wo-n1"), auto, pr)), s)
	modes = vb.Decisions[0].NewOrderModes
	if modes["wo-n1"] != domain.ModeAuto || modes["wo-n2"] != domain.ModeAuto || modes["wo-n3"] != domain.ModePR {
		t.Fatalf("Modi der neuen Aufträge = %v", modes)
	}

	// Aufweiten bleibt abgelehnt, auch bei ausdrücklicher Angabe.
	_, err := e.check(batch(planDecision("dec-1", auto)), baseSnapshot())
	wantRejection(t, err, CodeModeExtension, "dec-1")

	// Entscheidungen ohne Plan-Ergänzung tragen keine Modi.
	vb = e.mustAccept(batch(waitDecision("dec-1", "wo-c")), baseSnapshot())
	if vb.Decisions[0].NewOrderModes != nil {
		t.Errorf("Modi bei wait: %v", vb.Decisions[0].NewOrderModes)
	}
}

// ---- Sammeln, Format, Policy, Snapshot ---------------------------------------------------------------------------

func TestMultipleViolationsAreCollected(t *testing.T) {
	e := newEnv(t)
	s := baseSnapshot()
	s.Capacity.MaxWorkers = 2
	s.activate(domain.RoleImplementer, "exec-1", "wo-block")

	stale := implDecision("dec-1", "wo-a")
	stale.StateRevision = 6
	unknown := implDecision("dec-2", "wo-nope")
	blocked := implDecision("dec-3", "wo-block")
	double := docDecision("dec-4", "wo-a")
	badProfile := implDecision("dec-5", "wo-b")
	badProfile.Selection.Model = "model-x"
	mode := planned("wo-n1")
	mode.Mode = domain.ModeAuto
	plan := planDecision("dec-6", mode, planned("wo-n2", "wo-n3"), planned("wo-n3", "wo-n2"))
	b := batch(stale, unknown, blocked, double, badProfile, plan, completionDecision("dec-7"))
	b.StateRevision = 8

	vb, err := e.check(b, s)
	if vb.Decisions != nil {
		t.Fatal("bei Ablehnungen wird nichts freigegeben")
	}
	re := rejected(t, err)
	wantRejection(t, err, CodeStaleRevision, "")
	wantRejection(t, err, CodeStaleRevision, "dec-1")
	wantRejection(t, err, CodeUnknownReference, "dec-2")
	wantRejection(t, err, CodePreconditionUnmet, "dec-3")
	wantRejection(t, err, CodeDuplicateStart, "dec-4")
	wantRejection(t, err, CodeProfileNotAllowed, "dec-5")
	wantRejection(t, err, CodeModeExtension, "dec-6")
	wantRejection(t, err, CodeDependencyCycle, "dec-6")
	wantRejection(t, err, CodePrematureCompletion, "dec-7")
	// max 2, eine Ausführung aktiv: ein Platz frei. Keine der Delegationen ist grundsätzlich startbar (alle aus anderem
	// Grund abgelehnt), also zählt keine, und capacity_exceeded wäre eine falsche Rückmeldung.
	for _, r := range re.Rejections {
		if r.Code == CodeCapacityExceeded {
			t.Errorf("capacity_exceeded für eine aus anderem Grund abgelehnte Delegation: %v", r)
		}
	}
	if len(re.Rejections) < 8 {
		t.Fatalf("zu wenige Ablehnungen (%d):\n%s", len(re.Rejections), re.Error())
	}

	// Deterministisch: gleiche Eingabe, gleiche Liste in gleicher Reihenfolge; der Batch steht vor den Entscheidungen.
	_, err2 := e.check(b, s)
	if err.Error() != err2.Error() {
		t.Fatalf("nicht deterministisch:\n%s\n---\n%s", err, err2)
	}
	if re.Rejections[0].DecisionID != "" {
		t.Errorf("Ablehnungen des Batches stehen zuerst: %+v", re.Rejections[0])
	}

	// Die Fehlermeldung nennt jede Ablehnung mit Code, Entscheidungs-ID und Begründung und ist als Rückgabe an
	// das Modell zur Neuplanung formuliert.
	text := re.Error()
	for _, r := range re.Rejections {
		if !strings.Contains(text, "["+string(r.Code)+"]") || !strings.Contains(text, r.Reason) {
			t.Errorf("Fehlertext enthält %s/%s nicht", r.Code, r.DecisionID)
		}
	}
	if !strings.Contains(text, "batch-1") || !strings.Contains(strings.ToLower(text), "neu") {
		t.Errorf("Fehlertext nennt Batch oder Neuplanung nicht:\n%s", text)
	}
	if got := re.Codes(); len(got) < 7 {
		t.Errorf("Codes() = %v", got)
	}
}

func TestRejectionErrorIsMachineReadable(t *testing.T) {
	e := newEnv(t)
	_, err := e.check(batch(implDecision("dec-1", "wo-c"), waitDecision("dec-2", "tk-99")), baseSnapshot())
	var re *RejectionError
	if !errors.As(err, &re) {
		t.Fatalf("errors.As: %v", err)
	}
	if !re.Has(CodePreconditionUnmet) || re.Has(CodeSchema) {
		t.Errorf("Has: %v", re.Codes())
	}
	raw, jerr := json.Marshal(re)
	if jerr != nil {
		t.Fatal(jerr)
	}
	var back struct {
		BatchID    string `json:"batch_id"`
		Rejections []struct {
			Code       string `json:"code"`
			DecisionID string `json:"decision_id"`
			Reason     string `json:"reason"`
		} `json:"rejections"`
	}
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if back.BatchID != "batch-1" || len(back.Rejections) != len(re.Rejections) || back.Rejections[0].Reason == "" || back.Rejections[0].Code == "" {
		t.Errorf("JSON = %s", raw)
	}
	if !errors.Is(err, &RejectionError{}) {
		t.Error("errors.Is erkennt den Fehlertyp nicht")
	}
	if _, ok := AsRejection(errors.New("anderer Fehler")); ok {
		t.Error("AsRejection erkennt fremde Fehler")
	}
	if _, ok := AsRejection(nil); ok {
		t.Error("AsRejection(nil)")
	}
}

func TestIncompletePolicyRejectsEverything(t *testing.T) {
	e := newEnv(t)
	for name, pol := range map[string]Policy{
		"leer":          {},
		"ohne Regeln":   {Catalog: e.pol.Catalog, Resolver: e.pol.Resolver, Caps: e.pol.Caps},
		"ohne Resolver": {Catalog: e.pol.Catalog, Rules: e.pol.Rules, Caps: e.pol.Caps},
	} {
		t.Run(name, func(t *testing.T) {
			vb, err := Validator{}.Accept(batch(waitDecision("dec-1", "wo-a")), baseSnapshot(), pol)
			wantRejection(t, err, CodePolicyIncomplete, "")
			if vb.Decisions != nil {
				t.Fatal("ohne vollständige Policy entscheidet die Plattform nichts (fail-safe)")
			}
		})
	}
}

func TestInvalidSnapshotRejectsBatch(t *testing.T) {
	e := newEnv(t)
	for name, mutate := range map[string]func(s *ProjectSnapshot){
		"doppelter Auftrag":   func(s *ProjectSnapshot) { s.Orders = append(s.Orders, s.Orders[0]) },
		"unbekannter Zustand": func(s *ProjectSnapshot) { s.Orders[0].State = "erfunden" },
		"max_workers 0":       func(s *ProjectSnapshot) { s.Capacity.MaxWorkers = 0 },
		"Ausführung ohne Laufzustand": func(s *ProjectSnapshot) {
			s.Capacity.Active = []ActiveExecution{{ExecutionID: "e", Role: domain.RoleImplementer, State: domain.ExecSucceeded}}
		},
		"unbekannter Modus": func(s *ProjectSnapshot) { s.IntegrationMode = "merge" },
		"Schema-Version":    func(s *ProjectSnapshot) { s.SchemaVersion = 2 },
	} {
		t.Run(name, func(t *testing.T) {
			s := baseSnapshot()
			mutate(&s)
			vb, err := e.check(batch(waitDecision("dec-1", "wo-a")), s)
			wantRejection(t, err, CodeInvalidSnapshot, "")
			if vb.Decisions != nil {
				t.Fatal("ein ungültiger Snapshot ist keine Grundlage für Entscheidungen")
			}
		})
	}
}

func TestNewPolicyChecksItsParts(t *testing.T) {
	e := newEnv(t)
	res, err := profiles.NewResolver(e.cfg, e.cat)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewPolicy(nil, e.caps, e.cat, e.rules); err == nil {
		t.Error("NewPolicy ohne Resolver")
	}
	if _, err := NewPolicy(res, e.caps, nil, e.rules); err == nil {
		t.Error("NewPolicy ohne Katalog")
	}
	if _, err := NewPolicy(res, e.caps, e.cat, nil); err == nil {
		t.Error("NewPolicy ohne Regeln")
	}
	bad := e.caps.Clone()
	bad.Revision = 0
	if _, err := NewPolicy(res, bad, e.cat, e.rules); err == nil {
		t.Error("NewPolicy mit ungültigen Capabilities")
	}
	pol, err := NewPolicy(res, e.caps, e.cat, e.rules)
	if err != nil {
		t.Fatal(err)
	}
	// Der Domain-Katalog stammt aus models.yaml und dem Skill-Katalog (BuildCatalog), keine zweite Liste.
	want := profiles.BuildCatalog(e.caps, e.cat)
	if len(pol.Catalog.Models) != len(want.Models) || len(pol.Catalog.Fallbacks) != 1 || pol.Catalog.SkillRoles == nil {
		t.Errorf("Katalog = %+v", pol.Catalog)
	}
	if err := pol.Check(); err != nil {
		t.Error(err)
	}
}

// TestDocumentationNamesEveryCode: docs/architecture/supervisor.md nennt jeden Code (Schutz vor Drift zwischen Doku und Code).
func TestDocumentationNamesEveryCode(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "architecture", "supervisor.md"))
	if err != nil {
		t.Fatal(err)
	}
	doc := string(raw)
	for _, c := range Codes() {
		if !strings.Contains(doc, "`"+string(c)+"`") {
			t.Errorf("supervisor.md nennt den Code %q nicht", c)
		}
	}
}
