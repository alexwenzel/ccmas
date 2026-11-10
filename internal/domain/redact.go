package domain

import "regexp"

// redacted ersetzt maskierte Werte in Fehlertexten.
const redacted = "[redigiert]"

// Die Muster der Schutzschicht (RedactSecrets). Sie sind eine zweite Sicherung, kein Ersatz für saubere Adapter: Adapter
// geben keine Zugangsdaten in Fehlermeldungen (Abnahmebedingung von M05/M06). Die Muster maskieren lieber zu viel als zu
// wenig, erfassen aber nicht jede denkbare Form eines Geheimnisses.
var (
	// Authorization: <Schema> <Wert> (auch mit =), das Schema ist optional.
	reAuthorization = regexp.MustCompile(`(?i)\b(authorization)\s*[:=]\s*["']?(?:(?:bearer|basic|token|digest)\s+)?[^\s"']+["']?`)
	// Bearer <Wert>: nur Werte ab 8 Zeichen, die nicht aus lauter Kleinbuchstaben bestehen („bearer authentication“ ist Text).
	reBearer = regexp.MustCompile(`(?i)\b(bearer)\s+([A-Za-z0-9._~+/=-]{8,})`)
	// token=…, access_token=…, api_key=…, apikey=…, password=…, secret=… (ohne Rücksicht auf Groß- und Kleinschreibung, in
	// Query und Text), ebenso im JSON-Stil "password":"…". Der Doppelpunkt zählt nur nach einem Anführungszeichen, damit
	// Text wie „token: abgelaufen“ lesbar bleibt.
	reKeyValue = regexp.MustCompile(`(?i)((?:access_token|refresh_token|id_token|client_secret|api[_-]?key|apikey|passwd|password|secret|token)(?:["']?\s*=\s*|["']\s*:\s*))("[^"]*"|'[^']*'|[^\s&;,"']+)`)
	// scheme://nutzer:passwort@ und scheme://token@
	reUserinfo = regexp.MustCompile(`([A-Za-z][A-Za-z0-9+.-]*://)[^\s/?#@]+@`)
	// GitHub-Token-Präfixe (ghp_, gho_, ghu_, ghs_, ghr_, github_pat_).
	reGitHubToken = regexp.MustCompile(`(?:gh[pousr]_|github_pat_)[A-Za-z0-9_]{8,}`)
	// Asana-Personal-Access-Token: <ziffer>/<ziffern>:<32 Hexzeichen> oder <ziffer>/<ziffern>/<ziffern>:<32 Hexzeichen>.
	reAsanaToken = regexp.MustCompile(`\b\d/\d+(?:/\d+)?:[0-9a-fA-F]{32}\b`)
	// Lange Hex- und Base64-artige Folgen ab 32 Zeichen (Prüfung in isSecretLike).
	reLongRun = regexp.MustCompile(`[A-Za-z0-9+/_=-]{32,}`)
)

// RedactSecrets maskiert typische Geheimnisse in einem Text mit „[redigiert]“: Authorization- und Bearer-Werte,
// token=/access_token=/api_key=/apikey=/password=/secret=-Werte, Zugangsdaten in URLs (scheme://nutzer:passwort@), GitHub-
// und Asana-Token sowie lange Hex- und Base64-artige Folgen ab 32 Zeichen. Gewöhnliche Meldungen bleiben unverändert.
// Die Funktion ist idempotent.
//
// Lange Folgen gelten nur dann als Geheimnis, wenn sie rein hexadezimal sind oder Ziffern sowie Groß- und Kleinbuchstaben
// mischen; eine lange, kleingeschriebene Kennung (zum Beispiel ein Auftragsname mit Bindestrichen) bleibt lesbar.
func RedactSecrets(s string) string {
	s = reAuthorization.ReplaceAllString(s, "${1}: "+redacted)
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

// isSecretLike meldet, ob eine lange Zeichenfolge ein Hex- oder Base64-artiger Wert ist.
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
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F') {
			nonHex = true
		}
	}
	if !nonHex {
		return true
	}
	return lower && upper && digit
}
