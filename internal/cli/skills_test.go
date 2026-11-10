package cli

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexwenzel/ccmas/internal/skills"
)

// repoRoot ist die Wurzel des Repositorys; der echte Bestand dient als Testgrundlage.
const repoRoot = "../.."

var allSkillIDs = []string{
	"ccmas-antworten-abholen", "ccmas-asana-ticket", "ccmas-concept-ticket", "ccmas-feature-parallel",
	"ccmas-meilenstein-abschluss", "ccmas-morgen-briefing", "ccmas-neue-aufgabe-nehmen",
	"ccmas-obsidian-seite", "ccmas-projekt-anlegen", "ccmas-regeln-pflegen",
}

// isolate sorgt dafür, dass weder die Umgebung noch der Ort des Test-Binaries die
// Root-Auflösung beeinflussen.
func isolate(t *testing.T) {
	t.Helper()
	t.Setenv(EnvHome, "")
	oldExe, oldEnv := executablePath, lookupEnv
	executablePath = func() (string, error) { return filepath.Join(t.TempDir(), "bin", "ccmas"), nil }
	lookupEnv = os.Getenv
	t.Cleanup(func() { executablePath, lookupEnv = oldExe, oldEnv })
}

// copyCatalog kopiert den echten Bestand (.claude/skills) in einen temporären Softwareordner.
func copyCatalog(t *testing.T) string {
	t.Helper()
	dst := t.TempDir()
	src := filepath.Join(repoRoot, ".claude", "skills")
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, ".claude", "skills", rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, raw, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	return dst
}

func runSkills(t *testing.T, root string, args ...string) (int, string, string) {
	t.Helper()
	isolate(t)
	return run(t, append([]string{"skills"}, append(args, "--root", root)...)...)
}

func TestSkillsList(t *testing.T) {
	root := copyCatalog(t)

	t.Run("Text", func(t *testing.T) {
		code, out, errOut := runSkills(t, root, "list")
		if code != ExitOK || errOut != "" {
			t.Fatalf("Exit-Code %d, stderr %q", code, errOut)
		}
		for _, id := range allSkillIDs {
			if !strings.Contains(out, id) {
				t.Errorf("Ausgabe nennt %s nicht:\n%s", id, out)
			}
		}
		if !strings.Contains(out, "pending_adaptation") || !strings.Contains(out, "approved") {
			t.Errorf("Status fehlt:\n%s", out)
		}
	})
	t.Run("JSON", func(t *testing.T) {
		code, out, _ := runSkills(t, root, "list", "--json")
		if code != ExitOK {
			t.Fatalf("Exit-Code %d", code)
		}
		var got struct {
			Root   string `json:"root"`
			Skills []struct {
				ID         string   `json:"id"`
				Status     string   `json:"status"`
				Version    int      `json:"version"`
				DefaultFor []string `json:"default_for"`
			} `json:"skills"`
		}
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatalf("kein JSON: %v\n%s", err, out)
		}
		if len(got.Skills) != len(allSkillIDs) || got.Root == "" {
			t.Errorf("JSON = %+v", got)
		}
		for i, s := range got.Skills {
			if s.ID != allSkillIDs[i] || s.Version != 1 || s.DefaultFor == nil {
				t.Errorf("Eintrag %d = %+v", i, s)
			}
		}
	})
	t.Run("--role filtert", func(t *testing.T) {
		_, out, _ := runSkills(t, root, "list", "--role", "implementer")
		if !strings.Contains(out, "ccmas-feature-parallel") || strings.Contains(out, "ccmas-concept-ticket") {
			t.Errorf("implementer:\n%s", out)
		}
		_, out, _ = runSkills(t, root, "list", "--role", "reviewer", "--json")
		var got struct {
			Skills []any `json:"skills"`
		}
		if err := json.Unmarshal([]byte(out), &got); err != nil || got.Skills == nil || len(got.Skills) != 0 {
			t.Errorf("reviewer: %v, %s", err, out)
		}
	})
	t.Run("Bedienfehler", func(t *testing.T) {
		for name, args := range map[string][]string{
			"unbekannte Rolle":        {"list", "--role", "admin"},
			"unerwartetes Argument":   {"list", "extra"},
			"unbekanntes Flag":        {"list", "--gibtsnicht"},
			"unbekannter Unterbefehl": {"gibtsnicht"},
		} {
			code, out, errOut := runSkills(t, root, args...)
			if code != ExitUsage || out != "" || strings.TrimSpace(errOut) == "" {
				t.Errorf("%s: Exit-Code %d, stdout %q, stderr %q", name, code, out, errOut)
			}
		}
	})
}

func TestSkillsShow(t *testing.T) {
	root := copyCatalog(t)

	code, out, errOut := runSkills(t, root, "show", "ccmas-obsidian-seite")
	if code != ExitOK || errOut != "" {
		t.Fatalf("Exit-Code %d, stderr %q", code, errOut)
	}
	for _, want := range []string{"ccmas-obsidian-seite", "approved", "scripts/linkcheck.py", "tree_sha256", "Fachwissen/Claude/Skills/obsidian-seite.skill"} {
		if !strings.Contains(out, want) {
			t.Errorf("Ausgabe nennt %q nicht:\n%s", want, out)
		}
	}

	_, out, _ = runSkills(t, root, "show", "ccmas-feature-parallel", "--json")
	var sk skills.Skill
	if err := json.Unmarshal([]byte(out), &sk); err != nil {
		t.Fatalf("kein JSON: %v\n%s", err, out)
	}
	if sk.ID != "ccmas-feature-parallel" || sk.Status != skills.StatusPendingAdaptation || sk.Adaptation == "" ||
		len(sk.Files) != 1 || len(sk.TreeSHA256) != 64 || sk.Source.PackageSHA256 == "" {
		t.Errorf("JSON = %+v", sk)
	}

	// Unbekannter Skill: Laufzeitfehler mit Code und ID in der Meldung.
	code, out, errOut = runSkills(t, root, "show", "ccmas-gibtsnicht")
	if code != ExitError || out != "" || !strings.Contains(errOut, "ccmas-gibtsnicht") {
		t.Errorf("unbekannter Skill: Exit-Code %d, stdout %q, stderr %q", code, out, errOut)
	}
	// Bedienfehler.
	for name, args := range map[string][]string{
		"ohne ID": {"show"}, "zu viele": {"show", "a", "b"},
	} {
		if code, _, errOut := runSkills(t, root, args...); code != ExitUsage || errOut == "" {
			t.Errorf("%s: Exit-Code %d, stderr %q", name, code, errOut)
		}
	}
}

func TestSkillsValidate(t *testing.T) {
	t.Run("gültiger Bestand", func(t *testing.T) {
		root := copyCatalog(t)
		code, out, errOut := runSkills(t, root, "validate")
		if code != ExitOK || errOut != "" || !strings.Contains(out, "10 Skills") || !strings.Contains(out, "4 approved") {
			t.Errorf("Exit-Code %d, stdout %q, stderr %q", code, out, errOut)
		}
		code, out, _ = runSkills(t, root, "validate", "--json")
		var rep struct {
			OK       bool  `json:"ok"`
			Skills   int   `json:"skills"`
			Approved int   `json:"approved"`
			Pending  int   `json:"pending_adaptation"`
			Findings []any `json:"findings"`
		}
		if err := json.Unmarshal([]byte(out), &rep); err != nil {
			t.Fatalf("kein JSON: %v\n%s", err, out)
		}
		if code != ExitOK || !rep.OK || rep.Skills != 10 || rep.Approved != 4 || rep.Pending != 6 || rep.Findings == nil || len(rep.Findings) != 0 {
			t.Errorf("Exit-Code %d, Bericht %+v", code, rep)
		}
	})

	broken := func(t *testing.T) string {
		root := copyCatalog(t)
		p := filepath.Join(root, ".claude", "skills", "ccmas-asana-ticket", "SKILL.md")
		if err := os.WriteFile(p, []byte("---\nname: ccmas-asana-ticket\ndescription: manipuliert\n---\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(root, ".claude", "skills", "ccmas-schatten"), 0o755); err != nil {
			t.Fatal(err)
		}
		return root
	}
	t.Run("Befunde enden mit Exit-Code 1", func(t *testing.T) {
		code, out, errOut := runSkills(t, broken(t), "validate")
		if code != ExitError {
			t.Errorf("Exit-Code %d, erwartet %d", code, ExitError)
		}
		if out != "" {
			t.Errorf("stdout sollte leer sein: %q", out)
		}
		for _, want := range []string{"changed_resource", "ccmas-asana-ticket", "SKILL.md", "unregistered_skill", "ccmas-schatten"} {
			if !strings.Contains(errOut, want) {
				t.Errorf("stderr nennt %q nicht:\n%s", want, errOut)
			}
		}
	})
	t.Run("Befunde als JSON", func(t *testing.T) {
		code, out, errOut := runSkills(t, broken(t), "validate", "--json")
		if code != ExitError {
			t.Errorf("Exit-Code %d, erwartet %d", code, ExitError)
		}
		var rep struct {
			OK       bool `json:"ok"`
			Findings []struct {
				Code  string `json:"code"`
				Skill string `json:"skill"`
				Path  string `json:"path"`
			} `json:"findings"`
		}
		if err := json.Unmarshal([]byte(out), &rep); err != nil {
			t.Fatalf("kein JSON: %v\n%s", err, out)
		}
		if rep.OK || len(rep.Findings) != 2 {
			t.Fatalf("Bericht = %+v", rep)
		}
		got := map[string]string{}
		for _, f := range rep.Findings {
			got[f.Code] = f.Skill + "|" + f.Path
		}
		if got["changed_resource"] != "ccmas-asana-ticket|SKILL.md" || !strings.HasPrefix(got["unregistered_skill"], "ccmas-schatten|") {
			t.Errorf("Befunde = %v", got)
		}
		if !strings.Contains(errOut, "Befund") {
			t.Errorf("stderr = %q", errOut)
		}
	})
	t.Run("Wurzel ohne Manifest", func(t *testing.T) {
		code, out, errOut := runSkills(t, t.TempDir(), "validate")
		if code != ExitError || out != "" || !strings.Contains(errOut, "missing_manifest") {
			t.Errorf("Exit-Code %d, stdout %q, stderr %q", code, out, errOut)
		}
	})
	t.Run("echter Bestand des Repositorys", func(t *testing.T) {
		abs, _ := filepath.Abs(repoRoot)
		if code, _, errOut := runSkills(t, abs, "validate"); code != ExitOK {
			t.Errorf("Repository-Bestand ungültig: Exit-Code %d\n%s", code, errOut)
		}
	})
}

func TestSkillsSnapshot(t *testing.T) {
	root := copyCatalog(t)

	t.Run("Standardausstattung, Text", func(t *testing.T) {
		out := t.TempDir()
		code, stdout, errOut := runSkills(t, root, "snapshot", "--role", "concept", "--out", out)
		if code != ExitOK || errOut != "" {
			t.Fatalf("Exit-Code %d, stderr %q", code, errOut)
		}
		var id, path string
		for _, line := range strings.Split(stdout, "\n") {
			if v, ok := strings.CutPrefix(line, "snapshot_id: "); ok {
				id = v
			}
			if v, ok := strings.CutPrefix(line, "pfad: "); ok {
				path = v
			}
		}
		if !strings.HasPrefix(id, "sha256:") || path != filepath.Join(out, strings.TrimPrefix(id, "sha256:")) {
			t.Fatalf("Ausgabe = %q (id %q, pfad %q)", stdout, id, path)
		}
		t.Cleanup(func() { skills.RemoveSnapshotDir(out) })
		m, err := skills.VerifySnapshotDir(path)
		if err != nil {
			t.Fatalf("Snapshot nicht verifizierbar: %v", err)
		}
		if m.SnapshotID != id || len(m.Skills) != 2 {
			t.Errorf("snapshot.json = %+v", m)
		}
		// Gleicher Aufruf noch einmal: wiederverwendet, kein Fehler, kein zweites Verzeichnis.
		code, stdout2, _ := runSkills(t, root, "snapshot", "--role", "concept", "--out", out)
		entries, _ := os.ReadDir(out)
		if code != ExitOK || stdout2 != stdout || len(entries) != 1 {
			t.Errorf("Wiederholung: Exit-Code %d, stdout %q, Einträge %d", code, stdout2, len(entries))
		}
	})
	t.Run("Rolle ohne Standardausstattung, zweimal hintereinander", func(t *testing.T) {
		// Regressionstest: Ein Snapshot ohne Skills war nicht idempotent schreibbar (Befund 1).
		out := t.TempDir()
		t.Cleanup(func() { skills.RemoveSnapshotDir(out) })
		var first string
		for i := 1; i <= 2; i++ {
			code, stdout, errOut := runSkills(t, root, "snapshot", "--role", "reviewer", "--out", out)
			if code != ExitOK || errOut != "" {
				t.Fatalf("Aufruf %d: Exit-Code %d, stderr %q", i, code, errOut)
			}
			if i == 1 {
				first = stdout
			} else if stdout != first {
				t.Errorf("Aufruf 2 weicht ab:\n%q\n%q", stdout, first)
			}
		}
		var id, path string
		for _, line := range strings.Split(first, "\n") {
			if v, ok := strings.CutPrefix(line, "snapshot_id: "); ok {
				id = v
			}
			if v, ok := strings.CutPrefix(line, "pfad: "); ok {
				path = v
			}
		}
		if path != filepath.Join(out, strings.TrimPrefix(id, "sha256:")) {
			t.Errorf("Pfad %q passt nicht zur ID %q", path, id)
		}
		m, err := skills.VerifySnapshotDir(path)
		if err != nil || m.SnapshotID != id || len(m.Skills) != 0 {
			t.Errorf("Verifikation: %+v, %v", m, err)
		}
		if entries, _ := os.ReadDir(out); len(entries) != 1 {
			t.Errorf("Zielverzeichnis enthält %d Einträge", len(entries))
		}
	})
	t.Run("ausdrückliche Auswahl, JSON", func(t *testing.T) {
		out := t.TempDir()
		t.Cleanup(func() { skills.RemoveSnapshotDir(out) })
		code, stdout, errOut := runSkills(t, root, "snapshot", "--role", "documentation",
			"--skill", "ccmas-regeln-pflegen", "--skill", "ccmas-obsidian-seite", "--out", out, "--json")
		if code != ExitOK {
			t.Fatalf("Exit-Code %d, stderr %q", code, errOut)
		}
		var got struct {
			ID     string `json:"snapshot_id"`
			Path   string `json:"path"`
			Role   string `json:"role"`
			Skills []struct {
				ID         string `json:"id"`
				Version    int    `json:"version"`
				TreeSHA256 string `json:"tree_sha256"`
			} `json:"skills"`
		}
		if err := json.Unmarshal([]byte(stdout), &got); err != nil {
			t.Fatalf("kein JSON: %v\n%s", err, stdout)
		}
		if got.Role != "documentation" || len(got.Skills) != 2 || got.Skills[0].ID != "ccmas-obsidian-seite" || got.Skills[1].ID != "ccmas-regeln-pflegen" {
			t.Errorf("JSON = %+v", got)
		}
		if _, err := skills.VerifySnapshotDir(got.Path); err != nil {
			t.Errorf("Snapshot nicht verifizierbar: %v", err)
		}
	})
	t.Run("Resolve-Fehler enden mit Exit-Code 1 und schreiben nichts", func(t *testing.T) {
		cases := map[string]struct {
			args []string
			code string
		}{
			"pending Skill":          {[]string{"--role", "implementer", "--skill", "ccmas-feature-parallel"}, "skill_not_approved"},
			"unbekannter Skill":      {[]string{"--role", "concept", "--skill", "ccmas-gibtsnicht"}, "unknown_skill"},
			"Rules-Skill für Prüfer": {[]string{"--role", "reviewer", "--skill", "ccmas-regeln-pflegen"}, "skill_not_allowed_for_role"},
			"Skill nicht für Rolle":  {[]string{"--role", "implementer", "--skill", "ccmas-concept-ticket"}, "skill_not_allowed_for_role"},
		}
		for name, tc := range cases {
			out := t.TempDir()
			args := append([]string{"snapshot"}, tc.args...)
			args = append(args, "--out", out)
			code, stdout, errOut := runSkills(t, root, args...)
			if code != ExitError || stdout != "" {
				t.Errorf("%s: Exit-Code %d, stdout %q", name, code, stdout)
			}
			if entries, _ := os.ReadDir(out); len(entries) != 0 {
				t.Errorf("%s: Zielverzeichnis nicht leer", name)
			}
			if !strings.Contains(errOut, tc.args[len(tc.args)-1]) {
				t.Errorf("%s: stderr nennt den Skill nicht: %q", name, errOut)
			}
		}
	})
	t.Run("Änderung am Skill nach dem Manifest wird erkannt", func(t *testing.T) {
		root := copyCatalog(t)
		p := filepath.Join(root, ".claude", "skills", "ccmas-concept-ticket", "SKILL.md")
		if err := os.WriteFile(p, []byte("---\nname: ccmas-concept-ticket\ndescription: x\n---\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		out := t.TempDir()
		code, _, errOut := runSkills(t, root, "snapshot", "--role", "concept", "--out", out)
		if code != ExitError || !strings.Contains(errOut, "changed_resource") {
			t.Errorf("Exit-Code %d, stderr %q", code, errOut)
		}
	})
	t.Run("Bedienfehler", func(t *testing.T) {
		out := t.TempDir()
		for name, args := range map[string][]string{
			"ohne --role":           {"snapshot", "--out", out},
			"ohne --out":            {"snapshot", "--role", "concept"},
			"unbekannte Rolle":      {"snapshot", "--role", "admin", "--out", out},
			"unerwartetes Argument": {"snapshot", "--role", "concept", "--out", out, "extra"},
		} {
			code, stdout, errOut := runSkills(t, root, args...)
			if code != ExitUsage || stdout != "" || strings.TrimSpace(errOut) == "" {
				t.Errorf("%s: Exit-Code %d, stdout %q, stderr %q", name, code, stdout, errOut)
			}
		}
		if entries, _ := os.ReadDir(out); len(entries) != 0 {
			t.Error("Bedienfehler haben etwas geschrieben")
		}
	})
}

func TestSkillsRootResolution(t *testing.T) {
	good := copyCatalog(t)
	empty := t.TempDir()

	validate := func(t *testing.T, args ...string) (int, string, string) {
		t.Helper()
		return run(t, append([]string{"skills", "validate"}, args...)...)
	}

	t.Run("--root hat Vorrang vor CCMAS_HOME", func(t *testing.T) {
		isolate(t)
		t.Setenv(EnvHome, empty)
		code, out, _ := validate(t, "--root", good)
		if code != ExitOK || !strings.Contains(out, good) {
			t.Errorf("Exit-Code %d, stdout %q", code, out)
		}
	})
	t.Run("CCMAS_HOME ohne --root", func(t *testing.T) {
		isolate(t)
		t.Setenv(EnvHome, good)
		code, out, _ := validate(t)
		if code != ExitOK || !strings.Contains(out, good) {
			t.Errorf("Exit-Code %d, stdout %q", code, out)
		}
	})
	t.Run("CCMAS_HOME hat Vorrang vor dem Binary-Ort", func(t *testing.T) {
		isolate(t)
		t.Setenv(EnvHome, empty)
		executablePath = func() (string, error) { return filepath.Join(good, "bin", "ccmas"), nil }
		code, _, errOut := validate(t)
		if code != ExitError || !strings.Contains(errOut, "missing_manifest") {
			t.Errorf("Exit-Code %d, stderr %q", code, errOut)
		}
	})
	t.Run("Verzeichnis über dem Binary", func(t *testing.T) {
		isolate(t)
		executablePath = func() (string, error) { return filepath.Join(good, "bin", "ccmas"), nil }
		code, out, errOut := validate(t)
		if code != ExitOK || !strings.Contains(out, filepath.Clean(good)) {
			t.Errorf("Exit-Code %d, stdout %q, stderr %q", code, out, errOut)
		}
	})
	t.Run("Binary über Symlink gestartet", func(t *testing.T) {
		isolate(t)
		realBin := filepath.Join(good, "bin")
		if err := os.MkdirAll(realBin, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(realBin, "ccmas"), nil, 0o755); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(t.TempDir(), "ccmas")
		if err := os.Symlink(filepath.Join(realBin, "ccmas"), link); err != nil {
			t.Skipf("Symlinks nicht möglich: %v", err)
		}
		executablePath = func() (string, error) { return link, nil }
		if code, _, errOut := validate(t); code != ExitOK {
			t.Errorf("Exit-Code %d, stderr %q", code, errOut)
		}
	})
	t.Run("Binary-Ort ohne Manifest wird nicht verwendet", func(t *testing.T) {
		isolate(t)
		executablePath = func() (string, error) { return filepath.Join(empty, "bin", "ccmas"), nil }
		code, out, errOut := validate(t)
		if code != ExitError || out != "" || !strings.Contains(errOut, EnvHome) || !strings.Contains(errOut, "--root") {
			t.Errorf("Exit-Code %d, stdout %q, stderr %q", code, out, errOut)
		}
	})
	t.Run("Arbeitsverzeichnis wird nie gelesen", func(t *testing.T) {
		isolate(t)
		// Das Arbeitsverzeichnis enthält einen vollständigen, gültigen Bestand.
		t.Chdir(good)
		code, out, errOut := validate(t)
		if code != ExitError || out != "" || !strings.Contains(errOut, "nicht gefunden") {
			t.Errorf("ohne Root: Exit-Code %d, stdout %q, stderr %q", code, out, errOut)
		}
		// Mit ausdrücklicher anderer Wurzel gewinnt diese, nicht das Arbeitsverzeichnis.
		code, _, errOut = validate(t, "--root", empty)
		if code != ExitError || !strings.Contains(errOut, "missing_manifest") {
			t.Errorf("--root auf leeren Ordner: Exit-Code %d, stderr %q", code, errOut)
		}
		for _, cmd := range [][]string{{"skills", "list"}, {"skills", "show", "ccmas-asana-ticket"}, {"skills", "snapshot", "--role", "concept", "--out", t.TempDir()}} {
			if code, _, _ := run(t, cmd...); code != ExitError {
				t.Errorf("%v: Exit-Code %d, erwartet %d", cmd, code, ExitError)
			}
		}
	})
	t.Run("relative Wurzel wird gegen das Arbeitsverzeichnis des Aufrufs aufgelöst, nur wenn ausdrücklich angegeben", func(t *testing.T) {
		isolate(t)
		t.Chdir(filepath.Dir(good))
		code, _, errOut := validate(t, "--root", filepath.Base(good))
		if code != ExitOK {
			t.Errorf("Exit-Code %d, stderr %q", code, errOut)
		}
	})
}

func TestSkillsHelp(t *testing.T) {
	code, out, _ := run(t, "skills", "--help")
	if code != ExitOK {
		t.Fatalf("Exit-Code %d", code)
	}
	for _, want := range []string{"list", "show", "validate", "snapshot", "--root", EnvHome} {
		if !strings.Contains(out, want) {
			t.Errorf("Hilfe nennt %q nicht:\n%s", want, out)
		}
	}
	if strings.Contains(out, "noch nicht verfügbar") {
		t.Error("skills ist als nicht verfügbar gekennzeichnet")
	}
	// Ohne Unterbefehl zeigt skills die Hilfe.
	if code, out, _ := run(t, "skills"); code != ExitOK || !strings.Contains(out, "validate") {
		t.Errorf("ccmas skills: Exit-Code %d, stdout %q", code, out)
	}
	// rules bleibt geplant.
	if code, _, errOut := run(t, "rules"); code != ExitNotAvailable || !strings.Contains(errOut, "noch nicht verfügbar") {
		t.Errorf("rules: Exit-Code %d, stderr %q", code, errOut)
	}
}
