package skills

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ignoredFileName ist die einzige Datei, die im Skill-Verzeichnis ignoriert wird: von macOS
// angelegte Ordner-Metadaten. Jede andere Datei, auch versteckte, gehört ins Manifest.
const ignoredFileName = ".DS_Store"

// hashedFile ist eine Datei eines Skills mit ihrem Inhalt. data ist nur gesetzt, wenn die
// Datei gelesen wurde.
type hashedFile struct {
	path   string // relativ zum Skill-Verzeichnis, mit /
	sha256 string
	data   []byte
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// treeSHA256 berechnet die Prüfsumme eines Skill-Verzeichnisses: SHA-256 über die Zeilen
// "<datei-sha256>␠␠<pfad>\n", bytewise nach Pfad sortiert (Format wie "shasum -a 256").
func treeSHA256(files []hashedFile) string {
	sorted := append([]hashedFile(nil), files...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].path < sorted[j].path })
	h := sha256.New()
	for _, f := range sorted {
		fmt.Fprintf(h, "%s  %s\n", f.sha256, f.path)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// safeName prüft einen Dateinamen: gültiges UTF-8, keine Steuerzeichen.
func safeName(name string) bool {
	if !utf8.ValidString(name) {
		return false
	}
	return strings.IndexFunc(name, unicode.IsControl) < 0
}

// readTree liest ein Verzeichnis rekursiv, ohne Symlinks zu folgen, und liefert alle Dateien
// (außer .DS_Store) samt Inhalt und Hash, bytewise nach Pfad sortiert. Symlinks, Sonderdateien
// und unzulässige Namen sind Befunde (unsafe_resource); das Verzeichnis selbst muss ein echtes
// Verzeichnis sein. skill wird nur für Befunde verwendet.
func readTree(skill, dir string) ([]hashedFile, []*Error) {
	info, err := os.Lstat(dir)
	switch {
	case os.IsNotExist(err):
		return nil, []*Error{newErr(CodeMissingResource, skill, ".", "Skill %s: Verzeichnis fehlt", skill)}
	case err != nil:
		return nil, []*Error{newErr(CodeMissingResource, skill, ".", "Skill %s: Verzeichnis nicht lesbar: %v", skill, err)}
	case !info.IsDir():
		return nil, []*Error{newErr(CodeUnsafeResource, skill, ".", "Skill %s: %s ist kein Verzeichnis (Symlinks sind nicht erlaubt)", skill, filepath.Base(dir))}
	}
	var (
		files    []hashedFile
		findings []*Error
	)
	var walk func(rel string)
	walk = func(rel string) {
		entries, err := os.ReadDir(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			findings = append(findings, newErr(CodeMissingResource, skill, orDot(rel), "Skill %s: Verzeichnis %s nicht lesbar: %v", skill, orDot(rel), err))
			return
		}
		for _, e := range entries {
			name := e.Name()
			p := name
			if rel != "" {
				p = rel + "/" + name
			}
			if !safeName(name) {
				findings = append(findings, newErr(CodeUnsafeResource, skill, strings.ToValidUTF8(p, "�"), "Skill %s: unzulässiger Dateiname %q", skill, p))
				continue
			}
			fi, err := os.Lstat(filepath.Join(dir, filepath.FromSlash(p)))
			if err != nil {
				findings = append(findings, newErr(CodeMissingResource, skill, p, "Skill %s: %s nicht lesbar: %v", skill, p, err))
				continue
			}
			switch mode := fi.Mode(); {
			case mode.IsDir():
				walk(p)
			case mode.IsRegular():
				if name == ignoredFileName {
					continue
				}
				data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(p)))
				if err != nil {
					findings = append(findings, newErr(CodeMissingResource, skill, p, "Skill %s: Datei %s nicht lesbar: %v", skill, p, err))
					continue
				}
				files = append(files, hashedFile{path: p, sha256: sha256Hex(data), data: data})
			case mode&os.ModeSymlink != 0:
				findings = append(findings, newErr(CodeUnsafeResource, skill, p, "Skill %s: %s ist ein Symlink; Symlinks sind nicht erlaubt", skill, p))
			default:
				findings = append(findings, newErr(CodeUnsafeResource, skill, p, "Skill %s: %s ist keine reguläre Datei", skill, p))
			}
		}
	}
	walk("")
	sort.Slice(files, func(i, j int) bool { return files[i].path < files[j].path })
	return files, findings
}

func orDot(rel string) string {
	if rel == "" {
		return "."
	}
	return rel
}

// compareFiles vergleicht die gelesenen Dateien mit den Manifest-Einträgen und liefert je
// Abweichung einen Befund: missing_resource (gelistet, nicht vorhanden), changed_resource
// (anderer Hash) und unlisted_resource (vorhanden, nicht gelistet). Die Reihenfolge ist nach
// Pfad deterministisch.
func compareFiles(skill string, listed []File, actual []hashedFile) []*Error {
	want := make(map[string]string, len(listed))
	for _, f := range listed {
		want[f.Path] = f.SHA256
	}
	have := make(map[string]hashedFile, len(actual))
	for _, f := range actual {
		have[f.path] = f
	}
	paths := make([]string, 0, len(want)+len(have))
	for p := range want {
		paths = append(paths, p)
	}
	for p := range have {
		if _, ok := want[p]; !ok {
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)

	var out []*Error
	for _, p := range paths {
		wantSum, listedOK := want[p]
		got, present := have[p]
		switch {
		case listedOK && !present:
			out = append(out, newErr(CodeMissingResource, skill, p, "Skill %s: gelistete Datei %s fehlt", skill, p))
		case !listedOK:
			out = append(out, newErr(CodeUnlistedResource, skill, p, "Skill %s: Datei %s liegt im Verzeichnis, steht aber nicht im Manifest", skill, p))
		case got.sha256 != wantSum:
			out = append(out, newErr(CodeChangedResource, skill, p,
				"Skill %s: Datei %s wurde verändert (Manifest sha256 %s, tatsächlich %s)", skill, p, wantSum, got.sha256))
		}
	}
	return out
}
