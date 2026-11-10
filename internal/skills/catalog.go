package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/alexwenzel/ccmas/internal/domain"
)

const skillFileName = "SKILL.md"

// Catalog ist der geprüfte Skillbestand des CCMAS-Softwareordners. Er ist nach Load
// unveränderlich; alle Rückgaben sind Kopien. Ein Katalog kann nebenläufig benutzt werden.
type Catalog struct {
	root        string
	manifestSHA string
	skills      []Skill // aufsteigend nach ID
	byID        map[string]int
	now         func() time.Time
	contracts   map[string]Contract // vorhandene contract.yaml, in sich geprüft (Stufe 1, siehe contract.go)
	hasContract map[string]bool     // Skills mit einer contract.yaml im Verzeichnis
	declared    map[string]Skill    // alle im Manifest eingetragenen Skills, auch mit Befunden (Querbezüge von ValidateWorkflow)
	checked     bool                // mit LoadChecked geladen: Plattformregeln und Verträge geprüft
}

// Option verändert das Laden.
type Option func(*Catalog)

// WithClock setzt die Zeitquelle für created_at der Snapshots (Standard: time.Now).
func WithClock(now func() time.Time) Option {
	return func(c *Catalog) { c.now = now }
}

// Load liest <root>/.claude/skills/manifest.yaml und prüft den Bestand gegen die Invarianten
// (siehe docs/architecture/skill-migration.md): Manifest, Dateien und Hashes, Frontmatter und jede
// vorhandene contract.yaml streng in sich (Schema, unbekannte Schlüssel, Plattformschlüssel in jeder Tiefe,
// Bezüge zu internal/domain, siehe docs/architecture/workflow-rules.md).
// root ist der CCMAS-Softwareordner. Es wird nie das Arbeitsverzeichnis oder ein Zielprojekt gelesen.
//
// Load verlangt weder rules/roles.yaml noch das Vorhandensein von Verträgen: Ein Katalog ohne Regeln und
// ohne Verträge lädt. Die Plattformregeln (Vertragspflicht, Querbezüge zu den Rollenregeln und zum Katalog)
// prüft ValidateWorkflow; LoadChecked bündelt beides.
//
// Bei Befunden ist der Katalog nil und der Fehler ein *Error oder Findings; FindingsOf liefert
// die Einzelbefunde.
func Load(root string, opts ...Option) (*Catalog, error) {
	c, findings := load(root, opts...)
	if len(findings) > 0 {
		return nil, asError(findings)
	}
	return c, nil
}

// LoadChecked lädt den Katalog (Load), die Rollenregeln (LoadRules) und prüft beide gegeneinander
// (ValidateWorkflow). Es ist der Weg für alles, was auf den Plattformregeln entscheidet: die CLI
// `ccmas skills validate`, die Tests gegen den echten Bestand und jeder Aufrufer der Auswerter. Alle Befunde
// werden zusammen geliefert; bei Befunden sind Katalog und Regeln nil (ohne Regeln keine Entscheidung).
func LoadChecked(root string, opts ...Option) (*Catalog, *Rules, error) {
	c, findings := load(root, opts...)
	rules, ruleFindings := loadRules(root)
	findings = append(findings, ruleFindings...)
	if c != nil && rules != nil {
		findings = append(findings, validateWorkflow(c, rules)...)
	}
	if len(findings) > 0 {
		return nil, nil, asError(findings)
	}
	c.checked = true
	return c, rules, nil
}

// Checked meldet, ob der Katalog mit LoadChecked geladen wurde, also zusammen mit den Rollenregeln und den
// Workflow-Verträgen geprüft ist. Produktive Pfade, die Snapshots für Ausführungen schreiben, verlangen das
// (internal/workers/runner); mit Load geladene Kataloge dienen der Anzeige und der Profilprüfung.
func (c *Catalog) Checked() bool { return c != nil && c.checked }

// load liefert den Katalog auch bei Befunden (dann mit den fehlerfrei geladenen Skills), damit ValidateWorkflow
// daneben weitere Befunde sammeln kann; Load gibt ihn bei Befunden nicht heraus.
func load(root string, opts ...Option) (*Catalog, []*Error) {
	if root == "" {
		return nil, []*Error{newErr(CodeMissingManifest, "", manifestRel, "kein CCMAS-Softwareordner angegeben")}
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, []*Error{newErr(CodeMissingManifest, "", manifestRel, "Softwareordner %q nicht auflösbar: %v", root, err)}
	}
	manifestPath := filepath.Join(abs, filepath.FromSlash(manifestRel))
	raw, err := readLimited(manifestPath, maxManifestSize)
	if err != nil {
		return nil, []*Error{newErr(CodeMissingManifest, "", manifestRel, "Manifest %s nicht lesbar: %v", manifestRel, err)}
	}
	doc, err := decodeManifest(raw)
	if err != nil {
		return nil, []*Error{newErr(CodeInvalidManifest, "", manifestRel, "Manifest %s ist ungültig: %v", manifestRel, err)}
	}

	c := &Catalog{
		root: abs, manifestSHA: sha256Hex(raw), byID: map[string]int{}, now: time.Now,
		contracts: map[string]Contract{}, hasContract: map[string]bool{}, declared: map[string]Skill{},
	}
	for _, o := range opts {
		o(c)
	}

	var findings []*Error
	if doc.SchemaVersion != ManifestSchemaVersion {
		findings = append(findings, newErr(CodeInvalidManifest, "", manifestRel,
			"Manifest: schema_version %d wird nicht unterstützt (erwartet %d)", doc.SchemaVersion, ManifestSchemaVersion))
	}

	registered := map[string]bool{}
	contractData := map[string][]byte{}
	for _, e := range doc.Skills {
		if registered[e.ID] {
			findings = append(findings, newErr(CodeInvalidManifest, e.ID, manifestRel, "Manifest: Skill-ID %s kommt mehrfach vor", e.ID))
			continue
		}
		registered[e.ID] = true
		entryFindings := validateEntry(e)
		findings = append(findings, entryFindings...)
		sk := e.skill()
		c.declared[e.ID] = sk
		// Den Bestand auf der Platte prüfen, auch bei Feldfehlern: so sieht man alle Befunde auf einmal.
		files, fsFindings := verifySkillDir(abs, sk)
		findings = append(findings, fsFindings...)
		if len(fsFindings) == 0 {
			findings = append(findings, c.checkFrontmatter(&sk, files)...)
			for _, f := range files {
				if f.path == contractFile {
					contractData[e.ID] = f.data
				}
			}
		}
		if len(entryFindings) == 0 && len(fsFindings) == 0 {
			c.skills = append(c.skills, sk)
		}
	}
	findings = append(findings, unregisteredSkills(abs, registered)...)

	sort.Slice(c.skills, func(i, j int) bool { return c.skills[i].ID < c.skills[j].ID })
	for i, s := range c.skills {
		c.byID[s.ID] = i
	}
	findings = append(findings, c.loadContracts(contractData)...)
	return c, findings
}

// checkFrontmatter prüft die SKILL.md (name == ID, description nicht leer) und übernimmt die Beschreibung.
func (c *Catalog) checkFrontmatter(sk *Skill, files []hashedFile) []*Error {
	for _, f := range files {
		if f.path != skillFileName {
			continue
		}
		fm, err := parseFrontmatter(f.data)
		switch {
		case err != nil:
			return []*Error{newErr(CodeInvalidFrontmatter, sk.ID, skillFileName, "Skill %s: Frontmatter in %s: %v", sk.ID, skillFileName, err)}
		case fm.Name != sk.ID:
			return []*Error{newErr(CodeInvalidFrontmatter, sk.ID, skillFileName,
				"Skill %s: Frontmatter in SKILL.md: Feld name ist %q, erwartet wird die ID %q", sk.ID, fm.Name, sk.ID)}
		case strings.TrimSpace(fm.Description) == "":
			return []*Error{newErr(CodeInvalidFrontmatter, sk.ID, skillFileName, "Skill %s: Frontmatter in SKILL.md: Feld description fehlt oder ist leer", sk.ID)}
		}
		sk.Description = fm.Description
		return nil
	}
	return nil // fehlende SKILL.md hat verifySkillDir bereits gemeldet
}

// verifySkillDir liest das Verzeichnis eines Skills neu und vergleicht es mit dem Eintrag:
// fehlende, veränderte und nicht gelistete Dateien sowie die Verzeichnis-Prüfsumme.
func verifySkillDir(root string, sk Skill) ([]hashedFile, []*Error) {
	dir := filepath.Join(root, filepath.FromSlash(skillsDirRel), sk.ID)
	files, findings := readTree(sk.ID, dir)
	if len(findings) > 0 && len(files) == 0 && findings[0].Path == "." {
		return nil, findings
	}
	findings = append(findings, compareFiles(sk.ID, sk.Files, files)...)
	if !slices.ContainsFunc(files, func(f hashedFile) bool { return f.path == skillFileName }) &&
		!slices.ContainsFunc(findings, func(e *Error) bool { return e.Code == CodeMissingResource && e.Path == skillFileName }) {
		findings = append(findings, newErr(CodeMissingResource, sk.ID, skillFileName, "Skill %s: %s fehlt", sk.ID, skillFileName))
	}
	if len(findings) == 0 {
		if got := treeSHA256(files); got != sk.TreeSHA256 {
			findings = append(findings, newErr(CodeInvalidManifest, sk.ID, manifestRel,
				"Manifest, Skill %s: tree_sha256 passt nicht zu den gelisteten Dateien (Manifest %s, berechnet %s)", sk.ID, sk.TreeSHA256, got))
		}
	}
	return files, findings
}

// unregisteredSkills meldet ccmas-Verzeichnisse unter .claude/skills ohne Manifest-Eintrag.
func unregisteredSkills(root string, registered map[string]bool) []*Error {
	dir := filepath.Join(root, filepath.FromSlash(skillsDirRel))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return []*Error{newErr(CodeMissingManifest, "", skillsDirRel, "Verzeichnis %s nicht lesbar: %v", skillsDirRel, err)}
	}
	var out []*Error
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), skillIDPrefix) && !registered[e.Name()] {
			out = append(out, newErr(CodeUnregisteredSkill, e.Name(), skillsDirRel+"/"+e.Name(),
				"Skill %s: Verzeichnis %s/%s hat keinen Eintrag im Manifest", e.Name(), skillsDirRel, e.Name()))
		}
	}
	return out
}

func readLimited(path string, limit int64) ([]byte, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if fi.Size() > limit {
		return nil, fmt.Errorf("Datei ist mit %d Bytes größer als erlaubt (%d)", fi.Size(), limit)
	}
	return os.ReadFile(path)
}

// Root liefert den (absoluten) CCMAS-Softwareordner, aus dem der Katalog stammt.
func (c *Catalog) Root() string { return c.root }

// ManifestSHA256 ist die Prüfsumme der geladenen Manifest-Datei.
func (c *Catalog) ManifestSHA256() string { return c.manifestSHA }

// Skills liefert alle Skills aufsteigend nach ID (Kopien).
func (c *Catalog) Skills() []Skill {
	out := make([]Skill, len(c.skills))
	for i, s := range c.skills {
		out[i] = s.clone()
	}
	return out
}

// Skill liefert einen Skill nach ID (Kopie).
func (c *Catalog) Skill(id string) (Skill, bool) {
	i, ok := c.byID[id]
	if !ok {
		return Skill{}, false
	}
	return c.skills[i].clone(), true
}

// ForRole liefert alle Skills, die die Rolle erhalten darf (unabhängig vom Status), nach ID.
func (c *Catalog) ForRole(role domain.Role) []Skill {
	var out []Skill
	for _, s := range c.skills {
		if s.AllowsRole(role) {
			out = append(out, s.clone())
		}
	}
	return out
}

// DefaultSkillIDs liefert die Standardausstattung einer Rolle (nach ID sortiert).
func (c *Catalog) DefaultSkillIDs(role domain.Role) []string {
	var out []string
	for _, s := range c.skills {
		if s.Status == StatusApproved && s.DefaultForRole(role) {
			out = append(out, s.ID)
		}
	}
	return out
}

// Resolve wählt die Skills einer Rolle und liefert einen unveränderlichen Snapshot. Eine leere
// Liste bedeutet die Standardausstattung der Rolle (default_for), sonst genau die genannten
// Skills. Jede ID muss existieren (unknown_skill), freigegeben sein (skill_not_approved) und
// die Rolle erlauben (skill_not_allowed_for_role). Resolve liest die Dateien neu und
// vergleicht sie mit den Hashes des Manifests: eine Änderung seit Load wird erkannt
// (changed_resource, missing_resource, unlisted_resource).
func (c *Catalog) Resolve(role domain.Role, skillIDs []string) (Snapshot, error) {
	if !role.Valid() {
		return Snapshot{}, newErr(CodeInvalidRequest, "", "", "unbekannte Rolle %q", role)
	}
	ids := skillIDs
	if len(ids) == 0 {
		ids = c.DefaultSkillIDs(role)
	}
	seen := map[string]bool{}
	var selected []Skill
	for _, id := range ids {
		if seen[id] {
			return Snapshot{}, newErr(CodeInvalidRequest, id, "", "Skill %s ist mehrfach angefordert", id)
		}
		seen[id] = true
		sk, ok := c.Skill(id)
		switch {
		case !ok:
			return Snapshot{}, newErr(CodeUnknownSkill, id, "", "Skill %s ist im Manifest nicht bekannt", id)
		case sk.Status != StatusApproved:
			return Snapshot{}, newErr(CodeSkillNotApproved, id, "",
				"Skill %s ist nicht freigegeben (Status %s: %s)", id, sk.Status, sk.Adaptation)
		case !sk.AllowsRole(role):
			return Snapshot{}, newErr(CodeSkillNotAllowedForRole, id, "", "Skill %s ist für die Rolle %s nicht erlaubt", id, role)
		}
		selected = append(selected, sk)
	}
	sort.Slice(selected, func(i, j int) bool { return selected[i].ID < selected[j].ID })

	snap := Snapshot{
		role: role, manifestSHA: c.manifestSHA,
		createdAt: c.now().UTC().Truncate(time.Second),
	}
	var findings []*Error
	for _, sk := range selected {
		files, fs := verifySkillDir(c.root, sk)
		if len(fs) > 0 {
			findings = append(findings, fs...)
			continue
		}
		snap.skills = append(snap.skills, newSnapshotSkill(sk, files))
	}
	if len(findings) > 0 {
		return Snapshot{}, asError(findings)
	}
	snap.id = computeSnapshotID(role, snap.skills)
	return snap, nil
}
