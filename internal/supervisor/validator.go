package supervisor

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/alexwenzel/ccmas/internal/domain"
	"github.com/alexwenzel/ccmas/internal/profiles"
	"github.com/alexwenzel/ccmas/internal/skills"
)

// Validator prüft ein Planungsergebnis des Supervisors auf Zulässigkeit. Er enthält nur Zulässigkeitsregeln:
// Er wählt keine Aufgaben aus, priorisiert nichts und ergänzt nichts. Der Validator ist zustandslos; alles, was er
// braucht, steht im Batch, im Snapshot und in der Policy. Er verändert keine seiner Eingaben und führt nichts aus.
type Validator struct{}

// Delegation ist die Beauftragung einer Ausführung durch eine Entscheidung (concept_work, implementation, review,
// documentation_work) mit dem wirksamen Profil. Das Profil ist mit der Wahl des Supervisors aufgelöst
// (profiles.Overrides.Supervisor); ein individueller Agentenwert hat Vorrang. Reduktionen eines Efforts und die
// Herkunft jedes Werts stehen im Profil (Fallback, ValueSources).
type Delegation struct {
	OrderID string
	Profile domain.AgentProfile
}

// ValidatedDecision ist eine zulässige Entscheidung; Delegation ist nur bei beauftragenden Aktionen gesetzt.
type ValidatedDecision struct {
	Decision   domain.Decision
	Delegation *Delegation
	// NewOrderModes nennt bei einer Plan-Ergänzung je neuer Ref den wirksamen Modus.
	NewOrderModes map[string]domain.IntegrationMode
}

// ValidatedBatch ist ein vollständig zulässiger Batch. Er ist die einzige Form, in der der Orchestrator eine Planung
// ausführen darf; er entsteht nur, wenn keine einzige Entscheidung abgelehnt wurde.
type ValidatedBatch struct {
	BatchID       string
	StateRevision int64
	Goal          domain.DecisionTarget
	Decisions     []ValidatedDecision
}

// Delegations liefert die Beauftragungen in der Reihenfolge des Batches.
func (v ValidatedBatch) Delegations() []Delegation {
	var out []Delegation
	for _, d := range v.Decisions {
		if d.Delegation != nil {
			out = append(out, *d.Delegation)
		}
	}
	return out
}

// Validate prüft den Batch gegen Snapshot und Policy und liefert nil, wenn jede Entscheidung zulässig ist. Sonst ist
// der Fehler ein *RejectionError mit ALLEN Ablehnungen. Wer die wirksamen Profile braucht, ruft Accept.
func (v Validator) Validate(batch domain.DecisionBatch, snap ProjectSnapshot, pol Policy) error {
	_, err := v.Accept(batch, snap, pol)
	return err
}

// AcceptJSON liest einen Batch strikt (Schema plan-v1 und decision-v1) und prüft ihn wie Accept. Ein Schemafehler ist
// eine Ablehnung mit dem Code schema.
func (v Validator) AcceptJSON(raw []byte, snap ProjectSnapshot, pol Policy) (ValidatedBatch, error) {
	b, err := domain.ParseDecisionBatch(raw)
	if err != nil {
		rc := &collector{}
		rc.add(-1, "", CodeSchema, "%s", contractReason(err))
		return ValidatedBatch{}, rc.err()
	}
	return v.Accept(b, snap, pol)
}

// Accept prüft den Batch und liefert bei Zulässigkeit den validierten Batch mit dem wirksamen Profil je Delegation.
// Der Batch ist atomar: Bei einer einzigen Ablehnung ist das Ergebnis leer, der Fehler listet alle Ablehnungen
// (*RejectionError), und nichts wird ausgeführt.
//
// Geprüft wird in dieser Reihenfolge: Policy und Snapshot (ohne sie wird alles abgelehnt), Schema, dann alle
// inhaltlichen Regeln, die unabhängig voneinander gesammelt werden (docs/architecture/supervisor.md).
func (Validator) Accept(batch domain.DecisionBatch, snap ProjectSnapshot, pol Policy) (ValidatedBatch, error) {
	rc := &collector{batchID: batch.BatchID}
	if err := pol.Check(); err != nil {
		rc.add(-1, "", CodePolicyIncomplete, "%v: ohne vollständige Policy entscheidet die Plattform nichts", err)
		return ValidatedBatch{}, rc.err()
	}
	if problems := snap.Problems(); len(problems) > 0 {
		for _, p := range problems {
			rc.add(-1, "", CodeInvalidSnapshot, "Snapshot %s", p)
		}
		return ValidatedBatch{}, rc.err()
	}
	b := normalize(batch)
	if err := schemaCheck(b); err != nil {
		idx, id := schemaIndex(b, err), ""
		if idx >= 0 {
			id = b.Decisions[idx].DecisionID
		}
		rc.add(idx, id, CodeSchema, "%s", contractReason(err))
		return ValidatedBatch{}, rc.err()
	}
	r := &run{b: b, snap: snap, pol: pol, rc: rc}
	r.prepare()
	r.run()
	if err := rc.err(); err != nil {
		return ValidatedBatch{}, err
	}
	return r.result(), nil
}

// ---- Schema ------------------------------------------------------------------------------------

// normalize liefert eine Kopie, in der fehlende Listen leer statt nil sind (nil würde als null serialisiert und das
// Schema verletzen). Die Eingabe bleibt unverändert.
func normalize(in domain.DecisionBatch) domain.DecisionBatch {
	out := in
	out.Decisions = make([]domain.Decision, len(in.Decisions))
	for i, d := range in.Decisions {
		d.Preconditions = nonNil(slices.Clone(d.Preconditions))
		if d.Selection != nil {
			s := *d.Selection
			s.SkillIDs = nonNil(slices.Clone(s.SkillIDs))
			d.Selection = &s
		}
		if d.PlanExtension != nil {
			pe := domain.PlanExtension{WorkOrders: make([]domain.PlannedWorkOrder, len(d.PlanExtension.WorkOrders))}
			for j, w := range d.PlanExtension.WorkOrders {
				w.AcceptanceCriteria = nonNil(slices.Clone(w.AcceptanceCriteria))
				w.DependsOn = nonNil(slices.Clone(w.DependsOn))
				pe.WorkOrders[j] = w
			}
			d.PlanExtension = &pe
		}
		out.Decisions[i] = d
	}
	return out
}

func schemaCheck(b domain.DecisionBatch) error {
	raw, err := json.Marshal(b)
	if err != nil {
		return err
	}
	return domain.Validate(domain.TargetDecisionBatch, raw)
}

var decisionLocation = regexp.MustCompile(`^/decisions/(\d+)(/|$)`)

func schemaIndex(b domain.DecisionBatch, err error) int {
	var ce *domain.ContractError
	if errors.As(err, &ce) {
		if m := decisionLocation.FindStringSubmatch(ce.Location); m != nil {
			if i, _ := strconv.Atoi(m[1]); i < len(b.Decisions) {
				return i
			}
		}
	}
	return -1
}

// contractReason formuliert einen Vertragsfehler als Begründung mit Fundstelle.
func contractReason(err error) string {
	var ce *domain.ContractError
	if errors.As(err, &ce) {
		loc := ce.Location
		if loc == "" {
			loc = "/"
		}
		cats := make([]string, len(ce.Categories))
		for i, c := range ce.Categories {
			cats[i] = string(c)
		}
		return fmt.Sprintf("%s (Fundstelle %s, Klasse %s)", ce.Message, loc, strings.Join(cats, ","))
	}
	return err.Error()
}

// ---- Lauf ---------------------------------------------------------------------------------------

// run hält den Zustand einer Prüfung. Alle Regeln sind unabhängig voneinander: Sie melden ihre Ablehnungen und
// brechen die Prüfung der übrigen nicht ab.
type run struct {
	b    domain.DecisionBatch
	snap ProjectSnapshot
	pol  Policy
	rc   *collector

	orders     map[string]*Order
	scope      map[string]bool
	outOfScope map[string]bool
	newRefs    map[string]int      // neue Ref -> Index der ersten Entscheidung, die sie anlegt
	activeBy   map[string][]string // Auftrag -> aktive Ausführungen (ohne Supervisor)
	openQ      map[string][]string // Auftrag -> noch nicht zugestellte Fragen
	activeN    int                 // aktive Worker-Ausführungen (ohne Supervisor)

	profiles map[int]domain.AgentProfile
}

func (r *run) prepare() {
	s := &r.snap
	r.orders = map[string]*Order{}
	for i := range s.Orders {
		r.orders[s.Orders[i].ID] = &s.Orders[i]
	}
	r.scope, r.outOfScope = map[string]bool{}, map[string]bool{}
	for _, id := range s.Goal.Scope {
		r.scope[id] = true
	}
	for _, id := range s.Goal.OutOfScope {
		r.outOfScope[id] = true
	}
	r.activeBy = map[string][]string{}
	for _, a := range s.Capacity.Active {
		if a.Role == domain.RoleSupervisor {
			continue // der Supervisor belegt keinen Worker-Platz
		}
		r.activeN++
		if a.WorkOrderID != "" {
			r.activeBy[a.WorkOrderID] = append(r.activeBy[a.WorkOrderID], a.ExecutionID)
		}
	}
	for _, o := range s.Orders {
		if o.ActiveExecutionID != "" && !slices.Contains(r.activeBy[o.ID], o.ActiveExecutionID) {
			r.activeBy[o.ID] = append(r.activeBy[o.ID], o.ActiveExecutionID)
		}
	}
	r.openQ = map[string][]string{}
	for _, q := range s.OpenQuestions {
		if q.WorkOrderID != "" && (q.State == domain.QuestionOpen || q.State == domain.QuestionAnswered) {
			r.openQ[q.WorkOrderID] = append(r.openQ[q.WorkOrderID], q.QuestionID)
		}
	}
	r.newRefs = map[string]int{}
	for i, d := range r.b.Decisions {
		if d.PlanExtension == nil {
			continue
		}
		for _, w := range d.PlanExtension.WorkOrders {
			if _, dup := r.newRefs[w.Ref]; !dup {
				r.newRefs[w.Ref] = i
			}
		}
	}
	r.profiles = map[int]domain.AgentProfile{}
}

func (r *run) reject(i int, code Code, format string, args ...any) {
	id := ""
	if i >= 0 {
		id = r.b.Decisions[i].DecisionID
	}
	r.rc.add(i, id, code, format, args...)
}

func (r *run) run() {
	r.batchChecks()
	for i := range r.b.Decisions {
		r.decisionChecks(i)
	}
	r.planChecks()
	r.cycleChecks()
	r.startChecks()
}

func (r *run) result() ValidatedBatch {
	out := ValidatedBatch{BatchID: r.b.BatchID, StateRevision: r.b.StateRevision, Goal: r.b.Goal, Decisions: make([]ValidatedDecision, len(r.b.Decisions))}
	for i, d := range r.b.Decisions {
		vd := ValidatedDecision{Decision: d}
		if p, ok := r.profiles[i]; ok {
			vd.Delegation = &Delegation{OrderID: d.Target.ID, Profile: p}
		}
		if d.PlanExtension != nil {
			// Ohne eigenen Modus erbt der neue Auftrag den beauftragten Modus des Ziels; er wird nie aufgeweitet.
			vd.NewOrderModes = make(map[string]domain.IntegrationMode, len(d.PlanExtension.WorkOrders))
			for _, w := range d.PlanExtension.WorkOrders {
				mode := w.Mode
				if mode == "" {
					mode = r.snap.IntegrationMode
				}
				vd.NewOrderModes[w.Ref] = mode
			}
		}
		out.Decisions[i] = vd
	}
	return out
}

// ---- Batch -----------------------------------------------------------------------------------------

func (r *run) batchChecks() {
	b, s := r.b, r.snap
	if b.ProjectID != s.ProjectID {
		r.reject(-1, CodeForeignProject, "Der Batch gehört zu Projekt %q, dieser Snapshot zu Projekt %q. Es wird nur für das eigene Projekt geplant.", b.ProjectID, s.ProjectID)
	}
	if b.StateRevision != s.StateRevision {
		r.reject(-1, CodeStaleRevision, "Der Batch beruht auf Zustandsrevision %d, aktuell ist %d. Der Zustand hat sich geändert: auf dem aktuellen Snapshot neu planen.", b.StateRevision, s.StateRevision)
	}
	g := b.Goal
	switch {
	case g.ID == s.Goal.ID && g.Kind == s.Goal.Kind:
	case g.ID == s.Goal.ID:
		r.reject(-1, CodeInvalidTarget, "Zielbezug %s/%s: das beauftragte Ziel ist %s/%s.", g.Kind, g.ID, s.Goal.Kind, s.Goal.ID)
	case r.scope[g.ID]:
	case r.outOfScope[g.ID]:
		r.reject(-1, CodeScopeExtension, "Zielbezug %s %q liegt außerhalb des beauftragten Umfangs (Ziel: %s %s). Nicht beauftragte Tickets werden nicht automatisch übernommen.", g.Kind, g.ID, s.Goal.Kind, s.Goal.ID)
	default:
		r.reject(-1, CodeUnknownReference, "Zielbezug %s %q ist im Snapshot nicht bekannt (beauftragtes Ziel: %s %s).", g.Kind, g.ID, s.Goal.Kind, s.Goal.ID)
	}
	seen := map[string]int{}
	for i, d := range b.Decisions {
		if first, dup := seen[d.DecisionID]; dup {
			r.reject(i, CodeDuplicateDecisionID, "Die Entscheidungs-ID %q kommt im Batch mehrfach vor (Positionen %d und %d); jede Entscheidung braucht eine eigene ID.", d.DecisionID, first+1, i+1)
			continue
		}
		seen[d.DecisionID] = i
	}
}

// ---- Entscheidung ----------------------------------------------------------------------------------

// orderActions sind die Aktionen, die einen Auftrag als Ziel verlangen.
var orderActions = map[domain.Action]bool{
	domain.ActionConceptWork: true, domain.ActionImplementation: true, domain.ActionReview: true,
	domain.ActionDocumentationWork: true, domain.ActionRequestIntegration: true,
}

func isDelegation(a domain.Action) bool { return a.RequiresSelection() }

func (r *run) decisionChecks(i int) {
	d := r.b.Decisions[i]
	s := r.snap

	if d.ProjectID != s.ProjectID {
		r.reject(i, CodeForeignProject, "Die Entscheidung gehört zu Projekt %q, dieser Snapshot zu Projekt %q.", d.ProjectID, s.ProjectID)
	}
	if d.StateRevision != s.StateRevision {
		r.reject(i, CodeStaleRevision, "Die Entscheidung beruht auf Zustandsrevision %d, aktuell ist %d. Der Zustand hat sich geändert: auf dem aktuellen Snapshot neu planen.", d.StateRevision, s.StateRevision)
	}

	// Rolle, Modell, Effort (mit genehmigtem Fallback) und Skills gegen den Katalog: Decision.Check. Projekt und Revision
	// sind oben mit eigener Begründung geprüft und hier neutralisiert, damit ihre Verstöße die übrigen nicht verdecken.
	neutral := d
	neutral.ProjectID, neutral.StateRevision = s.ProjectID, s.StateRevision
	ctx := domain.DecisionContext{ProjectID: s.ProjectID, StateRevision: s.StateRevision, Catalog: r.pol.Catalog}
	_, selErr := neutral.CheckEffective(ctx)
	if selErr != nil {
		r.reject(i, CodeProfileNotAllowed, "Rolle, Modell, Effort oder Skills der Auswahl sind unzulässig: %s", contractReason(selErr))
	}

	if isDelegation(d.Action) {
		r.delegationComplete(i, d)
	}

	o, ok := r.target(i, d)
	if ok {
		switch d.Action {
		case domain.ActionConceptWork:
			r.conceptWork(i, d, o)
		case domain.ActionImplementation:
			r.implementation(i, d, o)
		case domain.ActionReview:
			r.review(i, d, o)
		case domain.ActionDocumentationWork:
			r.documentation(i, d, o)
		case domain.ActionRequestIntegration:
			r.integration(i, d, o)
		case domain.ActionRequestCompletion:
			r.completion(i, d)
		}
		r.declared(i, d, o)
	}
	r.coverage(i, d, o)

	// Wirksames Profil: nur für beauftragende Aktionen, deren Auswahl der Katalog angenommen hat.
	if isDelegation(d.Action) && d.Selection != nil && selErr == nil {
		r.resolveProfile(i, d)
	}
}

// delegationComplete verlangt bei jeder Beauftragung das erwartete Ergebnis und die Aufwandsschätzung. Rolle, Skills, Modell
// und Effort (Auswahl) und die Begründung verlangt das Schema; diese beiden Angaben sind dort aus Gründen der
// Abwärtskompatibilität von v1 optional, im Betrieb aber Pflicht: Jede Delegation nennt Rolle, Skills, Modell, Effort,
// erwartetes Ergebnis und Begründung (Spezifikation „Intelligente Arbeitsverteilung“), und der Aufwand ist eine eigene
// Angabe, getrennt vom Effort. Vollständigkeit der Schätzung (Größe, Komplexität, Unsicherheit) erzwingt das Schema.
func (r *run) delegationComplete(i int, d domain.Decision) {
	var missing []string
	if strings.TrimSpace(d.ExpectedOutcome) == "" {
		missing = append(missing, "das erwartete Ergebnis fehlt (expected_outcome: ein kurzer, überprüfbarer Satz)")
	}
	if d.Estimate == nil {
		missing = append(missing, "die Aufwandsschätzung fehlt (estimate: Größe, Komplexität, Unsicherheit; getrennt vom Effort)")
	}
	if len(missing) > 0 {
		r.reject(i, CodeDelegationIncomplete, "Die Beauftragung %s ist unvollständig: %s. Jede Delegation nennt Rolle, Skills, Modell, Effort, erwartetes Ergebnis, Aufwand und Begründung.", d.Action, strings.Join(missing, "; "))
	}
}

// target ordnet das Ziel der Entscheidung dem Snapshot zu und meldet unbekannte, falsche und nicht beauftragte Ziele.
// Es liefert den Auftrag (nil bei Zielen, die kein Auftrag sind) und ob das Ziel gültig ist.
func (r *run) target(i int, d domain.Decision) (*Order, bool) {
	t, g := d.Target, r.snap.Goal
	if d.Action == domain.ActionRequestCompletion {
		if t.ID == g.ID && t.Kind == g.Kind {
			return nil, true
		}
		r.reject(i, CodeInvalidTarget, "Der Zielabschluss gilt nur für das beauftragte Ziel %s/%s (die Entscheidung nennt %s/%s).", g.Kind, g.ID, t.Kind, t.ID)
		return nil, false
	}
	if orderActions[d.Action] && t.Kind != domain.KindWorkOrder {
		r.reject(i, CodeInvalidTarget, "Die Aktion %s verlangt ein Ziel der Art work_order, die Entscheidung nennt %s/%s.", d.Action, t.Kind, t.ID)
		return nil, false
	}
	if t.Kind == domain.KindWorkOrder {
		if o := r.orders[t.ID]; o != nil {
			return o, true
		}
		if at, isNew := r.newRefs[t.ID]; isNew {
			r.reject(i, CodeUnknownReference, "Der Auftrag %q wird erst durch die Plan-Ergänzung der Entscheidung %s angelegt und ist erst im nächsten Planungszyklus ansprechbar.", t.ID, r.b.Decisions[at].DecisionID)
		} else {
			r.reject(i, CodeUnknownReference, "Der Auftrag %q ist im Snapshot nicht bekannt.", t.ID)
		}
		return nil, false
	}
	switch {
	case t.ID == g.ID && t.Kind == g.Kind, r.scope[t.ID]:
		return nil, true
	case t.ID == g.ID:
		r.reject(i, CodeInvalidTarget, "Ziel %s/%s: %q ist das beauftragte Ziel der Art %s.", t.Kind, t.ID, t.ID, g.Kind)
	case r.outOfScope[t.ID]:
		r.reject(i, CodeScopeExtension, "Das Ziel %s %q liegt außerhalb des beauftragten Umfangs (Ziel: %s %s). Nicht beauftragte Tickets werden nicht automatisch übernommen; die Erweiterung des Umfangs braucht einen eigenen Auftrag des Nutzers.", t.Kind, t.ID, g.Kind, g.ID)
	default:
		r.reject(i, CodeUnknownReference, "Das Ziel %s %q ist im Snapshot nicht bekannt.", t.Kind, t.ID)
	}
	return nil, false
}

// ---- Aufträge: Art, Zustand, Fragen --------------------------------------------------------------

// startableStates sind die Zustände, aus denen Arbeit beauftragt werden darf.
var startableStates = []domain.WorkOrderState{domain.WOWaiting, domain.WOInProgress, domain.WOValidation, domain.WOReview, domain.WOWaitingIntegration}

func (r *run) wantKind(i int, o *Order, action domain.Action, kinds ...OrderKind) bool {
	if slices.Contains(kinds, o.Kind) {
		return true
	}
	names := make([]string, len(kinds))
	for k, v := range kinds {
		names[k] = string(v)
	}
	r.reject(i, CodeInvalidTarget, "Der Auftrag %s ist vom Typ %s; die Aktion %s ist nur für Aufträge vom Typ %s zulässig.", o.ID, o.Kind, action, strings.Join(names, " oder "))
	return false
}

// startable prüft Zustand und offene Fragen eines Auftrags, dem Arbeit beauftragt werden soll.
func (r *run) startable(i int, o *Order, action domain.Action, states []domain.WorkOrderState) {
	if !slices.Contains(states, o.State) {
		r.reject(i, CodePreconditionUnmet, "Der Auftrag %s ist %s und kann für %s nicht gestartet werden. Blockierte, abgebrochene und abgeschlossene Aufträge werden nicht gestartet.", o.ID, o.State, action)
	}
	if q := r.openQ[o.ID]; len(q) > 0 {
		r.reject(i, CodePreconditionUnmet, "Zum Auftrag %s ist die Frage %s offen oder noch nicht zugestellt; keine Antwort ist keine Freigabe. Unabhängige Arbeit darf weiterlaufen.", o.ID, strings.Join(q, ", "))
	}
}

func (r *run) conceptWork(i int, d domain.Decision, o *Order) {
	if !r.wantKind(i, o, d.Action, OrderConcept) {
		return
	}
	r.startable(i, o, d.Action, startableStates)
	if r.pendingFindings(o) && o.LastReview.ReviewerRole == domain.RoleConceptReviewer {
		r.repairBudget(i, o, o.ConceptRepairRounds+1, "Konzeptschleife", r.pol.Rules.ConceptRepairStep)
	}
}

func (r *run) implementation(i int, d domain.Decision, o *Order) {
	if !r.wantKind(i, o, d.Action, OrderImplementation, OrderBug) {
		return
	}
	r.startable(i, o, d.Action, startableStates)
	for _, dep := range r.openDependencies(o) {
		r.reject(i, CodePreconditionUnmet, "Der Auftrag %s hat eine offene Abhängigkeit: %s. Ein Nachfolger wird bis zur erfüllten Voraussetzung zurückgehalten (wait); unabhängige Arbeit darf vorgezogen werden.", o.ID, dep)
	}
	if msg := r.conceptProblem(o); msg != "" {
		r.reject(i, CodePreconditionUnmet, "%s", msg)
	}
	if r.pendingFindings(o) && o.LastReview.ReviewerRole != domain.RoleConceptReviewer {
		r.repairBudget(i, o, o.CodeRepairRounds+1, "Code-Schleife", r.pol.Rules.RepairStep)
	}
}

func (r *run) documentation(i int, d domain.Decision, o *Order) {
	r.startable(i, o, d.Action, append(slices.Clone(startableStates), domain.WOCompleted))
}

func (r *run) review(i int, d domain.Decision, o *Order) {
	if d.ReviewTarget != nil && d.ReviewTarget.WorkOrderID != o.ID {
		r.reject(i, CodeInvalidTarget, "Das Prüfziel nennt den Auftrag %s, die Entscheidung bezieht sich auf %s.", d.ReviewTarget.WorkOrderID, o.ID)
	}
	rt := d.ReviewTarget
	if rt == nil {
		return
	}
	role := domain.Role("")
	if d.Selection != nil {
		role = d.Selection.Role
	}
	if role == domain.RoleConceptReviewer {
		if !r.wantKind(i, o, d.Action, OrderConcept) {
			return
		}
		r.startable(i, o, d.Action, startableStates)
		c := o.Concept
		switch {
		case c == nil:
			r.reject(i, CodePreconditionUnmet, "Der Auftrag %s hat keinen Konzeptstand, der geprüft werden könnte.", o.ID)
		case rt.ConceptVersion != c.Version:
			r.reject(i, CodePreconditionUnmet, "Das Prüfziel nennt die Konzeptversion %q, aktuell ist %q; eine Prüfung gilt nur für eine bestimmte Version.", rt.ConceptVersion, c.Version)
		default:
			if route := r.pol.Rules.ConceptApprover(c.assessment()); route.Approver != skills.ApproverConceptReviewer {
				r.reject(i, CodePreconditionUnmet, "Das Konzept %s von Auftrag %s ist strategisch, nicht einstufbar oder widersprüchlich eingestuft: es entscheidet der Nutzer, zulässig ist nur %s.", c.Version, o.ID, route.Action)
			}
		}
		return
	}
	if !r.wantKind(i, o, d.Action, OrderImplementation, OrderBug) {
		return
	}
	r.startable(i, o, d.Action, startableStates)
	if slices.Contains(startableStates, o.State) && o.State != domain.WOValidation && o.State != domain.WOReview {
		r.reject(i, CodePreconditionUnmet, "Die Implementierung von %s ist nicht abgeschlossen (Zustand %s); ein Review ist erst nach der Schreibphase zulässig (Zustand validation oder review).", o.ID, o.State)
	}
	switch {
	case o.HeadCommit == "":
		r.reject(i, CodePreconditionUnmet, "Für den Auftrag %s ist kein Stand der Implementierung bekannt; es gibt nichts zu prüfen.", o.ID)
	case rt.Commit != o.HeadCommit:
		r.reject(i, CodePreconditionUnmet, "Das Prüfziel %s ist nicht der aktuelle Stand %s von %s; ein Review ist an genau einen Commit gebunden.", rt.Commit, o.HeadCommit, o.ID)
	}
	// Das Prüfziel trägt die Konzeptversion des Auftrags, ohne Konzeptbezug die feste Kennung NoConceptVersion (das Schema
	// verlangt dort einen Wert; das Modell erfindet keinen).
	switch want := reviewConceptVersion(r.orders, o); {
	case want == "":
		if rt.ConceptVersion == NoConceptVersion {
			r.reject(i, CodePreconditionUnmet, "Der Auftrag %s hat Konzeptbezug (Abhängigkeit auf den Konzeptauftrag %s) ohne Konzeptstand; die Kennung %q gilt nur für Aufträge ohne Konzeptbezug und ersetzt keinen Konzeptstand.", o.ID, strings.Join(r.conceptDependencies(o), ", "), NoConceptVersion)
		}
	case want == NoConceptVersion && rt.ConceptVersion != want:
		r.reject(i, CodePreconditionUnmet, "Der Auftrag %s hat keinen Konzeptbezug: das Prüfziel trägt dafür die feste Kennung %q, nicht %q.", o.ID, NoConceptVersion, rt.ConceptVersion)
	case rt.ConceptVersion != want:
		r.reject(i, CodePreconditionUnmet, "Das Prüfziel nennt die Konzeptversion %q, aktuell ist %q.", rt.ConceptVersion, want)
	}
	if msg := r.conceptProblem(o); msg != "" {
		r.reject(i, CodePreconditionUnmet, "%s", msg)
	}
}

func (r *run) integration(i int, d domain.Decision, o *Order) {
	if !r.wantKind(i, o, d.Action, OrderImplementation, OrderBug) {
		return
	}
	if err := r.pol.Rules.IntegrationModeAllowed(r.snap.IntegrationMode, o.Mode); err != nil {
		r.reject(i, CodeModeExtension, "Der Auftrag %s ist im Modus %s beauftragt, das Ziel aber im Modus %s: %v Der Supervisor weitet den Modus nie auf.", o.ID, o.Mode, r.snap.IntegrationMode, err)
	}
	if slices.Contains([]domain.WorkOrderState{domain.WOBlocked, domain.WOCancelled, domain.WOCompleted}, o.State) {
		r.reject(i, CodePreconditionUnmet, "Der Auftrag %s ist %s und kann nicht integriert werden.", o.ID, o.State)
	}
	if ids := r.activeBy[o.ID]; len(ids) > 0 {
		r.reject(i, CodePreconditionUnmet, "Der Auftrag %s hat die aktive Ausführung %s; solange sie läuft, wird nicht integriert.", o.ID, strings.Join(ids, ", "))
	}

	// Keine Dublette einer externen Aktion (Spezifikation „Externe Aktionen ohne Dubletten“; Review vor dem Push, Schritte 6 und
	// 7): Ein gemergter PR ist erledigt, und ein offener PR, der schon den freigegebenen Commit trägt, ändert durch eine erneute
	// Integration nichts. Zulässig bleibt die Aktualisierung eines offenen PR nach Reparatur und neuem FREIGEGEBEN für einen
	// anderen Commit. Ist der Kopf-Commit des offenen PR nicht belegt, lässt sich eine neue Wirkung nicht nachweisen: abgelehnt.
	if pr := o.PR; pr != nil {
		approved := ""
		if o.LastReview != nil && o.LastReview.Report.Verdict == domain.VerdictApproved {
			approved = o.LastReview.Report.Commit
		}
		switch {
		case pr.State == PRMerged:
			r.reject(i, CodeDuplicateStart, "Der PR #%d von %s ist bereits gemergt; die Integration ist erfolgt und wird nicht wiederholt.", pr.Number, o.ID)
		case pr.State == PROpen && pr.HeadCommit == "":
			r.reject(i, CodeDuplicateStart, "Der PR #%d von %s ist offen, sein Kopf-Commit ist im Snapshot nicht belegt; ohne den Beleg einer neuen Wirkung wird nicht erneut integriert (wait, bis der Merge bestätigt ist).", pr.Number, o.ID)
		case pr.State == PROpen && approved != "" && pr.HeadCommit == approved:
			r.reject(i, CodeDuplicateStart, "Der offene PR #%d von %s trägt bereits den freigegebenen Commit %s; eine erneute Integration hätte keine neue Wirkung (wait, bis der Merge bestätigt ist).", pr.Number, o.ID, approved)
		}
	}

	// Konzeptbezug (Konzeptstand am Auftrag oder Abhängigkeit auf einen Konzeptauftrag): wie bei implementation und review nur mit
	// gültiger Konzeptfreigabe; ein Konzeptstand ohne gültige Freigabe, ein strategisches Konzept ohne Nutzerentscheid und ein
	// Konzeptbezug nur über die Abhängigkeit werden abgelehnt. Ohne Konzeptbezug gibt es nichts freizugeben.
	if msg := r.conceptProblem(o); msg != "" {
		r.reject(i, CodePreconditionUnmet, "%s", msg)
	}

	// Review vor dem Push: derselbe Auswerter wie im Orchestrator, mit Rolle und Ausführungen aus dem Snapshot. Der Bericht trägt
	// die Konzeptversion des Auftrags, ohne Konzeptbezug die feste Kennung NoConceptVersion; es gibt keine Übernahme der
	// Berichtsversion.
	req := domain.IntegrationRequest{
		RequestID: "validate-" + d.DecisionID, DecisionID: d.DecisionID, WorkOrderID: o.ID, Mode: o.Mode,
		BaseBranch: domain.BaseBranch, HeadCommit: o.HeadCommit, Branch: o.Branch,
		ConceptVersion: reviewConceptVersion(r.orders, o),
	}
	if o.PR != nil {
		req.PRNumber = o.PR.Number
	}
	var report *domain.ReviewReport
	var reviewerRole domain.Role
	var implementerExec string
	if o.LastReview != nil {
		rep := o.LastReview.Report
		report, reviewerRole, implementerExec = &rep, o.LastReview.ReviewerRole, o.LastReview.ImplementerExecutionID
		req.ReviewReportID = rep.ReportID
	}
	if err := r.pol.Rules.PushAllowed(req, report, implementerExec, reviewerRole); err != nil {
		r.reject(i, CodeIntegrationNotApproved, "Die Integration von %s hat keine gültige, unabhängige Review-Freigabe: %v (Regelcode %s). Ohne Review mit Urteil FREIGEGEBEN für genau diesen Commit und diese Konzeptversion (ohne Konzeptbezug: %q) wird nicht gepusht.", o.ID, err, skills.CodeOf(err), NoConceptVersion)
	}
}

func (r *run) completion(i int, d domain.Decision) {
	g := r.snap.Goal
	var ev Evidence
	if g.Evidence != nil {
		ev = *g.Evidence
	}
	cev := ev.toSkills(r.snap.IntegrationMode)
	var c skills.Completion
	if g.Kind == domain.KindMilestone {
		c = r.pol.Rules.MilestoneComplete(cev)
	} else {
		c = r.pol.Rules.FeatureComplete(cev)
	}
	var open []string
	for _, o := range r.snap.Orders {
		if o.State != domain.WOCompleted && o.State != domain.WOCancelled {
			open = append(open, fmt.Sprintf("%s (%s)", o.ID, o.State))
		}
	}
	if c.Complete && len(open) == 0 {
		return
	}
	var parts []string
	if !c.Complete {
		parts = append(parts, "fehlende Nachweise: "+strings.Join(c.Missing, ", "))
	}
	if len(open) > 0 {
		parts = append(parts, "noch nicht abgeschlossene Aufträge: "+strings.Join(open, ", "))
	}
	msg := fmt.Sprintf("Der Abschluss von %s %s ist nicht belegt (%s).", g.Kind, g.ID, strings.Join(parts, "; "))
	if c.State == domain.WOWaitingIntegration {
		msg += " Ein offener PR ist der Wartezustand waiting_integration, kein Abschluss: wait, bis der Merge bestätigt ist."
	} else {
		msg += " Eine leere Warteschlange oder ungemergte fertige PRs reichen nicht."
	}
	r.reject(i, CodePrematureCompletion, "%s", msg)
}

// ---- Abhängigkeiten, Konzept, Reparatur --------------------------------------------------------------

// openDependencies nennt die Abhängigkeiten eines Auftrags, die nicht abgeschlossen sind. Was kein Auftrag ist
// (zum Beispiel ein Ticket des Umfangs), gilt nicht als erfüllt, solange der Snapshot es nicht belegt.
func (r *run) openDependencies(o *Order) []string {
	var out []string
	for _, dep := range o.DependsOn {
		if do := r.orders[dep]; do != nil {
			if do.State != domain.WOCompleted {
				out = append(out, fmt.Sprintf("%s (Zustand %s)", dep, do.State))
			}
			continue
		}
		out = append(out, fmt.Sprintf("%s (kein Auftrag, nicht als erfüllt belegt)", dep))
	}
	return out
}

func (c ConceptState) assessment() skills.ConceptAssessment {
	return skills.ConceptAssessment{Class: c.Class, Criteria: slices.Clone(c.Criteria)}
}

// conceptDependencies nennt die Abhängigkeiten eines Auftrags, die Konzeptaufträge sind.
func (r *run) conceptDependencies(o *Order) []string { return conceptDependencyIDs(r.orders, o) }

// conceptRelated meldet, ob der Snapshot einen Konzeptbezug des Auftrags führt: ein Konzeptstand am Auftrag oder eine
// Abhängigkeit auf einen Konzeptauftrag. Nur dann wird die Konzeptfreigabe verlangt (Spezifikation „Concept-Tickets in
// Asana“: abhängige Implementation-Tickets starten erst nach erfüllter Konzeptfreigabe; Bug und Organisation bleiben nutzbar).
func (r *run) conceptRelated(o *Order) bool {
	return o != nil && (o.Concept != nil || len(r.conceptDependencies(o)) > 0)
}

// conceptProblem prüft die Konzeptfreigabe eines Auftrags und liefert die Begründung, wenn sie nicht gilt. Ohne
// Konzeptbezug (conceptRelated) gibt es nichts freizugeben und keine Beanstandung.
func (r *run) conceptProblem(o *Order) string {
	c := o.Concept
	if c == nil {
		if deps := r.conceptDependencies(o); len(deps) > 0 {
			return fmt.Sprintf("Der Auftrag %s hängt vom Konzeptauftrag %s ab, führt aber keinen Konzeptstand: ohne freigegebenen Konzeptstand wird nicht implementiert.", o.ID, strings.Join(deps, ", "))
		}
		return ""
	}
	route := r.pol.Rules.ConceptApprover(c.assessment())
	hint := fmt.Sprintf("Freigabe durch den unabhängigen Konzeptprüfer (Aktion %s)", route.Action)
	if route.Approver == skills.ApproverUser {
		hint = fmt.Sprintf("es entscheidet der Nutzer, zulässig ist nur %s", route.Action)
	}
	if c.Approval == nil {
		return fmt.Sprintf("Das Konzept %s von Auftrag %s ist nicht freigegeben (Freigabe fehlt); %s.", c.Version, o.ID, hint)
	}
	ap := skills.ConceptApproval{Version: c.Approval.Version, Approver: c.Approval.Approver}
	if err := r.pol.Rules.ConceptApprovalValid(ap, c.assessment(), c.Version); err != nil {
		return fmt.Sprintf("Die Konzeptfreigabe von Auftrag %s gilt nicht: %v (%s).", o.ID, err, hint)
	}
	return ""
}

func (r *run) pendingFindings(o *Order) bool {
	return o.LastReview != nil && o.LastReview.Report.Verdict == domain.VerdictFindings
}

func (r *run) repairBudget(i int, o *Order, round int, loop string, step func(int) (skills.RepairDecision, error)) {
	dec, err := step(round)
	if err != nil {
		r.reject(i, CodeRepairBudgetExhausted, "Die Reparaturrunde %d von %s (%s) lässt sich nicht bewerten: %v", round, o.ID, loop, err)
		return
	}
	if dec.Halt {
		r.reject(i, CodeRepairBudgetExhausted, "Das Reparaturbudget von %s (%s) ist erschöpft: Runde %d überschreitet die höchstens %d Runden. Zulässig ist nur noch die Frage an den Nutzer (%s) oder Warten.", o.ID, loop, round, r.pol.Rules.MaxRepairRounds(), dec.Step.Action)
	}
}

// ---- Deklarierte Voraussetzungen ---------------------------------------------------------------------------

func (r *run) implDone(o *Order) bool {
	return slices.Contains([]domain.WorkOrderState{domain.WOValidation, domain.WOReview, domain.WOWaitingIntegration, domain.WOCompleted}, o.State) && len(r.activeBy[o.ID]) == 0
}

func (r *run) freeSlots() int {
	if n := r.snap.Capacity.MaxWorkers - r.activeN; n > 0 {
		return n
	}
	return 0
}

// declared prüft die Voraussetzungen, die die Entscheidung nennt. Bei Aktionen, die jetzt starten (Beauftragung,
// Integration, Abschluss), müssen sie gelten. Bei wait, ask_question und plan_extend sind sie die Bedingung des
// Wartens beziehungsweise der Ergänzung und werden nur auf gültige Bezüge geprüft.
func (r *run) declared(i int, d domain.Decision, o *Order) {
	mustHold := isDelegation(d.Action) || d.Action == domain.ActionRequestIntegration || d.Action == domain.ActionRequestCompletion
	for _, p := range d.Preconditions {
		switch p.Kind {
		case domain.PrecWorkOrderCompleted:
			subject := o
			self := p.Ref == "" || (o != nil && p.Ref == o.ID)
			if !self {
				if subject = r.orders[p.Ref]; subject == nil {
					r.reject(i, CodeUnknownReference, "Die Voraussetzung %s nennt den Auftrag %q; er ist im Snapshot nicht bekannt.", p.Kind, p.Ref)
					continue
				}
			}
			if !mustHold || (self && d.Action == domain.ActionReview) { // der Review prüft den Zustand selbst
				continue
			}
			switch {
			case subject == nil:
				r.reject(i, CodePreconditionUnmet, "Die Voraussetzung %s braucht einen Auftragsbezug.", p.Kind)
			case self && !r.implDone(subject):
				r.reject(i, CodePreconditionUnmet, "Die Voraussetzung %s gilt nicht: die Implementierung von %s ist nicht abgeschlossen (Zustand %s).", p.Kind, subject.ID, subject.State)
			case !self && subject.State != domain.WOCompleted:
				r.reject(i, CodePreconditionUnmet, "Die Voraussetzung %s gilt nicht: der Auftrag %s ist nicht abgeschlossen (Zustand %s).", p.Kind, subject.ID, subject.State)
			}
		case domain.PrecConceptApproved:
			if !mustHold {
				continue
			}
			switch {
			case o == nil:
				r.reject(i, CodePreconditionUnmet, "Die Voraussetzung %s braucht einen Auftragsbezug.", p.Kind)
			case p.Ref != "" && o.Concept == nil:
				r.reject(i, CodePreconditionUnmet, "Die Voraussetzung %s nennt die Konzeptversion %q, der Auftrag %s hat aber keinen Konzeptstand.", p.Kind, p.Ref, o.ID)
			case p.Ref != "" && p.Ref != o.Concept.Version:
				r.reject(i, CodePreconditionUnmet, "Die Voraussetzung %s nennt die Konzeptversion %q, aktuell ist %q; eine Freigabe gilt nur für eine bestimmte Version.", p.Kind, p.Ref, o.Concept.Version)
			case d.Action != domain.ActionImplementation && d.Action != domain.ActionReview: // dort ist die Prüfung Teil der Aktion
				if msg := r.conceptProblem(o); msg != "" {
					r.reject(i, CodePreconditionUnmet, "Die Voraussetzung %s gilt nicht: %s", p.Kind, msg)
				}
			}
		case domain.PrecReviewApproved:
			if !mustHold || d.Action == domain.ActionRequestIntegration { // dort entscheidet PushAllowed
				continue
			}
			if o == nil || o.LastReview == nil || o.LastReview.Report.Verdict != domain.VerdictApproved || o.HeadCommit == "" ||
				o.LastReview.Report.Commit != o.HeadCommit || (p.Ref != "" && p.Ref != o.LastReview.Report.ReportID) {
				r.reject(i, CodePreconditionUnmet, "Die Voraussetzung %s gilt nicht: es liegt kein Review mit Urteil FREIGEGEBEN für den aktuellen Stand vor.", p.Kind)
			}
		case domain.PrecCIPassed:
			subject := o
			if p.Ref != "" {
				if subject = r.orders[p.Ref]; subject == nil {
					r.reject(i, CodeUnknownReference, "Die Voraussetzung %s nennt den Auftrag %q; er ist im Snapshot nicht bekannt.", p.Kind, p.Ref)
					continue
				}
			}
			if mustHold && (subject == nil || subject.PR == nil || subject.PR.CI != CIPassed) {
				r.reject(i, CodePreconditionUnmet, "Die Voraussetzung %s gilt nicht: die verpflichtenden CI-Prüfungen sind nicht als bestanden belegt (ein fehlender Pflichtcheck zählt nicht als bestanden).", p.Kind)
			}
		case domain.PrecCapacityAvailable:
			// Für beauftragende Aktionen entscheidet die Kapazitätsregel (capacity_exceeded).
			if mustHold && !isDelegation(d.Action) && r.freeSlots() < 1 {
				r.reject(i, CodePreconditionUnmet, "Die Voraussetzung %s gilt nicht: alle %d Worker-Plätze sind belegt.", p.Kind, r.snap.Capacity.MaxWorkers)
			}
		}
	}
}

// ---- Folgeschritt in den Rollenregeln ----------------------------------------------------------------------

// coverage prüft, dass die Entscheidung ein Folgeschritt ist, den die Rollenregeln (NextSteps) vorsehen, und dass sie die
// dort geforderten Voraussetzungen nennt. Gedeckt ist ein Schritt, wenn er unter den Ergebnissen des Supervisors steht
// oder dem zuletzt gemeldeten Ergebnis der Rolle des Auftrags folgt.
func (r *run) coverage(i int, d domain.Decision, o *Order) {
	rules := r.pol.Rules
	role := domain.Role("")
	if d.Selection != nil && isDelegation(d.Action) {
		role = d.Selection.Role
	} else if roles := skills.ActionRoles(d.Action); len(roles) == 1 {
		role = roles[0]
	}

	var candidates []skills.Step
	for _, res := range rules.ResultIDs(domain.RoleSupervisor) {
		if steps, err := rules.NextSteps(domain.RoleSupervisor, res); err == nil {
			candidates = append(candidates, steps...)
		}
	}
	if o != nil && o.LastResult != nil {
		if steps, err := rules.NextSteps(o.LastResult.Role, o.LastResult.Result); err == nil {
			candidates = append(candidates, steps...)
		}
	}
	declared := map[domain.PreconditionKind]bool{}
	for _, p := range d.Preconditions {
		declared[p.Kind] = true
	}

	var best []domain.PreconditionKind
	found := false
	for _, st := range candidates {
		if st.Action != d.Action || st.Role != role {
			continue
		}
		requires := st.Requires
		// Mit Konzeptbezug ist die Konzeptfreigabe die Mindestvoraussetzung jeder Implementierung, auch über einen
		// Schritt der Rollenregeln, der sie nicht nennt (Schritt „ohne Konzeptbezug“).
		if d.Action == domain.ActionImplementation && r.conceptRelated(o) && !slices.Contains(requires, domain.PrecConceptApproved) {
			requires = append(slices.Clone(requires), domain.PrecConceptApproved)
		}
		var missing []domain.PreconditionKind
		for _, need := range requires {
			if !declared[need] {
				missing = append(missing, need)
			}
		}
		if len(missing) == 0 {
			return
		}
		if !found || len(missing) < len(best) {
			best, found = missing, true
		}
	}
	if found {
		names := make([]string, len(best))
		for k, m := range best {
			names[k] = string(m)
		}
		r.reject(i, CodeStepNotCovered, "Der Folgeschritt %s ist nach den Rollenregeln nur mit der Voraussetzung %s zulässig; sie fehlt unter preconditions.", stepName(d.Action, role), strings.Join(names, ", "))
		return
	}
	allowed := map[string]bool{}
	for _, st := range candidates {
		allowed[stepName(st.Action, st.Role)] = true
	}
	list := make([]string, 0, len(allowed))
	for k := range allowed {
		list = append(list, k)
	}
	sort.Strings(list)
	extra := ""
	if o != nil && o.LastResult != nil {
		extra = fmt.Sprintf(" (letztes Ergebnis von %s: %s/%s)", o.ID, o.LastResult.Role, o.LastResult.Result)
	}
	r.reject(i, CodeStepNotCovered, "Der Folgeschritt %s ist durch die Rollenregeln nicht gedeckt%s. Zulässig sind: %s.", stepName(d.Action, role), extra, strings.Join(list, ", "))
}

func stepName(a domain.Action, role domain.Role) string {
	if role == "" {
		return string(a)
	}
	return string(a) + "/" + string(role)
}

// ---- Profil ------------------------------------------------------------------------------------------------

// resolveProfile löst das wirksame Profil der Beauftragung auf: Wahl des Supervisors (Overrides.Supervisor) mit
// dem Vorrang des individuellen Agenten der Rolle, falls der Snapshot einen nennt.
func (r *run) resolveProfile(i int, d domain.Decision) {
	sel := d.Selection
	// Der Snapshot führt je Rolle höchstens ein Profilangebot (ProjectSnapshot.Problems); es gibt keine Auswahl unter
	// mehreren Agenten.
	agent := ""
	for _, offer := range r.snap.Roles {
		if offer.Role == sel.Role {
			agent = offer.AgentID
			break
		}
	}
	ov := profiles.Overrides{Supervisor: profiles.Selection{Model: sel.Model, Effort: sel.Effort, SkillIDs: slices.Clone(sel.SkillIDs)}}
	p, err := r.pol.Resolver.ResolveProfile(sel.Role, agent, ov, r.pol.Caps)
	if err != nil {
		code := string(profiles.CodeOf(err))
		if code == "" {
			code = string(skills.CodeOf(err))
		}
		r.reject(i, CodeProfileNotAllowed, "Das Profil der Rolle %s ist nicht zulässig: %v (Regelcode %s).", sel.Role, err, code)
		return
	}
	r.profiles[i] = p
}

// ---- Plan-Ergänzung ----------------------------------------------------------------------------------------

// planChecks prüft die neuen Aufträge aller plan_extend-Entscheidungen: eindeutige, nicht vergebene Refs,
// Abhängigkeiten auf Bekanntes innerhalb des Umfangs und einen nicht aufgeweiteten Modus.
func (r *run) planChecks() {
	declared := map[string]bool{}
	for i, d := range r.b.Decisions {
		if d.PlanExtension == nil {
			continue
		}
		for _, w := range d.PlanExtension.WorkOrders {
			g := r.snap.Goal
			switch {
			case r.outOfScope[w.Ref]:
				r.reject(i, CodeScopeExtension, "Die neue Ref %q ist ein bekanntes, aber nicht beauftragtes Ticket; es wird nicht stillschweigend übernommen. Plan-Ergänzungen bleiben innerhalb des Ziels %s %s.", w.Ref, g.Kind, g.ID)
			case r.orders[w.Ref] != nil || w.Ref == g.ID || r.scope[w.Ref]:
				r.reject(i, CodeDuplicateReference, "Die neue Ref %q ist bereits vergeben (Auftrag, Ziel oder Umfang); neue Aufträge brauchen eine eigene, noch nicht vergebene Ref.", w.Ref)
			case declared[w.Ref]:
				r.reject(i, CodeDuplicateReference, "Die neue Ref %q wird im Batch mehrfach angelegt; jede Ref darf nur einmal vorkommen.", w.Ref)
			}
			declared[w.Ref] = true
			for _, dep := range w.DependsOn {
				_, isNew := r.newRefs[dep]
				switch {
				case r.orders[dep] != nil, isNew, r.scope[dep], dep == g.ID:
				case r.outOfScope[dep]:
					r.reject(i, CodeScopeExtension, "Die neue Aufgabe %q hängt von %q ab, einem Ticket außerhalb des beauftragten Umfangs. Plan-Ergänzungen bleiben innerhalb des Ziels %s %s.", w.Ref, dep, g.Kind, g.ID)
				default:
					r.reject(i, CodeUnknownReference, "Die neue Aufgabe %q hängt von %q ab; das ist weder ein vorhandener Auftrag noch eine neue Ref dieses Batches.", w.Ref, dep)
				}
			}
			if w.Mode != "" {
				if err := r.pol.Rules.IntegrationModeAllowed(r.snap.IntegrationMode, w.Mode); err != nil {
					r.reject(i, CodeModeExtension, "Die neue Aufgabe %q fordert den Modus %s, das Ziel ist im Modus %s beauftragt: %v Der Supervisor weitet den Modus nie auf.", w.Ref, w.Mode, r.snap.IntegrationMode, err)
				}
			}
		}
	}
}

// cycleChecks findet Zyklen im Abhängigkeitsgraphen aus vorhandenen Abhängigkeiten plus Plan-Ergänzungen. Gemeldet
// werden sie bei den Entscheidungen, die einen Beteiligten anlegen oder beauftragen.
func (r *run) cycleChecks() {
	deps := map[string][]string{}
	for _, o := range r.snap.Orders {
		deps[o.ID] = append(deps[o.ID], o.DependsOn...)
	}
	creator := map[string][]int{} // Beteiligter -> Entscheidungen, die ihn anlegen
	for i, d := range r.b.Decisions {
		if d.PlanExtension == nil {
			continue
		}
		for _, w := range d.PlanExtension.WorkOrders {
			deps[w.Ref] = append(deps[w.Ref], w.DependsOn...)
			creator[w.Ref] = append(creator[w.Ref], i)
		}
	}
	for _, comp := range cycles(deps) {
		path := cyclePath(comp, deps)
		in := map[string]bool{}
		for _, n := range comp {
			in[n] = true
		}
		for i, d := range r.b.Decisions {
			involved := false
			for n := range in {
				if slices.Contains(creator[n], i) {
					involved = true
				}
			}
			if !involved && in[d.Target.ID] && d.Target.Kind == domain.KindWorkOrder && isDelegation(d.Action) {
				involved = true
			}
			if involved {
				r.reject(i, CodeDependencyCycle, "Die Abhängigkeiten enthalten einen Zyklus: %s. Ein Zyklus kann nie erfüllt werden; die Abhängigkeiten sind azyklisch zu planen.", path)
			}
		}
	}
}

// cycles liefert die zyklischen Komponenten (starke Zusammenhangskomponenten mit mehr als einem Knoten oder einer
// Selbstkante), jede sortiert, die Komponenten nach ihrem kleinsten Knoten.
func cycles(deps map[string][]string) [][]string {
	nodes := map[string]bool{}
	for n, ds := range deps {
		nodes[n] = true
		for _, d := range ds {
			nodes[d] = true
		}
	}
	names := make([]string, 0, len(nodes))
	for n := range nodes {
		names = append(names, n)
	}
	sort.Strings(names)

	index := map[string]int{}
	low := map[string]int{}
	on := map[string]bool{}
	var stack []string
	var out [][]string
	next := 0
	var visit func(n string)
	visit = func(n string) {
		index[n], low[n] = next, next
		next++
		stack = append(stack, n)
		on[n] = true
		ds := slices.Clone(deps[n])
		sort.Strings(ds)
		for _, m := range ds {
			if _, seen := index[m]; !seen {
				visit(m)
				low[n] = min(low[n], low[m])
			} else if on[m] {
				low[n] = min(low[n], index[m])
			}
		}
		if low[n] == index[n] {
			var comp []string
			for {
				m := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				on[m] = false
				comp = append(comp, m)
				if m == n {
					break
				}
			}
			if len(comp) > 1 || slices.Contains(deps[n], n) {
				sort.Strings(comp)
				out = append(out, comp)
			}
		}
	}
	for _, n := range names {
		if _, seen := index[n]; !seen {
			visit(n)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i][0] < out[j][0] })
	return out
}

// cyclePath beschreibt einen Zyklus innerhalb der Komponente als Weg „a -> b -> a“.
func cyclePath(comp []string, deps map[string][]string) string {
	in := map[string]bool{}
	for _, n := range comp {
		in[n] = true
	}
	start := comp[0]
	path := []string{start}
	seen := map[string]bool{start: true}
	var walk func(n string) bool
	walk = func(n string) bool {
		ds := slices.Clone(deps[n])
		sort.Strings(ds)
		for _, m := range ds {
			if !in[m] {
				continue
			}
			if m == start {
				path = append(path, m)
				return true
			}
			if seen[m] {
				continue
			}
			seen[m] = true
			path = append(path, m)
			if walk(m) {
				return true
			}
			path = path[:len(path)-1]
		}
		return false
	}
	walk(start)
	return strings.Join(path, " -> ")
}

// ---- Doppelstart und Kapazität ---------------------------------------------------------------------------------

func (r *run) startChecks() {
	// Doppelstart gilt nur für Aktionen, die etwas starten: Beauftragungen und die Integrationsanforderung. wait und
	// ask_question starten nichts und dürfen zum selben Auftrag nebeneinander stehen.
	first := map[string]string{}
	for i, d := range r.b.Decisions {
		if d.Target.Kind != domain.KindWorkOrder || !(isDelegation(d.Action) || d.Action == domain.ActionRequestIntegration) {
			continue
		}
		if prev, dup := first[d.Target.ID]; dup {
			r.reject(i, CodeDuplicateStart, "Für den Auftrag %s liegt bereits die Entscheidung %s in diesem Batch vor; zwei startende Entscheidungen (Beauftragung oder Integration) für denselben Auftrag sind ein Doppelstart.", d.Target.ID, prev)
		} else {
			first[d.Target.ID] = d.DecisionID
		}
		if isDelegation(d.Action) {
			if ids := r.activeBy[d.Target.ID]; len(ids) > 0 && r.orders[d.Target.ID] != nil {
				r.reject(i, CodeDuplicateStart, "Der Auftrag %s hat bereits die aktive Ausführung %s; ein zweiter Start ist ein Doppelstart.", d.Target.ID, strings.Join(ids, ", "))
			}
		}
	}

	// Kapazität: jede grundsätzlich startbare Ausführung (concept_work, implementation, review, documentation_work)
	// belegt einen Platz, Reviewer eingeschlossen. Der Supervisor zählt nicht mit. Eine aus anderem Grund abgelehnte
	// Delegation zählt nicht mit, damit die Rückmeldung an das Modell stimmt (sie würde ohnehin nicht starten). Eine
	// Entscheidung, die nicht mehr hineinpasst, wird einzeln abgelehnt (der Batch ist ohnehin atomar).
	free, n := r.freeSlots(), 0
	for i, d := range r.b.Decisions {
		if !isDelegation(d.Action) || r.rc.rejected(i) {
			continue
		}
		n++
		if n > free {
			r.reject(i, CodeCapacityExceeded, "Kapazität überschritten: max_workers=%d, aktive Ausführungen=%d (der Supervisor zählt nicht mit), es sind höchstens %d neue möglich; diese Entscheidung ist die %d. startbare Ausführung des Batches. Weniger beauftragen oder warten.", r.snap.Capacity.MaxWorkers, r.activeN, free, n)
		}
	}
}
