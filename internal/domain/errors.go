package domain

import (
	"fmt"
	"sort"
	"strings"
)

// Category ordnet einen Vertragsverstoß einer Fehlerklasse zu. Die Klassen sind in Go und
// Python identisch (Python: claude_sdk_runner.contracts.CATEGORIES) und werden von den
// gemeinsamen Fixtures belegt.
type Category string

const (
	// Verstöße gegen Form und Schema (gemeinsam mit Python geprüft).

	CatSyntax       Category = "syntax"        // kein einzelnes JSON-Objekt, doppelter Schlüssel, ungültiges UTF-8
	CatSize         Category = "size"          // Nachricht oder Feld größer als erlaubt
	CatVersion      Category = "version"       // Protokoll- oder Schema-Version nicht unterstützt
	CatField        Category = "field"         // Pflichtfeld fehlt
	CatUnknownField Category = "unknown_field" // unbekanntes Feld (Strict-Parsing)
	CatID           Category = "id"            // ID-Format oder ID-Bezug verletzt
	CatPath         Category = "path"          // Pfad nicht kanonisch/absolut oder mit ..
	CatModel        Category = "model"         // Modell ungültig oder unbekannt
	CatEffort       Category = "effort"        // Effort-Stufe ungültig oder vom Modell nicht unterstützt
	CatAction       Category = "action"        // unbekannte Supervisor-Aktion
	CatMessageType  Category = "message_type"  // unbekannter Nachrichtentyp oder falsche Richtung
	CatTimestamp    Category = "timestamp"     // kein gültiger RFC-3339-Zeitstempel
	CatLimit        Category = "limit"         // konfigurierbares Limit außerhalb des Bereichs
	CatRole         Category = "role"          // unbekannte oder unpassende Rolle
	CatCommit       Category = "commit"        // Commit ungültig oder passt nicht zum Beleg
	CatValue        Category = "value"         // sonstiger unzulässiger Wert (Typ, Aufzählung, Bereich)

	// Verstöße, die nur Go prüft (Zustand und Reihenfolge).

	CatSequence Category = "sequence" // Nachricht an dieser Stelle der Folge unzulässig
	CatStale    Category = "stale"    // Nachricht eines älteren Versuchs
	CatRevision Category = "revision" // veraltete Zustandsrevision
	CatState    Category = "state"    // Zustand erlaubt die Operation nicht
)

// Categories liefert alle Fehlerklassen in fester Reihenfolge.
func Categories() []Category {
	return []Category{
		CatSyntax, CatSize, CatVersion, CatField, CatUnknownField, CatID, CatPath, CatModel,
		CatEffort, CatAction, CatMessageType, CatTimestamp, CatLimit, CatRole, CatCommit,
		CatValue, CatSequence, CatStale, CatRevision, CatState,
	}
}

// ContractError ist der einheitliche Fehlertyp für Vertragsverstöße.
//
// Categories enthält die eindeutigen Klassen aller gefundenen Verstöße, alphabetisch
// sortiert; Category liefert die erste. Dieselbe Regel gilt in Python.
type ContractError struct {
	Categories []Category
	// Location nennt die Fundstelle als JSON-Pointer im Dokument (leer: ganzes Dokument).
	Location string
	Message  string
}

// Category liefert die führende Fehlerklasse.
func (e *ContractError) Category() Category {
	if len(e.Categories) == 0 {
		return CatValue
	}
	return e.Categories[0]
}

func (e *ContractError) Error() string {
	cats := make([]string, len(e.Categories))
	for i, c := range e.Categories {
		cats[i] = string(c)
	}
	loc := e.Location
	if loc == "" {
		loc = "/"
	}
	return fmt.Sprintf("vertragsverstoß [%s] bei %s: %s", strings.Join(cats, ","), loc, e.Message)
}

func newError(cat Category, location, format string, args ...any) *ContractError {
	return &ContractError{Categories: []Category{cat}, Location: location, Message: fmt.Sprintf(format, args...)}
}

// categorySet bildet die sortierte, eindeutige Klassenliste.
func categorySet(in []Category) []Category {
	seen := map[Category]bool{}
	var out []Category
	for _, c := range in {
		if !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
