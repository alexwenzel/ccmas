package domain

import (
	"regexp"
	"strings"
)

// redacted ersetzt maskierte Werte in Fehlertexten.
const redacted = "[redigiert]"

// Die Muster der Schutzschicht (RedactSecrets). Sie sind eine zweite Sicherung, kein Ersatz für saubere Adapter: Adapter
// geben keine Zugangsdaten in Fehlermeldungen (Abnahmebedingung von M05/M06). Die Muster maskieren lieber zu viel als zu
// wenig, erfassen aber nicht jede denkbare Form eines Geheimnisses.
var (
	// Authorization: <Schema> <Wert> (auch mit =), das Schema ist optional. Auch im JSON-Stil "Authorization": "Basic …"
	// (Anführungszeichen nach dem Namen und um den Wert). Der bereits ersetzte Wert wird als Ganzes erkannt (Idempotenz).
	reAuthorization = regexp.MustCompile(`(?i)\b(authorization)(["']?)\s*[:=]\s*["']?(?:(?:bearer|basic|token|digest)\s+)?(?:\[redigiert\]|[^\s"']+)["']?`)
	// Bearer <Wert>: nur Werte ab 8 Zeichen, die nicht aus lauter Kleinbuchstaben bestehen („bearer authentication“ ist Text).
	reBearer = regexp.MustCompile(`(?i)\b(bearer)\s+([A-Za-z0-9._~+/=-]{8,})`)
	// Die Header x-api-key und api-key mit Doppelpunkt (auch im JSON-Stil). Bewusst nur diese Namen: Eine allgemeine
	// Doppelpunkt-Regel würde gewöhnlichen Text („token: abgelaufen“) maskieren.
	reAPIKeyHeader = regexp.MustCompile(`(?i)\b((?:x-)?api-key["']?\s*:\s*)("[^"]*"|'[^']*'|[^\s&;,"']+)`)
	// token=…, access_token=…, api_key=…, apikey=…, password=…, secret=… (ohne Rücksicht auf Groß- und Kleinschreibung, in
	// Query und Text), ebenso im JSON-Stil "password":"…". Der Doppelpunkt zählt nur nach einem Anführungszeichen, damit
	// Text wie „token: abgelaufen“ lesbar bleibt.
	reKeyValue = regexp.MustCompile(`(?i)((?:access_token|refresh_token|id_token|client_secret|api[_-]?key|apikey|passwd|password|secret|token)(?:["']?\s*=\s*|["']\s*:\s*))("[^"]*"|'[^']*'|[^\s&;,"']+)`)
	// scheme://nutzer:passwort@ und scheme://token@
	reUserinfo = regexp.MustCompile(`([A-Za-z][A-Za-z0-9+.-]*://)[^\s/?#@]+@`)
	// GitHub-Token-Präfixe (ghp_, gho_, ghu_, ghs_, ghr_, github_pat_).
	reGitHubToken = regexp.MustCompile(`(?:gh[pousr]_|github_pat_)[A-Za-z0-9_]{8,}`)
	// Weitere Anbieter-Präfixe: Anthropic (sk-ant-), Slack (xoxb-, xoxp-) und die AWS-Zugangsschlüssel-ID (AKIA + 16
	// Großbuchstaben oder Ziffern).
	reProviderToken = regexp.MustCompile(`\bsk-ant-[A-Za-z0-9_-]{8,}|\bxox[bp]-[A-Za-z0-9-]{8,}|\bAKIA[0-9A-Z]{16}\b`)
	// Asana-Personal-Access-Token: <ziffer>/<ziffern>:<32 Hexzeichen> oder <ziffer>/<ziffern>/<ziffern>:<32 Hexzeichen>.
	reAsanaToken = regexp.MustCompile(`\b\d/\d+(?:/\d+)?:[0-9a-fA-F]{32}\b`)
	// Lange Hex- und Base64-artige Folgen ab 32 Zeichen (Beurteilung in isSecretLike). Der Schrägstrich gehört nicht zur
	// Zeichenklasse: Pfade und Referenzen (refs/heads/feature/CC-45-…) zerfallen in ihre Teile, jeder Teil wird für sich
	// beurteilt, und ein Lauf wird nie über einen Schrägstrich hinweg als ein Geheimnis gelesen.
	reLongRun = regexp.MustCompile(`[A-Za-z0-9+_=-]{32,}`)
)

// RedactSecrets maskiert typische Geheimnisse in einem Text mit „[redigiert]“: Authorization-, Bearer-, x-api-key- und
// api-key-Werte, token=/access_token=/api_key=/apikey=/password=/secret=-Werte, Zugangsdaten in URLs
// (scheme://nutzer:passwort@), GitHub-, Anthropic-, Slack-, AWS- und Asana-Token sowie lange Hex- und Base64-artige Folgen ab
// 32 Zeichen. Gewöhnliche Meldungen bleiben unverändert. Die Funktion ist idempotent.
//
// Lange Folgen (isSecretLike): rein hexadezimale Läufe gelten als Geheimnis, außer sie haben genau 40 oder 64 Zeichen (ein
// Commit-Hash oder SHA-256 ist kein Geheimnis und in Adaptermeldungen nützlich). Andere Läufe gelten als Geheimnis, wenn sie
// Ziffern sowie Groß- und Kleinbuchstaben mischen und kein Namensmuster haben: Ein Lauf aus mit - oder _ getrennten Segmenten,
// die überwiegend Wörter oder kurze Kennungen sind (feature-CC-45-outbox-abgleich), bleibt lesbar.
func RedactSecrets(s string) string {
	s = reAuthorization.ReplaceAllString(s, "${1}${2}: "+redacted)
	s = reAPIKeyHeader.ReplaceAllString(s, "${1}"+redacted)
	s = reBearer.ReplaceAllStringFunc(s, func(m string) string {
		sub := reBearer.FindStringSubmatch(m)
		if isLowerWord(sub[2]) {
			return m
		}
		return sub[1] + " " + redacted
	})
	s = reKeyValue.ReplaceAllString(s, "${1}"+redacted)
	s = reUserinfo.ReplaceAllString(s, "${1}"+redacted+"@")
	s = reGitHubToken.ReplaceAllString(s, redacted)
	s = reProviderToken.ReplaceAllString(s, redacted)
	s = reAsanaToken.ReplaceAllString(s, redacted)
	return reLongRun.ReplaceAllStringFunc(s, func(m string) string {
		if isSecretLike(m) {
			return redacted
		}
		return m
	})
}

func isLowerWord(s string) bool {
	for _, r := range s {
		if r < 'a' || r > 'z' {
			return false
		}
	}
	return true
}

func isHexRune(r rune) bool {
	return r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F'
}

// isSecretLike meldet, ob ein langer Lauf (ab 32 Zeichen aus A-Za-z0-9+_=-, ohne Schrägstrich) ein Geheimnis sein kann.
func isSecretLike(s string) bool {
	var lower, upper, digit, nonHex bool
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
			lower = true
		case r >= 'A' && r <= 'Z':
			upper = true
		case r >= '0' && r <= '9':
			digit = true
		}
		if !isHexRune(r) {
			nonHex = true
		}
	}
	if !nonHex {
		// Ein vollständiger Commit-Hash (SHA-1, 40) oder SHA-256 (64) ist kein Geheimnis; jede andere Länge schon.
		return len(s) != 40 && len(s) != 64
	}
	if !(lower && upper && digit) || isNamePattern(s) {
		return false
	}
	return true
}

// isNamePattern meldet, ob ein Lauf aus mit - oder _ getrennten Segmenten besteht, die überwiegend Wörter oder kurze
// Kennungen sind (Branch- und Auftragsnamen wie feature-CC-45-outbox-abgleich). Ein Base64url-Wert besteht dagegen aus
// zufällig gemischten Segmenten.
func isNamePattern(s string) bool {
	segs := strings.FieldsFunc(s, func(r rune) bool { return r == '-' || r == '_' })
	if len(segs) < 2 {
		return false
	}
	words := 0
	for _, seg := range segs {
		if isWordSegment(seg) {
			words++
		}
	}
	return words*2 > len(segs)
}

// isWordSegment meldet, ob ein Segment ein Wort (klein, groß oder Großschreibung am Anfang), eine Zahl oder eine kurze
// Kennung ist: Buchstaben mit Ziffern am Ende (v2, sha256) oder Ziffern mit wenigen Buchstaben (3rd, 1b).
func isWordSegment(seg string) bool {
	var letters, digits, lower, upper int
	letterThenDigit, digitThenLetter, seenDigit, seenLetter := true, true, false, false
	for _, r := range seg {
		switch {
		case r >= 'a' && r <= 'z':
			letters, lower = letters+1, lower+1
			seenLetter = true
			if seenDigit {
				letterThenDigit = false
			}
		case r >= 'A' && r <= 'Z':
			letters, upper = letters+1, upper+1
			seenLetter = true
			if seenDigit {
				letterThenDigit = false
			}
		case r >= '0' && r <= '9':
			digits++
			seenDigit = true
			if seenLetter {
				digitThenLetter = false
			}
		default:
			return false
		}
	}
	switch {
	case letters == 0:
		return digits > 0
	case digits == 0:
		// nur Buchstaben: klein, groß oder Großschreibung am Anfang
		return lower == 0 || upper == 0 || (upper == 1 && isUpperRune(rune(seg[0])))
	case letterThenDigit:
		return len(seg) <= 8 && digits <= 4
	default:
		// Ziffern am Anfang, dann wenige Buchstaben (digitThenLetter ist nur noch für gemischte Folgen falsch)
		return digitThenLetter && letters <= 3 && digits <= 4
	}
}

func isUpperRune(r rune) bool { return r >= 'A' && r <= 'Z' }
