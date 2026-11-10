package domain

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"
)

// Grenzen einer Outbox-Aktion (Spalten der Tabelle outbox und Vertrag der Adapter).
const (
	// MaxOutboxPayloadBytes begrenzt die Nutzdaten (Länge des übergebenen JSON).
	MaxOutboxPayloadBytes = 64 << 10
	// MaxOutboxTargetBytes begrenzt das Ziel (zum Beispiel eine Task-GID oder ein Repository).
	MaxOutboxTargetBytes = 256
	// MaxOutboxExternalRefBytes begrenzt die externe Referenz (GID, PR-Nummer oder URL).
	MaxOutboxExternalRefBytes = 256
	// MaxOutboxLastErrorBytes entspricht der Spalte outbox.last_error.
	MaxOutboxLastErrorBytes = 1024
	// MaxOperationKeyBytes entspricht der Spalte outbox.operation_key.
	MaxOperationKeyBytes = 256
	// MaxOperationKeyParts begrenzt die stabilen Teile eines Schlüssels.
	MaxOperationKeyParts = 6
	// MaxOutboxReasonBytes begrenzt den Grund einer Klärung.
	MaxOutboxReasonBytes = 256
)

// ---- Schlüssel und Marker ---------------------------------------------------------------------------------

// OperationKey bildet den stabilen Schlüssel einer externen Absicht: <projekt>/<art>/<teil>[/<teil>…].
//
// Der Schlüssel enthält das Projekt, weil outbox.operation_key global eindeutig ist: Gleiche Operationen verschiedener
// Projekte sollen nicht als Konflikt kollidieren. Die stabilen Teile nennen, was die Absicht eindeutig macht (zum Beispiel
// Auftrag und Zweck: wo-1/result); sie ändern sich bei einer Wiederholung derselben Absicht nie (also keine Zeit, keine
// Zufallszahl). Jeder Teil hat das Format einer ID (CheckID, ohne Schrägstrich); 1 bis 6 Teile, insgesamt höchstens 256 Zeichen.
func OperationKey(projectID, kind string, parts ...string) (string, error) {
	if len(parts) == 0 {
		return "", newError(CatField, "/operation_key", "ein Schlüssel braucht mindestens einen stabilen Teil")
	}
	// Jede Komponente einzeln: Ein Schrägstrich im Teil ergäbe sonst stillschweigend zwei Teile.
	for _, c := range append([]string{projectID}, parts...) {
		if err := CheckID(c); err != nil {
			return "", wrapLocation(err, "/operation_key")
		}
	}
	key := projectID + "/" + kind + "/" + strings.Join(parts, "/")
	if _, _, _, err := ParseOperationKey(key); err != nil {
		return "", err
	}
	return key, nil
}

// ParseOperationKey zerlegt einen Schlüssel in Projekt, Art und stabile Teile und prüft dabei seine Form.
func ParseOperationKey(key string) (projectID, kind string, parts []string, err error) {
	if len(key) > MaxOperationKeyBytes {
		return "", "", nil, newError(CatSize, "/operation_key", "Schlüssel hat %d Bytes, erlaubt sind %d", len(key), MaxOperationKeyBytes)
	}
	segs := strings.Split(key, "/")
	if len(segs) < 3 {
		return "", "", nil, newError(CatValue, "/operation_key", "Schlüssel %q hat nicht die Form <projekt>/<art>/<teil>", key)
	}
	if err := CheckID(segs[0]); err != nil {
		return "", "", nil, wrapLocation(err, "/operation_key")
	}
	if err := CheckOutboxKind(segs[1]); err != nil {
		return "", "", nil, wrapLocation(err, "/operation_key")
	}
	parts = segs[2:]
	if len(parts) > MaxOperationKeyParts {
		return "", "", nil, newError(CatLimit, "/operation_key", "%d stabile Teile, erlaubt sind %d", len(parts), MaxOperationKeyParts)
	}
	for _, p := range parts {
		if err := CheckID(p); err != nil {
			return "", "", nil, wrapLocation(err, "/operation_key")
		}
	}
	return segs[0], segs[1], parts, nil
}

// CheckOperationKey prüft die Form eines Schlüssels (siehe OperationKey).
func CheckOperationKey(key string) error {
	_, _, _, err := ParseOperationKey(key)
	return err
}

const (
	operationMarkerPrefix = "ccmas-op:"
	operationHashLen      = 24 // Hexzeichen: 96 Bit des SHA-256
)

func operationHash(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])[:operationHashLen]
}

// OperationMarker liefert den deterministischen Marker eines Schlüssels: ccmas-op: plus die ersten 24 Hexzeichen des
// SHA-256 des Schlüssels. Adapter schreiben ihn in das externe Objekt (Beschreibung, Kommentar, PR-Text) und suchen ihn beim
// Abgleich. Er verrät den Schlüssel nicht im Klartext und ist für gleiche Schlüssel immer gleich.
func OperationMarker(key string) string { return operationMarkerPrefix + operationHash(key) }

// IsOperationMarker meldet, ob s die Form eines Markers hat.
func IsOperationMarker(s string) bool {
	h, ok := strings.CutPrefix(s, operationMarkerPrefix)
	if !ok || len(h) != operationHashLen {
		return false
	}
	for _, c := range h {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// OutboxActionID liefert die Aktions-ID zu einem Schlüssel: op- plus dieselben 24 Hexzeichen wie der Marker. Die ID folgt aus
// dem Schlüssel; dieselbe Absicht ergibt nach einem Neustart dieselbe Zeile.
func OutboxActionID(key string) string { return "op-" + operationHash(key) }

// ---- Aktion ---------------------------------------------------------------------------------------------------

// OutboxAction ist eine externe Aktion in der Outbox (Spezifikation „Externe Aktionen ohne Dubletten“): die Absicht, in
// einem externen System (Asana, GitHub) zu schreiben, gespeichert vor der Ausführung in derselben Transaktion wie die
// fachliche Änderung, die sie auslöst.
//
// Die Nutzdaten sind ein JSON-Objekt (ohne doppelte Schlüssel, Ganzzahlen statt Kommazahlen, höchstens
// MaxOutboxPayloadBytes); sie enthalten keine Zugangsdaten. Das Audit-Ereignis schreibt sie nie mit.
type OutboxAction struct {
	// ActionID folgt aus dem Schlüssel (OutboxActionID).
	ActionID  string `json:"action_id"`
	ProjectID string `json:"project_id"`
	// OperationKey ist der stabile, projektbezogene Schlüssel (OperationKey).
	OperationKey string `json:"operation_key"`
	// Kind ist die Art <system>.<aktion> (CheckOutboxKind); das System wählt den Adapter.
	Kind string `json:"kind"`
	// Target nennt das Ziel im externen System (zum Beispiel eine Task-GID); optional.
	Target  string          `json:"target,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"` // leer: {}
	State   OutboxState     `json:"state"`
	// Attempts zählt die Aufrufe (Beanspruchungen); ReconcileAttempts die nicht schlüssigen Abgleiche der laufenden
	// Ungewissheit.
	Attempts          int `json:"attempts"`
	ReconcileAttempts int `json:"reconcile_attempts"`
	// NextAttemptAt ist der nächste Versuch (retry_wait) oder Abgleich (reconciling); leer: sofort fällig.
	NextAttemptAt time.Time `json:"next_attempt_at,omitzero"`
	// ClaimExpiresAt gilt nur im Zustand claimed.
	ClaimExpiresAt time.Time `json:"claim_expires_at,omitzero"`
	// LastError ist die redigierte Fehlerkennung des letzten Fehlers (OutboxError.String), keine Rohmeldung.
	LastError string `json:"last_error,omitempty"`
	// ExternalRef ist die Referenz des externen Objekts; Pflicht im Zustand succeeded, sonst leer.
	ExternalRef string    `json:"external_ref,omitempty"`
	CreatedAt   time.Time `json:"created_at,omitzero"`
	UpdatedAt   time.Time `json:"updated_at,omitzero"`
}

// NewOutboxAction bildet eine neue Aktion im Zustand pending: Schlüssel aus Projekt, Art und stabilen Teilen, Aktions-ID aus
// dem Schlüssel. Dieselbe Eingabe ergibt dieselbe Aktion.
func NewOutboxAction(projectID, kind, target string, payload json.RawMessage, keyParts ...string) (OutboxAction, error) {
	key, err := OperationKey(projectID, kind, keyParts...)
	if err != nil {
		return OutboxAction{}, err
	}
	a := OutboxAction{
		ActionID: OutboxActionID(key), ProjectID: projectID, OperationKey: key, Kind: kind, Target: target,
		Payload: payload, State: OutboxPending,
	}
	return a, a.Check()
}

// Marker liefert den Marker der Aktion (OperationMarker ihres Schlüssels).
func (a OutboxAction) Marker() string { return OperationMarker(a.OperationKey) }

// CanonicalPayload prüft die Nutzdaten und liefert ihre kanonische Form: kompakt, Schlüssel sortiert, ohne Maskierung
// von <, > und &, Zahlen unverändert. Leere Nutzdaten sind {}. Gleiche Nutzdaten ergeben dieselben Bytes; darauf beruht der
// Vergleich beim erneuten Ablegen einer Aktion.
func (a OutboxAction) CanonicalPayload() ([]byte, error) {
	raw := bytes.TrimSpace(a.Payload)
	if len(raw) == 0 {
		return []byte("{}"), nil
	}
	if len(a.Payload) > MaxOutboxPayloadBytes {
		return nil, newError(CatSize, "/payload", "Nutzdaten haben %d Bytes, erlaubt sind %d", len(a.Payload), MaxOutboxPayloadBytes)
	}
	if cerr := strictParse(raw); cerr != nil {
		cerr.Location = "/payload" + cerr.Location
		return nil, cerr
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v map[string]any
	if err := dec.Decode(&v); err != nil {
		return nil, newError(CatSyntax, "/payload", "Nutzdaten nicht lesbar: %v", err)
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, newError(CatValue, "/payload", "Nutzdaten nicht serialisierbar: %v", err)
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

// CheckExternalRef prüft eine externe Referenz: nicht leer, höchstens 256 Bytes, gültiges UTF-8 ohne Steuerzeichen.
func CheckExternalRef(ref string) error {
	switch {
	case ref == "":
		return newError(CatField, "/external_ref", "die externe Referenz fehlt")
	case len(ref) > MaxOutboxExternalRefBytes:
		return newError(CatSize, "/external_ref", "externe Referenz hat %d Bytes, erlaubt sind %d", len(ref), MaxOutboxExternalRefBytes)
	case !plainText(ref, false):
		return newError(CatValue, "/external_ref", "externe Referenz enthält Steuerzeichen oder ungültiges UTF-8")
	}
	return nil
}

// Check prüft Form und Grenzen der Aktion sowie die Stimmigkeit von Zustand und Feldern (Claim nur im Zustand claimed,
// Zeitpunkt in retry_wait, Referenz genau bei succeeded). Ob das Projekt existiert, prüft die Persistenz.
func (a OutboxAction) Check() error {
	if err := CheckID(a.ActionID); err != nil {
		return err
	}
	if err := CheckID(a.ProjectID); err != nil {
		return err
	}
	keyProject, keyKind, _, err := ParseOperationKey(a.OperationKey)
	if err != nil {
		return err
	}
	if keyProject != a.ProjectID {
		return newError(CatValue, "/operation_key", "der Schlüssel nennt das Projekt %q, die Aktion gehört zu %q", keyProject, a.ProjectID)
	}
	if err := CheckOutboxKind(a.Kind); err != nil {
		return err
	}
	if keyKind != a.Kind {
		return newError(CatValue, "/operation_key", "der Schlüssel nennt die Art %q, die Aktion hat %q", keyKind, a.Kind)
	}
	if a.ActionID != OutboxActionID(a.OperationKey) {
		return newError(CatID, "/action_id", "die Aktions-ID %q folgt nicht aus dem Schlüssel (erwartet %q)", a.ActionID, OutboxActionID(a.OperationKey))
	}
	if len(a.Target) > MaxOutboxTargetBytes || !plainText(a.Target, false) {
		return newError(CatValue, "/target", "das Ziel ist zu lang (höchstens %d Bytes) oder enthält Steuerzeichen", MaxOutboxTargetBytes)
	}
	if _, err := a.CanonicalPayload(); err != nil {
		return err
	}
	if !a.State.Valid() {
		return newError(CatState, "/state", "unbekannter Zustand %q", a.State)
	}
	if a.Attempts < 0 || a.ReconcileAttempts < 0 {
		return newError(CatValue, "/attempts", "negative Zähler")
	}
	if len(a.LastError) > MaxOutboxLastErrorBytes || !plainText(a.LastError, false) {
		return newError(CatValue, "/last_error", "der Fehlertext ist zu lang (höchstens %d Bytes) oder enthält Steuerzeichen", MaxOutboxLastErrorBytes)
	}
	if a.ExternalRef != "" {
		if err := CheckExternalRef(a.ExternalRef); err != nil {
			return err
		}
	}
	// Form je Zustand (dieselben Regeln erzwingt die Datenbank durch Trigger).
	if (a.State == OutboxClaimed) != !a.ClaimExpiresAt.IsZero() {
		return newError(CatState, "/claim_expires_at", "ein Claim-Ablauf gehört genau zum Zustand claimed")
	}
	switch a.State {
	case OutboxRetryWait:
		if a.NextAttemptAt.IsZero() {
			return newError(CatState, "/next_attempt_at", "retry_wait braucht den Zeitpunkt des nächsten Versuchs")
		}
	case OutboxClaimed, OutboxSucceeded, OutboxNeedsAttention, OutboxCancelled:
		if !a.NextAttemptAt.IsZero() {
			return newError(CatState, "/next_attempt_at", "im Zustand %s gibt es keinen nächsten Versuch", a.State)
		}
	}
	if (a.State == OutboxSucceeded) != (a.ExternalRef != "") {
		return newError(CatState, "/external_ref", "eine externe Referenz gehört genau zum Zustand succeeded")
	}
	return nil
}

// ---- Klärung ----------------------------------------------------------------------------------------------------

// OutboxResolveAction ist der Weg, einen Klärungsbedarf (needs_attention) ausdrücklich zu beenden.
type OutboxResolveAction string

const (
	// ResolveRetry: erneut versuchen (pending, Zähler zurückgesetzt). Der Mensch verantwortet, dass das externe Objekt
	// nicht existiert oder der Adapter es erkennt.
	ResolveRetry OutboxResolveAction = "retry"
	// ResolveConfirm: als erledigt bestätigen, mit der externen Referenz des vorhandenen Objekts (succeeded).
	ResolveConfirm OutboxResolveAction = "confirm"
	// ResolveCancel: abbrechen (cancelled).
	ResolveCancel OutboxResolveAction = "cancel"
)

// OutboxResolution ist die ausdrückliche Klärung einer Aktion mit Klärungsbedarf. Akteur und Zeit stehen im Audit-Ereignis.
type OutboxResolution struct {
	Action OutboxResolveAction
	// ExternalRef ist bei ResolveConfirm Pflicht und sonst leer.
	ExternalRef string
	// Reason begründet die Klärung (Pflicht, höchstens 256 Bytes, ohne Steuerzeichen).
	Reason string
}

// Check prüft die Klärung.
func (r OutboxResolution) Check() error {
	switch r.Action {
	case ResolveRetry, ResolveCancel:
		if r.ExternalRef != "" {
			return newError(CatField, "/external_ref", "eine externe Referenz gehört nur zur Bestätigung")
		}
	case ResolveConfirm:
		if err := CheckExternalRef(r.ExternalRef); err != nil {
			return err
		}
	default:
		return newError(CatValue, "/action", "unbekannter Klärungsweg %q (retry, confirm oder cancel)", r.Action)
	}
	switch {
	case strings.TrimSpace(r.Reason) == "":
		return newError(CatField, "/reason", "eine Klärung braucht einen Grund")
	case len(r.Reason) > MaxOutboxReasonBytes || !plainText(r.Reason, false):
		return newError(CatValue, "/reason", "der Grund ist zu lang (höchstens %d Bytes) oder enthält Steuerzeichen", MaxOutboxReasonBytes)
	}
	return nil
}
