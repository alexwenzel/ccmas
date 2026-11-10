package runner

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/alexwenzel/ccmas/internal/domain"
)

func TestNormalRunHasExactlyOneTerminalResult(t *testing.T) {
	h := newHarness(t, "ok")
	req := testRequest(t)
	exec, err := h.r.Execute(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if exec.State != domain.ExecSucceeded || exec.Crashed || exec.ExitCode != 0 {
		t.Fatalf("State %s, Crashed %v, Exit %d", exec.State, exec.Crashed, exec.ExitCode)
	}
	if terminalCount(exec) != 1 || exec.Terminal == nil || exec.Terminal.Source != "result" {
		t.Errorf("terminal = %+v, Anzahl %d", exec.Terminal, terminalCount(exec))
	}
	if len(exec.Events) != 5 || len(exec.Violations) != 0 {
		t.Errorf("Ereignisse %d, Verstöße %v", len(exec.Events), exec.Violations)
	}
	if exec.SessionStarted == nil || exec.SessionStarted.Model != "claude-sonnet-5-5" || exec.SessionStarted.SessionID != "sess-1" {
		t.Errorf("session_started = %+v", exec.SessionStarted)
	}
	if res, ok := exec.Result(); !ok || res.Outcome != domain.OutcomeSucceeded {
		t.Errorf("Result = %+v, %v", res, ok)
	}
	if exec.ExecutionID != "exec-1" || exec.ProjectID != "proj-1" || exec.Attempt != 1 {
		t.Errorf("IDs: %+v", exec)
	}
	if exec.StartedAt.IsZero() || exec.EndedAt.Before(exec.StartedAt) {
		t.Errorf("Zeiten %v .. %v", exec.StartedAt, exec.EndedAt)
	}
}

func TestStartIsSentWithProfileValuesAndRecordKeepsNoPrompt(t *testing.T) {
	h := newHarness(t, "ok")
	req := testRequest(t)
	exec, err := h.r.Execute(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	cmd, err := domain.ParseRunCommand([]byte(h.read("start.json")))
	if err != nil {
		t.Fatal(err)
	}
	spec := cmd.Payload.(domain.RunSpec)
	wantCwd, _ := filepath.EvalSymlinks(req.Cwd)
	if spec.Prompt != testPrompt || spec.Cwd != wantCwd || spec.ClaudeBinary != req.ClaudeBinary {
		t.Errorf("RunSpec beim Runner: %+v", spec)
	}
	p := req.Profile
	if spec.Model != p.Model || spec.Effort != p.Effort || spec.Role != p.Role || spec.PolicyProfile != p.PolicyProfile ||
		spec.ProfileID != p.ProfileID || spec.ProfileVersion != p.Version || spec.Runtime != p.Runtime {
		t.Errorf("RunSpec weicht vom Profil ab: %+v", spec)
	}
	if cmd.Envelope.Type != domain.MsgStart || cmd.Envelope.ProjectID != "proj-1" || cmd.Envelope.ExecutionID != "exec-1" || cmd.Envelope.Attempt != 1 {
		t.Errorf("Umschlag: %+v", cmd.Envelope)
	}
	// Der Datensatz kennt nur Länge und Hash, nie den Text.
	if exec.Spec.PromptBytes != len(testPrompt) || len(exec.Spec.PromptSHA256) != 64 {
		t.Errorf("Spec = %+v", exec.Spec)
	}
	raw, err := exec.JSON()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "GEHEIMER") {
		t.Error("der Prompt-Text steht im Datensatz")
	}
	if strings.Contains(exec.Stderr, "GEHEIMER") {
		t.Error("der Prompt-Text steht im mitgeschnittenen stderr")
	}
	var back map[string]any
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("Datensatz ist kein gültiges JSON: %v", err)
	}
}

func TestProfileInRecordIsAnImmutableCopy(t *testing.T) {
	h := newHarness(t, "ok")
	req := testRequest(t)
	req.Profile.SkillIDs = nil
	exec, err := h.r.Execute(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	want := exec.Profile
	req.Profile.Model = "claude-geaendert"
	req.Profile.ValueSources.Model = domain.SourceOrder
	if exec.Profile.Model != "claude-sonnet-5-5" || exec.Profile.ValueSources.Model != domain.SourceRole || want.RequestedEffort != domain.EffortLow {
		t.Errorf("Profil im Datensatz hat sich geändert: %+v", exec.Profile)
	}
}

func TestProcessEndWithoutResultIsACrash(t *testing.T) {
	for scenario, wantExit := range map[string]int{"exit0-noresult": 0, "exit3-noresult": 3} {
		t.Run(scenario, func(t *testing.T) {
			exec, err := newHarness(t, scenario).r.Execute(context.Background(), testRequest(t))
			if err != nil {
				t.Fatal(err)
			}
			if !exec.Crashed || exec.State != domain.ExecCrashed || exec.ExitCode != wantExit {
				t.Errorf("Crashed %v, State %s, Exit %d", exec.Crashed, exec.State, exec.ExitCode)
			}
			if exec.Terminal == nil || exec.Terminal.Source != "process_exit" || exec.Terminal.ExitCode != wantExit {
				t.Errorf("terminal = %+v", exec.Terminal)
			}
			if terminalCount(exec) != 0 {
				t.Error("ein Absturz hat kein Ergebnis aus dem Runner")
			}
			if _, ok := exec.Result(); ok {
				t.Error("Result() bei Absturz")
			}
		})
	}
}

func TestTerminalErrorEndsTheAttemptWithoutCrash(t *testing.T) {
	exec, err := newHarness(t, "error-terminal").r.Execute(context.Background(), testRequest(t))
	if err != nil {
		t.Fatal(err)
	}
	e, ok := exec.TerminalError()
	if !ok || e.Code != "not_logged_in" || exec.State != domain.ExecControlledFailure || exec.Crashed {
		t.Errorf("TerminalError = %+v, %v, State %s", e, ok, exec.State)
	}
}

func TestSecondTerminalAndMessagesAfterItAreSequenceViolations(t *testing.T) {
	exec, err := newHarness(t, "double-terminal").r.Execute(context.Background(), testRequest(t))
	if err != nil {
		t.Fatal(err)
	}
	if exec.State != domain.ExecSucceeded || terminalCount(exec) != 1 {
		t.Errorf("State %s, terminale Ergebnisse %d", exec.State, terminalCount(exec))
	}
	n := 0
	for _, v := range exec.Violations {
		if v.Category == domain.CatSequence {
			n++
		}
	}
	if n != 2 {
		t.Errorf("sequence-Verstöße = %d (%v), erwartet 2: zweites Ergebnis und Nachricht danach", n, exec.Violations)
	}
	if res, _ := exec.Result(); res.Outcome != domain.OutcomeSucceeded {
		t.Errorf("das erste Ergebnis muss gelten: %+v", res)
	}
}

func TestInvalidAndOversizeLinesAreDroppedAndTheRunContinues(t *testing.T) {
	exec, err := newHarness(t, "garbage").r.Execute(context.Background(), testRequest(t))
	if err != nil {
		t.Fatal(err)
	}
	if exec.State != domain.ExecSucceeded || exec.SessionStarted == nil {
		t.Fatalf("State %s, session %v", exec.State, exec.SessionStarted)
	}
	if !hasViolation(exec, domain.CatSize) || !hasViolation(exec, domain.CatSyntax) {
		t.Errorf("Verstöße = %v", exec.Violations)
	}
	if len(exec.Violations) != 3 { // Text, übergroß, unbekannte Felder (leere Zeile zählt nicht)
		t.Errorf("Verstöße = %d: %v", len(exec.Violations), exec.Violations)
	}
	for _, v := range exec.Violations {
		if len(v.Message) > maxViolationMessageLen+8 {
			t.Errorf("Meldung nicht begrenzt (%d Zeichen)", len(v.Message))
		}
	}
}

func TestStaleAndFutureAttemptsDoNotChangeTheState(t *testing.T) {
	req := testRequest(t)
	req.Attempt = 2
	exec, err := newHarness(t, "stale").r.Execute(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if exec.Attempt != 2 || exec.State != domain.ExecControlledFailure {
		t.Fatalf("Versuch %d, State %s", exec.Attempt, exec.State)
	}
	if !hasViolation(exec, domain.CatStale) || !hasViolation(exec, domain.CatSequence) {
		t.Errorf("Verstöße = %v", exec.Violations)
	}
	if res, _ := exec.Result(); res.Outcome != domain.OutcomeControlledFailure {
		t.Errorf("das alte Ergebnis darf nicht gelten: %+v", res)
	}
	for _, ev := range exec.Events {
		if ev.Envelope.Attempt != 2 {
			t.Errorf("Ereignis aus Versuch %d übernommen", ev.Envelope.Attempt)
		}
	}
}

func TestEventAndStderrBuffersAreBounded(t *testing.T) {
	exec, err := newHarness(t, "many", func(c *Config) { c.MaxEvents = 5 }).r.Execute(context.Background(), testRequest(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(exec.Events) > 6 || exec.DroppedEvents == 0 || terminalCount(exec) != 1 || exec.State != domain.ExecSucceeded {
		t.Errorf("Ereignisse %d, verworfen %d, terminal %d, State %s", len(exec.Events), exec.DroppedEvents, terminalCount(exec), exec.State)
	}
	exec, err = newHarness(t, "stderr", func(c *Config) { c.StderrLimit = 50 }).r.Execute(context.Background(), testRequest(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(exec.Stderr) > 50 || !exec.StderrTruncated || !strings.HasSuffix(exec.Stderr, "ENDE") {
		t.Errorf("stderr = %q, abgeschnitten %v", exec.Stderr, exec.StderrTruncated)
	}
}

func TestProcessRunsInANeutralDirectoryNotTheSessionDirectory(t *testing.T) {
	h := newHarness(t, "ok")
	req := testRequest(t)
	if _, err := h.r.Execute(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	got := h.read("cwd.txt")
	session, _ := filepath.EvalSymlinks(req.Cwd)
	here, _ := os.Getwd()
	gotReal, _ := filepath.EvalSymlinks(got)
	if gotReal == session || strings.HasPrefix(gotReal, session+string(filepath.Separator)) {
		t.Errorf("der Prozess läuft im Sitzungsverzeichnis: %s", got)
	}
	if gotReal == here {
		t.Errorf("der Prozess erbt das Arbeitsverzeichnis des Aufrufers: %s", got)
	}
	if _, err := os.Stat(got); err == nil {
		t.Errorf("das neutrale Verzeichnis %s wurde nicht aufgeräumt", got)
	}
}

func TestStartRejectsInvalidInputBeforeAnyProcess(t *testing.T) {
	mutate := map[string]func(*Request){
		"Projekt-ID ungültig":   func(r *Request) { r.ProjectID = "ungültig!" },
		"Ausführungs-ID leer":   func(r *Request) { r.ExecutionID = "" },
		"Versuch negativ":       func(r *Request) { r.Attempt = -1 },
		"Prompt leer":           func(r *Request) { r.Prompt = "" },
		"Sitzungsverz. fehlt":   func(r *Request) { r.Cwd = "" },
		"Sitzungsverz. fehlt 2": func(r *Request) { r.Cwd = filepath.Join(r.Cwd, "gibt-es-nicht") },
		"Binary fehlt":          func(r *Request) { r.ClaudeBinary = "" },
		"Binary relativ":        func(r *Request) { r.ClaudeBinary = "claude" },
		"Modell ungültig":       func(r *Request) { r.Profile.Model = "" },
		"Runtime fehlt":         func(r *Request) { r.Profile.Runtime = "" },
	}
	for name, f := range mutate {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, "ok")
			req := testRequest(t)
			f(&req)
			if _, err := h.r.Start(context.Background(), req); err == nil {
				t.Fatal("erwartet: Fehler")
			}
			if h.exists("start.json") {
				t.Error("der Runner-Prozess wurde trotz ungültiger Angaben gestartet")
			}
		})
	}
}

func TestStartWithCancelledContextStartsNothing(t *testing.T) {
	h := newHarness(t, "ok")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := h.r.Start(ctx, testRequest(t)); err == nil {
		t.Fatal("erwartet: Fehler")
	}
	if h.exists("start.json") {
		t.Error("Prozess gestartet")
	}
}

func TestMissingCommandIsAStartError(t *testing.T) {
	r := New(Config{Command: []string{filepath.Join(t.TempDir(), "gibt-es-nicht")}})
	if _, err := r.Execute(context.Background(), testRequest(t)); err == nil || !strings.Contains(err.Error(), "nicht startbar") {
		t.Errorf("err = %v", err)
	}
}

func TestEnvironmentContainsOnlyTheAllowlistAndTheContractsDir(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-test-nicht-echt")
	t.Setenv("ANTHROPIC_BASE_URL", "https://example.invalid")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "abc")
	t.Setenv("CLAUDECODE", "1")
	t.Setenv("GH_TOKEN", "ghp-nicht-echt")
	t.Setenv("HTTPS_PROXY", "http://proxy.invalid")
	t.Setenv("HOME", "/home/test")
	t.Setenv("LANG", "de_DE.UTF-8")
	h := newHarness(t, "ok", func(c *Config) { c.SoftwareRoot = "/opt/ccmas-test" })
	if _, err := h.r.Execute(context.Background(), testRequest(t)); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, l := range strings.Split(h.read("env.txt"), "\n") {
		if k, v, ok := strings.Cut(l, "="); ok {
			got[k] = v
		}
	}
	allowed := append(slices.Clone(EnvAllowlist), EnvContractsDir)
	for k := range got {
		if !slices.Contains(allowed, k) {
			t.Errorf("Variable %s gelangt in den Runner-Prozess", k)
		}
	}
	for _, k := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_BASE_URL", "CLAUDE_CODE_SESSION_ID", "CLAUDECODE", "GH_TOKEN", "HTTPS_PROXY"} {
		if _, ok := got[k]; ok {
			t.Errorf("%s darf nicht vererbt werden", k)
		}
	}
	if got["HOME"] != "/home/test" || got["LANG"] != "de_DE.UTF-8" || got["PATH"] == "" {
		t.Errorf("Allowlist-Variablen fehlen: %v", got)
	}
	if got[EnvContractsDir] != filepath.Join("/opt/ccmas-test", "contracts") {
		t.Errorf("%s = %q", EnvContractsDir, got[EnvContractsDir])
	}
}

func TestEnvironmentWithInjectedGetenv(t *testing.T) {
	env := Environment(func(k string) string {
		return map[string]string{"PATH": "/bin", "ANTHROPIC_API_KEY": "x", "HOME": "/h", "USER": ""}[k]
	}, "")
	if !slices.Equal(env, []string{"PATH=/bin", "HOME=/h"}) {
		t.Errorf("env = %v", env)
	}
}

func TestContextCancelSendsStop(t *testing.T) {
	h := newHarness(t, "wait-stop", func(c *Config) { c.StopGrace = 20 * time.Second })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p, err := h.r.Start(ctx, testRequest(t))
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "Zustand running", func() bool { return p.State() == domain.ExecRunning })
	cancel()
	exec := waitExec(t, p, 10*time.Second)
	if !exec.StopSent || exec.ForcedKill || exec.State != domain.ExecCancelled || exec.Crashed {
		t.Errorf("StopSent %v, ForcedKill %v, State %s, Crashed %v", exec.StopSent, exec.ForcedKill, exec.State, exec.Crashed)
	}
	if got := h.read("commands.txt"); !strings.Contains(got, "stop") {
		t.Errorf("Der Runner hat kein stop erhalten: %q", got)
	}
}

func TestExplicitInterruptReachesTheRunner(t *testing.T) {
	h := newHarness(t, "wait-stop")
	p, err := h.r.Start(context.Background(), testRequest(t))
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "Zustand running", func() bool { return p.State() == domain.ExecRunning })
	if err := p.Interrupt(domain.ReasonUserRequest); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "interrupt angekommen", func() bool { return h.exists("commands.txt") && strings.Contains(h.read("commands.txt"), "interrupt") })
	p.Stop(domain.ReasonShutdown)
	exec := waitExec(t, p, 10*time.Second)
	if exec.State != domain.ExecCancelled {
		t.Errorf("State %s", exec.State)
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("Zeitüberschreitung: %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func waitExec(t *testing.T, p *Run, d time.Duration) Execution {
	t.Helper()
	ch := make(chan Execution, 1)
	go func() { ch <- p.Wait() }()
	select {
	case e := <-ch:
		return e
	case <-time.After(d):
		t.Fatal("Wait kehrt nicht zurück")
		return Execution{}
	}
}
