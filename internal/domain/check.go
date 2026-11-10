package domain

import (
	"path"
	"regexp"
	"strings"
	"time"
	"unicode"
)

// Dieser Teil enthält die Invarianten, die ein JSON-Schema nicht ausdrückt oder die vom
// aktuellen Zustand abhängen. Welche Regel wo gilt, steht in docs/architecture/contracts.md.

var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

// CheckID prüft das ID-Format (wie das Schema).
func CheckID(id string) error {
	if !idPattern.MatchString(id) {
		return newError(CatID, "", "ungültige ID %q", id)
	}
	return nil
}

func hasControl(s string) bool {
	return strings.IndexFunc(s, func(r rune) bool { return unicode.IsControl(r) }) >= 0
}

// CheckCanonicalPath verlangt einen absoluten, bereinigten Pfad ohne Steuerzeichen und
// ohne . oder .. Segmente (POSIX-Pfade; v1 gilt für macOS und Linux).
func CheckCanonicalPath(p string) error {
	switch {
	case p == "" || !strings.HasPrefix(p, "/"):
		return newError(CatPath, "", "Pfad %q ist nicht absolut", p)
	case hasControl(p):
		return newError(CatPath, "", "Pfad enthält Steuerzeichen")
	case path.Clean(p) != p:
		return newError(CatPath, "", "Pfad %q ist nicht kanonisch (. / .. / doppelte oder abschließende /)", p)
	}
	return nil
}

// CheckRelativePath verlangt einen bereinigten relativen Pfad ohne . und .. Segmente.
func CheckRelativePath(p string) error {
	switch {
	case p == "" || strings.HasPrefix(p, "/"):
		return newError(CatPath, "", "Pfad %q ist nicht relativ", p)
	case hasControl(p):
		return newError(CatPath, "", "Pfad enthält Steuerzeichen")
	case path.Clean(p) != p || p == "." || p == ".." || strings.HasPrefix(p, "../"):
		return newError(CatPath, "", "Pfad %q ist nicht kanonisch", p)
	}
	return nil
}

// Check prüft eine ProjectConfig unabhängig vom Eingabeweg (auch für direkt gebaute Werte).
func (c ProjectConfig) Check() error {
	if c.SchemaVersion != SchemaVersion {
		return newError(CatVersion, "/schema_version", "Schema-Version %d wird nicht unterstützt", c.SchemaVersion)
	}
	if err := CheckID(c.ProjectID); err != nil {
		return err
	}
	for _, p := range []string{c.CodeDir, c.ExecutionDir, c.Vault.Path} {
		if err := CheckCanonicalPath(p); err != nil {
			return err
		}
	}
	if err := CheckRelativePath(c.Vault.ProjectArea); err != nil {
		return err
	}
	if c.BaseBranch != BaseBranch {
		return newError(CatValue, "/base_branch", "base_branch muss %q sein (kein Ersatz durch main)", BaseBranch)
	}
	if c.MaxWorkers < MinWorkers || c.MaxWorkers > MaxWorkers {
		return newError(CatLimit, "/max_workers", "max_workers=%d außerhalb %d-%d", c.MaxWorkers, MinWorkers, MaxWorkers)
	}
	if c.DefaultMode != ModePR && c.DefaultMode != ModeAuto {
		return newError(CatValue, "/default_mode", "unbekannter Modus %q", c.DefaultMode)
	}
	return nil
}

// ---- Katalog: bekannte Modelle, Effort-Stufen, Skills und Policies ----------

// Catalog enthält die konfigurierten, geprüften Möglichkeiten. Er wird bei der Einrichtung
// aus der Konfiguration aufgebaut; im Code stehen keine festen Modell-IDs.
type Catalog struct {
	// Models bildet eine Modell-ID auf die vom Modell unterstützten Effort-Stufen ab.
	Models   map[string][]Effort
	Skills   map[string]bool
	Policies map[string]bool

	// Fallbacks sind die in models.yaml ausdrücklich genehmigten Effort-Reduktionen (seit CC-14). Die Daten
	// liefert profiles.BuildCatalog; es gibt keine zweite Liste.
	Fallbacks []CatalogFallback
	// SkillRoles nennt je Skill die Rollen, die ihn erhalten dürfen (seit CC-14). Ist die Abbildung gesetzt
	// (nicht nil), muss ein Skill der Auswahl dort mit der Rolle der Auswahl stehen; ein Skill ohne Eintrag
	// ist dann für keine Rolle erlaubt. Ist sie nil, kennt der Katalog keine Rolleninformation und prüft nur,
	// ob der Skill bekannt (freigegeben) ist. profiles.BuildCatalog setzt sie immer.
	SkillRoles map[string][]Role
}

// CatalogFallback ist eine genehmigte Reduktion: Fordert jemand für Model die Stufe Requested an und
// unterstützt das Modell sie nicht, läuft es mit Effective. Die Genehmigung gilt nur für genau dieses Paar.
type CatalogFallback struct {
	Model     string
	Requested Effort
	Effective Effort
	Source    string // Quelle der Genehmigung (zum Beispiel die Dokumentation)
}

// CheckModelEffort unterscheidet Modellfehler (unbekanntes Modell) von Effort-Fehlern
// (unbekannte Stufe oder vom Modell nicht unterstützt).
func (c Catalog) CheckModelEffort(model string, effort Effort) error {
	supported, ok := c.Models[model]
	if !ok {
		return newError(CatModel, "", "Modell %q ist nicht konfiguriert", model)
	}
	if !effort.Valid() {
		return newError(CatEffort, "", "unbekannte Effort-Stufe %q", effort)
	}
	if !contains(supported, effort) {
		return newError(CatEffort, "", "Modell %q unterstützt Effort %q nicht", model, effort)
	}
	return nil
}

// EffectiveEffort liefert die wirksame Stufe für ein Modell und eine angeforderte Stufe. Unterstützt das
// Modell die Stufe, ist sie wirksam. Sonst gilt nur eine ausdrücklich genehmigte Reduktion für genau dieses
// Paar (Catalog.Fallbacks); sie wird als EffortFallback ausgewiesen, damit die Reduktion sichtbar bleibt.
// Ohne Genehmigung, bei unbekanntem Modell oder unbekannter Stufe ist es derselbe Fehler wie
// CheckModelEffort. Ein Modell wird nie ersetzt.
func (c Catalog) EffectiveEffort(model string, requested Effort) (Effort, *EffortFallback, error) {
	err := c.CheckModelEffort(model, requested)
	if err == nil {
		return requested, nil, nil
	}
	supported := c.Models[model]
	if supported == nil || !requested.Valid() {
		return "", nil, err
	}
	for _, f := range c.Fallbacks {
		if f.Model != model || f.Requested != requested {
			continue
		}
		// Eine Genehmigung gilt nur, wenn sie in sich stimmt: reduziert und vom Modell unterstützt.
		if contains(supported, f.Effective) && effortRank(f.Effective) < effortRank(requested) {
			return f.Effective, &EffortFallback{Kind: FallbackEffortReduction, From: requested, To: f.Effective, Source: f.Source}, nil
		}
	}
	return "", nil, err
}

func (c Catalog) checkSkills(ids []string) error {
	for _, id := range ids {
		if !c.Skills[id] {
			return newError(CatValue, "", "Skill %q ist nicht bekannt", id)
		}
	}
	return nil
}

// checkSkillsForRole prüft, dass jeder Skill bekannt (freigegeben) und, wenn der Katalog Rolleninformation
// führt, für die Rolle erlaubt ist.
func (c Catalog) checkSkillsForRole(ids []string, role Role, location string) error {
	if err := c.checkSkills(ids); err != nil {
		return err
	}
	if c.SkillRoles == nil {
		return nil
	}
	for _, id := range ids {
		if !contains(c.SkillRoles[id], role) {
			return newError(CatRole, location, "Skill %q ist für die Rolle %q nicht erlaubt", id, role)
		}
	}
	return nil
}

// CheckProfile prüft ein AgentProfile gegen den Katalog.
func (c Catalog) CheckProfile(p AgentProfile) error {
	if !p.Role.Valid() {
		return newError(CatRole, "/role", "unbekannte Rolle %q", p.Role)
	}
	if err := c.CheckModelEffort(p.Model, p.Effort); err != nil {
		return err
	}
	if err := c.checkSkillsForRole(p.SkillIDs, p.Role, "/skill_ids"); err != nil {
		return err
	}
	if !c.Policies[p.PolicyProfile] {
		return newError(CatValue, "/policy_profile", "Policy-Profil %q ist nicht bekannt", p.PolicyProfile)
	}
	if err := c.checkRequested(p); err != nil {
		return err
	}
	return checkValueSources(p.ValueSources)
}

// effortRank ist die Position einer Stufe in Efforts() (aufsteigend); -1 für unbekannte Stufen.
func effortRank(e Effort) int {
	for i, v := range Efforts() {
		if v == e {
			return i
		}
	}
	return -1
}

// checkRequested prüft die optionalen Felder requested_model, requested_effort und fallback
// (seit CC-46) auf Widerspruchsfreiheit. Ein Modell wird nie ersetzt. Ein abweichender
// Effort ist nur als sichtbare Reduktion zulässig: die angeforderte Stufe ist vom Modell
// nicht unterstützt, die wirksame ist es und liegt darunter.
func (c Catalog) checkRequested(p AgentProfile) error {
	if p.RequestedModel != "" && p.RequestedModel != p.Model {
		return newError(CatModel, "/requested_model", "angefordertes Modell %q weicht vom wirksamen %q ab; Modelle werden nicht ersetzt", p.RequestedModel, p.Model)
	}
	f := p.Fallback
	if f == nil {
		if p.RequestedEffort != "" && p.RequestedEffort != p.Effort {
			return newError(CatEffort, "/requested_effort", "angeforderter Effort %q weicht vom wirksamen %q ab, ohne dass ein Fallback ausgewiesen ist", p.RequestedEffort, p.Effort)
		}
		return nil
	}
	switch {
	case f.Kind != FallbackEffortReduction:
		return newError(CatValue, "/fallback/kind", "unbekannte Fallback-Art %q", f.Kind)
	case p.RequestedEffort == "":
		return newError(CatEffort, "/requested_effort", "ein Fallback braucht den angeforderten Effort")
	case f.From != p.RequestedEffort:
		return newError(CatEffort, "/fallback/from", "Fallback beginnt bei %q, angefordert war %q", f.From, p.RequestedEffort)
	case f.To != p.Effort:
		return newError(CatEffort, "/fallback/to", "Fallback endet bei %q, wirksam ist %q", f.To, p.Effort)
	case effortRank(f.From) <= effortRank(f.To):
		return newError(CatEffort, "/fallback", "ein Fallback reduziert die Stufe: %q liegt nicht unter %q", f.To, f.From)
	case contains(c.Models[p.Model], f.From):
		return newError(CatEffort, "/fallback/from", "Modell %q unterstützt Effort %q; eine Reduktion ist nicht zulässig", p.Model, f.From)
	}
	return nil
}

func checkValueSources(v *ValueSources) error {
	if v == nil {
		return nil
	}
	for _, f := range []struct {
		name   string
		source ValueSource
	}{{"model", v.Model}, {"effort", v.Effort}, {"skill_ids", v.SkillIDs}} {
		if !f.source.Valid() {
			return newError(CatValue, "/value_sources/"+f.name, "unbekannte Herkunft %q", f.source)
		}
	}
	return nil
}

// ---- Entscheidungen ---------------------------------------------------------

// DecisionContext ist der aktuelle Zustand, gegen den eine Decision geprüft wird.
type DecisionContext struct {
	ProjectID     string
	StateRevision int64
	Catalog       Catalog
}

// EffectiveSelection ist die wirksame Auswahl einer geprüften Entscheidung. Effort ist die wirksame Stufe;
// weicht sie von RequestedEffort ab, steht die genehmigte Reduktion in Fallback. Ein Modell wird nie ersetzt.
type EffectiveSelection struct {
	Model           string
	RequestedEffort Effort
	Effort          Effort
	Fallback        *EffortFallback
}

// Check prüft eine formal gültige Decision gegen den aktuellen Zustand: Projektbezug,
// unveränderte Zustandsrevision sowie Rolle, Modell, Effort und Skills der Auswahl. Weitere
// Prüfungen (Voraussetzungen, Kapazität, Locks, Belege) gehören zum Orchestrator beziehungsweise zum
// Validator in internal/supervisor. Check ändert die Entscheidung nicht.
//
// Der Effort der Auswahl ist zulässig, wenn das Modell ihn unterstützt oder models.yaml genau diese
// Reduktion genehmigt (Catalog.Fallbacks); CheckEffective liefert dann zusätzlich den wirksamen Wert. Die
// Skills müssen freigegeben und für die Rolle der Auswahl erlaubt sein.
func (d Decision) Check(ctx DecisionContext) error {
	_, err := d.CheckEffective(ctx)
	return err
}

// CheckEffective prüft wie Check und liefert die wirksame Auswahl: Modell, angeforderten und wirksamen
// Effort sowie eine genehmigte Reduktion (sichtbar, nie still). Ohne Auswahl ist das Ergebnis nil.
func (d Decision) CheckEffective(ctx DecisionContext) (*EffectiveSelection, error) {
	if !d.Action.Valid() {
		return nil, newError(CatAction, "/action", "unbekannte Aktion %q", d.Action)
	}
	if d.ProjectID != ctx.ProjectID {
		return nil, newError(CatID, "/project_id", "Entscheidung gehört zu Projekt %q, nicht zu %q", d.ProjectID, ctx.ProjectID)
	}
	if d.StateRevision != ctx.StateRevision {
		return nil, newError(CatRevision, "/state_revision", "Zustandsrevision %d ist veraltet, aktuell ist %d", d.StateRevision, ctx.StateRevision)
	}
	if d.Selection == nil {
		return nil, nil
	}
	s := d.Selection
	if !s.Role.Valid() {
		return nil, newError(CatRole, "/selection/role", "unbekannte Rolle %q", s.Role)
	}
	if allowed := d.Action.Roles(); allowed != nil && !contains(allowed, s.Role) {
		return nil, newError(CatRole, "/selection/role", "Rolle %q passt nicht zur Aktion %q", s.Role, d.Action)
	}
	effective, fallback, err := ctx.Catalog.EffectiveEffort(s.Model, s.Effort)
	if err != nil {
		return nil, err
	}
	if err := ctx.Catalog.checkSkillsForRole(s.SkillIDs, s.Role, "/selection/skill_ids"); err != nil {
		return nil, err
	}
	return &EffectiveSelection{Model: s.Model, RequestedEffort: s.Effort, Effort: effective, Fallback: fallback}, nil
}

// ---- Review und Integration -------------------------------------------------

// Check prüft die Invarianten eines ReviewReport: FREIGEGEBEN ohne, BEFUNDE mit Befunden.
func (r ReviewReport) Check() error {
	switch r.Verdict {
	case VerdictApproved:
		if len(r.Findings) != 0 {
			return newError(CatValue, "/findings", "FREIGEGEBEN verlangt keine Befunde")
		}
	case VerdictFindings:
		if len(r.Findings) == 0 {
			return newError(CatValue, "/findings", "BEFUNDE verlangt mindestens einen Befund")
		}
	default:
		return newError(CatValue, "/verdict", "unbekanntes Urteil %q", r.Verdict)
	}
	return nil
}

// CheckReview prüft, ob der Review-Bericht die Integration trägt: gleicher Auftrag, gleicher
// Bericht, gleicher Commit, gleiche Konzeptversion und eindeutige Freigabe. Eine Freigabe
// für einen anderen Stand ist nicht übertragbar.
func (q IntegrationRequest) CheckReview(r ReviewReport) error {
	if err := r.Check(); err != nil {
		return err
	}
	switch {
	case r.ReportID != q.ReviewReportID:
		return newError(CatID, "/review_report_id", "Bericht %q ist nicht der angeforderte %q", r.ReportID, q.ReviewReportID)
	case r.WorkOrderID != q.WorkOrderID:
		return newError(CatID, "/work_order_id", "Bericht gehört zu Auftrag %q, nicht zu %q", r.WorkOrderID, q.WorkOrderID)
	case r.Commit != q.HeadCommit:
		return newError(CatCommit, "/head_commit", "Freigabe gilt für Commit %s, nicht für %s", r.Commit, q.HeadCommit)
	case r.ConceptVersion != q.ConceptVersion:
		return newError(CatValue, "/concept_version", "Freigabe gilt für Konzeptversion %q, nicht für %q", r.ConceptVersion, q.ConceptVersion)
	case r.Verdict != VerdictApproved:
		return newError(CatValue, "/verdict", "Integration verlangt das Urteil %s", VerdictApproved)
	}
	return nil
}

// ---- Fragen und Antworten ---------------------------------------------------

// Accept ordnet eine Antwort der Frage zu (offen -> beantwortet). Eine Antwort auf eine
// andere Frage oder eine zweite Antwort wird nicht stillschweigend übernommen.
func (q *Question) Accept(a Answer) error {
	if a.QuestionID != q.QuestionID {
		return newError(CatID, "/question_id", "Antwort gehört zu Frage %q, nicht zu %q", a.QuestionID, q.QuestionID)
	}
	if !q.State.CanTransition(QuestionAnswered) {
		return newError(CatState, "", "Frage im Zustand %q kann nicht beantwortet werden", q.State)
	}
	q.State, q.AnswerID = QuestionAnswered, a.AnswerID
	return nil
}

// ConfirmDelivery bestätigt die Zustellung der Antwort (beantwortet -> zugestellt).
func (q *Question) ConfirmDelivery(at time.Time) error {
	if !q.State.CanTransition(QuestionDelivered) {
		return newError(CatState, "", "Frage im Zustand %q hat keine zuzustellende Antwort", q.State)
	}
	q.State, q.DeliveryConfirmedAt = QuestionDelivered, at
	return nil
}

// ---- Prozessmessung und Agentenübersicht ------------------------------------

// Identity liefert die Prozessidentität (PID und Startzeit).
func (s ProcessSample) Identity() ProcessIdentity {
	return ProcessIdentity{PID: s.PID, StartTime: s.StartTime}
}

// ParentIdentity liefert die Identität des Elternprozesses.
func (s ProcessSample) ParentIdentity() ProcessIdentity {
	return ProcessIdentity{PID: s.ParentPID, StartTime: s.ParentStartTime}
}

// Check prüft eine Messung: PID mit Startzeit, bekannter OS-Zustand, keine negativen Werte,
// Elternbezug nur mit Startzeit, Messzeit nach dem Prozessstart.
func (s ProcessSample) Check() error {
	switch {
	case s.PID <= 0:
		return newError(CatValue, "/pid", "PID %d ungültig", s.PID)
	case s.StartTime.IsZero():
		return newError(CatTimestamp, "/start_time", "PID %d ohne Startzeit ist keine Identität", s.PID)
	case !contains(OSProcessStates(), s.State):
		return newError(CatValue, "/state", "unbekannter Prozesszustand %q", s.State)
	case s.CPUPercent != nil && *s.CPUPercent < 0:
		return newError(CatValue, "/cpu_percent", "negative CPU-Auslastung")
	case s.ParentPID != 0 && (s.ParentPID < 0 || s.ParentStartTime.IsZero()):
		return newError(CatValue, "/parent_start_time", "Elternbezug braucht PID und Startzeit")
	case s.SampledAt.IsZero():
		return newError(CatTimestamp, "/sampled_at", "Messzeitpunkt fehlt")
	case s.SampledAt.Before(s.StartTime):
		return newError(CatTimestamp, "/sampled_at", "Messung liegt vor dem Prozessstart")
	}
	return nil
}

// Check prüft, dass die getrennten Ebenen des Snapshots zusammenpassen: ein aktiver oder
// reservierter Agent hat eine Ausführung, ein freier oder nicht verfügbarer keine laufende.
func (a AgentSnapshot) Check() error {
	if !a.Role.Valid() {
		return newError(CatRole, "/role", "unbekannte Rolle %q", a.Role)
	}
	if !contains(AgentAvailabilities(), a.Availability) {
		return newError(CatState, "/availability", "unbekannte Verfügbarkeit %q", a.Availability)
	}
	if a.ExecutionState != "" && !contains(ExecutionStates(), a.ExecutionState) {
		return newError(CatState, "/execution_state", "unbekannter Ausführungszustand %q", a.ExecutionState)
	}
	if a.WorkOrderState != "" && !contains(WorkOrderStates(), a.WorkOrderState) {
		return newError(CatState, "/work_order_state", "unbekannter Auftragszustand %q", a.WorkOrderState)
	}
	running := a.ExecutionID != "" && a.ExecutionState != "" && !a.ExecutionState.IsTerminal()
	switch a.Availability {
	case AgentActive:
		if !running {
			return newError(CatState, "/availability", "aktiver Agent ohne laufende Ausführung")
		}
	case AgentAvailable, AgentUnavailable:
		if running && a.Availability == AgentAvailable {
			return newError(CatState, "/availability", "freier Agent mit laufender Ausführung")
		}
	}
	if a.Process != nil {
		return a.Process.Check()
	}
	return nil
}
