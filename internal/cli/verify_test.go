package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/alexwenzel/ccmas/internal/domain"
	"github.com/alexwenzel/ccmas/internal/workers/runner"
)

// verifyEnv ist eine Umgebung für profile verify: initialisierte Profile, Runner-Projekt, "uv" und
// "claude" als Attrappen im PATH. Die Attrappen legen eine Markerdatei an, wenn sie ausgeführt werden:
// ohne --live darf nichts davon geschehen.
type verifyEnv struct {
	*profileEnv
	marker string
	claude string
}

func newVerifyEnv(t *testing.T) *verifyEnv {
	t.Helper()
	e := &verifyEnv{profileEnv: newProfileEnv(t).initialized()}
	runnerDir := filepath.Join(e.root, "runners", "claude_sdk")
	if err := os.MkdirAll(runnerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"pyproject.toml", "uv.lock"} {
		if err := os.WriteFile(filepath.Join(runnerDir, f), []byte("# test\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	bin := t.TempDir()
	e.marker = filepath.Join(bin, "ausgefuehrt")
	script := "#!/bin/sh\necho \"$0\" >> '" + e.marker + "'\nexit 0\n"
	for _, n := range []string{"uv", "claude"} {
		if err := os.WriteFile(filepath.Join(bin, n), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	e.claude, _ = filepath.EvalSymlinks(filepath.Join(bin, "claude"))
	t.Setenv("PATH", bin)
	t.Setenv(runner.EnvClaudeBinary, "")
	// Ohne Absicherung darf kein Test einen echten Runner starten.
	old := newLiveExecutor
	newLiveExecutor = func(runner.Config) liveExecutor { return &fakeExecutor{t: t, fail: true} }
	t.Cleanup(func() { newLiveExecutor = old })
	return e
}

func (e *verifyEnv) assertNothingExecuted(t *testing.T) {
	t.Helper()
	if _, err := os.Stat(e.marker); err == nil {
		t.Error("ein Prozess (uv oder claude) wurde ausgeführt")
	}
}

// fakeExecutor ist ein Test-Double des Runner-Adapters: es zeichnet die Aufträge auf und liefert
// vorbereitete Datensätze. Der echte Adapter ist in internal/workers/runner mit echten Prozessen getestet.
type fakeExecutor struct {
	t     *testing.T
	fail  bool // jeder Aufruf ist ein Testfehler
	mu    sync.Mutex
	reqs  []runner.Request
	cwds  []bool // existierte das Sitzungsverzeichnis beim Aufruf?
	reply func(runner.Request) runner.Execution
}

func (f *fakeExecutor) Execute(_ context.Context, req runner.Request) (runner.Execution, error) {
	if f.fail {
		f.t.Errorf("der Runner-Adapter wurde aufgerufen (Modell %s)", req.Profile.Model)
		return runner.Execution{}, os.ErrInvalid
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	st, err := os.Stat(req.Cwd)
	f.cwds = append(f.cwds, err == nil && st.IsDir())
	f.reqs = append(f.reqs, req)
	return f.reply(req), nil
}

func (e *verifyEnv) useExecutor(t *testing.T, reply func(runner.Request) runner.Execution) *fakeExecutor {
	t.Helper()
	f := &fakeExecutor{t: t, reply: reply}
	newLiveExecutor = func(runner.Config) liveExecutor { return f }
	return f
}

func okExecution(req runner.Request, effectiveModel string) runner.Execution {
	ss := domain.SessionStarted{SessionID: "sess-" + req.Profile.Model, Model: effectiveModel, Effort: req.Profile.Effort,
		ClaudeVersion: "2.1.311", AuthSource: domain.AuthClaudeAI}
	res := domain.Result{Outcome: domain.OutcomeSucceeded, Summary: "ok"}
	return runner.Execution{
		ProjectID: req.ProjectID, ExecutionID: req.ExecutionID, Attempt: 1, State: domain.ExecSucceeded,
		SessionStarted: &ss,
		Events:         []domain.RunEvent{{Payload: ss}, {Payload: res}},
		Terminal:       &domain.TerminalResult{Attempt: 1, State: domain.ExecSucceeded, Source: "result"},
	}
}

type verifyJSON struct {
	OK     bool `json:"ok"`
	Live   bool `json:"live"`
	Checks []struct {
		Name, Level, Status, Detail string
	} `json:"checks"`
	LiveRuns []struct {
		Model          string   `json:"model"`
		Roles          []string `json:"roles"`
		Status         string   `json:"status"`
		EffortAsked    string   `json:"effort_requested"`
		EffectiveModel string   `json:"effective_model"`
		SessionID      string   `json:"session_id"`
		Code           string   `json:"code"`
		Diagnosis      string   `json:"diagnosis"`
	} `json:"live_runs"`
}

func (v verifyJSON) check(name string) (struct{ Name, Level, Status, Detail string }, bool) {
	for _, c := range v.Checks {
		if c.Name == name {
			return c, true
		}
	}
	return struct{ Name, Level, Status, Detail string }{}, false
}

func TestVerifyOfflineStartsNoProcessAndUsesNoQuota(t *testing.T) {
	e := newVerifyEnv(t)
	e.ok("agent", "set", "impl-1", "--role", "implementer", "--effort", "medium")
	out := e.ok("profile", "verify")
	e.assertNothingExecuted(t)
	for _, want := range []string{"[ok]", "vorhanden", "konfiguriert", "funktional nachgewiesen", "Rolle supervisor", "Agent impl-1",
		"nur mit --live", "Ergebnis: in Ordnung", "Runner-Projekt", "Claude-Binary"} {
		if !strings.Contains(out, want) {
			t.Errorf("Ausgabe enthält %q nicht:\n%s", want, out)
		}
	}
	var v verifyJSON
	decode(t, e.ok("profile", "verify", "--json"), &v)
	if !v.OK || v.Live || len(v.LiveRuns) != 0 {
		t.Errorf("JSON: %+v", v)
	}
	for _, r := range domain.Roles() {
		c, ok := v.check("Rolle " + string(r))
		if !ok || c.Status != "ok" || c.Level != levelConfigured {
			t.Errorf("Rolle %s: %+v, %v", r, c, ok)
		}
	}
	if c, ok := v.check("Agent impl-1"); !ok || c.Status != "ok" {
		t.Errorf("Agent: %+v", c)
	}
	if c, _ := v.check("Claude-Binary"); c.Level != levelPresent || c.Detail != e.claude {
		t.Errorf("Claude-Binary: %+v (erwartet %s)", c, e.claude)
	}
	if c, _ := v.check("Live-Lauf je Modell"); c.Status != statusNotChecked || c.Level != levelFunctional {
		t.Errorf("Live-Lauf: %+v", c)
	}
	e.assertNothingExecuted(t)
}

func TestVerifyRoleFilterRestrictsRolesAndAgents(t *testing.T) {
	e := newVerifyEnv(t)
	e.ok("agent", "set", "impl-1", "--role", "implementer")
	e.ok("agent", "set", "rev-1", "--role", "reviewer")
	var v verifyJSON
	decode(t, e.ok("profile", "verify", "--role", "reviewer", "--json"), &v)
	if _, ok := v.check("Rolle reviewer"); !ok {
		t.Error("Rolle reviewer fehlt")
	}
	if _, ok := v.check("Rolle supervisor"); ok {
		t.Error("Rolle supervisor darf nicht geprüft werden")
	}
	if _, ok := v.check("Agent rev-1"); !ok {
		t.Error("Agent der Rolle fehlt")
	}
	if _, ok := v.check("Agent impl-1"); ok {
		t.Error("Agent einer anderen Rolle darf nicht geprüft werden")
	}
	code, _, errOut := e.run("profile", "verify", "--role", "wizard")
	wantFail(t, code, errOut, ExitUsage, "wizard")
	e.assertNothingExecuted(t)
}

func TestVerifyReportsFindingsWithExitCodeOne(t *testing.T) {
	t.Run("uv fehlt", func(t *testing.T) {
		e := newVerifyEnv(t)
		t.Setenv("PATH", t.TempDir())
		t.Setenv(runner.EnvClaudeBinary, e.claude)
		var v verifyJSON
		code, out, _ := e.run("profile", "verify", "--json")
		decode(t, out, &v)
		if code != ExitError || v.OK {
			t.Errorf("Exit %d, ok=%v", code, v.OK)
		}
		if c, _ := v.check("uv"); c.Status != statusFail {
			t.Errorf("uv: %+v", c)
		}
		if c, _ := v.check("Claude-Binary"); c.Status != statusOK {
			t.Errorf("Claude-Binary über %s: %+v", runner.EnvClaudeBinary, c)
		}
	})
	t.Run("Claude-Binary fehlt", func(t *testing.T) {
		e := newVerifyEnv(t)
		t.Setenv("PATH", filepath.Dir(e.claude)) // uv vorhanden
		code, out, _ := e.run("profile", "verify", "--claude-binary", filepath.Join(t.TempDir(), "gibt-es-nicht"), "--json")
		var v verifyJSON
		decode(t, out, &v)
		if c, _ := v.check("Claude-Binary"); code != ExitError || c.Status != statusFail {
			t.Errorf("Exit %d, Claude-Binary: %+v", code, c)
		}
	})
	t.Run("Runner-Projekt fehlt", func(t *testing.T) {
		e := newVerifyEnv(t)
		if err := os.RemoveAll(filepath.Join(e.root, "runners")); err != nil {
			t.Fatal(err)
		}
		code, out, _ := e.run("profile", "verify", "--json")
		var v verifyJSON
		decode(t, out, &v)
		if c, _ := v.check("Runner-Projekt"); code != ExitError || c.Status != statusFail {
			t.Errorf("Exit %d, Runner-Projekt: %+v", code, c)
		}
	})
	t.Run("nicht initialisiert", func(t *testing.T) {
		e := newVerifyEnv(t)
		e.dir = filepath.Join(t.TempDir(), "leer")
		code, out, _ := e.run("profile", "verify", "--json")
		var v verifyJSON
		decode(t, out, &v)
		if c, _ := v.check("agents.yaml"); code != ExitError || c.Status != statusFail || !strings.Contains(c.Detail, "not_initialized") {
			t.Errorf("Exit %d, agents.yaml: %+v", code, c)
		}
	})
	t.Run("Agent nicht auflösbar", func(t *testing.T) {
		e := newVerifyEnv(t)
		e.ok("agent", "set", "a1", "--role", "implementer", "--effort", "max")
		// Von Hand geänderte Modellfähigkeiten: Sonnet kann max nicht mehr.
		e.write("models.yaml", modelsWithLimitedSonnet(false))
		code, out, _ := e.run("profile", "verify", "--json")
		var v verifyJSON
		decode(t, out, &v)
		c, _ := v.check("Agent a1")
		if code != ExitError || c.Status != statusFail || !strings.Contains(c.Detail, "unsupported_effort") {
			t.Errorf("Exit %d, Agent: %+v", code, c)
		}
		if c, _ := v.check("Rolle supervisor"); c.Status != statusOK {
			t.Errorf("andere Rollen bleiben geprüft: %+v", c)
		}
	})
	e := newVerifyEnv(t)
	e.assertNothingExecuted(t)
}

func TestVerifyLiveRunsOneMinimalRunPerUniqueModel(t *testing.T) {
	e := newVerifyEnv(t)
	f := e.useExecutor(t, func(req runner.Request) runner.Execution { return okExecution(req, req.Profile.Model) })
	var v verifyJSON
	decode(t, e.ok("profile", "verify", "--live", "--json"), &v)
	if !v.OK || !v.Live {
		t.Fatalf("JSON: %+v", v)
	}
	// Startprofile: opus (supervisor, reviewer, concept_reviewer) und sonnet (übrige) = 2 Modelle.
	if len(f.reqs) != 2 || len(v.LiveRuns) != 2 {
		t.Fatalf("Läufe: %d Aufträge, %d Ergebnisse, erwartet 2", len(f.reqs), len(v.LiveRuns))
	}
	models := map[string]bool{}
	ids := map[string]bool{}
	for i, req := range f.reqs {
		models[req.Profile.Model] = true
		ids[req.ExecutionID] = true
		p := req.Profile
		if p.PolicyProfile != "readonly" || len(p.SkillIDs) != 0 || p.SkillIDs == nil || p.Effort != domain.EffortLow || p.RequestedEffort != domain.EffortLow {
			t.Errorf("Profil des Minimallaufs: %+v", p)
		}
		if req.Catalog != nil || req.SnapshotDir != "" {
			t.Errorf("verify darf keinen Snapshot anfordern")
		}
		if req.Attempt != 1 || req.ClaudeBinary != e.claude || len(req.Prompt) > 60 || req.MaxRuntimeSeconds <= 0 || req.MaxRuntimeSeconds > 300 {
			t.Errorf("Auftrag: %+v", req)
		}
		if !f.cwds[i] {
			t.Error("das Sitzungsverzeichnis existierte beim Aufruf nicht")
		}
		if _, err := os.Stat(req.Cwd); err == nil {
			t.Error("das Sitzungsverzeichnis wurde nicht aufgeräumt")
		}
		if strings.HasPrefix(e.root, req.Cwd) || strings.HasPrefix(req.Cwd, e.root) {
			t.Errorf("Sitzungsverzeichnis %s im Softwareordner", req.Cwd)
		}
	}
	if !models["claude-opus-5-5"] || !models["claude-sonnet-5-5"] || len(ids) != 2 {
		t.Errorf("Modelle %v, IDs %v", models, ids)
	}
	for _, l := range v.LiveRuns {
		if l.Status != "ok" || l.EffectiveModel != l.Model || l.SessionID == "" || l.EffortAsked != "low" {
			t.Errorf("Lauf: %+v", l)
		}
	}
	if c, _ := v.check("Live-Lauf je Modell"); c.Status == statusNotChecked {
		t.Error("mit --live darf der Live-Lauf nicht als nicht geprüft gelten")
	}
	text := e.ok("profile", "verify", "--live")
	if !strings.Contains(text, "Live-Läufe") || !strings.Contains(text, "sess-claude-opus-5-5") || !strings.Contains(text, "nicht den Effort") {
		t.Errorf("Text:\n%s", text)
	}
}

// Die Sitzungswerte skills und skill_snapshot_id aus session_started stehen im Live-Ergebnis, damit
// die Belegzeilen sie übernehmen können, statt sie fest zu schreiben.
func TestVerifyLiveCarriesSessionSkills(t *testing.T) {
	t.Run("ohne Skills: leere Liste, keine Snapshot-ID", func(t *testing.T) {
		e := newVerifyEnv(t)
		e.useExecutor(t, func(req runner.Request) runner.Execution { return okExecution(req, req.Profile.Model) })
		var raw struct {
			LiveRuns []map[string]any `json:"live_runs"`
		}
		decode(t, e.ok("profile", "verify", "--live", "--role", "documentation", "--json"), &raw)
		l := raw.LiveRuns[0]
		if sk, ok := l["session_skills"].([]any); !ok || len(sk) != 0 {
			t.Errorf("session_skills = %#v, erwartet []", l["session_skills"])
		}
		if _, has := l["skill_snapshot_id"]; has {
			t.Errorf("skill_snapshot_id ohne Snapshot vorhanden: %v", l["skill_snapshot_id"])
		}
	})
	t.Run("Sitzung meldet Skills: Befund und Werte im Ergebnis", func(t *testing.T) {
		e := newVerifyEnv(t)
		e.useExecutor(t, func(req runner.Request) runner.Execution {
			ex := okExecution(req, req.Profile.Model)
			ex.SessionStarted.Skills, ex.SessionStarted.SkillSnapshotID = []string{"ccmas-x"}, "sha256:"+strings.Repeat("ab", 32)
			return ex
		})
		code, out, errOut := e.run("profile", "verify", "--live", "--role", "documentation", "--json")
		wantFail(t, code, errOut, ExitError, "Befund")
		var raw struct {
			LiveRuns []struct {
				Code       string   `json:"code"`
				Skills     []string `json:"session_skills"`
				SnapshotID string   `json:"skill_snapshot_id"`
			} `json:"live_runs"`
		}
		decode(t, out, &raw)
		l := raw.LiveRuns[0]
		if l.Code != "skills_loaded" || len(l.Skills) != 1 || l.Skills[0] != "ccmas-x" || l.SnapshotID != "sha256:"+strings.Repeat("ab", 32) {
			t.Errorf("Ergebnis = %+v", l)
		}
	})
	t.Run("ohne Sitzung: session_skills ist null", func(t *testing.T) {
		e := newVerifyEnv(t)
		e.useExecutor(t, func(req runner.Request) runner.Execution {
			ex := okExecution(req, req.Profile.Model)
			ex.SessionStarted, ex.State = nil, domain.ExecControlledFailure
			ex.Events = []domain.RunEvent{{Payload: domain.ErrorPayload{Code: "not_logged_in", Message: "keine Anmeldung", Kind: domain.ErrKindRuntime, Terminal: true}}}
			ex.Terminal = &domain.TerminalResult{Attempt: 1, State: domain.ExecControlledFailure, Source: "error"}
			return ex
		})
		_, out, _ := e.run("profile", "verify", "--live", "--role", "documentation", "--json")
		var raw struct {
			LiveRuns []map[string]any `json:"live_runs"`
		}
		decode(t, out, &raw)
		if v, has := raw.LiveRuns[0]["session_skills"]; !has || v != nil {
			t.Errorf("session_skills = %#v, erwartet null", v)
		}
	})
}

func TestVerifyLiveSingleModelRunsOnce(t *testing.T) {
	e := newVerifyEnv(t)
	for _, r := range domain.Roles() {
		if r == domain.RoleImplementer || r == domain.RoleReviewer {
			continue
		}
		e.ok("profile", "set", string(r), "--model", "claude-sonnet-5-5")
	}
	f := e.useExecutor(t, func(req runner.Request) runner.Execution { return okExecution(req, req.Profile.Model) })
	e.ok("profile", "verify", "--live", "--role", "supervisor", "--role", "concept", "--role", "documentation")
	if len(f.reqs) != 1 || f.reqs[0].Profile.Model != "claude-sonnet-5-5" {
		t.Errorf("Aufträge: %d", len(f.reqs))
	}
}

func TestVerifyLiveDiagnoses(t *testing.T) {
	cases := map[string]struct {
		reply func(runner.Request) runner.Execution
		parts []string
		code  string
	}{
		"wirksames Modell weicht ab": {
			reply: func(req runner.Request) runner.Execution { return okExecution(req, "claude-anderes-5-5") },
			parts: []string{"claude-anderes-5-5", "weicht"}, code: "model_mismatch",
		},
		"nicht angemeldet": {
			reply: func(req runner.Request) runner.Execution {
				ex := okExecution(req, req.Profile.Model)
				ex.SessionStarted, ex.State = nil, domain.ExecControlledFailure
				ex.Events = []domain.RunEvent{{Payload: domain.ErrorPayload{Code: "not_logged_in", Message: "keine Anmeldung", Kind: domain.ErrKindRuntime, Terminal: true}}}
				ex.Terminal = &domain.TerminalResult{Attempt: 1, State: domain.ExecControlledFailure, Source: "error"}
				return ex
			},
			parts: []string{"/login", "API-Fallback"}, code: "not_logged_in",
		},
		"Abo-Limit": {
			reply: func(req runner.Request) runner.Execution {
				ex := okExecution(req, req.Profile.Model)
				ex.State = domain.ExecControlledFailure
				ex.Events = []domain.RunEvent{
					{Payload: domain.ErrorPayload{Code: "subscription_limit", Message: "Limit erreicht", Kind: domain.ErrKindSubscriptionLimit}},
					{Payload: domain.Result{Outcome: domain.OutcomeControlledFailure, Summary: "Limit"}},
				}
				ex.Terminal = &domain.TerminalResult{Attempt: 1, State: domain.ExecControlledFailure, Source: "result"}
				return ex
			},
			parts: []string{"Abo-Limit", "Kein Fallback"}, code: "subscription_limit",
		},
		"Absturz": {
			reply: func(req runner.Request) runner.Execution {
				ex := okExecution(req, req.Profile.Model)
				ex.Events, ex.State, ex.Crashed, ex.ExitCode = nil, domain.ExecCrashed, true, 0
				ex.Terminal = &domain.TerminalResult{Attempt: 1, State: domain.ExecCrashed, Source: "process_exit"}
				ex.Stderr = "[runner] zeile 1\n[runner] letzte Zeile\n"
				return ex
			},
			parts: []string{"ohne Ergebnis", "Exit-Code 0", "letzte Zeile"},
		},
		"Anmeldeart ist kein Abo": {
			reply: func(req runner.Request) runner.Execution {
				ex := okExecution(req, req.Profile.Model)
				ex.SessionStarted.AuthSource = domain.AuthAPIKey
				return ex
			},
			parts: []string{"api_key"}, code: "auth_source",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			e := newVerifyEnv(t)
			e.useExecutor(t, tc.reply)
			code, out, errOut := e.run("profile", "verify", "--live", "--role", "documentation", "--json")
			wantFail(t, code, errOut, ExitError, "Befund")
			var v verifyJSON
			decode(t, out, &v)
			if v.OK || len(v.LiveRuns) != 1 || v.LiveRuns[0].Status == "ok" {
				t.Fatalf("Ergebnis: %+v", v)
			}
			for _, p := range tc.parts {
				if !strings.Contains(v.LiveRuns[0].Diagnosis, p) {
					t.Errorf("Diagnose %q enthält %q nicht", v.LiveRuns[0].Diagnosis, p)
				}
			}
			if v.LiveRuns[0].Code != tc.code {
				t.Errorf("Code = %q, erwartet %q", v.LiveRuns[0].Code, tc.code)
			}
		})
	}
}

func TestVerifyLiveDoesNotStartWhenPrerequisitesFail(t *testing.T) {
	e := newVerifyEnv(t) // der Executor meldet jeden Aufruf als Fehler
	t.Setenv("PATH", t.TempDir())
	code, out, errOut := e.run("profile", "verify", "--live")
	wantFail(t, code, errOut, ExitError, "Befund")
	if !strings.Contains(out, "Live-Läufe nicht gestartet") {
		t.Errorf("Ausgabe:\n%s", out)
	}
}

func TestVerifyIsListedInHelp(t *testing.T) {
	_, out, _ := run(t, "profile", "--help")
	if !strings.Contains(out, "verify") {
		t.Errorf("profile --help nennt verify nicht:\n%s", out)
	}
	_, out, _ = run(t, "profile", "verify", "--help")
	for _, s := range []string{"--live", "--role", "--json", "--claude-binary", "Kontingent"} {
		if !strings.Contains(out, s) {
			t.Errorf("verify --help nennt %q nicht", s)
		}
	}
}
