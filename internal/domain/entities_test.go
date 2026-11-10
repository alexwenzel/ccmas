package domain

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

func testCatalog() Catalog {
	return Catalog{
		Models: map[string][]Effort{
			"model-a": {EffortLow, EffortMedium, EffortHigh},
			"model-b": {EffortLow, EffortMedium, EffortHigh, EffortXHigh, EffortMax},
		},
		Skills:   map[string]bool{"ccmas-feature-parallel": true},
		Policies: map[string]bool{"default": true},
	}
}

func TestCatalogDifferentiatesModelAndEffort(t *testing.T) {
	c := testCatalog()
	if err := c.CheckModelEffort("model-b", EffortMax); err != nil {
		t.Fatal(err)
	}
	wantCategory(t, c.CheckModelEffort("model-unknown", EffortLow), CatModel)
	// Gültige Stufe, aber vom Modell nicht unterstützt: Effort-Fehler, kein Modellfehler.
	wantCategory(t, c.CheckModelEffort("model-a", EffortMax), CatEffort)
	wantCategory(t, c.CheckModelEffort("model-a", Effort("ultra")), CatEffort)

	// Keine festen Modell-IDs im Code: ein leerer Katalog kennt kein Modell.
	wantCategory(t, Catalog{}.CheckModelEffort("model-a", EffortLow), CatModel)
}

func TestCatalogCheckProfile(t *testing.T) {
	c := testCatalog()
	p := AgentProfile{
		ProfileID: "impl", Version: 1, Role: RoleImplementer, Runtime: RuntimeClaude, Model: "model-a",
		Effort: EffortHigh, SkillIDs: []string{"ccmas-feature-parallel"}, PolicyProfile: "default",
	}
	if err := c.CheckProfile(p); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		mutate func(p *AgentProfile)
		want   Category
	}{
		"Rolle":             {func(p *AgentProfile) { p.Role = "wizard" }, CatRole},
		"Modell":            {func(p *AgentProfile) { p.Model = "model-x" }, CatModel},
		"Effort":            {func(p *AgentProfile) { p.Model = "model-a"; p.Effort = EffortMax }, CatEffort},
		"Skill":             {func(p *AgentProfile) { p.SkillIDs = []string{"fremd"} }, CatValue},
		"unbekannte Policy": {func(p *AgentProfile) { p.PolicyProfile = "wild" }, CatValue},
	} {
		t.Run(name, func(t *testing.T) {
			bad := p
			tc.mutate(&bad)
			wantCategory(t, c.CheckProfile(bad), tc.want)
		})
	}
}

func TestDecisionCheck(t *testing.T) {
	ctx := DecisionContext{ProjectID: "proj-1", StateRevision: 7, Catalog: testCatalog()}
	parse := func(t *testing.T, mutate func(m map[string]any)) Decision {
		t.Helper()
		m := decisionFor("implementation")
		if mutate != nil {
			mutate(m)
		}
		d, err := ParseDecision(mustJSON(t, m))
		if err != nil {
			t.Fatalf("Entscheidung formal ungültig: %v", err)
		}
		return d
	}

	if err := parse(t, nil).Check(ctx); err != nil {
		t.Fatalf("gültige Entscheidung: %v", err)
	}
	sel := func(m map[string]any) map[string]any { return m["selection"].(map[string]any) }

	cases := map[string]struct {
		mutate func(m map[string]any)
		want   Category
	}{
		"veraltete Revision":           {func(m map[string]any) { m["state_revision"] = 6 }, CatRevision},
		"anderes Projekt":              {func(m map[string]any) { m["project_id"] = "proj-2" }, CatID},
		"unbekanntes Modell":           {func(m map[string]any) { sel(m)["model"] = "model-x" }, CatModel},
		"Effort nicht unterstützt":     {func(m map[string]any) { sel(m)["model"] = "model-a"; sel(m)["effort"] = "max" }, CatEffort},
		"unbekannter Skill":            {func(m map[string]any) { sel(m)["skill_ids"] = []any{"fremd"} }, CatValue},
		"Rolle passt nicht zur Aktion": {func(m map[string]any) { sel(m)["role"] = "reviewer" }, CatRole},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			wantCategory(t, parse(t, tc.mutate).Check(ctx), tc.want)
		})
	}

	// Formal gültiges JSON ist allein keine Ausführungsfreigabe: ohne Katalog scheitert die Prüfung.
	wantCategory(t, parse(t, nil).Check(DecisionContext{ProjectID: "proj-1", StateRevision: 7}), CatModel)

	// Aktionen ohne Auswahl brauchen keinen Katalog.
	wait, err := ParseDecision(mustJSON(t, decisionFor("wait")))
	if err != nil {
		t.Fatal(err)
	}
	if err := wait.Check(DecisionContext{ProjectID: "proj-1", StateRevision: 7}); err != nil {
		t.Fatal(err)
	}
}

func TestProjectConfigDefaultsAndLimits(t *testing.T) {
	min := `{"schema_version":1,"project_id":"proj-1","code_dir":"/work/code","execution_dir":"/work/exec",
	"repository":"owner/name","base_branch":"develop",
	"vault":{"id":"vault-1","path":"/vault","project_area":"Eigene Projekte/x"},"asana_project_gid":"1220359723506992"}`
	cfg, err := ParseProjectConfig([]byte(min))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxWorkers != 3 || cfg.DefaultMode != ModePR || cfg.BaseBranch != "develop" {
		t.Fatalf("Standardwerte falsch: %+v", cfg)
	}
	if cfg.Resources.MaxParallelBuilds != 1 || cfg.Resources.MonitorIntervalSeconds != 5 ||
		cfg.Resources.PlanningIntervalSeconds != 60 || cfg.Resources.AttemptRuntimeMinutes != 60 {
		t.Fatalf("Ressourcenvorgaben falsch: %+v", cfg.Resources)
	}
	if err := cfg.Check(); err != nil {
		t.Fatal(err)
	}

	for _, n := range []int{1, 16} {
		withWorkers := strings.Replace(min, `"base_branch"`, `"max_workers":`+strconv.Itoa(n)+`,"base_branch"`, 1)
		got, err := ParseProjectConfig([]byte(withWorkers))
		if err != nil || got.MaxWorkers != n {
			t.Fatalf("max_workers=%d: %v %+v", n, err, got)
		}
	}
	for _, n := range []int{0, 17, -1} {
		withWorkers := strings.Replace(min, `"base_branch"`, `"max_workers":`+strconv.Itoa(n)+`,"base_branch"`, 1)
		_, err := ParseProjectConfig([]byte(withWorkers))
		wantCategory(t, err, CatLimit)
	}

	// Der Basisbranch ist fest develop; kein stiller Ersatz durch main.
	_, err = ParseProjectConfig([]byte(strings.Replace(min, `"develop"`, `"main"`, 1)))
	wantCategory(t, err, CatValue)

	// Go erzwingt kanonische Pfade auch für direkt gebaute Strukturen.
	bad := cfg
	bad.CodeDir = "/work/../etc"
	wantCategory(t, bad.Check(), CatPath)
	bad = cfg
	bad.ExecutionDir = "relativ/pfad"
	wantCategory(t, bad.Check(), CatPath)
	bad = cfg
	bad.Vault.ProjectArea = "/absolut"
	wantCategory(t, bad.Check(), CatPath)
	bad = cfg
	bad.MaxWorkers = 17
	wantCategory(t, bad.Check(), CatLimit)
}

func TestCanonicalPath(t *testing.T) {
	ok := []string{"/", "/a", "/a/b.c", "/Users/x/Library/Mobile Documents/y"}
	for _, p := range ok {
		if err := CheckCanonicalPath(p); err != nil {
			t.Errorf("%q: %v", p, err)
		}
	}
	bad := []string{"", "a", "./a", "/a/", "/a//b", "/a/./b", "/a/../b", "/..", "~/x", "/a\x00b", "/a\nb"}
	for _, p := range bad {
		wantCategory(t, CheckCanonicalPath(p), CatPath)
	}
}

func TestReviewReportBinding(t *testing.T) {
	head := strings.Repeat("a", 40)
	report := ReviewReport{
		ReportID: "rep-1", WorkOrderID: "wo-1", ReviewerExecutionID: "exec-r", Commit: head,
		ConceptVersion: "1.2.0", Verdict: VerdictApproved, Findings: []Finding{},
		CreatedAt: time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC),
	}
	if err := report.Check(); err != nil {
		t.Fatal(err)
	}
	req := IntegrationRequest{
		RequestID: "int-1", DecisionID: "dec-1", WorkOrderID: "wo-1", Mode: ModePR, BaseBranch: "develop",
		HeadCommit: head, Branch: "feature/CC-24-vertraege", ReviewReportID: "rep-1",
		ConceptVersion: "1.2.0", CreatedAt: report.CreatedAt,
	}
	if err := req.CheckReview(report); err != nil {
		t.Fatalf("passender Beleg: %v", err)
	}

	// Verändert sich der Stand, ist die alte Freigabe nicht übertragbar.
	moved := req
	moved.HeadCommit = strings.Repeat("b", 40)
	wantCategory(t, moved.CheckReview(report), CatCommit)

	otherConcept := req
	otherConcept.ConceptVersion = "1.3.0"
	wantCategory(t, otherConcept.CheckReview(report), CatValue)

	otherOrder := req
	otherOrder.WorkOrderID = "wo-2"
	wantCategory(t, otherOrder.CheckReview(report), CatID)

	findings := report
	findings.Verdict = VerdictFindings
	findings.Findings = []Finding{{FindingID: "f-1", Severity: SeverityMajor, Summary: "Fehler"}}
	if err := findings.Check(); err != nil {
		t.Fatal(err)
	}
	wantCategory(t, req.CheckReview(findings), CatValue)

	// Urteil und Befunde müssen zusammenpassen.
	inconsistent := report
	inconsistent.Findings = findings.Findings
	wantCategory(t, inconsistent.Check(), CatValue)
	empty := findings
	empty.Findings = nil
	wantCategory(t, empty.Check(), CatValue)
}

func TestQuestionAnswerFlow(t *testing.T) {
	created := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	q := Question{
		QuestionID: "q-1", WorkOrderID: "wo-1", ExecutionID: "exec-1", Attempt: 1,
		Text: "Welche Variante?", Context: "Kontext", State: QuestionOpen, CreatedAt: created,
	}
	a := Answer{AnswerID: "a-1", QuestionID: "q-1", Text: "Variante A", CreatedAt: created.Add(time.Minute)}

	// Keine Antwort ist keine Freigabe: Zustellung ohne Antwort ist unmöglich.
	wantCategory(t, q.ConfirmDelivery(created.Add(2*time.Minute)), CatState)

	wrong := a
	wrong.QuestionID = "q-other"
	wantCategory(t, q.Accept(wrong), CatID)
	if q.State != QuestionOpen {
		t.Fatal("falsche Antwort änderte den Zustand")
	}

	if err := q.Accept(a); err != nil {
		t.Fatal(err)
	}
	if q.State != QuestionAnswered || q.AnswerID != "a-1" {
		t.Fatalf("Zustand %s, Antwort %q", q.State, q.AnswerID)
	}
	// Eine zweite, widersprüchliche Antwort wird nicht stillschweigend übernommen.
	second := a
	second.AnswerID = "a-2"
	wantCategory(t, q.Accept(second), CatState)
	if q.AnswerID != "a-1" {
		t.Fatal("zweite Antwort überschrieb die erste")
	}

	if err := q.ConfirmDelivery(created.Add(2 * time.Minute)); err != nil {
		t.Fatal(err)
	}
	if q.State != QuestionDelivered || q.DeliveryConfirmedAt.IsZero() {
		t.Fatalf("Zustand %s", q.State)
	}
	wantCategory(t, q.ConfirmDelivery(created.Add(3*time.Minute)), CatState)
}

func TestAgentSnapshotCheck(t *testing.T) {
	snap := AgentSnapshot{
		AgentID: "agent-1", Role: RoleImplementer, ProfileID: "impl", ProfileVersion: 1,
		Availability: AgentActive, ExecutionID: "exec-1", ExecutionState: ExecRunning,
		WorkOrderID: "wo-1", WorkOrderState: WOInProgress, Model: "model-a", Effort: EffortHigh,
		SampledAt: time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC),
	}
	if err := snap.Check(); err != nil {
		t.Fatal(err)
	}
	idle := AgentSnapshot{AgentID: "agent-2", Role: RoleReviewer, ProfileID: "rev", ProfileVersion: 1, Availability: AgentAvailable, SampledAt: snap.SampledAt}
	if err := idle.Check(); err != nil {
		t.Fatalf("freier Agent ohne Ausführung: %v", err)
	}
	// Ein aktiver Agent ohne Ausführung widerspricht sich.
	bad := idle
	bad.Availability = AgentActive
	wantCategory(t, bad.Check(), CatState)
	// Ein freier Agent mit laufender Ausführung ebenso.
	bad = snap
	bad.Availability = AgentAvailable
	wantCategory(t, bad.Check(), CatState)
	bad = snap
	bad.ExecutionState = "kaputt"
	wantCategory(t, bad.Check(), CatState)
}

func TestCommandEncodingValidatesBeforeSend(t *testing.T) {
	env := Envelope{
		ProtocolVersion: ProtocolVersion, ProjectID: "proj-1", ExecutionID: "exec-1", Attempt: 1,
		CorrelationID: "corr-1", Type: MsgStart, Timestamp: mustTime(t, "2026-11-10T12:00:00Z"),
	}
	spec := RunSpec{
		WorkOrderID: "wo-1", Role: RoleImplementer, Runtime: RuntimeClaude, Model: "model-a", Effort: EffortHigh,
		SkillIDs: []string{"ccmas-feature-parallel"}, PolicyProfile: "default",
		Cwd: "/work/exec/wt", ClaudeBinary: "/usr/local/bin/claude", Prompt: "Aufgabe", MaxRuntimeSeconds: 3600,
	}
	raw, err := EncodeRunCommand(env, spec)
	if err != nil {
		t.Fatal(err)
	}
	cmd, err := ParseRunCommand(raw)
	if err != nil {
		t.Fatalf("selbst erzeugte Nachricht muss lesbar sein: %v", err)
	}
	got, ok := cmd.Payload.(RunSpec)
	if !ok || got.Model != "model-a" || cmd.Envelope.Type != MsgStart {
		t.Fatalf("Round-Trip: %#v", cmd)
	}

	// Ungültige Inhalte werden nicht gesendet.
	bad := spec
	bad.Cwd = "relativ"
	_, err = EncodeRunCommand(env, bad)
	wantCategory(t, err, CatPath)
	bad = spec
	bad.Effort = "ultra"
	_, err = EncodeRunCommand(env, bad)
	wantCategory(t, err, CatEffort)
	// Eine Antwort wird ohne Zustellbestätigung gesendet; die Bestätigung entsteht erst danach.
	answerEnv := env
	answerEnv.Type = MsgAnswer
	answer := Answer{AnswerID: "a-1", QuestionID: "q-1", Text: "Variante A", CreatedAt: mustTime(t, "2026-11-10T12:05:00Z")}
	if _, err := EncodeRunCommand(answerEnv, answer); err != nil {
		t.Fatalf("Antwort: %v", err)
	}
	confirmed := mustTime(t, "2026-11-10T12:06:00Z")
	answer.DeliveryConfirmedAt = &confirmed
	_, err = EncodeRunCommand(answerEnv, answer)
	wantCategory(t, err, CatValue)
	// Typ und Payload müssen zusammenpassen.
	wrongType := env
	wrongType.Type = MsgStop
	_, err = EncodeRunCommand(wrongType, spec)
	wantCategory(t, err, CatMessageType)
	// Nur Go->Python-Typen werden gesendet.
	fromRunner := env
	fromRunner.Type = MsgResult
	_, err = EncodeRunCommand(fromRunner, spec)
	wantCategory(t, err, CatMessageType)
}
