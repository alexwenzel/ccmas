package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"modernc.org/sqlite"

	"github.com/alexwenzel/ccmas/internal/domain"
)

// Code ist ein stabiler, maschinenlesbarer Fehlercode. Die Codes sind Teil der Schnittstelle (CLI, Dienst,
// Statusprüfung) und ändern sich nicht ohne Versionswechsel.
type Code string

const (
	// CodeBusy: Die Datenbank blieb über den busy_timeout hinaus gesperrt (SQLITE_BUSY). Wiederholen ist zulässig.
	CodeBusy Code = "busy"
	// CodeSchemaOutdated: Die Datenbank ist nicht (vollständig) migriert; Migrate ist explizit aufzurufen.
	CodeSchemaOutdated Code = "schema_outdated"
	// CodeSchemaTooNew: Die Datenbank trägt eine höhere Schemaversion, als diese Software kennt. Kein Zugriff, kein Downgrade.
	CodeSchemaTooNew Code = "schema_too_new"
	// CodeChecksumMismatch: Die Prüfsumme einer angewandten Migration weicht von der eingebetteten ab (migration_checksum_mismatch).
	CodeChecksumMismatch Code = "migration_checksum_mismatch"
	// CodeHistoryMismatch: Die angewandten Migrationen sind kein Präfix der bekannten (migration_history_mismatch).
	CodeHistoryMismatch Code = "migration_history_mismatch"
	// CodeNotFound: Die Entität gibt es nicht.
	CodeNotFound Code = "not_found"
	// CodeConflict: Die Entität gibt es schon mit anderem Inhalt, oder der erwartete Ist-Zustand stimmt nicht.
	CodeConflict Code = "conflict"
	// CodeState: Der Zustandsübergang ist nach der Übergangstabelle der Ebene nicht erlaubt (Fehlerklasse state).
	CodeState Code = "state"
	// CodeInvalid: Die Eingabe verletzt ihren Vertrag (Schema, ID, Wertebereich) und wurde nicht geschrieben.
	CodeInvalid Code = "invalid"
	// CodeConstraint: Die Datenbank hat die Änderung abgewiesen (Fremdschlüssel, CHECK, Trigger).
	CodeConstraint Code = "constraint"
	// CodeClosed: Der Store oder die Transaktion ist geschlossen.
	CodeClosed Code = "closed"
	// CodeInternal: Ein unerwarteter Fehler der Datenbank oder des Dateisystems.
	CodeInternal Code = "internal"
)

// Error ist der Fehlertyp des Pakets: ein stabiler Code, eine Meldung und die Ursache (errors.Unwrap).
// Verletzt eine Eingabe den Vertrag, ist die Ursache ein *domain.ContractError.
type Error struct {
	Code    Code
	Message string
	Err     error
}

func (e *Error) Error() string {
	switch {
	case e.Message != "" && e.Err != nil:
		return fmt.Sprintf("persistence [%s]: %s: %v", e.Code, e.Message, e.Err)
	case e.Message != "":
		return fmt.Sprintf("persistence [%s]: %s", e.Code, e.Message)
	case e.Err != nil:
		return fmt.Sprintf("persistence [%s]: %v", e.Code, e.Err)
	}
	return fmt.Sprintf("persistence [%s]", e.Code)
}

// Unwrap liefert die Ursache.
func (e *Error) Unwrap() error { return e.Err }

// Is erlaubt errors.Is(err, &Error{Code: CodeBusy}): gleicher Code.
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t.Code == e.Code && t.Message == "" && t.Err == nil
}

// CodeOf liefert den Code des ersten *Error in der Kette (leer, wenn es keinen gibt).
func CodeOf(err error) Code {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

func errf(code Code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

func wrapf(code Code, cause error, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...), Err: cause}
}

// invalid macht aus einem Vertragsverstoß der Domain einen Fehler der Klasse invalid.
func invalid(err error) error {
	if err == nil {
		return nil
	}
	var pe *Error
	if errors.As(err, &pe) {
		return err
	}
	return &Error{Code: CodeInvalid, Err: err}
}

func invalidf(format string, args ...any) error {
	return &Error{Code: CodeInvalid, Err: &domain.ContractError{
		Categories: []domain.Category{domain.CatValue}, Message: fmt.Sprintf(format, args...)}}
}

// stateErr ist ein unerlaubter Übergang: Code state, Ursache ein ContractError der Klasse state.
func stateErr(format string, args ...any) error {
	return &Error{Code: CodeState, Err: &domain.ContractError{
		Categories: []domain.Category{domain.CatState}, Message: fmt.Sprintf(format, args...)}}
}

// SQLite-Ergebniscodes (https://sqlite.org/rescode.html), die das Paket unterscheidet.
const (
	sqliteBusy             = 5
	sqliteLocked           = 6
	sqliteConstraint       = 19
	sqliteConstraintPK     = 1555
	sqliteConstraintUnique = 2067
)

// mapErr ordnet einen Fehler der Datenbank einem stabilen Code zu. Fehler des Pakets und Abbrüche des Kontexts
// bleiben unverändert.
func mapErr(err error) error {
	if err == nil {
		return nil
	}
	var pe *Error
	if errors.As(err, &pe) {
		return err
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var se *sqlite.Error
	if errors.As(err, &se) {
		code := se.Code()
		switch code & 0xff {
		case sqliteBusy, sqliteLocked:
			return wrapf(CodeBusy, err, "Datenbank gesperrt")
		case sqliteConstraint:
			if code == sqliteConstraintPK || code == sqliteConstraintUnique {
				return wrapf(CodeConflict, err, "Eintrag existiert bereits")
			}
			return wrapf(CodeConstraint, err, "Datenbank hat die Änderung abgewiesen")
		}
	}
	if errors.Is(err, sql.ErrNoRows) {
		return wrapf(CodeNotFound, err, "nicht gefunden")
	}
	return wrapf(CodeInternal, err, "Datenbankfehler")
}
