package supervisor

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/alexwenzel/ccmas/internal/domain"
	"github.com/alexwenzel/ccmas/internal/skills"
)

// SnapshotSchemaVersion ist die Version des ProjectSnapshot.
const SnapshotSchemaVersion = 1

// NoConceptVersion ist die feste Kennung der Konzeptversion für Aufträge ohne Konzeptbezug (zum Beispiel Fehler- und
// Organisationsaufträge): Prüfziel (review_target.concept_version, Pflichtfeld des Schemas) und Review-Bericht tragen sie
// statt einer Version. Sie ist reserviert; ein echter Konzeptstand darf sie nicht als Version führen.
const NoConceptVersion = "kein-konzept"

// OrderKind ist die Art eines Auftrags (Typ des Tickets).
type OrderKind string

const (
	OrderConcept        OrderKind = "concept"        // Konzeptarbeit
	OrderImplementation OrderKind = "implementation" // Umsetzung
	OrderBug            OrderKind = "bug"            // Fehlerbehebung (wie Umsetzung)
	OrderOrganisation   OrderKind = "organisation"   // Organisation (keine Umsetzung, keine Konzeptarbeit)
)

// OrderKinds liefert die Auftragsarten.
func OrderKinds() []OrderKind {
	return []OrderKind{OrderConcept, OrderImplementation, OrderBug, OrderOrganisation}
}

// PRStatus ist der Stand eines PR.
type PRStatus string

const (
	PROpen   PRStatus = "open"
	PRMerged PRStatus = "merged"
	PRClosed PRStatus = "closed"
)

// CIStatus ist das Ergebnis der verpflichtenden CI-Prüfungen eines PR. Ein fehlender oder nicht
// ausgeführter Pflichtcheck zählt nicht als bestanden.
type CIStatus string

const (
	CIPassed  CIStatus = "passed"
	CIFailed  CIStatus = "failed"
	CIPending CIStatus = "pending"
	CIMissing CIStatus = "missing"
)

// ProjectSnapshot ist der Zustand eines Projekts für die Planung: genau das, was der Supervisor sehen muss, um
// zu planen, und was der Validator braucht, um den Plan zu prüfen. Er ist ein Wert zu einer Zustandsrevision;
// eine Entscheidung gilt nur für diese Revision (sonst stale_revision).
//
// Der Snapshot ist strikt dekodierbar (DecodeSnapshot) und deterministisch serialisiert (json.Marshal sortiert
// Aufträge, Abhängigkeiten, Fragen, Ausführungen, Rollen und Skills und gibt nie null aus). Er ist ein
// Go-Typ, kein Vertrag zwischen den Sprachen: Befüllt wird er vom Orchestrator (M03) aus dem führenden
// Zustand; die Felder, die erst M03 liefern kann (Review-Datensatz mit Reviewer-Rolle und
// Ausführungs-IDs, Kopf-Commit, PR-Stand, Abschlussnachweise, letztes Rollenergebnis), sind hier als
// Schnittstelle vorbereitet. Der Validator verlässt sich nie auf Angaben des Modells, nur auf diesen Snapshot.
type ProjectSnapshot struct {
	SchemaVersion   int                    `json:"schema_version"`
	ProjectID       string                 `json:"project_id"`
	StateRevision   int64                  `json:"state_revision"`
	Goal            Goal                   `json:"goal"`
	IntegrationMode domain.IntegrationMode `json:"integration_mode"` // beauftragter Modus des Ziels
	Capacity        Capacity               `json:"capacity"`
	Orders          []Order                `json:"orders"`
	OpenQuestions   []OpenQuestion         `json:"open_questions"`
	Roles           []RoleOffer            `json:"roles"`  // erlaubte Rollenprofile
	Skills          []SkillOffer           `json:"skills"` // freigegebene Skills
}

// Goal ist das beauftragte Ziel mit Kriterien und Umfang.
type Goal struct {
	Kind               domain.TargetKind `json:"kind"` // ticket, feature oder milestone
	ID                 string            `json:"id"`
	Title              string            `json:"title,omitempty"`
	AcceptanceCriteria []string          `json:"acceptance_criteria"`
	// Scope nennt die beauftragten Tickets und Features (zusätzlich zum Ziel selbst und seinen Aufträgen).
	Scope []string `json:"scope"`
	// OutOfScope nennt bekannte, aber nicht beauftragte Tickets (zum Beispiel unausgewählte): Der Supervisor
	// darf sie nicht stillschweigend übernehmen. Eine ID, die der Snapshot gar nicht kennt, ist unbekannt.
	OutOfScope []string `json:"out_of_scope"`
	// Evidence sind die Abschlussnachweise; ohne sie gilt nichts als belegt.
	Evidence *Evidence `json:"evidence,omitempty"`
}

// Evidence sind die Nachweise für den Abschluss des Ziels (skills.CompletionEvidence ohne Modus, der aus dem
// Snapshot kommt). Jedes Feld ist ein Beleg, kein Wunsch: false heißt nicht belegt.
type Evidence struct {
	Integration skills.IntegrationState `json:"integration,omitempty"`
	MergedBy    skills.MergeActor       `json:"merged_by,omitempty"`

	AcceptanceCriteriaMet bool `json:"acceptance_criteria_met"`
	ReviewEvidence        bool `json:"review_evidence"`
	TestEvidence          bool `json:"test_evidence"`
	CIEvidence            bool `json:"ci_evidence"`
	TaskStateUpdated      bool `json:"task_state_updated"`
	KnowledgeUpdated      bool `json:"knowledge_updated"`

	ChildrenComplete bool `json:"children_complete"` // nur Meilenstein
	OwnerAcceptance  bool `json:"owner_acceptance"`  // nur Meilenstein
}

func (e Evidence) toSkills(mode domain.IntegrationMode) skills.CompletionEvidence {
	return skills.CompletionEvidence{
		Mode: mode, Integration: e.Integration, MergedBy: e.MergedBy,
		AcceptanceCriteriaMet: e.AcceptanceCriteriaMet, ReviewEvidence: e.ReviewEvidence, TestEvidence: e.TestEvidence,
		CIEvidence: e.CIEvidence, TaskStateUpdated: e.TaskStateUpdated, KnowledgeUpdated: e.KnowledgeUpdated,
		ChildrenComplete: e.ChildrenComplete, OwnerAcceptance: e.OwnerAcceptance,
	}
}

// Capacity ist die Kapazität: das Worker-Limit des Projekts und die laufenden Ausführungen.
type Capacity struct {
	MaxWorkers int `json:"max_workers"`
	// Active sind die aktiven (nicht beendeten) Ausführungen. Der Supervisor selbst gehört nicht zu den
	// Worker-Plätzen: Einträge der Rolle supervisor werden nicht mitgezählt.
	Active []ActiveExecution `json:"active"`
}

// ActiveExecution ist eine laufende Ausführung.
type ActiveExecution struct {
	ExecutionID string                `json:"execution_id"`
	Role        domain.Role           `json:"role"`
	WorkOrderID string                `json:"work_order_id,omitempty"`
	State       domain.ExecutionState `json:"state"`
}

// Order ist ein Arbeitsauftrag des Ziels mit allem, was die Zulässigkeit der nächsten Schritte bestimmt.
type Order struct {
	ID        string                 `json:"id"`
	Kind      OrderKind              `json:"kind"`
	Title     string                 `json:"title,omitempty"`
	State     domain.WorkOrderState  `json:"state"`
	Mode      domain.IntegrationMode `json:"mode"` // beauftragter Modus dieses Auftrags
	DependsOn []string               `json:"depends_on"`

	Concept *ConceptState `json:"concept,omitempty"`

	// HeadCommit und Branch sind der aktuelle Stand der Implementierung (Kopf des Auftragsbranches).
	HeadCommit string `json:"head_commit,omitempty"`
	Branch     string `json:"branch,omitempty"`

	LastReview *ReviewRecord `json:"last_review,omitempty"`
	LastResult *LastResult   `json:"last_result,omitempty"`
	PR         *PRInfo       `json:"pr,omitempty"`

	// Reparaturrunden, die bereits gelaufen sind, je Schleife gezählt (Code- und Konzeptschleife).
	CodeRepairRounds    int `json:"code_repair_rounds"`
	ConceptRepairRounds int `json:"concept_repair_rounds"`

	ActiveExecutionID string `json:"active_execution_id,omitempty"`

	// ReviewConceptVersion ist die Konzeptversion, die ein Review und dessen Bericht für diesen Auftrag tragen müssen
	// (review_target.concept_version): die Version des Konzeptstands, NoConceptVersion ohne Konzeptbezug, leer, solange ein
	// Konzeptbezug ohne Konzeptstand besteht (dann ist weder Review noch Integration zulässig). Der Wert ist abgeleitet:
	// Canonical setzt ihn immer neu, ein vom Befüller gesetzter Wert zählt nicht. Der Validator rechnet selbst nach.
	ReviewConceptVersion string `json:"review_concept_version,omitempty"`
}

// ConceptState ist der Konzeptstand eines Auftrags: Version, Einstufung und Freigabe.
type ConceptState struct {
	Version  string               `json:"version"`
	Class    skills.ConceptClass  `json:"class"`              // routine, strategic; leer: nicht eingestuft
	Criteria []string             `json:"criteria"`           // zutreffende Kriterien für „strategisch“
	Approval *ConceptApprovalInfo `json:"approval,omitempty"` // nil: nicht freigegeben
}

// ConceptApprovalInfo ist eine erteilte Konzeptfreigabe; sie gilt nur für genau die genannte Version.
type ConceptApprovalInfo struct {
	Version  string          `json:"version"`
	Approver skills.Approver `json:"approver"`
}

// ReviewRecord ist das letzte Review eines Auftrags. Der Bericht trägt Urteil, Commit, Konzeptversion und die
// Ausführung des Reviewers; die Rolle der Reviewer-Ausführung und die Ausführung der Implementierung kennt nur
// der Ausführungsdatensatz (Orchestrator), nie der Bericht selbst.
type ReviewRecord struct {
	Report                 domain.ReviewReport `json:"report"`
	ReviewerRole           domain.Role         `json:"reviewer_role"`
	ImplementerExecutionID string              `json:"implementer_execution_id,omitempty"`
}

// LastResult ist das zuletzt gemeldete Ergebnis einer Rolle für diesen Auftrag (Rolle und Ergebnisname der
// Rollenregeln). Aus den Rollenregeln folgen die zulässigen Folgeschritte.
type LastResult struct {
	Role   domain.Role `json:"role"`
	Result string      `json:"result"`
}

// PRInfo ist der PR-Stand eines Auftrags.
type PRInfo struct {
	Number int64    `json:"number"`
	State  PRStatus `json:"state"`
	CI     CIStatus `json:"ci"`
	// HeadCommit ist der Commit, den der PR als Kopf trägt (vollständiger Hash). Optional; Schnittstelle zu M03. Ohne
	// ihn lässt sich bei einem offenen PR nicht belegen, ob eine erneute Integration eine neue Wirkung hätte, und der
	// Validator lehnt sie ab (fail-safe).
	HeadCommit string `json:"head_commit,omitempty"`
}

// OpenQuestion ist eine noch nicht zugestellte Frage. Keine Antwort ist keine Freigabe.
type OpenQuestion struct {
	QuestionID  string               `json:"question_id"`
	WorkOrderID string               `json:"work_order_id,omitempty"`
	State       domain.QuestionState `json:"state"`
	Text        string               `json:"text,omitempty"`
}

// RoleOffer ist ein erlaubtes Rollenprofil (siehe BuildRoleOffers). Welche Werte tatsächlich zulässig sind,
// entscheidet die Auflösung des Profils (Policy), nicht diese Angabe; sie zeigt dem Supervisor die Auswahl.
type RoleOffer struct {
	Role domain.Role `json:"role"`
	// AgentID ist der individuelle Agent, der diese Rolle ausführt; seine Werte haben Vorrang vor der Wahl des
	// Supervisors.
	AgentID         string          `json:"agent_id,omitempty"`
	Model           string          `json:"model"`
	Effort          domain.Effort   `json:"effort"`
	Policy          string          `json:"policy"`
	AllowedModels   []string        `json:"allowed_models"`
	AllowedEfforts  []domain.Effort `json:"allowed_efforts"`
	SkillIDs        []string        `json:"skill_ids"`
	DefaultSkillIDs []string        `json:"default_skill_ids"`
}

// SkillOffer ist ein freigegebener Skill des Katalogs.
type SkillOffer struct {
	ID          string        `json:"id"`
	Category    string        `json:"category"`
	Description string        `json:"description,omitempty"`
	Roles       []domain.Role `json:"roles"`
}

// ---- Serialisierung ------------------------------------------------------------

// DecodeSnapshot liest einen Snapshot streng: genau ein JSON-Wert, keine unbekannten Felder, keine Folgedaten.
// Die inhaltliche Prüfung ist Check.
func DecodeSnapshot(raw []byte) (ProjectSnapshot, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var s ProjectSnapshot
	if err := dec.Decode(&s); err != nil {
		return ProjectSnapshot{}, fmt.Errorf("snapshot nicht lesbar: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return ProjectSnapshot{}, errors.New("snapshot: unerwartete Daten nach dem JSON-Objekt")
	}
	return s, nil
}

// MarshalJSON serialisiert deterministisch: Das Ergebnis hängt nicht von der Reihenfolge der Eingabe ab.
func (s ProjectSnapshot) MarshalJSON() ([]byte, error) {
	type plain ProjectSnapshot // ohne Methoden, sonst Rekursion
	return json.Marshal(plain(s.Canonical()))
}

// Canonical liefert eine tiefe Kopie in kanonischer Form: sortierte Listen und nie nil-Listen. Gleiche
// Inhalte ergeben byte-gleiche Serialisierung.
func (s ProjectSnapshot) Canonical() ProjectSnapshot {
	out := s
	out.Goal.AcceptanceCriteria = nonNil(slices.Clone(s.Goal.AcceptanceCriteria))
	out.Goal.Scope = sortedCopy(s.Goal.Scope)
	out.Goal.OutOfScope = sortedCopy(s.Goal.OutOfScope)
	if s.Goal.Evidence != nil {
		ev := *s.Goal.Evidence
		out.Goal.Evidence = &ev
	}
	out.Capacity.Active = nonNil(slices.Clone(s.Capacity.Active))
	sort.SliceStable(out.Capacity.Active, func(i, j int) bool { return out.Capacity.Active[i].ExecutionID < out.Capacity.Active[j].ExecutionID })

	byID := make(map[string]*Order, len(s.Orders))
	for i := range s.Orders {
		byID[s.Orders[i].ID] = &s.Orders[i]
	}
	out.Orders = make([]Order, len(s.Orders))
	for i, o := range s.Orders {
		o.ReviewConceptVersion = reviewConceptVersion(byID, &o)
		o.DependsOn = sortedCopy(o.DependsOn)
		if o.Concept != nil {
			c := *o.Concept
			c.Criteria = sortedCopy(c.Criteria)
			if c.Approval != nil {
				a := *c.Approval
				c.Approval = &a
			}
			o.Concept = &c
		}
		if o.LastReview != nil {
			r := *o.LastReview
			r.Report.Findings = nonNil(slices.Clone(r.Report.Findings))
			o.LastReview = &r
		}
		if o.LastResult != nil {
			l := *o.LastResult
			o.LastResult = &l
		}
		if o.PR != nil {
			p := *o.PR
			o.PR = &p
		}
		out.Orders[i] = o
	}
	sort.SliceStable(out.Orders, func(i, j int) bool { return out.Orders[i].ID < out.Orders[j].ID })

	out.OpenQuestions = nonNil(slices.Clone(s.OpenQuestions))
	sort.SliceStable(out.OpenQuestions, func(i, j int) bool { return out.OpenQuestions[i].QuestionID < out.OpenQuestions[j].QuestionID })

	out.Roles = make([]RoleOffer, len(s.Roles))
	for i, r := range s.Roles {
		r.AllowedModels = sortedCopy(r.AllowedModels)
		r.AllowedEfforts = nonNil(slices.Clone(r.AllowedEfforts))
		sort.SliceStable(r.AllowedEfforts, func(a, b int) bool { return effortRank(r.AllowedEfforts[a]) < effortRank(r.AllowedEfforts[b]) })
		r.SkillIDs = sortedCopy(r.SkillIDs)
		r.DefaultSkillIDs = sortedCopy(r.DefaultSkillIDs)
		out.Roles[i] = r
	}
	sort.SliceStable(out.Roles, func(i, j int) bool {
		a, b := out.Roles[i], out.Roles[j]
		if ra, rb := roleRank(a.Role), roleRank(b.Role); ra != rb {
			return ra < rb
		}
		return a.AgentID < b.AgentID
	})

	out.Skills = make([]SkillOffer, len(s.Skills))
	for i, k := range s.Skills {
		k.Roles = nonNil(slices.Clone(k.Roles))
		sort.SliceStable(k.Roles, func(a, b int) bool { return roleRank(k.Roles[a]) < roleRank(k.Roles[b]) })
		out.Skills[i] = k
	}
	sort.SliceStable(out.Skills, func(i, j int) bool { return out.Skills[i].ID < out.Skills[j].ID })
	return out
}

// conceptDependencyIDs nennt die Abhängigkeiten eines Auftrags, die Konzeptaufträge sind.
func conceptDependencyIDs(orders map[string]*Order, o *Order) []string {
	var out []string
	for _, dep := range o.DependsOn {
		if do := orders[dep]; do != nil && do.Kind == OrderConcept {
			out = append(out, dep)
		}
	}
	return out
}

// reviewConceptVersion ist die Konzeptversion, die Prüfziel und Bericht eines Reviews (und die Integrationsanforderung) für den
// Auftrag tragen müssen: die Version des Konzeptstands; NoConceptVersion für Umsetzungs- und Fehlerauftrag ohne Konzeptbezug;
// leer, wo es keinen gültigen Wert gibt (Konzeptbezug über eine Abhängigkeit ohne Konzeptstand am Auftrag; andere Auftragsarten
// ohne Konzeptstand). Der Validator verlangt genau diesen Wert und rechnet selbst, nie nach dem Feld des Snapshots.
func reviewConceptVersion(orders map[string]*Order, o *Order) string {
	switch {
	case o.Concept != nil:
		return o.Concept.Version
	case len(conceptDependencyIDs(orders, o)) > 0:
		return ""
	case o.Kind == OrderImplementation || o.Kind == OrderBug:
		return NoConceptVersion
	}
	return ""
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

func sortedCopy(in []string) []string {
	out := nonNil(slices.Clone(in))
	sort.Strings(out)
	return out
}

func effortRank(e domain.Effort) int {
	if i := slices.Index(domain.Efforts(), e); i >= 0 {
		return i
	}
	return len(domain.Efforts())
}

func roleRank(r domain.Role) int {
	if i := slices.Index(domain.Roles(), r); i >= 0 {
		return i
	}
	return len(domain.Roles())
}

// ---- Prüfung -------------------------------------------------------------------

var commitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

// SnapshotProblem ist ein Verstoß gegen die Invarianten eines Snapshots.
type SnapshotProblem struct {
	Path    string
	Message string
}

func (p SnapshotProblem) String() string { return p.Path + ": " + p.Message }

// Problems prüft die Invarianten und liefert alle Verstöße (leer: gültig). Ein ungültiger Snapshot ist keine
// Grundlage für eine Entscheidung; der Validator lehnt dann jeden Batch ab (invalid_snapshot).
func (s ProjectSnapshot) Problems() []SnapshotProblem {
	var out []SnapshotProblem
	bad := func(path, format string, args ...any) {
		out = append(out, SnapshotProblem{Path: path, Message: fmt.Sprintf(format, args...)})
	}
	id := func(path, v string) bool {
		if err := domain.CheckID(v); err != nil {
			bad(path, "ungültige ID %q", v)
			return false
		}
		return true
	}

	if s.SchemaVersion != SnapshotSchemaVersion {
		bad("schema_version", "Version %d wird nicht unterstützt (erwartet %d)", s.SchemaVersion, SnapshotSchemaVersion)
	}
	id("project_id", s.ProjectID)
	if s.StateRevision < 0 {
		bad("state_revision", "negative Zustandsrevision %d", s.StateRevision)
	}
	if !slices.Contains(domain.IntegrationModes(), s.IntegrationMode) {
		bad("integration_mode", "unbekannter Modus %q", s.IntegrationMode)
	}

	// Ziel.
	if !slices.Contains([]domain.TargetKind{domain.KindTicket, domain.KindFeature, domain.KindMilestone}, s.Goal.Kind) {
		bad("goal.kind", "Zielart %q ist nicht ticket, feature oder milestone", s.Goal.Kind)
	}
	id("goal.id", s.Goal.ID)
	known := map[string]string{s.Goal.ID: "goal"}
	for _, list := range []struct {
		name string
		ids  []string
	}{{"goal.scope", s.Goal.Scope}, {"goal.out_of_scope", s.Goal.OutOfScope}} {
		for i, v := range list.ids {
			p := fmt.Sprintf("%s[%d]", list.name, i)
			if !id(p, v) {
				continue
			}
			if prev, dup := known[v]; dup {
				bad(p, "ID %q steht bereits in %s", v, prev)
			}
			known[v] = list.name
		}
	}
	if s.Goal.Evidence != nil {
		ev := s.Goal.Evidence
		if !slices.Contains([]skills.IntegrationState{skills.IntegrationNone, skills.IntegrationPROpen, skills.IntegrationMerged}, ev.Integration) {
			bad("goal.evidence.integration", "unbekannter Integrationsstand %q", ev.Integration)
		}
		if ev.MergedBy != "" && ev.MergedBy != skills.MergedByHuman && ev.MergedBy != skills.MergedByQueue {
			bad("goal.evidence.merged_by", "unbekannter Merge-Akteur %q", ev.MergedBy)
		}
	}

	// Kapazität.
	if s.Capacity.MaxWorkers < domain.MinWorkers || s.Capacity.MaxWorkers > domain.MaxWorkers {
		bad("capacity.max_workers", "max_workers=%d außerhalb %d-%d", s.Capacity.MaxWorkers, domain.MinWorkers, domain.MaxWorkers)
	}
	execs := map[string]ActiveExecution{}
	for i, a := range s.Capacity.Active {
		p := fmt.Sprintf("capacity.active[%d]", i)
		if id(p+".execution_id", a.ExecutionID) {
			if _, dup := execs[a.ExecutionID]; dup {
				bad(p, "Ausführung %q kommt mehrfach vor", a.ExecutionID)
			}
			execs[a.ExecutionID] = a
		}
		if !a.Role.Valid() {
			bad(p+".role", "unbekannte Rolle %q", a.Role)
		}
		if !slices.Contains(domain.ExecutionStates(), a.State) || a.State.IsTerminal() {
			bad(p+".state", "eine aktive Ausführung hat keinen laufenden Zustand (ist %q)", a.State)
		}
		if a.WorkOrderID != "" {
			id(p+".work_order_id", a.WorkOrderID)
		}
	}

	// Aufträge.
	orders := map[string]bool{}
	for i, o := range s.Orders {
		p := fmt.Sprintf("orders[%d]", i)
		if id(p+".id", o.ID) {
			if orders[o.ID] {
				bad(p+".id", "Auftrag %q kommt mehrfach vor", o.ID)
			}
			if prev, dup := known[o.ID]; dup {
				bad(p+".id", "Auftrag %q kollidiert mit %s", o.ID, prev)
			}
			orders[o.ID] = true
		}
	}
	for i, o := range s.Orders {
		p := fmt.Sprintf("orders[%d]", i)
		if !slices.Contains(OrderKinds(), o.Kind) {
			bad(p+".kind", "unbekannte Auftragsart %q", o.Kind)
		}
		if !slices.Contains(domain.WorkOrderStates(), o.State) {
			bad(p+".state", "unbekannter Auftragszustand %q", o.State)
		}
		if !slices.Contains(domain.IntegrationModes(), o.Mode) {
			bad(p+".mode", "unbekannter Modus %q", o.Mode)
		}
		for j, d := range o.DependsOn {
			dp := fmt.Sprintf("%s.depends_on[%d]", p, j)
			if id(dp, d) && d == o.ID {
				bad(dp, "Auftrag %q hängt von sich selbst ab", o.ID)
			}
		}
		if o.Concept != nil {
			c := o.Concept
			if c.Version == "" {
				bad(p+".concept.version", "Konzeptversion fehlt")
			}
			if c.Version == NoConceptVersion {
				bad(p+".concept.version", "die Kennung %q ist für Aufträge ohne Konzeptbezug reserviert und keine Konzeptversion", NoConceptVersion)
			}
			if c.Class != "" && c.Class != skills.ConceptRoutine && c.Class != skills.ConceptStrategic {
				bad(p+".concept.class", "unbekannte Einstufung %q", c.Class)
			}
			if c.Approval != nil && c.Approval.Approver != skills.ApproverUser && c.Approval.Approver != skills.ApproverConceptReviewer {
				bad(p+".concept.approval.approver", "unbekannter Freigeber %q", c.Approval.Approver)
			}
		}
		if o.HeadCommit != "" && !commitPattern.MatchString(o.HeadCommit) {
			bad(p+".head_commit", "kein vollständiger Commit-Hash")
		}
		if o.LastReview != nil {
			r := o.LastReview
			// Eine fehlende Rolle heißt „unbekannt“; PushAllowed gibt dann nichts frei (fail-safe).
			if r.ReviewerRole != "" && !r.ReviewerRole.Valid() {
				bad(p+".last_review.reviewer_role", "unbekannte Rolle %q", r.ReviewerRole)
			}
			if r.Report.WorkOrderID != o.ID {
				bad(p+".last_review.report.work_order_id", "Bericht gehört zu Auftrag %q, nicht zu %q", r.Report.WorkOrderID, o.ID)
			}
			if err := r.Report.Check(); err != nil {
				bad(p+".last_review.report", "%v", err)
			}
		}
		if o.LastResult != nil && (!o.LastResult.Role.Valid() || o.LastResult.Result == "") {
			bad(p+".last_result", "Rolle %q und Ergebnis %q sind keine gültige Angabe", o.LastResult.Role, o.LastResult.Result)
		}
		if o.PR != nil {
			if o.PR.Number < 1 {
				bad(p+".pr.number", "PR-Nummer %d ungültig", o.PR.Number)
			}
			if !slices.Contains([]PRStatus{PROpen, PRMerged, PRClosed}, o.PR.State) {
				bad(p+".pr.state", "unbekannter PR-Stand %q", o.PR.State)
			}
			if !slices.Contains([]CIStatus{CIPassed, CIFailed, CIPending, CIMissing}, o.PR.CI) {
				bad(p+".pr.ci", "unbekannter CI-Stand %q", o.PR.CI)
			}
			if o.PR.HeadCommit != "" && !commitPattern.MatchString(o.PR.HeadCommit) {
				bad(p+".pr.head_commit", "kein vollständiger Commit-Hash")
			}
		}
		if o.CodeRepairRounds < 0 || o.ConceptRepairRounds < 0 {
			bad(p, "negative Reparaturrunden")
		}
		if o.ActiveExecutionID != "" {
			if a, ok := execs[o.ActiveExecutionID]; !ok || a.WorkOrderID != o.ID {
				bad(p+".active_execution_id", "Ausführung %q ist nicht als aktive Ausführung dieses Auftrags geführt", o.ActiveExecutionID)
			}
		}
	}
	for i, o := range s.Orders {
		for j, d := range o.DependsOn {
			if !orders[d] && d != o.ID {
				if _, other := known[d]; other {
					continue // Verweis auf Ticket oder Feature des Umfangs: zulässig, aber kein Auftrag
				}
				bad(fmt.Sprintf("orders[%d].depends_on[%d]", i, j), "Abhängigkeit %q ist weder Auftrag noch Teil des Umfangs", d)
			}
		}
	}

	// Fragen.
	seenQ := map[string]bool{}
	for i, q := range s.OpenQuestions {
		p := fmt.Sprintf("open_questions[%d]", i)
		if id(p+".question_id", q.QuestionID) {
			if seenQ[q.QuestionID] {
				bad(p, "Frage %q kommt mehrfach vor", q.QuestionID)
			}
			seenQ[q.QuestionID] = true
		}
		if !slices.Contains(domain.QuestionStates(), q.State) {
			bad(p+".state", "unbekannter Fragezustand %q", q.State)
		}
		if q.WorkOrderID != "" && !orders[q.WorkOrderID] {
			bad(p+".work_order_id", "Auftrag %q ist nicht im Snapshot", q.WorkOrderID)
		}
	}

	// Rollenprofile und Skills.
	seenRole := map[domain.Role]int{}
	for i, r := range s.Roles {
		p := fmt.Sprintf("roles[%d]", i)
		if !r.Role.Valid() {
			bad(p+".role", "unbekannte Rolle %q", r.Role)
		}
		// Höchstens ein Profilangebot (und damit höchstens ein Agent) je Rolle: der Supervisor sieht die Auswahl, Go
		// wählt unter mehreren Agenten nicht still aus.
		if first, dup := seenRole[r.Role]; dup {
			bad(p+".role", "Rolle %s hat mehrere Profilangebote (roles[%d] und roles[%d]); höchstens ein Agent je Rolle", r.Role, first, i)
		} else {
			seenRole[r.Role] = i
		}
		if r.AgentID != "" {
			id(p+".agent_id", r.AgentID)
		}
	}
	for i, k := range s.Skills {
		id(fmt.Sprintf("skills[%d].id", i), k.ID)
	}
	return out
}

// Check prüft die Invarianten (siehe Problems) und liefert alle Verstöße in einem Fehler.
func (s ProjectSnapshot) Check() error {
	problems := s.Problems()
	if len(problems) == 0 {
		return nil
	}
	lines := make([]string, len(problems))
	for i, p := range problems {
		lines[i] = p.String()
	}
	return errors.New("snapshot ungültig: " + strings.Join(lines, "; "))
}
