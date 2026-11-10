package runner

import (
	"context"
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
