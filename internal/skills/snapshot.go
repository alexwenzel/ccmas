package skills

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/alexwenzel/ccmas/internal/domain"
)

// Layout eines geschriebenen Snapshots (Plugin-kompatibel):
//
//	<dir>/<snapshot_id ohne "sha256:">/
//	  snapshot.json
//	  .claude-plugin/plugin.json      {"name":"ccmas"}
//	  skills/<id>/…                   alle Dateien des Skills
const (
	snapshotFileName = "snapshot.json"
	pluginFileRel    = ".claude-plugin/plugin.json"
	pluginFileData   = `{"name":"ccmas"}`
	snapshotSkillDir = "skills"
	snapshotIDPrefix = "sha256:"
	snapshotIDHeader = "ccmas-skill-snapshot v1\n"
)

var hexDirPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// SnapshotManifest ist der Inhalt der snapshot.json (Vertrag skill-snapshot-v1).
type SnapshotManifest = domain.SkillSnapshot

type snapshotFile struct {
	path   string
	sha256 string
	data   []byte
}

type snapshotSkill struct {
	id         string
	version    int
	treeSHA256 string
	source     Source
	files      []snapshotFile
}

func newSnapshotSkill(sk Skill, files []hashedFile) snapshotSkill {
	out := snapshotSkill{id: sk.ID, version: sk.Version, treeSHA256: sk.TreeSHA256, source: sk.Source}
	for _, f := range files {
		out.files = append(out.files, snapshotFile{path: f.path, sha256: f.sha256, data: bytes.Clone(f.data)})
	}
	return out
}

// Snapshot ist ein unveränderlicher Wert: er hält eigene Kopien der Dateiinhalte und gibt
// nur Kopien heraus. Spätere Änderungen an Quelldateien oder am Katalog ändern ihn nicht.
// Der Nullwert ist kein gültiger Snapshot.
type Snapshot struct {
	id          string
	role        domain.Role
	createdAt   time.Time
	manifestSHA string
	skills      []snapshotSkill // aufsteigend nach ID
}

// computeSnapshotID berechnet die snapshot_id: "sha256:" + SHA-256 über den Kopf, die Rolle
// und je Skill (nach ID sortiert) "skill <id> <version> <tree_sha256>\n". Gleiche Auswahl und
// gleicher Inhalt ergeben dieselbe ID; Zeit und Manifest-Datei gehen nicht ein.
func computeSnapshotID(role domain.Role, skills []snapshotSkill) string {
	sorted := slices.Clone(skills)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].id < sorted[j].id })
	h := sha256.New()
	fmt.Fprint(h, snapshotIDHeader)
	fmt.Fprintf(h, "role %s\n", role)
	for _, s := range sorted {
		fmt.Fprintf(h, "skill %s %d %s\n", s.id, s.version, s.treeSHA256)
	}
	return snapshotIDPrefix + hex.EncodeToString(h.Sum(nil))
}

// ID ist die Kennung des Snapshots ("sha256:…").
func (s Snapshot) ID() string { return s.id }

// Role ist die Rolle, für die der Snapshot aufgelöst wurde.
func (s Snapshot) Role() domain.Role { return s.role }

// CreatedAt ist der Zeitpunkt der Auflösung (UTC, auf Sekunden gekürzt). Er geht nicht in die ID ein.
func (s Snapshot) CreatedAt() time.Time { return s.createdAt }

// ManifestSHA256 ist die Prüfsumme der Manifest-Datei, aus der aufgelöst wurde.
func (s Snapshot) ManifestSHA256() string { return s.manifestSHA }

// SkillIDs liefert die enthaltenen Skill-IDs, aufsteigend (Kopie).
func (s Snapshot) SkillIDs() []string {
	out := make([]string, len(s.skills))
	for i, sk := range s.skills {
		out[i] = sk.id
	}
	return out
}

// File liefert eine Kopie des Dateiinhalts eines Skills im Snapshot.
func (s Snapshot) File(skillID, path string) ([]byte, bool) {
	for _, sk := range s.skills {
		if sk.id != skillID {
			continue
		}
		for _, f := range sk.files {
			if f.path == path {
				return bytes.Clone(f.data), true
			}
		}
	}
	return nil, false
}

// Document liefert den Inhalt der snapshot.json als frische Kopie.
func (s Snapshot) Document() SnapshotManifest {
	doc := SnapshotManifest{
		SchemaVersion:  domain.SkillSnapshotSchemaVersion,
		SnapshotID:     s.id,
		Role:           s.role,
		CreatedAt:      s.createdAt,
		ManifestSHA256: s.manifestSHA,
		Skills:         []domain.SkillSnapshotSkill{},
	}
	for _, sk := range s.skills {
		d := domain.SkillSnapshotSkill{
			ID: sk.id, Version: sk.version, TreeSHA256: sk.treeSHA256,
			Source: domain.SkillSource{Package: sk.source.Package, PackageSHA256: sk.source.PackageSHA256},
			Files:  []domain.SkillSnapshotFile{},
		}
		for _, f := range sk.files {
			d.Files = append(d.Files, domain.SkillSnapshotFile{Path: f.path, SHA256: f.sha256})
		}
		doc.Skills = append(doc.Skills, d)
	}
	return doc
}

// encodeSnapshotJSON serialisiert die snapshot.json (eingerückt, ohne HTML-Escaping, mit Zeilenende).
func encodeSnapshotJSON(doc SnapshotManifest) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Write schreibt den Snapshot atomar nach <dir>/<snapshot_id ohne "sha256:">/ und liefert diesen
// Pfad. Erst entsteht ein Temp-Verzeichnis im selben Verzeichnis, dann wird umbenannt; danach
// sind Dateien schreibgeschützt (0444) und Verzeichnisse nicht beschreibbar (0555); `skills/` wird
// immer angelegt, bei einer Rolle ohne Skills leer. Existiert
// das Ziel schon, wird es mit VerifySnapshotDir geprüft und wiederverwendet; ein vorhandenes
// Ziel wird nie überschrieben.
func (s Snapshot) Write(dir string) (string, error) {
	if s.id == "" {
		return "", newErr(CodeInvalidRequest, "", "", "leerer Snapshot kann nicht geschrieben werden")
	}
	raw, err := encodeSnapshotJSON(s.Document())
	if err != nil {
		return "", newErr(CodeSnapshotWrite, "", snapshotFileName, "snapshot.json nicht serialisierbar: %v", err)
	}
	if _, err := domain.ParseSkillSnapshot(raw); err != nil {
		return "", newErr(CodeInvalidSnapshot, "", snapshotFileName, "snapshot.json verletzt den Vertrag skill-snapshot-v1: %v", err)
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return "", newErr(CodeSnapshotWrite, "", "", "Zielverzeichnis %q nicht auflösbar: %v", dir, err)
	}
	if err := os.MkdirAll(absDir, 0o755); err != nil {
		return "", newErr(CodeSnapshotWrite, "", "", "Zielverzeichnis %s nicht anlegbar: %v", absDir, err)
	}
	name := strings.TrimPrefix(s.id, snapshotIDPrefix)
	target := filepath.Join(absDir, name)
	if _, err := os.Lstat(target); err == nil {
		return s.reuse(target)
	}

	tmp, err := os.MkdirTemp(absDir, ".tmp-"+name[:12]+"-")
	if err != nil {
		return "", newErr(CodeSnapshotWrite, "", "", "Temp-Verzeichnis in %s nicht anlegbar: %v", absDir, err)
	}
	renamed := false
	defer func() {
		if !renamed {
			removeAllWritable(tmp)
		}
	}()

	var dirs []string // alle angelegten Verzeichnisse, relativ
	mkdir := func(rel string) error {
		if rel == "" || slices.Contains(dirs, rel) {
			return nil
		}
		dirs = append(dirs, rel)
		return os.MkdirAll(filepath.Join(tmp, filepath.FromSlash(rel)), 0o755)
	}
	put := func(rel string, data []byte) error {
		parent := rel[:max(strings.LastIndex(rel, "/"), 0)]
		// Elternverzeichnisse einzeln erfassen, damit alle auf 0555 gesetzt werden.
		for p := parent; p != ""; p = p[:max(strings.LastIndex(p, "/"), 0)] {
			if err := mkdir(p); err != nil {
				return err
			}
		}
		full := filepath.Join(tmp, filepath.FromSlash(rel))
		if err := os.WriteFile(full, data, 0o444); err != nil {
			return err
		}
		return os.Chmod(full, 0o444)
	}
	writeAll := func() error {
		if err := put(snapshotFileName, raw); err != nil {
			return err
		}
		if err := put(pluginFileRel, []byte(pluginFileData)); err != nil {
			return err
		}
		// skills/ gehört immer zum Layout, auch ohne Skills (Rolle ohne Standardausstattung).
		if err := mkdir(snapshotSkillDir); err != nil {
			return err
		}
		for _, sk := range s.skills {
			for _, f := range sk.files {
				if err := put(snapshotSkillDir+"/"+sk.id+"/"+f.path, f.data); err != nil {
					return err
				}
			}
		}
		// Verzeichnisse erst nach den Dateien schützen, tiefste zuerst.
		sort.Slice(dirs, func(i, j int) bool { return len(dirs[i]) > len(dirs[j]) })
		for _, d := range dirs {
			if err := os.Chmod(filepath.Join(tmp, filepath.FromSlash(d)), 0o555); err != nil {
				return err
			}
		}
		return os.Chmod(tmp, 0o555)
	}
	if err := writeAll(); err != nil {
		return "", newErr(CodeSnapshotWrite, "", "", "Snapshot nicht schreibbar: %v", err)
	}
	if err := os.Rename(tmp, target); err != nil {
		if _, statErr := os.Lstat(target); statErr == nil {
			return s.reuse(target) // paralleler Schreiber war schneller; sein Ergebnis wird geprüft
		}
		return "", newErr(CodeSnapshotWrite, "", "", "Snapshot nicht umbenennbar: %v", err)
	}
	renamed = true
	return target, nil
}

// reuse prüft ein vorhandenes Zielverzeichnis und akzeptiert es nur, wenn es unverändert
// genau diesen Snapshot enthält.
func (s Snapshot) reuse(target string) (string, error) {
	m, err := VerifySnapshotDir(target)
	if err != nil {
		return "", err
	}
	if m.SnapshotID != s.id {
		return "", newErr(CodeSnapshotMismatch, "", "", "vorhandenes Verzeichnis %s enthält den Snapshot %s, erwartet %s", target, m.SnapshotID, s.id)
	}
	return target, nil
}

// removeAllWritable löscht einen (möglicherweise schreibgeschützten) Verzeichnisbaum.
func removeAllWritable(path string) {
	_ = filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			_ = os.Chmod(p, 0o755)
		}
		return nil
	})
	_ = os.RemoveAll(path)
}

// RemoveSnapshotDir löscht ein geschriebenes (schreibgeschütztes) Snapshot-Verzeichnis.
// Nur für Aufräumen und Tests; ein Snapshot in Benutzung wird nie gelöscht.
func RemoveSnapshotDir(path string) { removeAllWritable(path) }

// VerifySnapshotDir prüft ein Snapshot-Verzeichnis und liefert dessen snapshot.json. Es
// rechnet alle Hashes nach (Dateien, tree_sha256, snapshot_id) und meldet veränderte, fehlende
// und zusätzliche Dateien oder Verzeichnisse sowie Symlinks als snapshot_mismatch mit dem
// relativen Pfad. Trägt das Verzeichnis den Namen einer snapshot_id (Layout von Write), muss
// er zur ID in der snapshot.json passen. Rechte werden nicht geprüft.
func VerifySnapshotDir(path string) (SnapshotManifest, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return SnapshotManifest{}, newErr(CodeInvalidSnapshot, "", "", "Snapshot-Verzeichnis %s nicht lesbar: %v", path, err)
	}
	if !info.IsDir() {
		return SnapshotManifest{}, newErr(CodeSnapshotMismatch, "", "", "%s ist kein Verzeichnis (oder ein Symlink)", path)
	}
	jsonPath := filepath.Join(path, snapshotFileName)
	if fi, err := os.Lstat(jsonPath); err != nil || !fi.Mode().IsRegular() {
		return SnapshotManifest{}, newErr(CodeInvalidSnapshot, "", snapshotFileName, "%s fehlt oder ist keine reguläre Datei", snapshotFileName)
	}
	raw, err := os.ReadFile(jsonPath)
	if err != nil {
		return SnapshotManifest{}, newErr(CodeInvalidSnapshot, "", snapshotFileName, "%s nicht lesbar: %v", snapshotFileName, err)
	}
	doc, err := domain.ParseSkillSnapshot(raw)
	if err != nil {
		return SnapshotManifest{}, newErr(CodeInvalidSnapshot, "", snapshotFileName, "%s verletzt den Vertrag skill-snapshot-v1: %v", snapshotFileName, err)
	}

	mismatch := func(p, format string, args ...any) *Error {
		return newErr(CodeSnapshotMismatch, "", p, "Snapshot %s, %s: "+format, append([]any{filepath.Base(path), p}, args...)...)
	}

	// Kennungen und Prüfsummen aus snapshot.json nachrechnen.
	var skills []snapshotSkill
	for _, sk := range doc.Skills {
		files := make([]hashedFile, len(sk.Files))
		for i, f := range sk.Files {
			files[i] = hashedFile{path: f.Path, sha256: f.SHA256}
		}
		if got := treeSHA256(files); got != sk.TreeSHA256 {
			return SnapshotManifest{}, newErr(CodeSnapshotMismatch, sk.ID, snapshotFileName,
				"Snapshot %s: tree_sha256 von Skill %s stimmt nicht (snapshot.json %s, berechnet %s)", filepath.Base(path), sk.ID, sk.TreeSHA256, got)
		}
		skills = append(skills, snapshotSkill{id: sk.ID, version: sk.Version, treeSHA256: sk.TreeSHA256})
	}
	if got := computeSnapshotID(doc.Role, skills); got != doc.SnapshotID {
		return SnapshotManifest{}, mismatch(snapshotFileName, "snapshot_id stimmt nicht (snapshot.json %s, berechnet %s)", doc.SnapshotID, got)
	}
	if base := filepath.Base(path); hexDirPattern.MatchString(base) && base != strings.TrimPrefix(doc.SnapshotID, snapshotIDPrefix) {
		return SnapshotManifest{}, mismatch(snapshotFileName, "Verzeichnisname passt nicht zur snapshot_id %s", doc.SnapshotID)
	}

	// Erwarteter Bestand.
	wantFiles := map[string]string{} // relativer Pfad -> erwartetes sha256 ("" = fester Inhalt)
	wantDirs := map[string]bool{".claude-plugin": true, snapshotSkillDir: true}
	addDirs := func(rel string) {
		for p := rel[:max(strings.LastIndex(rel, "/"), 0)]; p != ""; p = p[:max(strings.LastIndex(p, "/"), 0)] {
			wantDirs[p] = true
		}
	}
	wantFiles[snapshotFileName] = ""
	wantFiles[pluginFileRel] = sha256Hex([]byte(pluginFileData))
	for _, sk := range doc.Skills {
		wantDirs[snapshotSkillDir+"/"+sk.ID] = true
		for _, f := range sk.Files {
			rel := snapshotSkillDir + "/" + sk.ID + "/" + f.Path
			wantFiles[rel] = f.SHA256
			addDirs(rel)
		}
	}
	for d := range wantDirs {
		if _, clash := wantFiles[d]; clash {
			return SnapshotManifest{}, mismatch(d, "%s ist zugleich Datei und Verzeichnis", d)
		}
	}

	// Tatsächlicher Bestand (ohne Symlinks zu folgen).
	var problems []*Error
	seen := map[string]bool{}
	var walk func(rel string)
	walk = func(rel string) {
		full := filepath.Join(path, filepath.FromSlash(rel))
		entries, err := os.ReadDir(full)
		if err != nil {
			problems = append(problems, mismatch(orDot(rel), "Verzeichnis %s nicht lesbar: %v", orDot(rel), err))
			return
		}
		for _, e := range entries {
			p := e.Name()
			if rel != "" {
				p = rel + "/" + p
			}
			fi, err := os.Lstat(filepath.Join(path, filepath.FromSlash(p)))
			if err != nil {
				problems = append(problems, mismatch(p, "%s nicht lesbar: %v", p, err))
				continue
			}
			switch mode := fi.Mode(); {
			case mode.IsDir():
				if !wantDirs[p] {
					problems = append(problems, mismatch(p, "zusätzliches Verzeichnis %s", p))
					continue
				}
				seen[p] = true
				walk(p)
			case mode.IsRegular():
				want, ok := wantFiles[p]
				if !ok {
					problems = append(problems, mismatch(p, "zusätzliche Datei %s", p))
					continue
				}
				seen[p] = true
				data, err := os.ReadFile(filepath.Join(path, filepath.FromSlash(p)))
				if err != nil {
					problems = append(problems, mismatch(p, "Datei %s nicht lesbar: %v", p, err))
					continue
				}
				if p != snapshotFileName && sha256Hex(data) != want {
					problems = append(problems, mismatch(p, "Datei %s wurde verändert", p))
				}
			default:
				problems = append(problems, mismatch(p, "%s ist ein Symlink oder keine reguläre Datei", p))
			}
		}
	}
	walk("")
	missing := make([]string, 0)
	for p := range wantFiles {
		if !seen[p] {
			missing = append(missing, p)
		}
	}
	for p := range wantDirs {
		if !seen[p] {
			missing = append(missing, p)
		}
	}
	sort.Strings(missing)
	for _, p := range missing {
		problems = append(problems, mismatch(p, "%s fehlt", p))
	}
	if len(problems) > 0 {
		sort.SliceStable(problems, func(i, j int) bool { return problems[i].Path < problems[j].Path })
		return SnapshotManifest{}, asError(problems)
	}
	return doc, nil
}
