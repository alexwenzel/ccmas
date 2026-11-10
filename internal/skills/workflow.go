package skills

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/alexwenzel/ccmas/internal/domain"
)

// Dieser Teil lädt und prüft die Rollenregeln (rules/roles.yaml) und die Workflow-Verträge
// (contract.yaml je Workflow-Skill). Aufbau, Prüfungen und Auswerter: docs/architecture/workflow-rules.md.
//
// Grundsatz: Regeln und Verträge wählen aus und beschränken. Sie können Worker-Limit,
// Integrationsmodus, Policy-Profil, Berechtigungen und Zielbranch weder setzen noch erweitern;
// solche Schlüssel sind ein Ladefehler (platform_limit).

const (
	// RulesSchemaVersion und ContractSchemaVersion sind die unterstützten schema_version-Werte.
	RulesSchemaVersion    = 1
	ContractSchemaVersion = 1

	rulesRel     = "rules/roles.yaml"
	contractFile = "contract.yaml"
	maxRulesSize = 1 << 20
)

// Fähigkeiten, die ein Vertrag erlauben oder verbieten kann (scope). Die Regeldatei muss genau
// diese Menge führen; ihre Bedeutung für die Inventarprüfung steht in capabilityWrites.
var knownCapabilities = []string{
	"read_repo", "read_vault", "write_worktree", "run_checks", "commit", "push", "open_pr", "merge",
	"write_asana", "write_vault", "start_agents", "change_platform_limits",
}

// capabilityWrites ordnet Schreibfähigkeiten dem Inventarfeld external_writes des Manifests zu.
var capabilityWrites = map[string]string{
	"write_asana": "asana", "write_vault": "vault", "commit": "git", "push": "git", "open_pr": "github", "merge": "github",
}

var mandatoryAlwaysForbidden = []string{"start_agents", "change_platform_limits"}

// protectedPlatformValues sind die Plattformwerte, die weder Regeldatei noch Vertrag setzen.
var protectedPlatformValues = []string{"integration_mode", "max_workers", "permissions", "policy_profile", "target_branch"}

// platformKeys sind Schlüssel (kleingeschrieben), die in Regeldatei und Verträgen nirgends vorkommen
// dürfen, weil sie Plattformgrenzen setzen würden. Einzige Ausnahme: platform.target_branch in der Regeldatei.
var platformKeys = map[string]bool{
	"max_workers": true, "worker_limit": true, "workers": true,
	"integration_mode": true, "merge": true, "merge_mode": true, "auto_merge": true, "default_mode": true, "mode": true,
	"policy_profile": true, "policy": true, "policies": true,
	"permissions": true, "permission": true, "permission_mode": true, "allowed_tools": true, "tools": true,
	"base_branch": true, "target_branch": true, "branch": true,
}

// Nachweise für den Abschluss (feste Menge; die Regeldatei wählt nicht aus, sie bestätigt sie).
var (
	featureEvidence   = []string{"acceptance_criteria_met", "merge_confirmed", "review_evidence", "test_evidence", "ci_evidence", "task_state_updated", "knowledge_updated"}
	milestoneEvidence = []string{"children_complete", "owner_acceptance"}
)

var questionHops = []string{"worker", "supervisor", "central", "supervisor", "worker"}

var workflowCategoryNames = []Category{CategoryConcept, CategoryReview, CategoryDelivery, CategoryCompletion, CategoryQuestions}

var (
	identPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	yamlLineErr  = regexp.MustCompile(`^line (\d+): (.*)$`)
)

// preconditionKinds liefert die Voraussetzungsarten einer Decision (wie decision-v1.schema.json).
func preconditionKinds() []domain.PreconditionKind {
	return []domain.PreconditionKind{
		domain.PrecWorkOrderCompleted, domain.PrecConceptApproved, domain.PrecReviewApproved,
		domain.PrecCIPassed, domain.PrecCapacityAvailable,
	}
}

// actionRoles nennt die Rollen, die eine Aktion tragen darf. selection ist wahr, wenn domain.Decision.Check
// die Rolle der Auswahl gegen diese Liste prüft (concept_work, implementation, review); für die übrigen
// Aktionen gilt dieselbe Zuordnung als Regel der Plattform. Eine leere Liste heißt: die Aktion trägt keine
// Rolle (Frage an den Nutzer, Integration durch die Plattform, Warten).
func actionRoles(a domain.Action) (roles []domain.Role, selection bool) {
	switch a {
	case domain.ActionConceptWork:
		return []domain.Role{domain.RoleConcept}, true
	case domain.ActionImplementation:
		return []domain.Role{domain.RoleImplementer}, true
	case domain.ActionReview:
		return []domain.Role{domain.RoleReviewer, domain.RoleConceptReviewer}, true
	case domain.ActionPlanExtend:
		return []domain.Role{domain.RoleSupervisor}, false
	case domain.ActionRequestCompletion:
		return []domain.Role{domain.RoleCompletion}, false
	}
	return nil, false
}

// ---- Vertrag -----------------------------------------------------------------

// Input ist eine Eingabe eines Workflow-Skills.
type Input struct {
	Name        string `yaml:"name" json:"name"`
	Required    bool   `yaml:"required" json:"required"`
	Description string `yaml:"description" json:"description"`
}

// Scope ist der erlaubte Umfang: Fähigkeiten aus dem Vokabular der Regeldatei.
type Scope struct {
	WithinWorkOrder bool     `yaml:"within_work_order" json:"within_work_order"`
	Allowed         []string `yaml:"allowed" json:"allowed"`
	Forbidden       []string `yaml:"forbidden" json:"forbidden"`
}

// ResultSpec beschreibt das Ergebnisformat (Name aus der Regeldatei).
type ResultSpec struct {
	Format string `yaml:"format" json:"format"`
}

// Check ist eine Prüfung, die der Skill ausführt.
type Check struct {
	ID          string `yaml:"id" json:"id"`
	Description string `yaml:"description" json:"description"`
}

// Blocker ist eine Blockade: Bedingung, bei der der Skill stoppt, und das Ergebnis, das er dann meldet.
type Blocker struct {
	ID          string `yaml:"id" json:"id"`
	Description string `yaml:"description" json:"description"`
	Result      string `yaml:"result" json:"result"`
}

// Followup ist ein Folgeauftrag nach einem Ergebnis: Aktion, Zielrolle und Voraussetzungen.
type Followup struct {
	Action        domain.Action             `yaml:"action" json:"action"`
	Role          domain.Role               `yaml:"role" json:"role,omitempty"`
	Preconditions []domain.PreconditionKind `yaml:"preconditions" json:"preconditions,omitempty"`
}

// ResultContract beschreibt ein Ergebnis: optional das Urteil und die zulässigen Folgeaufträge.
type ResultContract struct {
	Verdict   domain.Verdict `yaml:"verdict" json:"verdict,omitempty"`
	Followups []Followup     `yaml:"followups" json:"followups"`
}

// Contract ist der Workflow-Vertrag eines Skills (contract.yaml).
type Contract struct {
	SchemaVersion      int                       `yaml:"schema_version" json:"schema_version"`
	Skill              string                    `yaml:"skill" json:"skill"`
	Role               domain.Role               `yaml:"role" json:"role"`
	UsesSkills         []string                  `yaml:"uses_skills" json:"uses_skills"`
	Inputs             []Input                   `yaml:"inputs" json:"inputs"`
	Preconditions      []domain.PreconditionKind `yaml:"preconditions" json:"preconditions"`
	Scope              Scope                     `yaml:"scope" json:"scope"`
	Result             ResultSpec                `yaml:"result" json:"result"`
	Checks             []Check                   `yaml:"checks" json:"checks"`
	Blockers           []Blocker                 `yaml:"blockers" json:"blockers"`
	CompletionCriteria []string                  `yaml:"completion_criteria" json:"completion_criteria"`
	Results            map[string]ResultContract `yaml:"results" json:"results"`
}

var contractRequiredKeys = []string{
	"schema_version", "skill", "role", "uses_skills", "inputs", "preconditions", "scope", "result",
	"checks", "blockers", "completion_criteria", "results",
}

func (c Contract) clone() Contract {
	c.UsesSkills = nonNil(slices.Clone(c.UsesSkills))
	c.Inputs = nonNil(slices.Clone(c.Inputs))
	c.Preconditions = nonNil(slices.Clone(c.Preconditions))
	c.Scope.Allowed = nonNil(slices.Clone(c.Scope.Allowed))
	c.Scope.Forbidden = nonNil(slices.Clone(c.Scope.Forbidden))
	c.Checks = nonNil(slices.Clone(c.Checks))
	c.Blockers = nonNil(slices.Clone(c.Blockers))
	c.CompletionCriteria = nonNil(slices.Clone(c.CompletionCriteria))
	res := make(map[string]ResultContract, len(c.Results))
	for k, v := range c.Results {
		fs := make([]Followup, len(v.Followups))
		for i, f := range v.Followups {
			f.Preconditions = nonNil(slices.Clone(f.Preconditions))
			fs[i] = f
		}
		v.Followups = fs
		res[k] = v
	}
	c.Results = res
	return c
}

// ---- Regeldatei --------------------------------------------------------------

type approvalDoc struct {
	Approver string        `yaml:"approver"`
	Action   domain.Action `yaml:"action"`
}

type criterionDoc struct {
	ID     string `yaml:"id"`
	Source string `yaml:"source"`
}

type stepDoc struct {
	Action   domain.Action             `yaml:"action"`
	Role     domain.Role               `yaml:"role"`
	Requires []domain.PreconditionKind `yaml:"requires"`
}

type resultDoc struct {
	Verdict domain.Verdict `yaml:"verdict"`
	Next    []stepDoc      `yaml:"next"`
}

type roleDoc struct {
	May     []string             `yaml:"may"`
	Format  string               `yaml:"format"`
	Results map[string]resultDoc `yaml:"results"`
}

type rulesDoc struct {
	SchemaVersion int `yaml:"schema_version"`
	Platform      struct {
		TargetBranch string   `yaml:"target_branch"`
		Protected    []string `yaml:"protected"`
	} `yaml:"platform"`
	Capabilities       []string                   `yaml:"capabilities"`
	AlwaysForbidden    []string                   `yaml:"always_forbidden"`
	WorkflowCategories map[Category][]domain.Role `yaml:"workflow_categories"`
	Formats            map[string]struct {
		Fields []string `yaml:"fields"`
	} `yaml:"formats"`
	ReviewBeforePush struct {
		VerdictRequired      domain.Verdict          `yaml:"verdict_required"`
		BoundTo              []string                `yaml:"bound_to"`
		IndependentExecution bool                    `yaml:"independent_execution"`
		RereviewAfterChange  bool                    `yaml:"rereview_after_change"`
		RequiredPrecondition domain.PreconditionKind `yaml:"required_precondition"`
		GuardedActions       []domain.Action         `yaml:"guarded_actions"`
	} `yaml:"review_before_push"`
	ConceptApproval struct {
		Routine           approvalDoc    `yaml:"routine"`
		Strategic         approvalDoc    `yaml:"strategic"`
		Unclassifiable    approvalDoc    `yaml:"unclassifiable"`
		BoundTo           string         `yaml:"bound_to"`
		StrategicCriteria []criterionDoc `yaml:"strategic_criteria"`
	} `yaml:"concept_approval"`
	RepairLoop struct {
		MaxRounds         int     `yaml:"max_rounds"`
		OnFindings        stepDoc `yaml:"on_findings"`
		OnConceptFindings stepDoc `yaml:"on_concept_findings"`
		OnExhausted       stepDoc `yaml:"on_exhausted"`
	} `yaml:"repair_loop"`
	Integration struct {
		Default                   domain.IntegrationMode `yaml:"default"`
		AutoRequiresExplicitOrder bool                   `yaml:"auto_requires_explicit_order"`
		WaitingState              domain.WorkOrderState  `yaml:"waiting_state"`
	} `yaml:"integration"`
	Completion struct {
		OpenPRCountsAsCompletion bool     `yaml:"open_pr_counts_as_completion"`
		Requires                 []string `yaml:"requires"`
		MilestoneAlsoRequires    []string `yaml:"milestone_also_requires"`
	} `yaml:"completion"`
	QuestionRoute struct {
		Hops              []string `yaml:"hops"`
		SilenceIsApproval bool     `yaml:"silence_is_approval"`
	} `yaml:"question_route"`
	Roles map[domain.Role]roleDoc `yaml:"roles"`
}

var rulesRequiredKeys = []string{
	"schema_version", "platform", "capabilities", "always_forbidden", "workflow_categories", "formats",
	"review_before_push", "concept_approval", "repair_loop", "integration", "completion", "question_route", "roles",
}

// Rules sind die geprüften Rollenregeln. Rules ist nach dem Laden unveränderlich; alle Rückgaben sind Kopien.
type Rules struct {
	doc rulesDoc
}

// LoadRules liest und prüft <root>/rules/roles.yaml. root ist der CCMAS-Softwareordner.
func LoadRules(root string) (*Rules, error) {
	r, findings := loadRules(root)
	if len(findings) > 0 {
		return nil, asError(findings)
	}
	return r, nil
}

func loadRules(root string) (*Rules, []*Error) {
	if root == "" {
		return nil, []*Error{newErr(CodeMissingRules, "", rulesRel, "Regeldatei %s: kein CCMAS-Softwareordner angegeben", rulesRel)}
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, []*Error{newErr(CodeMissingRules, "", rulesRel, "Regeldatei %s: Softwareordner %q nicht auflösbar: %v", rulesRel, root, err)}
	}
	path := filepath.Join(abs, filepath.FromSlash(rulesRel))
	fi, err := os.Lstat(path)
	switch {
	case err != nil:
		return nil, []*Error{newErr(CodeMissingRules, "", rulesRel, "Regeldatei %s nicht lesbar: %v", rulesRel, err)}
	case !fi.Mode().IsRegular():
		return nil, []*Error{newErr(CodeInvalidRules, "", rulesRel, "Regeldatei %s ist keine reguläre Datei (Symlinks sind nicht erlaubt)", rulesRel)}
	}
	raw, err := readLimited(path, maxRulesSize)
	if err != nil {
		return nil, []*Error{newErr(CodeMissingRules, "", rulesRel, "Regeldatei %s nicht lesbar: %v", rulesRel, err)}
	}
	r, fs := parseRules(raw)
	return r, fs
}

// ParseRules prüft den Inhalt einer Regeldatei (ohne Dateisystem).
func ParseRules(raw []byte) (*Rules, error) {
	r, findings := parseRules(raw)
	if len(findings) > 0 {
		return nil, asError(findings)
	}
	return r, nil
}

func parseRules(raw []byte) (*Rules, []*Error) {
	mk := func(code Code, format string, args ...any) *Error {
		return newErr(code, "", rulesRel, "Regeldatei %s: %s", rulesRel, fmt.Sprintf(format, args...))
	}
	root, err := parseYAMLNode(raw)
	if err != nil {
		return nil, []*Error{mk(CodeInvalidRules, "%v", err)}
	}
	if hits := scanPlatformKeys(root, "platform.target_branch"); len(hits) > 0 {
		out := make([]*Error, len(hits))
		for i, h := range hits {
			out[i] = mk(CodePlatformLimit, "%s: der Schlüssel %q setzt eine Plattformgrenze; Worker-Limit, Integrationsmodus, Policy-Profil, Berechtigungen und Zielbranch gehören nicht in die Regeln", h.path, h.key)
		}
		return nil, out
	}
	var doc rulesDoc
	if bad := nonIntegers(root, []string{"schema_version"}, []string{"repair_loop", "max_rounds"}); len(bad) > 0 {
		out := make([]*Error, len(bad))
		for i, p := range bad {
			out[i] = mk(CodeInvalidRules, "%s: muss eine ganze Zahl sein", p)
		}
		return nil, out
	}
	if err := decodeStrict(raw, root, &doc); err != nil {
		return nil, []*Error{mk(CodeInvalidRules, "%v", err)}
	}
	var out []*Error
	for _, k := range rulesRequiredKeys {
		if !hasKey(root, k) {
			out = append(out, mk(CodeInvalidRules, "%s: Abschnitt fehlt", k))
		}
	}
	if len(out) > 0 {
		return nil, out
	}
	for _, p := range validateRulesDoc(&doc) {
		out = append(out, mk(p.code, "%s", p.msg))
	}
	if len(out) > 0 {
		return nil, out
	}
	return &Rules{doc: doc}, nil
}

type problem struct {
	code Code
	msg  string
}

// validateRulesDoc prüft Inhalt und Querbezüge der Regeldatei und liefert alle Befunde.
func validateRulesDoc(d *rulesDoc) []problem {
	var out []problem
	bad := func(format string, args ...any) {
		out = append(out, problem{CodeInvalidRules, fmt.Sprintf(format, args...)})
	}
	limit := func(format string, args ...any) {
		out = append(out, problem{CodePlatformLimit, fmt.Sprintf(format, args...)})
	}

	if d.SchemaVersion != RulesSchemaVersion {
		bad("schema_version: %d wird nicht unterstützt (erwartet %d)", d.SchemaVersion, RulesSchemaVersion)
	}

	// Plattform-Invarianten.
	if d.Platform.TargetBranch != domain.BaseBranch {
		limit("platform.target_branch: muss %q sein (ist %q)", domain.BaseBranch, d.Platform.TargetBranch)
	}
	if !sameSet(d.Platform.Protected, protectedPlatformValues) {
		limit("platform.protected: muss genau %v nennen (ist %v)", protectedPlatformValues, d.Platform.Protected)
	}

	// Fähigkeiten.
	if !sameSet(d.Capabilities, knownCapabilities) || hasDuplicates(d.Capabilities) {
		bad("capabilities: muss genau %v nennen, ohne Wiederholung (ist %v)", knownCapabilities, d.Capabilities)
	}
	for _, a := range mandatoryAlwaysForbidden {
		if !slices.Contains(d.AlwaysForbidden, a) {
			bad("always_forbidden: muss %s enthalten", a)
		}
	}
	for _, a := range d.AlwaysForbidden {
		if !slices.Contains(knownCapabilities, a) {
			bad("always_forbidden: unbekannte Fähigkeit %q", a)
		}
	}

	// Workflow-Kategorien.
	for _, cat := range workflowCategoryNames {
		if _, ok := d.WorkflowCategories[cat]; !ok {
			bad("workflow_categories.%s: fehlt", cat)
		}
	}
	for cat, roles := range d.WorkflowCategories {
		if !slices.Contains(workflowCategoryNames, cat) {
			bad("workflow_categories.%s: unbekannte Kategorie (erlaubt: %v)", cat, workflowCategoryNames)
			continue
		}
		if len(roles) == 0 {
			bad("workflow_categories.%s: mindestens eine Rolle nötig", cat)
		}
		for _, r := range roles {
			if !r.Valid() {
				bad("workflow_categories.%s: unbekannte Rolle %q", cat, r)
			}
		}
	}

	// Ergebnisformate.
	for name, f := range d.Formats {
		if !identPattern.MatchString(name) {
			bad("formats.%s: ungültiger Name", name)
		}
		if len(f.Fields) == 0 || hasDuplicates(f.Fields) || slices.Contains(f.Fields, "") {
			bad("formats.%s.fields: mindestens ein Feld, eindeutig und nicht leer", name)
		}
	}
	if f, ok := d.Formats["review_report"]; ok && !sameSet(f.Fields, jsonFields(domain.ReviewReport{})) {
		bad("formats.review_report.fields: muss den Feldern von domain.ReviewReport entsprechen (%v)", jsonFields(domain.ReviewReport{}))
	}
	if f, ok := d.Formats["decision"]; ok {
		all := jsonFields(domain.Decision{})
		for _, fld := range f.Fields {
			if !slices.Contains(all, fld) {
				bad("formats.decision.fields: %q ist kein Feld von domain.Decision", fld)
			}
		}
	}

	// Review vor dem Push: feste Werte.
	rb := d.ReviewBeforePush
	if rb.VerdictRequired != domain.VerdictApproved {
		bad("review_before_push.verdict_required: muss %s sein", domain.VerdictApproved)
	}
	if !sameSet(rb.BoundTo, []string{"commit", "concept_version"}) {
		bad("review_before_push.bound_to: muss genau [commit, concept_version] sein")
	}
	if !rb.IndependentExecution {
		bad("review_before_push.independent_execution: muss true sein (Review nur von einer anderen, frischen Ausführung)")
	}
	if !rb.RereviewAfterChange {
		bad("review_before_push.rereview_after_change: muss true sein (jede weitere Änderung braucht ein neues Review)")
	}
	if rb.RequiredPrecondition != domain.PrecReviewApproved {
		bad("review_before_push.required_precondition: muss %s sein", domain.PrecReviewApproved)
	}
	if !slices.Contains(rb.GuardedActions, domain.ActionRequestIntegration) {
		bad("review_before_push.guarded_actions: muss %s enthalten", domain.ActionRequestIntegration)
	}
	for _, a := range rb.GuardedActions {
		if !a.Valid() {
			bad("review_before_push.guarded_actions: unbekannte Aktion %q", a)
		}
	}

	// Konzeptfreigabe.
	ca := d.ConceptApproval
	if ca.Routine.Approver != string(domain.RoleConceptReviewer) || ca.Routine.Action != domain.ActionReview {
		bad("concept_approval.routine: Routinekonzepte prüft der Konzeptprüfer (approver concept_reviewer, action review)")
	}
	if ca.Strategic.Approver != "user" || ca.Strategic.Action != domain.ActionAskQuestion {
		bad("concept_approval.strategic: strategische Konzepte entscheidet der Nutzer (approver user, action ask_question)")
	}
	if ca.Unclassifiable.Approver != "user" || ca.Unclassifiable.Action != domain.ActionAskQuestion {
		bad("concept_approval.unclassifiable: nicht einstufbare Konzepte entscheidet der Nutzer (approver user, action ask_question)")
	}
	if ca.BoundTo != "concept_version" {
		bad("concept_approval.bound_to: die Freigabe gilt für eine Konzeptversion (concept_version)")
	}
	if len(ca.StrategicCriteria) == 0 {
		bad("concept_approval.strategic_criteria: mindestens ein Kriterium nötig")
	}
	seenCrit := map[string]bool{}
	for i, c := range ca.StrategicCriteria {
		if !identPattern.MatchString(c.ID) || seenCrit[c.ID] || strings.TrimSpace(c.Source) == "" {
			bad("concept_approval.strategic_criteria[%d]: id (eindeutig, a-z0-9_) und source nötig", i)
		}
		seenCrit[c.ID] = true
	}

	// Reparaturschleife.
	rl := d.RepairLoop
	if rl.MaxRounds < 1 || rl.MaxRounds > maxRepairRounds {
		bad("repair_loop.max_rounds: muss zwischen 1 und %d liegen (ist %d)", maxRepairRounds, rl.MaxRounds)
	}
	if rl.OnFindings.Action != domain.ActionImplementation || rl.OnFindings.Role != domain.RoleImplementer {
		bad("repair_loop.on_findings: Befunde gehen als Implementierung an den implementer zurück")
	}
	if rl.OnConceptFindings.Action != domain.ActionConceptWork || rl.OnConceptFindings.Role != domain.RoleConcept {
		bad("repair_loop.on_concept_findings: Befunde der Konzeptprüfung gehen als Konzeptarbeit an den concept-Agenten zurück (action concept_work, role concept)")
	}
	if rl.OnExhausted.Action != domain.ActionAskQuestion || rl.OnExhausted.Role != "" {
		bad("repair_loop.on_exhausted: nach dem Budget folgt ein sichtbarer Halt mit Frage an den Nutzer (action ask_question)")
	}

	// Integration.
	if d.Integration.Default != domain.ModePR {
		bad("integration.default: der Standard ist pr")
	}
	if !d.Integration.AutoRequiresExplicitOrder {
		bad("integration.auto_requires_explicit_order: muss true sein (Auto nur ausdrücklich je Auftrag)")
	}
	if d.Integration.WaitingState != domain.WOWaitingIntegration {
		bad("integration.waiting_state: ein offener PR ist der Zustand %s", domain.WOWaitingIntegration)
	}

	// Abschluss.
	if d.Completion.OpenPRCountsAsCompletion {
		bad("completion.open_pr_counts_as_completion: muss false sein (offener PR ist nie Abschluss)")
	}
	if !sameSet(d.Completion.Requires, featureEvidence) || hasDuplicates(d.Completion.Requires) {
		bad("completion.requires: muss genau %v nennen", featureEvidence)
	}
	if !sameSet(d.Completion.MilestoneAlsoRequires, milestoneEvidence) || hasDuplicates(d.Completion.MilestoneAlsoRequires) {
		bad("completion.milestone_also_requires: muss genau %v nennen", milestoneEvidence)
	}

	// Fragenweg.
	if !slices.Equal(d.QuestionRoute.Hops, questionHops) {
		bad("question_route.hops: muss %v sein (ist %v)", questionHops, d.QuestionRoute.Hops)
	}
	if d.QuestionRoute.SilenceIsApproval {
		bad("question_route.silence_is_approval: muss false sein (keine Antwort ist keine Freigabe)")
	}

	// Rollen.
	known := domain.Roles()
	for _, r := range known {
		if _, ok := d.Roles[r]; !ok {
			bad("roles.%s: fehlt", r)
		}
	}
	for r := range d.Roles {
		if !r.Valid() {
			bad("roles.%s: unbekannte Rolle", r)
		}
	}
	for _, cat := range workflowCategoryNames {
		for _, r := range d.WorkflowCategories[cat] {
			if _, ok := d.Roles[r]; r.Valid() && !ok {
				bad("workflow_categories.%s: Rolle %s hat keinen Abschnitt unter roles", cat, r)
			}
		}
	}
	roleNames := make([]string, 0, len(d.Roles))
	for r := range d.Roles {
		roleNames = append(roleNames, string(r))
	}
	sort.Strings(roleNames)
	for _, rn := range roleNames {
		role := domain.Role(rn)
		rd := d.Roles[role]
		at := "roles." + rn
		for _, c := range rd.May {
			switch {
			case !slices.Contains(knownCapabilities, c):
				bad("%s.may: unbekannte Fähigkeit %q", at, c)
			case slices.Contains(d.AlwaysForbidden, c):
				bad("%s.may: %q ist für alle Rollen verboten (always_forbidden)", at, c)
			}
		}
		if hasDuplicates(rd.May) {
			bad("%s.may: Fähigkeit mehrfach genannt", at)
		}
		if _, ok := d.Formats[rd.Format]; !ok {
			bad("%s.format: unbekanntes Ergebnisformat %q", at, rd.Format)
		}
		if len(rd.Results) == 0 {
			bad("%s.results: mindestens ein Ergebnis nötig", at)
		}
		resNames := make([]string, 0, len(rd.Results))
		for n := range rd.Results {
			resNames = append(resNames, n)
		}
		sort.Strings(resNames)
		for _, rn := range resNames {
			res := rd.Results[rn]
			rat := at + ".results." + rn
			if !identPattern.MatchString(rn) {
				bad("%s: ungültiger Ergebnisname", rat)
			}
			if res.Verdict != "" {
				if !slices.Contains(domain.Verdicts(), res.Verdict) {
					bad("%s.verdict: unbekanntes Urteil %q", rat, res.Verdict)
				} else if rd.Format != "review_report" {
					bad("%s.verdict: Urteile gibt es nur bei Rollen mit dem Ergebnisformat review_report", rat)
				}
			}
			if len(res.Next) == 0 {
				bad("%s.next: mindestens ein Folgeschritt nötig (keine Sackgasse)", rat)
			}
			for i, s := range res.Next {
				for _, msg := range d.checkStep(s) {
					bad("%s.next[%d]: %s", rat, i, msg)
				}
			}
		}
	}
	if len(out) == 0 {
		out = append(out, d.deadEnds()...)
	}
	return out
}

// checkStep prüft einen Folgeschritt: Aktion, Rolle zur Aktion, Voraussetzungen, Schutz der Integration.
func (d *rulesDoc) checkStep(s stepDoc) []string {
	var out []string
	if !s.Action.Valid() {
		return []string{fmt.Sprintf("unbekannte Aktion %q", s.Action)}
	}
	roles, _ := actionRoles(s.Action)
	switch {
	case len(roles) == 0 && s.Role != "":
		out = append(out, fmt.Sprintf("die Aktion %s trägt keine Rolle (ist %q)", s.Action, s.Role))
	case len(roles) > 0 && !slices.Contains(roles, s.Role):
		out = append(out, fmt.Sprintf("Rolle %q passt nicht zur Aktion %s (erlaubt: %v)", s.Role, s.Action, roles))
	}
	for _, p := range s.Requires {
		if !slices.Contains(preconditionKinds(), p) {
			out = append(out, fmt.Sprintf("unbekannte Voraussetzung %q", p))
		}
	}
	if slices.Contains(d.ReviewBeforePush.GuardedActions, s.Action) && !slices.Contains(s.Requires, d.ReviewBeforePush.RequiredPrecondition) {
		out = append(out, fmt.Sprintf("%s verlangt die Voraussetzung %s (review_before_push.guarded_actions)", s.Action, d.ReviewBeforePush.RequiredPrecondition))
	}
	return out
}

// deadEnds meldet Ergebnisse, von denen aus über die Folgeschritte nie ein pausierender Schritt
// (Frage an den Nutzer, Warten, Integration) erreichbar ist.
func (d *rulesDoc) deadEnds() []problem {
	type node struct {
		role domain.Role
		res  string
	}
	var nodes []node
	for r, rd := range d.Roles {
		for n := range rd.Results {
			nodes = append(nodes, node{r, n})
		}
	}
	reaches := map[node]bool{}
	for changed := true; changed; {
		changed = false
		for _, n := range nodes {
			if reaches[n] {
				continue
			}
			for _, s := range d.Roles[n.role].Results[n.res].Next {
				ok := s.Action == domain.ActionAskQuestion || s.Action == domain.ActionWait || s.Action == domain.ActionRequestIntegration
				if !ok && s.Role != "" {
					for next := range d.Roles[s.Role].Results {
						ok = ok || reaches[node{s.Role, next}]
					}
				}
				if ok {
					reaches[n], changed = true, true
					break
				}
			}
		}
	}
	var out []problem
	for _, n := range nodes {
		if !reaches[n] {
			out = append(out, problem{CodeInvalidRules, fmt.Sprintf("roles.%s.results.%s: Sackgasse, über die Folgeschritte folgt nie Warten, eine Frage an den Nutzer oder die Integration", n.role, n.res)})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].msg < out[j].msg })
	return out
}

// maxRepairRounds begrenzt das konfigurierbare Reparaturbudget.
const maxRepairRounds = 10

// ---- Zugriff auf die Regeln ----------------------------------------------------

// Roles liefert alle Rollen mit Regeln, aufsteigend.
func (r *Rules) Roles() []domain.Role {
	out := make([]domain.Role, 0, len(r.doc.Roles))
	for k := range r.doc.Roles {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Capabilities liefert das Vokabular für scope.allowed und scope.forbidden.
func (r *Rules) Capabilities() []string { return slices.Clone(r.doc.Capabilities) }

// WorkflowCategories liefert die Kategorien, deren Skills einen Vertrag brauchen, aufsteigend.
func (r *Rules) WorkflowCategories() []Category {
	out := make([]Category, 0, len(r.doc.WorkflowCategories))
	for k := range r.doc.WorkflowCategories {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// IsWorkflowCategory meldet, ob Skills der Kategorie einen Vertrag brauchen.
func (r *Rules) IsWorkflowCategory(c Category) bool {
	_, ok := r.doc.WorkflowCategories[c]
	return ok
}

// ResultIDs liefert die Ergebnisse einer Rolle, aufsteigend (leer bei unbekannter Rolle).
func (r *Rules) ResultIDs(role domain.Role) []string {
	rd, ok := r.doc.Roles[role]
	if !ok {
		return nil
	}
	out := make([]string, 0, len(rd.Results))
	for k := range rd.Results {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ResultFormat liefert den Namen des Ergebnisformats einer Rolle ("" bei unbekannter Rolle).
func (r *Rules) ResultFormat(role domain.Role) string { return r.doc.Roles[role].Format }

// ResultVerdict liefert das Urteil, das zu einem Ergebnis gehört ("" ohne Urteil oder bei Unbekanntem).
func (r *Rules) ResultVerdict(role domain.Role, result string) domain.Verdict {
	return r.doc.Roles[role].Results[result].Verdict
}

// FormatFields liefert die Pflichtfelder eines Ergebnisformats.
func (r *Rules) FormatFields(format string) []string {
	return slices.Clone(r.doc.Formats[format].Fields)
}

// MayDo liefert die Fähigkeiten, die ein Vertrag der Rolle höchstens erlauben darf.
func (r *Rules) MayDo(role domain.Role) []string { return slices.Clone(r.doc.Roles[role].May) }

// TargetBranch liefert den Zielbranch der Plattform (immer develop).
func (r *Rules) TargetBranch() string { return r.doc.Platform.TargetBranch }

// ProtectedPlatformValues liefert die Plattformwerte, die Skills und Verträge nicht ändern, aufsteigend.
func (r *Rules) ProtectedPlatformValues() []string {
	out := slices.Clone(r.doc.Platform.Protected)
	sort.Strings(out)
	return out
}

// GuardedActions liefert die Aktionen, die nur mit gültigem Review erlaubt sind.
func (r *Rules) GuardedActions() []domain.Action {
	return slices.Clone(r.doc.ReviewBeforePush.GuardedActions)
}

// QuestionRoute liefert den Fragenweg Worker, Supervisor, Zentrale, Supervisor, Worker.
// Ohne geladene Regeln liefert es nil.
func (r *Rules) QuestionRoute() []string {
	if r == nil {
		return nil
	}
	return slices.Clone(r.doc.QuestionRoute.Hops)
}

// SilenceIsApproval ist immer false: keine Antwort ist keine Freigabe.
func (r *Rules) SilenceIsApproval() bool { return r != nil && r.doc.QuestionRoute.SilenceIsApproval }

// DefaultIntegrationMode liefert den Standardmodus (pr).
func (r *Rules) DefaultIntegrationMode() domain.IntegrationMode { return r.doc.Integration.Default }

// StrategicCriteria liefert die Kennungen der Kriterien für „strategisch“.
func (r *Rules) StrategicCriteria() []string {
	out := make([]string, len(r.doc.ConceptApproval.StrategicCriteria))
	for i, c := range r.doc.ConceptApproval.StrategicCriteria {
		out[i] = c.ID
	}
	return out
}

// ---- YAML-Hilfen ----------------------------------------------------------------

// parseYAMLNode liest genau ein YAML-Dokument als Baum; leer, mehrere Dokumente und Syntaxfehler sind Fehler.
func parseYAMLNode(raw []byte) (*yaml.Node, error) {
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	var n yaml.Node
	if err := dec.Decode(&n); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errors.New("Datei ist leer")
		}
		return nil, fmt.Errorf("kein gültiges YAML: %v", err)
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("Datei enthält mehr als ein YAML-Dokument")
	}
	if n.Kind != yaml.DocumentNode || len(n.Content) != 1 || n.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("die Datei muss ein YAML-Objekt sein")
	}
	return n.Content[0], nil
}

// decodeStrict dekodiert streng (unbekannte und doppelte Schlüssel sind Fehler). Fehlermeldungen von
// yaml.v3 tragen nur eine Zeilennummer; sie wird über den Baum auf den Schlüsselpfad abgebildet.
func decodeStrict(raw []byte, root *yaml.Node, out any) error {
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	err := dec.Decode(out)
	if err == nil {
		return nil
	}
	paths := lineIndex(root)
	var te *yaml.TypeError
	if errors.As(err, &te) {
		msgs := make([]string, len(te.Errors))
		for i, m := range te.Errors {
			msgs[i] = withPath(m, paths)
		}
		return errors.New(strings.Join(msgs, "; "))
	}
	return errors.New(withPath(err.Error(), paths))
}

func withPath(msg string, paths map[int]string) string {
	if m := yamlLineErr.FindStringSubmatch(msg); m != nil {
		line, _ := strconv.Atoi(m[1])
		if p, ok := paths[line]; ok {
			return fmt.Sprintf("%s (Zeile %d): %s", p, line, m[2])
		}
	}
	return msg
}

// lineIndex bildet Zeilennummern auf den Pfad des ersten dort beginnenden Knotens ab.
func lineIndex(root *yaml.Node) map[int]string {
	idx := map[int]string{}
	var walk func(n *yaml.Node, path string)
	rec := func(line int, path string) {
		if _, ok := idx[line]; !ok && path != "" {
			idx[line] = path
		}
	}
	walk = func(n *yaml.Node, path string) {
		switch n.Kind {
		case yaml.MappingNode:
			for i := 0; i+1 < len(n.Content); i += 2 {
				k, v := n.Content[i], n.Content[i+1]
				p := joinPath(path, k.Value)
				rec(k.Line, p)
				walk(v, p)
			}
		case yaml.SequenceNode:
			for i, c := range n.Content {
				p := fmt.Sprintf("%s[%d]", path, i)
				rec(c.Line, p)
				walk(c, p)
			}
		}
	}
	walk(root, "")
	return idx
}

func joinPath(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

type platformHit struct{ path, key string }

// scanPlatformKeys findet Schlüssel, die eine Plattformgrenze setzen würden. allowed ist ein Pfad, an dem
// target_branch erlaubt ist (leer: nirgends).
func scanPlatformKeys(root *yaml.Node, allowed string) []platformHit {
	var hits []platformHit
	var walk func(n *yaml.Node, path string)
	walk = func(n *yaml.Node, path string) {
		switch n.Kind {
		case yaml.MappingNode:
			for i := 0; i+1 < len(n.Content); i += 2 {
				k := n.Content[i]
				p := joinPath(path, k.Value)
				if platformKeys[strings.ToLower(k.Value)] && !(allowed != "" && p == allowed) {
					hits = append(hits, platformHit{p, k.Value})
				}
				walk(n.Content[i+1], p)
			}
		case yaml.SequenceNode:
			for i, c := range n.Content {
				walk(c, fmt.Sprintf("%s[%d]", path, i))
			}
		}
	}
	walk(root, "")
	return hits
}

// nonIntegers meldet Pfade (Schlüsselfolgen), deren Wert vorhanden, aber keine ganze Zahl ist. yaml.v3
// nimmt sonst auch 2.5 oder 1.0 für ein int-Feld an.
func nonIntegers(root *yaml.Node, paths ...[]string) []string {
	var out []string
	for _, path := range paths {
		n := root
		for _, k := range path {
			var next *yaml.Node
			if n.Kind == yaml.MappingNode {
				for i := 0; i+1 < len(n.Content); i += 2 {
					if n.Content[i].Value == k {
						next = n.Content[i+1]
					}
				}
			}
			if n = next; n == nil {
				break
			}
		}
		if n != nil && (n.Kind != yaml.ScalarNode || n.ShortTag() != "!!int") {
			out = append(out, strings.Join(path, "."))
		}
	}
	return out
}

func hasKey(root *yaml.Node, key string) bool {
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == key {
			return true
		}
	}
	return false
}

func sameSet(a, b []string) bool {
	x, y := slices.Clone(a), slices.Clone(b)
	sort.Strings(x)
	sort.Strings(y)
	x, y = slices.Compact(x), slices.Compact(y)
	return slices.Equal(x, y)
}

func hasDuplicates(a []string) bool {
	seen := map[string]bool{}
	for _, v := range a {
		if seen[v] {
			return true
		}
		seen[v] = true
	}
	return false
}

// jsonFields liefert die JSON-Feldnamen eines Struct-Typs, aufsteigend.
func jsonFields(v any) []string {
	rt := reflect.TypeOf(v)
	var out []string
	for i := 0; i < rt.NumField(); i++ {
		name, _, _ := strings.Cut(rt.Field(i).Tag.Get("json"), ",")
		if name != "" && name != "-" {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}
