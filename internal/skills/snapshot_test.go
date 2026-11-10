package skills

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/alexwenzel/ccmas/internal/domain"
)

var update = flag.Bool("update", false, "Beispiel-Snapshot unter contracts/fixtures/skill-snapshots/example neu erzeugen")

// exampleSkills ist der kleine, feste Test-Katalog, aus dem die gemeinsame Beispiel-Fixture entsteht.
func exampleSkills() []testSkill {
	return []testSkill{
		{id: "ccmas-demo-know", status: StatusApproved, category: CategoryKnowledge,
			roles: []domain.Role{domain.RoleConcept, domain.RoleDocumentation}, defaultFor: []domain.Role{domain.RoleConcept},
			writes: []string{"vault"},
			files: map[string]string{
				"SKILL.md":         "---\nname: ccmas-demo-know\ndescription: Beispiel für einen Wissens-Skill.\n---\n\n# Demo\n\nText mit Umlauten: äöüß.\n",
				"scripts/check.py": "print('ok')\n",
			}},
		{id: "ccmas-demo-track", status: StatusApproved, category: CategoryTracking,
			roles: []domain.Role{domain.RoleConcept, domain.RoleSupervisor}, defaultFor: []domain.Role{domain.RoleConcept},
			writes: []string{"asana"},
			files: map[string]string{
				"SKILL.md": "---\nname: ccmas-demo-track\ndescription: Beispiel für einen Tracking-Skill.\n---\n\n# Track\n",
			}},
	}
}

func resolveWrite(t *testing.T, c *Catalog, role domain.Role, ids []string) (Snapshot, string, string) {
	t.Helper()
	snap, err := c.Resolve(role, ids)
	if err != nil {
		t.Fatal(err)
	}
	out := t.TempDir()
	makeWritable(t, out)
	dir, err := snap.Write(out)
	if err != nil {
		t.Fatal(err)
	}
	return snap, out, dir
}

func TestSnapshotWriteLayout(t *testing.T) {
	f := newCatalogFixture(t, exampleSkills())
	snap, out, dir := resolveWrite(t, f.mustLoad(fixedClock()), domain.RoleConcept, nil)

	if want := filepath.Join(out, strings.TrimPrefix(snap.ID(), "sha256:")); dir != want {
		t.Errorf("Pfad = %s, erwartet %s", dir, want)
	}
	files, dirs := treeFiles(t, dir)
	wantFiles := []string{
		".claude-plugin/plugin.json", "skills/ccmas-demo-know/SKILL.md", "skills/ccmas-demo-know/scripts/check.py",
		"skills/ccmas-demo-track/SKILL.md", "snapshot.json",
	}
	var gotFiles []string
	for p := range files {
		gotFiles = append(gotFiles, p)
	}
	slices.Sort(gotFiles)
	if !slices.Equal(gotFiles, wantFiles) {
		t.Errorf("Dateien = %v, erwartet %v", gotFiles, wantFiles)
	}
	slices.Sort(dirs)
	wantDirs := []string{".claude-plugin", "skills", "skills/ccmas-demo-know", "skills/ccmas-demo-know/scripts", "skills/ccmas-demo-track"}
	if !slices.Equal(dirs, wantDirs) {
		t.Errorf("Verzeichnisse = %v, erwartet %v", dirs, wantDirs)
	}
	if files[".claude-plugin/plugin.json"] != `{"name":"ccmas"}` {
		t.Errorf("plugin.json = %q", files[".claude-plugin/plugin.json"])
	}
	// Skill-Dateien sind byteweise die der Quelle.
	src, _ := os.ReadFile(filepath.Join(f.skillDir("ccmas-demo-know"), "scripts", "check.py"))
	if files["skills/ccmas-demo-know/scripts/check.py"] != string(src) {
		t.Error("Skill-Datei im Snapshot weicht von der Quelle ab")
	}
	// snapshot.json erfüllt den gemeinsamen Vertrag und passt zum Wert.
	doc, err := domain.ParseSkillSnapshot([]byte(files["snapshot.json"]))
	if err != nil {
		t.Fatal(err)
	}
	if doc.SnapshotID != snap.ID() || doc.Role != domain.RoleConcept || doc.CreatedAt.Format("2006-01-02T15:04:05Z") != "2026-11-10T12:00:00Z" {
		t.Errorf("snapshot.json = %+v", doc)
	}
	if doc.ManifestSHA256 != f.mustLoad().ManifestSHA256() {
		t.Error("manifest_sha256 passt nicht zum geladenen Manifest")
	}
	for _, forbidden := range []string{f.root, os.TempDir(), "/Users/"} {
		if strings.Contains(files["snapshot.json"], forbidden) {
			t.Errorf("snapshot.json enthält den Pfad %q", forbidden)
		}
	}

	if runtime.GOOS != "windows" {
		for p := range files {
			if mode := statMode(t, filepath.Join(dir, p)); mode != 0o444 {
				t.Errorf("%s hat Modus %o, erwartet 444", p, mode)
			}
		}
		for _, d := range append(dirs, ".") {
			if mode := statMode(t, filepath.Join(dir, d)); mode != 0o555 {
				t.Errorf("Verzeichnis %s hat Modus %o, erwartet 555", d, mode)
			}
		}
		if err := os.WriteFile(filepath.Join(dir, "skills", "ccmas-demo-know", "SKILL.md"), []byte("x"), 0o644); err == nil && os.Getuid() != 0 {
			t.Error("Skill-Datei im Snapshot ist beschreibbar")
		}
	}
	if _, err := VerifySnapshotDir(dir); err != nil {
		t.Errorf("frisch geschriebener Snapshot nicht verifizierbar: %v", err)
	}
	// Kein Temp-Verzeichnis bleibt zurück.
	entries, _ := os.ReadDir(out)
	if len(entries) != 1 {
		t.Errorf("Zielverzeichnis enthält %d Einträge: %v", len(entries), entries)
	}
}

func statMode(t *testing.T, p string) os.FileMode {
	t.Helper()
	fi, err := os.Lstat(p)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Mode().Perm()
}

func TestSnapshotWriteReusesExisting(t *testing.T) {
	f := newCatalogFixture(t, exampleSkills())
	c := f.mustLoad(fixedClock())
	snap, out, dir := resolveWrite(t, c, domain.RoleConcept, nil)
	before, _ := treeFiles(t, dir)

	again, err := snap.Write(out)
	if err != nil {
		t.Fatalf("zweites Schreiben: %v", err)
	}
	if again != dir {
		t.Errorf("zweiter Pfad %s != %s", again, dir)
	}
	// Ein gleich aufgelöster Snapshot (neue Zeit) wird auf das vorhandene Verzeichnis abgebildet.
	snap2, err := f.mustLoad().Resolve(domain.RoleConcept, nil)
	if err != nil {
		t.Fatal(err)
	}
	if again, err := snap2.Write(out); err != nil || again != dir {
		t.Errorf("Wiederverwendung: %s, %v", again, err)
	}
	after, _ := treeFiles(t, dir)
	if !equalMaps(before, after) {
		t.Error("vorhandener Snapshot wurde verändert")
	}
	entries, _ := os.ReadDir(out)
	if len(entries) != 1 {
		t.Errorf("Zielverzeichnis enthält %d Einträge", len(entries))
	}
}

func TestSnapshotWriteNeverOverwrites(t *testing.T) {
	f := newCatalogFixture(t, exampleSkills())
	snap, out, dir := resolveWrite(t, f.mustLoad(fixedClock()), domain.RoleConcept, nil)

	// Jemand verändert eine Datei im vorhandenen Snapshot.
	victim := filepath.Join(dir, "skills", "ccmas-demo-know", "SKILL.md")
	if err := os.Chmod(filepath.Dir(victim), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(victim, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(victim, []byte("manipuliert"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := snap.Write(out)
	wantErr(t, err, CodeSnapshotMismatch, "", "skills/ccmas-demo-know/SKILL.md")
	if got, _ := os.ReadFile(victim); string(got) != "manipuliert" {
		t.Errorf("Write hat das vorhandene Verzeichnis angefasst: %q", got)
	}

	// Ein Verzeichnis mit der Kennung, das einen anderen Snapshot enthält, wird ebenfalls nicht übernommen.
	other, err := f.mustLoad(fixedClock()).Resolve(domain.RoleDocumentation, []string{"ccmas-demo-know"})
	if err != nil {
		t.Fatal(err)
	}
	out2 := t.TempDir()
	makeWritable(t, out2)
	otherDir, err := other.Write(out2)
	if err != nil {
		t.Fatal(err)
	}
	// Den anderen Snapshot unter dem Namen von snap ablegen.
	forged := filepath.Join(out2, strings.TrimPrefix(snap.ID(), "sha256:"))
	if err := os.Rename(otherDir, forged); err != nil {
		t.Fatal(err)
	}
	_, err = snap.Write(out2)
	wantErr(t, err, CodeSnapshotMismatch, "", "")
}

func TestSnapshotWriteErrors(t *testing.T) {
	t.Run("leerer Snapshot", func(t *testing.T) {
		_, err := Snapshot{}.Write(t.TempDir())
		wantErr(t, err, CodeInvalidRequest, "", "")
	})
	t.Run("Zielverzeichnis ist eine Datei", func(t *testing.T) {
		f := newCatalogFixture(t, exampleSkills())
		snap, err := f.mustLoad().Resolve(domain.RoleConcept, nil)
		if err != nil {
			t.Fatal(err)
		}
		file := filepath.Join(t.TempDir(), "datei")
		if err := os.WriteFile(file, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		_, err = snap.Write(file)
		wantErr(t, err, CodeSnapshotWrite, "", "")
	})
	t.Run("Zielverzeichnis nicht beschreibbar, nichts bleibt zurück", func(t *testing.T) {
		if os.Getuid() == 0 || runtime.GOOS == "windows" {
			t.Skip("Dateirechte greifen hier nicht")
		}
		f := newCatalogFixture(t, exampleSkills())
		snap, err := f.mustLoad().Resolve(domain.RoleConcept, nil)
		if err != nil {
			t.Fatal(err)
		}
		out := t.TempDir()
		if err := os.Chmod(out, 0o555); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(out, 0o755) })
		_, err = snap.Write(out)
		wantErr(t, err, CodeSnapshotWrite, "", "")
		entries, _ := os.ReadDir(out)
		if len(entries) != 0 {
			t.Errorf("Zielverzeichnis enthält %v", entries)
		}
	})
}

func TestSnapshotDirTampered(t *testing.T) {
	setup := func(t *testing.T) (dir string) {
		f := newCatalogFixture(t, exampleSkills())
		_, _, dir = resolveWrite(t, f.mustLoad(fixedClock()), domain.RoleConcept, nil)
		return dir
	}
	open := func(t *testing.T, dir, _ string) {
		t.Helper()
		_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
			if err == nil && d.IsDir() {
				_ = os.Chmod(p, 0o755)
			}
			return nil
		})
	}
	skillFile := "skills/ccmas-demo-know/scripts/check.py"

	cases := map[string]struct {
		mutate func(t *testing.T, dir string)
		path   string
	}{
		"Datei verändert": {func(t *testing.T, dir string) {
			open(t, dir, skillFile)
			p := filepath.Join(dir, skillFile)
			_ = os.Chmod(p, 0o644)
			if err := os.WriteFile(p, []byte("print('boese')\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}, skillFile},
		"Datei gelöscht": {func(t *testing.T, dir string) {
			open(t, dir, skillFile)
			if err := os.Remove(filepath.Join(dir, skillFile)); err != nil {
				t.Fatal(err)
			}
		}, skillFile},
		"zusätzliche Datei": {func(t *testing.T, dir string) {
			open(t, dir, "skills/ccmas-demo-know/extra.txt")
			if err := os.WriteFile(filepath.Join(dir, "skills", "ccmas-demo-know", "extra.txt"), []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
		}, "skills/ccmas-demo-know/extra.txt"},
		"zusätzliche Datei im Wurzelverzeichnis": {func(t *testing.T, dir string) {
			open(t, dir, "x")
			if err := os.WriteFile(filepath.Join(dir, ".DS_Store"), []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
		}, ".DS_Store"},
		"zusätzliches Verzeichnis": {func(t *testing.T, dir string) {
			open(t, dir, "x")
			if err := os.Mkdir(filepath.Join(dir, "skills", "ccmas-demo-fremd"), 0o755); err != nil {
				t.Fatal(err)
			}
		}, "skills/ccmas-demo-fremd"},
		"zusätzlicher Skill": {func(t *testing.T, dir string) {
			open(t, dir, "x")
			d := filepath.Join(dir, "skills", "ccmas-demo-fremd")
			if err := os.Mkdir(d, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(d, "SKILL.md"), []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
		}, "skills/ccmas-demo-fremd"},
		"Skill-Verzeichnis gelöscht": {func(t *testing.T, dir string) {
			open(t, dir, "x")
			if err := os.RemoveAll(filepath.Join(dir, "skills", "ccmas-demo-track")); err != nil {
				t.Fatal(err)
			}
		}, "skills/ccmas-demo-track/SKILL.md"},
		"plugin.json verändert": {func(t *testing.T, dir string) {
			p := filepath.Join(dir, ".claude-plugin", "plugin.json")
			_ = os.Chmod(filepath.Dir(p), 0o755)
			_ = os.Chmod(p, 0o644)
			if err := os.WriteFile(p, []byte(`{"name":"anders"}`), 0o644); err != nil {
				t.Fatal(err)
			}
		}, ".claude-plugin/plugin.json"},
		"plugin.json gelöscht": {func(t *testing.T, dir string) {
			_ = os.Chmod(filepath.Join(dir, ".claude-plugin"), 0o755)
			if err := os.Remove(filepath.Join(dir, ".claude-plugin", "plugin.json")); err != nil {
				t.Fatal(err)
			}
		}, ".claude-plugin/plugin.json"},
		"Datei durch Symlink ersetzt": {func(t *testing.T, dir string) {
			open(t, dir, skillFile)
			p := filepath.Join(dir, skillFile)
			if err := os.Remove(p); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(dir, "snapshot.json"), p); err != nil {
				t.Skipf("Symlinks nicht möglich: %v", err)
			}
		}, skillFile},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			dir := setup(t)
			if runtime.GOOS == "windows" {
				t.Skip("Dateirechte")
			}
			if _, err := VerifySnapshotDir(dir); err != nil {
				t.Fatalf("unveränderter Snapshot: %v", err)
			}
			tc.mutate(t, dir)
			_, err := VerifySnapshotDir(dir)
			wantErr(t, err, CodeSnapshotMismatch, "", tc.path)
		})
	}

	t.Run("snapshot.json manipuliert", func(t *testing.T) {
		rewrite := func(t *testing.T, mutate func(m map[string]any)) (string, error) {
			dir := setup(t)
			_ = os.Chmod(dir, 0o755)
			p := filepath.Join(dir, "snapshot.json")
			_ = os.Chmod(p, 0o644)
			raw, _ := os.ReadFile(p)
			var m map[string]any
			if err := json.Unmarshal(raw, &m); err != nil {
				t.Fatal(err)
			}
			mutate(m)
			out, _ := json.Marshal(m)
			if err := os.WriteFile(p, out, 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := VerifySnapshotDir(dir)
			return dir, err
		}
		_, err := rewrite(t, func(m map[string]any) { m["role"] = "reviewer" })
		wantErr(t, err, CodeSnapshotMismatch, "", "snapshot.json")
		_, err = rewrite(t, func(m map[string]any) {
			m["skills"].([]any)[0].(map[string]any)["version"] = 2
		})
		wantErr(t, err, CodeSnapshotMismatch, "", "snapshot.json")
		_, err = rewrite(t, func(m map[string]any) {
			m["skills"].([]any)[0].(map[string]any)["files"].([]any)[0].(map[string]any)["sha256"] = strings.Repeat("0", 64)
		})
		wantErr(t, err, CodeSnapshotMismatch, "ccmas-demo-know", "snapshot.json")
		// Konsistent umgeschrieben (Rolle und ID), aber das Verzeichnis trägt noch die alte Kennung.
		_, err = rewrite(t, func(m map[string]any) {
			m["role"] = "reviewer"
			skills := []snapshotSkill{}
			for _, s := range m["skills"].([]any) {
				sm := s.(map[string]any)
				skills = append(skills, snapshotSkill{id: sm["id"].(string), version: int(sm["version"].(float64)), treeSHA256: sm["tree_sha256"].(string)})
			}
			m["snapshot_id"] = computeSnapshotID(domain.RoleReviewer, skills)
		})
		wantErr(t, err, CodeSnapshotMismatch, "", "snapshot.json")
		if err != nil && !strings.Contains(err.Error(), "Verzeichnisname") {
			t.Errorf("Meldung nennt den Verzeichnisnamen nicht: %v", err)
		}
		_, err = rewrite(t, func(m map[string]any) { m["extra"] = true })
		wantErr(t, err, CodeInvalidSnapshot, "", "snapshot.json")
		_, err = rewrite(t, func(m map[string]any) { m["schema_version"] = 2 })
		wantErr(t, err, CodeInvalidSnapshot, "", "snapshot.json")
	})
	t.Run("snapshot.json fehlt oder kein Verzeichnis", func(t *testing.T) {
		dir := setup(t)
		_ = os.Chmod(dir, 0o755)
		if err := os.Remove(filepath.Join(dir, "snapshot.json")); err != nil {
			t.Fatal(err)
		}
		_, err := VerifySnapshotDir(dir)
		wantErr(t, err, CodeInvalidSnapshot, "", "snapshot.json")
		_, err = VerifySnapshotDir(filepath.Join(dir, "gibtsnicht"))
		wantErr(t, err, CodeInvalidSnapshot, "", "")
		_, err = VerifySnapshotDir(filepath.Join(dir, ".claude-plugin", "plugin.json"))
		wantErr(t, err, CodeSnapshotMismatch, "", "")
	})
	t.Run("mehrere Abweichungen werden gesammelt", func(t *testing.T) {
		dir := setup(t)
		open(t, dir, skillFile)
		_ = os.Chmod(filepath.Join(dir, skillFile), 0o644)
		_ = os.WriteFile(filepath.Join(dir, skillFile), []byte("x"), 0o644)
		_ = os.WriteFile(filepath.Join(dir, "skills", "ccmas-demo-know", "extra.txt"), []byte("x"), 0o644)
		_, err := VerifySnapshotDir(dir)
		wantErr(t, err, CodeSnapshotMismatch, "", skillFile)
		wantErr(t, err, CodeSnapshotMismatch, "", "skills/ccmas-demo-know/extra.txt")
	})
}

// TestExampleSnapshotFixture belegt, dass die gemeinsame Beispiel-Snapshot-Struktur vom Go-Code
// stammt: der Test erzeugt sie aus dem Test-Katalog neu und vergleicht Byte für Byte. Mit
// -update wird die Fixture neu geschrieben (go test ./internal/skills -run Example -update).
func TestExampleSnapshotFixture(t *testing.T) {
	const fixture = "../../contracts/fixtures/skill-snapshots/example"
	f := newCatalogFixture(t, exampleSkills())
	snap, _, dir := resolveWrite(t, f.mustLoad(fixedClock()), domain.RoleConcept, nil)

	if *update {
		if err := os.RemoveAll(fixture); err != nil {
			t.Fatal(err)
		}
		files, _ := treeFiles(t, dir)
		for rel, content := range files {
			full := filepath.Join(fixture, filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}

	want, _ := treeFiles(t, dir)
	got, _ := treeFiles(t, fixture)
	if len(got) == 0 {
		t.Fatalf("Fixture %s fehlt; mit -update erzeugen", fixture)
	}
	for rel, w := range want {
		g, ok := got[rel]
		switch {
		case !ok:
			t.Errorf("Fixture: %s fehlt", rel)
		case g != w:
			t.Errorf("Fixture: %s weicht vom Go-Code ab (mit -update neu erzeugen, falls gewollt)", rel)
		}
	}
	for rel := range got {
		if _, ok := want[rel]; !ok {
			t.Errorf("Fixture: zusätzliche Datei %s", rel)
		}
	}

	// Die Fixture ist ein gültiger Snapshot und trägt die erwartete ID.
	m, err := VerifySnapshotDir(fixture)
	if err != nil {
		t.Fatalf("VerifySnapshotDir(Fixture): %v", err)
	}
	if m.SnapshotID != snap.ID() {
		t.Errorf("snapshot_id der Fixture %s != %s", m.SnapshotID, snap.ID())
	}
	raw, err := os.ReadFile(filepath.Join(fixture, "snapshot.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := domain.Validate(domain.TargetSkillSnapshot, raw); err != nil {
		t.Errorf("Vertrag skill-snapshot-v1: %v", err)
	}
	if !bytes.HasSuffix(raw, []byte("}\n")) {
		t.Error("snapshot.json endet nicht mit Zeilenende")
	}
}

// Eine Rolle ohne freigegebenen Skill ergibt einen gültigen Snapshot ohne Skills. Er muss sich
// wie jeder andere schreiben, verifizieren und idempotent wiederverwenden lassen
// (Regressionstest zu Befund 1 der Befundrunde 1).
func TestSnapshotWithoutSkills(t *testing.T) {
	f := newCatalogFixture(t, exampleSkills())
	c := f.mustLoad(fixedClock())
	snap, out, dir := resolveWrite(t, c, domain.RoleReviewer, nil)
	if len(snap.SkillIDs()) != 0 {
		t.Fatalf("Standard reviewer = %v, erwartet leer", snap.SkillIDs())
	}

	files, dirs := treeFiles(t, dir)
	var gotFiles []string
	for p := range files {
		gotFiles = append(gotFiles, p)
	}
	slices.Sort(gotFiles)
	if want := []string{".claude-plugin/plugin.json", "snapshot.json"}; !slices.Equal(gotFiles, want) {
		t.Errorf("Dateien = %v, erwartet %v", gotFiles, want)
	}
	slices.Sort(dirs)
	if want := []string{".claude-plugin", "skills"}; !slices.Equal(dirs, want) {
		t.Errorf("Verzeichnisse = %v, erwartet %v (skills/ wird immer angelegt)", dirs, want)
	}
	if runtime.GOOS != "windows" {
		if mode := statMode(t, filepath.Join(dir, "skills")); mode != 0o555 {
			t.Errorf("skills/ hat Modus %o, erwartet 555", mode)
		}
	}
	m, err := VerifySnapshotDir(dir)
	if err != nil {
		t.Fatalf("Snapshot ohne Skills nicht verifizierbar: %v", err)
	}
	if m.SnapshotID != snap.ID() || len(m.Skills) != 0 {
		t.Errorf("snapshot.json = %+v", m)
	}

	// Zweites Schreiben (auch eines neu aufgelösten Snapshots): derselbe Pfad, kein Fehler.
	again, err := snap.Write(out)
	if err != nil || again != dir {
		t.Errorf("zweites Schreiben: %s, %v (erwartet %s)", again, err, dir)
	}
	snap2, err := f.mustLoad().Resolve(domain.RoleReviewer, nil)
	if err != nil {
		t.Fatal(err)
	}
	if again, err := snap2.Write(out); err != nil || again != dir {
		t.Errorf("Wiederverwendung: %s, %v", again, err)
	}
	if entries, _ := os.ReadDir(out); len(entries) != 1 {
		t.Errorf("Zielverzeichnis enthält %d Einträge", len(entries))
	}

	// Fehlendes skills/ ist ein Befund, nicht stillschweigend erlaubt.
	t.Run("skills fehlt", func(t *testing.T) {
		_, _, d := resolveWrite(t, c, domain.RoleReviewer, nil)
		_ = os.Chmod(d, 0o755)
		if err := os.Remove(filepath.Join(d, "skills")); err != nil {
			t.Fatal(err)
		}
		_, err := VerifySnapshotDir(d)
		wantErr(t, err, CodeSnapshotMismatch, "", "skills")
	})
	// Eine fremde Datei in einem leeren skills/ ist ein Befund.
	t.Run("fremde Datei in leerem skills", func(t *testing.T) {
		_, _, d := resolveWrite(t, c, domain.RoleReviewer, nil)
		_ = os.Chmod(filepath.Join(d, "skills"), 0o755)
		if err := os.WriteFile(filepath.Join(d, "skills", "fremd.txt"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := VerifySnapshotDir(d)
		wantErr(t, err, CodeSnapshotMismatch, "", "skills/fremd.txt")
	})
}
