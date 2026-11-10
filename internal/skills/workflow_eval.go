package skills

import (
	"slices"

	"github.com/alexwenzel/ccmas/internal/domain"
)

// Dieser Teil enthält die kleinen Auswerter der Plattform auf den geprüften Rollenregeln. Sie
// entscheiden Zulässigkeit, nicht Fachplanung: Welche Arbeit als Nächstes sinnvoll ist, beurteilt der
// Supervisor; diese Funktionen sagen nur, was die Regeln erlauben oder verlangen. Alle sind
// fail-safe: Unbekanntes, Fehlendes und Widersprüchliches führt zur strengeren Seite.

// ---- Review vor dem Push ---------------------------------------------------------

// PushAllowed entscheidet, ob ein Stand gepusht und integriert werden darf (Aktion request_integration).
// Verlangt wird ein Review-Bericht mit Urteil FREIGEGEBEN, der zu genau diesem Auftrag, Bericht, Commit
// und dieser Konzeptversion gehört (domain.IntegrationRequest.CheckReview), und der von einer anderen
// Ausführung stammt als die Implementierung. Fehlt eine Kennung, gilt die Unabhängigkeit als nicht
// nachgewiesen. Jede weitere Änderung am Stand (anderer Commit, andere Konzeptversion) macht den Bericht
// unbrauchbar; ein neues Review ist nötig.
//
// Fehlercodes: review_missing, review_invalid, review_not_independent.
func (r *Rules) PushAllowed(req domain.IntegrationRequest, report *domain.ReviewReport, implementerExecutionID string) error {
	if report == nil {
		return newErr(CodeReviewMissing, "", "", "Kein Review-Bericht: ohne unabhängiges Review mit Urteil %s wird nicht gepusht", domain.VerdictApproved)
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

// MaxRepairRounds liefert das Budget an Reparaturrunden (Standard 3).
func (r *Rules) MaxRepairRounds() int { return r.doc.RepairLoop.MaxRounds }

// RepairStep entscheidet für die n-te Reparaturrunde (1-basiert, die erste Runde folgt dem ersten Review
// mit BEFUNDE): innerhalb des Budgets Implementierung mit den Befunden und danach frisches Review, darüber
// hinaus ein sichtbarer Halt mit Frage an den Nutzer.
func (r *Rules) RepairStep(round int) (RepairDecision, error) {
	if round < 1 {
		return RepairDecision{}, newErr(CodeInvalidRequest, "", "", "Reparaturrunde %d ungültig (zählt ab 1)", round)
	}
	rl := r.doc.RepairLoop
	if round > rl.MaxRounds {
		return RepairDecision{Round: round, Halt: true, Step: Step{Action: rl.OnExhausted.Action}}, nil
	}
	return RepairDecision{Round: round, Step: Step{Action: rl.OnFindings.Action, Role: rl.OnFindings.Role}}, nil
}

// ---- Rollenübergänge -------------------------------------------------------------

// NextSteps liefert die zulässigen Folgeschritte für das Ergebnis einer Rolle; der erste ist der
// Standardschritt. Unbekannte Rolle oder unbekanntes Ergebnis ist ein Fehler (invalid_request).
func (r *Rules) NextSteps(role domain.Role, result string) ([]Step, error) {
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
	return r.complete(ev, r.doc.Completion.Requires)
}

// MilestoneComplete wertet zusätzlich die Teilziele und die Abnahme durch den Projektinhaber aus.
func (r *Rules) MilestoneComplete(ev CompletionEvidence) Completion {
	req := append(slices.Clone(r.doc.Completion.Requires), r.doc.Completion.MilestoneAlsoRequires...)
	return r.complete(ev, req)
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
