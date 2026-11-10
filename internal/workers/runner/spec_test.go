package runner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/alexwenzel/ccmas/internal/domain"
	"github.com/alexwenzel/ccmas/internal/skills"
)

func implementerProfile(ids ...string) domain.AgentProfile {
	p := testProfile()
	p.ProfileID, p.Role, p.SkillIDs = "implementer", domain.RoleImplementer, ids
	return p
}

func TestRunWithSkillsWritesTheSnapshotAndSetsTheReference(t *testing.T) {
	h := newHarness(t, "ok")
	req := testRequest(t)
	req.Profile = implementerProfile(skillImpl)
	req.Catalog = syntheticCatalog(t)
	req.SnapshotDir = filepath.Join(t.TempDir(), "snapshots")
	exec, err := h.r.Execute(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	cmd, err := domain.ParseRunCommand([]byte(h.read("start.json")))
	if err != nil {
		t.Fatal(err)
	}
	spec := cmd.Payload.(domain.RunSpec)
	if spec.SkillSnapshot == nil {
		t.Fatal("RunSpec.skill_snapshot fehlt")
	}
	if !slices.Equal(spec.SkillIDs, []string{skillImpl}) {
		t.Errorf("skill_ids = %v", spec.SkillIDs)
	}
	manifest, err := skills.VerifySnapshotDir(spec.SkillSnapshot.Path)
	if err != nil {
		t.Fatalf("das Snapshot-Verzeichnis ist nicht gültig: %v", err)
	}
	if manifest.SnapshotID != spec.SkillSnapshot.SnapshotID || manifest.Role != domain.RoleImplementer {
		t.Errorf("snapshot.json = %s/%s, Referenz %s", manifest.SnapshotID, manifest.Role, spec.SkillSnapshot.SnapshotID)
	}
	if len(manifest.Skills) != 1 || manifest.Skills[0].ID != skillImpl {
		t.Errorf("Skills im Snapshot: %+v", manifest.Skills)
	}
	real, _ := filepath.EvalSymlinks(req.SnapshotDir)
	if !strings.HasPrefix(spec.SkillSnapshot.Path, real+string(filepath.Separator)) {
		t.Errorf("Snapshot-Pfad %s liegt nicht kanonisch unter %s", spec.SkillSnapshot.Path, real)
	}
	if exec.SkillSnapshotID != manifest.SnapshotID || exec.Spec.SkillSnapshot == nil || exec.Spec.SkillSnapshot.Path != spec.SkillSnapshot.Path {
		t.Errorf("Datensatz: %q, %+v", exec.SkillSnapshotID, exec.Spec.SkillSnapshot)
	}
	if !slices.Equal(exec.Spec.SkillIDs, []string{skillImpl}) {
		t.Errorf("Spec.SkillIDs = %v", exec.Spec.SkillIDs)
	}
	if exec.SessionStarted == nil || exec.SessionStarted.SkillSnapshotID != manifest.SnapshotID {
		t.Errorf("session_started = %+v", exec.SessionStarted)
	}
	skills.RemoveSnapshotDir(spec.SkillSnapshot.Path)
}

func TestRunWithoutSkillsWritesNoSnapshot(t *testing.T) {
	h := newHarness(t, "ok")
	req := testRequest(t)
	req.Profile = implementerProfile() // keine Skills
	req.Catalog = syntheticCatalog(t)
	req.SnapshotDir = filepath.Join(t.TempDir(), "snapshots")
	exec, err := h.r.Execute(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	cmd, _ := domain.ParseRunCommand([]byte(h.read("start.json")))
	spec := cmd.Payload.(domain.RunSpec)
	if spec.SkillSnapshot != nil || len(spec.SkillIDs) != 0 {
		t.Errorf("RunSpec = %+v", spec)
	}
	if strings.Contains(h.read("start.json"), "skill_snapshot") {
		t.Error("skill_snapshot steht im Start")
	}
	if _, err := os.Stat(req.SnapshotDir); err == nil {
		t.Error("das Snapshot-Verzeichnis wurde ohne Skills angelegt")
	}
	if exec.SkillSnapshotID != "" || exec.Spec.SkillSnapshot != nil {
		t.Errorf("Datensatz: %q, %+v", exec.SkillSnapshotID, exec.Spec.SkillSnapshot)
	}
	// JSON-Form: skill_ids ist [] und nicht null.
	raw, _ := exec.JSON()
	if !strings.Contains(string(raw), `"skill_ids": []`) {
		t.Errorf("skill_ids nicht als []:\n%s", raw)
	}
}

// TestSnapshotsNeedACheckedCatalog: Der Adapter schreibt Snapshots für Ausführungen nur aus einem Katalog, der mit
// skills.LoadChecked geladen ist (Katalog, Rollenregeln und Verträge geprüft). Ein nur mit Load geladener Katalog
// ist keine Grundlage, auch wenn er in sich gültig ist; es wird weder ein Snapshot geschrieben noch ein Prozess gestartet.
func TestSnapshotsNeedACheckedCatalog(t *testing.T) {
	loaded, err := skills.Load(syntheticRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	h := newHarness(t, "ok")
	req := testRequest(t)
	req.Profile = implementerProfile(skillImpl)
	req.Catalog = loaded
	req.SnapshotDir = filepath.Join(t.TempDir(), "snapshots")
	_, err = h.r.Start(context.Background(), req)
	if err == nil || !strings.Contains(err.Error(), "LoadChecked") {
		t.Fatalf("err = %v", err)
	}
	if h.exists("start.json") {
		t.Error("Prozess gestartet")
	}
	if _, statErr := os.Stat(req.SnapshotDir); statErr == nil {
		t.Error("Snapshot-Verzeichnis angelegt")
	}
	// Ohne Skills braucht es keinen Katalog und keine Prüfung.
	req = testRequest(t)
	if _, err := h.r.BuildSpec(req); err != nil {
		t.Fatalf("Profil ohne Skills: %v", err)
	}
}

func TestSkillsNeedCatalogAndSnapshotDir(t *testing.T) {
	for name, mod := range map[string]func(*Request){
		"ohne Katalog":              func(r *Request) { r.Catalog = nil },
		"ohne Snapshot-Verzeichnis": func(r *Request) { r.SnapshotDir = "" },
		"Skill nicht für die Rolle": func(r *Request) { r.Profile = implementerProfile(skillDoc) },
		"Skill unbekannt":           func(r *Request) { r.Profile = implementerProfile("ccmas-gibts-nicht") },
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, "ok")
			req := testRequest(t)
			req.Profile = implementerProfile(skillImpl)
			req.Catalog = syntheticCatalog(t)
			req.SnapshotDir = filepath.Join(t.TempDir(), "snapshots")
			mod(&req)
			if _, err := h.r.Start(context.Background(), req); err == nil {
				t.Fatal("erwartet: Fehler")
			}
			if h.exists("start.json") {
				t.Error("Prozess gestartet")
			}
		})
	}
}

func TestPolicyScopedWriteNeedsTheExplicitOption(t *testing.T) {
	scoped := func() Request {
		req := testRequest(t)
		req.Profile.PolicyProfile = PolicyScopedWrite
		return req
	}
	t.Run("ohne Option abgelehnt, nichts gestartet", func(t *testing.T) {
		h := newHarness(t, "ok")
		_, err := h.r.Start(context.Background(), scoped())
		if !errors.Is(err, ErrPolicyNotAllowed) {
			t.Fatalf("err = %v", err)
		}
		if h.exists("start.json") {
			t.Error("der Runner-Prozess wurde gestartet")
		}
	})
	t.Run("ohne Option wird auch kein Snapshot geschrieben", func(t *testing.T) {
		h := newHarness(t, "ok")
		req := scoped()
		req.Profile = implementerProfile(skillImpl)
		req.Profile.PolicyProfile = PolicyScopedWrite
		req.Catalog = syntheticCatalog(t)
		req.SnapshotDir = filepath.Join(t.TempDir(), "snapshots")
		if _, err := h.r.Start(context.Background(), req); !errors.Is(err, ErrPolicyNotAllowed) {
			t.Fatalf("err = %v", err)
		}
		if _, err := os.Stat(req.SnapshotDir); err == nil {
			t.Error("Snapshot-Verzeichnis trotz abgelehnter Policy angelegt")
		}
	})
	t.Run("mit Option zugelassen", func(t *testing.T) {
		h := newHarness(t, "ok", func(c *Config) { c.AllowScopedWrite = true })
		exec, err := h.r.Execute(context.Background(), scoped())
		if err != nil {
			t.Fatal(err)
		}
		if exec.Spec.PolicyProfile != PolicyScopedWrite || exec.State != domain.ExecSucceeded {
			t.Errorf("Spec %+v, State %s", exec.Spec, exec.State)
		}
	})
	t.Run("autonomous und Unbekanntes nie", func(t *testing.T) {
		h := newHarness(t, "ok", func(c *Config) { c.AllowScopedWrite = true })
		for _, pol := range []string{"autonomous", "wild", ""} {
			req := testRequest(t)
			req.Profile.PolicyProfile = pol
			if _, err := h.r.Start(context.Background(), req); !errors.Is(err, ErrPolicyNotAllowed) {
				t.Errorf("Policy %q: err = %v", pol, err)
			}
		}
		if h.exists("start.json") {
			t.Error("Prozess gestartet")
		}
	})
	t.Run("readonly ohne Option", func(t *testing.T) {
		exec, err := newHarness(t, "ok").r.Execute(context.Background(), testRequest(t))
		if err != nil || exec.State != domain.ExecSucceeded {
			t.Errorf("err %v, State %s", err, exec.State)
		}
	})
}

func TestNewIDHasTheContractFormat(t *testing.T) {
	a, b := NewID("verify"), NewID("verify")
	if a == b {
		t.Error("IDs wiederholen sich")
	}
	if err := domain.CheckID(a); err != nil {
		t.Error(err)
	}
}

// ---- Systemprompt, Ausgabeschema und Rundenzahl je Ausführung (CC-14) -------------------------------

const testSchema = `{"type":"object","properties":{"answer":{"type":"string"}},"required":["answer"],"additionalProperties":false}`

func TestSystemPromptAndOutputSchemaReachTheRunnerAndTheRecordKeepsOnlyHashes(t *testing.T) {
	h := newHarness(t, "structured")
	req := testRequest(t)
	req.SystemPrompt = "GEHEIMER-SYSTEMPROMPT: Rolle und Grenzen"
	req.OutputSchema = json.RawMessage(testSchema)
	exec, err := h.r.Execute(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	cmd, err := domain.ParseRunCommand([]byte(h.read("start.json")))
	if err != nil {
		t.Fatal(err)
	}
	spec := cmd.Payload.(domain.RunSpec)
	if spec.SystemPrompt != req.SystemPrompt || string(spec.OutputSchema) != testSchema {
		t.Errorf("RunSpec beim Runner: %+v", spec)
	}
	// Die strukturierte Ausgabe des Ergebnisses kommt roh am Datensatz an.
	raw, ok := exec.StructuredOutput()
	if !ok {
		t.Fatalf("keine strukturierte Ausgabe im Datensatz: %+v", exec.Terminal)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil || got["system_prompt"] != req.SystemPrompt || got["schema_present"] != true {
		t.Errorf("strukturierte Ausgabe = %s (%v)", raw, err)
	}
	// Der Datensatz hält Länge und SHA-256, nie den Text.
	sum := sha256.Sum256([]byte(req.SystemPrompt))
	if exec.Spec.SystemPromptBytes != len(req.SystemPrompt) || exec.Spec.SystemPromptSHA256 != hex.EncodeToString(sum[:]) {
		t.Errorf("Spec = %+v", exec.Spec)
	}
	schemaSum := sha256.Sum256([]byte(testSchema))
	if exec.Spec.OutputSchemaBytes != len(testSchema) || exec.Spec.OutputSchemaSHA256 != hex.EncodeToString(schemaSum[:]) {
		t.Errorf("Spec = %+v", exec.Spec)
	}
	rec, _ := exec.JSON()
	// Der Text steht nicht im RunSpec-Teil des Datensatzes (die Ereignisse sind etwas anderes: hier echot der Stub ihn).
	var parts struct {
		Spec map[string]any `json:"spec"`
	}
	_ = json.Unmarshal(rec, &parts)
	for key, v := range parts.Spec {
		if s, ok := v.(string); ok && strings.Contains(s, "GEHEIMER-SYSTEMPROMPT") {
			t.Errorf("Spec.%s trägt den Text des Systemprompts", key)
		}
	}
	if _, leaked := parts.Spec["system_prompt"]; leaked {
		t.Error("Spec trägt den Systemprompt")
	}
}

func TestWithoutSystemPromptAndSchemaTheStartIsAsBefore(t *testing.T) {
	h := newHarness(t, "ok")
	if _, err := h.r.Execute(context.Background(), testRequest(t)); err != nil {
		t.Fatal(err)
	}
	start := h.read("start.json")
	if strings.Contains(start, "system_prompt") || strings.Contains(start, "output_schema") {
		t.Errorf("leere optionale Felder im Start: %s", start)
	}
	exec, _ := newHarness(t, "ok").r.Execute(context.Background(), testRequest(t))
	if exec.Spec.SystemPromptSHA256 != "" || exec.Spec.OutputSchemaSHA256 != "" {
		t.Errorf("Spec = %+v", exec.Spec)
	}
	if _, ok := exec.StructuredOutput(); ok {
		t.Error("StructuredOutput ohne strukturierte Ausgabe")
	}
}

func TestInvalidOutputSchemaFailsBeforeTheProcessStarts(t *testing.T) {
	h := newHarness(t, "structured")
	for name, schema := range map[string]string{
		"kein Draft-07":      `{"type":"objekt"}`,
		"kein Objekt":        `["a"]`,
		"Schlüsselwort 2020": `{"type":"object","$defs":{"a":{"type":"string"}}}`,
		"anderes Metaschema": `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object"}`,
		"übergroß":           `{"type":"object","description":"` + strings.Repeat("a", 33000) + `"}`,
	} {
		t.Run(name, func(t *testing.T) {
			req := testRequest(t)
			req.OutputSchema = json.RawMessage(schema)
			if _, err := h.r.Execute(context.Background(), req); err == nil {
				t.Error("kein Fehler")
			}
			if h.exists("start.json") {
				t.Error("der Prozess wurde gestartet")
			}
		})
	}
}

func TestOversizedSystemPromptIsRejectedByTheContract(t *testing.T) {
	h := newHarness(t, "structured")
	req := testRequest(t)
	req.SystemPrompt = strings.Repeat("a", 32001)
	if _, err := h.r.Execute(context.Background(), req); err == nil || h.exists("start.json") {
		t.Errorf("err = %v, gestartet: %v", err, h.exists("start.json"))
	}
}

func TestStructuredOutputFailureIsATerminalErrorWithoutOutput(t *testing.T) {
	req := testRequest(t)
	req.OutputSchema = json.RawMessage(testSchema)
	exec, err := newHarness(t, "structured-missing").r.Execute(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := exec.StructuredOutput(); ok {
		t.Error("StructuredOutput trotz Fehler")
	}
	if e, ok := exec.TerminalError(); !ok || e.Code != "structured_output_failed" {
		t.Errorf("TerminalError = %+v, %v", e, ok)
	}
}

func TestPrewrittenSkillSnapshotIsPassedThroughAndNeedsNoCatalog(t *testing.T) {
	cat := syntheticCatalog(t)
	snap, err := cat.Resolve(domain.RoleImplementer, []string{skillImpl})
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "snapshots")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	real, _ := filepath.EvalSymlinks(dir)
	path, err := snap.Write(real)
	if err != nil {
		t.Fatal(err)
	}
	defer skills.RemoveSnapshotDir(path)

	h := newHarness(t, "structured")
	req := testRequest(t)
	req.Profile = implementerProfile(skillImpl)
	req.SkillSnapshot = &domain.SkillSnapshotRef{SnapshotID: snap.ID(), Path: path} // weder Catalog noch SnapshotDir
	exec, err := h.r.Execute(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	cmd, _ := domain.ParseRunCommand([]byte(h.read("start.json")))
	spec := cmd.Payload.(domain.RunSpec)
	if spec.SkillSnapshot == nil || spec.SkillSnapshot.SnapshotID != snap.ID() || spec.SkillSnapshot.Path != path {
		t.Errorf("skill_snapshot = %+v", spec.SkillSnapshot)
	}
	if exec.SkillSnapshotID != snap.ID() {
		t.Errorf("SkillSnapshotID = %q", exec.SkillSnapshotID)
	}
	// Ein Snapshot ohne Skills im Profil ist ein Fehler des Aufrufers.
	req.Profile = implementerProfile()
	if _, err := h.r.BuildSpec(req); err == nil {
		t.Error("Snapshot ohne Skills im Profil nicht abgewiesen")
	}
}

func TestMaxTurnsPerRequestGoesBeforeTheConfiguredValue(t *testing.T) {
	bin := t.TempDir()
	fakeExecutable(t, bin, "uv", 0o755)
	t.Setenv("PATH", bin)
	r := New(Config{SoftwareRoot: "/opt/ccmas-test", MaxTurns: 3})
	for want, got := range map[string]int{"3": 0, "7": 7} {
		argv, err := r.commandFor(got)
		if err != nil {
			t.Fatal(err)
		}
		if i := slices.Index(argv, "--max-turns"); i < 0 || argv[i+1] != want {
			t.Errorf("commandFor(%d) = %q, erwartet --max-turns %s", got, argv, want)
		}
	}
	argv, _ := New(Config{SoftwareRoot: "/opt/ccmas-test"}).commandFor(0)
	if slices.Contains(argv, "--max-turns") {
		t.Errorf("--max-turns ohne Angabe: %q", argv)
	}
	for _, bad := range []int{-1, MaxTurnsLimit + 1} {
		req := testRequest(t)
		req.MaxTurns = bad
		if _, err := newHarness(t, "ok").r.Execute(context.Background(), req); err == nil {
			t.Errorf("MaxTurns %d nicht abgewiesen", bad)
		}
	}
}
