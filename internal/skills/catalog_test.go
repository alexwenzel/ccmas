package skills

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/alexwenzel/ccmas/internal/domain"
)

// repoRoot ist die Wurzel des Repositorys (zwei Ebenen über internal/skills).
const repoRoot = "../.."

// TestHashDefinitions hält die Hash-Definitionen fest, die Python später identisch nachrechnet.
func TestHashDefinitions(t *testing.T) {
	// SHA-256 bekannter Eingaben (unabhängig von dieser Implementierung berechnet).
	const (
		shaHello = "5891b5b522d5df086d0ff0b110fbd9d21bb4fc7163af34d08286a2e846f6be03" // "hello\n"
		shaEmpty = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" // ""
	)
	if got := sha256Hex([]byte("hello\n")); got != shaHello {
		t.Fatalf("sha256Hex(hello) = %s", got)
	}
	if got := sha256Hex(nil); got != shaEmpty {
		t.Fatalf("sha256Hex(leer) = %s", got)
	}

	// Bytewise Reihenfolge: '-' (0x2d) < '.' (0x2e) < '/' (0x2f). Ein Durchlauf, der Verzeichnisse
	// zuerst behandelt, würde "a/x.txt" vor "a.txt" einsortieren; eine Sortierung nach Tiefe
	// würde "c.txt" vor "b/y.txt" setzen.
	f := newCatalogFixture(t, []testSkill{{
		id: "ccmas-demo-hash", status: StatusApproved, category: CategoryKnowledge, roles: []domain.Role{domain.RoleConcept},
		files: map[string]string{"SKILL.md": "---\nname: ccmas-demo-hash\ndescription: x\n---\n", "a.txt": "hello\n", "a-b.txt": "", "a/x.txt": "hello\n", "b/y.txt": "", "c.txt": "hello\n"},
	}})
	// Eine .DS_Store im Skill-Verzeichnis ändert nichts.
	f.write("ccmas-demo-hash", ".DS_Store", "macOS-Metadaten")
	f.write("ccmas-demo-hash", "a/.DS_Store", "macOS-Metadaten")
	c := f.mustLoad()

	skillSum := sha256Hex([]byte("---\nname: ccmas-demo-hash\ndescription: x\n---\n"))
	// Erwartete Zeilen in bytewise Reihenfolge: "SKILL.md" (0x53) < "a-b.txt" < "a.txt" < "a/x.txt".
	want := sha256.Sum256([]byte(skillSum + "  SKILL.md\n" + shaEmpty + "  a-b.txt\n" + shaHello + "  a.txt\n" + shaHello + "  a/x.txt\n" + shaEmpty + "  b/y.txt\n" + shaHello + "  c.txt\n"))
	sk, _ := c.Skill("ccmas-demo-hash")
	if sk.TreeSHA256 != hex.EncodeToString(want[:]) {
		t.Errorf("tree_sha256 = %s, erwartet %s", sk.TreeSHA256, hex.EncodeToString(want[:]))
	}
	var paths []string
	for _, f := range sk.Files {
		paths = append(paths, f.Path)
	}
	if !slices.Equal(paths, []string{"SKILL.md", "a-b.txt", "a.txt", "a/x.txt", "b/y.txt", "c.txt"}) {
		t.Errorf("Dateien = %v", paths)
	}
}

func TestSnapshotIDDeterministic(t *testing.T) {
	f := newCatalogFixture(t, defaultTestSkills())
	c := f.mustLoad(fixedClock())

	a, err := c.Resolve(domain.RoleConcept, nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := c.Resolve(domain.RoleConcept, []string{"ccmas-demo-know"})
	if err != nil {
		t.Fatal(err)
	}
	if a.ID() != b.ID() {
		t.Errorf("Standardausstattung und ausdrückliche Auswahl derselben Skills: %s != %s", a.ID(), b.ID())
	}
	// Neu geladener Katalog, andere Uhrzeit: gleiche ID.
	c2 := f.mustLoad()
	a2, err := c2.Resolve(domain.RoleConcept, nil)
	if err != nil {
		t.Fatal(err)
	}
	if a.ID() != a2.ID() {
		t.Errorf("gleiche Auswahl, gleicher Inhalt, aber %s != %s", a.ID(), a2.ID())
	}

	// Die ID folgt dem festgelegten Format.
	tree := f.entry("ccmas-demo-know").TreeSHA256
	sum := sha256.Sum256([]byte("ccmas-skill-snapshot v1\nrole concept\nskill ccmas-demo-know 1 " + tree + "\n"))
	if want := "sha256:" + hex.EncodeToString(sum[:]); a.ID() != want {
		t.Errorf("snapshot_id = %s, erwartet %s", a.ID(), want)
	}

	// Reihenfolge der Anforderung spielt keine Rolle.
	x, err := c.Resolve(domain.RoleDocumentation, []string{"ccmas-demo-rules", "ccmas-demo-know"})
	if err != nil {
		t.Fatal(err)
	}
	y, err := c.Resolve(domain.RoleDocumentation, []string{"ccmas-demo-know", "ccmas-demo-rules"})
	if err != nil {
		t.Fatal(err)
	}
	if x.ID() != y.ID() {
		t.Errorf("Reihenfolge verändert die ID: %s != %s", x.ID(), y.ID())
	}
	if !slices.Equal(x.SkillIDs(), []string{"ccmas-demo-know", "ccmas-demo-rules"}) {
		t.Errorf("SkillIDs = %v", x.SkillIDs())
	}

	// Die Rolle geht in die ID ein.
	other, err := c.Resolve(domain.RoleDocumentation, []string{"ccmas-demo-know"})
	if err != nil {
		t.Fatal(err)
	}
	if other.ID() == a.ID() {
		t.Error("andere Rolle, aber dieselbe snapshot_id")
	}

	// Inhalt geändert und Manifest nachgezogen: neue ID. Version erhöht: neue ID.
	f.write("ccmas-demo-know", "scripts/check.py", "print('neu')\n")
	e := f.entry("ccmas-demo-know")
	for i := range e.Files {
		if e.Files[i].Path == "scripts/check.py" {
			e.Files[i].SHA256 = sha256Hex([]byte("print('neu')\n"))
		}
	}
	lines := ""
	for _, fl := range e.Files {
		lines += fl.SHA256 + "  " + fl.Path + "\n"
	}
	t1 := sha256.Sum256([]byte(lines))
	e.TreeSHA256 = hex.EncodeToString(t1[:])
	f.writeManifest()
	changed, err := f.mustLoad().Resolve(domain.RoleConcept, nil)
	if err != nil {
		t.Fatal(err)
	}
	if changed.ID() == a.ID() {
		t.Error("geänderter Inhalt ergibt dieselbe snapshot_id")
	}
	e.Version = 2
	f.writeManifest()
	bumped, err := f.mustLoad().Resolve(domain.RoleConcept, nil)
	if err != nil {
		t.Fatal(err)
	}
	if bumped.ID() == changed.ID() {
		t.Error("höhere Version ergibt dieselbe snapshot_id")
	}
}

func TestRoleSkillSelection(t *testing.T) {
	f := newCatalogFixture(t, defaultTestSkills())
	c := f.mustLoad()

	t.Run("Reviewer-Standard enthält nur Prüfskills", func(t *testing.T) {
		for _, role := range []domain.Role{domain.RoleReviewer, domain.RoleConceptReviewer} {
			s, err := c.Resolve(role, nil)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(s.SkillIDs(), []string{"ccmas-demo-review"}) {
				t.Errorf("%s: Standard = %v", role, s.SkillIDs())
			}
			for _, id := range s.SkillIDs() {
				if sk, _ := c.Skill(id); sk.Category != CategoryReview {
					t.Errorf("%s erhält Skill %s der Kategorie %s", role, id, sk.Category)
				}
			}
		}
	})
	t.Run("Delivery- und Rules-Skills sind für Prüfrollen gesperrt", func(t *testing.T) {
		for _, role := range []domain.Role{domain.RoleReviewer, domain.RoleConceptReviewer} {
			for _, id := range []string{"ccmas-demo-impl", "ccmas-demo-rules"} {
				_, err := c.Resolve(role, []string{id})
				wantErr(t, err, CodeSkillNotAllowedForRole, id, "")
				// Auch zusammen mit einem erlaubten Skill.
				_, err = c.Resolve(role, []string{"ccmas-demo-review", id})
				wantErr(t, err, CodeSkillNotAllowedForRole, id, "")
			}
		}
	})
	t.Run("ausdrückliche Auswahl ersetzt die Standardausstattung", func(t *testing.T) {
		s, err := c.Resolve(domain.RoleDocumentation, []string{"ccmas-demo-rules"})
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(s.SkillIDs(), []string{"ccmas-demo-rules"}) {
			t.Errorf("Skills = %v", s.SkillIDs())
		}
		// Standard der Rolle concept ist ccmas-demo-know; ausdrücklich nur ein anderer ist ein anderer Satz.
		d, _ := c.Resolve(domain.RoleConcept, nil)
		if !slices.Equal(d.SkillIDs(), []string{"ccmas-demo-know"}) {
			t.Errorf("Standard concept = %v", d.SkillIDs())
		}
	})
	t.Run("Rolle ohne Standardausstattung ergibt leeren Snapshot", func(t *testing.T) {
		s, err := c.Resolve(domain.RoleSupervisor, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(s.SkillIDs()) != 0 || !strings.HasPrefix(s.ID(), "sha256:") {
			t.Errorf("Snapshot = %v / %s", s.SkillIDs(), s.ID())
		}
		if _, err := domain.ParseSkillSnapshot(mustJSON(t, s.Document())); err != nil {
			t.Errorf("leere snapshot.json verletzt den Vertrag: %v", err)
		}
	})
	t.Run("Skill ohne Erlaubnis der Rolle", func(t *testing.T) {
		_, err := c.Resolve(domain.RoleImplementer, []string{"ccmas-demo-know"})
		wantErr(t, err, CodeSkillNotAllowedForRole, "ccmas-demo-know", "")
	})
	t.Run("unbekannte Rolle und doppelte ID", func(t *testing.T) {
		_, err := c.Resolve(domain.Role("admin"), nil)
		wantErr(t, err, CodeInvalidRequest, "", "")
		_, err = c.Resolve(domain.RoleConcept, []string{"ccmas-demo-know", "ccmas-demo-know"})
		wantErr(t, err, CodeInvalidRequest, "ccmas-demo-know", "")
	})
	t.Run("ForRole nennt alle erlaubten Skills", func(t *testing.T) {
		var ids []string
		for _, s := range c.ForRole(domain.RoleDocumentation) {
			ids = append(ids, s.ID)
		}
		if !slices.Equal(ids, []string{"ccmas-demo-know", "ccmas-demo-rules"}) {
			t.Errorf("ForRole(documentation) = %v", ids)
		}
	})
}

func TestUnapprovedSkillRejected(t *testing.T) {
	f := newCatalogFixture(t, defaultTestSkills())
	c := f.mustLoad()
	_, err := c.Resolve(domain.RoleSupervisor, []string{"ccmas-demo-plan"})
	wantErr(t, err, CodeSkillNotApproved, "ccmas-demo-plan", "")
	if !strings.Contains(err.Error(), "CC-14") {
		t.Errorf("Meldung nennt die ausstehende Anpassung nicht: %v", err)
	}
	// Ein nicht freigegebener Skill gelangt auch nicht über die Standardausstattung hinein:
	// default_for ist bei pending schon im Manifest unzulässig.
	f.entry("ccmas-demo-plan").DefaultFor = []domain.Role{domain.RoleSupervisor}
	f.writeManifest()
	_, err = f.load()
	wantErr(t, err, CodeInvalidManifest, "ccmas-demo-plan", "")
}

func TestMissingSkill(t *testing.T) {
	skill := "ccmas-demo-know"
	t.Run("fehlende SKILL.md", func(t *testing.T) {
		f := newCatalogFixture(t, defaultTestSkills())
		if err := os.Remove(filepath.Join(f.skillDir(skill), "SKILL.md")); err != nil {
			t.Fatal(err)
		}
		_, err := f.load()
		wantErr(t, err, CodeMissingResource, skill, "SKILL.md")
	})
	t.Run("fehlendes Skill-Verzeichnis", func(t *testing.T) {
		f := newCatalogFixture(t, defaultTestSkills())
		if err := os.RemoveAll(f.skillDir(skill)); err != nil {
			t.Fatal(err)
		}
		_, err := f.load()
		wantErr(t, err, CodeMissingResource, skill, ".")
		if n := len(FindingsOf(err)); n != 1 {
			t.Errorf("%d Befunde statt einem für das fehlende Verzeichnis: %v", n, err)
		}
	})
	t.Run("fehlende gelistete Ressource", func(t *testing.T) {
		f := newCatalogFixture(t, defaultTestSkills())
		if err := os.Remove(filepath.Join(f.skillDir(skill), "scripts", "check.py")); err != nil {
			t.Fatal(err)
		}
		_, err := f.load()
		wantErr(t, err, CodeMissingResource, skill, "scripts/check.py")
	})
	t.Run("veränderte Ressource", func(t *testing.T) {
		f := newCatalogFixture(t, defaultTestSkills())
		f.write(skill, "scripts/check.py", "print('manipuliert')\n")
		_, err := f.load()
		wantErr(t, err, CodeChangedResource, skill, "scripts/check.py")
		var wantSum string
		for _, fl := range f.entry(skill).Files {
			if fl.Path == "scripts/check.py" {
				wantSum = fl.SHA256
			}
		}
		if !strings.Contains(err.Error(), wantSum) {
			t.Errorf("Meldung nennt den erwarteten Hash nicht: %v", err)
		}
	})
	t.Run("nicht gelistete Datei", func(t *testing.T) {
		f := newCatalogFixture(t, defaultTestSkills())
		f.write(skill, "notes/geheim.txt", "x")
		_, err := f.load()
		wantErr(t, err, CodeUnlistedResource, skill, "notes/geheim.txt")
	})
	t.Run("unbekannte Skill-ID", func(t *testing.T) {
		f := newCatalogFixture(t, defaultTestSkills())
		_, err := f.mustLoad().Resolve(domain.RoleConcept, []string{"ccmas-gibtsnicht"})
		wantErr(t, err, CodeUnknownSkill, "ccmas-gibtsnicht", "")
	})
	t.Run("unregistriertes Verzeichnis", func(t *testing.T) {
		f := newCatalogFixture(t, defaultTestSkills())
		f.write("ccmas-schatten", "SKILL.md", "---\nname: ccmas-schatten\ndescription: x\n---\n")
		_, err := f.load()
		wantErr(t, err, CodeUnregisteredSkill, "ccmas-schatten", ".claude/skills/ccmas-schatten")
	})
	t.Run("Verzeichnisse ohne ccmas-Präfix werden ignoriert", func(t *testing.T) {
		f := newCatalogFixture(t, defaultTestSkills())
		f.write("fremd", "SKILL.md", "x")
		f.mustLoad()
	})
	t.Run("alles gleichzeitig wird gesammelt", func(t *testing.T) {
		f := newCatalogFixture(t, defaultTestSkills())
		f.write(skill, "scripts/check.py", "anders")
		f.write(skill, "extra.txt", "x")
		f.write("ccmas-schatten", "SKILL.md", "x")
		_, err := f.load()
		wantErr(t, err, CodeChangedResource, skill, "scripts/check.py")
		wantErr(t, err, CodeUnlistedResource, skill, "extra.txt")
		wantErr(t, err, CodeUnregisteredSkill, "ccmas-schatten", "")
	})

	// Änderungen nach Load erkennt Resolve (liest die Dateien neu).
	t.Run("Resolve erkennt Änderungen nach Load", func(t *testing.T) {
		cases := map[string]struct {
			mutate func(f *catalogFixture)
			code   Code
			path   string
		}{
			"verändert": {func(f *catalogFixture) { f.write(skill, "scripts/check.py", "anders") }, CodeChangedResource, "scripts/check.py"},
			"gelöscht": {func(f *catalogFixture) {
				_ = os.Remove(filepath.Join(f.skillDir(skill), "scripts", "check.py"))
			}, CodeMissingResource, "scripts/check.py"},
			"SKILL.md gelöscht": {func(f *catalogFixture) {
				_ = os.Remove(filepath.Join(f.skillDir(skill), "SKILL.md"))
			}, CodeMissingResource, "SKILL.md"},
			"hinzugefügt": {func(f *catalogFixture) { f.write(skill, "neu.txt", "x") }, CodeUnlistedResource, "neu.txt"},
			"Skill-Verzeichnis gelöscht": {func(f *catalogFixture) {
				_ = os.RemoveAll(f.skillDir(skill))
			}, CodeMissingResource, "."},
		}
		for name, tc := range cases {
			t.Run(name, func(t *testing.T) {
				f := newCatalogFixture(t, defaultTestSkills())
				c := f.mustLoad()
				tc.mutate(f)
				_, err := c.Resolve(domain.RoleConcept, nil)
				wantErr(t, err, tc.code, skill, tc.path)
				// Ein anderer, unveränderter Skill bleibt auflösbar.
				if _, err := c.Resolve(domain.RoleReviewer, nil); err != nil {
					t.Errorf("unveränderter Skill: %v", err)
				}
			})
		}
	})
}

func TestUnsafeResources(t *testing.T) {
	skill := "ccmas-demo-know"
	t.Run("Symlink auf Datei außerhalb", func(t *testing.T) {
		f := newCatalogFixture(t, defaultTestSkills())
		outside := filepath.Join(t.TempDir(), "secret.txt")
		if err := os.WriteFile(outside, []byte("geheim"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(f.skillDir(skill), "link.txt")); err != nil {
			t.Skipf("Symlinks nicht möglich: %v", err)
		}
		_, err := f.load()
		wantErr(t, err, CodeUnsafeResource, skill, "link.txt")
	})
	t.Run("Symlink auf Verzeichnis außerhalb", func(t *testing.T) {
		f := newCatalogFixture(t, defaultTestSkills())
		if err := os.Symlink(t.TempDir(), filepath.Join(f.skillDir(skill), "ausbruch")); err != nil {
			t.Skipf("Symlinks nicht möglich: %v", err)
		}
		_, err := f.load()
		wantErr(t, err, CodeUnsafeResource, skill, "ausbruch")
	})
	t.Run("Symlink auf gelistete Datei innerhalb", func(t *testing.T) {
		f := newCatalogFixture(t, defaultTestSkills())
		p := filepath.Join(f.skillDir(skill), "scripts", "check.py")
		if err := os.Remove(p); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(f.skillDir(skill), "SKILL.md"), p); err != nil {
			t.Skipf("Symlinks nicht möglich: %v", err)
		}
		_, err := f.load()
		wantErr(t, err, CodeUnsafeResource, skill, "scripts/check.py")
	})
	t.Run("Skill-Verzeichnis ist ein Symlink", func(t *testing.T) {
		f := newCatalogFixture(t, defaultTestSkills())
		real := f.skillDir(skill)
		moved := filepath.Join(t.TempDir(), "moved")
		if err := os.Rename(real, moved); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(moved, real); err != nil {
			t.Skipf("Symlinks nicht möglich: %v", err)
		}
		_, err := f.load()
		wantErr(t, err, CodeUnsafeResource, skill, ".")
	})
	t.Run("Dateiname mit Zeilenumbruch", func(t *testing.T) {
		f := newCatalogFixture(t, defaultTestSkills())
		if err := os.WriteFile(filepath.Join(f.skillDir(skill), "a\nb.txt"), []byte("x"), 0o644); err != nil {
			t.Skipf("Dateiname nicht möglich: %v", err)
		}
		_, err := f.load()
		wantErr(t, err, CodeUnsafeResource, skill, "")
	})
	t.Run("Manifest-Pfad mit Ausbruch", func(t *testing.T) {
		for _, p := range []string{"../SKILL.md", "/etc/passwd", "a/../b", "a//b", "./a"} {
			f := newCatalogFixture(t, defaultTestSkills())
			f.entry(skill).Files = append(f.entry(skill).Files, manifestFile{Path: p, SHA256: strings.Repeat("0", 64)})
			f.writeManifest()
			_, err := f.load()
			wantErr(t, err, CodeInvalidManifest, skill, "")
		}
	})
	t.Run(".DS_Store wird ignoriert", func(t *testing.T) {
		f := newCatalogFixture(t, defaultTestSkills())
		f.write(skill, ".DS_Store", "x")
		f.write(skill, "notes/.DS_Store", "x")
		f.mustLoad()
	})
	t.Run("andere versteckte Dateien zählen", func(t *testing.T) {
		f := newCatalogFixture(t, defaultTestSkills())
		f.write(skill, ".gitkeep", "")
		_, err := f.load()
		wantErr(t, err, CodeUnlistedResource, skill, ".gitkeep")
	})
}

func TestManifestInvariants(t *testing.T) {
	type mut = func(f *catalogFixture)
	cases := map[string]struct {
		mutate mut
		code   Code
		skill  string
	}{
		"rules für reviewer": {func(f *catalogFixture) {
			f.entry("ccmas-demo-rules").Roles = []domain.Role{domain.RoleDocumentation, domain.RoleReviewer}
		}, CodeInvalidManifest, "ccmas-demo-rules"},
		"rules für concept_reviewer": {func(f *catalogFixture) { f.entry("ccmas-demo-rules").Roles = []domain.Role{domain.RoleConceptReviewer} }, CodeInvalidManifest, "ccmas-demo-rules"},
		"delivery für reviewer": {func(f *catalogFixture) {
			f.entry("ccmas-demo-impl").Roles = []domain.Role{domain.RoleImplementer, domain.RoleReviewer}
		}, CodeInvalidManifest, "ccmas-demo-impl"},
		"delivery für concept_reviewer": {func(f *catalogFixture) {
			e := f.entry("ccmas-demo-impl")
			e.Roles = append(e.Roles, domain.RoleConceptReviewer)
			e.DefaultFor = []domain.Role{domain.RoleConceptReviewer}
		}, CodeInvalidManifest, "ccmas-demo-impl"},
		"default_for bei pending":    {func(f *catalogFixture) { f.entry("ccmas-demo-plan").DefaultFor = []domain.Role{domain.RoleSupervisor} }, CodeInvalidManifest, "ccmas-demo-plan"},
		"default_for nicht in roles": {func(f *catalogFixture) { f.entry("ccmas-demo-know").DefaultFor = []domain.Role{domain.RoleReviewer} }, CodeInvalidManifest, "ccmas-demo-know"},
		"pending ohne adaptation":    {func(f *catalogFixture) { f.entry("ccmas-demo-plan").Adaptation = "" }, CodeInvalidManifest, "ccmas-demo-plan"},
		"approved mit adaptation":    {func(f *catalogFixture) { f.entry("ccmas-demo-know").Adaptation = "alt" }, CodeInvalidManifest, "ccmas-demo-know"},
		"unbekannter Status":         {func(f *catalogFixture) { f.entry("ccmas-demo-know").Status = "draft" }, CodeInvalidManifest, "ccmas-demo-know"},
		"unbekannte Kategorie":       {func(f *catalogFixture) { f.entry("ccmas-demo-know").Category = "misc" }, CodeInvalidManifest, "ccmas-demo-know"},
		"unbekannte Rolle":           {func(f *catalogFixture) { f.entry("ccmas-demo-know").Roles = []domain.Role{"admin"} }, CodeInvalidManifest, "ccmas-demo-know"},
		"leere Rollen": {func(f *catalogFixture) {
			f.entry("ccmas-demo-know").Roles = nil
			f.entry("ccmas-demo-know").DefaultFor = nil
		}, CodeInvalidManifest, "ccmas-demo-know"},
		"Rolle doppelt": {func(f *catalogFixture) {
			f.entry("ccmas-demo-know").Roles = []domain.Role{domain.RoleConcept, domain.RoleConcept}
		}, CodeInvalidManifest, "ccmas-demo-know"},
		"unbekanntes external_write": {func(f *catalogFixture) { f.entry("ccmas-demo-know").ExternalWrites = []string{"slack"} }, CodeInvalidManifest, "ccmas-demo-know"},
		"Version 0":                  {func(f *catalogFixture) { f.entry("ccmas-demo-know").Version = 0 }, CodeInvalidManifest, "ccmas-demo-know"},
		"ID ohne Präfix":             {func(f *catalogFixture) { f.entry("ccmas-demo-know").ID = "demo-know" }, CodeInvalidManifest, "demo-know"},
		"Hash kein Hex":              {func(f *catalogFixture) { f.entry("ccmas-demo-know").Files[0].SHA256 = "XYZ" }, CodeInvalidManifest, "ccmas-demo-know"},
		"tree_sha256 falsch":         {func(f *catalogFixture) { f.entry("ccmas-demo-know").TreeSHA256 = strings.Repeat("0", 64) }, CodeInvalidManifest, "ccmas-demo-know"},
		"SKILL.md nicht gelistet": {func(f *catalogFixture) {
			e := f.entry("ccmas-demo-know")
			e.Files = e.Files[1:]
		}, CodeInvalidManifest, "ccmas-demo-know"},
		"Datei doppelt gelistet": {func(f *catalogFixture) {
			e := f.entry("ccmas-demo-know")
			e.Files = append(e.Files, e.Files[0])
		}, CodeInvalidManifest, "ccmas-demo-know"},
		"Datum ungültig":      {func(f *catalogFixture) { f.entry("ccmas-demo-know").Source.Derived = "09.11.2026" }, CodeInvalidManifest, "ccmas-demo-know"},
		"Paket absolut":       {func(f *catalogFixture) { f.entry("ccmas-demo-know").Source.Package = "/Users/x/a.skill" }, CodeInvalidManifest, "ccmas-demo-know"},
		"Paket-Hash ungültig": {func(f *catalogFixture) { f.entry("ccmas-demo-know").Source.PackageSHA256 = "abc" }, CodeInvalidManifest, "ccmas-demo-know"},
		"changes leer":        {func(f *catalogFixture) { f.entry("ccmas-demo-know").Source.Changes = "" }, CodeInvalidManifest, "ccmas-demo-know"},
		"ID doppelt": {func(f *catalogFixture) {
			f.doc.Skills = append(f.doc.Skills, f.doc.Skills[0])
		}, CodeInvalidManifest, "ccmas-demo-review"},
		"schema_version 2": {func(f *catalogFixture) { f.doc.SchemaVersion = 2 }, CodeInvalidManifest, ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := newCatalogFixture(t, defaultTestSkills())
			tc.mutate(f)
			f.writeManifest()
			c, err := f.load()
			if c != nil {
				t.Error("Katalog trotz Befund geliefert")
			}
			wantErr(t, err, tc.code, tc.skill, "")
		})
	}

	t.Run("Vorlage ist gültig", func(t *testing.T) {
		newCatalogFixture(t, defaultTestSkills()).mustLoad()
	})
}

func TestManifestSyntax(t *testing.T) {
	f := newCatalogFixture(t, defaultTestSkills())
	valid, err := os.ReadFile(filepath.Join(f.root, ".claude", "skills", "manifest.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]struct {
		raw  string
		code Code
	}{
		"unbekannter Schlüssel oben":      {"unknown_key: 1\n" + string(valid), CodeInvalidManifest},
		"unbekannter Schlüssel im Skill":  {strings.Replace(string(valid), "    version: 1\n", "    version: 1\n    owner: alex\n", 1), CodeInvalidManifest},
		"unbekannter Schlüssel in source": {strings.Replace(string(valid), "      changes: \"Test\"\n", "      changes: \"Test\"\n      extra: 1\n", 1), CodeInvalidManifest},
		"doppelter Schlüssel":             {strings.Replace(string(valid), "schema_version: 1\n", "schema_version: 1\nschema_version: 1\n", 1), CodeInvalidManifest},
		"zwei Dokumente":                  {string(valid) + "---\nschema_version: 1\nskills: []\n", CodeInvalidManifest},
		"kein YAML":                       {"skills: [\n", CodeInvalidManifest},
		"leere Datei":                     {"", CodeInvalidManifest},
		"Version als Text":                {strings.Replace(string(valid), "\n    version: 1\n", "\n    version: eins\n", 1), CodeInvalidManifest},
		"Skills als Zahl":                 {"schema_version: 1\nskills: 3\n", CodeInvalidManifest},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f.writeManifestRaw(tc.raw)
			_, err := f.load()
			wantErr(t, err, tc.code, "", ".claude/skills/manifest.yaml")
		})
	}

	t.Run("Manifest fehlt", func(t *testing.T) {
		g := newCatalogFixture(t, defaultTestSkills())
		if err := os.Remove(filepath.Join(g.root, ".claude", "skills", "manifest.yaml")); err != nil {
			t.Fatal(err)
		}
		_, err := g.load()
		wantErr(t, err, CodeMissingManifest, "", ".claude/skills/manifest.yaml")
	})
	t.Run("Wurzel ohne Skills", func(t *testing.T) {
		_, err := Load(t.TempDir())
		wantErr(t, err, CodeMissingManifest, "", "")
		_, err = Load("")
		wantErr(t, err, CodeMissingManifest, "", "")
	})
	t.Run("leere Skill-Liste ist gültig", func(t *testing.T) {
		g := newCatalogFixture(t, nil)
		c := g.mustLoad()
		if len(c.Skills()) != 0 {
			t.Error("Skills vorhanden")
		}
		s, err := c.Resolve(domain.RoleConcept, nil)
		if err != nil || len(s.SkillIDs()) != 0 {
			t.Errorf("Resolve = %v, %v", s.SkillIDs(), err)
		}
	})
}

func TestFrontmatter(t *testing.T) {
	skill := "ccmas-demo-know"
	cases := map[string]string{
		"name weicht von der ID ab":     "---\nname: ccmas-anderer\ndescription: x\n---\nText\n",
		"Name ohne Präfix":              "---\nname: demo-know\ndescription: x\n---\nText\n",
		"description fehlt":             "---\nname: ccmas-demo-know\n---\nText\n",
		"description leer":              "---\nname: ccmas-demo-know\ndescription: \"  \"\n---\nText\n",
		"kein Frontmatter":              "# Titel\n",
		"Frontmatter nicht geschlossen": "---\nname: ccmas-demo-know\ndescription: x\n",
		"kein gültiges YAML":            "---\nname: ccmas-demo-know\ndescription: a: b: [\n---\n",
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			f := newCatalogFixture(t, []testSkill{{
				id: skill, status: StatusApproved, category: CategoryKnowledge, roles: []domain.Role{domain.RoleConcept},
				files: map[string]string{"SKILL.md": content},
			}})
			_, err := f.load()
			wantErr(t, err, CodeInvalidFrontmatter, skill, "SKILL.md")
		})
	}
	t.Run("weitere Schlüssel und CRLF sind erlaubt", func(t *testing.T) {
		f := newCatalogFixture(t, []testSkill{{
			id: skill, status: StatusApproved, category: CategoryKnowledge, roles: []domain.Role{domain.RoleConcept},
			files: map[string]string{"SKILL.md": "---\r\nname: ccmas-demo-know\r\ndescription: Beschreibung\r\nallowed-tools: Read\r\n---\r\nText\r\n"},
		}})
		c := f.mustLoad()
		if sk, _ := c.Skill(skill); sk.Description != "Beschreibung" {
			t.Errorf("Beschreibung = %q", sk.Description)
		}
	})
}

func TestSnapshotImmutable(t *testing.T) {
	skill := "ccmas-demo-know"
	f := newCatalogFixture(t, defaultTestSkills())
	c := f.mustLoad(fixedClock())
	snap, err := c.Resolve(domain.RoleConcept, nil)
	if err != nil {
		t.Fatal(err)
	}
	out := t.TempDir()
	makeWritable(t, out)
	dir, err := snap.Write(out)
	if err != nil {
		t.Fatal(err)
	}
	beforeFiles, beforeDirs := treeFiles(t, dir)
	beforeDoc := mustJSON(t, snap.Document())
	beforeContent, _ := snap.File(skill, "scripts/check.py")
	if string(beforeContent) != "print('ok')\n" {
		t.Fatalf("Inhalt im Snapshot = %q", beforeContent)
	}

	assertUnchanged := func(t *testing.T) {
		t.Helper()
		if got := mustJSON(t, snap.Document()); string(got) != string(beforeDoc) {
			t.Errorf("Snapshot-Wert hat sich verändert:\n%s\nvorher\n%s", got, beforeDoc)
		}
		if got, _ := snap.File(skill, "scripts/check.py"); string(got) != string(beforeContent) {
			t.Errorf("Dateiinhalt im Snapshot verändert: %q", got)
		}
		files, dirs := treeFiles(t, dir)
		if !equalMaps(files, beforeFiles) || !slices.Equal(dirs, beforeDirs) {
			t.Errorf("Snapshot-Verzeichnis hat sich verändert")
		}
		if _, err := VerifySnapshotDir(dir); err != nil {
			t.Errorf("Snapshot-Verzeichnis nicht mehr verifizierbar: %v", err)
		}
	}

	t.Run("Quelle ändert sich, Katalog bleibt", func(t *testing.T) {
		f.write(skill, "scripts/check.py", "print('manipuliert')\n")
		assertUnchanged(t)
		// Der alte Katalog erkennt die Änderung beim nächsten Resolve.
		_, err := c.Resolve(domain.RoleConcept, nil)
		wantErr(t, err, CodeChangedResource, skill, "scripts/check.py")
		assertUnchanged(t)
	})

	t.Run("Manifest nachgezogen: neuer Resolve liefert andere ID", func(t *testing.T) {
		e := f.entry(skill)
		var lines []string
		for i := range e.Files {
			if e.Files[i].Path == "scripts/check.py" {
				e.Files[i].SHA256 = sha256Hex([]byte("print('manipuliert')\n"))
			}
			lines = append(lines, e.Files[i].SHA256+"  "+e.Files[i].Path+"\n")
		}
		sum := sha256.Sum256([]byte(strings.Join(lines, "")))
		e.TreeSHA256 = hex.EncodeToString(sum[:])
		f.writeManifest()
		next, err := f.mustLoad(fixedClock()).Resolve(domain.RoleConcept, nil)
		if err != nil {
			t.Fatal(err)
		}
		if next.ID() == snap.ID() {
			t.Error("neuer Stand, aber dieselbe snapshot_id")
		}
		nextDir, err := next.Write(out)
		if err != nil {
			t.Fatal(err)
		}
		if nextDir == dir {
			t.Error("neuer Snapshot im selben Verzeichnis")
		}
		assertUnchanged(t)
		if got, _ := next.File(skill, "scripts/check.py"); string(got) != "print('manipuliert')\n" {
			t.Errorf("neuer Snapshot enthält %q", got)
		}
	})

	t.Run("Quelle gelöscht", func(t *testing.T) {
		_ = os.RemoveAll(f.skillDir(skill))
		assertUnchanged(t)
	})
}

func equalMaps(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			return false
		}
	}
	return true
}

func TestSnapshotGettersReturnCopies(t *testing.T) {
	f := newCatalogFixture(t, defaultTestSkills())
	c := f.mustLoad(fixedClock())
	snap, err := c.Resolve(domain.RoleConcept, nil)
	if err != nil {
		t.Fatal(err)
	}
	skill := "ccmas-demo-know"
	before := mustJSON(t, snap.Document())

	ids := snap.SkillIDs()
	ids[0] = "manipuliert"
	data, _ := snap.File(skill, "SKILL.md")
	data[0] = 'X'
	doc := snap.Document()
	doc.Skills[0].ID = "manipuliert"
	doc.Skills[0].Files[0].SHA256 = "manipuliert"
	doc.Skills = append(doc.Skills, doc.Skills[0])
	sk, _ := c.Skill(skill)
	sk.Files[0].SHA256 = "manipuliert"
	sk.Roles[0] = domain.RoleSupervisor
	all := c.Skills()
	all[0].ID = "manipuliert"
	all[0].DefaultFor = append(all[0].DefaultFor, domain.RoleSupervisor)
	_ = c.ForRole(domain.RoleConcept)[0].Files[0]

	if got := mustJSON(t, snap.Document()); string(got) != string(before) {
		t.Errorf("Snapshot durch Getter-Mutation verändert")
	}
	if again, _ := snap.File(skill, "SKILL.md"); again[0] == 'X' {
		t.Error("Dateiinhalt durch Mutation der Kopie verändert")
	}
	if !slices.Equal(snap.SkillIDs(), []string{skill}) {
		t.Errorf("SkillIDs = %v", snap.SkillIDs())
	}
	if again, _ := c.Skill(skill); again.Files[0].SHA256 == "manipuliert" || again.Roles[0] != domain.RoleConcept {
		t.Error("Katalog durch Mutation der Kopie verändert")
	}
	if c.Skills()[0].ID == "manipuliert" {
		t.Error("Katalogliste durch Mutation verändert")
	}
	// Auch eine Kopie des Snapshot-Werts teilt nichts Veränderbares.
	copyOf := snap
	if d, _ := copyOf.File(skill, "SKILL.md"); d != nil {
		d[1] = 'Y'
	}
	if d, _ := snap.File(skill, "SKILL.md"); d[1] == 'Y' {
		t.Error("Wertkopie teilt Dateiinhalt")
	}
}

func TestSnapshotNotAffectedByLaterEdits(t *testing.T) {
	// Der Snapshot hält den Stand zum Zeitpunkt von Resolve, nicht die Datei auf der Platte.
	f := newCatalogFixture(t, defaultTestSkills())
	c := f.mustLoad()
	snap, err := c.Resolve(domain.RoleConcept, nil)
	if err != nil {
		t.Fatal(err)
	}
	orig, _ := snap.File("ccmas-demo-know", "SKILL.md")
	f.write("ccmas-demo-know", "SKILL.md", "---\nname: ccmas-demo-know\ndescription: neu\n---\n")
	now, _ := snap.File("ccmas-demo-know", "SKILL.md")
	if string(now) != string(orig) {
		t.Error("Snapshot folgt der Quelldatei")
	}
}

func TestCatalogIgnoresTargetProjectSkills(t *testing.T) {
	software := newCatalogFixture(t, defaultTestSkills())

	// Zielprojekt im Arbeitsverzeichnis mit eigenem .claude/skills, auch mit gültigem Manifest.
	project := newCatalogFixture(t, []testSkill{{
		id: "ccmas-zielprojekt", status: StatusApproved, category: CategoryKnowledge,
		roles: []domain.Role{domain.RoleConcept}, defaultFor: []domain.Role{domain.RoleConcept},
	}})
	if _, err := project.load(); err != nil {
		t.Fatal(err)
	}
	t.Chdir(project.root)

	c := software.mustLoad()
	if _, ok := c.Skill("ccmas-zielprojekt"); ok {
		t.Fatal("Skill des Zielprojekts im Katalog")
	}
	for _, s := range c.Skills() {
		if s.ID == "ccmas-zielprojekt" {
			t.Fatal("Skill des Zielprojekts in der Liste")
		}
	}
	snap, err := c.Resolve(domain.RoleConcept, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(snap.SkillIDs(), []string{"ccmas-demo-know"}) {
		t.Errorf("Standard concept = %v", snap.SkillIDs())
	}
	_, err = c.Resolve(domain.RoleConcept, []string{"ccmas-zielprojekt"})
	wantErr(t, err, CodeUnknownSkill, "ccmas-zielprojekt", "")

	// Ein Relativpfad wird nicht als „Zielprojekt“ gedeutet: leere Wurzel liest nichts.
	_, err = Load("")
	wantErr(t, err, CodeMissingManifest, "", "")
	// Ein Softwareordner ohne Manifest bleibt ein Fehler, auch wenn das Arbeitsverzeichnis eines hat.
	_, err = Load(t.TempDir())
	wantErr(t, err, CodeMissingManifest, "", "")
}

func TestRepoManifestMatchesFiles(t *testing.T) {
	c, err := Load(repoRoot)
	if err != nil {
		for _, e := range FindingsOf(err) {
			t.Errorf("%s: %s", e.Code, e.Message)
		}
		t.Fatal("Manifest und Dateien des Repositorys passen nicht zusammen (ccmas skills validate)")
	}

	type want struct {
		status     Status
		category   Category
		roles      []domain.Role
		defaultFor []domain.Role
		writes     []string
	}
	R := func(rs ...domain.Role) []domain.Role { return rs }
	inventory := map[string]want{
		"ccmas-concept-ticket":        {StatusApproved, CategoryConcept, R("concept"), R("concept"), []string{"asana", "vault"}},
		"ccmas-obsidian-seite":        {StatusApproved, CategoryKnowledge, R("concept", "documentation"), R("concept", "documentation"), []string{"vault"}},
		"ccmas-asana-ticket":          {StatusApproved, CategoryTracking, R("supervisor", "concept", "completion", "question_handler"), R("concept"), []string{"asana"}},
		"ccmas-regeln-pflegen":        {StatusApproved, CategoryRules, R("documentation"), nil, []string{"vault"}},
		"ccmas-projekt-anlegen":       {StatusPendingAdaptation, CategorySetup, R("project_setup"), nil, []string{"asana", "vault"}},
		"ccmas-neue-aufgabe-nehmen":   {StatusPendingAdaptation, CategoryPlanning, R("supervisor"), nil, []string{"asana", "vault"}},
		"ccmas-morgen-briefing":       {StatusPendingAdaptation, CategoryPlanning, R("supervisor"), nil, nil},
		"ccmas-feature-parallel":      {StatusPendingAdaptation, CategoryDelivery, R("implementer"), nil, []string{"git", "github"}},
		"ccmas-meilenstein-abschluss": {StatusPendingAdaptation, CategoryCompletion, R("completion"), nil, []string{"asana", "vault"}},
		"ccmas-antworten-abholen":     {StatusPendingAdaptation, CategoryQuestions, R("question_handler"), nil, []string{"asana", "vault"}},
	}
	got := c.Skills()
	if len(got) != len(inventory) {
		t.Errorf("%d Skills im Manifest, erwartet %d", len(got), len(inventory))
	}
	for _, s := range got {
		w, ok := inventory[s.ID]
		if !ok {
			t.Errorf("unerwarteter Skill %s", s.ID)
			continue
		}
		if s.Version != 1 || s.Status != w.status || s.Category != w.category ||
			!slices.Equal(s.Roles, w.roles) || !slices.Equal(nonNil(s.DefaultFor), nonNil(w.defaultFor)) ||
			!slices.Equal(nonNil(s.ExternalWrites), nonNil(w.writes)) {
			t.Errorf("%s: Manifest weicht vom beschlossenen Inventar ab: %+v", s.ID, s)
		}
		if s.Description == "" {
			t.Errorf("%s: keine Beschreibung", s.ID)
		}
	}

	// Jedes ccmas-Verzeichnis ist registriert und umgekehrt.
	entries, err := os.ReadDir(filepath.Join(repoRoot, ".claude", "skills"))
	if err != nil {
		t.Fatal(err)
	}
	var dirs []string
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), "ccmas-") {
			dirs = append(dirs, e.Name())
		}
	}
	var ids []string
	for _, s := range got {
		ids = append(ids, s.ID)
	}
	if !slices.Equal(dirs, ids) {
		t.Errorf("Verzeichnisse %v != Manifest %v", dirs, ids)
	}

	// Der Platzhalter skills/ im Repository ist entfallen (Entscheidung 10.11.2026).
	if _, err := os.Stat(filepath.Join(repoRoot, "skills")); !os.IsNotExist(err) {
		t.Errorf("Platzhalterverzeichnis skills/ existiert noch: %v", err)
	}
}

func TestRepoRoleSelection(t *testing.T) {
	c, err := Load(repoRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range domain.Roles() {
		t.Run("Standard "+string(role), func(t *testing.T) {
			s, err := c.Resolve(role, nil)
			if err != nil {
				t.Fatal(err)
			}
			for _, id := range s.SkillIDs() {
				sk, _ := c.Skill(id)
				if sk.Status != StatusApproved {
					t.Errorf("Standardausstattung von %s enthält %s mit Status %s", role, id, sk.Status)
				}
				if (role == domain.RoleReviewer || role == domain.RoleConceptReviewer) &&
					(sk.Category == CategoryRules || sk.Category == CategoryDelivery) {
					t.Errorf("%s erhält %s der Kategorie %s", role, id, sk.Category)
				}
			}
		})
	}
	for _, sk := range c.Skills() {
		for _, role := range []domain.Role{domain.RoleReviewer, domain.RoleConceptReviewer} {
			if sk.Category == CategoryRules || sk.Category == CategoryDelivery {
				_, err := c.Resolve(role, []string{sk.ID})
				if !errors.Is(err, &Error{Code: CodeSkillNotAllowedForRole}) && !errors.Is(err, &Error{Code: CodeSkillNotApproved}) {
					t.Errorf("%s darf %s (%s) anfordern: %v", role, sk.ID, sk.Category, err)
				}
			}
		}
		if sk.Status == StatusPendingAdaptation {
			_, err := c.Resolve(sk.Roles[0], []string{sk.ID})
			wantErr(t, err, CodeSkillNotApproved, sk.ID, "")
		}
	}
	// Die Standardausstattung für Konzept entspricht der Entscheidung.
	s, err := c.Resolve(domain.RoleConcept, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(s.SkillIDs(), []string{"ccmas-asana-ticket", "ccmas-concept-ticket", "ccmas-obsidian-seite"}) {
		t.Errorf("Standard concept = %v", s.SkillIDs())
	}
	// regeln-pflegen nur auf ausdrücklichen Auftrag.
	d, _ := c.Resolve(domain.RoleDocumentation, nil)
	if slices.Contains(d.SkillIDs(), "ccmas-regeln-pflegen") {
		t.Error("ccmas-regeln-pflegen gehört nicht zur Standardausstattung")
	}
	if _, err := c.Resolve(domain.RoleDocumentation, []string{"ccmas-regeln-pflegen"}); err != nil {
		t.Errorf("ausdrücklich angeforderter Regeln-Skill: %v", err)
	}
}

func TestErrorMatching(t *testing.T) {
	e := newErr(CodeChangedResource, "ccmas-x", "a/b.txt", "m")
	if !errors.Is(e, &Error{Code: CodeChangedResource}) {
		t.Error("Code allein trifft nicht")
	}
	if !errors.Is(e, &Error{Code: CodeChangedResource, Skill: "ccmas-x", Path: "a/b.txt"}) {
		t.Error("Code, Skill und Pfad treffen nicht")
	}
	if errors.Is(e, &Error{Code: CodeChangedResource, Skill: "ccmas-y"}) {
		t.Error("anderer Skill trifft")
	}
	if errors.Is(e, &Error{Code: CodeMissingResource}) {
		t.Error("anderer Code trifft")
	}
	multi := Findings{e, newErr(CodeUnknownSkill, "ccmas-q", "", "q")}
	if !errors.Is(multi, &Error{Code: CodeUnknownSkill}) || !errors.Is(multi, &Error{Code: CodeChangedResource}) {
		t.Error("Sammlung: einzelne Befunde nicht auffindbar")
	}
	if CodeOf(multi) != CodeChangedResource {
		t.Errorf("CodeOf = %s", CodeOf(multi))
	}
	if len(FindingsOf(multi)) != 2 || len(FindingsOf(e)) != 1 || FindingsOf(errors.New("x")) != nil {
		t.Error("FindingsOf liefert falsche Anzahl")
	}
	if asError(nil) != nil || asError([]*Error{e}) != error(e) {
		t.Error("asError")
	}
}

func TestResolveConcurrent(t *testing.T) {
	f := newCatalogFixture(t, defaultTestSkills())
	c := f.mustLoad()
	var wg sync.WaitGroup
	ids := make([]string, 16)
	for i := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, err := c.Resolve(domain.RoleConcept, nil)
			if err != nil {
				t.Error(err)
				return
			}
			ids[i] = s.ID()
		}()
	}
	wg.Wait()
	for _, id := range ids {
		if id != ids[0] {
			t.Fatalf("uneinheitliche IDs: %v", ids)
		}
	}
}
