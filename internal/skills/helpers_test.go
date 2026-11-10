package skills

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alexwenzel/ccmas/internal/domain"
)

// testSkill beschreibt einen Skill des Test-Katalogs. Fehlt SKILL.md in files, wird eine
// gültige erzeugt.
type testSkill struct {
	id         string
	status     Status
	adaptation string
	category   Category
	roles      []domain.Role
	defaultFor []domain.Role
	writes     []string
	files      map[string]string
}

func defaultTestSkills() []testSkill {
	return []testSkill{
		{id: "ccmas-demo-review", status: StatusApproved, category: CategoryReview,
			roles: []domain.Role{domain.RoleReviewer, domain.RoleConceptReviewer}, defaultFor: []domain.Role{domain.RoleReviewer, domain.RoleConceptReviewer}},
		{id: "ccmas-demo-impl", status: StatusApproved, category: CategoryDelivery,
			roles: []domain.Role{domain.RoleImplementer}, defaultFor: []domain.Role{domain.RoleImplementer}, writes: []string{"git", "github"}},
		{id: "ccmas-demo-rules", status: StatusApproved, category: CategoryRules,
			roles: []domain.Role{domain.RoleDocumentation}, writes: []string{"vault"}},
		{id: "ccmas-demo-know", status: StatusApproved, category: CategoryKnowledge,
			roles: []domain.Role{domain.RoleConcept, domain.RoleDocumentation}, defaultFor: []domain.Role{domain.RoleConcept},
			files: map[string]string{"scripts/check.py": "print('ok')\n", "notes/a.txt": "a\n"}},
		{id: "ccmas-demo-plan", status: StatusPendingAdaptation, adaptation: "CC-14: Anpassung an die Supervisor-Planung",
			category: CategoryPlanning, roles: []domain.Role{domain.RoleSupervisor}},
	}
}

// catalogFixture ist ein Test-Katalog in einem temporären Softwareordner.
type catalogFixture struct {
	t    *testing.T
	root string
	doc  manifestDoc
}

// newCatalogFixture legt Skills und Manifest an. Die Hashes rechnet das Hilfsprogramm
// bewusst mit eigenem, einfachem Code (nicht mit der Implementierung des Pakets).
func newCatalogFixture(t *testing.T, skills []testSkill) *catalogFixture {
	t.Helper()
	f := &catalogFixture{t: t, root: t.TempDir(), doc: manifestDoc{SchemaVersion: 1}}
	for _, s := range skills {
		f.addSkill(s)
	}
	f.writeManifest()
	return f
}

func (f *catalogFixture) skillDir(id string) string {
	return filepath.Join(f.root, ".claude", "skills", id)
}

func (f *catalogFixture) addSkill(s testSkill) {
	f.t.Helper()
	files := map[string]string{}
	for k, v := range s.files {
		files[k] = v
	}
	if _, ok := files["SKILL.md"]; !ok {
		files["SKILL.md"] = fmt.Sprintf("---\nname: %s\ndescription: Beschreibung von %s\n---\n\n# %s\n\nInhalt.\n", s.id, s.id, s.id)
	}
	var lines []string
	e := manifestEntry{
		ID: s.id, Version: 1, Status: s.status, Adaptation: s.adaptation, Category: s.category,
		Roles: s.roles, DefaultFor: s.defaultFor, ExternalWrites: s.writes,
		Source: manifestSource{
			Package:       "Fachwissen/Claude/Skills/" + strings.TrimPrefix(s.id, "ccmas-") + ".skill",
			PackageSHA256: strings.Repeat("ab", 32), Derived: "2026-11-09", Changes: "Test",
		},
	}
	var paths []string
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		full := filepath.Join(f.skillDir(s.id), filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			f.t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(files[p]), 0o644); err != nil {
			f.t.Fatal(err)
		}
		sum := sha256.Sum256([]byte(files[p]))
		e.Files = append(e.Files, manifestFile{Path: p, SHA256: hex.EncodeToString(sum[:])})
		lines = append(lines, hex.EncodeToString(sum[:])+"  "+p+"\n")
	}
	tree := sha256.Sum256([]byte(strings.Join(lines, "")))
	e.TreeSHA256 = hex.EncodeToString(tree[:])
	f.doc.Skills = append(f.doc.Skills, e)
}

func (f *catalogFixture) writeManifest() {
	f.t.Helper()
	f.writeManifestRaw(renderManifest(f.doc))
}

func (f *catalogFixture) writeManifestRaw(raw string) {
	f.t.Helper()
	path := filepath.Join(f.root, ".claude", "skills", "manifest.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func (f *catalogFixture) entry(id string) *manifestEntry {
	f.t.Helper()
	for i := range f.doc.Skills {
		if f.doc.Skills[i].ID == id {
			return &f.doc.Skills[i]
		}
	}
	f.t.Fatalf("Skill %s nicht im Test-Katalog", id)
	return nil
}

// write ersetzt eine Datei im Skill-Verzeichnis (ohne das Manifest anzupassen).
func (f *catalogFixture) write(id, rel, content string) {
	f.t.Helper()
	full := filepath.Join(f.skillDir(id), filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func (f *catalogFixture) load(opts ...Option) (*Catalog, error) {
	return Load(f.root, opts...)
}

func (f *catalogFixture) mustLoad(opts ...Option) *Catalog {
	f.t.Helper()
	c, err := f.load(opts...)
	if err != nil {
		f.t.Fatalf("Load: %v", err)
	}
	return c
}

// fixedClock liefert immer denselben Zeitpunkt.
func fixedClock() Option {
	return WithClock(func() time.Time { return time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC) })
}

// wantErr prüft Code, Skill und Pfad eines Fehlers; leere Angaben werden nicht geprüft,
// außer path ist "-" (muss leer sein ist hier nicht nötig).
func wantErr(t *testing.T, err error, code Code, skill, path string) {
	t.Helper()
	if err == nil {
		t.Fatalf("Fehler %s erwartet, aber kein Fehler", code)
	}
	for _, e := range FindingsOf(err) {
		if e.Code == code && (skill == "" || e.Skill == skill) && (path == "" || e.Path == path) {
			if !errors.Is(err, &Error{Code: code, Skill: skill, Path: path}) {
				t.Errorf("errors.Is findet %s/%s/%s nicht in %v", code, skill, path, err)
			}
			if skill != "" && !strings.Contains(e.Message, skill) {
				t.Errorf("Meldung nennt den Skill %q nicht: %q", skill, e.Message)
			}
			if path != "" && path != "." && !strings.Contains(e.Message, path) {
				t.Errorf("Meldung nennt den Pfad %q nicht: %q", path, e.Message)
			}
			return
		}
	}
	t.Fatalf("kein Befund %s (Skill %q, Pfad %q) in: %v", code, skill, path, err)
}

// makeWritable macht einen Baum löschbar; für t.TempDir-Bäume mit Snapshots (0555).
func makeWritable(t *testing.T, root string) {
	t.Helper()
	t.Cleanup(func() { RemoveSnapshotDir(root) })
}

// treeFiles liest alle Dateien unter dir (Pfad -> Inhalt) und die Verzeichnisse.
func treeFiles(t *testing.T, dir string) (files map[string]string, dirs []string) {
	t.Helper()
	files = map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel != "." {
				dirs = append(dirs, rel)
			}
			return nil
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		files[rel] = string(raw)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files, dirs
}

func chmodWritable(t *testing.T, path string) {
	t.Helper()
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := encodeSnapshotJSONAny(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func encodeSnapshotJSONAny(v any) ([]byte, error) { return json.Marshal(v) }

// renderManifest schreibt das Manifest als festes YAML. Eine eigene Ausgabe (statt einer
// YAML-Bibliothek) hält die Bytes stabil, damit die Beispiel-Fixture nicht von der
// Formatierung einer Bibliotheksversion abhängt.
func renderManifest(doc manifestDoc) string {
	var b strings.Builder
	list := func(items []string) string {
		quoted := make([]string, len(items))
		for i, it := range items {
			quoted[i] = strconv.Quote(it)
		}
		return "[" + strings.Join(quoted, ", ") + "]"
	}
	roles := func(rs []domain.Role) string {
		s := make([]string, len(rs))
		for i, r := range rs {
			s[i] = string(r)
		}
		return list(s)
	}
	fmt.Fprintf(&b, "schema_version: %d\nskills:\n", doc.SchemaVersion)
	for _, e := range doc.Skills {
		fmt.Fprintf(&b, "  - id: %s\n    version: %d\n    status: %s\n    adaptation: %s\n    category: %s\n",
			strconv.Quote(e.ID), e.Version, strconv.Quote(string(e.Status)), strconv.Quote(e.Adaptation), strconv.Quote(string(e.Category)))
		fmt.Fprintf(&b, "    roles: %s\n    default_for: %s\n    external_writes: %s\n    tree_sha256: %s\n    files:\n",
			roles(e.Roles), roles(e.DefaultFor), list(e.ExternalWrites), strconv.Quote(e.TreeSHA256))
		for _, f := range e.Files {
			fmt.Fprintf(&b, "      - path: %s\n        sha256: %s\n", strconv.Quote(f.Path), strconv.Quote(f.SHA256))
		}
		fmt.Fprintf(&b, "    source:\n      package: %s\n      package_sha256: %s\n      derived: %s\n      changes: %s\n",
			strconv.Quote(e.Source.Package), strconv.Quote(e.Source.PackageSHA256), strconv.Quote(e.Source.Derived), strconv.Quote(e.Source.Changes))
	}
	if len(doc.Skills) == 0 {
		return fmt.Sprintf("schema_version: %d\nskills: []\n", doc.SchemaVersion)
	}
	return b.String()
}
