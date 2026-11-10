package persistence

import (
	"database/sql"
	"time"
)

// timeLayout ist das feste Format aller Zeitstempel: RFC 3339 in UTC mit genau neun Nachkommastellen, damit die
// Textordnung der Zeitordnung entspricht.
const timeLayout = "2006-01-02T15:04:05.000000000Z"

func fmtTime(t time.Time) string { return t.UTC().Format(timeLayout) }

func parseTime(s string) (time.Time, error) { return time.Parse(timeLayout, s) }

// nullTime formatiert eine optionale Zeit (Nullzeit: NULL).
func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return fmtTime(t)
}

// scanTime liest eine Zeit aus einer TEXT-Spalte.
func scanTime(s string) (time.Time, error) {
	t, err := parseTime(s)
	if err != nil {
		return time.Time{}, wrapf(CodeInternal, err, "Zeitstempel %q nicht lesbar", s)
	}
	return t, nil
}

// scanNullTime liest eine optionale Zeit (NULL: Nullzeit).
func scanNullTime(ns sql.NullString) (time.Time, error) {
	if !ns.Valid {
		return time.Time{}, nil
	}
	return scanTime(ns.String)
}
