package persistence

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"unicode/utf8"

	"github.com/alexwenzel/ccmas/internal/domain"
)

// jsonText serialisiert v als kompaktes JSON.
func jsonText(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", wrapf(CodeInternal, err, "JSON")
	}
	return string(b), nil
}

// decodeStrict liest einen JSON-Wert ohne unbekannte Felder und ohne Folgedaten.
func decodeStrict(text string, into any) error {
	dec := json.NewDecoder(bytes.NewReader([]byte(text)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		return wrapf(CodeInternal, err, "gespeichertes JSON nicht lesbar")
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return errf(CodeInternal, "gespeichertes JSON hat Folgedaten")
	}
	return nil
}

// decodeLoose liest JSON und ignoriert unbekannte Felder (für Protokolle, die über Versionen wachsen).
func decodeLoose(text string, into any) error {
	if err := json.Unmarshal([]byte(text), into); err != nil {
		return wrapf(CodeInternal, err, "gespeichertes JSON nicht lesbar")
	}
	return nil
}

func sha256Hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// orNil macht aus einer leeren Zeichenkette NULL.
func orNil(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nonNil(in []string) []string {
	if in == nil {
		return []string{}
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}

func str(ns sql.NullString) string {
	if ns.Valid {
		return ns.String
	}
	return ""
}

// checkText prüft einen Freitext: gültiges UTF-8, ohne Steuerzeichen (Zeilenumbruch und Tab nur mit layout),
// höchstens max Bytes.
func checkText(name, s string, max int, layout, required bool) error {
	switch {
	case required && s == "":
		return invalidf("%s darf nicht leer sein", name)
	case len(s) > max:
		return invalidf("%s hat %d Bytes, erlaubt sind %d", name, len(s), max)
	case !utf8.ValidString(s):
		return invalidf("%s ist kein gültiges UTF-8", name)
	}
	for _, r := range s {
		if (r == '\n' || r == '\t') && layout {
			continue
		}
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) {
			return invalidf("%s enthält Steuerzeichen", name)
		}
	}
	return nil
}

func checkIDs(ids ...string) error {
	for _, id := range ids {
		if err := domain.CheckID(id); err != nil {
			return invalid(err)
		}
	}
	return nil
}

func checkOptionalID(id string) error {
	if id == "" {
		return nil
	}
	return checkIDs(id)
}

// isCommit meldet, ob s ein vollständiger Commit-Hash ist (40 Hex-Zeichen, klein).
func isCommit(s string) bool {
	if len(s) != 40 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func isHex64(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
