package profiles

import "errors"

// Code ist der stabile, maschinenlesbare Fehlercode (CLI, JSON-Ausgabe, spätere Auswertung).
type Code string

const (
	// CodeUnknownModel: Das Modell ist in models.yaml nicht konfiguriert.
	CodeUnknownModel Code = "unknown_model"
	// CodeUnsupportedEffort: Das Modell unterstützt die Stufe nicht und es gibt keinen dafür
	// genehmigten Fallback (oder die Stufe ist unbekannt).
	CodeUnsupportedEffort Code = "unsupported_effort"
	// CodeNotAllowedForRole: Der Wert liegt außerhalb der Erlaubnis des Rollenprofils.
	CodeNotAllowedForRole Code = "not_allowed_for_role"
	// CodeUnknownAgent: Der Agent ist in agents.yaml nicht angelegt.
	CodeUnknownAgent Code = "unknown_agent"
	// CodeAgentRoleMismatch: Der Agent gehört zu einer anderen Rolle als angefragt.
	CodeAgentRoleMismatch Code = "agent_role_mismatch"
	// CodeInvalidConfig: models.yaml oder agents.yaml verletzt Format oder Invarianten.
	CodeInvalidConfig Code = "invalid_config"
	// CodeInvalidRequest: Unzulässige Anfrage (unbekannte Rolle, doppelte Skill-ID, ...).
	CodeInvalidRequest Code = "invalid_request"
	// CodeNotInitialized: Die Konfigurationsdateien fehlen (ccmas profile init).
	CodeNotInitialized Code = "not_initialized"
	// CodeConflict: Die Datei wurde zwischenzeitlich geändert oder wird gerade geschrieben.
	CodeConflict Code = "conflict"
	// CodeStorage: Lesen oder Schreiben im Dateisystem ist fehlgeschlagen.
	CodeStorage Code = "storage"
)

// Error ist der einheitliche Fehlertyp des Pakets. Field nennt, wenn sinnvoll, das betroffene
// Feld (model, effort, skill_ids, policy, ...).
//
// errors.Is vergleicht den Code: errors.Is(err, &profiles.Error{Code: profiles.CodeUnknownModel})
// trifft jeden Fehler dieses Codes; ist Field im Ziel gesetzt, muss es gleich sein.
type Error struct {
	Code    Code   `json:"code"`
	Field   string `json:"field,omitempty"`
	Message string `json:"message"`
	cause   error
}

func (e *Error) Error() string { return e.Message }

// Unwrap macht eine zugrunde liegende Ursache (zum Beispiel einen Dateisystemfehler) erreichbar.
func (e *Error) Unwrap() error { return e.cause }

// Is wird von errors.Is verwendet.
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	if !ok || t == nil || t.Code != e.Code {
		return false
	}
	return t.Field == "" || t.Field == e.Field
}

func newErr(code Code, field, format string, args ...any) *Error {
	return &Error{Code: code, Field: field, Message: sprintf(format, args...)}
}

// CodeOf liefert den Code des ersten *Error in der Fehlerkette, sonst "".
func CodeOf(err error) Code {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}
