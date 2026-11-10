package domain

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"
)

// ---- Übergangstabelle -----------------------------------------------------------------------------------

func TestOutboxTransitionTable(t *testing.T) {
	want := map[OutboxState][]OutboxState{
		OutboxPending:        {OutboxClaimed, OutboxCancelled},
		OutboxClaimed:        {OutboxSucceeded, OutboxRetryWait, OutboxReconciling, OutboxNeedsAttention},
		OutboxRetryWait:      {OutboxClaimed, OutboxCancelled, OutboxNeedsAttention},
		OutboxReconciling:    {OutboxReconciling, OutboxSucceeded, OutboxPending, OutboxNeedsAttention},
		OutboxNeedsAttention: {OutboxPending, OutboxSucceeded, OutboxCancelled},
		OutboxSucceeded:      nil,
		OutboxCancelled:      nil,
	}
	if len(want) != len(OutboxStates()) {
		t.Fatalf("Tabelle im Test deckt %d von %d Zuständen ab", len(want), len(OutboxStates()))
	}
	for _, from := range OutboxStates() {
		for _, to := range OutboxStates() {
			if got, exp := from.CanTransition(to), slices.Contains(want[from], to); got != exp {
				t.Errorf("%s -> %s: CanTransition %v, erwartet %v", from, to, got, exp)
			}
		}
	}
	if OutboxState("unbekannt").CanTransition(OutboxPending) || OutboxPending.CanTransition("unbekannt") {
		t.Error("unbekannte Zustände dürfen keinen Übergang haben")
	}
}

// Ein ungewisser Ausgang führt nie direkt zurück nach pending oder in die Wartezeit: erst der Abgleich entscheidet.
func TestOutboxUncertaintyNeverRetriesBlindly(t *testing.T) {
	if OutboxClaimed.CanTransition(OutboxPending) {
		t.Error("claimed -> pending: ein abgelaufener Claim darf nicht blind zurück in die Warteschlange")
	}
	if OutboxClaimed.CanTransition(OutboxClaimed) {
		t.Error("claimed -> claimed: ein Claim wird nie ohne Abgleich erneuert")
	}
	if !OutboxClaimed.CanTransition(OutboxReconciling) {
		t.Error("claimed -> reconciling fehlt")
	}
}

func TestOutboxTerminalStates(t *testing.T) {
	for _, s := range OutboxStates() {
		want := s == OutboxSucceeded || s == OutboxCancelled
		if s.IsTerminal() != want {
			t.Errorf("%s.IsTerminal() = %v", s, s.IsTerminal())
		}
		if s.IsTerminal() {
			for _, to := range OutboxStates() {
				if s.CanTransition(to) {
					t.Errorf("Endzustand %s hat Übergang nach %s", s, to)
				}
			}
		}
	}
}

// ---- Schlüssel und Marker -------------------------------------------------------------------------------

func TestOperationKey(t *testing.T) {
	key, err := OperationKey("ccmas", "asana.comment", "wo-1", "result:2")
	if err != nil {
		t.Fatal(err)
	}
	if key != "ccmas/asana.comment/wo-1/result:2" {
		t.Errorf("Schlüssel %q", key)
	}
	if err := CheckOperationKey(key); err != nil {
		t.Errorf("CheckOperationKey: %v", err)
	}
	project, kind, parts, err := ParseOperationKey(key)
	if err != nil || project != "ccmas" || kind != "asana.comment" || !slices.Equal(parts, []string{"wo-1", "result:2"}) {
		t.Errorf("ParseOperationKey: %q %q %v %v", project, kind, parts, err)
	}
	// Derselbe Vorgang in einem anderen Projekt ergibt einen anderen Schlüssel: operation_key ist global eindeutig.
	other, err := OperationKey("other", "asana.comment", "wo-1", "result:2")
	if err != nil || other == key {
		t.Errorf("Schlüssel verschiedener Projekte: %q %q %v", key, other, err)
	}
}

func TestOperationKeyRejectsBadInput(t *testing.T) {
	long := strings.Repeat("x", 120)
	cases := []struct {
		name    string
		project string
		kind    string
		parts   []string
	}{
		{"ohne Projekt", "", "asana.comment", []string{"a"}},
		{"Projekt mit Schrägstrich", "a/b", "asana.comment", []string{"a"}},
		{"ungültige Art", "ccmas", "Asana", []string{"a"}},
		{"ohne stabile Teile", "ccmas", "asana.comment", nil},
		{"leerer Teil", "ccmas", "asana.comment", []string{"a", ""}},
		{"Teil mit Schrägstrich", "ccmas", "asana.comment", []string{"a/b"}},
		{"Teil mit Leerzeichen", "ccmas", "asana.comment", []string{"a b"}},
		{"Teil mit Zeilenumbruch", "ccmas", "asana.comment", []string{"a\nb"}},
		{"zu viele Teile", "ccmas", "asana.comment", []string{"1", "2", "3", "4", "5", "6", "7"}},
		{"zu lang", "ccmas", "asana.comment", []string{long, long, long}},
	}
	for _, c := range cases {
		if k, err := OperationKey(c.project, c.kind, c.parts...); err == nil {
			t.Errorf("%s: Schlüssel %q angenommen", c.name, k)
		}
	}
	for _, bad := range []string{"", "ccmas", "ccmas/asana.comment", "ccmas/asana.comment/", "/asana.comment/x", "ccmas//x",
		"ccmas/asana/x", "ccmas/asana.comment/x//y", "ccmas/asana.comment/x y", "asana:comment:wo-1:1"} {
		if CheckOperationKey(bad) == nil {
			t.Errorf("CheckOperationKey(%q) ohne Fehler", bad)
		}
	}
}

func TestOperationMarkerIsDeterministic(t *testing.T) {
	k1, _ := OperationKey("ccmas", "github.create_pr", "wo-1", "attempt-1")
	k2, _ := OperationKey("ccmas", "github.create_pr", "wo-1", "attempt-2")
	m1, m1again, m2 := OperationMarker(k1), OperationMarker(k1), OperationMarker(k2)
	if m1 != m1again {
		t.Errorf("Marker nicht deterministisch: %q %q", m1, m1again)
	}
	if m1 == m2 {
		t.Errorf("verschiedene Schlüssel, gleicher Marker %q", m1)
	}
	// Festes Format: ccmas-op: plus 24 Hexzeichen (96 Bit des SHA-256).
	if len(m1) != len("ccmas-op:")+24 || !strings.HasPrefix(m1, "ccmas-op:") {
		t.Errorf("Markerformat %q", m1)
	}
	if !IsOperationMarker(m1) || IsOperationMarker("ccmas-op:xyz") || IsOperationMarker("ccmas-op:"+strings.Repeat("A", 24)) {
		t.Error("IsOperationMarker prüft das Format falsch")
	}
	// Der Marker verrät den Schlüssel nicht im Klartext.
	if strings.Contains(m1, "wo-1") || strings.Contains(m1, "ccmas/") {
		t.Errorf("Marker enthält Klartext: %q", m1)
	}
	// Ein fester Wert fängt eine unbeabsichtigte Formatänderung ab: Er steht in externen Objekten und muss stabil bleiben.
	// (sha256 von "ccmas/asana.comment/wo-1/result:2", erste 24 Hexzeichen)
	if got := OperationMarker("ccmas/asana.comment/wo-1/result:2"); got != "ccmas-op:0611e4124f526b85d929952a" {
		t.Errorf("Marker %q", got)
	}
	if OutboxActionID(k1) == OutboxActionID(k2) || OutboxActionID(k1) != OutboxActionID(k1) {
		t.Error("Aktions-ID folgt nicht deterministisch aus dem Schlüssel")
	}
	if err := CheckID(OutboxActionID(k1)); err != nil {
		t.Errorf("Aktions-ID ist keine gültige ID: %v", err)
	}
}

// ---- Aktion ----------------------------------------------------------------------------------------------

func outboxFixture(t *testing.T) OutboxAction {
	t.Helper()
	a, err := NewOutboxAction("ccmas", "asana.comment", "task-9", json.RawMessage(`{"text":"fertig","n":3}`), "wo-1", "result")
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestNewOutboxAction(t *testing.T) {
	a := outboxFixture(t)
	if a.State != OutboxPending || a.Attempts != 0 || a.ProjectID != "ccmas" || a.Kind != "asana.comment" {
		t.Errorf("Aktion: %+v", a)
	}
	if a.OperationKey != "ccmas/asana.comment/wo-1/result" || a.ActionID != OutboxActionID(a.OperationKey) {
		t.Errorf("Schlüssel/ID: %q %q", a.OperationKey, a.ActionID)
	}
	if a.Marker() != OperationMarker(a.OperationKey) {
		t.Error("Marker der Aktion weicht vom Marker ihres Schlüssels ab")
	}
	if err := a.Check(); err != nil {
		t.Errorf("Check: %v", err)
	}
	// Gleiche Eingabe, gleiche Aktion: der Aufrufer kann eine Aktion nach einem Neustart erneut ablegen.
	if b := outboxFixture(t); b.ActionID != a.ActionID || b.OperationKey != a.OperationKey {
		t.Error("NewOutboxAction ist nicht deterministisch")
	}
	canon, err := a.CanonicalPayload()
	if err != nil || string(canon) != `{"n":3,"text":"fertig"}` {
		t.Errorf("kanonische Nutzdaten %q %v", canon, err)
	}
	empty := a
	empty.Payload = nil
	if canon, err := empty.CanonicalPayload(); err != nil || string(canon) != "{}" {
		t.Errorf("leere Nutzdaten: %q %v", canon, err)
	}
}

func TestOutboxActionCheck(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	mut := func(f func(a *OutboxAction)) OutboxAction {
		a := outboxFixture(t)
		f(&a)
		return a
	}
	bad := map[string]OutboxAction{
		"ohne Aktions-ID":                  mut(func(a *OutboxAction) { a.ActionID = "" }),
		"ohne Projekt":                     mut(func(a *OutboxAction) { a.ProjectID = "" }),
		"Schlüssel ohne Projektbezug":      mut(func(a *OutboxAction) { a.OperationKey = "asana:comment:wo-1:1" }),
		"Schlüssel eines anderen Projekts": mut(func(a *OutboxAction) { a.ProjectID = "other" }),
		"Schlüssel einer anderen Art":      mut(func(a *OutboxAction) { a.Kind = "asana.update" }),
		"ungültige Art":                    mut(func(a *OutboxAction) { a.Kind = "kaputt"; a.OperationKey = "ccmas/kaputt/x" }),
		"Ziel mit Steuerzeichen":           mut(func(a *OutboxAction) { a.Target = "a\x00b" }),
		"Ziel zu lang":                     mut(func(a *OutboxAction) { a.Target = strings.Repeat("x", MaxOutboxTargetBytes+1) }),
		"Nutzdaten kein Objekt":            mut(func(a *OutboxAction) { a.Payload = json.RawMessage(`[1]`) }),
		"Nutzdaten doppelter Schlüssel":    mut(func(a *OutboxAction) { a.Payload = json.RawMessage(`{"a":1,"a":2}`) }),
		"Nutzdaten Kommazahl":              mut(func(a *OutboxAction) { a.Payload = json.RawMessage(`{"a":1.5}`) }),
		"Nutzdaten zu groß": mut(func(a *OutboxAction) {
			a.Payload = json.RawMessage(`{"a":"` + strings.Repeat("x", MaxOutboxPayloadBytes) + `"}`)
		}),
		"unbekannter Zustand":                mut(func(a *OutboxAction) { a.State = "halb" }),
		"negative Versuche":                  mut(func(a *OutboxAction) { a.Attempts = -1 }),
		"negative Abgleichversuche":          mut(func(a *OutboxAction) { a.ReconcileAttempts = -1 }),
		"claimed ohne Ablauf":                mut(func(a *OutboxAction) { a.State = OutboxClaimed }),
		"Claim-Ablauf außerhalb von claimed": mut(func(a *OutboxAction) { a.ClaimExpiresAt = now }),
		"retry_wait ohne Zeitpunkt":          mut(func(a *OutboxAction) { a.State = OutboxRetryWait }),
		"succeeded ohne Referenz":            mut(func(a *OutboxAction) { a.State = OutboxSucceeded }),
		"Referenz außerhalb von succeeded":   mut(func(a *OutboxAction) { a.ExternalRef = "gid-1" }),
		"Referenz mit Steuerzeichen":         mut(func(a *OutboxAction) { a.State = OutboxSucceeded; a.ExternalRef = "gid\n1" }),
		"Fehlertext zu lang":                 mut(func(a *OutboxAction) { a.LastError = strings.Repeat("x", MaxOutboxLastErrorBytes+1) }),
	}
	for name, a := range bad {
		if err := a.Check(); err == nil {
			t.Errorf("%s: Aktion angenommen", name)
		}
	}
	good := map[string]OutboxAction{
		"claimed":         mut(func(a *OutboxAction) { a.State = OutboxClaimed; a.Attempts = 1; a.ClaimExpiresAt = now }),
		"retry_wait":      mut(func(a *OutboxAction) { a.State = OutboxRetryWait; a.Attempts = 1; a.NextAttemptAt = now }),
		"reconciling":     mut(func(a *OutboxAction) { a.State = OutboxReconciling; a.Attempts = 1; a.ReconcileAttempts = 2 }),
		"succeeded":       mut(func(a *OutboxAction) { a.State = OutboxSucceeded; a.ExternalRef = "gid-1" }),
		"needs_attention": mut(func(a *OutboxAction) { a.State = OutboxNeedsAttention; a.LastError = "permanent: nicht erlaubt" }),
		"ohne Nutzdaten":  mut(func(a *OutboxAction) { a.Payload = nil }),
		"ohne Ziel":       mut(func(a *OutboxAction) { a.Target = "" }),
	}
	for name, a := range good {
		if err := a.Check(); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// ---- Ausgang eines Aufrufs und Fehlertext ----------------------------------------------------------------

func TestOutboxOutcome(t *testing.T) {
	for _, o := range []OutboxOutcome{OutboxSuccess, OutboxRetryable, OutboxPermanent, OutboxUncertain} {
		if !o.Valid() {
			t.Errorf("%q gilt nicht", o)
		}
	}
	for _, o := range []OutboxOutcome{"", "ok", "SUCCESS"} {
		if o.Valid() {
			t.Errorf("%q gilt", o)
		}
	}
	// Nur „ungewiss“ und „Erfolg“ verlangen keine Wiederholung ohne Abgleich; nur „ungewiss“ führt in den Abgleich.
	if !OutboxUncertain.NeedsReconcile() || OutboxRetryable.NeedsReconcile() || OutboxPermanent.NeedsReconcile() || OutboxSuccess.NeedsReconcile() {
		t.Error("NeedsReconcile: nur ungewiss führt in den Abgleich")
	}
}

func TestOutboxErrorIsBoundedAndPlain(t *testing.T) {
	long := OutboxError{Code: "HTTP 429!!", Message: "zu viele\nAnfragen\x00\x1b[31m" + strings.Repeat("ä", 500)}.Normalized()
	if long.Code != "http_429__" {
		t.Errorf("Code %q", long.Code)
	}
	if len(long.Message) > MaxOutboxErrorMessageBytes || !plainText(long.Message, false) {
		t.Errorf("Meldung %d Bytes, Text %q", len(long.Message), long.Message)
	}
	if !strings.HasPrefix(long.Message, "zu viele Anfragen") || !strings.HasSuffix(long.Message, "…") {
		t.Errorf("Meldung %q", long.Message)
	}
	s := long.String()
	if len(s) > MaxOutboxLastErrorBytes || !strings.HasPrefix(s, "http_429__: ") {
		t.Errorf("String %q", s)
	}
	if got := (OutboxError{}).Normalized(); got.Code != "error" || got.String() != "error" {
		t.Errorf("leerer Fehler: %+v %q", got, got.String())
	}
	if got := (OutboxError{Code: "timeout", Message: "\xff\xfe"}).Normalized(); !plainText(got.Message, false) {
		t.Errorf("ungültiges UTF-8 bleibt: %q", got.Message)
	}
	if got := (OutboxError{Code: strings.Repeat("a", 200)}).Normalized(); len(got.Code) != MaxOutboxErrorCodeBytes {
		t.Errorf("Code-Länge %d", len(got.Code))
	}
}

func TestOutboxResolutionCheck(t *testing.T) {
	ok := []OutboxResolution{
		{Action: ResolveRetry, Reason: "Asana-Zugriff repariert"},
		{Action: ResolveConfirm, ExternalRef: "gid-5", Reason: "von Hand geprüft"},
		{Action: ResolveCancel, Reason: "Ticket wurde gelöscht"},
	}
	for _, r := range ok {
		if err := r.Check(); err != nil {
			t.Errorf("%+v: %v", r, err)
		}
	}
	bad := []OutboxResolution{
		{Action: "", Reason: "x"},
		{Action: "weiter", Reason: "x"},
		{Action: ResolveRetry},                                      // Grund fehlt
		{Action: ResolveRetry, Reason: "   "},                       // nur Leerraum
		{Action: ResolveRetry, Reason: "a\nb"},                      // Steuerzeichen
		{Action: ResolveRetry, Reason: strings.Repeat("x", 300)},    // zu lang
		{Action: ResolveConfirm, Reason: "geprüft"},                 // Referenz fehlt
		{Action: ResolveRetry, ExternalRef: "gid-5", Reason: "x"},   // Referenz nur bei confirm
		{Action: ResolveCancel, ExternalRef: "gid-5", Reason: "x"},  // Referenz nur bei confirm
		{Action: ResolveConfirm, ExternalRef: "a\nb", Reason: "ok"}, // ungültige Referenz
	}
	for _, r := range bad {
		if r.Check() == nil {
			t.Errorf("%+v angenommen", r)
		}
	}
}

func TestOutboxAuditKinds(t *testing.T) {
	kinds := OutboxAuditKinds()
	if len(kinds) < 8 {
		t.Fatalf("zu wenige Ereignisarten: %v", kinds)
	}
	seen := map[string]bool{}
	for _, k := range kinds {
		if seen[k] {
			t.Errorf("doppelt: %s", k)
		}
		seen[k] = true
		ev := AuditEvent{ProjectID: "ccmas", OccurredAt: time.Now(), Actor: "orchestrator", Kind: k, EntityType: "outbox", EntityID: "a", Level: AuditLevelOutbox}
		if err := ev.Check(); err != nil {
			t.Errorf("%s: %v", k, err)
		}
		if !strings.HasPrefix(k, "outbox.") {
			t.Errorf("%s hat nicht den Gegenstand outbox", k)
		}
	}
	// Die Arten gehören nicht in die Liste von audit.go (sie liegen bei der Outbox, wie die der Nachrichten).
	for _, k := range AuditKinds() {
		if seen[k] {
			t.Errorf("%s steht auch in AuditKinds()", k)
		}
	}
}

// Alle Werte sind erfunden; sie haben nur die Form echter Geheimnisse.
func TestRedactSecrets(t *testing.T) {
	cases := map[string]struct{ in, leak string }{
		"Authorization Bearer":    {"HTTP 401 Authorization: Bearer abc123DEF456ghi", "abc123DEF456ghi"},
		"Authorization Basic":     {"authorization=Basic dXNlcjpwYXNz weiter", "dXNlcjpwYXNz"},
		"Authorization ohne":      {"Authorization: sekret-wert-1", "sekret-wert-1"},
		"Authorization Anführung": {`header "Authorization: Bearer x1Y2z3W4v5"`, "x1Y2z3W4v5"},
		"Bearer allein":           {"abgewiesen mit Bearer Zk93mQ71xT", "Zk93mQ71xT"},
		"token":                   {"GET /v1/x?token=erfundenerWert1&a=1", "erfundenerWert1"},
		"TOKEN groß":              {"TOKEN=ERFUNDEN-77", "ERFUNDEN-77"},
		"access_token":            {"access_token=zz-erfunden-42", "zz-erfunden-42"},
		"api_key":                 {"api_key=erfunden99 abgelehnt", "erfunden99"},
		"apikey":                  {"?apikey=erfunden99&b=2", "erfunden99"},
		"API-Key":                 {"API-KEY=erfunden99", "erfunden99"},
		"password":                {"password=Pa55w0rd! zu kurz", "Pa55w0rd!"},
		"password angeführt":      {`password="mein geheimes wort" falsch`, "geheimes"},
		"secret":                  {"Secret=nichtsagen", "nichtsagen"},
		"JSON":                    {`{"password":"hunter-erfunden","user":"u"}`, "hunter-erfunden"},
		"URL mit Passwort":        {"clone https://nutzer:pw-erfunden@github.com/o/r.git fehlgeschlagen", "pw-erfunden"},
		"URL nur Nutzer":          {"fetch https://tokenwert123@example.org/x", "tokenwert123"},
		"ghp":                     {"Token ghp_ErfundenerWert1234567890abcdef", "ErfundenerWert1234567890abcdef"},
		"gho":                     {"gho_erfundenerwert123456", "erfundenerwert123456"},
		"ghu":                     {"ghu_erfundenerwert123456", "erfundenerwert123456"},
		"ghs":                     {"ghs_erfundenerwert123456", "erfundenerwert123456"},
		"ghr":                     {"ghr_erfundenerwert123456", "erfundenerwert123456"},
		"github_pat":              {"github_pat_11ERFUNDEN0_abcdefghijklmn", "11ERFUNDEN0_abcdefghijklmn"},
		"Asana":                   {"Asana 1/1200000000000001" + ":0123456789abcdef0123456789abcdef abgelehnt", "0123456789abcdef0123456789abcdef"},
		"Asana lang":              {"PAT 2/1200000000000001/1200000000000002" + ":0123456789abcdef0123456789abcdef", "0123456789abcdef0123456789abcdef"},
		"Hex lang":                {"Signatur deadbeefdeadbeefdeadbeefdeadbeef01 ungültig", "deadbeefdeadbeefdeadbeefdeadbeef01"},
		"Base64 lang":             {"Wert QWJjZGVmZ2hpamtsbW5vcHFyc3R1dnd4eXoxMjM0NTY3 ungültig", "QWJjZGVmZ2hpamtsbW5vcHFyc3R1dnd4eXoxMjM0NTY3"},
		"Base64 mit Plus":         {"x=Zm9vYmFyK2Zvb2JhciUvZm9vYmFyK2Zvb2Jhcg==", "Zm9vYmFyK2Zvb2JhciUvZm9vYmFyK2Zvb2Jhcg"},
		"Base64url":               {"Wert Zk93mQ71xT-aB4cD5eF6gH7iJ8kL9mN0oP1qR2sT3u ungültig", "Zk93mQ71xT-aB4cD5eF6gH7iJ8kL9mN0oP1qR2sT3u"},
		"Base64url mit Unterstr.": {"Wert Zk93mQ71xT_aB4cD5eF6gH7iJ8kL9mN0oP1qR2sT3u ungültig", "Zk93mQ71xT_aB4cD5eF6gH7iJ8kL9mN0oP1qR2sT3u"},
		"Hex 41":                  {"Schlüssel 0123456789abcdef0123456789abcdef01234567a ungültig", "0123456789abcdef0123456789abcdef01234567a"},
		"Hex 63":                  {"Schlüssel 0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcde", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcde"},
		"Hex 65":                  {"Schlüssel 0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef0", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef0"},
		"Hex 32":                  {"Schlüssel 0123456789abcdef0123456789abcdef ungültig", "0123456789abcdef0123456789abcdef"},
		"Hex in Pfad":             {"GET /v1/files/0123456789abcdef0123456789abcdef01 fehlt", "0123456789abcdef0123456789abcdef01"},
		"Basic JSON-Header":       {`headers {"Authorization": "Basic dXNlcjpwYXNzd29yZA==", "Accept": "x"}`, "dXNlcjpwYXNzd29yZA"},
		"Bearer JSON-Header":      {`{"authorization":"Bearer ey-erfunden.abc123"}`, "ey-erfunden.abc123"},
		"x-api-key":               {"X-Api-Key: erfundener-Schluessel-77 abgelehnt", "erfundener-Schluessel-77"},
		"api-key Header":          {"api-key: erfunden99 abgelehnt", "erfunden99"},
		"x-api-key JSON":          {`{"x-api-key": "erfunden-wert-5"}`, "erfunden-wert-5"},
		"sk-ant":                  {"Schlüssel sk-ant-api03-ErfundenerWert_abc123 abgelehnt", "ErfundenerWert_abc123"},
		"xoxb":                    {"Slack xox" + "b-1234567890-erfundenerWert abgelehnt", "erfundenerWert"},
		"xoxp":                    {"Slack xox" + "p-1234567890-erfundenerWert abgelehnt", "erfundenerWert"},
		"AKIA":                    {"Schlüssel-ID AKIAABCDEFGHIJKLMNOP abgelehnt", "AKIAABCDEFGHIJKLMNOP"},
	}
	for name, c := range cases {
		got := RedactSecrets(c.in)
		if strings.Contains(got, c.leak) {
			t.Errorf("%s: Geheimnis bleibt: %q", name, got)
		}
		if !strings.Contains(got, "[redigiert]") {
			t.Errorf("%s: keine Maskierung: %q", name, got)
		}
		if again := RedactSecrets(got); again != got {
			t.Errorf("%s: nicht idempotent: %q -> %q", name, got, again)
		}
	}

	// Gewöhnliche Meldungen bleiben unverändert und lesbar.
	for _, plain := range []string{
		"context deadline exceeded",
		"HTTP 429 Too Many Requests, retry after 30s",
		"token: abgelaufen",
		"missing bearer authentication",
		"Ticket 1200000000000001 nicht gefunden",
		"https://app.asana.com/0/123/456 nicht erreichbar",
		"Auftrag implement-feature-oauth-login-handler-for-project fehlgeschlagen",
		"ccmas/asana.comment/wo-1/result existiert bereits",
		"dial tcp 10.0.0.5:443: connect: connection refused",
		"mailto:team@example.org",
		"verboten",
		"",
		// Branch-, Referenz- und Commit-Namen der GitHub-Adapter bleiben lesbar (Befund F-A).
		"branch feature/CC-45-outbox-abgleich-und-nachrichten already exists",
		"Reference refs/heads/fix/CC-27-sqlite-zustand-mit-migrationen-und-audit already exists",
		"Reference refs/heads/docs/CC-46-Modelle-und-Effort-als-Rollenprofile-v2 already exists",
		"branch feature/CC-14-Supervisor-mit-plan-v1-Validator-Planner-und-Nachweis-M02 existiert bereits",
		"commit CC-45: Fehlertexte der Outbox auf Geheimnisse maskieren vor Speichern und Kuerzen gescheitert",
		"push nach refs/heads/feature/CC-45-outbox-abgleich-und-nachrichten abgelehnt (non-fast-forward)",
		"Branch Feature_CC_45_Outbox_Abgleich_Und_Nachrichten_Teil2 gefunden",
		"https://github.com/o/r/pull/12 und https://github.com/o/r/tree/feature/CC-45-outbox-abgleich-und-nachrichten",
		// Ein vollständiger Commit-Hash (40) und ein SHA-256 (64) sind kein Geheimnis.
		"merge base 0123456789abcdef0123456789abcdef01234567 nicht gefunden",
		"digest sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef passt nicht",
		"revision DEADBEEFDEADBEEFDEADBEEFDEADBEEFDEADBEEF fehlt",
		// Nur diese Header-Namen haben die Doppelpunkt-Regel.
		"key: nicht vorhanden",
		"invalid x api key format",
	} {
		if got := RedactSecrets(plain); got != plain {
			t.Errorf("gewöhnliche Meldung verändert: %q -> %q", plain, got)
		}
	}
}

func TestRedactSecretsKeepsHeaderShape(t *testing.T) {
	// Der JSON-Stil bleibt lesbar: Name und Struktur stehen weiter da, nur der Wert ist ersetzt.
	got := RedactSecrets(`{"Authorization": "Basic dXNlcjpwYXNz", "Accept": "x"}`)
	if want := `{"Authorization": [redigiert], "Accept": "x"}`; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if got := RedactSecrets("x-api-key: abc123 weiter"); got != "x-api-key: [redigiert] weiter" {
		t.Errorf("x-api-key: %q", got)
	}
}

func TestOutboxErrorNormalizedRedactsAndBoundsAfterwards(t *testing.T) {
	// Die Maskierung geschieht vor der Kürzung: Ein Geheimnis am Ende des Rohtexts darf nicht durch eine Kürzung in der Mitte
	// zerlegt (und damit nicht mehr erkannt) werden, und der gespeicherte Text bleibt in der Grenze.
	secret := "ghp_ErfundenerWert1234567890abcdef"
	msg := "Anfrage abgelehnt: " + strings.Repeat("x ", 100) + " token=" + secret + " " + strings.Repeat("y ", 300)
	got := OutboxError{Code: "http_401", Message: msg}.Normalized()
	if strings.Contains(got.Message, "ghp_") || strings.Contains(got.Message, "Erfunden") {
		t.Errorf("Geheimnis in %q", got.Message)
	}
	if len(got.Message) > MaxOutboxErrorMessageBytes || len(got.String()) > MaxOutboxLastErrorBytes {
		t.Errorf("Länge %d / %d", len(got.Message), len(got.String()))
	}
	// Die Längengrenze gilt nach dem Maskieren: viele kurze Geheimnisse werden länger oder kürzer, nie über die Grenze.
	many := OutboxError{Code: "x", Message: strings.Repeat("password=a ", 100)}.Normalized()
	if len(many.Message) > MaxOutboxErrorMessageBytes || strings.Contains(many.Message, "password=a ") {
		t.Errorf("viele Geheimnisse: %d Bytes, %q", len(many.Message), many.Message)
	}
	// Normalized ist idempotent.
	if again := many.Normalized(); again != many {
		t.Errorf("nicht idempotent: %+v -> %+v", many, again)
	}
	// Lesbare Meldungen bleiben lesbar.
	if ok := (OutboxError{Code: "http_403", Message: "Keine Berechtigung für das Projekt"}).Normalized(); ok.Message != "Keine Berechtigung für das Projekt" {
		t.Errorf("Meldung verändert: %q", ok.Message)
	}
}
