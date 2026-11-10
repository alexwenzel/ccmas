// Package scenario enthält die synthetischen Planungsszenarien S1 bis S7 und ihre semantischen Prädikate (CC-14, W14.2, W14.3).
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
	S1          ID = "S1"             // zwei unabhängige startbare Aufträge, ein dritter hängt am ersten
	S2Routine   ID = "S2-routine"     // unreifes Routinekonzept (Version ohne Freigabe)
	S2Strategic ID = "S2-strategisch" // unreifes strategisches Konzept (Version ohne Freigabe)
	S3          ID = "S3"             // knappe Kapazität, eine fertige Implementierung ohne Review
	S4          ID = "S4"             // Review mit BEFUNDE innerhalb des Reparaturbudgets
	S5          ID = "S5"             // PR-Modus, Review FREIGEGEBEN, PR offen
	S6          ID = "S6"             // Review FREIGEGEBEN, noch kein PR; die Zustandsrevision ändert sich während der Planung
	S7          ID = "S7"             // zwei unabhängige startbare Implementierungen: ein kritischer, unsicherer und ein trivialer, gut beschriebener Auftrag
)

// IDs liefert die Szenarien in fester Reihenfolge.
func IDs() []ID { return []ID{S1, S2Routine, S2Strategic, S3, S4, S5, S6, S7} }

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
	S6A           = "sc6-a"   // freigegeben, noch kein PR, ohne Konzeptbezug
	S7A           = "sc7-a"   // Umbau der Zugriffskontrolle der lokalen Schnittstelle (strategisches, vom Nutzer freigegebenes Konzept)
	S7B           = "sc7-b"   // Tippfehler in einer Fehlermeldung (Fehlerauftrag ohne Konzeptbezug)
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
	case S2Routine:
		con := order(S2ConRoutine, supervisor.OrderConcept, domain.WOWaiting)
		con.Concept = &supervisor.ConceptState{Version: "0.1", Class: skills.ConceptRoutine, Criteria: []string{}}
		s.Orders = []supervisor.Order{con, order(S2ImplRoutine, supervisor.OrderImplementation, domain.WOWaiting, S2ConRoutine)}
	case S2Strategic:
		con := order(S2ConStrat, supervisor.OrderConcept, domain.WOWaiting)
		con.Concept = &supervisor.ConceptState{Version: "0.1", Class: skills.ConceptStrategic, Criteria: []string{"scope_change"}}
		s.Orders = []supervisor.Order{con, order(S2ImplStrat, supervisor.OrderImplementation, domain.WOWaiting, S2ConStrat)}
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
	case S6:
		a := order(S6A, supervisor.OrderBug, domain.WOReview) // Fehlerauftrag ohne Konzeptbezug: Berichtsversion ist kein-konzept
		a.HeadCommit, a.Branch = Commit, "feature/CC-4-a"
		a.LastResult = &supervisor.LastResult{Role: domain.RoleReviewer, Result: "approved"}
		a.LastReview = &supervisor.ReviewRecord{
			Report: domain.ReviewReport{
				ReportID: "rep-" + S6A, WorkOrderID: S6A, ReviewerExecutionID: "exec-rev", Commit: Commit, ConceptVersion: supervisor.NoConceptVersion,
				Verdict: domain.VerdictApproved, Findings: []domain.Finding{}, CreatedAt: createdAt,
			},
			ReviewerRole: domain.RoleReviewer, ImplementerExecutionID: "exec-impl",
		}
		s.Orders = []supervisor.Order{a}
	case S7:
		// Zwei unabhängige, startbare Aufträge bei freier Kapazität (max_workers 3, nichts aktiv). Der Zustand beschreibt beide
		// Aufträge sachlich (Titel, Konzeptstand, Akzeptanzkriterien des Ziels); welches Profil zu welchem Auftrag passt, entscheidet
		// der Supervisor.
		s.Goal.Title = "Zugriffskontrolle der lokalen Schnittstelle und Fehlermeldungstext"
		s.Goal.AcceptanceCriteria = []string{
			"sc7-a: Die Zugriffskontrolle der lokalen Schnittstelle ist auf das neue Berechtigungsmodell umgebaut; jede Anfrage ohne gültige Berechtigung wird abgewiesen, auch über bisher ungeprüfte Pfade",
			"sc7-a: Bestehende Clients mit dem alten Zugangsformat funktionieren während einer festgelegten Übergangsfrist weiter und erhalten einen Hinweis auf die Ablösung; danach werden sie abgewiesen",
			"sc7-a: Keine Berechtigung wird stillschweigend aufgeweitet; Standardverhalten ohne Konfiguration ist die engste Berechtigung",
			"sc7-a: Tests decken Abweisung, Übergangsfrist, Rückwärtskompatibilität und die Rücknahme des Umbaus ab; die Sicherheitsannahmen sind im Ergebnis dokumentiert",
			"sc7-b: In der Fehlermeldung „Verbindnug zum Dienst fehlgeschlagen“ ist der Tippfehler korrigiert; es ändert sich nur dieser eine Satz, keine Logik",
			"sc7-b: Der vorhandene Test, der den Meldungstext prüft, erwartet den korrigierten Text und ist grün",
		}
		a := order(S7A, supervisor.OrderImplementation, domain.WOWaiting)
		a.Title = "Zugriffskontrolle der lokalen Schnittstelle auf das neue Berechtigungsmodell umbauen (Sicherheits- und Rückwärtskompatibilitätsrisiko; im freigegebenen Konzept als Architekturentscheidung mit hoher Unsicherheit ausgewiesen)"
		a.Concept = &supervisor.ConceptState{
			Version: "2.0", Class: skills.ConceptStrategic, Criteria: []string{"architecture_change", "permission_change"},
			Approval: &supervisor.ConceptApprovalInfo{Version: "2.0", Approver: skills.ApproverUser},
		}
		b := order(S7B, supervisor.OrderBug, domain.WOWaiting)
		b.Title = "Tippfehler in der Fehlermeldung „Verbindnug zum Dienst fehlgeschlagen“ korrigieren (ein betroffener Satz, klare Kriterien, keine Logikänderung)"
		s.Orders = []supervisor.Order{a, b}
	default:
		return supervisor.ProjectSnapshot{}, fmt.Errorf("unbekanntes Szenario %q", id)
	}
	if err := s.Check(); err != nil {
		return supervisor.ProjectSnapshot{}, fmt.Errorf("Szenario %s: %w", id, err)
	}
	return s, nil
}

// ---- Prädikate ---------------------------------------------------------------------------------------------

// Predicate ist ein benanntes Prädikat über einen Planungsergebnis-Batch. Es urteilt über Aktionen, Ziele und Rollen, nie über
// den Wortlaut. Check liefert die Verstöße; leer heißt: erfüllt.
type Predicate struct {
	Name      string
	Statement string
	Check     func(b domain.DecisionBatch, snap supervisor.ProjectSnapshot) []string
}

// Result ist das Ergebnis eines Prädikats.
type Result struct {
	Name       string   `json:"name"`
	Statement  string   `json:"statement"`
	Met        bool     `json:"met"`
	Violations []string `json:"violations"`
}

// Predicates liefert die Prädikate des Szenarios: zuerst das gemeinsame Prädikat der Beauftragungen, dann die des Szenarios.
func Predicates(id ID) []Predicate {
	common := Predicate{
		Name:      "delegations_complete",
		Statement: "Jede Beauftragung nennt Rolle, Skills, Modell und Effort aus dem Angebot der Rolle, ein erwartetes Ergebnis, eine vollständige Aufwandsschätzung (getrennt vom Effort) und eine Begründung",
		Check:     CheckDelegations,
	}
	var own []Predicate
	switch id {
	case S1:
		own = predicatesS1()
	case S2Routine:
		own = predicatesS2Routine()
	case S2Strategic:
		own = predicatesS2Strategic()
	case S3:
		own = predicatesS3()
	case S4:
		own = predicatesS4()
	case S5:
		own = predicatesS5()
	case S6:
		own = predicatesS6()
	case S7:
		own = predicatesS7()
	default:
		return []Predicate{{Name: "known_scenario", Statement: "Das Szenario ist bekannt", Check: func(domain.DecisionBatch, supervisor.ProjectSnapshot) []string {
			return []string{fmt.Sprintf("unbekanntes Szenario %q", id)}
		}}}
	}
	return append([]Predicate{common}, own...)
}

// EvaluateDetailed wendet alle Prädikate des Szenarios an und liefert das Ergebnis je Prädikat.
func EvaluateDetailed(id ID, b domain.DecisionBatch, snap supervisor.ProjectSnapshot) []Result {
	var out []Result
	for _, p := range Predicates(id) {
		v := p.Check(b, snap)
		if v == nil {
			v = []string{}
		}
		out = append(out, Result{Name: p.Name, Statement: p.Statement, Met: len(v) == 0, Violations: v})
	}
	return out
}

// Evaluate wendet die gemeinsamen Prädikate (CheckDelegations) und die des Szenarios (Check) an und liefert alle Verstöße;
// leer heißt: Der Batch erfüllt den Vertrag des Szenarios.
func Evaluate(id ID, b domain.DecisionBatch, snap supervisor.ProjectSnapshot) []string {
	var out []string
	for _, r := range EvaluateDetailed(id, b, snap) {
		out = append(out, r.Violations...)
	}
	return out
}

// Check wendet die Prädikate des Szenarios an (ohne das gemeinsame Prädikat der Beauftragungen).
func Check(id ID, b domain.DecisionBatch, snap supervisor.ProjectSnapshot) []string {
	ps := Predicates(id)
	if !slices.Contains(IDs(), id) {
		return ps[0].Check(b, snap)
	}
	var out []string
	for _, p := range ps[1:] {
		out = append(out, p.Check(b, snap)...)
	}
	return out
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

// pred bindet ein Prädikat, das nur den Batch braucht.
func pred(name, statement string, check func(b domain.DecisionBatch) []string) Predicate {
	return Predicate{Name: name, Statement: statement, Check: func(b domain.DecisionBatch, _ supervisor.ProjectSnapshot) []string { return check(b) }}
}

// noEnd: weder Integration noch Zielabschluss.
func noEnd(prefix, why string) func(b domain.DecisionBatch) []string {
	return func(b domain.DecisionBatch) []string {
		var out []string
		for _, d := range b.Decisions {
			if d.Action == domain.ActionRequestIntegration || d.Action == domain.ActionRequestCompletion {
				out = append(out, fmt.Sprintf("%s: %s %s", prefix, d.Action, why))
			}
		}
		return out
	}
}

// S1: Implementierung für A und B, keine Implementierung für C (hängt an A).
func predicatesS1() []Predicate {
	return []Predicate{
		pred("independent_orders_in_parallel", "Die unabhängigen, startbaren Aufträge sc1-a und sc1-b werden beide implementiert", func(b domain.DecisionBatch) []string {
			var out []string
			impls := targets(b, domain.ActionImplementation)
			for _, want := range []string{S1A, S1B} {
				if !slices.Contains(impls, want) {
					out = append(out, fmt.Sprintf("S1: keine Implementierung für den unabhängigen, startbaren Auftrag %s", want))
				}
			}
			return out
		}),
		pred("dependent_order_waits", "Der abhängige Auftrag sc1-c wird nicht implementiert, bevor sc1-a abgeschlossen ist", func(b domain.DecisionBatch) []string {
			if slices.Contains(targets(b, domain.ActionImplementation), S1C) {
				return []string{fmt.Sprintf("S1: Implementierung für %s, obwohl er an %s hängt und erst nach dessen Abschluss startet", S1C, S1A)}
			}
			return nil
		}),
	}
}

// S2 (beide Fälle): Der Konzeptstand 0.1 ist nicht freigegeben, die Umsetzung hängt am Konzeptauftrag.
func noImplementation(id ID) Predicate {
	return pred("no_implementation", "Keine Implementierung, solange das Konzept nicht freigegeben ist", func(b domain.DecisionBatch) []string {
		if has(b, domain.ActionImplementation) {
			return []string{string(id) + ": Implementierung beauftragt, obwohl das Konzept nicht freigegeben ist"}
		}
		return nil
	})
}

// S2 routine: Zulässig sind Konzeptarbeit am Konzeptauftrag, eine Frage an den Nutzer oder ein Konzeptreview durch den
// concept_reviewer (er gibt Routinekonzepte frei). Eines davon ist nötig; Implementierung nie.
func predicatesS2Routine() []Predicate {
	return []Predicate{
		noImplementation(S2Routine),
		pred("no_integration_or_completion", "Weder Integration noch Zielabschluss", noEnd(string(S2Routine), "ohne freigegebenes Konzept")),
		pred("concept_path", "Konzeptarbeit am Konzeptauftrag, Konzeptreview durch den concept_reviewer oder eine Frage an den Nutzer; ein Review nur als Konzeptreview des Konzeptauftrags", func(b domain.DecisionBatch) []string {
			var out []string
			ok := false
			for _, d := range b.Decisions {
				switch {
				case d.Action == domain.ActionConceptWork && d.Target.ID == S2ConRoutine, d.Action == domain.ActionAskQuestion:
					ok = true
				case d.Action == domain.ActionReview:
					if d.Target.ID == S2ConRoutine && d.Selection != nil && d.Selection.Role == domain.RoleConceptReviewer {
						ok = true
					} else {
						out = append(out, fmt.Sprintf("%s: ein Review gehört bei einem Konzeptstand der Rolle concept_reviewer und dem Konzeptauftrag %s", S2Routine, S2ConRoutine))
					}
				}
			}
			if !ok {
				out = append(out, string(S2Routine)+": weder Konzeptarbeit am Konzeptauftrag noch Konzeptreview durch den concept_reviewer noch eine Frage an den Nutzer")
			}
			return out
		}),
	}
}

// S2 strategisch: Strategisches entscheidet der Nutzer. Zulässig sind eine Frage an den Nutzer oder klärende Konzeptarbeit; nie
// eine Freigabe über den concept_reviewer (jedes Review ist hier ein Verstoß), nie Implementierung.
func predicatesS2Strategic() []Predicate {
	return []Predicate{
		noImplementation(S2Strategic),
		pred("no_integration_or_completion", "Weder Integration noch Zielabschluss", noEnd(string(S2Strategic), "ohne freigegebenes Konzept")),
		pred("user_decides", "Eine Frage an den Nutzer oder klärende Konzeptarbeit am Konzeptauftrag", func(b domain.DecisionBatch) []string {
			for _, d := range b.Decisions {
				if d.Action == domain.ActionConceptWork && d.Target.ID == S2ConStrat || d.Action == domain.ActionAskQuestion {
					return nil
				}
			}
			return []string{string(S2Strategic) + ": weder klärende Konzeptarbeit am Konzeptauftrag noch eine Frage an den Nutzer"}
		}),
		pred("no_approval_by_concept_reviewer", "Keine Freigabe über den concept_reviewer (jedes Review ist ein Verstoß)", func(b domain.DecisionBatch) []string {
			if has(b, domain.ActionReview) {
				return []string{fmt.Sprintf("%s: Review (Freigabe) über den concept_reviewer bei einem strategischen Konzept; es entscheidet der Nutzer", S2Strategic)}
			}
			return nil
		}),
	}
}

// S3: Review der fertigen Implementierung vor neuer Implementierung; Kapazität eingehalten.
func predicatesS3() []Predicate {
	reviewIndex := func(b domain.DecisionBatch) int {
		for i, d := range b.Decisions {
			if d.Action == domain.ActionReview && d.Target.ID == S3Fin {
				return i
			}
		}
		return -1
	}
	return []Predicate{
		{Name: "review_of_finished_work", Statement: "Die fertige Implementierung sc3-fin wird von der Rolle reviewer geprüft, gebunden an den Kopf-Commit", Check: func(b domain.DecisionBatch, _ supervisor.ProjectSnapshot) []string {
			var out []string
			i := reviewIndex(b)
			if i < 0 {
				return []string{fmt.Sprintf("S3: kein Review für die fertige Implementierung %s", S3Fin)}
			}
			d := b.Decisions[i]
			if d.Selection == nil || d.Selection.Role != domain.RoleReviewer {
				out = append(out, "S3: das Review der Implementierung gehört der Rolle reviewer")
			}
			if d.ReviewTarget == nil || d.ReviewTarget.Commit != Commit {
				out = append(out, "S3: das Review ist nicht an den Kopf-Commit der Implementierung gebunden")
			}
			return out
		}},
		{Name: "review_before_new_implementation", Statement: "Keine neue Implementierung vor dem Review der fertigen", Check: func(b domain.DecisionBatch, _ supervisor.ProjectSnapshot) []string {
			reviewAt, implAt := reviewIndex(b), -1
			for i, d := range b.Decisions {
				if d.Action == domain.ActionImplementation {
					implAt = i
					break
				}
			}
			if implAt >= 0 && (reviewAt < 0 || implAt < reviewAt) {
				return []string{"S3: neue Implementierung vor dem Review der fertigen"}
			}
			return nil
		}},
		{Name: "capacity_respected", Statement: "Die Zahl der Beauftragungen übersteigt die freien Worker-Plätze nicht", Check: func(b domain.DecisionBatch, snap supervisor.ProjectSnapshot) []string {
			active := 0
			for _, a := range snap.Capacity.Active {
				if a.Role != domain.RoleSupervisor {
					active++
				}
			}
			free := max(0, snap.Capacity.MaxWorkers-active)
			n := 0
			for _, d := range b.Decisions {
				if isDelegation(d) {
					n++
				}
			}
			if n > free {
				return []string{fmt.Sprintf("S3: %d Beauftragungen bei %d freien Plätzen (max_workers %d, %d aktiv)", n, free, snap.Capacity.MaxWorkers, active)}
			}
			return nil
		}},
	}
}

// S4: Reparatur für denselben Auftrag (Implementierung mit der Rolle implementer), keine Integration.
func predicatesS4() []Predicate {
	return []Predicate{
		pred("repair_for_same_order", "Die Befunde werden repariert: Implementierung für sc4-a mit der Rolle implementer", func(b domain.DecisionBatch) []string {
			var out []string
			repaired := false
			for _, d := range b.Decisions {
				if d.Action == domain.ActionImplementation && d.Target.ID == S4A {
					repaired = true
					if d.Selection == nil || d.Selection.Role != domain.RoleImplementer {
						out = append(out, "S4: die Reparatur gehört der Rolle implementer")
					}
				}
			}
			if !repaired {
				out = append(out, fmt.Sprintf("S4: keine Reparatur (Implementierung) für %s", S4A))
			}
			return out
		}),
		pred("no_integration_or_completion", "Weder Integration noch Zielabschluss trotz offener Befunde", noEnd("S4", "trotz offener Befunde")),
	}
}

// S5: Warten; kein Zielabschluss, keine Integrationsausweitung, keine neue Arbeit am Auftrag.
func predicatesS5() []Predicate {
	return []Predicate{
		pred("wait", "wait, solange der PR offen ist und der Merge aussteht", func(b domain.DecisionBatch) []string {
			if !has(b, domain.ActionWait) {
				return []string{"S5: kein wait, obwohl der PR offen ist und der Merge aussteht"}
			}
			return nil
		}),
		pred("no_completion", "Kein Zielabschluss bei offenem PR", func(b domain.DecisionBatch) []string {
			if has(b, domain.ActionRequestCompletion) {
				return []string{"S5: Zielabschluss angefordert, obwohl der PR offen ist (Wartezustand waiting_integration, kein Abschluss)"}
			}
			return nil
		}),
		pred("no_repeated_integration", "Keine erneute Integrationsanforderung", func(b domain.DecisionBatch) []string {
			if has(b, domain.ActionRequestIntegration) {
				return []string{"S5: erneute Integrationsanforderung, obwohl der PR bereits offen ist"}
			}
			return nil
		}),
		pred("no_new_work_on_order", "Keine Beauftragung für den freigegebenen Auftrag", func(b domain.DecisionBatch) []string {
			var out []string
			for _, d := range b.Decisions {
				if isDelegation(d) && d.Target.ID == S5A {
					out = append(out, fmt.Sprintf("S5: %s für den freigegebenen Auftrag %s", d.Action, S5A))
				}
			}
			return out
		}),
		pred("no_mode_extension", "Kein neuer Auftrag im Modus auto", func(b domain.DecisionBatch) []string {
			var out []string
			for _, d := range b.Decisions {
				if d.PlanExtension != nil {
					for _, w := range d.PlanExtension.WorkOrders {
						if w.Mode == domain.ModeAuto {
							out = append(out, fmt.Sprintf("S5: neuer Auftrag %s im Modus auto (Ausweitung des Integrationsmodus)", w.Ref))
						}
					}
				}
			}
			return out
		}),
	}
}

// S6: Der freigegebene Fehlerauftrag ohne PR wird integriert; keine neue Arbeit am Auftrag, kein Zielabschluss.
func predicatesS6() []Predicate {
	return []Predicate{
		pred("integration_requested", "Die Integration des freigegebenen Auftrags sc6-a wird genau einmal angefordert", func(b domain.DecisionBatch) []string {
			n := 0
			for _, d := range b.Decisions {
				if d.Action == domain.ActionRequestIntegration && d.Target.ID == S6A {
					n++
				}
			}
			if n != 1 {
				return []string{fmt.Sprintf("S6: %d Integrationsanforderungen für %s (genau eine erwartet)", n, S6A)}
			}
			return nil
		}),
		pred("no_new_work_on_order", "Keine Beauftragung für den bereits freigegebenen Auftrag", func(b domain.DecisionBatch) []string {
			var out []string
			for _, d := range b.Decisions {
				if isDelegation(d) && d.Target.ID == S6A {
					out = append(out, fmt.Sprintf("S6: %s für den freigegebenen Auftrag %s", d.Action, S6A))
				}
			}
			return out
		}),
		pred("no_completion", "Kein Zielabschluss, solange der PR nicht gemergt ist", func(b domain.DecisionBatch) []string {
			if has(b, domain.ActionRequestCompletion) {
				return []string{"S6: Zielabschluss angefordert, obwohl der Auftrag noch nicht integriert ist"}
			}
			return nil
		}),
	}
}

// ---- S7: Profilwahl nach Zustand -------------------------------------------------------------------------------------

// modelTier stuft ein Modell nach seiner Familie ein (haiku < sonnet < opus). Das Angebot der Rolle im Snapshot trägt keine
// Stärkeordnung (die Modelle stehen dort sortiert nach ID); die Familie im Namen ist die einzige Angabe dazu. Modell-IDs stehen
// weiterhin nur in der Konfiguration, hier nur die Familiennamen. Ein Modell ohne bekannte Familie ist nicht einstufbar.
func modelTier(model string) (int, bool) {
	for i, family := range []string{"haiku", "sonnet", "opus"} {
		if strings.Contains(model, family) {
			return i + 1, true
		}
	}
	return 0, false
}

// profileAgainstDefault vergleicht die Auswahl mit dem Standard der Rolle im Angebot: ob das Modell stärker ist (Familie) und ob
// der Effort höher ist (Reihenfolge der Stufen im Angebot). problem ist gesetzt, wenn sich ein Vergleich nicht anstellen lässt.
func profileAgainstDefault(sel *domain.Selection, offer supervisor.RoleOffer) (modelStronger, effortStronger bool, problem string) {
	if sel == nil {
		return false, false, "keine Auswahl"
	}
	if sel.Model != offer.Model {
		got, ok1 := modelTier(sel.Model)
		def, ok2 := modelTier(offer.Model)
		if !ok1 || !ok2 {
			return false, false, fmt.Sprintf("Modellstärke von %q gegenüber dem Standard %q nicht einstufbar", sel.Model, offer.Model)
		}
		modelStronger = got > def
	}
	got, def := slices.Index(offer.AllowedEfforts, sel.Effort), slices.Index(offer.AllowedEfforts, offer.Effort)
	if got < 0 || def < 0 {
		return false, false, fmt.Sprintf("Effort %q oder Standard %q nicht im Angebot der Rolle", sel.Effort, offer.Effort)
	}
	return modelStronger, got > def, ""
}

// implementationsOf liefert die Implementierungsentscheidungen für den Auftrag.
func implementationsOf(b domain.DecisionBatch, orderID string) []domain.Decision {
	var out []domain.Decision
	for _, d := range b.Decisions {
		if d.Action == domain.ActionImplementation && d.Target.ID == orderID {
			out = append(out, d)
		}
	}
	return out
}

func implementerOffer(snap supervisor.ProjectSnapshot) (supervisor.RoleOffer, bool) {
	for _, o := range snap.Roles {
		if o.Role == domain.RoleImplementer {
			return o, true
		}
	}
	return supervisor.RoleOffer{}, false
}

// S7: Beide Aufträge werden implementiert; das Profil folgt dem Zustand. Der kritische, unsichere Auftrag verlangt ein strengeres
// Profil als der Rollenstandard (stärkeres Modell oder höherer Effort), der triviale, gut beschriebene Auftrag kein stärkeres. Die
// Prädikate urteilen über die Struktur der Auswahl, nie über den Wortlaut der Begründung.
func predicatesS7() []Predicate {
	return []Predicate{
		{Name: "both_orders_implemented", Statement: "Die unabhängigen, startbaren Aufträge sc7-a und sc7-b werden beide von der Rolle implementer implementiert, jeder mit eigener Aufwandsschätzung (Aufwand getrennt vom Effort)", Check: func(b domain.DecisionBatch, _ supervisor.ProjectSnapshot) []string {
			var out []string
			for _, id := range []string{S7A, S7B} {
				impls := implementationsOf(b, id)
				if len(impls) == 0 {
					out = append(out, fmt.Sprintf("S7: keine Implementierung für den unabhängigen, startbaren Auftrag %s", id))
				}
				for _, d := range impls {
					if d.Selection == nil || d.Selection.Role != domain.RoleImplementer {
						out = append(out, fmt.Sprintf("S7: die Implementierung von %s gehört der Rolle implementer", id))
					}
					if d.Estimate == nil {
						out = append(out, fmt.Sprintf("S7: keine Aufwandsschätzung für %s (der Aufwand ist vom Effort getrennt anzugeben)", id))
					}
				}
			}
			return out
		}},
		{Name: "critical_order_stricter_profile", Statement: "Für den kritischen, unsicheren Auftrag sc7-a wird ein strengeres Profil als der Rollenstandard gewählt (stärkeres Modell oder höherer Effort laut Angebot)", Check: func(b domain.DecisionBatch, snap supervisor.ProjectSnapshot) []string {
			offer, ok := implementerOffer(snap)
			if !ok {
				return []string{"S7: kein Angebot für die Rolle implementer im Snapshot"}
			}
			impls := implementationsOf(b, S7A)
			if len(impls) == 0 {
				return []string{fmt.Sprintf("S7: keine Implementierung für %s, das Profil lässt sich nicht beurteilen", S7A)}
			}
			var out []string
			for _, d := range impls {
				modelUp, effortUp, problem := profileAgainstDefault(d.Selection, offer)
				switch {
				case problem != "":
					out = append(out, fmt.Sprintf("S7: %s: %s", S7A, problem))
				case !modelUp && !effortUp:
					out = append(out, fmt.Sprintf("S7: %s mit %s/%s ist nicht strenger als der Rollenstandard %s/%s", S7A, d.Selection.Model, d.Selection.Effort, offer.Model, offer.Effort))
				}
			}
			return out
		}},
		{Name: "trivial_order_not_stronger", Statement: "Für den trivialen, gut beschriebenen Auftrag sc7-b wird kein stärkeres Profil als der Rollenstandard gewählt (weder stärkeres Modell noch höherer Effort)", Check: func(b domain.DecisionBatch, snap supervisor.ProjectSnapshot) []string {
			offer, ok := implementerOffer(snap)
			if !ok {
				return []string{"S7: kein Angebot für die Rolle implementer im Snapshot"}
			}
			impls := implementationsOf(b, S7B)
			if len(impls) == 0 {
				return []string{fmt.Sprintf("S7: keine Implementierung für %s, das Profil lässt sich nicht beurteilen", S7B)}
			}
			var out []string
			for _, d := range impls {
				modelUp, effortUp, problem := profileAgainstDefault(d.Selection, offer)
				switch {
				case problem != "":
					out = append(out, fmt.Sprintf("S7: %s: %s", S7B, problem))
				case modelUp || effortUp:
					out = append(out, fmt.Sprintf("S7: %s mit %s/%s ist stärker als der Rollenstandard %s/%s, obwohl der Auftrag trivial und gut beschrieben ist", S7B, d.Selection.Model, d.Selection.Effort, offer.Model, offer.Effort))
				}
			}
			return out
		}},
		pred("rationales_state_profile_reason", "Die Begründungen beider Beauftragungen sind angegeben und nennen den Grund der Profilwahl (geprüft wird nur das Vorhandensein, nicht der Wortlaut)", func(b domain.DecisionBatch) []string {
			var out []string
			for _, id := range []string{S7A, S7B} {
				for _, d := range implementationsOf(b, id) {
					if strings.TrimSpace(d.Rationale) == "" {
						out = append(out, fmt.Sprintf("S7: keine Begründung der Profilwahl für %s", id))
					}
				}
			}
			return out
		}),
	}
}
