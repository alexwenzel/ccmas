package skills

import (
	"errors"
	"fmt"
	"strings"
)

// Code ist der stabile, maschinenlesbare Fehlercode (CLI und JSON-Ausgabe).
type Code string

const (
	// CodeUnknownSkill: Die Skill-ID kommt im Manifest nicht vor.
	CodeUnknownSkill Code = "unknown_skill"
	// CodeUnregisteredSkill: Ein ccmas-Verzeichnis unter .claude/skills hat keinen Manifest-Eintrag.
	CodeUnregisteredSkill Code = "unregistered_skill"
	// CodeSkillNotApproved: Der Skill hat nicht den Status approved.
	CodeSkillNotApproved Code = "skill_not_approved"
	// CodeSkillNotAllowedForRole: Die Rolle darf den Skill nicht erhalten.
	CodeSkillNotAllowedForRole Code = "skill_not_allowed_for_role"
	// CodeMissingResource: Verzeichnis, SKILL.md oder eine gelistete Datei fehlt.
	CodeMissingResource Code = "missing_resource"
	// CodeChangedResource: Der Inhalt einer Datei weicht vom Manifest-Hash ab.
	CodeChangedResource Code = "changed_resource"
	// CodeUnlistedResource: Eine Datei liegt im Skill-Verzeichnis, steht aber nicht im Manifest.
	CodeUnlistedResource Code = "unlisted_resource"
	// CodeUnsafeResource: Symlink, Sonderdatei oder unzulässiger Dateiname im Skill-Verzeichnis.
	CodeUnsafeResource Code = "unsafe_resource"
	// CodeMissingManifest: Die Manifest-Datei fehlt oder ist nicht lesbar.
	CodeMissingManifest Code = "missing_manifest"
	// CodeInvalidManifest: Das Manifest verletzt Format oder Invarianten.
	CodeInvalidManifest Code = "invalid_manifest"
	// CodeInvalidFrontmatter: Das Frontmatter der SKILL.md fehlt, ist fehlerhaft oder passt nicht zur ID.
	CodeInvalidFrontmatter Code = "invalid_frontmatter"
	// CodeInvalidRequest: Unzulässige Anfrage (unbekannte Rolle, doppelte Skill-ID, ...).
	CodeInvalidRequest Code = "invalid_request"
	// CodeSnapshotMismatch: Ein Snapshot-Verzeichnis weicht von seinem snapshot.json ab.
	CodeSnapshotMismatch Code = "snapshot_mismatch"
	// CodeInvalidSnapshot: snapshot.json fehlt, ist nicht lesbar oder verletzt den Vertrag.
	CodeInvalidSnapshot Code = "invalid_snapshot"
	// CodeSnapshotWrite: Der Snapshot konnte nicht geschrieben werden (Dateisystem).
	CodeSnapshotWrite Code = "snapshot_write"
)

// Error ist der einheitliche Fehlertyp des Pakets. Skill und Path sind leer, wenn der
// Fehler nicht an einem Skill beziehungsweise einer Datei hängt. Path ist relativ zum
// Skill-Verzeichnis (bei Manifest- und Snapshot-Fehlern relativ zum Wurzelverzeichnis
// beziehungsweise zum Snapshot-Verzeichnis).
//
// errors.Is vergleicht den Code: errors.Is(err, &skills.Error{Code: skills.CodeUnknownSkill})
// trifft jeden Fehler dieses Codes; sind Skill oder Path im Ziel gesetzt, müssen auch sie gleich sein.
type Error struct {
	Code    Code   `json:"code"`
	Skill   string `json:"skill,omitempty"`
	Path    string `json:"path,omitempty"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Message }

// Is wird von errors.Is verwendet.
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	if !ok || t == nil || t.Code != e.Code {
		return false
	}
	return (t.Skill == "" || t.Skill == e.Skill) && (t.Path == "" || t.Path == e.Path)
}

func newErr(code Code, skill, path, format string, args ...any) *Error {
	return &Error{Code: code, Skill: skill, Path: path, Message: fmt.Sprintf(format, args...)}
}

// CodeOf liefert den Code des ersten *Error in der Fehlerkette, sonst "".
func CodeOf(err error) Code {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

// Findings sind mehrere Befunde aus einer Prüfung. errors.Is und errors.As sehen jeden Befund.
type Findings []*Error

func (f Findings) Error() string {
	lines := make([]string, len(f))
	for i, e := range f {
		lines[i] = e.Message
	}
	return strings.Join(lines, "\n")
}

// Unwrap macht die einzelnen Befunde für errors.Is/As erreichbar.
func (f Findings) Unwrap() []error {
	out := make([]error, len(f))
	for i, e := range f {
		out[i] = e
	}
	return out
}

// FindingsOf liefert die Einzelbefunde eines Fehlers: bei Findings alle, bei einem *Error
// diesen einen, sonst nil.
func FindingsOf(err error) []*Error {
	var f Findings
	if errors.As(err, &f) {
		return append([]*Error(nil), f...)
	}
	var e *Error
	if errors.As(err, &e) {
		return []*Error{e}
	}
	return nil
}

// asError gibt bei genau einem Befund diesen zurück, sonst die Sammlung; ohne Befund nil.
func asError(f []*Error) error {
	switch len(f) {
	case 0:
		return nil
	case 1:
		return f[0]
	}
	return Findings(f)
}
