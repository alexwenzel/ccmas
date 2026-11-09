package cli

import (
	"errors"
	"fmt"
)

// usageError kennzeichnet Bedienfehler (Exit-Code ExitUsage).
type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }

func usageErrorf(format string, a ...any) error {
	return &usageError{msg: fmt.Sprintf(format, a...)}
}

// notAvailableError kennzeichnet Befehle, die vorgesehen, aber noch nicht
// implementiert sind (Exit-Code ExitNotAvailable).
type notAvailableError struct{ group string }

func (e *notAvailableError) Error() string {
	return fmt.Sprintf("%q ist noch nicht verfügbar (geplante Befehlsgruppe)", e.group)
}

// exitCode ordnet einen Fehler dem zentral festgelegten Exit-Code zu.
func exitCode(err error) int {
	var (
		usage *usageError
		na    *notAvailableError
	)
	switch {
	case err == nil:
		return ExitOK
	case errors.As(err, &usage):
		return ExitUsage
	case errors.As(err, &na):
		return ExitNotAvailable
	default:
		return ExitError
	}
}
