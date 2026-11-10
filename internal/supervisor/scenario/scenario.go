// Package scenario enthält die synthetischen Planungsszenarien S1 bis S5 und ihre semantischen Prädikate (CC-14, W14.2).
//
// Die Snapshots sind erfundene Testdaten (Aufträge sc1-a, sc3-fin, ...), nie der Stand des echten ccmas-Boards. Die Prädikate
// bewerten einen Planungsergebnis-Batch inhaltlich und ohne Wortlaut: Sie fragen, welche Aktionen auf welche Aufträge zielen,
// nicht wie die Begründung formuliert ist. Dieselben Prädikate gelten für handgeschriebene Batches in den Tests und, in W14.3,
// für die echten Ausgaben des Supervisors.
//
// Das Paket ist Auswertungswerkzeug. Es wählt nichts aus, ergänzt keinen Plan und wird vom Planer nie aufgerufen: Go ersetzt
// die Planung des Supervisors nicht durch eine fest programmierte Auswahl (Spezifikation „Intelligente Arbeitsverteilung als
// Herzstück“).
package scenario

import (
	"fmt"
	"slices"
	"strings"

	"github.com/alexwenzel/ccmas/internal/domain"
	"github.com/alexwenzel/ccmas/internal/skills"
	"github.com/alexwenzel/ccmas/internal/supervisor"
)

// ID bezeichnet ein Szenario.
type ID string

const (
	S1 ID = "S1" // zwei unabhängige startbare Aufträge, ein dritter hängt am ersten
	S2 ID = "S2" // unreife Konzepte, einmal Routine, einmal strategisch
	S3 ID = "S3" // knappe Kapazität, eine fertige Implementierung ohne Review
	S4 ID = "S4" // Review mit BEFUNDE innerhalb des Reparaturbudgets
	S5 ID = "S5" // PR-Modus, Review FREIGEGEBEN, PR offen
)

// IDs liefert die Szenarien in fester Reihenfolge.
func IDs() []ID { return []ID{S1, S2, S3, S4, S5} }

// Aufträge der Szenarien (erfundene IDs).
const (
	S1A, S1B, S1C = "sc1-a", "sc1-b", "sc1-c" // C hängt von A ab
	S2ConRoutine  = "sc2-con-routine"
	S2ConStrat    = "sc2-con-strategic"
	S2ImplRoutine = "sc2-impl-routine"
	S2ImplStrat   = "sc2-impl-strategic"
	S3Run         = "sc3-run" // läuft
	S3Fin         = "sc3-fin" // fertig, ohne Review
	S3New         = "sc3-new" // startbar
	S4A           = "sc4-a"   // Review mit BEFUNDE
	S5A           = "sc5-a"   // freigegeben, PR offen
)

// Commit des Kopfstands in S3 bis S5 (erfunden).
const Commit = "0123456789abcdef0123456789abcdef01234567"

// ProjectID, GoalID und Revision der Szenarien.
const (
	ProjectID = "ccmas"
	GoalID    = "ms-1"
	Revision  = 12
)

// Env sind die Angebote, die ein Snapshot trägt: erlaubte Rollenprofile und freigegebene Skills. Sie stammen aus Konfiguration
// und Katalog (supervisor.BuildRoleOffers, supervisor.BuildSkillOffers), nicht aus diesem Paket.
type Env struct {
	Roles  []supervisor.RoleOffer
	Skills []supervisor.SkillOffer
}

func approvedConcept(version string) *supervisor.ConceptState {
	return &supervisor.ConceptState{
		Version: version, Class: skills.ConceptRoutine, Criteria: []string{},
		Approval: &supervisor.ConceptApprovalInfo{Version: version, Approver: skills.ApproverConceptReviewer},
	}
}

func order(id string, kind supervisor.OrderKind, state domain.WorkOrderState, deps ...string) supervisor.Order {
	if deps == nil {
		deps = []string{}
	}
	return supervisor.Order{ID: id, Kind: kind, Title: "Auftrag " + id, State: state, Mode: domain.ModePR, DependsOn: deps}
}

func impl(id string, state domain.WorkOrderState, deps ...string) supervisor.Order {
	o := order(id, supervisor.OrderImplementation, state, deps...)
	o.Concept = approvedConcept("1.0")
	return o
}

// Snapshot liefert den synthetischen Snapshot des Szenarios.
func Snapshot(id ID, env Env) (supervisor.ProjectSnapshot, error) {
	s := supervisor.ProjectSnapshot{
		SchemaVersion: supervisor.SnapshotSchemaVersion, ProjectID: ProjectID, StateRevision: Revision,
		Goal: supervisor.Goal{
			Kind: domain.KindMilestone, ID: GoalID, Title: "Beispielmeilenstein",
			AcceptanceCriteria: []string{"Alle Aufträge abgeschlossen und integriert"}, Scope: []string{}, OutOfScope: []string{},
		},
		IntegrationMode: domain.ModePR,
		Capacity:        supervisor.Capacity{MaxWorkers: 3, Active: []supervisor.ActiveExecution{}},
		OpenQuestions:   []supervisor.OpenQuestion{},
		Roles:           env.Roles, Skills: env.Skills,
	}
	switch id {
	case S1:
		s.Orders = []supervisor.Order{impl(S1A, domain.WOWaiting), impl(S1B, domain.WOWaiting), impl(S1C, domain.WOWaiting, S1A)}
	case S2:
		routine := order(S2ConRoutine, supervisor.OrderConcept, domain.WOWaiting)
		routine.Concept = &supervisor.ConceptState{Version: "0.1", Class: skills.ConceptRoutine, Criteria: []string{}}
		strat := order(S2ConStrat, supervisor.OrderConcept, domain.WOWaiting)
		strat.Concept = &supervisor.ConceptState{Version: "0.1", Class: skills.ConceptStrategic, Criteria: []string{"scope_change"}}
		s.Orders = []supervisor.Order{
			routine, strat,
			order(S2ImplRoutine, supervisor.OrderImplementation, domain.WOWaiting, S2ConRoutine),
			order(S2ImplStrat, supervisor.OrderImplementation, domain.WOWaiting, S2ConStrat),
		}
	case S3:
		s.Capacity.MaxWorkers = 2
		run := impl(S3Run, domain.WOInProgress)
		run.ActiveExecutionID = "exec-run"
		s.Capacity.Active = []supervisor.ActiveExecution{{ExecutionID: "exec-run", Role: domain.RoleImplementer, WorkOrderID: S3Run, State: domain.ExecRunning}}
		fin := impl(S3Fin, domain.WOValidation)
		fin.HeadCommit, fin.Branch = Commit, "feature/CC-1-fin"
		fin.LastResult = &supervisor.LastResult{Role: domain.RoleImplementer, Result: "implemented"}
		s.Orders = []supervisor.Order{run, fin, impl(S3New, domain.WOWaiting)}
	case S4:
		a := impl(S4A, domain.WOReview)
		a.HeadCommit, a.Branch = Commit, "feature/CC-2-a"
		a.CodeRepairRounds = 1
		a.LastResult = &supervisor.LastResult{Role: domain.RoleReviewer, Result: "findings"}
		a.LastReview = &supervisor.ReviewRecord{
			Report: domain.ReviewReport{
				ReportID: "rep-" + S4A, WorkOrderID: S4A, ReviewerExecutionID: "exec-rev", Commit: Commit, ConceptVersion: "1.0",
				Verdict:   domain.VerdictFindings,
				Findings:  []domain.Finding{{FindingID: "f-1", Severity: domain.SeverityMajor, Summary: "Fehlerfall ohne Test", Location: "internal/x/y.go"}},
				CreatedAt: createdAt,
			},
			ReviewerRole: domain.RoleReviewer, ImplementerExecutionID: "exec-impl",
		}
		s.Orders = []supervisor.Order{a}
	case S5:
		a := impl(S5A, domain.WOWaitingIntegration)
		a.HeadCommit, a.Branch = Commit, "feature/CC-3-a"
		a.LastResult = &supervisor.LastResult{Role: domain.RoleReviewer, Result: "approved"}
		a.PR = &supervisor.PRInfo{Number: 42, State: supervisor.PROpen, CI: supervisor.CIPassed, HeadCommit: Commit}
		a.LastReview = &supervisor.ReviewRecord{
			Report: domain.ReviewReport{
				ReportID: "rep-" + S5A, WorkOrderID: S5A, ReviewerExecutionID: "exec-rev", Commit: Commit, ConceptVersion: "1.0",
				Verdict: domain.VerdictApproved, Findings: []domain.Finding{}, CreatedAt: createdAt,
			},
			ReviewerRole: domain.RoleReviewer, ImplementerExecutionID: "exec-impl",
		}
		s.Orders = []supervisor.Order{a}
		s.Goal.Evidence = &supervisor.Evidence{Integration: skills.IntegrationPROpen, AcceptanceCriteriaMet: true, ReviewEvidence: true, TestEvidence: true, CIEvidence: true}
	default:
		return supervisor.ProjectSnapshot{}, fmt.Errorf("unbekanntes Szenario %q", id)
	}
	if err := s.Check(); err != nil {
		return supervisor.ProjectSnapshot{}, fmt.Errorf("Szenario %s: %w", id, err)
	}
	return s, nil
}

// ---- Prädikate ---------------------------------------------------------------------------------------------

// Evaluate wendet die gemeinsamen Prädikate (CheckDelegations) und die des Szenarios (Check) an und liefert alle Verstöße;
// leer heißt: Der Batch erfüllt den Vertrag des Szenarios.
func Evaluate(id ID, b domain.DecisionBatch, snap supervisor.ProjectSnapshot) []string {
	return append(CheckDelegations(b, snap), Check(id, b, snap)...)
}

// delegationActions sind die Aktionen, die eine Ausführung beauftragen.
var delegationActions = []domain.Action{domain.ActionConceptWork, domain.ActionImplementation, domain.ActionReview, domain.ActionDocumentationWork}

func isDelegation(d domain.Decision) bool { return slices.Contains(delegationActions, d.Action) }

// CheckDelegations prüft, was für jede Beauftragung gilt: Rolle, Skills, Modell, Effort aus dem Angebot der Rolle im Snapshot,
// erwartetes Ergebnis, Aufwandsschätzung (Größe, Komplexität, Unsicherheit) getrennt vom Effort und eine Begründung. Es prüft
// Vorhandensein und Zugehörigkeit zum Angebot, nie den Wortlaut.
func CheckDelegations(b domain.DecisionBatch, snap supervisor.ProjectSnapshot) []string {
	var out []string
	offers := map[domain.Role]supervisor.RoleOffer{}
	for _, o := range snap.Roles {
		offers[o.Role] = o
	}
	for _, d := range b.Decisions {
		if !isDelegation(d) {
			continue
		}
		bad := func(format string, args ...any) {
			out = append(out, fmt.Sprintf("%s (%s): ", d.DecisionID, d.Action)+fmt.Sprintf(format, args...))
		}
		sel := d.Selection
		if sel == nil {
			bad("keine Auswahl (Rolle, Skills, Modell, Effort)")
			continue
		}
		if !sel.Role.Valid() {
			bad("Rolle %q unbekannt", sel.Role)
		}
		if len(sel.SkillIDs) == 0 {
			bad("keine Skills genannt")
		}
		if sel.Model == "" {
			bad("kein Modell")
		}
		if !sel.Effort.Valid() {
			bad("Effort %q unbekannt", sel.Effort)
		}
		if offer, ok := offers[sel.Role]; ok {
			if sel.Model != "" && !slices.Contains(offer.AllowedModels, sel.Model) {
				bad("Modell %q gehört nicht zum Angebot der Rolle %s (%v)", sel.Model, sel.Role, offer.AllowedModels)
			}
			if sel.Effort.Valid() && !slices.Contains(offer.AllowedEfforts, sel.Effort) {
				bad("Effort %q gehört nicht zum Angebot der Rolle %s (%v)", sel.Effort, sel.Role, offer.AllowedEfforts)
			}
			for _, sk := range sel.SkillIDs {
				if !slices.Contains(offer.SkillIDs, sk) {
					bad("Skill %q gehört nicht zum Angebot der Rolle %s", sk, sel.Role)
				}
			}
		} else if sel.Role.Valid() {
			bad("kein Angebot für die Rolle %s im Snapshot", sel.Role)
		}
		if strings.TrimSpace(d.ExpectedOutcome) == "" {
			bad("kein erwartetes Ergebnis")
		}
		// Aufwand ist eine eigene Angabe, getrennt vom Effort der Auswahl: ohne Schätzung gilt der Aufwand als nicht angegeben.
		if e := d.Estimate; e == nil {
			bad("keine Aufwandsschätzung")
		} else if !slices.Contains(domain.EstimateSizes(), e.Size) || !slices.Contains(domain.EstimateLevels(), e.Complexity) || !slices.Contains(domain.EstimateLevels(), e.Uncertainty) {
			bad("Aufwandsschätzung unvollständig oder unbekannt: %+v", *e)
		}
		if strings.TrimSpace(d.Rationale) == "" {
			bad("keine Begründung")
		}
	}
	return out
}

// Check wendet die Prädikate des Szenarios an.
func Check(id ID, b domain.DecisionBatch, snap supervisor.ProjectSnapshot) []string {
	switch id {
	case S1:
		return checkS1(b)
	case S2:
		return checkS2(b)
	case S3:
		return checkS3(b, snap)
	case S4:
		return checkS4(b)
	case S5:
		return checkS5(b)
	}
	return []string{fmt.Sprintf("unbekanntes Szenario %q", id)}
}

// targets nennt die Aufträge, auf die Entscheidungen der Aktion zielen.
func targets(b domain.DecisionBatch, action domain.Action) []string {
	var out []string
	for _, d := range b.Decisions {
		if d.Action == action {
			out = append(out, d.Target.ID)
		}
	}
	return out
}

func has(b domain.DecisionBatch, action domain.Action) bool { return len(targets(b, action)) > 0 }

// S1: Implementierung für A und B, keine Implementierung für C (hängt an A).
func checkS1(b domain.DecisionBatch) []string {
	var out []string
	impls := targets(b, domain.ActionImplementation)
	for _, want := range []string{S1A, S1B} {
		if !slices.Contains(impls, want) {
			out = append(out, fmt.Sprintf("S1: keine Implementierung für den unabhängigen, startbaren Auftrag %s", want))
		}
	}
	if slices.Contains(impls, S1C) {
		out = append(out, fmt.Sprintf("S1: Implementierung für %s, obwohl er an %s hängt und erst nach dessen Abschluss startet", S1C, S1A))
	}
	return out
}

// S2: keine Implementierung (die Konzepte sind unreif); Konzeptarbeit beziehungsweise Frage.
func checkS2(b domain.DecisionBatch) []string {
	var out []string
	if has(b, domain.ActionImplementation) {
		out = append(out, "S2: Implementierung beauftragt, obwohl die Konzepte unreif sind")
	}
	if !has(b, domain.ActionConceptWork) && !has(b, domain.ActionAskQuestion) {
		out = append(out, "S2: weder Konzeptarbeit noch eine Frage an den Nutzer")
	}
	for _, d := range b.Decisions {
		if d.Action == domain.ActionRequestIntegration || d.Action == domain.ActionRequestCompletion {
			out = append(out, fmt.Sprintf("S2: %s ohne reifes Konzept", d.Action))
		}
	}
	return out
}

// S3: Review der fertigen Implementierung vor neuer Implementierung; Kapazität eingehalten.
func checkS3(b domain.DecisionBatch, snap supervisor.ProjectSnapshot) []string {
	var out []string
	reviewAt, implAt := -1, -1
	for i, d := range b.Decisions {
		switch {
		case d.Action == domain.ActionReview && d.Target.ID == S3Fin && reviewAt < 0:
			reviewAt = i
			if d.Selection == nil || d.Selection.Role != domain.RoleReviewer {
				out = append(out, "S3: das Review der Implementierung gehört der Rolle reviewer")
			}
			if d.ReviewTarget == nil || d.ReviewTarget.Commit != Commit {
				out = append(out, "S3: das Review ist nicht an den Kopf-Commit der Implementierung gebunden")
			}
		case d.Action == domain.ActionImplementation && implAt < 0:
			implAt = i
		}
	}
	if reviewAt < 0 {
		out = append(out, fmt.Sprintf("S3: kein Review für die fertige Implementierung %s", S3Fin))
	}
	if implAt >= 0 && (reviewAt < 0 || implAt < reviewAt) {
		out = append(out, "S3: neue Implementierung vor dem Review der fertigen")
	}
	free, active := 0, 0
	for _, a := range snap.Capacity.Active {
		if a.Role != domain.RoleSupervisor {
			active++
		}
	}
	free = max(0, snap.Capacity.MaxWorkers-active)
	n := 0
	for _, d := range b.Decisions {
		if isDelegation(d) {
			n++
		}
	}
	if n > free {
		out = append(out, fmt.Sprintf("S3: %d Beauftragungen bei %d freien Plätzen (max_workers %d, %d aktiv)", n, free, snap.Capacity.MaxWorkers, active))
	}
	return out
}

// S4: Reparatur für denselben Auftrag (Implementierung mit der Rolle implementer), keine Integration.
func checkS4(b domain.DecisionBatch) []string {
	var out []string
	repaired := false
	for _, d := range b.Decisions {
		if d.Action == domain.ActionImplementation && d.Target.ID == S4A {
			repaired = true
			if d.Selection == nil || d.Selection.Role != domain.RoleImplementer {
				out = append(out, "S4: die Reparatur gehört der Rolle implementer")
			}
		}
		if d.Action == domain.ActionRequestIntegration || d.Action == domain.ActionRequestCompletion {
			out = append(out, fmt.Sprintf("S4: %s trotz offener Befunde", d.Action))
		}
	}
	if !repaired {
		out = append(out, fmt.Sprintf("S4: keine Reparatur (Implementierung) für %s", S4A))
	}
	return out
}

// S5: Warten; kein Zielabschluss, keine Integrationsausweitung, keine neue Arbeit am Auftrag.
func checkS5(b domain.DecisionBatch) []string {
	var out []string
	if !has(b, domain.ActionWait) {
		out = append(out, "S5: kein wait, obwohl der PR offen ist und der Merge aussteht")
	}
	for _, d := range b.Decisions {
		switch {
		case d.Action == domain.ActionRequestCompletion:
			out = append(out, "S5: Zielabschluss angefordert, obwohl der PR offen ist (Wartezustand waiting_integration, kein Abschluss)")
		case d.Action == domain.ActionRequestIntegration:
			out = append(out, "S5: erneute Integrationsanforderung, obwohl der PR bereits offen ist")
		case isDelegation(d) && d.Target.ID == S5A:
			out = append(out, fmt.Sprintf("S5: %s für den freigegebenen Auftrag %s", d.Action, S5A))
		}
		if d.PlanExtension != nil {
			for _, w := range d.PlanExtension.WorkOrders {
				if w.Mode == domain.ModeAuto {
					out = append(out, fmt.Sprintf("S5: neuer Auftrag %s im Modus auto (Ausweitung des Integrationsmodus)", w.Ref))
				}
			}
		}
	}
	return out
}
