package supervisor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/alexwenzel/ccmas/internal/domain"
	"github.com/alexwenzel/ccmas/internal/profiles"
	"github.com/alexwenzel/ccmas/internal/skills"
	"github.com/alexwenzel/ccmas/internal/workers/runner"
)

// repoRoot ist der Softwareordner (der echte Skill-Bestand und die echten Rollenregeln werden geladen).
const repoRoot = "../.."

// Echte Skills des Bestands, die die Entscheidungen der Tests nennen.
const (
	realImplSkill    = "ccmas-feature-parallel"
	realReviewSkill  = "ccmas-code-review"
	realConceptSkill = "ccmas-concept-ticket"
)

// ---- Fake-Runtime und Fake-Quelle (nur in Tests) -----------------------------------------------------------

type runStep func(req runner.Request) (runner.Execution, error)

// fakeRuntime spielt ein Skript von Antworten ab und hält jede Anfrage fest.
type fakeRuntime struct {
	mu    sync.Mutex
	steps []runStep
	reqs  []runner.Request
}

func (f *fakeRuntime) Execute(ctx context.Context, req runner.Request) (runner.Execution, error) {
	f.mu.Lock()
	i := len(f.reqs)
	f.reqs = append(f.reqs, req)
	f.mu.Unlock()
	if i >= len(f.steps) {
		return runner.Execution{}, fmt.Errorf("unerwarteter Aufruf %d der Runtime", i+1)
	}
	return f.steps[i](req)
}

func (f *fakeRuntime) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.reqs)
}

func baseExecution(req runner.Request) runner.Execution {
	return runner.Execution{
		ProjectID: req.ProjectID, ExecutionID: req.ExecutionID, Attempt: 1, Profile: req.Profile,
		SessionStarted: &domain.SessionStarted{SessionID: "sess-" + req.ExecutionID, Model: req.Profile.Model, Effort: req.Profile.Effort,
			ClaudeVersion: "2.1.311", SDKVersion: "0.2.180", SkillSnapshotID: req.SkillSnapshot.SnapshotID, Skills: req.Profile.SkillIDs},
		Events: []domain.RunEvent{}, State: domain.ExecSucceeded,
	}
}

// answer: Die Sitzung endet mit einem Ergebnis und dieser strukturierten Ausgabe (roh).
func answer(output string) runStep {
	return func(req runner.Request) (runner.Execution, error) {
		e := baseExecution(req)
		e.Events = append(e.Events, domain.RunEvent{Payload: domain.Result{Outcome: domain.OutcomeSucceeded, Summary: "fertig", StructuredOutput: json.RawMessage(output)}})
		e.Terminal = &domain.TerminalResult{Attempt: 1, State: domain.ExecSucceeded, Source: "result"}
		return e, nil
	}
}

// answerWithoutOutput: Erfolg, aber ohne strukturierte Ausgabe.
func answerWithoutOutput() runStep {
	return func(req runner.Request) (runner.Execution, error) {
		e := baseExecution(req)
		e.Events = append(e.Events, domain.RunEvent{Payload: domain.Result{Outcome: domain.OutcomeSucceeded, Summary: "nur Text"}})
		e.Terminal = &domain.TerminalResult{Attempt: 1, State: domain.ExecSucceeded, Source: "result"}
		return e, nil
	}
}

// failWith: terminaler Fehler des Runners mit diesem Code.
func failWith(code, msg string) runStep {
	return func(req runner.Request) (runner.Execution, error) {
		e := baseExecution(req)
		e.Events = append(e.Events, domain.RunEvent{Payload: domain.ErrorPayload{Code: code, Message: msg, Kind: domain.ErrKindRuntime, Terminal: true}})
		e.Terminal = &domain.TerminalResult{Attempt: 1, State: domain.ExecControlledFailure, Source: "error"}
		e.State = domain.ExecControlledFailure
		return e, nil
	}
}

// limitReached: Abo-Limit, danach kontrolliertes Ende ohne Ausgabe.
func limitReached() runStep {
	return func(req runner.Request) (runner.Execution, error) {
		e := baseExecution(req)
		e.Events = append(e.Events,
			domain.RunEvent{Payload: domain.ErrorPayload{Code: "subscription_limit", Message: "Abo-Limit erreicht", Kind: domain.ErrKindSubscriptionLimit}},
			domain.RunEvent{Payload: domain.Result{Outcome: domain.OutcomeControlledFailure, Summary: "Abo-Limit erreicht"}})
		e.Terminal = &domain.TerminalResult{Attempt: 1, State: domain.ExecControlledFailure, Source: "result"}
		e.State = domain.ExecControlledFailure
		return e, nil
	}
}

func crashed() runStep {
	return func(req runner.Request) (runner.Execution, error) {
		e := baseExecution(req)
		e.SessionStarted = nil
		e.Terminal = &domain.TerminalResult{Attempt: 1, State: domain.ExecCrashed, Source: "process_exit", ExitCode: 3}
		e.State, e.Crashed, e.ExitCode = domain.ExecCrashed, true, 3
		return e, nil
	}
}

func startFails(msg string) runStep {
	return func(runner.Request) (runner.Execution, error) { return runner.Execution{}, fmt.Errorf("%s", msg) }
}

// fakeSource liefert nacheinander diese Zustände (der letzte bleibt).
type fakeSource struct {
	mu    sync.Mutex
	snaps []ProjectSnapshot
	err   error
	calls int
}

func (f *fakeSource) CurrentSnapshot(ctx context.Context, projectID string) (ProjectSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return ProjectSnapshot{}, f.err
	}
	i := min(f.calls, len(f.snaps)-1)
	f.calls++
	return f.snaps[i], nil
}

// ---- Umgebung ---------------------------------------------------------------------------------------------

type plannerEnv struct {
	t       *testing.T
	cat     *skills.Catalog
	rules   *skills.Rules
	cfg     profiles.Config
	caps    profiles.Capabilities
	pol     Policy
	rt      *fakeRuntime
	src     *fakeSource
	snapDir string
	clock   time.Time
}

// newPlannerEnv lädt den echten Skill-Bestand und die echten Rollenregeln, dazu die erfundenen Modelle der Tests.
func newPlannerEnv(t *testing.T) *plannerEnv {
	t.Helper()
	cat, rules, err := skills.LoadChecked(repoRoot)
	if err != nil {
		t.Fatalf("LoadChecked(%s): %v", repoRoot, err)
	}
	e := &plannerEnv{t: t, cat: cat, rules: rules, cfg: testConfig(), caps: testCaps(), rt: &fakeRuntime{}, clock: time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)}
	e.pol = e.policyFor(e.cfg)
	e.snapDir = t.TempDir()
	t.Cleanup(func() { // Snapshots sind schreibgeschützt (0444/0555) und werden gezielt entfernt
		entries, _ := os.ReadDir(e.snapDir)
		for _, en := range entries {
			skills.RemoveSnapshotDir(filepath.Join(e.snapDir, en.Name()))
		}
	})
	e.src = &fakeSource{snaps: []ProjectSnapshot{e.snapshot()}}
	return e
}

func (e *plannerEnv) policyFor(cfg profiles.Config) Policy {
	e.t.Helper()
	res, err := profiles.NewResolver(cfg, e.cat)
	if err != nil {
		e.t.Fatal(err)
	}
	pol, err := NewPolicy(res, e.caps, e.cat, e.rules)
	if err != nil {
		e.t.Fatal(err)
	}
	return pol
}

// snapshot ist der Grundsnapshot der Tests (Revision 7) mit den Angeboten aus Konfiguration und Katalog.
func (e *plannerEnv) snapshot() ProjectSnapshot {
	s := baseSnapshot()
	s.Roles = BuildRoleOffers(e.cfg, e.caps, e.cat, nil)
	s.Skills = BuildSkillOffers(e.cat)
	return s
}

func (e *plannerEnv) planner(mod ...func(*PlannerConfig)) *Planner {
	e.t.Helper()
	cfg := PlannerConfig{
		Runtime: e.rt, Source: e.src, Policy: e.pol, Catalog: e.cat, ClaudeBinary: "/opt/test/claude", SnapshotDir: e.snapDir,
		Now:   func() time.Time { e.clock = e.clock.Add(1500 * time.Millisecond); return e.clock },
		NewID: func(prefix string) string { return fmt.Sprintf("%s-%d", prefix, e.rt.calls()+1) },
	}
	for _, m := range mod {
		m(&cfg)
	}
	p, err := NewPlanner(cfg)
	if err != nil {
		e.t.Fatal(err)
	}
	return p
}

// ---- Ausgaben, wie ein Modell sie nach dem Schema liefert -------------------------------------------------

func realImpl(id, order string) domain.Decision {
	d := implDecision(id, order)
	d.Selection = selection(domain.RoleImplementer, mMid, domain.EffortHigh, realImplSkill)
	d.Preconditions = []domain.Precondition{{Kind: domain.PrecConceptApproved, Ref: "1.0"}}
	d.ExpectedOutcome = "Getesteter Stand im Worktree."
	d.Estimate = &domain.Estimate{Size: domain.SizeM, Complexity: domain.LevelMedium, Uncertainty: domain.LevelLow}
	return d
}

func realReview(id, order, commit, concept string) domain.Decision {
	d := codeReviewDecision(id, order, commit, concept)
	d.Selection = selection(domain.RoleReviewer, mBig, domain.EffortHigh, realReviewSkill)
	d.ExpectedOutcome = "Unabhängiger Bericht für genau diesen Commit."
	d.Estimate = &domain.Estimate{Size: domain.SizeS, Complexity: domain.LevelMedium, Uncertainty: domain.LevelLow}
	return d
}

// modelOutput serialisiert Entscheidungen als Ausgabe des Modells: ohne die Felder, die Go setzt, und passend zum
// Modellschema (das prüft der Aufruf).
func modelOutput(t *testing.T, ds ...domain.Decision) string {
	t.Helper()
	b := batch(ds...)
	raw, _ := json.Marshal(b)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	for _, k := range []string{"schema_version", "batch_id", "project_id", "state_revision"} {
		delete(m, k)
	}
	for _, d := range m["decisions"].([]any) {
		for _, k := range []string{"schema_version", "project_id", "state_revision"} {
			delete(d.(map[string]any), k)
		}
	}
	out, _ := json.Marshal(m)
	conformsToModelSchema(t, string(out))
	return string(out)
}

func conformsToModelSchema(t *testing.T, raw string) {
	t.Helper()
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft7)
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(ModelOutputSchema()))
	if err != nil {
		t.Fatal(err)
	}
	if err := c.AddResource("mem://model.json", doc); err != nil {
		t.Fatal(err)
	}
	sch, err := c.Compile("mem://model.json")
	if err != nil {
		t.Fatal(err)
	}
	inst, err := jsonschema.UnmarshalJSON(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if err := sch.Validate(inst); err != nil {
		t.Fatalf("die Testausgabe entspricht nicht dem Modellschema: %v\n%s", err, raw)
	}
}

// goodPlan: zwei unabhängige Umsetzungen (zulässig im Grundsnapshot).
func goodPlan(t *testing.T) string {
	return modelOutput(t, realImpl("dec-1", "wo-a"), realImpl("dec-2", "wo-b"))
}

// badPlan: eine Umsetzung mit offener Abhängigkeit (wo-c hängt an wo-a): precondition_unmet.
func badPlan(t *testing.T) string {
	return modelOutput(t, realImpl("dec-1", "wo-c"))
}

func mustSuspend(t *testing.T, err error, reason SuspendReason, attempts int) *SuspendedError {
	t.Helper()
	se, ok := AsSuspended(err)
	if !ok {
		t.Fatalf("kein Wartezustand: %v", err)
	}
	if !errors.Is(err, ErrPlanningSuspended) || se.Reason != reason || len(se.Attempts) != attempts {
		t.Fatalf("Wartezustand = %v (Grund %s, %d Versuche), erwartet Grund %s mit %d Versuchen", err, se.Reason, len(se.Attempts), reason, attempts)
	}
	if !strings.Contains(err.Error(), "Planung ausgesetzt") {
		t.Errorf("Fehlertext sagt nicht, dass die Planung ausgesetzt ist: %v", err)
	}
	return se
}
