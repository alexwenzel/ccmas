package skills

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/alexwenzel/ccmas/internal/domain"
)

// ---- Hilfen -----------------------------------------------------------------

func repoRules(t *testing.T) *Rules {
	t.Helper()
	r, err := LoadRules(repoRoot)
	if err != nil {
		for _, e := range FindingsOf(err) {
			t.Errorf("%s: %s", e.Code, e.Message)
		}
		t.Fatal("rules/roles.yaml des Repositorys ist ungültig")
	}
	return r
}

// wantFinding verlangt einen Befund mit Code und einem Feldverweis in der Meldung. Der Feldverweis ist
// der strukturelle Ort ("results.findings.followups[0]"), nicht der Wortlaut der Erklärung.
func wantFinding(t *testing.T, err error, code Code, field string) {
	t.Helper()
	if err == nil {
		t.Fatalf("Befund %s (%s) erwartet, aber kein Fehler", code, field)
	}
	for _, e := range FindingsOf(err) {
		if e.Code == code && strings.Contains(e.Message, field) {
			return
		}
	}
	t.Fatalf("kein Befund %s mit Feld %q in: %v", code, field, err)
}

// at liefert die verschachtelte Struktur unter den Schlüsseln.
func at(t *testing.T, m map[string]any, path ...string) map[string]any {
	t.Helper()
	cur := m
	for _, k := range path {
		next, ok := cur[k].(map[string]any)
		if !ok {
			t.Fatalf("Schlüssel %q fehlt oder ist kein Objekt (Pfad %v)", k, path)
		}
		cur = next
	}
	return cur
}

func list(t *testing.T, m map[string]any, key string) []any {
	t.Helper()
	l, ok := m[key].([]any)
	if !ok {
		t.Fatalf("Schlüssel %q fehlt oder ist keine Liste", key)
	}
	return l
}

func strs(vals ...string) []any {
	out := make([]any, len(vals))
	for i, v := range vals {
		out[i] = v
	}
	return out
}

func mustParseRules(t *testing.T, m map[string]any) (*Rules, error) {
	t.Helper()
	return ParseRules([]byte(marshalYAML(t, m)))
}

func repoRulesMap(t *testing.T) map[string]any {
	t.Helper()
	return parseYAMLMap(t, readRepoFile(t, "rules/roles.yaml"))
}

// decisionAcceptsRole fragt die echte Prüfung von domain.Decision, ob die Rolle zur Aktion passt.
func decisionAcceptsRole(t *testing.T, action domain.Action, role domain.Role) bool {
	t.Helper()
	d := domain.Decision{
		SchemaVersion: 1, DecisionID: "d", ProjectID: "p", StateRevision: 1, Action: action,
		Selection: &domain.Selection{Role: role, SkillIDs: []string{}, Model: "m", Effort: domain.EffortHigh},
	}
	err := d.Check(domain.DecisionContext{
		ProjectID: "p", StateRevision: 1,
		Catalog: domain.Catalog{Models: map[string][]domain.Effort{"m": {domain.EffortHigh}}},
	})
	if err == nil {
		return true
	}
	var ce *domain.ContractError
	if !errors.As(err, &ce) || ce.Category() != domain.CatRole {
		t.Fatalf("Decision.Check(%s,%s): unerwarteter Fehler %v", action, role, err)
	}
	return false
}

var pauseActions = []domain.Action{domain.ActionAskQuestion, domain.ActionWait, domain.ActionRequestIntegration}

var workflowSkillIDs = []string{
	"ccmas-antworten-abholen", "ccmas-code-review", "ccmas-concept-review", "ccmas-concept-ticket",
	"ccmas-feature-parallel", "ccmas-meilenstein-abschluss",
}

// ---- Verträge ---------------------------------------------------------------

func TestWorkflowSkillContracts(t *testing.T) {
	t.Run("echter Bestand", func(t *testing.T) {
		c, err := Load(repoRoot)
		if err != nil {
			for _, e := range FindingsOf(err) {
				t.Errorf("%s: %s", e.Code, e.Message)
			}
			t.Fatal("Load des Repositorys")
		}
		rules := c.Rules()
		var got []string
		for _, sk := range c.Skills() {
			_, has := c.Contract(sk.ID)
			if rules.IsWorkflowCategory(sk.Category) {
				got = append(got, sk.ID)
			} else if has {
				t.Errorf("Hilfsskill %s (%s) hat einen Vertrag", sk.ID, sk.Category)
			}
		}
		if !slices.Equal(got, workflowSkillIDs) {
			t.Fatalf("Workflow-Skills = %v, erwartet %v", got, workflowSkillIDs)
		}
		for _, id := range got {
			t.Run(id, func(t *testing.T) {
				sk, _ := c.Skill(id)
				ct, ok := c.Contract(id)
				if !ok {
					t.Fatal("kein Vertrag")
				}
				if ct.Skill != id || !slices.Contains(sk.Roles, ct.Role) {
					t.Errorf("Vertrag %s/%s passt nicht zum Manifest (Rollen %v)", ct.Skill, ct.Role, sk.Roles)
				}
				if len(ct.Inputs) == 0 || len(ct.Checks) == 0 || len(ct.Blockers) == 0 || len(ct.CompletionCriteria) == 0 ||
					len(ct.Scope.Allowed) == 0 || len(ct.Scope.Forbidden) == 0 || !ct.Scope.WithinWorkOrder {
					t.Errorf("Pflichtteile unvollständig: %+v", ct)
				}
				if ct.Result.Format != rules.ResultFormat(ct.Role) {
					t.Errorf("Ergebnisformat %q, Rolle %s verlangt %q", ct.Result.Format, ct.Role, rules.ResultFormat(ct.Role))
				}
				for _, p := range ct.Preconditions {
					if !slices.Contains(preconditionKinds(), p) {
						t.Errorf("unbekannte Voraussetzungsart %q", p)
					}
				}
				for _, u := range ct.UsesSkills {
					used, ok := c.Skill(u)
					if !ok || !used.AllowsRole(ct.Role) {
						t.Errorf("uses_skills %s: nicht vorhanden oder für %s nicht erlaubt", u, ct.Role)
					}
				}
				// Jedes Ergebnis der Rolle hat einen Folgeauftrag, nicht mehr und nicht weniger.
				var have []string
				for r := range ct.Results {
					have = append(have, r)
				}
				sort.Strings(have)
				if want := rules.ResultIDs(ct.Role); !slices.Equal(have, want) {
					t.Fatalf("Ergebnisse %v, Rolle %s hat %v", have, ct.Role, want)
				}
				for result, rc := range ct.Results {
					if len(rc.Followups) == 0 {
						t.Errorf("Ergebnis %s ohne Folgeauftrag", result)
					}
					allowed, _ := rules.NextSteps(ct.Role, result)
					for _, f := range rc.Followups {
						if !f.Action.Valid() {
							t.Errorf("%s: ungültige Aktion %q", result, f.Action)
						}
						switch f.Action {
						case domain.ActionConceptWork, domain.ActionImplementation, domain.ActionReview:
							if !decisionAcceptsRole(t, f.Action, f.Role) {
								t.Errorf("%s: Rolle %s passt nicht zur Aktion %s (Decision.Check)", result, f.Role, f.Action)
							}
						}
						if !slices.ContainsFunc(allowed, func(s Step) bool { return s.Action == f.Action && s.Role == f.Role }) {
							t.Errorf("%s: Folgeauftrag %s/%s steht nicht in den Rollenregeln", result, f.Action, f.Role)
						}
					}
					if rv := rules.ResultVerdict(ct.Role, result); rc.Verdict != rv {
						t.Errorf("%s: Urteil %q, Rollenregeln verlangen %q", result, rc.Verdict, rv)
					}
				}
				for _, b := range ct.Blockers {
					if _, ok := ct.Results[b.Result]; !ok {
						t.Errorf("Blockade %s verweist auf unbekanntes Ergebnis %q", b.ID, b.Result)
					}
				}
			})
		}
	})

	t.Run("Prüfskills sind nur lesend", func(t *testing.T) {
		c := mustLoadRepo(t)
		for _, id := range []string{"ccmas-code-review", "ccmas-concept-review"} {
			ct, _ := c.Contract(id)
			for _, forbidden := range []string{"write_worktree", "commit", "push", "open_pr", "merge", "write_asana", "write_vault", "start_agents", "change_platform_limits"} {
				if slices.Contains(ct.Scope.Allowed, forbidden) || !slices.Contains(ct.Scope.Forbidden, forbidden) {
					t.Errorf("%s: %s muss verboten sein", id, forbidden)
				}
			}
		}
	})

	t.Run("Test-Skills mit gültigem Vertrag laden", func(t *testing.T) {
		f := newCatalogFixture(t, defaultTestSkills())
		c := f.mustLoad()
		ct, ok := c.Contract("ccmas-demo-review")
		if !ok || ct.Role != domain.RoleReviewer {
			t.Fatalf("Vertrag fehlt oder falsche Rolle: %+v %v", ct, ok)
		}
		if _, ok := c.Contract("ccmas-demo-know"); ok {
			t.Error("Hilfsskill hat einen Vertrag")
		}
	})

	broken := map[string]struct {
		mutate func(t *testing.T, m map[string]any)
		field  string
	}{
		"Schlüssel skill fehlt":           {func(t *testing.T, m map[string]any) { delete(m, "skill") }, "skill"},
		"unbekannter Schlüssel":           {func(t *testing.T, m map[string]any) { m["bonus"] = 1 }, "bonus"},
		"falsche schema_version":          {func(t *testing.T, m map[string]any) { m["schema_version"] = 2 }, "schema_version"},
		"fremde Skill-ID":                 {func(t *testing.T, m map[string]any) { m["skill"] = "ccmas-anderer" }, "skill"},
		"Rolle nicht im Manifest":         {func(t *testing.T, m map[string]any) { m["role"] = "implementer" }, "role"},
		"unbekannte Rolle":                {func(t *testing.T, m map[string]any) { m["role"] = "zauberer" }, "role"},
		"uses_skills unbekannt":           {func(t *testing.T, m map[string]any) { m["uses_skills"] = strs("ccmas-gibt-es-nicht") }, "uses_skills"},
		"uses_skills nicht für die Rolle": {func(t *testing.T, m map[string]any) { m["uses_skills"] = strs("ccmas-demo-impl") }, "uses_skills"},
		"unbekannte Voraussetzung":        {func(t *testing.T, m map[string]any) { m["preconditions"] = strs("magie") }, "preconditions"},
		"Eingabe ohne Name": {func(t *testing.T, m map[string]any) {
			m["inputs"] = []any{map[string]any{"name": "", "required": true, "description": "x"}}
		}, "inputs[0]"},
		"Eingaben leer": {func(t *testing.T, m map[string]any) { m["inputs"] = []any{} }, "inputs"},
		"Eingabe doppelt": {func(t *testing.T, m map[string]any) {
			in := map[string]any{"name": "a", "required": true, "description": "x"}
			m["inputs"] = []any{in, in}
		}, "inputs[1]"},
		"Umfang überschreitet Auftrag": {func(t *testing.T, m map[string]any) { at(t, m, "scope")["within_work_order"] = false }, "scope.within_work_order"},
		"Fähigkeit nicht erlaubt für die Rolle": {func(t *testing.T, m map[string]any) {
			s := at(t, m, "scope")
			s["allowed"] = append(list(t, s, "allowed"), "push")
			s["forbidden"] = strs("write_worktree", "commit", "open_pr", "merge", "write_asana", "write_vault", "start_agents", "change_platform_limits")
		}, "scope.allowed"},
		"Pflichtverbot fehlt": {func(t *testing.T, m map[string]any) {
			at(t, m, "scope")["forbidden"] = strs("push", "start_agents", "change_platform_limits")
		}, "scope.forbidden"},
		"Agenten starten erlaubt": {func(t *testing.T, m map[string]any) {
			s := at(t, m, "scope")
			s["allowed"] = append(list(t, s, "allowed"), "start_agents")
		}, "scope.allowed"},
		"unbekannte Fähigkeit": {func(t *testing.T, m map[string]any) {
			s := at(t, m, "scope")
			s["forbidden"] = append(list(t, s, "forbidden"), "fliegen")
		}, "scope.forbidden"},
		"Fähigkeit erlaubt und verboten": {func(t *testing.T, m map[string]any) {
			s := at(t, m, "scope")
			s["forbidden"] = append(list(t, s, "forbidden"), "read_repo")
		}, "scope"},
		"falsches Ergebnisformat": {func(t *testing.T, m map[string]any) { at(t, m, "result")["format"] = "work_report" }, "result.format"},
		"Prüfung ohne Beschreibung": {func(t *testing.T, m map[string]any) {
			m["checks"] = []any{map[string]any{"id": "x", "description": ""}}
		}, "checks[0]"},
		"Blockade verweist ins Leere": {func(t *testing.T, m map[string]any) {
			m["blockers"] = []any{map[string]any{"id": "x", "description": "d", "result": "gibt_es_nicht"}}
		}, "blockers[0].result"},
		"Abschlusskriterium leer": {func(t *testing.T, m map[string]any) { m["completion_criteria"] = strs("") }, "completion_criteria[0]"},
		"Ergebnis fehlt":          {func(t *testing.T, m map[string]any) { delete(at(t, m, "results"), "findings") }, "results"},
		"unbekanntes Ergebnis": {func(t *testing.T, m map[string]any) {
			at(t, m, "results")["bonus"] = map[string]any{"followups": []any{}}
		}, "results.bonus"},
		"Folgeaufträge leer":          {func(t *testing.T, m map[string]any) { at(t, m, "results", "findings")["followups"] = []any{} }, "results.findings.followups"},
		"Urteil weicht von Regeln ab": {func(t *testing.T, m map[string]any) { at(t, m, "results", "approved")["verdict"] = "BEFUNDE" }, "results.approved.verdict"},
		"Urteil fehlt":                {func(t *testing.T, m map[string]any) { delete(at(t, m, "results", "approved"), "verdict") }, "results.approved.verdict"},
		"unbekannte Aktion": {func(t *testing.T, m map[string]any) {
			at(t, m, "results", "findings")["followups"] = []any{map[string]any{"action": "deploy", "role": "implementer"}}
		}, "results.findings.followups[0]"},
		"Rolle passt nicht zur Aktion": {func(t *testing.T, m map[string]any) {
			at(t, m, "results", "findings")["followups"] = []any{map[string]any{"action": "implementation", "role": "reviewer"}}
		}, "results.findings.followups[0]"},
		"Rolle fehlt bei Aktion mit Rolle": {func(t *testing.T, m map[string]any) {
			at(t, m, "results", "findings")["followups"] = []any{map[string]any{"action": "implementation"}}
		}, "results.findings.followups[0]"},
		"Rolle bei Aktion ohne Rolle": {func(t *testing.T, m map[string]any) {
			at(t, m, "results", "findings")["followups"] = []any{map[string]any{"action": "wait", "role": "supervisor"}}
		}, "results.findings.followups[0]"},
		"Folgeauftrag außerhalb der Rollenregeln": {func(t *testing.T, m map[string]any) {
			at(t, m, "results", "findings")["followups"] = []any{map[string]any{"action": "wait"}}
		}, "results.findings.followups[0]"},
		"Voraussetzung der Rollenregeln fehlt": {func(t *testing.T, m map[string]any) {
			at(t, m, "results", "approved")["followups"] = []any{map[string]any{"action": "request_integration"}}
		}, "results.approved.followups[0]"},
		"unbekannte Voraussetzung im Folgeauftrag": {func(t *testing.T, m map[string]any) {
			at(t, m, "results", "approved")["followups"] = []any{map[string]any{"action": "request_integration", "preconditions": strs("review_approved", "magie")}}
		}, "results.approved.followups[0]"},
	}
	names := make([]string, 0, len(broken))
	for n := range broken {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		tc := broken[name]
		t.Run("kaputt: "+name, func(t *testing.T) {
			f := newCatalogFixture(t, defaultTestSkills())
			m := baseContract(t, defaultTestSkills()[0])
			tc.mutate(t, m)
			f.setContract("ccmas-demo-review", m)
			_, err := f.load()
			wantFinding(t, err, CodeInvalidContract, tc.field)
			for _, e := range FindingsOf(err) {
				if e.Code == CodeInvalidContract && (e.Skill != "ccmas-demo-review" || e.Path != "contract.yaml") {
					t.Errorf("Befund ohne Skill/Pfad: %+v", e)
				}
			}
		})
	}

	for _, key := range []string{"inputs", "preconditions", "scope", "result", "checks", "blockers", "completion_criteria", "results", "role", "uses_skills"} {
		t.Run("Pflichtteil fehlt: "+key, func(t *testing.T) {
			f := newCatalogFixture(t, defaultTestSkills())
			m := baseContract(t, defaultTestSkills()[0])
			delete(m, key)
			f.setContract("ccmas-demo-review", m)
			_, err := f.load()
			wantFinding(t, err, CodeInvalidContract, key)
		})
	}

	t.Run("Vertrag fehlt", func(t *testing.T) {
		f := newCatalogFixture(t, defaultTestSkills())
		f.remove("ccmas-demo-impl", "contract.yaml")
		_, err := f.load()
		wantErr(t, err, CodeMissingContract, "ccmas-demo-impl", "contract.yaml")
	})

	t.Run("Vertrag in Hilfsskill", func(t *testing.T) {
		f := newCatalogFixture(t, defaultTestSkills())
		f.setContractRaw("ccmas-demo-know", marshalYAML(t, baseContract(t, defaultTestSkills()[0])))
		_, err := f.load()
		wantErr(t, err, CodeInvalidContract, "ccmas-demo-know", "contract.yaml")
	})

	t.Run("Vertrag ist kein YAML", func(t *testing.T) {
		f := newCatalogFixture(t, defaultTestSkills())
		f.setContractRaw("ccmas-demo-review", "skill: [unvollständig\n")
		_, err := f.load()
		wantErr(t, err, CodeInvalidContract, "ccmas-demo-review", "contract.yaml")
	})

	t.Run("doppelter Schlüssel", func(t *testing.T) {
		f := newCatalogFixture(t, defaultTestSkills())
		raw := marshalYAML(t, baseContract(t, defaultTestSkills()[0])) + "role: reviewer\n"
		f.setContractRaw("ccmas-demo-review", raw)
		_, err := f.load()
		wantErr(t, err, CodeInvalidContract, "ccmas-demo-review", "contract.yaml")
	})

	t.Run("mehrere YAML-Dokumente", func(t *testing.T) {
		f := newCatalogFixture(t, defaultTestSkills())
		raw := marshalYAML(t, baseContract(t, defaultTestSkills()[0])) + "---\nskill: x\n"
		f.setContractRaw("ccmas-demo-review", raw)
		_, err := f.load()
		wantErr(t, err, CodeInvalidContract, "ccmas-demo-review", "contract.yaml")
	})

	t.Run("Schreibfähigkeit nicht im Inventar", func(t *testing.T) {
		// demo-impl darf laut Manifest nur git und github, der Vertrag erlaubt Asana-Schreiben.
		f := newCatalogFixture(t, defaultTestSkills())
		m := baseContract(t, defaultTestSkills()[1])
		s := at(t, m, "scope")
		s["allowed"] = append(list(t, s, "allowed"), "write_asana")
		s["forbidden"] = strs("push", "open_pr", "merge", "write_vault", "start_agents", "change_platform_limits")
		f.setContract("ccmas-demo-impl", m)
		_, err := f.load()
		wantFinding(t, err, CodeInvalidContract, "scope.allowed")
	})

	t.Run("Ladefehler wird gemeldet, auch neben anderen Befunden", func(t *testing.T) {
		f := newCatalogFixture(t, defaultTestSkills())
		m := baseContract(t, defaultTestSkills()[0])
		delete(m, "checks")
		f.setContract("ccmas-demo-review", m)
		f.remove("ccmas-demo-impl", "contract.yaml")
		_, err := f.load()
		wantFinding(t, err, CodeInvalidContract, "checks")
		wantErr(t, err, CodeMissingContract, "ccmas-demo-impl", "contract.yaml")
	})
}

func mustLoadRepo(t *testing.T) *Catalog {
	t.Helper()
	c, err := Load(repoRoot)
	if err != nil {
		for _, e := range FindingsOf(err) {
			t.Errorf("%s: %s", e.Code, e.Message)
		}
		t.Fatal("Load des Repositorys")
	}
	return c
}

// ---- Review vor dem Push ----------------------------------------------------

func TestReviewBeforePushRule(t *testing.T) {
	r := repoRules(t)
	const head = "0123456789abcdef0123456789abcdef01234567"
	const other = "fedcba9876543210fedcba9876543210fedcba98"
	req := domain.IntegrationRequest{
		RequestID: "req-1", DecisionID: "dec-1", WorkOrderID: "wo-1", Mode: domain.ModePR, BaseBranch: "develop",
		HeadCommit: head, Branch: "feature/CC-1-x", ReviewReportID: "rep-1", ConceptVersion: "v3",
	}
	good := domain.ReviewReport{
		ReportID: "rep-1", WorkOrderID: "wo-1", ReviewerExecutionID: "exec-review", Commit: head,
		ConceptVersion: "v3", Verdict: domain.VerdictApproved, Findings: []domain.Finding{},
	}
	finding := []domain.Finding{{FindingID: "f1", Severity: domain.SeverityMajor, Summary: "Lücke"}}
	with := func(mod func(*domain.ReviewReport)) *domain.ReviewReport {
		c := good
		mod(&c)
		return &c
	}
	const impl = "exec-impl"

	cases := []struct {
		name string
		req  domain.IntegrationRequest
		rep  *domain.ReviewReport
		impl string
		want Code // "" = erlaubt
	}{
		{"FREIGEGEBEN für genau den Commit, andere Ausführung", req, &good, impl, ""},
		{"kein Review", req, nil, impl, CodeReviewMissing},
		{"Review eines anderen Commits", req, with(func(r *domain.ReviewReport) { r.Commit = other }), impl, CodeReviewInvalid},
		{"Commit nach dem Review geändert", func() domain.IntegrationRequest { q := req; q.HeadCommit = other; return q }(), &good, impl, CodeReviewInvalid},
		{"andere Konzeptversion", req, with(func(r *domain.ReviewReport) { r.ConceptVersion = "v2" }), impl, CodeReviewInvalid},
		{"Konzeptversion nach dem Review geändert", func() domain.IntegrationRequest { q := req; q.ConceptVersion = "v4"; return q }(), &good, impl, CodeReviewInvalid},
		{"anderer Bericht", req, with(func(r *domain.ReviewReport) { r.ReportID = "rep-9" }), impl, CodeReviewInvalid},
		{"anderer Auftrag", req, with(func(r *domain.ReviewReport) { r.WorkOrderID = "wo-9" }), impl, CodeReviewInvalid},
		{"BEFUNDE", req, with(func(r *domain.ReviewReport) { r.Verdict = domain.VerdictFindings; r.Findings = finding }), impl, CodeReviewInvalid},
		{"FREIGEGEBEN mit Befunden", req, with(func(r *domain.ReviewReport) { r.Findings = finding }), impl, CodeReviewInvalid},
		{"unbekanntes Urteil", req, with(func(r *domain.ReviewReport) { r.Verdict = "VIELLEICHT" }), impl, CodeReviewInvalid},
		{"selbe Ausführung wie die Implementierung", req, with(func(r *domain.ReviewReport) { r.ReviewerExecutionID = impl }), impl, CodeReviewNotIndependent},
		{"Ausführung der Implementierung unbekannt", req, &good, "", CodeReviewNotIndependent},
		{"Ausführung des Reviews unbekannt", req, with(func(r *domain.ReviewReport) { r.ReviewerExecutionID = "" }), impl, CodeReviewNotIndependent},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := r.PushAllowed(tc.req, tc.rep, tc.impl)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("erlaubt erwartet: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Push muss verweigert werden (%s)", tc.want)
			}
			if CodeOf(err) != tc.want {
				t.Fatalf("Code %q, erwartet %q (%v)", CodeOf(err), tc.want, err)
			}
		})
	}

	t.Run("kein Vertrag sieht Integration ohne review_approved vor", func(t *testing.T) {
		c := mustLoadRepo(t)
		guarded := r.GuardedActions()
		if !slices.Contains(guarded, domain.ActionRequestIntegration) {
			t.Fatalf("request_integration ist nicht geschützt: %v", guarded)
		}
		seen := 0
		for _, sk := range c.Skills() {
			ct, ok := c.Contract(sk.ID)
			if !ok {
				continue
			}
			for result, rc := range ct.Results {
				for _, f := range rc.Followups {
					if slices.Contains(guarded, f.Action) {
						seen++
						if !slices.Contains(f.Preconditions, domain.PrecReviewApproved) {
							t.Errorf("%s/%s: %s ohne review_approved", sk.ID, result, f.Action)
						}
					}
				}
			}
		}
		if seen == 0 {
			t.Error("kein Vertrag enthält einen Integrations-Folgeauftrag; der Test prüft nichts")
		}
		steps, _ := r.NextSteps(domain.RoleReviewer, "approved")
		if len(steps) != 1 || steps[0].Action != domain.ActionRequestIntegration || !slices.Contains(steps[0].Requires, domain.PrecReviewApproved) {
			t.Errorf("reviewer/approved = %+v", steps)
		}
	})

	t.Run("ein Vertrag kann die Voraussetzung nicht weglassen", func(t *testing.T) {
		f := newCatalogFixture(t, defaultTestSkills())
		m := baseContract(t, defaultTestSkills()[0])
		at(t, m, "results", "approved")["followups"] = []any{map[string]any{"action": "request_integration", "preconditions": strs("ci_passed")}}
		f.setContract("ccmas-demo-review", m)
		_, err := f.load()
		wantFinding(t, err, CodeInvalidContract, "review_approved")
	})

	t.Run("Regeldatei kann den Schutz nicht lockern", func(t *testing.T) {
		mut := map[string]func(*testing.T, map[string]any){
			"Urteil":                     func(t *testing.T, m map[string]any) { at(t, m, "review_before_push")["verdict_required"] = "BEFUNDE" },
			"unabhängige Ausführung":     func(t *testing.T, m map[string]any) { at(t, m, "review_before_push")["independent_execution"] = false },
			"neues Review nach Änderung": func(t *testing.T, m map[string]any) { at(t, m, "review_before_push")["rereview_after_change"] = false },
			"Bindung an Commit":          func(t *testing.T, m map[string]any) { at(t, m, "review_before_push")["bound_to"] = strs("commit") },
			"Voraussetzung": func(t *testing.T, m map[string]any) {
				at(t, m, "review_before_push")["required_precondition"] = "ci_passed"
			},
			"keine geschützte Aktion": func(t *testing.T, m map[string]any) { at(t, m, "review_before_push")["guarded_actions"] = []any{} },
			"Integrationsübergang ohne review_approved": func(t *testing.T, m map[string]any) {
				step := list(t, at(t, m, "roles", "reviewer", "results", "approved"), "next")[0].(map[string]any)
				delete(step, "requires")
			},
		}
		for name, fn := range mut {
			t.Run(name, func(t *testing.T) {
				m := repoRulesMap(t)
				fn(t, m)
				_, err := mustParseRules(t, m)
				wantFinding(t, err, CodeInvalidRules, "review_before_push")
			})
		}
	})
}

// ---- Konzeptfreigabe --------------------------------------------------------

func TestConceptApprovalModes(t *testing.T) {
	r := repoRules(t)
	criteria := r.StrategicCriteria()
	if len(criteria) == 0 {
		t.Fatal("keine Kriterien für strategisch")
	}

	t.Run("Routine geht an den unabhängigen Konzeptprüfer", func(t *testing.T) {
		got := r.ConceptApprover(ConceptAssessment{Class: ConceptRoutine})
		want := ApprovalRoute{Approver: ApproverConceptReviewer, Role: domain.RoleConceptReviewer, Action: domain.ActionReview}
		if got != want {
			t.Fatalf("%+v, erwartet %+v", got, want)
		}
		if !decisionAcceptsRole(t, got.Action, got.Role) {
			t.Error("Route passt nicht zu Decision.Check")
		}
	})
	userRoute := ApprovalRoute{Approver: ApproverUser, Action: domain.ActionAskQuestion}
	t.Run("strategisch und unklar gehen an den Nutzer", func(t *testing.T) {
		cases := map[string]ConceptAssessment{
			"strategisch":                       {Class: ConceptStrategic},
			"nicht eingestuft":                  {},
			"unbekannte Einstufung":             {Class: "wichtig"},
			"routine mit unbekanntem Kriterium": {Class: ConceptRoutine, Criteria: []string{"gibt_es_nicht"}},
			"routine mit leerem Kriterium":      {Class: ConceptRoutine, Criteria: []string{""}},
		}
		for _, id := range criteria {
			cases["routine, aber Kriterium "+id] = ConceptAssessment{Class: ConceptRoutine, Criteria: []string{id}}
			cases["strategisch mit Kriterium "+id] = ConceptAssessment{Class: ConceptStrategic, Criteria: []string{id}}
		}
		for name, a := range cases {
			if got := r.ConceptApprover(a); got != userRoute {
				t.Errorf("%s: %+v, erwartet %+v", name, got, userRoute)
			}
		}
	})
	t.Run("Kriterien stammen aus Regel 4 und 7.5", func(t *testing.T) {
		for _, want := range []string{"scope_change", "paid_service", "product_decision_deviation", "architecture_change", "accepted_limitation", "permission_change", "unclear_requirements"} {
			if !slices.Contains(criteria, want) {
				t.Errorf("Kriterium %s fehlt", want)
			}
		}
	})

	t.Run("Freigabe gilt nur für die genannte Version", func(t *testing.T) {
		routine := ConceptAssessment{Class: ConceptRoutine}
		ok := ConceptApproval{Version: "v2", Approver: ApproverConceptReviewer}
		if err := r.ConceptApprovalValid(ok, routine, "v2"); err != nil {
			t.Fatalf("gültige Freigabe abgelehnt: %v", err)
		}
		for name, tc := range map[string]struct {
			ap  ConceptApproval
			a   ConceptAssessment
			cur string
		}{
			"neuere Version":                           {ok, routine, "v3"},
			"leere aktuelle Version":                   {ok, routine, ""},
			"leere freigegebene Version":               {ConceptApproval{Approver: ApproverConceptReviewer}, routine, ""},
			"Konzeptprüfer gibt Strategisches frei":    {ok, ConceptAssessment{Class: ConceptStrategic}, "v2"},
			"Konzeptprüfer bei ungeklärter Einstufung": {ok, ConceptAssessment{}, "v2"},
			"unbekannter Freigeber":                    {ConceptApproval{Version: "v2", Approver: "agent"}, routine, "v2"},
		} {
			err := r.ConceptApprovalValid(tc.ap, tc.a, tc.cur)
			if CodeOf(err) != CodeConceptApprovalInvalid {
				t.Errorf("%s: %v", name, err)
			}
		}
		// Der Nutzer darf auch Strategisches freigeben, ebenfalls nur für die Version.
		user := ConceptApproval{Version: "v2", Approver: ApproverUser}
		if err := r.ConceptApprovalValid(user, ConceptAssessment{Class: ConceptStrategic}, "v2"); err != nil {
			t.Errorf("Nutzerfreigabe abgelehnt: %v", err)
		}
		if err := r.ConceptApprovalValid(user, ConceptAssessment{Class: ConceptStrategic}, "v3"); CodeOf(err) != CodeConceptApprovalInvalid {
			t.Errorf("Nutzerfreigabe für andere Version: %v", err)
		}
	})

	t.Run("Regeldatei kann die Wege nicht umbiegen", func(t *testing.T) {
		mut := map[string]func(*testing.T, map[string]any){
			"strategisch an den Konzeptprüfer": func(t *testing.T, m map[string]any) {
				at(t, m, "concept_approval", "strategic")["approver"] = "concept_reviewer"
			},
			"unklar an den Konzeptprüfer": func(t *testing.T, m map[string]any) {
				at(t, m, "concept_approval", "unclassifiable")["approver"] = "concept_reviewer"
			},
			"Routine an den Nutzer": func(t *testing.T, m map[string]any) {
				at(t, m, "concept_approval", "routine")["approver"] = "user"
			},
			"Freigabe nicht an die Version gebunden": func(t *testing.T, m map[string]any) {
				at(t, m, "concept_approval")["bound_to"] = "ticket"
			},
			"keine Kriterien": func(t *testing.T, m map[string]any) {
				at(t, m, "concept_approval")["strategic_criteria"] = []any{}
			},
		}
		for name, fn := range mut {
			t.Run(name, func(t *testing.T) {
				m := repoRulesMap(t)
				fn(t, m)
				_, err := mustParseRules(t, m)
				wantFinding(t, err, CodeInvalidRules, "concept_approval")
			})
		}
	})
}

// ---- Plattformgrenzen -------------------------------------------------------

func TestSkillsCannotExtendPlatformLimits(t *testing.T) {
	forbiddenKeys := map[string]any{
		"max_workers":      9,
		"integration_mode": "auto",
		"merge":            "auto",
		"merge_mode":       "auto",
		"policy_profile":   "unrestricted",
		"permissions":      strs("Bash"),
		"permission_mode":  "bypassPermissions",
		"allowed_tools":    strs("Bash"),
		"target_branch":    "main",
		"base_branch":      "main",
		"mode":             "auto",
	}
	names := make([]string, 0, len(forbiddenKeys))
	for k := range forbiddenKeys {
		names = append(names, k)
	}
	sort.Strings(names)

	for _, key := range names {
		val := forbiddenKeys[key]
		t.Run("Vertrag: "+key, func(t *testing.T) {
			places := map[string]func(m map[string]any){
				"oberste Ebene": func(m map[string]any) { m[key] = val },
				"scope":         func(m map[string]any) { at(t, m, "scope")[key] = val },
				"Folgeauftrag": func(m map[string]any) {
					list(t, at(t, m, "results", "findings"), "followups")[0].(map[string]any)[key] = val
				},
				"Ergebnis":      func(m map[string]any) { at(t, m, "results", "approved")[key] = val },
				"Prüfung":       func(m map[string]any) { list(t, m, "checks")[0].(map[string]any)[key] = val },
				"result-Format": func(m map[string]any) { at(t, m, "result")[key] = val },
			}
			for place, set := range places {
				f := newCatalogFixture(t, defaultTestSkills())
				m := baseContract(t, defaultTestSkills()[0])
				set(m)
				f.setContract("ccmas-demo-review", m)
				_, err := f.load()
				if err == nil {
					t.Errorf("%s: Vertrag mit %s wurde geladen", place, key)
					continue
				}
				wantFinding(t, err, CodePlatformLimit, key)
			}
		})
		t.Run("Regeldatei: "+key, func(t *testing.T) {
			places := map[string]func(m map[string]any){
				"oberste Ebene": func(m map[string]any) { m[key] = val },
				"Rolle":         func(m map[string]any) { at(t, m, "roles", "reviewer")[key] = val },
				"Integration":   func(m map[string]any) { at(t, m, "integration")[key] = val },
				"Übergang": func(m map[string]any) {
					list(t, at(t, m, "roles", "reviewer", "results", "findings"), "next")[0].(map[string]any)[key] = val
				},
				"Reparaturschleife": func(m map[string]any) { at(t, m, "repair_loop")[key] = val },
			}
			for place, set := range places {
				m := repoRulesMap(t)
				set(m)
				_, err := mustParseRules(t, m)
				if err == nil {
					t.Errorf("%s: Regeldatei mit %s wurde geladen", place, key)
					continue
				}
				wantFinding(t, err, CodePlatformLimit, key)
			}
		})
	}

	t.Run("Regeldatei mit anderem Zielbranch", func(t *testing.T) {
		m := repoRulesMap(t)
		at(t, m, "platform")["target_branch"] = "main"
		_, err := mustParseRules(t, m)
		wantFinding(t, err, CodePlatformLimit, "platform.target_branch")
	})
	t.Run("Regeldatei schützt nicht mehr alle Plattformwerte", func(t *testing.T) {
		for _, drop := range []string{"max_workers", "integration_mode", "policy_profile", "permissions", "target_branch"} {
			m := repoRulesMap(t)
			p := at(t, m, "platform")
			var keep []any
			for _, v := range list(t, p, "protected") {
				if v != drop {
					keep = append(keep, v)
				}
			}
			p["protected"] = keep
			_, err := mustParseRules(t, m)
			wantFinding(t, err, CodePlatformLimit, "platform.protected")
		}
	})
	t.Run("Load meldet den Verstoß aus der Regeldatei", func(t *testing.T) {
		f := newCatalogFixture(t, defaultTestSkills())
		m := repoRulesMap(t)
		m["max_workers"] = 8
		f.setRulesRaw(marshalYAML(t, m))
		_, err := f.load()
		wantFinding(t, err, CodePlatformLimit, "max_workers")
	})
	t.Run("Die Plattform bleibt unverändert", func(t *testing.T) {
		r := repoRules(t)
		if r.TargetBranch() != domain.BaseBranch || r.TargetBranch() != "develop" {
			t.Errorf("Zielbranch %q", r.TargetBranch())
		}
		if got := r.ProtectedPlatformValues(); !slices.Equal(got, []string{"integration_mode", "max_workers", "permissions", "policy_profile", "target_branch"}) {
			t.Errorf("geschützt: %v", got)
		}
	})
	t.Run("Auto gilt nur ausdrücklich je Auftrag", func(t *testing.T) {
		r := repoRules(t)
		if err := r.IntegrationModeAllowed(domain.ModePR, domain.ModeAuto); CodeOf(err) != CodePlatformLimit {
			t.Errorf("Aufweitung pr -> auto: %v", err)
		}
		if err := r.IntegrationModeAllowed("", domain.ModeAuto); CodeOf(err) != CodePlatformLimit {
			t.Errorf("Aufweitung von unbekanntem Modus: %v", err)
		}
		for _, tc := range [][2]domain.IntegrationMode{{domain.ModePR, domain.ModePR}, {domain.ModeAuto, domain.ModeAuto}, {domain.ModeAuto, domain.ModePR}} {
			if err := r.IntegrationModeAllowed(tc[0], tc[1]); err != nil {
				t.Errorf("%s -> %s: %v", tc[0], tc[1], err)
			}
		}
		if err := r.IntegrationModeAllowed(domain.ModePR, "manuell"); CodeOf(err) != CodeInvalidRequest {
			t.Errorf("unbekannter Modus: %v", err)
		}
		if r.DefaultIntegrationMode() != domain.ModePR {
			t.Errorf("Standardmodus %s", r.DefaultIntegrationMode())
		}
	})
}

// ---- Reparaturschleife ------------------------------------------------------

func TestRepairBudget(t *testing.T) {
	r := repoRules(t)
	if r.MaxRepairRounds() != 3 {
		t.Fatalf("Standardbudget %d", r.MaxRepairRounds())
	}
	for round := 1; round <= 3; round++ {
		d, err := r.RepairStep(round)
		if err != nil {
			t.Fatal(err)
		}
		if d.Halt || d.Step.Action != domain.ActionImplementation || d.Step.Role != domain.RoleImplementer {
			t.Errorf("Runde %d: %+v", round, d)
		}
	}
	for _, round := range []int{4, 5, 100} {
		d, err := r.RepairStep(round)
		if err != nil {
			t.Fatal(err)
		}
		if !d.Halt || d.Step.Action != domain.ActionAskQuestion || d.Step.Role != "" {
			t.Errorf("Runde %d: kein sichtbarer Halt mit Frage an den Nutzer: %+v", round, d)
		}
	}
	for _, round := range []int{0, -1} {
		if _, err := r.RepairStep(round); CodeOf(err) != CodeInvalidRequest {
			t.Errorf("Runde %d: %v", round, err)
		}
	}
	// BEFUNDE führen laut Übergangstabelle in die Implementierung; das Budget kommt aus der Regeldatei.
	steps, _ := r.NextSteps(domain.RoleReviewer, "findings")
	if len(steps) != 1 || steps[0].Action != domain.ActionImplementation {
		t.Errorf("reviewer/findings = %+v", steps)
	}

	t.Run("Budget aus der Regeldatei", func(t *testing.T) {
		m := repoRulesMap(t)
		at(t, m, "repair_loop")["max_rounds"] = 1
		r1, err := mustParseRules(t, m)
		if err != nil {
			t.Fatal(err)
		}
		if d, _ := r1.RepairStep(2); !d.Halt {
			t.Errorf("Runde 2 bei Budget 1: %+v", d)
		}
	})
	t.Run("ungültiges Budget", func(t *testing.T) {
		for _, v := range []any{0, -3, 11, "viele", 2.5} {
			m := repoRulesMap(t)
			at(t, m, "repair_loop")["max_rounds"] = v
			_, err := mustParseRules(t, m)
			wantFinding(t, err, CodeInvalidRules, "repair_loop.max_rounds")
		}
	})
	t.Run("Schleife läuft in die Implementierung und Halt fragt den Nutzer", func(t *testing.T) {
		mut := map[string]func(*testing.T, map[string]any){
			"on_findings": func(t *testing.T, m map[string]any) { at(t, m, "repair_loop", "on_findings")["action"] = "wait" },
			"on_exhausted": func(t *testing.T, m map[string]any) {
				at(t, m, "repair_loop", "on_exhausted")["action"] = "implementation"
			},
		}
		for name, fn := range mut {
			m := repoRulesMap(t)
			fn(t, m)
			_, err := mustParseRules(t, m)
			wantFinding(t, err, CodeInvalidRules, "repair_loop."+name)
		}
	})
}

// ---- Abschluss --------------------------------------------------------------

func fullEvidence() CompletionEvidence {
	return CompletionEvidence{
		Mode: domain.ModePR, Integration: IntegrationMerged, MergedBy: MergedByHuman,
		AcceptanceCriteriaMet: true, ReviewEvidence: true, TestEvidence: true, CIEvidence: true,
		TaskStateUpdated: true, KnowledgeUpdated: true,
	}
}

func TestFeatureComplete(t *testing.T) {
	r := repoRules(t)

	t.Run("alles belegt und gemergt", func(t *testing.T) {
		got := r.FeatureComplete(fullEvidence())
		if !got.Complete || got.State != domain.WOCompleted || len(got.Missing) != 0 {
			t.Fatalf("%+v", got)
		}
	})
	t.Run("offener PR ist Wartezustand, nie Abschluss", func(t *testing.T) {
		for _, mode := range []domain.IntegrationMode{domain.ModePR, domain.ModeAuto} {
			ev := fullEvidence()
			ev.Mode, ev.Integration, ev.MergedBy = mode, IntegrationPROpen, ""
			got := r.FeatureComplete(ev)
			if got.Complete || got.State != domain.WOWaitingIntegration || !slices.Equal(got.Missing, []string{"merge_confirmed"}) {
				t.Errorf("%s: %+v", mode, got)
			}
		}
	})
	t.Run("offener PR mit widersprüchlicher Merge-Angabe bleibt Wartezustand", func(t *testing.T) {
		ev := fullEvidence()
		ev.Integration = IntegrationPROpen // MergedBy bleibt gesetzt
		got := r.FeatureComplete(ev)
		if got.Complete || got.State != domain.WOWaitingIntegration {
			t.Errorf("%+v", got)
		}
	})
	t.Run("Auto: Merge über die Queue zählt, im PR-Modus nicht", func(t *testing.T) {
		ev := fullEvidence()
		ev.Mode, ev.MergedBy = domain.ModeAuto, MergedByQueue
		if got := r.FeatureComplete(ev); !got.Complete {
			t.Errorf("Auto + Queue: %+v", got)
		}
		ev.Mode = domain.ModePR
		if got := r.FeatureComplete(ev); got.Complete || !slices.Contains(got.Missing, "merge_confirmed") {
			t.Errorf("PR-Modus mit automatischem Merge: %+v", got)
		}
		ev.Mode = ""
		if got := r.FeatureComplete(ev); got.Complete {
			t.Errorf("unbekannter Modus gilt als PR-Modus: %+v", got)
		}
	})
	t.Run("nicht gemergt", func(t *testing.T) {
		ev := fullEvidence()
		ev.Integration, ev.MergedBy = IntegrationNone, ""
		got := r.FeatureComplete(ev)
		if got.Complete || got.State == domain.WOCompleted || got.State == domain.WOWaitingIntegration {
			t.Errorf("%+v", got)
		}
	})
	singles := map[string]func(*CompletionEvidence){
		"acceptance_criteria_met": func(e *CompletionEvidence) { e.AcceptanceCriteriaMet = false },
		"review_evidence":         func(e *CompletionEvidence) { e.ReviewEvidence = false },
		"test_evidence":           func(e *CompletionEvidence) { e.TestEvidence = false },
		"ci_evidence":             func(e *CompletionEvidence) { e.CIEvidence = false },
		"task_state_updated":      func(e *CompletionEvidence) { e.TaskStateUpdated = false },
		"knowledge_updated":       func(e *CompletionEvidence) { e.KnowledgeUpdated = false },
	}
	for id, drop := range singles {
		t.Run("fehlt: "+id, func(t *testing.T) {
			ev := fullEvidence()
			drop(&ev)
			got := r.FeatureComplete(ev)
			if got.Complete || got.State == domain.WOCompleted || !slices.Equal(got.Missing, []string{id}) {
				t.Errorf("%+v", got)
			}
		})
	}
	t.Run("ohne jeden Nachweis", func(t *testing.T) {
		got := r.FeatureComplete(CompletionEvidence{})
		if got.Complete || len(got.Missing) != 7 {
			t.Errorf("%+v", got)
		}
	})
	t.Run("Meilenstein braucht Teilziele und Abnahme", func(t *testing.T) {
		ev := fullEvidence()
		got := r.MilestoneComplete(ev)
		if got.Complete || !slices.Equal(got.Missing, []string{"children_complete", "owner_acceptance"}) {
			t.Errorf("%+v", got)
		}
		ev.ChildrenComplete, ev.OwnerAcceptance = true, true
		if got := r.MilestoneComplete(ev); !got.Complete {
			t.Errorf("%+v", got)
		}
		ev.ChildrenComplete = false
		if got := r.MilestoneComplete(ev); got.Complete || !slices.Equal(got.Missing, []string{"children_complete"}) {
			t.Errorf("%+v", got)
		}
		// Ein fertiger Meilenstein-Nachweis macht ein Feature nicht unfertig und umgekehrt.
		if got := r.FeatureComplete(fullEvidence()); !got.Complete {
			t.Errorf("Feature: %+v", got)
		}
	})
	t.Run("Regeldatei kann den Abschluss nicht aufweichen", func(t *testing.T) {
		mut := map[string]func(*testing.T, map[string]any){
			"offener PR als Abschluss": func(t *testing.T, m map[string]any) { at(t, m, "completion")["open_pr_counts_as_completion"] = true },
			"Merge nicht verlangt": func(t *testing.T, m map[string]any) {
				at(t, m, "completion")["requires"] = strs("acceptance_criteria_met", "review_evidence", "test_evidence", "ci_evidence", "task_state_updated", "knowledge_updated")
			},
			"unbekannter Nachweis": func(t *testing.T, m map[string]any) {
				at(t, m, "completion")["requires"] = append(list(t, at(t, m, "completion"), "requires"), "bauchgefuehl")
			},
			"Abnahme des Meilensteins entfällt": func(t *testing.T, m map[string]any) {
				at(t, m, "completion")["milestone_also_requires"] = strs("children_complete")
			},
			"Auto ohne ausdrücklichen Auftrag": func(t *testing.T, m map[string]any) {
				at(t, m, "integration")["auto_requires_explicit_order"] = false
			},
			"Auto als Standard":    func(t *testing.T, m map[string]any) { at(t, m, "integration")["default"] = "auto" },
			"anderer Wartezustand": func(t *testing.T, m map[string]any) { at(t, m, "integration")["waiting_state"] = "completed" },
		}
		for name, fn := range mut {
			t.Run(name, func(t *testing.T) {
				m := repoRulesMap(t)
				fn(t, m)
				_, err := mustParseRules(t, m)
				if err == nil {
					t.Fatal("Regeldatei wurde geladen")
				}
				if CodeOf(err) != CodeInvalidRules && CodeOf(err) != CodePlatformLimit {
					t.Fatalf("Code %q: %v", CodeOf(err), err)
				}
			})
		}
	})
}

func TestQuestionRoute(t *testing.T) {
	r := repoRules(t)
	want := []string{"worker", "supervisor", "central", "supervisor", "worker"}
	if got := r.QuestionRoute(); !slices.Equal(got, want) {
		t.Errorf("Fragenweg %v, erwartet %v", got, want)
	}
	if r.SilenceIsApproval() {
		t.Error("keine Antwort darf keine Freigabe sein")
	}
	mut := map[string]func(*testing.T, map[string]any){
		"Schweigen als Freigabe": func(t *testing.T, m map[string]any) { at(t, m, "question_route")["silence_is_approval"] = true },
		"Zentrale übersprungen": func(t *testing.T, m map[string]any) {
			at(t, m, "question_route")["hops"] = strs("worker", "supervisor", "worker")
		},
		"unbekannte Station": func(t *testing.T, m map[string]any) {
			at(t, m, "question_route")["hops"] = strs("worker", "supervisor", "central", "supervisor", "nutzer")
		},
	}
	for name, fn := range mut {
		m := repoRulesMap(t)
		fn(t, m)
		_, err := mustParseRules(t, m)
		wantFinding(t, err, CodeInvalidRules, "question_route")
		_ = name
	}
}

// ---- Rollenübergänge --------------------------------------------------------

func TestNextStepNoDeadEnds(t *testing.T) {
	r := repoRules(t)
	type node struct {
		role   domain.Role
		result string
	}
	var nodes []node
	for _, role := range domain.Roles() {
		ids := r.ResultIDs(role)
		if len(ids) == 0 {
			t.Fatalf("Rolle %s hat kein Ergebnis", role)
		}
		for _, id := range ids {
			nodes = append(nodes, node{role, id})
			steps, err := r.NextSteps(role, id)
			if err != nil || len(steps) == 0 {
				t.Errorf("%s/%s: kein Folgeschritt (%v)", role, id, err)
				continue
			}
			primary, err := r.NextStep(role, id)
			if err != nil || !reflect.DeepEqual(primary, steps[0]) {
				t.Errorf("%s/%s: NextStep %+v != erster Schritt %+v (%v)", role, id, primary, steps[0], err)
			}
			for _, s := range steps {
				if !s.Action.Valid() {
					t.Errorf("%s/%s: ungültige Aktion %q", role, id, s.Action)
				}
				switch s.Action {
				case domain.ActionConceptWork, domain.ActionImplementation, domain.ActionReview:
					if !decisionAcceptsRole(t, s.Action, s.Role) {
						t.Errorf("%s/%s: Rolle %q passt nicht zur Aktion %s", role, id, s.Role, s.Action)
					}
				}
				if s.Role != "" && (!s.Role.Valid() || len(r.ResultIDs(s.Role)) == 0) {
					t.Errorf("%s/%s: Zielrolle %q unbekannt oder ohne Ergebnisse", role, id, s.Role)
				}
				if slices.Contains(r.GuardedActions(), s.Action) && !slices.Contains(s.Requires, domain.PrecReviewApproved) {
					t.Errorf("%s/%s: %s ohne review_approved", role, id, s.Action)
				}
				for _, p := range s.Requires {
					if !slices.Contains(preconditionKinds(), p) {
						t.Errorf("%s/%s: unbekannte Voraussetzung %q", role, id, p)
					}
				}
			}
		}
	}
	// Sackgassen: Von jedem Ergebnis muss über die Folgeschritte ein Schritt erreichbar sein, der
	// pausiert (Frage an den Nutzer, Warten, Integration). Eine Schleife ohne Ausgang wäre eine Sackgasse.
	reaches := map[node]bool{}
	for changed := true; changed; {
		changed = false
		for _, n := range nodes {
			if reaches[n] {
				continue
			}
			steps, _ := r.NextSteps(n.role, n.result)
			for _, s := range steps {
				ok := slices.Contains(pauseActions, s.Action)
				if !ok && s.Role != "" {
					for _, next := range r.ResultIDs(s.Role) {
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
	for _, n := range nodes {
		if !reaches[n] {
			t.Errorf("Sackgasse: %s/%s erreicht nie Warten, Frage oder Integration", n.role, n.result)
		}
	}

	t.Run("erwartete Übergänge", func(t *testing.T) {
		type want struct {
			role   domain.Role
			result string
			action domain.Action
			to     domain.Role
		}
		for _, w := range []want{
			{domain.RoleImplementer, "implemented", domain.ActionReview, domain.RoleReviewer},
			{domain.RoleReviewer, "approved", domain.ActionRequestIntegration, ""},
			{domain.RoleReviewer, "findings", domain.ActionImplementation, domain.RoleImplementer},
			{domain.RoleConcept, "concept_ready", domain.ActionReview, domain.RoleConceptReviewer},
			{domain.RoleConceptReviewer, "concept_findings", domain.ActionConceptWork, domain.RoleConcept},
			{domain.RoleConceptReviewer, "needs_user_decision", domain.ActionAskQuestion, ""},
			{domain.RoleCompletion, "waiting_integration", domain.ActionWait, ""},
			{domain.RoleQuestionHandler, "no_answer", domain.ActionWait, ""},
		} {
			got, err := r.NextStep(w.role, w.result)
			if err != nil || got.Action != w.action || got.Role != w.to {
				t.Errorf("%s/%s = %+v (%v), erwartet %s/%s", w.role, w.result, got, err, w.action, w.to)
			}
		}
	})
	t.Run("unbekannte Rolle oder Ergebnis", func(t *testing.T) {
		if _, err := r.NextStep("zauberer", "x"); CodeOf(err) != CodeInvalidRequest {
			t.Errorf("Rolle: %v", err)
		}
		if _, err := r.NextStep(domain.RoleReviewer, "vielleicht"); CodeOf(err) != CodeInvalidRequest {
			t.Errorf("Ergebnis: %v", err)
		}
		if _, err := r.NextSteps(domain.RoleReviewer, ""); CodeOf(err) != CodeInvalidRequest {
			t.Errorf("leeres Ergebnis: %v", err)
		}
	})
	t.Run("Regeldatei mit Sackgasse oder Lücke wird abgelehnt", func(t *testing.T) {
		mut := map[string]func(*testing.T, map[string]any){
			"Rolle fehlt":      func(t *testing.T, m map[string]any) { delete(at(t, m, "roles"), "supervisor") },
			"unbekannte Rolle": func(t *testing.T, m map[string]any) { at(t, m, "roles")["zauberer"] = map[string]any{} },
			"Ergebnis ohne next": func(t *testing.T, m map[string]any) {
				at(t, m, "roles", "reviewer", "results", "findings")["next"] = []any{}
			},
			"unbekannte Aktion": func(t *testing.T, m map[string]any) {
				at(t, m, "roles", "reviewer", "results", "findings")["next"] = []any{map[string]any{"action": "deploy"}}
			},
			"Rolle passt nicht zur Aktion": func(t *testing.T, m map[string]any) {
				at(t, m, "roles", "reviewer", "results", "findings")["next"] = []any{map[string]any{"action": "implementation", "role": "reviewer"}}
			},
			"Zielrolle unbekannt": func(t *testing.T, m map[string]any) {
				at(t, m, "roles", "reviewer", "results", "findings")["next"] = []any{map[string]any{"action": "plan_extend", "role": "zauberer"}}
			},
			"Integration ohne review_approved": func(t *testing.T, m map[string]any) {
				at(t, m, "roles", "supervisor", "results", "idle")["next"] = []any{map[string]any{"action": "request_integration"}}
			},
			"Voraussetzung unbekannt": func(t *testing.T, m map[string]any) {
				at(t, m, "roles", "supervisor", "results", "idle")["next"] = []any{map[string]any{"action": "wait", "requires": strs("magie")}}
			},
			"Ergebnisformat unbekannt": func(t *testing.T, m map[string]any) { at(t, m, "roles", "reviewer")["format"] = "gedicht" },
			"Fähigkeit unbekannt":      func(t *testing.T, m map[string]any) { at(t, m, "roles", "reviewer")["may"] = strs("fliegen") },
			"Rolle darf Agenten starten": func(t *testing.T, m map[string]any) {
				at(t, m, "roles", "reviewer")["may"] = strs("read_repo", "start_agents")
			},
			"Urteil unbekannt": func(t *testing.T, m map[string]any) {
				at(t, m, "roles", "reviewer", "results", "approved")["verdict"] = "VIELLEICHT"
			},
			"Sackgasse": func(t *testing.T, m map[string]any) {
				for _, res := range []string{"concept_approved", "concept_findings", "needs_user_decision"} {
					at(t, m, "roles", "concept_reviewer", "results", res)["next"] = []any{map[string]any{"action": "review", "role": "concept_reviewer"}}
				}
			},
		}
		for name, fn := range mut {
			t.Run(name, func(t *testing.T) {
				m := repoRulesMap(t)
				fn(t, m)
				_, err := mustParseRules(t, m)
				if CodeOf(err) != CodeInvalidRules {
					t.Fatalf("Code %q: %v", CodeOf(err), err)
				}
			})
		}
	})
}

// ---- Regeldatei: Form, Strenge und Bezug zum Vertragsbestand -----------------

func TestRulesFileStrictness(t *testing.T) {
	t.Run("echte Regeldatei", func(t *testing.T) {
		r := repoRules(t)
		if len(r.Capabilities()) == 0 {
			t.Fatal("keine Fähigkeiten")
		}
		cats := r.WorkflowCategories()
		want := []Category{CategoryCompletion, CategoryConcept, CategoryDelivery, CategoryQuestions, CategoryReview}
		if !slices.Equal(cats, want) {
			t.Errorf("Workflow-Kategorien %v, erwartet %v", cats, want)
		}
	})
	mut := map[string]func(*testing.T, map[string]any){
		"unbekannter Schlüssel":    func(t *testing.T, m map[string]any) { m["bonus"] = true },
		"schema_version":           func(t *testing.T, m map[string]any) { m["schema_version"] = 2 },
		"Abschnitt platform fehlt": func(t *testing.T, m map[string]any) { delete(m, "platform") },
		"Abschnitt roles fehlt":    func(t *testing.T, m map[string]any) { delete(m, "roles") },
		"Abschnitt formats fehlt":  func(t *testing.T, m map[string]any) { delete(m, "formats") },
		"Kategorie unbekannt": func(t *testing.T, m map[string]any) {
			at(t, m, "workflow_categories")["misc"] = strs("reviewer")
		},
		"Kategorie ohne Rolle":      func(t *testing.T, m map[string]any) { at(t, m, "workflow_categories")["review"] = []any{} },
		"Kategorie Rolle unbekannt": func(t *testing.T, m map[string]any) { at(t, m, "workflow_categories")["review"] = strs("zauberer") },
		"Kategorie fehlt":           func(t *testing.T, m map[string]any) { delete(at(t, m, "workflow_categories"), "completion") },
		"Fähigkeiten doppelt": func(t *testing.T, m map[string]any) {
			m["capabilities"] = append(list(t, m, "capabilities"), "push")
		},
		"always_forbidden fehlt":    func(t *testing.T, m map[string]any) { m["always_forbidden"] = strs("start_agents") },
		"Integration: Wartezustand": func(t *testing.T, m map[string]any) { at(t, m, "integration")["waiting_state"] = "in_progress" },
		"Format ohne Felder":        func(t *testing.T, m map[string]any) { at(t, m, "formats", "review_report")["fields"] = []any{} },
		"Format weicht von ReviewReport ab": func(t *testing.T, m map[string]any) {
			at(t, m, "formats", "review_report")["fields"] = strs("report_id", "verdict")
		},
	}
	names := make([]string, 0, len(mut))
	for n := range mut {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			m := repoRulesMap(t)
			mut[name](t, m)
			_, err := mustParseRules(t, m)
			if CodeOf(err) != CodeInvalidRules && CodeOf(err) != CodePlatformLimit {
				t.Fatalf("Code %q: %v", CodeOf(err), err)
			}
			for _, e := range FindingsOf(err) {
				if e.Path != "rules/roles.yaml" {
					t.Errorf("Befund ohne Pfad: %+v", e)
				}
			}
		})
	}
	t.Run("kein YAML, leer, mehrere Dokumente, doppelter Schlüssel", func(t *testing.T) {
		raw := readRepoFile(t, "rules/roles.yaml")
		for name, in := range map[string]string{
			"leer":                "",
			"kein YAML":           "platform: [unvollständig\n",
			"zwei Dokumente":      raw + "---\nschema_version: 1\n",
			"doppelter Schlüssel": raw + "\nintegration:\n  default: pr\n",
		} {
			if _, err := ParseRules([]byte(in)); CodeOf(err) != CodeInvalidRules {
				t.Errorf("%s: Code %q: %v", name, CodeOf(err), err)
			}
		}
	})
	t.Run("Load: Regeldatei fehlt oder ist ungültig", func(t *testing.T) {
		f := newCatalogFixture(t, defaultTestSkills())
		if err := os.Remove(filepath.Join(f.root, "rules", "roles.yaml")); err != nil {
			t.Fatal(err)
		}
		_, err := f.load()
		wantErr(t, err, CodeMissingRules, "", "rules/roles.yaml")

		f.setRulesRaw("schema_version: 9\n")
		_, err = f.load()
		wantErr(t, err, CodeInvalidRules, "", "rules/roles.yaml")
	})
	t.Run("Load: kein Symlink als Regeldatei", func(t *testing.T) {
		f := newCatalogFixture(t, defaultTestSkills())
		p := filepath.Join(f.root, "rules", "roles.yaml")
		real := filepath.Join(f.root, "rules", "real.yaml")
		if err := os.Rename(p, real); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(real, p); err != nil {
			t.Skip("Symlinks nicht möglich:", err)
		}
		_, err := f.load()
		if err == nil {
			t.Fatal("Symlink als Regeldatei wurde akzeptiert")
		}
	})
}

// TestRulesMatchPlatformDefinitions hält die Regeldatei an den Verträgen der Plattform fest, statt
// sie zu duplizieren: Rollen, Ergebnisformate, Aktions-Rollen-Zuordnung und Voraussetzungsarten.
func TestRulesMatchPlatformDefinitions(t *testing.T) {
	r := repoRules(t)

	t.Run("alle Rollen aus internal/domain", func(t *testing.T) {
		got := r.Roles()
		var want []domain.Role
		want = append(want, domain.Roles()...)
		sort.Slice(want, func(i, j int) bool { return want[i] < want[j] })
		if !slices.Equal(got, want) {
			t.Errorf("Rollen %v, erwartet %v", got, want)
		}
	})
	t.Run("ReviewReport- und Decision-Felder", func(t *testing.T) {
		tags := func(v any) []string {
			var out []string
			rt := reflect.TypeOf(v)
			for i := 0; i < rt.NumField(); i++ {
				name, _, _ := strings.Cut(rt.Field(i).Tag.Get("json"), ",")
				out = append(out, name)
			}
			sort.Strings(out)
			return out
		}
		got := append([]string(nil), r.FormatFields("review_report")...)
		sort.Strings(got)
		if want := tags(domain.ReviewReport{}); !slices.Equal(got, want) {
			t.Errorf("review_report = %v, ReviewReport hat %v", got, want)
		}
		all := tags(domain.Decision{})
		for _, f := range r.FormatFields("decision") {
			if !slices.Contains(all, f) {
				t.Errorf("decision: Feld %q gibt es in domain.Decision nicht", f)
			}
		}
	})
	t.Run("Voraussetzungsarten wie im Decision-Schema", func(t *testing.T) {
		raw, err := os.ReadFile(filepath.Join(repoRoot, "contracts", "decision-v1.schema.json"))
		if err != nil {
			t.Fatal(err)
		}
		var doc map[string]any
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatal(err)
		}
		var kinds []string
		var walk func(v any)
		walk = func(v any) {
			switch x := v.(type) {
			case map[string]any:
				if e, ok := x["enum"].([]any); ok && slices.Contains(e, any("review_approved")) {
					for _, k := range e {
						kinds = append(kinds, fmt.Sprint(k))
					}
				}
				for _, c := range x {
					walk(c)
				}
			case []any:
				for _, c := range x {
					walk(c)
				}
			}
		}
		walk(doc)
		var have []string
		for _, k := range preconditionKinds() {
			have = append(have, string(k))
		}
		sort.Strings(kinds)
		sort.Strings(have)
		if len(kinds) == 0 || !slices.Equal(kinds, have) {
			t.Errorf("Schema %v, Code %v", kinds, have)
		}
	})
	t.Run("Aktions-Rollen-Zuordnung wie domain.Decision.Check", func(t *testing.T) {
		for _, action := range domain.DecisionActions() {
			roles, selection := actionRoles(action)
			if !selection {
				continue
			}
			for _, role := range domain.Roles() {
				if got, want := slices.Contains(roles, role), decisionAcceptsRole(t, action, role); got != want {
					t.Errorf("%s/%s: Tabelle %v, Decision.Check %v", action, role, got, want)
				}
			}
		}
	})
}

// ---- Manifest: Skills ohne Originalpaket -------------------------------------

func TestManifestSourceWithoutPackage(t *testing.T) {
	zeros := strings.Repeat("0", 64)
	set := func(e *manifestEntry, pkg, sum string) { e.Source.Package, e.Source.PackageSHA256 = pkg, sum }

	t.Run("neuer Skill ohne Paket ist gültig", func(t *testing.T) {
		f := newCatalogFixture(t, defaultTestSkills())
		set(f.entry("ccmas-demo-know"), NoPackage, NoPackageSHA256)
		f.entry("ccmas-demo-know").Source.Changes = "Neu in CC-33, kein Originalpaket."
		f.writeManifest()
		c := f.mustLoad(fixedClock())
		sk, _ := c.Skill("ccmas-demo-know")
		if sk.Source.Package != NoPackage || sk.Source.PackageSHA256 != NoPackageSHA256 || NoPackageSHA256 != zeros {
			t.Errorf("Source = %+v", sk.Source)
		}
		// Ein Snapshot bleibt gültig und bekommt dieselbe Kennzeichnung.
		snap, err := c.Resolve(domain.RoleConcept, []string{"ccmas-demo-know"})
		if err != nil {
			t.Fatal(err)
		}
		dir := t.TempDir()
		makeWritable(t, dir)
		path, err := snap.Write(dir)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := VerifySnapshotDir(path); err != nil {
			t.Errorf("Snapshot: %v", err)
		}
	})
	for name, tc := range map[string]struct{ pkg, sum string }{
		"Kennzeichnung mit echtem Hash":  {NoPackage, strings.Repeat("ab", 32)},
		"Nullhash mit echtem Paketpfad":  {"Fachwissen/Claude/Skills/x.skill", zeros},
		"Kennzeichnung ohne Prüfsumme":   {NoPackage, ""},
		"Paketpfad leer, Prüfsumme echt": {"", strings.Repeat("ab", 32)},
	} {
		t.Run("ungültig: "+name, func(t *testing.T) {
			f := newCatalogFixture(t, defaultTestSkills())
			set(f.entry("ccmas-demo-know"), tc.pkg, tc.sum)
			f.writeManifest()
			_, err := f.load()
			wantErr(t, err, CodeInvalidManifest, "ccmas-demo-know", "")
		})
	}
}
