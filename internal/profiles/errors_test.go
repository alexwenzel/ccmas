package profiles

import (
	"errors"
	"fmt"
	"testing"
)

func isCode(err error, code Code) bool { return errors.Is(err, &Error{Code: code}) }

func TestErrorIsMatchesCodeAndOptionalField(t *testing.T) {
	err := fmt.Errorf("Kontext: %w", &Error{Code: CodeNotAllowedForRole, Field: "effort", Message: "nicht erlaubt"})
	if !errors.Is(err, &Error{Code: CodeNotAllowedForRole}) {
		t.Error("errors.Is ohne Feld trifft nicht")
	}
	if !errors.Is(err, &Error{Code: CodeNotAllowedForRole, Field: "effort"}) {
		t.Error("errors.Is mit passendem Feld trifft nicht")
	}
	if errors.Is(err, &Error{Code: CodeNotAllowedForRole, Field: "model"}) {
		t.Error("errors.Is mit anderem Feld trifft")
	}
	if errors.Is(err, &Error{Code: CodeUnknownModel}) {
		t.Error("errors.Is mit anderem Code trifft")
	}
	if got := CodeOf(err); got != CodeNotAllowedForRole {
		t.Errorf("CodeOf = %q", got)
	}
	if CodeOf(errors.New("fremd")) != "" || CodeOf(nil) != "" {
		t.Error("CodeOf liefert für fremde Fehler einen Code")
	}
}

func TestErrorCodesAreStable(t *testing.T) {
	// Die Codes sind ein Vertrag (CLI-Ausgabe, spätere Auswertung in CC-14); sie ändern sich nicht.
	want := map[Code]string{
		CodeUnknownModel: "unknown_model", CodeUnsupportedEffort: "unsupported_effort",
		CodeNotAllowedForRole: "not_allowed_for_role", CodeUnknownAgent: "unknown_agent",
		CodeAgentRoleMismatch: "agent_role_mismatch", CodeInvalidConfig: "invalid_config",
		CodeInvalidRequest: "invalid_request", CodeNotInitialized: "not_initialized",
		CodeConflict: "conflict", CodeStorage: "storage",
	}
	for c, s := range want {
		if string(c) != s {
			t.Errorf("Code %q, erwartet %q", c, s)
		}
	}
}
