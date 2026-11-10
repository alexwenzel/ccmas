package supervisor

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
)

// Code ist der stabile, maschinenlesbare Code einer Ablehnung. Die Codes sind Teil der Schnittstelle zum
// Modell (Neuplanung) und zum Orchestrator; sie ändern sich nicht ohne Versionswechsel.
type Code string

const (
	// CodeSchema: Der Batch verletzt das Schema (plan-v1 und decision-v1).
	CodeSchema Code = "schema"
	// CodePolicyIncomplete: Die Policy ist unvollständig (Regeln, Profil-Resolver oder Katalog fehlen); ohne sie
	// entscheidet die Plattform nichts (fail-safe).
	CodePolicyIncomplete Code = "policy_incomplete"
	// CodeInvalidSnapshot: Der Snapshot verletzt seine Invarianten und ist keine Grundlage für Entscheidungen.
	CodeInvalidSnapshot Code = "invalid_snapshot"
	// CodeForeignProject: Batch oder Entscheidung gehört zu einem anderen Projekt.
	CodeForeignProject Code = "foreign_project"
	// CodeStaleRevision: Batch oder Entscheidung beruht nicht auf der aktuellen Zustandsrevision.
	CodeStaleRevision Code = "stale_revision"
	// CodeUnknownReference: Eine Auftrags-, Ziel- oder Referenz-ID ist im Snapshot nicht bekannt.
	CodeUnknownReference Code = "unknown_reference"
	// CodeInvalidTarget: Das Ziel hat die falsche Art für die Aktion oder passt nicht zur Angabe.
	CodeInvalidTarget Code = "invalid_target"
	// CodeDuplicateDecisionID: Eine Entscheidungs-ID kommt im Batch mehrfach vor.
	CodeDuplicateDecisionID Code = "duplicate_decision_id"
	// CodeDuplicateReference: Eine neue Ref ist bereits vergeben (Auftrag, Ziel, Umfang oder andere neue Ref).
	CodeDuplicateReference Code = "duplicate_reference"
	// CodeDependencyCycle: Die Abhängigkeiten (vorhandene plus Plan-Ergänzung) enthalten einen Zyklus.
	CodeDependencyCycle Code = "dependency_cycle"
	// CodePreconditionUnmet: Eine Voraussetzung der Aktion ist nicht erfüllt (offene Abhängigkeit, fehlende
	// Konzeptfreigabe, Zustand des Auftrags, offene Frage, nicht erfüllte deklarierte Voraussetzung).
	CodePreconditionUnmet Code = "precondition_unmet"
	// CodeCapacityExceeded: Die neuen Ausführungen passen nicht in max_workers.
	CodeCapacityExceeded Code = "capacity_exceeded"
	// CodeDuplicateStart: Zwei startende Entscheidungen (Beauftragung oder Integration) für denselben Auftrag, ein Auftrag mit
	// aktiver Ausführung oder eine erneute Integration ohne neue Wirkung (PR gemergt, oder der offene PR trägt bereits den
	// freigegebenen Commit). wait und ask_question starten nichts.
	CodeDuplicateStart Code = "duplicate_start"
	// CodeScopeExtension: Das Ziel liegt außerhalb des beauftragten Umfangs.
	CodeScopeExtension Code = "scope_extension"
	// CodeModeExtension: Der Integrationsmodus würde gegenüber dem beauftragten aufgeweitet.
	CodeModeExtension Code = "mode_extension"
	// CodeIntegrationNotApproved: Die Integration hat keine gültige, unabhängige Review-Freigabe (PushAllowed).
	CodeIntegrationNotApproved Code = "integration_not_approved"
	// CodePrematureCompletion: Der Abschluss ist nicht belegt (FeatureComplete/MilestoneComplete).
	CodePrematureCompletion Code = "premature_completion"
	// CodeRepairBudgetExhausted: Das Reparaturbudget ist erschöpft; zulässig ist nur noch die Frage an den Nutzer.
	CodeRepairBudgetExhausted Code = "repair_budget_exhausted"
	// CodeProfileNotAllowed: Modell, Effort, Fallback, Rollenerlaubnis oder Skills der Auswahl sind unzulässig.
	CodeProfileNotAllowed Code = "profile_not_allowed"
	// CodeStepNotCovered: Die Entscheidung ist als Folgeschritt durch die Rollenregeln nicht gedeckt.
	CodeStepNotCovered Code = "step_not_covered"
	// CodeDelegationIncomplete: Eine Beauftragung (concept_work, implementation, review, documentation_work) nennt kein
	// erwartetes Ergebnis oder keine Aufwandsschätzung. Das Schema lässt beides optional (v1-kompatibel); jede Delegation
	// nennt aber Rolle, Skills, Modell, Effort, erwartetes Ergebnis und Begründung (Spezifikation „Intelligente
	// Arbeitsverteilung“), und der Aufwand ist eine eigene Angabe neben dem Effort.
	CodeDelegationIncomplete Code = "delegation_incomplete"
)

// Codes liefert alle Codes in fester Reihenfolge (die Reihenfolge ordnet auch die Ablehnungen einer Entscheidung).
func Codes() []Code {
	return []Code{
		CodeSchema, CodePolicyIncomplete, CodeInvalidSnapshot, CodeForeignProject, CodeStaleRevision,
		CodeUnknownReference, CodeInvalidTarget, CodeDuplicateDecisionID, CodeDuplicateReference,
		CodeDependencyCycle, CodePreconditionUnmet, CodeCapacityExceeded, CodeDuplicateStart,
		CodeScopeExtension, CodeModeExtension, CodeIntegrationNotApproved, CodePrematureCompletion,
		CodeRepairBudgetExhausted, CodeProfileNotAllowed, CodeStepNotCovered, CodeDelegationIncomplete,
	}
}

func codeRank(c Code) int {
	if i := slices.Index(Codes(), c); i >= 0 {
		return i
	}
	return len(Codes())
}

// Rejection ist eine einzelne Ablehnung: Code, betroffene Entscheidung (leer: der Batch als Ganzes) und
// Begründung. Die Begründung ist so formuliert, dass sie dem Modell zur Neuplanung zurückgegeben werden kann.
type Rejection struct {
	Code       Code   `json:"code"`
	DecisionID string `json:"decision_id,omitempty"`
	Reason     string `json:"reason"`
}

func (r Rejection) String() string {
	if r.DecisionID == "" {
		return fmt.Sprintf("[%s] (Batch) %s", r.Code, r.Reason)
	}
	return fmt.Sprintf("[%s] %s: %s", r.Code, r.DecisionID, r.Reason)
}

// RejectionError listet ALLE Ablehnungen eines Batches. Mit einer Ablehnung wird keine Entscheidung des
// Batches ausgeführt (atomar). Ablehnungen des Batches stehen zuerst, danach die der Entscheidungen in
// der Reihenfolge des Batches, je Entscheidung nach Code; gleiche Eingabe ergibt dieselbe Liste.
type RejectionError struct {
	BatchID    string      `json:"batch_id"`
	Rejections []Rejection `json:"rejections"`
}

// Error formuliert die Liste als Rückmeldung an das Modell: Was abgelehnt wurde, warum, und dass auf dem
// aktuellen Zustand neu geplant werden soll.
func (e *RejectionError) Error() string {
	if e == nil {
		return "<nil>"
	}
	var b strings.Builder
	id := e.BatchID
	if id == "" {
		id = "(ohne ID)"
	}
	fmt.Fprintf(&b, "Batch %s abgelehnt: %d Ablehnung(en). Es wurde nichts ausgeführt. Bitte auf dem aktuellen Zustand neu planen und jeden genannten Punkt beheben.", id, len(e.Rejections))
	for _, r := range e.Rejections {
		b.WriteString("\n- ")
		b.WriteString(r.String())
	}
	return b.String()
}

// Is erkennt den Fehlertyp (errors.Is(err, &RejectionError{})).
func (e *RejectionError) Is(target error) bool {
	_, ok := target.(*RejectionError)
	return ok
}

// Has meldet, ob eine Ablehnung den Code trägt.
func (e *RejectionError) Has(c Code) bool {
	return e != nil && slices.ContainsFunc(e.Rejections, func(r Rejection) bool { return r.Code == c })
}

// Codes liefert die vorkommenden Codes, eindeutig, in der Reihenfolge von Codes().
func (e *RejectionError) Codes() []Code {
	if e == nil {
		return nil
	}
	seen := map[Code]bool{}
	var out []Code
	for _, r := range e.Rejections {
		if !seen[r.Code] {
			seen[r.Code] = true
			out = append(out, r.Code)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return codeRank(out[i]) < codeRank(out[j]) })
	return out
}

// AsRejection liefert die Ablehnungsliste, wenn err eine ist.
func AsRejection(err error) (*RejectionError, bool) {
	var re *RejectionError
	if errors.As(err, &re) && re != nil {
		return re, true
	}
	return nil, false
}

// collector sammelt Ablehnungen in der Reihenfolge ihrer Entdeckung und ordnet sie am Ende deterministisch.
type collector struct {
	batchID string
	items   []collected
}

type collected struct {
	idx int // -1: Batch
	seq int
	r   Rejection
}

func (c *collector) add(idx int, decisionID string, code Code, format string, args ...any) {
	c.items = append(c.items, collected{idx: idx, seq: len(c.items), r: Rejection{Code: code, DecisionID: decisionID, Reason: fmt.Sprintf(format, args...)}})
}

func (c *collector) any() bool { return len(c.items) > 0 }

// rejected meldet, ob die Entscheidung an Position idx bereits abgelehnt wurde.
func (c *collector) rejected(idx int) bool {
	return slices.ContainsFunc(c.items, func(it collected) bool { return it.idx == idx })
}

func (c *collector) err() error {
	if len(c.items) == 0 {
		return nil
	}
	items := slices.Clone(c.items)
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if a.idx != b.idx {
			return a.idx < b.idx
		}
		if ra, rb := codeRank(a.r.Code), codeRank(b.r.Code); ra != rb {
			return ra < rb
		}
		return a.seq < b.seq
	})
	out := &RejectionError{BatchID: c.batchID, Rejections: make([]Rejection, 0, len(items))}
	seen := map[Rejection]bool{}
	for _, it := range items {
		if !seen[it.r] { // gleiche Ablehnung nur einmal
			seen[it.r] = true
			out.Rejections = append(out.Rejections, it.r)
		}
	}
	return out
}
