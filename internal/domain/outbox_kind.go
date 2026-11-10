package domain

import "regexp"

// maxOutboxKindLen entspricht der Spalte outbox.kind.
const maxOutboxKindLen = 64

var outboxKindPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*\.[a-z][a-z0-9_]*$`)

// CheckOutboxKind prüft die Art einer externen Aktion: <system>.<aktion> in Kleinbuchstaben, zum Beispiel
// asana.comment oder github.create_pr, höchstens 64 Zeichen. Es ist eine Formatregel, keine Liste: Welche Aktionen es gibt,
// legt der Adapter eines Systems fest. Die Datenbank erzwingt dieselbe Form durch Trigger (Migration 003).
func CheckOutboxKind(kind string) error {
	if len(kind) > maxOutboxKindLen || !outboxKindPattern.MatchString(kind) {
		return newError(CatValue, "/kind", "Aktionsart %q hat nicht die Form <system>.<aktion> (Kleinbuchstaben, Ziffern, _; höchstens %d Zeichen)", kind, maxOutboxKindLen)
	}
	return nil
}
