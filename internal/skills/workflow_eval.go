package skills

import (
	"slices"

	"github.com/alexwenzel/ccmas/internal/domain"
)

// Dieser Teil enthält die kleinen Auswerter der Plattform auf den geprüften Rollenregeln. Sie
// entscheiden Zulässigkeit, nicht Fachplanung: Welche Arbeit als Nächstes sinnvoll ist, beurteilt der
// Supervisor; diese Funktionen sagen nur, was die Regeln erlauben oder verlangen. Alle sind
// fail-safe: Unbekanntes, Fehlendes und Widersprüchliches führt zur strengeren Seite. Das gilt auch für
// fehlende Regeln: Ein nil-*Rules (Regeln nicht geladen) ergibt keine Freigabe, sondern missing_rules
// beziehungsweise die strengste Antwort. Geladen werden die Regeln mit LoadRules oder LoadChecked.

// errNoRules ist die Antwort der Auswerter ohne geladene Regeln.
func errNoRules() *Error {
	return newErr(CodeMissingRules, "", rulesRel, "Rollenregeln nicht geladen: ohne Regeln entscheidet die Plattform nichts (fail-safe)")
}

// ---- Review vor dem Push ---------------------------------------------------------

// PushAllowed entscheidet, ob ein Stand gepusht und integriert werden darf (Aktion request_integration).
// Verlangt wird ein Review-Bericht mit Urteil FREIGEGEBEN, der zu genau diesem Auftrag, Bericht, Commit
// und dieser Konzeptversion gehört (domain.IntegrationRequest.CheckReview), und der von einer anderen
// Ausführung stammt als die Implementierung. Fehlt eine Kennung, gilt die Unabhängigkeit als nicht
// nachgewiesen. Jede weitere Änderung am Stand (anderer Commit, andere Konzeptversion) macht den Bericht
// unbrauchbar; ein neues Review ist nötig.
//
// reviewerRole ist die Rolle, in der die Plattform die Ausführung report.ReviewerExecutionID gestartet hat
// (Ausführungskontext, nicht Inhalt des Berichts). Nur die Rolle reviewer gibt Code frei: Ein Bericht des
// Konzeptprüfers (concept_reviewer) trägt als commit nur den Bezugsstand des Code-Repositorys und ist keine
// Code-Freigabe. Das ReviewReport-Schema hat kein Rollenfeld; ein Bericht kann seine Rolle nicht selbst
// belegen, deshalb liefert der Aufrufer sie aus dem Ausführungsdatensatz. Fehlt sie, gilt der Bericht nicht.
//
// Fehlercodes: review_missing, review_invalid, review_not_independent, missing_rules.
func (r *Rules) PushAllowed(req domain.IntegrationRequest, report *domain.ReviewReport, implementerExecutionID string, reviewerRole domain.Role) error {
	if r == nil {
		return errNoRules()
	}
	if report == nil {
		return newErr(CodeReviewMissing, "", "", "Kein Review-Bericht: ohne unabhängiges Review mit Urteil %s wird nicht gepusht", domain.VerdictApproved)
	}
	if reviewerRole != domain.RoleReviewer {
		return newErr(CodeReviewInvalid, "", "", "Review-Bericht %s stammt aus der Rolle %q; Code gibt nur ein Review der Rolle %s frei (ein Konzeptprüfbericht ist keine Code-Freigabe)", report.ReportID, reviewerRole, domain.RoleReviewer)
	}
	if err := req.CheckReview(*report); err != nil {
		return newErr(CodeReviewInvalid, "", "", "Review-Bericht %s trägt den Push nicht: %v", report.ReportID, err)
	}
	switch {
	case implementerExecutionID == "":
		return newErr(CodeReviewNotIndependent, "", "", "Ausführung der Implementierung unbekannt: Unabhängigkeit des Reviews nicht nachgewiesen")
	case report.ReviewerExecutionID == "":
		return newErr(CodeReviewNotIndependent, "", "", "Ausführung des Reviews unbekannt: Unabhängigkeit des Reviews nicht nachgewiesen")
	case report.ReviewerExecutionID == implementerExecutionID:
		return newErr(CodeReviewNotIndependent, "", "", "Review und Implementierung stammen aus derselben Ausführung %s; verlangt ist eine andere, frische Ausführung", implementerExecutionID)
	}
	return nil
}

// ---- Konzeptfreigabe -------------------------------------------------------------

// ConceptClass ist die Einstufung eines Konzepts.
type ConceptClass string

const (
	ConceptRoutine   ConceptClass = "routine"
	ConceptStrategic ConceptClass = "strategic"
)

// Approver nennt, wer ein Konzept freigibt.
type Approver string

const (
	ApproverConceptReviewer Approver = "concept_reviewer"
	ApproverUser            Approver = "user"
)

// ConceptAssessment ist die Einstufung eines Konzepts durch Konzept-Agent oder Konzeptprüfer: die Klasse und
// die zutreffenden Kriterien für „strategisch“ (Kennungen aus der Regeldatei).
type ConceptAssessment struct {
	Class    ConceptClass
	Criteria []string
}

// ApprovalRoute sagt, wer entscheidet und mit welcher Aktion die Entscheidung angefordert wird.
type ApprovalRoute struct {
	Approver Approver
	Role     domain.Role // nur bei Approver concept_reviewer
	Action   domain.Action
}

// ConceptApprover bestimmt den Freigabeweg: Ein Routinekonzept ohne zutreffendes Kriterium prüft der
// unabhängige Konzeptprüfer; strategische, nicht einstufbare, unbekannt eingestufte oder widersprüchliche
// Fälle (Routine, aber ein Kriterium trifft zu) entscheidet der Nutzer.
func (r *Rules) ConceptApprover(a ConceptAssessment) ApprovalRoute {
	if r == nil { // ohne Regeln entscheidet der Nutzer
		return ApprovalRoute{Approver: ApproverUser, Action: domain.ActionAskQuestion}
	}
	ca := r.doc.ConceptApproval
	user := ApprovalRoute{Approver: ApproverUser, Action: ca.Unclassifiable.Action}
	if a.Class == ConceptStrategic {
		return ApprovalRoute{Approver: ApproverUser, Action: ca.Strategic.Action}
	}
	if a.Class != ConceptRoutine || len(a.Criteria) > 0 {
		return user
	}
	return ApprovalRoute{Approver: ApproverConceptReviewer, Role: domain.RoleConceptReviewer, Action: ca.Routine.Action}
}

// ConceptApproval ist eine erteilte Konzeptfreigabe.
type ConceptApproval struct {
	Version  string
	Approver Approver
}

// ConceptApprovalValid prüft, ob eine Freigabe für die aktuelle Konzeptversion gilt: gleiche Version
// (nicht leer), bekannter Freigeber, und der Konzeptprüfer gibt nur frei, was nach der Einstufung Routine
// ist. Der Nutzer darf jede Version freigeben, ebenfalls nur die genannte. Fehlercode:
// concept_approval_invalid.
func (r *Rules) ConceptApprovalValid(ap ConceptApproval, a ConceptAssessment, currentVersion string) error {
	if r == nil {
		return errNoRules()
	}
	switch {
	case ap.Version == "" || currentVersion == "":
		return newErr(CodeConceptApprovalInvalid, "", "", "Freigabe ohne Konzeptversion: eine Freigabe gilt nur für eine bestimmte Version")
	case ap.Version != currentVersion:
		return newErr(CodeConceptApprovalInvalid, "", "", "Freigabe gilt für Konzeptversion %q, nicht für %q", ap.Version, currentVersion)
	}
	switch ap.Approver {
	case ApproverUser:
		return nil
	case ApproverConceptReviewer:
		if r.ConceptApprover(a).Approver != ApproverConceptReviewer {
			return newErr(CodeConceptApprovalInvalid, "", "", "Der Konzeptprüfer darf dieses Konzept nicht freigeben (strategisch, nicht einstufbar oder widersprüchlich); es entscheidet der Nutzer")
		}
		return nil
	}
	return newErr(CodeConceptApprovalInvalid, "", "", "Unbekannter Freigeber %q", ap.Approver)
}

// ---- Reparaturschleife -----------------------------------------------------------

// Step ist ein Folgeschritt: Aktion, Zielrolle (leer bei Aktionen ohne Rolle) und die mindestens
// erforderlichen Voraussetzungen.
type Step struct {
	Action   domain.Action
	Role     domain.Role
	Requires []domain.PreconditionKind
}

// RepairDecision ist die Entscheidung über eine weitere Reparaturrunde.
type RepairDecision struct {
	Round int
	Halt  bool // Budget überschritten: sichtbarer Halt, keine weitere Runde
	Step  Step // bei Halt die Frage an den Nutzer, sonst die Implementierung mit den Befunden
}

// MaxRepairRounds liefert das Budget an Reparaturrunden (Standard 3). Es gilt gleich für die Code-Schleife
// (RepairStep) und die Konzeptprüfschleife (ConceptRepairStep); ohne Regeln ist es 0 (keine weitere Runde).
func (r *Rules) MaxRepairRounds() int {
	if r == nil {
		return 0
	}
	return r.doc.RepairLoop.MaxRounds
}

// RepairStep entscheidet für die n-te Reparaturrunde der Code-Schleife (1-basiert, die erste Runde folgt dem
// ersten Review mit BEFUNDE): innerhalb des Budgets Implementierung mit den Befunden und danach frisches
// Review, darüber hinaus ein sichtbarer Halt mit Frage an den Nutzer.
func (r *Rules) RepairStep(round int) (RepairDecision, error) {
	if r == nil {
		return RepairDecision{}, errNoRules()
	}
	return r.repairStep(round, r.doc.RepairLoop.OnFindings)
}

// ConceptRepairStep entscheidet für die n-te Runde der Konzeptprüfschleife (concept_findings -> concept_work ->
// erneute Prüfung durch eine frische Ausführung; 1-basiert, die erste Runde folgt der ersten Konzeptprüfung mit
// BEFUNDE). Sie hat dasselbe Budget wie die Code-Schleife (repair_loop.max_rounds), wird aber je Schleife
// getrennt gezählt: Innerhalb des Budgets Konzeptarbeit mit den Befunden, darüber hinaus ein sichtbarer
// Halt mit Frage an den Nutzer.
func (r *Rules) ConceptRepairStep(round int) (RepairDecision, error) {
	if r == nil {
		return RepairDecision{}, errNoRules()
	}
	return r.repairStep(round, r.doc.RepairLoop.OnConceptFindings)
}

func (r *Rules) repairStep(round int, onFindings stepDoc) (RepairDecision, error) {
	if round < 1 {
		return RepairDecision{}, newErr(CodeInvalidRequest, "", "", "Reparaturrunde %d ungültig (zählt ab 1)", round)
	}
	rl := r.doc.RepairLoop
	if round > rl.MaxRounds {
		return RepairDecision{Round: round, Halt: true, Step: Step{Action: rl.OnExhausted.Action}}, nil
	}
	return RepairDecision{Round: round, Step: Step{Action: onFindings.Action, Role: onFindings.Role}}, nil
}

// ---- Rollenübergänge -------------------------------------------------------------

// NextSteps liefert die zulässigen Folgeschritte für das Ergebnis einer Rolle; der erste ist der
// Standardschritt. Unbekannte Rolle oder unbekanntes Ergebnis ist ein Fehler (invalid_request).
func (r *Rules) NextSteps(role domain.Role, result string) ([]Step, error) {
	if r == nil {
		return nil, errNoRules()
	}
	rd, ok := r.doc.Roles[role]
	if !ok {
		return nil, newErr(CodeInvalidRequest, "", "", "Rolle %q hat keine Rollenregeln", role)
	}
	res, ok := rd.Results[result]
	if !ok {
		return nil, newErr(CodeInvalidRequest, "", "", "Rolle %s kennt das Ergebnis %q nicht (bekannt: %v)", role, result, r.ResultIDs(role))
	}
	out := make([]Step, len(res.Next))
	for i, s := range res.Next {
		out[i] = Step{Action: s.Action, Role: s.Role, Requires: nonNil(slices.Clone(s.Requires))}
	}
	return out, nil
}

// NextStep liefert den Standardschritt (den ersten) für das Ergebnis einer Rolle.
func (r *Rules) NextStep(role domain.Role, result string) (Step, error) {
	steps, err := r.NextSteps(role, result)
	if err != nil {
		return Step{}, err
	}
	return steps[0], nil
}

// ---- Integrationsmodus -----------------------------------------------------------

// IntegrationModeAllowed prüft, ob ein angeforderter Modus zum beauftragten passt. Der Modus wird je Auftrag
// ausdrücklich gesetzt; er darf nie aufgeweitet werden (pr -> auto, unbekannt -> auto). Auto -> pr ist
// zulässig (restriktiver). Fehlercodes: platform_limit, invalid_request.
func (r *Rules) IntegrationModeAllowed(ordered, requested domain.IntegrationMode) error {
	if r == nil {
		return errNoRules()
	}
	if !slices.Contains(domain.IntegrationModes(), requested) {
		return newErr(CodeInvalidRequest, "", "", "Unbekannter Integrationsmodus %q", requested)
	}
	if requested == domain.ModeAuto && ordered != domain.ModeAuto {
		return newErr(CodePlatformLimit, "", "", "Modus %s ist nicht beauftragt (Auftrag: %q); Auto gilt nur ausdrücklich je Auftrag und wird nie aufgeweitet", requested, ordered)
	}
	return nil
}

// ---- Abschluss -------------------------------------------------------------------

// IntegrationState ist der Integrationsstand eines Features.
type IntegrationState string

const (
	IntegrationNone   IntegrationState = ""        // noch kein PR
	IntegrationPROpen IntegrationState = "pr_open" // PR bereitgestellt, Merge steht aus
	IntegrationMerged IntegrationState = "merged"  // Merge bestätigt
)

// MergeActor nennt, wer gemergt hat.
type MergeActor string

const (
	MergedByHuman MergeActor = "human"       // Nutzer, in CCMAS oder außerhalb (abgeglichen)
	MergedByQueue MergeActor = "merge_queue" // kontrollierte Merge Queue der Plattform
)

// CompletionEvidence sind die Nachweise für den Abschluss eines Features oder Meilensteins. Jedes Feld
// ist ein Beleg, kein Wunsch: false heißt nicht belegt.
type CompletionEvidence struct {
	Mode        domain.IntegrationMode
	Integration IntegrationState
	MergedBy    MergeActor

	AcceptanceCriteriaMet bool
	ReviewEvidence        bool
	TestEvidence          bool
	CIEvidence            bool
	TaskStateUpdated      bool
	KnowledgeUpdated      bool

	// Nur Meilenstein.
	ChildrenComplete bool
	OwnerAcceptance  bool
}

// Completion ist das Ergebnis der Abschlussprüfung.
type Completion struct {
	Complete bool
	State    domain.WorkOrderState // completed, waiting_integration (offener PR) oder in_progress
	Missing  []string              // fehlende Nachweise (Kennungen aus der Regeldatei), in Regelreihenfolge
}

// FeatureComplete wertet die Abschlussregeln für ein Feature aus. Abgeschlossen ist es nur mit bestätigtem
// Merge, Review-, Test- und CI-Nachweisen sowie nachgezogenem Aufgaben- und Wissensstand. Ein offener PR
// ist der Wartezustand waiting_integration, nie Abschluss. Im PR-Modus zählt nur ein menschlicher Merge;
// ein unbekannter Modus gilt als PR-Modus.
func (r *Rules) FeatureComplete(ev CompletionEvidence) Completion {
	if r == nil {
		return noRulesCompletion()
	}
	return r.complete(ev, r.doc.Completion.Requires)
}

// MilestoneComplete wertet zusätzlich die Teilziele und die Abnahme durch den Projektinhaber aus.
func (r *Rules) MilestoneComplete(ev CompletionEvidence) Completion {
	if r == nil {
		return noRulesCompletion()
	}
	req := append(slices.Clone(r.doc.Completion.Requires), r.doc.Completion.MilestoneAlsoRequires...)
	return r.complete(ev, req)
}

// noRulesCompletion ist die Antwort ohne geladene Regeln: kein Abschluss, nichts belegt, Kennung "rules".
func noRulesCompletion() Completion {
	return Completion{State: domain.WOInProgress, Missing: []string{"rules"}}
}

func (r *Rules) complete(ev CompletionEvidence, required []string) Completion {
	merged := ev.Integration == IntegrationMerged &&
		(ev.MergedBy == MergedByHuman || (ev.MergedBy == MergedByQueue && ev.Mode == domain.ModeAuto))
	have := map[string]bool{
		"acceptance_criteria_met": ev.AcceptanceCriteriaMet,
		"merge_confirmed":         merged,
		"review_evidence":         ev.ReviewEvidence,
		"test_evidence":           ev.TestEvidence,
		"ci_evidence":             ev.CIEvidence,
		"task_state_updated":      ev.TaskStateUpdated,
		"knowledge_updated":       ev.KnowledgeUpdated,
		"children_complete":       ev.ChildrenComplete,
		"owner_acceptance":        ev.OwnerAcceptance,
	}
	missing := []string{}
	for _, id := range required {
		if !have[id] {
			missing = append(missing, id)
		}
	}
	c := Completion{Missing: missing}
	switch {
	case len(missing) == 0:
		c.Complete, c.State = true, domain.WOCompleted
	case ev.Integration == IntegrationPROpen:
		c.State = domain.WOWaitingIntegration
	default:
		c.State = domain.WOInProgress
	}
	return c
}
