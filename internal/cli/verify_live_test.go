package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/alexwenzel/ccmas/config"
	"github.com/alexwenzel/ccmas/internal/domain"
	"github.com/alexwenzel/ccmas/internal/profiles"
	"github.com/alexwenzel/ccmas/internal/skills"
	"github.com/alexwenzel/ccmas/internal/workers/runner"
)

// Live-Tests des Go-Adapters mit dem echten Runner, dem lokalen Claude-Binary und der eigenen
// claude.ai-Anmeldung. Sie verbrauchen Abo-Kontingent und laufen nur ausdrücklich:
//
//	CCMAS_LIVE_TESTS=1 go test ./internal/cli -run TestLive -count=1 -v
//
// Mit CCMAS_EVIDENCE_DIR=<Verzeichnis> entsteht der redigierte Beleg live-profile-verify.jsonl.
// Gelaufen wird mit dem Startprofil aus config/*.example.yaml und dem echten Skill-Katalog.

const liveEvidenceName = "live-profile-verify"

var liveRecords []map[string]any

func requireLive(t *testing.T) string {
	t.Helper()
	if os.Getenv("CCMAS_LIVE_TESTS") != "1" {
		t.Skip("Live-Test: nur mit CCMAS_LIVE_TESTS=1 (verbraucht Abo-Kontingent)")
	}
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod nicht gefunden")
		}
		dir = parent
	}
}

func liveConfigDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "config")
	if _, err := profiles.NewStore(dir).Init(config.ModelsExample(), config.AgentsExample()); err != nil {
		t.Fatal(err)
	}
	return dir
}

var sdkPin = regexp.MustCompile(`claude-agent-sdk==([0-9.]+)`)

func sdkVersion(root string) string {
	raw, err := os.ReadFile(filepath.Join(root, "runners", "claude_sdk", "pyproject.toml"))
	if err != nil {
		return "unbekannt"
	}
	if m := sdkPin.FindSubmatch(raw); m != nil {
		return string(m[1])
	}
	return "unbekannt"
}

func writeLiveEvidence(t *testing.T, root string) {
	t.Helper()
	target := os.Getenv("CCMAS_EVIDENCE_DIR")
	if target == "" {
		return
	}
	cli := ""
	for _, r := range liveRecords {
		if v, _ := r["claude_version"].(string); v != "" {
			cli = v
		}
	}
	meta := map[string]any{
		"evidence": liveEvidenceName, "date": time.Now().Format("2006-01-02"),
		"sdk_version": sdkVersion(root), "cli_version": cli,
		"invocation": "go test ./internal/cli -run TestLive mit CCMAS_LIVE_TESTS=1: Go-Adapter internal/workers/runner startet " +
			"`uv run --frozen --project runners/claude_sdk python -m claude_sdk_runner --max-turns 3` mit Allowlist-Umgebung",
		"note": "Zeile 'verify_*': ccmas profile verify --live über die Startprofile (readonly, ohne Skills, Effort low). Zeile " +
			"'snapshot_*': Lauf mit Skill-Snapshot (Rolle concept, Standardausstattung aus dem echten Katalog, readonly, Modell und Effort " +
			"als ausdrückliche Auftragswerte). Der Effort ist angefordert, nicht gemessen: das SDK meldet den wirksamen Effort nicht.",
		"redaction": "keine Pfade, keine Konto-, E-Mail- oder Organisationsdaten; nur Modell-IDs, Session-IDs, Skill-IDs und Ergebnisse",
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(meta); err != nil {
		t.Fatal(err)
	}
	for _, r := range liveRecords {
		if err := enc.Encode(r); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, liveEvidenceName+".jsonl"), buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLiveProfileVerify(t *testing.T) {
	root := requireLive(t)
	cfgDir := liveConfigDir(t)
	var out, errOut bytes.Buffer
	code := Execute([]string{"profile", "verify", "--live", "--json", "--root", root, "--config-dir", cfgDir}, &out, &errOut)
	t.Logf("Ausgabe:\n%s\nstderr: %s", out.String(), errOut.String())
	var v struct {
		OK       bool `json:"ok"`
		LiveRuns []struct {
			Model          string   `json:"model"`
			SessionSkills  []string `json:"session_skills"`
			SnapshotID     string   `json:"skill_snapshot_id"`
			Roles          []string `json:"roles"`
			Status         string   `json:"status"`
			EffortAsked    string   `json:"effort_requested"`
			EffectiveModel string   `json:"effective_model"`
			SessionID      string   `json:"session_id"`
			ClaudeVersion  string   `json:"claude_version"`
			AuthSource     string   `json:"auth_source"`
			State          string   `json:"state"`
			ExitCode       int      `json:"exit_code"`
			Code           string   `json:"code"`
			Diagnosis      string   `json:"diagnosis"`
		} `json:"live_runs"`
	}
	if err := json.Unmarshal(out.Bytes(), &v); err != nil {
		t.Fatalf("keine gültige JSON-Ausgabe: %v", err)
	}
	for _, l := range v.LiveRuns {
		liveRecords = append(liveRecords, map[string]any{
			"label": "verify_" + l.Model, "requested_model": l.Model, "effective_model": l.EffectiveModel,
			"roles": l.Roles, "policy_profile": "readonly",
			"skills": l.SessionSkills, "skill_snapshot_id": l.SnapshotID,
			"effort_requested": l.EffortAsked, "effort_effective": "nicht gemessen",
			"session_id": l.SessionID, "claude_version": l.ClaudeVersion, "auth_source": l.AuthSource,
			"state": l.State, "exit_code": l.ExitCode, "status": l.Status, "code": l.Code, "diagnosis": l.Diagnosis,
		})
	}
	writeLiveEvidence(t, root)
	if code != ExitOK || !v.OK {
		t.Fatalf("profile verify --live: Exit-Code %d", code)
	}
	if len(v.LiveRuns) < 1 {
		t.Fatal("keine Live-Läufe")
	}
	for _, l := range v.LiveRuns {
		if l.Status != "ok" || l.EffectiveModel != l.Model || l.AuthSource != "claude_ai" {
			t.Errorf("Lauf %s: %+v", l.Model, l)
		}
	}
}

func TestLiveRunWithSnapshotFromGo(t *testing.T) {
	root := requireLive(t)
	st := profiles.NewStore(liveConfigDir(t))
	cfg, err := st.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	caps, err := st.LoadCapabilities()
	if err != nil {
		t.Fatal(err)
	}
	cat, err := skills.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	res, err := profiles.NewResolver(cfg, cat)
	if err != nil {
		t.Fatal(err)
	}
	const model = "claude-haiku-5-5" // kleiner Kontrolllauf; zugleich bestätigt er die Haiku-ID
	var ov profiles.Overrides
	ov.Order.Model, ov.Order.Effort = model, domain.EffortLow
	prof, err := res.ResolveProfile(domain.RoleConcept, "", ov, caps)
	if err != nil {
		t.Fatal(err)
	}
	if len(prof.SkillIDs) == 0 {
		t.Fatal("die Rolle concept hat im Katalog keine Standardausstattung")
	}
	if prof.PolicyProfile != runner.PolicyReadonly {
		t.Fatalf("Policy %s", prof.PolicyProfile)
	}
	claude, err := runner.ResolveClaudeBinary("", os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	snapDir := filepath.Join(t.TempDir(), "snapshots")
	exec, err := runner.New(runner.Config{SoftwareRoot: root, MaxTurns: verifyMaxTurns}).Execute(t.Context(), runner.Request{
		ProjectID: "verify", ExecutionID: runner.NewID("snapshot"), Attempt: 1, WorkOrderID: "verify",
		Profile: prof, Prompt: verifyPrompt, Cwd: cwd, ClaudeBinary: claude, MaxRuntimeSeconds: verifyMaxRuntimeSecs,
		Catalog: cat, SnapshotDir: snapDir,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer skills.RemoveSnapshotDir(exec.Spec.SkillSnapshot.Path)
	ss := exec.SessionStarted
	rec := map[string]any{
		"label": "snapshot_" + string(prof.Role), "requested_model": model, "roles": []string{string(prof.Role)},
		"policy_profile": prof.PolicyProfile, "effort_requested": string(prof.Effort), "effort_effective": "nicht gemessen",
		"skills_selected": prof.SkillIDs, "snapshot_id_go": exec.SkillSnapshotID,
		"state": string(exec.State), "exit_code": exec.ExitCode, "violations": len(exec.Violations),
	}
	if ss != nil {
		rec["effective_model"], rec["session_id"], rec["claude_version"], rec["auth_source"] = ss.Model, ss.SessionID, ss.ClaudeVersion, string(ss.AuthSource)
		rec["skills_in_session"], rec["skill_snapshot_id_in_session"] = ss.Skills, ss.SkillSnapshotID
	}
	if r, ok := exec.Result(); ok {
		rec["outcome"] = string(r.Outcome)
	}
	if e, ok := exec.TerminalError(); ok {
		rec["error_code"] = e.Code
	}
	liveRecords = append(liveRecords, rec)
	writeLiveEvidence(t, root)
	t.Logf("Lauf: %+v\nstderr (Ende): %s", rec, stderrTail(exec.Stderr, 5))

	if exec.State != domain.ExecSucceeded || ss == nil {
		t.Fatalf("State %s, session_started %v", exec.State, ss)
	}
	if ss.Model != model {
		t.Errorf("wirksames Modell %q, erwartet %q", ss.Model, model)
	}
	got := slices.Clone(ss.Skills)
	slices.Sort(got)
	if !slices.Equal(got, prof.SkillIDs) {
		t.Errorf("Skills der Sitzung %v, ausgewählt %v", got, prof.SkillIDs)
	}
	manifest, err := skills.VerifySnapshotDir(exec.Spec.SkillSnapshot.Path)
	if err != nil {
		t.Fatal(err)
	}
	if ss.SkillSnapshotID != manifest.SnapshotID || ss.SkillSnapshotID != exec.SkillSnapshotID {
		t.Errorf("Snapshot-ID der Sitzung %s, von Go geschrieben %s", ss.SkillSnapshotID, manifest.SnapshotID)
	}
	if strings.Contains(exec.Stderr, verifyPrompt) {
		t.Error("der Prompt steht im stderr")
	}
}
