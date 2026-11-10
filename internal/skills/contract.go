package skills

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/alexwenzel/ccmas/internal/domain"
)

// Dieser Teil prüft die Workflow-Verträge (contract.yaml) in zwei getrennten Stufen:
//
//  1. Beim Laden des Katalogs (Load): jeder vorhandene Vertrag wird streng gelesen und in sich und gegen
//     internal/domain geprüft (Schema, unbekannte Schlüssel, Plattformschlüssel in jeder Tiefe, Aktionen,
//     Rollen, Voraussetzungen, Urteile) sowie gegen den eigenen Manifest-Eintrag (Skill-ID, Rolle). Dazu
//     braucht es weder die Regeldatei noch das Vorhandensein von Verträgen.
//  2. In ValidateWorkflow: Vertragspflicht je Workflow-Kategorie und alle Querbezüge, die die Rollenregeln
//     (rules/roles.yaml) oder andere Skills des Katalogs brauchen.
//
// So lässt sich ein synthetischer Katalog ohne Regeln und ohne Verträge laden; die Plattformregeln prüft,
// wer sie braucht (ccmas skills validate, die Tests gegen den echten Bestand, jeder Aufrufer der Auswerter).

// ---- Stufe 1: Vertrag in sich ------------------------------------------------

// parseContractShape liest den Inhalt einer contract.yaml streng und prüft ihn in sich (ohne Regeldatei und
// ohne Katalog).
//
// decoded ist wahr, wenn der Vertrag lesbar war; ct ist dann auch bei Befunden gefüllt, damit die
// übrigen Bezüge trotzdem geprüft werden.
func parseContractShape(raw []byte) (ct Contract, decoded bool, out []problem) {
	root, err := parseYAMLNode(raw)
	if err != nil {
		return Contract{}, false, []problem{{CodeInvalidContract, err.Error()}}
	}
	if hits := scanPlatformKeys(root, ""); len(hits) > 0 {
		for _, h := range hits {
			out = append(out, problem{CodePlatformLimit, fmt.Sprintf("%s: der Schlüssel %q setzt oder erweitert eine Plattformgrenze; Worker-Limit, Integrationsmodus, Policy-Profil, Berechtigungen und Zielbranch kann ein Vertrag nicht ändern", h.path, h.key)})
		}
		return Contract{}, false, out
	}
	for _, p := range nonIntegers(root, []string{"schema_version"}) {
		out = append(out, problem{CodeInvalidContract, p + ": muss eine ganze Zahl sein"})
	}
	if len(out) > 0 {
		return Contract{}, false, out
	}
	if err := decodeStrict(raw, root, &ct); err != nil {
		return Contract{}, false, []problem{{CodeInvalidContract, err.Error()}}
	}
	for _, k := range contractRequiredKeys {
		if !hasKey(root, k) {
			out = append(out, problem{CodeInvalidContract, fmt.Sprintf("%s: Schlüssel fehlt", k)})
		}
	}
	if len(out) > 0 {
		return ct, true, out
	}
	for _, m := range checkContractShape(ct) {
		out = append(out, problem{CodeInvalidContract, m})
	}
	return ct.clone(), true, out
}

// checkContractShape prüft einen dekodierten Vertrag in sich und gegen internal/domain; es liefert alle Befunde.
// Es verwendet nur feste Vokabulare des Pakets, nie die Regeldatei.
func checkContractShape(ct Contract) []string {
	var out []string
	bad := func(format string, args ...any) { out = append(out, fmt.Sprintf(format, args...)) }

	if ct.SchemaVersion != ContractSchemaVersion {
		bad("schema_version: %d wird nicht unterstützt (erwartet %d)", ct.SchemaVersion, ContractSchemaVersion)
	}
	if !skillIDPattern.MatchString(ct.Skill) {
		bad("skill: %q ist keine gültige Skill-ID", ct.Skill)
	}
	if !ct.Role.Valid() {
		bad("role: unbekannte Rolle %q", ct.Role)
	}
	for i, u := range ct.UsesSkills {
		if !skillIDPattern.MatchString(u) {
			bad("uses_skills[%d]: %q ist keine gültige Skill-ID", i, u)
		}
	}
	if hasDuplicates(ct.UsesSkills) {
		bad("uses_skills: Skill mehrfach genannt")
	}

	if len(ct.Inputs) == 0 {
		bad("inputs: mindestens eine Eingabe nötig")
	}
	seenIn := map[string]bool{}
	for i, in := range ct.Inputs {
		if !identPattern.MatchString(in.Name) || seenIn[in.Name] || strings.TrimSpace(in.Description) == "" {
			bad("inputs[%d]: name (eindeutig, a-z0-9_) und description nötig", i)
		}
		seenIn[in.Name] = true
	}
	seenPre := map[domain.PreconditionKind]bool{}
	for i, p := range ct.Preconditions {
		if !slices.Contains(preconditionKinds(), p) || seenPre[p] {
			bad("preconditions[%d]: %q ist keine bekannte oder eine doppelte Voraussetzungsart", i, p)
		}
		seenPre[p] = true
	}

	// Umfang: Vokabular und Pflichtverbote sind fest; welche Fähigkeiten eine Rolle höchstens haben darf, sagen die Rollenregeln.
	if !ct.Scope.WithinWorkOrder {
		bad("scope.within_work_order: muss true sein (ein Skill erweitert den Nutzerauftrag nicht)")
	}
	if len(ct.Scope.Allowed) == 0 {
		bad("scope.allowed: mindestens eine erlaubte Fähigkeit nötig")
	}
	if len(ct.Scope.Forbidden) == 0 {
		bad("scope.forbidden: mindestens eine verbotene Fähigkeit nötig")
	}
	for _, a := range ct.Scope.Allowed {
		switch {
		case !slices.Contains(knownCapabilities, a):
			bad("scope.allowed: unbekannte Fähigkeit %q", a)
		case slices.Contains(mandatoryAlwaysForbidden, a):
			bad("scope.allowed: %q ist für alle Rollen verboten", a)
		}
		if slices.Contains(ct.Scope.Forbidden, a) {
			bad("scope: Fähigkeit %q ist zugleich erlaubt und verboten", a)
		}
	}
	for _, f := range ct.Scope.Forbidden {
		if !slices.Contains(knownCapabilities, f) {
			bad("scope.forbidden: unbekannte Fähigkeit %q", f)
		}
	}
	for _, m := range mandatoryAlwaysForbidden {
		if !slices.Contains(ct.Scope.Forbidden, m) {
			bad("scope.forbidden: muss %q ausdrücklich verbieten", m)
		}
	}
	if hasDuplicates(ct.Scope.Allowed) {
		bad("scope.allowed: Fähigkeit mehrfach genannt")
	}
	if hasDuplicates(ct.Scope.Forbidden) {
		bad("scope.forbidden: Fähigkeit mehrfach genannt")
	}

	if !identPattern.MatchString(ct.Result.Format) {
		bad("result.format: ungültiger Formatname %q", ct.Result.Format)
	}

	if len(ct.Checks) == 0 {
		bad("checks: mindestens eine Prüfung nötig")
	}
	seenCheck := map[string]bool{}
	for i, c := range ct.Checks {
		if !identPattern.MatchString(c.ID) || seenCheck[c.ID] || strings.TrimSpace(c.Description) == "" {
			bad("checks[%d]: id (eindeutig, a-z0-9_) und description nötig", i)
		}
		seenCheck[c.ID] = true
	}
	if len(ct.Blockers) == 0 {
		bad("blockers: mindestens eine Blockade nötig")
	}
	seenBlock := map[string]bool{}
	for i, b := range ct.Blockers {
		if !identPattern.MatchString(b.ID) || seenBlock[b.ID] || strings.TrimSpace(b.Description) == "" {
			bad("blockers[%d]: id (eindeutig, a-z0-9_) und description nötig", i)
		}
		seenBlock[b.ID] = true
		if _, ok := ct.Results[b.Result]; !ok {
			bad("blockers[%d].result: %q ist kein Ergebnis dieses Vertrags", i, b.Result)
		}
	}
	if len(ct.CompletionCriteria) == 0 {
		bad("completion_criteria: mindestens ein Abschlusskriterium nötig")
	}
	for i, c := range ct.CompletionCriteria {
		if strings.TrimSpace(c) == "" {
			bad("completion_criteria[%d]: darf nicht leer sein", i)
		}
	}

	// Ergebnisse und Folgeaufträge: Aktion, Rolle zur Aktion und Voraussetzungen gegen internal/domain.
	if len(ct.Results) == 0 {
		bad("results: mindestens ein Ergebnis nötig")
	}
	names := make([]string, 0, len(ct.Results))
	for n := range ct.Results {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, res := range names {
		rc := ct.Results[res]
		at := "results." + res
		if !identPattern.MatchString(res) {
			bad("%s: ungültiger Ergebnisname", at)
		}
		if rc.Verdict != "" && !slices.Contains(domain.Verdicts(), rc.Verdict) {
			bad("%s.verdict: unbekanntes Urteil %q", at, rc.Verdict)
		}
		if len(rc.Followups) == 0 {
			bad("%s.followups: mindestens ein Folgeauftrag nötig", at)
		}
		for i, f := range rc.Followups {
			fat := fmt.Sprintf("%s.followups[%d]", at, i)
			if !f.Action.Valid() {
				bad("%s: unbekannte Aktion %q", fat, f.Action)
				continue
			}
			roles, _ := actionRoles(f.Action)
			switch {
			case len(roles) == 0 && f.Role != "":
				bad("%s: die Aktion %s trägt keine Rolle (ist %q)", fat, f.Action, f.Role)
			case len(roles) > 0 && !slices.Contains(roles, f.Role):
				bad("%s: Rolle %q passt nicht zur Aktion %s (erlaubt: %v)", fat, f.Role, f.Action, roles)
			}
			seen := map[domain.PreconditionKind]bool{}
			for _, p := range f.Preconditions {
				if !slices.Contains(preconditionKinds(), p) || seen[p] {
					bad("%s: Voraussetzung %q ist unbekannt oder doppelt", fat, p)
				}
				seen[p] = true
			}
			// Fester Kern der Plattform: Integration (Push) nur mit gültigem Review.
			if f.Action == domain.ActionRequestIntegration && !slices.Contains(f.Preconditions, domain.PrecReviewApproved) {
				bad("%s: %s braucht die Voraussetzung %s (Review vor dem Push)", fat, f.Action, domain.PrecReviewApproved)
			}
		}
	}
	return out
}

// ---- Stufe 2: Vertrag gegen Regeln und Katalog ------------------------------------

// checkContractRules prüft einen (in sich gültigen) Vertrag gegen die Rollenregeln und liefert alle Befunde.
func (r *Rules) checkContractRules(ct Contract) []string {
	var out []string
	bad := func(format string, args ...any) { out = append(out, fmt.Sprintf(format, args...)) }
	d := &r.doc

	rd, roleOK := d.Roles[ct.Role]
	if !roleOK {
		bad("role: die Rolle %q hat keine Rollenregeln", ct.Role)
		return out
	}

	// Umfang.
	for _, a := range ct.Scope.Allowed {
		switch {
		case !slices.Contains(d.Capabilities, a):
			bad("scope.allowed: unbekannte Fähigkeit %q", a)
		case slices.Contains(d.AlwaysForbidden, a):
			bad("scope.allowed: %q ist für alle Rollen verboten", a)
		case !slices.Contains(rd.May, a):
			bad("scope.allowed: %q ist für die Rolle %s nicht zulässig (erlaubt: %v)", a, ct.Role, rd.May)
		}
	}
	for _, f := range ct.Scope.Forbidden {
		if !slices.Contains(d.Capabilities, f) {
			bad("scope.forbidden: unbekannte Fähigkeit %q", f)
		}
	}
	for _, c := range d.Capabilities {
		if !slices.Contains(rd.May, c) && !slices.Contains(ct.Scope.Forbidden, c) {
			bad("scope.forbidden: muss %q ausdrücklich verbieten (die Rolle %s darf es nicht)", c, ct.Role)
		}
	}

	if ct.Result.Format != rd.Format {
		bad("result.format: die Rolle %s liefert %q (ist %q)", ct.Role, rd.Format, ct.Result.Format)
	}

	// Ergebnisse und Folgeaufträge: genau die Ergebnisse der Rolle, Folgeaufträge nur aus den Rollenregeln.
	for res := range rd.Results {
		if _, ok := ct.Results[res]; !ok {
			bad("results.%s: Ergebnis der Rolle %s fehlt", res, ct.Role)
		}
	}
	names := make([]string, 0, len(ct.Results))
	for n := range ct.Results {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, res := range names {
		rc := ct.Results[res]
		at := "results." + res
		ruleRes, ok := rd.Results[res]
		if !ok {
			bad("%s: kein Ergebnis der Rolle %s", at, ct.Role)
			continue
		}
		if rc.Verdict != ruleRes.Verdict {
			bad("%s.verdict: die Rollenregeln verlangen %q (ist %q)", at, ruleRes.Verdict, rc.Verdict)
		}
		for i, f := range rc.Followups {
			fat := fmt.Sprintf("%s.followups[%d]", at, i)
			bounds := slices.IndexFunc(ruleRes.Next, func(s stepDoc) bool { return s.Action == f.Action && s.Role == f.Role })
			if bounds < 0 {
				bad("%s: %s/%s steht für %s/%s nicht in den Rollenregeln", fat, f.Action, f.Role, ct.Role, res)
				continue
			}
			for _, need := range ruleRes.Next[bounds].Requires {
				if !slices.Contains(f.Preconditions, need) {
					bad("%s: fehlende Voraussetzung %s (laut Rollenregeln)", fat, need)
				}
			}
			if slices.Contains(d.ReviewBeforePush.GuardedActions, f.Action) && !slices.Contains(f.Preconditions, d.ReviewBeforePush.RequiredPrecondition) {
				bad("%s: %s braucht die Voraussetzung %s (review_before_push)", fat, f.Action, d.ReviewBeforePush.RequiredPrecondition)
			}
		}
	}
	return out
}

// ValidateWorkflow prüft den Katalog gegen die Plattformregeln. Es ist die zweite, getrennte Stufe nach Load:
//
//   - Vertragspflicht je Workflow-Kategorie (missing_contract), kein Vertrag bei Hilfsskills (invalid_contract),
//   - jeder Vertrag gegen die Rollenregeln: Rolle und Kategorie, Umfang gegen die Fähigkeiten der Rolle,
//     Ergebnisformat, genau die Ergebnisse der Rolle, Folgeaufträge als Auswahl aus den Folgeschritten,
//     geschützte Aktionen (guarded_actions) nur mit review_approved,
//   - Bezüge zum Katalog: uses_skills existieren und erlauben die Rolle, Schreibfähigkeiten stehen in
//     external_writes des Manifests,
//   - fehlende Regeln (rules == nil) sind missing_rules: ohne Regeln entscheidet die Plattform nichts.
//
// Load prüft die Verträge nur in sich; erst ValidateWorkflow macht aus einem geladenen Katalog einen Bestand,
// auf dem die Plattformregeln gelten. Alle Befunde werden zusammen geliefert.
func ValidateWorkflow(c *Catalog, rules *Rules) error {
	return asError(validateWorkflow(c, rules))
}

func validateWorkflow(c *Catalog, rules *Rules) []*Error {
	if rules == nil {
		return []*Error{newErr(CodeMissingRules, "", rulesRel, "Regeldatei %s nicht geladen: ohne Rollenregeln entscheidet die Plattform nichts (fail-safe)", rulesRel)}
	}
	if c == nil {
		return []*Error{newErr(CodeInvalidRequest, "", "", "kein Skill-Katalog zum Prüfen übergeben")}
	}
	var out []*Error
	for _, sk := range c.skills { // aufsteigend nach ID
		id := sk.ID
		has := c.hasContract[id]
		wf := rules.IsWorkflowCategory(sk.Category)
		mk := func(code Code, format string, args ...any) *Error {
			return newErr(code, id, contractFile, "Vertrag %s/%s: %s", id, contractFile, fmt.Sprintf(format, args...))
		}
		switch {
		case wf && !has:
			out = append(out, mk(CodeMissingContract, "Workflow-Skill der Kategorie %s braucht eine %s", sk.Category, contractFile))
			continue
		case !wf && has:
			out = append(out, mk(CodeInvalidContract, "Skill der Kategorie %s ist kein Workflow-Skill und hat keinen Vertrag", sk.Category))
			continue
		case !wf:
			continue
		}
		ct, ok := c.contracts[id]
		if !ok {
			continue // in sich ungültig: Load hat es bereits gemeldet
		}
		var msgs []string
		bad := func(format string, args ...any) { msgs = append(msgs, fmt.Sprintf(format, args...)) }
		msgs = append(msgs, rules.checkContractRules(ct)...)
		if !slices.Contains(rules.doc.WorkflowCategories[sk.Category], ct.Role) {
			bad("role: die Kategorie %s wird von den Rollen %v getragen, nicht von %s", sk.Category, rules.doc.WorkflowCategories[sk.Category], ct.Role)
		}
		for i, u := range ct.UsesSkills {
			used, found := c.declared[u]
			switch {
			case !found:
				bad("uses_skills[%d]: Skill %s ist im Manifest nicht bekannt", i, u)
			case !used.AllowsRole(ct.Role):
				bad("uses_skills[%d]: Skill %s ist für die Rolle %s nicht erlaubt", i, u, ct.Role)
			}
		}
		for _, a := range ct.Scope.Allowed {
			if sys, ok := capabilityWrites[a]; ok && !slices.Contains(sk.ExternalWrites, sys) {
				bad("scope.allowed: %s verlangt %q in external_writes des Manifests (ist %v)", a, sys, sk.ExternalWrites)
			}
		}
		for _, m := range msgs {
			out = append(out, mk(CodeInvalidContract, "%s", m))
		}
	}
	return out
}

// loadContracts liest die contract.yaml aller geladenen Skills streng und in sich (Stufe 1) und prüft Skill-ID
// und Rolle gegen den eigenen Manifest-Eintrag. Es verlangt weder Regeln noch das Vorhandensein von Verträgen.
func (c *Catalog) loadContracts(contractData map[string][]byte) []*Error {
	var out []*Error
	for _, sk := range c.skills {
		id := sk.ID
		data, has := contractData[id]
		if !has {
			continue
		}
		c.hasContract[id] = true
		mk := func(code Code, format string, args ...any) *Error {
			return newErr(code, id, contractFile, "Vertrag %s/%s: %s", id, contractFile, fmt.Sprintf(format, args...))
		}
		ct, decoded, problems := parseContractShape(data)
		for _, p := range problems {
			out = append(out, mk(p.code, "%s", p.msg))
		}
		if !decoded {
			continue
		}
		var cross []string
		if ct.Skill != id {
			cross = append(cross, fmt.Sprintf("skill: %q passt nicht zur Skill-ID %q", ct.Skill, id))
		}
		if ct.Role.Valid() && !slices.Contains(sk.Roles, ct.Role) {
			cross = append(cross, fmt.Sprintf("role: %s steht nicht in den Rollen des Manifests (%v)", ct.Role, sk.Roles))
		}
		for _, m := range cross {
			out = append(out, mk(CodeInvalidContract, "%s", m))
		}
		if len(cross) == 0 && len(problems) == 0 {
			c.contracts[id] = ct
		}
	}
	return out
}

// Contract liefert den in Load geprüften Workflow-Vertrag eines Skills (Kopie); Hilfsskills haben keinen. Der
// Vertrag ist in sich und gegen internal/domain gültig; gegen die Rollenregeln prüft ihn erst ValidateWorkflow.
func (c *Catalog) Contract(id string) (Contract, bool) {
	ct, ok := c.contracts[id]
	if !ok {
		return Contract{}, false
	}
	return ct.clone(), true
}

// Contracts liefert alle in Load geprüften Verträge aufsteigend nach Skill-ID (Kopien).
func (c *Catalog) Contracts() []Contract {
	ids := make([]string, 0, len(c.contracts))
	for id := range c.contracts {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]Contract, len(ids))
	for i, id := range ids {
		out[i] = c.contracts[id].clone()
	}
	return out
}
