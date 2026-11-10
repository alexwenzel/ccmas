// Package skilltest legt für Tests einen gültigen, synthetischen CCMAS-Softwareordner an: Manifest, Skills mit
// berechneten Hashes, Workflow-Verträge und die Rollenregeln rules/roles.yaml. skills.LoadChecked lädt das
// Ergebnis ohne Befund, sodass Tests mit erfundenen Skills (und erfundenen Modell-IDs) nicht vom Stand des
// echten Bestands abhängen und trotzdem den produktiven Ladeweg benutzen.
//
// Das Paket gehört in Tests; es importiert nur internal/domain, nicht internal/skills, damit auch Tests
// innerhalb von internal/skills es nutzen könnten. Die Hashes rechnet es bewusst mit eigenem, einfachem Code
// (nicht mit der Implementierung des Katalogs), sodass ein Fehler im Katalog nicht spiegelnd mitläuft.
//
// Die Rollenregeln und die Vorlagen der Workflow-Verträge stammen aus dem Repository (rules/roles.yaml und
// die Verträge der echten Workflow-Skills): Ein synthetischer Workflow-Skill erhält den Vertrag der echten
// Vorlage seiner Kategorie mit eigener Skill-ID und ohne Verweise auf andere Skills.
package skilltest

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/alexwenzel/ccmas/internal/domain"
)

// Skill beschreibt einen Test-Skill. Leere Felder erhalten Standardwerte: Status approved, Kategorie knowledge.
type Skill struct {
	ID         string
	Status     string // approved (Standard) oder pending_adaptation
	Adaptation string // Pflicht bei pending_adaptation
	Category   string // Standard: knowledge; Workflow-Kategorien (concept, review, delivery, completion, questions) erhalten einen Vertrag
	Roles      []domain.Role
	DefaultFor []domain.Role
	// Files sind zusätzliche Dateien neben SKILL.md (relativer Pfad -> Inhalt).
	Files map[string]string
}

// contractTemplate nennt je Workflow-Kategorie den echten Skill des Repositorys, dessen Vertrag als Vorlage dient.
var contractTemplate = map[string]string{
	"concept":    "ccmas-concept-ticket",
	"review":     "ccmas-code-review",
	"delivery":   "ccmas-feature-parallel",
	"completion": "ccmas-meilenstein-abschluss",
	"questions":  "ccmas-antworten-abholen",
}

// DefaultSkills liefert einen kleinen, gültigen Bestand, der jede Rolle außer Projektanlage, Abschluss und
// Fragenbearbeitung abdeckt: Implementierung (delivery, mit Vertrag), Review und Konzeptprüfung (review, mit
// Vertrag), Konzept und Dokumentation (knowledge) sowie einen noch nicht freigegebenen Supervisor-Skill.
func DefaultSkills() []Skill {
	return []Skill{
		{ID: "ccmas-t-impl", Category: "delivery", Roles: []domain.Role{domain.RoleImplementer}, DefaultFor: []domain.Role{domain.RoleImplementer}},
		{ID: "ccmas-t-review", Category: "review", Roles: []domain.Role{domain.RoleReviewer, domain.RoleConceptReviewer},
			DefaultFor: []domain.Role{domain.RoleReviewer, domain.RoleConceptReviewer}},
		{ID: "ccmas-t-know", Category: "knowledge", Roles: []domain.Role{domain.RoleConcept, domain.RoleDocumentation},
			DefaultFor: []domain.Role{domain.RoleConcept}},
		{ID: "ccmas-t-doc", Category: "knowledge", Roles: []domain.Role{domain.RoleDocumentation}, DefaultFor: []domain.Role{domain.RoleDocumentation}},
		{ID: "ccmas-t-plan", Status: "pending_adaptation", Adaptation: "wird angepasst", Category: "planning", Roles: []domain.Role{domain.RoleSupervisor}},
	}
}

// NewRoot legt einen Softwareordner mit den Skills (ohne Angabe: DefaultSkills) in einem temporären Verzeichnis
// an und liefert dessen Wurzel. Er enthält .claude/skills/manifest.yaml, die Skill-Verzeichnisse und
// rules/roles.yaml.
func NewRoot(t testing.TB, skills ...Skill) string {
	t.Helper()
	if len(skills) == 0 {
		skills = DefaultSkills()
	}
	root := t.TempDir()
	repo := repoRoot(t)

	rules, err := os.ReadFile(filepath.Join(repo, "rules", "roles.yaml"))
	if err != nil {
		t.Fatalf("skilltest: Rollenregeln nicht lesbar: %v", err)
	}
	write(t, filepath.Join(root, "rules", "roles.yaml"), string(rules))

	templateWrites := templateExternalWrites(t, repo)
	var b strings.Builder
	b.WriteString("schema_version: 1\n")
	if len(skills) == 0 {
		b.WriteString("skills: []\n")
	} else {
		b.WriteString("skills:\n")
	}
	sorted := append([]Skill(nil), skills...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })
	for _, s := range sorted {
		writeSkill(t, &b, root, repo, s, templateWrites)
	}
	write(t, filepath.Join(root, ".claude", "skills", "manifest.yaml"), b.String())
	return root
}

func writeSkill(t testing.TB, b *strings.Builder, root, repo string, s Skill, templateWrites map[string][]string) {
	t.Helper()
	status, category := s.Status, s.Category
	if status == "" {
		status = "approved"
	}
	if category == "" {
		category = "knowledge"
	}
	files := map[string]string{}
	for k, v := range s.Files {
		files[k] = v
	}
	if _, ok := files["SKILL.md"]; !ok {
		files["SKILL.md"] = fmt.Sprintf("---\nname: %s\ndescription: Beschreibung von %s\n---\n\n# %s\n\nInhalt.\n", s.ID, s.ID, s.ID)
	}
	var writes []string
	if tmpl, ok := contractTemplate[category]; ok {
		files["contract.yaml"] = contractFor(t, repo, tmpl, s)
		writes = templateWrites[tmpl]
	}

	var paths []string
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	var lines []string
	var fileEntries strings.Builder
	for _, p := range paths {
		write(t, filepath.Join(root, ".claude", "skills", s.ID, filepath.FromSlash(p)), files[p])
		sum := sha256.Sum256([]byte(files[p]))
		h := hex.EncodeToString(sum[:])
		lines = append(lines, h+"  "+p+"\n")
		fmt.Fprintf(&fileEntries, "      - path: %s\n        sha256: %s\n", strconv.Quote(p), strconv.Quote(h))
	}
	tree := sha256.Sum256([]byte(strings.Join(lines, "")))

	fmt.Fprintf(b, "  - id: %s\n    version: 1\n    status: %s\n    adaptation: %s\n    category: %s\n",
		strconv.Quote(s.ID), strconv.Quote(status), strconv.Quote(s.Adaptation), strconv.Quote(category))
	fmt.Fprintf(b, "    roles: %s\n    default_for: %s\n    external_writes: %s\n    tree_sha256: %s\n    files:\n",
		roleList(s.Roles), roleList(s.DefaultFor), stringList(writes), strconv.Quote(hex.EncodeToString(tree[:])))
	b.WriteString(fileEntries.String())
	fmt.Fprintf(b, "    source:\n      package: %s\n      package_sha256: %s\n      derived: \"2026-11-09\"\n      changes: Test\n",
		strconv.Quote("Fachwissen/Claude/Skills/"+strings.TrimPrefix(s.ID, "ccmas-")+".skill"), strconv.Quote(strings.Repeat("ab", 32)))
}

// contractFor leitet den Vertrag aus der echten Vorlage ab: Skill-ID ersetzt, keine Verweise auf andere Skills.
// Die Rolle der Vorlage muss zu den Rollen des Test-Skills gehören.
func contractFor(t testing.TB, repo, template string, s Skill) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repo, ".claude", "skills", template, "contract.yaml"))
	if err != nil {
		t.Fatalf("skilltest: Vertragsvorlage %s nicht lesbar: %v", template, err)
	}
	var out []string
	var role string
	for _, line := range strings.Split(string(raw), "\n") {
		switch {
		case strings.HasPrefix(line, "skill: "):
			line = "skill: " + s.ID
		case strings.HasPrefix(line, "uses_skills:"):
			line = "uses_skills: []"
		case strings.HasPrefix(line, "role: "):
			role = strings.TrimSpace(strings.TrimPrefix(line, "role: "))
		}
		out = append(out, line)
	}
	found := false
	for _, r := range s.Roles {
		found = found || string(r) == role
	}
	if !found {
		t.Fatalf("skilltest: Skill %s (Kategorie-Vorlage %s) verlangt die Rolle %s, hat aber %v", s.ID, template, role, s.Roles)
	}
	return strings.Join(out, "\n")
}

// templateExternalWrites liest external_writes der Vorlage-Skills aus dem echten Manifest, damit die
// Schreibfähigkeiten der abgeleiteten Verträge im Test-Manifest gedeckt sind.
func templateExternalWrites(t testing.TB, repo string) map[string][]string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repo, ".claude", "skills", "manifest.yaml"))
	if err != nil {
		t.Fatalf("skilltest: Manifest des Repositorys nicht lesbar: %v", err)
	}
	var doc struct {
		Skills []struct {
			ID             string   `yaml:"id"`
			ExternalWrites []string `yaml:"external_writes"`
		} `yaml:"skills"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("skilltest: Manifest des Repositorys ungültig: %v", err)
	}
	out := map[string][]string{}
	for _, s := range doc.Skills {
		out[s.ID] = s.ExternalWrites
	}
	return out
}

func roleList(rs []domain.Role) string {
	s := make([]string, len(rs))
	for i, r := range rs {
		s[i] = string(r)
	}
	return stringList(s)
}

func stringList(items []string) string {
	quoted := make([]string, len(items))
	for i, it := range items {
		quoted[i] = strconv.Quote(it)
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

func write(t testing.TB, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// repoRoot findet die Wurzel des CCMAS-Repositorys: zuerst über den Ort dieser Quelldatei, sonst aufwärts vom
// Arbeitsverzeichnis des Tests (zum Beispiel bei -trimpath).
func repoRoot(t testing.TB) string {
	t.Helper()
	has := func(dir string) bool {
		fi, err := os.Stat(filepath.Join(dir, "rules", "roles.yaml"))
		return err == nil && fi.Mode().IsRegular()
	}
	if _, file, _, ok := runtime.Caller(0); ok {
		if dir := filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "..")); has(dir) {
			return dir
		}
	}
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if has(dir) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("skilltest: Repository-Wurzel (rules/roles.yaml) nicht gefunden")
		}
		dir = parent
	}
}
