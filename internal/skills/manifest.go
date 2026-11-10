package skills

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"time"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"

	"github.com/alexwenzel/ccmas/internal/domain"
)

// ManifestSchemaVersion ist die unterstützte schema_version des Manifests.
const ManifestSchemaVersion = 1

// Orte relativ zum CCMAS-Softwareordner.
const (
	skillsDirRel    = ".claude/skills"
	manifestRel     = ".claude/skills/manifest.yaml"
	skillIDPrefix   = "ccmas-"
	maxManifestSize = 1 << 20
)

// Status bestimmt, ob ein Skill in einen Laufzeit-Snapshot darf.
type Status string

const (
	// StatusApproved: freigegeben; darf in einen Snapshot.
	StatusApproved Status = "approved"
	// StatusPendingAdaptation: vorhanden und sichtbar, aber noch nicht an CCMAS angepasst;
	// Resolve lehnt ihn ab.
	StatusPendingAdaptation Status = "pending_adaptation"
)

// Category ordnet einen Skill einer geschlossenen Menge von Aufgabenarten zu.
type Category string

const (
	CategoryPlanning   Category = "planning"
	CategorySetup      Category = "setup"
	CategoryConcept    Category = "concept"
	CategoryKnowledge  Category = "knowledge"
	CategoryTracking   Category = "tracking"
	CategoryRules      Category = "rules"
	CategoryDelivery   Category = "delivery"
	CategoryCompletion Category = "completion"
	CategoryQuestions  Category = "questions"
	CategoryReview     Category = "review"
)

// Categories liefert alle zulässigen Kategorien.
func Categories() []Category {
	return []Category{
		CategoryPlanning, CategorySetup, CategoryConcept, CategoryKnowledge, CategoryTracking,
		CategoryRules, CategoryDelivery, CategoryCompletion, CategoryQuestions, CategoryReview,
	}
}

// ExternalWrites sind die externen Systeme, in die ein Skill nach Inventarangabe schreiben
// lässt. Das ist keine Berechtigung: Schreibvorgänge laufen später über kontrollierte Adapter.
func ExternalWrites() []string { return []string{"asana", "vault", "git", "github"} }

// forbiddenForReview sind die Kategorien, die nie an Prüfrollen gehen dürfen.
var forbiddenForReview = []Category{CategoryRules, CategoryDelivery}

var reviewRoles = []domain.Role{domain.RoleReviewer, domain.RoleConceptReviewer}

var (
	skillIDPattern = regexp.MustCompile(`^ccmas-[a-z0-9]+(-[a-z0-9]+)*$`)
	sha256Pattern  = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// File ist eine Datei eines Skills mit der erwarteten Prüfsumme.
type File struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

// Source beschreibt die Herkunft des Skills (nur Inventarangabe; Go liest nie den Vault).
type Source struct {
	Package       string `json:"package"`        // vault-relativer Pfad des Originalpakets
	PackageSHA256 string `json:"package_sha256"` // Prüfsumme des Originalpakets
	Derived       string `json:"derived"`        // Datum der Ableitung (JJJJ-MM-TT)
	Changes       string `json:"changes"`        // Kurztext der Änderungen gegenüber dem Original
}

// Skill ist ein Eintrag des Katalogs. Werte sind Kopien; Änderungen daran wirken nicht auf den Katalog.
type Skill struct {
	ID             string        `json:"id"`
	Version        int           `json:"version"`
	Status         Status        `json:"status"`
	Adaptation     string        `json:"adaptation,omitempty"`
	Category       Category      `json:"category"`
	Description    string        `json:"description"` // aus dem Frontmatter der SKILL.md
	Roles          []domain.Role `json:"roles"`
	DefaultFor     []domain.Role `json:"default_for"`
	ExternalWrites []string      `json:"external_writes"`
	Files          []File        `json:"files"`
	TreeSHA256     string        `json:"tree_sha256"`
	Source         Source        `json:"source"`
}

func (s Skill) clone() Skill {
	s.Roles = nonNil(slices.Clone(s.Roles))
	s.DefaultFor = nonNil(slices.Clone(s.DefaultFor))
	s.ExternalWrites = nonNil(slices.Clone(s.ExternalWrites))
	s.Files = nonNil(slices.Clone(s.Files))
	return s
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// AllowsRole meldet, ob die Rolle den Skill erhalten darf.
func (s Skill) AllowsRole(r domain.Role) bool { return slices.Contains(s.Roles, r) }

// DefaultForRole meldet, ob der Skill zur Standardausstattung der Rolle gehört.
func (s Skill) DefaultForRole(r domain.Role) bool { return slices.Contains(s.DefaultFor, r) }

// ---- YAML-Form ---------------------------------------------------------------

type manifestDoc struct {
	SchemaVersion int             `yaml:"schema_version"`
	Skills        []manifestEntry `yaml:"skills"`
}

type manifestEntry struct {
	ID             string         `yaml:"id"`
	Version        int            `yaml:"version"`
	Status         Status         `yaml:"status"`
	Adaptation     string         `yaml:"adaptation"`
	Category       Category       `yaml:"category"`
	Roles          []domain.Role  `yaml:"roles"`
	DefaultFor     []domain.Role  `yaml:"default_for"`
	ExternalWrites []string       `yaml:"external_writes"`
	Files          []manifestFile `yaml:"files"`
	TreeSHA256     string         `yaml:"tree_sha256"`
	Source         manifestSource `yaml:"source"`
}

type manifestFile struct {
	Path   string `yaml:"path"`
	SHA256 string `yaml:"sha256"`
}

type manifestSource struct {
	Package       string `yaml:"package"`
	PackageSHA256 string `yaml:"package_sha256"`
	Derived       string `yaml:"derived"`
	Changes       string `yaml:"changes"`
}

// decodeManifest dekodiert streng: unbekannte Schlüssel, doppelte Schlüssel und weitere
// YAML-Dokumente sind Fehler.
func decodeManifest(raw []byte) (manifestDoc, error) {
	var doc manifestDoc
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&doc); err != nil {
		if errors.Is(err, io.EOF) {
			return doc, errors.New("Manifest ist leer")
		}
		return doc, err
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return doc, errors.New("Manifest enthält mehr als ein YAML-Dokument")
	}
	return doc, nil
}

// validateEntry prüft die Felder eines Eintrags (ohne Dateisystem) und liefert alle Befunde.
func validateEntry(e manifestEntry) []*Error {
	var out []*Error
	bad := func(format string, args ...any) {
		out = append(out, newErr(CodeInvalidManifest, e.ID, manifestRel, "Manifest, Skill %s: %s", e.ID, fmt.Sprintf(format, args...)))
	}
	if !skillIDPattern.MatchString(e.ID) {
		bad("id %q ist ungültig (erwartet: %s…, nur a-z, 0-9 und -)", e.ID, skillIDPrefix)
	}
	if e.Version < 1 {
		bad("version muss eine Ganzzahl >= 1 sein (ist %d)", e.Version)
	}
	switch e.Status {
	case StatusApproved:
		if e.Adaptation != "" {
			bad("adaptation ist nur bei status %s erlaubt", StatusPendingAdaptation)
		}
	case StatusPendingAdaptation:
		if e.Adaptation == "" {
			bad("adaptation ist bei status %s Pflicht (was und wo wird angepasst)", StatusPendingAdaptation)
		}
		if len(e.DefaultFor) > 0 {
			bad("default_for ist bei status %s nicht erlaubt (Standardausstattung nur mit freigegebenen Skills)", StatusPendingAdaptation)
		}
	default:
		bad("status %q ist ungültig (erlaubt: %s, %s)", e.Status, StatusApproved, StatusPendingAdaptation)
	}
	if !slices.Contains(Categories(), e.Category) {
		bad("category %q ist ungültig (erlaubt: %v)", e.Category, Categories())
	}
	if len(e.Roles) == 0 {
		bad("roles darf nicht leer sein")
	}
	seenRole := map[domain.Role]bool{}
	for _, r := range e.Roles {
		switch {
		case !r.Valid():
			bad("roles enthält die unbekannte Rolle %q", r)
		case seenRole[r]:
			bad("roles enthält %q mehrfach", r)
		}
		seenRole[r] = true
	}
	seenDefault := map[domain.Role]bool{}
	for _, r := range e.DefaultFor {
		switch {
		case !slices.Contains(e.Roles, r):
			bad("default_for nennt %q, das nicht in roles steht", r)
		case seenDefault[r]:
			bad("default_for enthält %q mehrfach", r)
		}
		seenDefault[r] = true
	}
	if slices.Contains(forbiddenForReview, e.Category) {
		for _, r := range reviewRoles {
			if slices.Contains(e.Roles, r) {
				bad("Kategorie %s darf der Rolle %s nie erlaubt werden", e.Category, r)
			}
		}
	}
	seenWrite := map[string]bool{}
	for _, w := range e.ExternalWrites {
		switch {
		case !slices.Contains(ExternalWrites(), w):
			bad("external_writes enthält %q (erlaubt: %v)", w, ExternalWrites())
		case seenWrite[w]:
			bad("external_writes enthält %q mehrfach", w)
		}
		seenWrite[w] = true
	}
	if len(e.Files) == 0 {
		bad("files darf nicht leer sein")
	}
	seenFile := map[string]bool{}
	hasSkillMD := false
	for _, f := range e.Files {
		if err := domain.CheckRelativePath(f.Path); err != nil || !utf8.ValidString(f.Path) {
			bad("files: Pfad %q ist nicht kanonisch relativ", f.Path)
		}
		if seenFile[f.Path] {
			bad("files enthält %q mehrfach", f.Path)
		}
		seenFile[f.Path] = true
		if f.Path == skillFileName {
			hasSkillMD = true
		}
		if !sha256Pattern.MatchString(f.SHA256) {
			bad("files: sha256 von %s ist kein SHA-256 in Hex-Kleinbuchstaben", f.Path)
		}
	}
	if len(e.Files) > 0 && !hasSkillMD {
		bad("files muss %s enthalten", skillFileName)
	}
	if !sha256Pattern.MatchString(e.TreeSHA256) {
		bad("tree_sha256 ist kein SHA-256 in Hex-Kleinbuchstaben")
	}
	if err := domain.CheckRelativePath(e.Source.Package); err != nil || !utf8.ValidString(e.Source.Package) {
		bad("source.package %q ist kein vault-relativer Pfad", e.Source.Package)
	}
	if !sha256Pattern.MatchString(e.Source.PackageSHA256) {
		bad("source.package_sha256 ist kein SHA-256 in Hex-Kleinbuchstaben")
	}
	if _, err := time.Parse("2006-01-02", e.Source.Derived); err != nil {
		bad("source.derived %q ist kein Datum (JJJJ-MM-TT)", e.Source.Derived)
	}
	if e.Source.Changes == "" {
		bad("source.changes darf nicht leer sein")
	}
	return out
}

func (e manifestEntry) skill() Skill {
	s := Skill{
		ID: e.ID, Version: e.Version, Status: e.Status, Adaptation: e.Adaptation, Category: e.Category,
		Roles: slices.Clone(e.Roles), DefaultFor: slices.Clone(e.DefaultFor),
		ExternalWrites: slices.Clone(e.ExternalWrites), TreeSHA256: e.TreeSHA256,
		Source: Source{
			Package: e.Source.Package, PackageSHA256: e.Source.PackageSHA256,
			Derived: e.Source.Derived, Changes: e.Source.Changes,
		},
	}
	for _, f := range e.Files {
		s.Files = append(s.Files, File{Path: f.Path, SHA256: f.SHA256})
	}
	return s.clone()
}
