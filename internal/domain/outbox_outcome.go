package domain

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// OutboxOutcome ist der Ausgang eines Aufrufs an ein externes System, wie ein Adapter ihn klassifiziert.
//
// Entscheidend ist die Unterscheidung zwischen „wiederholbar“ und „ungewiss“: Nur ein Fehler, der sicher belegt, dass das
// externe System nichts getan hat (zum Beispiel Verbindungsaufbau gescheitert, Rate-Limit vor der Verarbeitung, abgewiesene
// Anfrage), ist wiederholbar. Ein Timeout oder eine abgebrochene Verbindung beweist weder Erfolg noch Misserfolg: Die Anfrage
// kann angekommen und ausgeführt worden sein. Das ist ungewiss und führt in den Abgleich, nie in eine blinde Wiederholung.
type OutboxOutcome string

const (
	// OutboxSuccess: Das externe System hat die Aktion ausgeführt; die externe Referenz liegt vor.
	OutboxSuccess OutboxOutcome = "success"
	// OutboxRetryable: Sicher nichts ausgeführt, ein späterer Versuch ist zulässig (Backoff, begrenzt).
	OutboxRetryable OutboxOutcome = "retryable"
	// OutboxPermanent: Die Aktion kann so nicht gelingen (abgelehnt, keine Berechtigung, ungültig): Klärungsbedarf.
	OutboxPermanent OutboxOutcome = "permanent"
	// OutboxUncertain: Ob die Aktion ausgeführt wurde, ist unbekannt: Abgleich.
	OutboxUncertain OutboxOutcome = "uncertain"
)

// Valid meldet, ob o ein bekannter Ausgang ist.
func (o OutboxOutcome) Valid() bool {
	return o == OutboxSuccess || o == OutboxRetryable || o == OutboxPermanent || o == OutboxUncertain
}

// NeedsReconcile meldet, ob der Ausgang einen Abgleich mit dem externen System verlangt (nur „ungewiss“).
func (o OutboxOutcome) NeedsReconcile() bool { return o == OutboxUncertain }

// Grenzen der Fehlerkennung. Der Fehlertext enthält nur Code und eine kurze Meldung des Adapters: Adapter geben keine
// Zugangsdaten, Header oder Antwortkörper hinein (das kann kein Wertetest beweisen und bleibt ihre Abnahmebedingung);
// begrenzt, von Steuerzeichen befreit und als zweite Sicherung von typischen Geheimnissen maskiert wird er hier
// (RedactSecrets).
const (
	MaxOutboxErrorCodeBytes    = 64
	MaxOutboxErrorMessageBytes = 256
)

// OutboxError ist die kurze, redigierte Fehlerkennung eines Aufrufs: ein Code in Kleinbuchstaben (zum Beispiel http_429,
// timeout) und eine kurze Meldung.
type OutboxError struct {
	Code    string
	Message string
}

// Normalized liefert die Fehlerkennung in der Form, in der sie gespeichert wird: Code nur aus a-z, 0-9, _, . und -
// (anderes wird zu _; leer: „error“; höchstens 64 Bytes), Meldung ohne Steuerzeichen und ungültiges UTF-8, Leerraum
// zusammengefasst, typische Geheimnisse durch „[redigiert]“ ersetzt (RedactSecrets), danach höchstens 256 Bytes (bei Kürzung
// mit „…“).
func (e OutboxError) Normalized() OutboxError {
	var code strings.Builder
	for _, r := range strings.ToLower(e.Code) {
		if code.Len() >= MaxOutboxErrorCodeBytes {
			break
		}
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '.' || r == '-' {
			code.WriteRune(r)
		} else {
			code.WriteByte('_')
		}
	}
	if code.Len() == 0 {
		code.WriteString("error")
	}
	return OutboxError{Code: code.String(), Message: cleanErrorMessage(e.Message)}
}

func cleanErrorMessage(s string) string {
	s = strings.ToValidUTF8(s, "�")
	var b strings.Builder
	space := true // führenden Leerraum vermeiden
	for _, r := range s {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) || unicode.IsSpace(r) {
			if !space {
				b.WriteByte(' ')
				space = true
			}
			continue
		}
		b.WriteRune(r)
		space = false
	}
	// Zuerst maskieren, dann kürzen: Die Längengrenze gilt für den gespeicherten Text.
	out := RedactSecrets(strings.TrimRight(b.String(), " "))
	if len(out) <= MaxOutboxErrorMessageBytes {
		return out
	}
	const ellipsis = "…"
	cut := MaxOutboxErrorMessageBytes - len(ellipsis)
	for cut > 0 && !utf8.RuneStart(out[cut]) {
		cut--
	}
	return strings.TrimRight(out[:cut], " ") + ellipsis
}

// String liefert die gespeicherte Form „code: meldung“ (ohne Meldung nur den Code), höchstens 322 Bytes und damit unter
// der Grenze der Spalte last_error (1024 Bytes).
func (e OutboxError) String() string {
	n := e.Normalized()
	if n.Message == "" {
		return n.Code
	}
	return n.Code + ": " + n.Message
}
