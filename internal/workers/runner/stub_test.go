package runner

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alexwenzel/ccmas/internal/domain"
)

// Der Stub-Runner ist das Testbinary selbst, erneut gestartet mit -ccmas-stub=<Szenario>: ein echter
// Hilfsprozess mit stdin, stdout, stderr, Prozessgruppe und Exit-Code, ohne Python und ohne Konto.
// Er existiert nur in Tests.
const (
	stubFlag = "-ccmas-stub="
	dumpFlag = "-ccmas-dump="
)

func TestMain(m *testing.M) {
	var scenario, dump string
	for _, a := range os.Args[1:] {
		switch {
		case strings.HasPrefix(a, stubFlag):
			scenario = strings.TrimPrefix(a, stubFlag)
		case strings.HasPrefix(a, dumpFlag):
			dump = strings.TrimPrefix(a, dumpFlag)
		}
	}
	if scenario != "" {
		os.Exit(stubMain(scenario, dump))
	}
	os.Exit(m.Run())
}

type stub struct {
	env     domain.Envelope
	spec    domain.RunSpec
	dump    string
	out     *bufio.Writer
	stopCh  chan struct{}
	stopMu  sync.Once
	cmdFile *os.File
}

func (s *stub) emit(attempt int, typ string, payload any) {
	raw, _ := json.Marshal(payload)
	line, _ := json.Marshal(map[string]any{
		"protocol_version": "1", "project_id": s.env.ProjectID, "execution_id": s.env.ExecutionID,
		"attempt": attempt, "correlation_id": s.env.CorrelationID, "type": typ,
		"timestamp": "2026-11-10T12:00:00Z", "payload": json.RawMessage(raw),
	})
	s.out.Write(line)
	s.out.WriteByte('\n')
	s.out.Flush()
}

func (s *stub) started(attempt int) {
	p := map[string]any{"session_id": "sess-1", "model": s.spec.Model, "effort": s.spec.Effort, "claude_version": "2.1.311"}
	if s.spec.SkillSnapshot != nil {
		p["skill_snapshot_id"] = s.spec.SkillSnapshot.SnapshotID
		p["skills"] = s.spec.SkillIDs
	}
	s.emit(attempt, "session_started", p)
}

func (s *stub) result(attempt int, outcome string) {
	s.emit(attempt, "result", map[string]any{"outcome": outcome, "summary": "fertig"})
}

func stubMain(scenario, dump string) int {
	if scenario == "sleeper" { // Kindprozess des Szenarios ignore-stop: bleibt in der Prozessgruppe des Runners
		_ = os.WriteFile(filepath.Join(dump, "child.pid"), []byte(fmt.Sprint(os.Getpid())), 0o600)
		time.Sleep(time.Hour)
		return 0
	}
	in := bufio.NewReaderSize(os.Stdin, 4<<20)
	first, err := in.ReadBytes('\n')
	if err != nil {
		return 2
	}
	cmd, err := domain.ParseRunCommand(first)
	if err != nil {
		fmt.Fprintln(os.Stderr, "stub: ungültiger Start:", err)
		return 2
	}
	s := &stub{env: cmd.Envelope, spec: cmd.Payload.(domain.RunSpec), dump: dump, out: bufio.NewWriter(os.Stdout), stopCh: make(chan struct{})}
	attempt := s.env.Attempt
	if dump != "" {
		_ = os.WriteFile(filepath.Join(dump, "start.json"), first, 0o600)
		env := os.Environ()
		sort.Strings(env)
		_ = os.WriteFile(filepath.Join(dump, "env.txt"), []byte(strings.Join(env, "\n")), 0o600)
		wd, _ := os.Getwd()
		_ = os.WriteFile(filepath.Join(dump, "cwd.txt"), []byte(wd), 0o600)
		s.cmdFile, _ = os.OpenFile(filepath.Join(dump, "commands.txt"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	}
	go func() { // weitere Kommandos mitschreiben
		for {
			line, err := in.ReadBytes('\n')
			if err != nil {
				return
			}
			c, err := domain.ParseRunCommand(line)
			if err != nil {
				continue
			}
			if s.cmdFile != nil {
				fmt.Fprintln(s.cmdFile, c.Envelope.Type)
			}
			if c.Envelope.Type == domain.MsgStop {
				s.stopMu.Do(func() { close(s.stopCh) })
			}
		}
	}()

	switch scenario {
	case "ok":
		s.started(attempt)
		s.emit(attempt, "activity", map[string]any{"kind": "assistant_text", "summary": "denkt"})
		s.emit(attempt, "tool_event", map[string]any{"tool_use_id": "t1", "tool": "Read", "phase": "started"})
		s.emit(attempt, "tool_event", map[string]any{"tool_use_id": "t1", "tool": "Read", "phase": "finished"})
		s.result(attempt, "succeeded")
		return 0
	case "structured":
		// Gibt die Auftragsfelder von CC-14 zurück, damit der Test sieht, was beim Runner ankam.
		s.started(attempt)
		out := map[string]any{"system_prompt": s.spec.SystemPrompt, "schema_present": len(s.spec.OutputSchema) > 0, "skill_ids": s.spec.SkillIDs}
		if s.spec.SkillSnapshot != nil {
			out["snapshot_id"] = s.spec.SkillSnapshot.SnapshotID
		}
		s.emit(attempt, "result", map[string]any{"outcome": "succeeded", "summary": "fertig", "structured_output": out})
		return 0
	case "structured-missing":
		s.started(attempt)
		s.emit(attempt, "error", map[string]any{"code": "structured_output_failed", "message": "keine gültige Ausgabe", "kind": "runtime", "terminal": true})
		return 0
	case "exit0-noresult":
		s.started(attempt)
		return 0
	case "exit3-noresult":
		s.started(attempt)
		fmt.Fprintln(os.Stderr, "[runner] abgestürzt")
		return 3
	case "double-terminal":
		s.started(attempt)
		s.result(attempt, "succeeded")
		s.result(attempt, "controlled_failure")
		s.emit(attempt, "activity", map[string]any{"kind": "assistant_text", "summary": "zu spät"})
		return 0
	case "error-terminal":
		s.started(attempt)
		s.emit(attempt, "error", map[string]any{"code": "not_logged_in", "message": "nicht angemeldet", "kind": "runtime", "terminal": true})
		return 0
	case "garbage":
		os.Stdout.WriteString("das ist kein json\n")
		os.Stdout.WriteString(strings.Repeat("x", domain.MaxMessageBytes+10) + "\n")
		os.Stdout.WriteString("\n")
		os.Stdout.WriteString(`{"protocol_version":"1","unbekannt":true}` + "\n")
		s.started(attempt)
		s.result(attempt, "succeeded")
		return 0
	case "stale":
		s.emit(attempt-1, "activity", map[string]any{"kind": "assistant_text", "summary": "alter Versuch"})
		s.started(attempt)
		s.emit(attempt+1, "activity", map[string]any{"kind": "assistant_text", "summary": "nie begonnener Versuch"})
		s.emit(attempt-1, "result", map[string]any{"outcome": "succeeded", "summary": "alt"})
		s.result(attempt, "controlled_failure")
		return 0
	case "wait-stop":
		s.started(attempt)
		select {
		case <-s.stopCh:
			s.result(attempt, "cancelled")
			return 0
		case <-time.After(30 * time.Second):
			return 4
		}
	case "ignore-stop":
		ignoreTerm()
		child := exec.Command(os.Args[0], stubFlag+"sleeper", dumpFlag+dump)
		child.Stdin = nil
		if err := child.Start(); err != nil {
			return 5
		}
		s.started(attempt)
		_ = os.WriteFile(filepath.Join(dump, "main.pid"), []byte(fmt.Sprint(os.Getpid())), 0o600)
		time.Sleep(time.Hour)
		return 0
	case "many":
		s.started(attempt)
		for i := 0; i < 20; i++ {
			s.emit(attempt, "activity", map[string]any{"kind": "assistant_text", "summary": fmt.Sprint("a", i)})
		}
		s.result(attempt, "succeeded")
		return 0
	case "stderr":
		fmt.Fprint(os.Stderr, strings.Repeat("0123456789", 100)+"ENDE")
		s.started(attempt)
		s.result(attempt, "succeeded")
		return 0
	}
	return 9
}

// runStub führt ein Szenario als Runner-Befehl aus.
type harness struct {
	t    *testing.T
	dump string
	r    *Runner
}

func newHarness(t *testing.T, scenario string, mod ...func(*Config)) *harness {
	t.Helper()
	dump := t.TempDir()
	cfg := Config{
		Command:   []string{os.Args[0], stubFlag + scenario, dumpFlag + dump},
		StopGrace: 300 * time.Millisecond, KillDelay: 300 * time.Millisecond,
	}
	for _, m := range mod {
		m(&cfg)
	}
	return &harness{t: t, dump: dump, r: New(cfg)}
}

func (h *harness) read(name string) string {
	h.t.Helper()
	raw, err := os.ReadFile(filepath.Join(h.dump, name))
	if err != nil {
		h.t.Fatal(err)
	}
	return string(raw)
}

func (h *harness) exists(name string) bool {
	_, err := os.Stat(filepath.Join(h.dump, name))
	return err == nil
}

// testProfile ist ein gültiges, aufgelöstes Profil ohne Skills.
func testProfile() domain.AgentProfile {
	return domain.AgentProfile{
		ProfileID: "concept", Version: 3, Role: domain.RoleConcept, Runtime: domain.RuntimeClaude,
		Model: "claude-sonnet-5-5", Effort: domain.EffortLow, SkillIDs: []string{}, PolicyProfile: PolicyReadonly,
		RequestedModel: "claude-sonnet-5-5", RequestedEffort: domain.EffortLow,
		ValueSources: &domain.ValueSources{Model: domain.SourceRole, Effort: domain.SourceOrder, SkillIDs: domain.SourceRole},
	}
}

const testPrompt = "GEHEIMER-PROMPT-TEXT: bitte nichts protokollieren"

func testRequest(t *testing.T) Request {
	t.Helper()
	return Request{
		ProjectID: "proj-1", ExecutionID: "exec-1", Attempt: 1,
		Profile: testProfile(), Prompt: testPrompt, Cwd: t.TempDir(),
		ClaudeBinary: "/opt/test/claude", MaxRuntimeSeconds: 60,
	}
}

func terminalCount(e Execution) int {
	n := 0
	for _, ev := range e.Events {
		switch p := ev.Payload.(type) {
		case domain.Result:
			n++
		case domain.ErrorPayload:
			if p.Terminal {
				n++
			}
		}
	}
	return n
}

func hasViolation(e Execution, cat domain.Category) bool {
	for _, v := range e.Violations {
		if v.Category == cat {
			return true
		}
	}
	return false
}
