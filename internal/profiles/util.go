package profiles

import (
	"fmt"
	"slices"
	"strings"
	"unicode"

	"github.com/alexwenzel/ccmas/internal/domain"
)

func sprintf(format string, args ...any) string { return fmt.Sprintf(format, args...) }

// nonNilStrings liefert eine unabhängige Kopie, nie nil (JSON: [] statt null).
func nonNilStrings(in []string) []string {
	if len(in) == 0 {
		return []string{}
	}
	return slices.Clone(in)
}

// checkModelID verlangt eine nicht leere Modell-ID ohne Leer- und Steuerzeichen (wie das Schema).
func checkModelID(id string) error {
	if id == "" || len(id) > 128 || strings.IndexFunc(id, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return fmt.Errorf("Modell-ID %q ist ungültig (nicht leer, höchstens 128 Zeichen, keine Leer- und Steuerzeichen)", id)
	}
	return nil
}

func checkEffortLevel(e domain.Effort) error {
	if !e.Valid() {
		return fmt.Errorf("unbekannte Effort-Stufe %q (erlaubt: %s)", e, joinEfforts(domain.Efforts()))
	}
	return nil
}

func joinEfforts(in []domain.Effort) string {
	parts := make([]string, len(in))
	for i, e := range in {
		parts[i] = string(e)
	}
	return strings.Join(parts, ", ")
}

// effortRank ist die Position in domain.Efforts() (aufsteigend); -1 bei unbekannter Stufe.
func effortRank(e domain.Effort) int { return slices.Index(domain.Efforts(), e) }

func hasDuplicates[T comparable](in []T) (T, bool) {
	seen := map[T]bool{}
	for _, v := range in {
		if seen[v] {
			return v, true
		}
		seen[v] = true
	}
	var zero T
	return zero, false
}

func errf(format string, args ...any) error { return fmt.Errorf(format, args...) }
