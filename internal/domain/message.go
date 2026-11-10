package domain

import (
	"bytes"
	"encoding/json"
	"sort"
	"strings"
	"time"
)

// ---- Zustellung -------------------------------------------------------------

// DeliveryState ist der Zustand der Zustellung einer Nachricht an einen Empfänger (Tabelle message_deliveries; je
// Empfänger eine Zeile, jede mit eigenem Zustand).
//
// Übergangstabelle (CC-45):
//
//	pending   -> delivered       erste Zustellung; zählt als Versuch
//	delivered -> delivered       wiederholte Zustellung einer zugestellten, nicht bestätigten Nachricht (zum Beispiel nach
//	                             einem Neustart); zählt als weiterer Versuch, der Zustand geht dabei nicht verloren
//	delivered -> acknowledged    Quittung des Empfängers
//
// Begründung: Zustellung und Verarbeitung sind quittierbar und wiederholbar (Spezifikation „Nachrichten“). „Zugestellt“
// ist deshalb ein Zustand, den der Empfänger durch Bestätigen beendet, und der bis dahin ausdrücklich wiederholbar ist:
// Wer nach einem Absturz nicht weiß, ob die Verarbeitung gelang, bekommt die Nachricht erneut, mit höherem Versuchszähler.
// Ohne vorherige Zustellung gibt es keine Quittung (pending -> acknowledged ist verboten): Eine Bestätigung ohne
// Auslieferung deutet auf einen Protokollfehler hin und darf keine Nachricht stillschweigend schließen. Aus „bestätigt“
// gibt es keinen Weg zurück; eine bestätigte Nachricht wird nicht erneut ausgeliefert. Ein Rückfall nach pending gibt es nicht.
// Die Datenbank erzwingt dieselbe Tabelle durch Trigger (Migration 003).
type DeliveryState string

const (
	DeliveryPending      DeliveryState = "pending"
	DeliveryDelivered    DeliveryState = "delivered"
	DeliveryAcknowledged DeliveryState = "acknowledged"
)

// DeliveryStates liefert alle Zustände.
func DeliveryStates() []DeliveryState {
	return []DeliveryState{DeliveryPending, DeliveryDelivered, DeliveryAcknowledged}
}

// Valid meldet, ob s ein bekannter Zustand ist.
func (s DeliveryState) Valid() bool { return contains(DeliveryStates(), s) }

var deliveryTransitions = map[DeliveryState][]DeliveryState{
	DeliveryPending:   {DeliveryDelivered},
	DeliveryDelivered: {DeliveryDelivered, DeliveryAcknowledged},
}

// CanTransition meldet, ob der Übergang s -> to erlaubt ist (siehe Typkommentar).
func (s DeliveryState) CanTransition(to DeliveryState) bool {
	return contains(deliveryTransitions[s], to)
}

// IsTerminal meldet, ob s ein Endzustand ist (nur „bestätigt“).
func (s DeliveryState) IsTerminal() bool { return s == DeliveryAcknowledged }

// MessageDelivery ist die Zustellung einer Nachricht an einen Empfänger.
type MessageDelivery struct {
	MessageID string        `json:"message_id"`
	Recipient string        `json:"recipient"`
	State     DeliveryState `json:"state"`
	// Attempts zählt die Zustellungen (erste Zustellung und jede Wiederholung).
	Attempts       int       `json:"attempts"`
	DeliveredAt    time.Time `json:"delivered_at,omitzero"`
	AcknowledgedAt time.Time `json:"acknowledged_at,omitzero"`
}

// Ereignisarten des Auditverlaufs für Nachrichten (Ebene AuditLevelMessage). Sie stehen bewusst hier und nicht in
// audit.go; MessageAuditKinds liefert sie.
const (
	AuditMessageCreated      = "message.created"
	AuditMessageDelivered    = "message.delivered"
	AuditMessageRedelivered  = "message.redelivered"
	AuditMessageAcknowledged = "message.acknowledged"
)

// MessageAuditKinds liefert die Ereignisarten der Nachrichten.
func MessageAuditKinds() []string {
	return []string{AuditMessageCreated, AuditMessageDelivered, AuditMessageRedelivered, AuditMessageAcknowledged}
}

// ---- Art der Nachricht -------------------------------------------------------

// MessageKind ist die Art einer Nachricht der internen Kommunikation (Spezifikation „Nachrichten“). Er ist nicht der
// MessageType des Runner-Protokolls; dessen Werte sind keine Nachrichtenarten.
type MessageKind string

const (
	MessageTaskStart          MessageKind = "task_start"
	MessageTaskProgress       MessageKind = "task_progress"
	MessageTaskResult         MessageKind = "task_result"
	MessageReviewRequest      MessageKind = "review_request"
	MessageDependencyRequest  MessageKind = "dependency_request"
	MessageSupervisorDecision MessageKind = "supervisor_decision"
	MessageAgentMessage       MessageKind = "agent_message"
	MessageQuestion           MessageKind = "question"
	MessageAnswer             MessageKind = "answer"
	// MessageDelegationRequest ist die Anforderung einer Delegation durch einen Agenten. Sie ist nur eine Nachricht an
	// den Orchestrator: Er allein startet und reserviert die tatsächliche Ausführung.
	MessageDelegationRequest MessageKind = "delegation_request"
)

// MessageKinds liefert alle Arten. Die Liste ist die der Datenbankprüfung auf messages.type (Migration 003).
func MessageKinds() []MessageKind {
	return []MessageKind{
		MessageTaskStart, MessageTaskProgress, MessageTaskResult, MessageReviewRequest, MessageDependencyRequest,
		MessageSupervisorDecision, MessageAgentMessage, MessageQuestion, MessageAnswer, MessageDelegationRequest,
	}
}

// Valid meldet, ob k eine bekannte Art ist.
func (k MessageKind) Valid() bool { return contains(MessageKinds(), k) }

// ---- Adressen ------------------------------------------------------------------

// Adressen von Absender und Empfänger: kein Freitext. Gültig sind die festen Namen orchestrator, supervisor und cli
// sowie agent:<id> und execution:<id> mit einer ID im Format von CheckID. Eine Adresse hat höchstens 128 Zeichen
// (Spalte der Datenbank).
const (
	AddressOrchestrator = "orchestrator"
	AddressSupervisor   = "supervisor"
	AddressCLI          = "cli"

	agentAddressPrefix     = "agent:"
	executionAddressPrefix = "execution:"
	maxAddressLen          = 128
)

// AgentAddress bildet die Adresse eines Agenten.
func AgentAddress(agentID string) string { return agentAddressPrefix + agentID }

// ExecutionAddress bildet die Adresse einer Ausführung.
func ExecutionAddress(executionID string) string { return executionAddressPrefix + executionID }

// CheckAddress prüft das Format einer Adresse.
func CheckAddress(a string) error {
	switch a {
	case AddressOrchestrator, AddressSupervisor, AddressCLI:
		return nil
	}
	for _, prefix := range []string{agentAddressPrefix, executionAddressPrefix} {
		if id, ok := strings.CutPrefix(a, prefix); ok {
			if len(a) > maxAddressLen {
				return newError(CatValue, "", "Adresse hat %d Zeichen, erlaubt sind %d", len(a), maxAddressLen)
			}
			if err := CheckID(id); err != nil {
				return newError(CatValue, "", "Adresse %q: %v", a, err)
			}
			return nil
		}
	}
	return newError(CatValue, "", "Adresse %q ist weder orchestrator, supervisor, cli, agent:<id> noch execution:<id>", a)
}

// ---- Nachricht -------------------------------------------------------------------

// Grenzen einer Nachricht.
const (
	// MaxMessagePayloadBytes begrenzt die Nutzdaten (Länge des übergebenen JSON).
	MaxMessagePayloadBytes = 64 << 10
	// MaxMessageRecipients begrenzt die Empfänger einer Nachricht.
	MaxMessageRecipients = 16
)

// Message ist die persistente Nachricht der internen Kommunikation (Spezifikation „Nachrichten“): ID, Projekt,
// Auftrag, Ausführung und Versuch (beide oder keins), Absender, Empfänger, Art, Korrelations-ID, Zeitstempel und
// strukturierte Nutzdaten. Jeder Empfänger hat eine eigene Zustellung (MessageDelivery). Nachrichten gehören nicht in
// den ProjectSnapshot.
//
// Die Nutzdaten sind ein JSON-Objekt (ohne doppelte Schlüssel, Ganzzahlen statt Kommazahlen, höchstens
// MaxMessagePayloadBytes). Sie enthalten keine Zugangsdaten und keine Prompts; das kann kein Wertetest beweisen, deshalb
// schreibt das Audit-Ereignis die Nutzdaten nie mit.
type Message struct {
	MessageID string `json:"message_id"`
	ProjectID string `json:"project_id"`
	// WorkOrderID ist der Auftrag, zu dem die Nachricht gehört (optional).
	WorkOrderID string `json:"work_order_id,omitempty"`
	// ExecutionID und Attempt nennen den Versuch, zu dem die Nachricht gehört (beide oder keins). Mit einer Ausführung
	// ist der Auftrag Pflicht; die Persistenz prüft, dass die Ausführung zu diesem Auftrag gehört.
	ExecutionID string `json:"execution_id,omitempty"`
	Attempt     int    `json:"attempt,omitempty"`
	Sender      string `json:"sender"`
	// Recipients sind die Empfänger (1 bis MaxMessageRecipients, ohne Dubletten); die Persistenz liefert sie sortiert.
	Recipients    []string        `json:"recipients"`
	Kind          MessageKind     `json:"kind"`
	CorrelationID string          `json:"correlation_id,omitempty"`
	CreatedAt     time.Time       `json:"created_at,omitzero"` // leer: Zeit der Transaktion
	Payload       json.RawMessage `json:"payload,omitempty"`   // leer: {}
}

// Check prüft Form und Grenzen der Nachricht. Ob die Ausführung zum Auftrag gehört und ob Projekt, Auftrag und
// Versuch existieren, prüft die Persistenz.
func (m Message) Check() error {
	for _, id := range []string{m.MessageID, m.ProjectID} {
		if err := CheckID(id); err != nil {
			return err
		}
	}
	for _, id := range []string{m.WorkOrderID, m.ExecutionID, m.CorrelationID} {
		if id != "" {
			if err := CheckID(id); err != nil {
				return err
			}
		}
	}
	switch {
	case m.ExecutionID != "" && m.Attempt < 1 && m.Attempt != 0:
		return newError(CatValue, "/attempt", "Versuch %d ungültig", m.Attempt)
	case m.ExecutionID != "" && m.Attempt == 0:
		return newError(CatField, "/attempt", "eine Nachricht mit Ausführung nennt auch den Versuch")
	case m.ExecutionID == "" && m.Attempt != 0:
		return newError(CatField, "/execution_id", "ein Versuch gehört zu einer Ausführung")
	case m.ExecutionID != "" && m.WorkOrderID == "":
		return newError(CatField, "/work_order_id", "eine Nachricht mit Ausführung nennt auch den Auftrag")
	}
	if err := CheckAddress(m.Sender); err != nil {
		return wrapLocation(err, "/sender")
	}
	switch {
	case len(m.Recipients) == 0:
		return newError(CatField, "/recipients", "eine Nachricht braucht mindestens einen Empfänger")
	case len(m.Recipients) > MaxMessageRecipients:
		return newError(CatLimit, "/recipients", "%d Empfänger, erlaubt sind %d", len(m.Recipients), MaxMessageRecipients)
	}
	seen := map[string]bool{}
	for _, r := range m.Recipients {
		if err := CheckAddress(r); err != nil {
			return wrapLocation(err, "/recipients")
		}
		if seen[r] {
			return newError(CatValue, "/recipients", "Empfänger %q kommt doppelt vor", r)
		}
		seen[r] = true
	}
	if !m.Kind.Valid() {
		return newError(CatMessageType, "/kind", "unbekannte Nachrichtenart %q", m.Kind)
	}
	if m.Kind == MessageDelegationRequest {
		if !strings.HasPrefix(m.Sender, agentAddressPrefix) && !strings.HasPrefix(m.Sender, executionAddressPrefix) {
			return newError(CatValue, "/sender", "eine Delegation fordert ein Agent oder eine Ausführung an, nicht %q", m.Sender)
		}
		if len(m.Recipients) != 1 || m.Recipients[0] != AddressOrchestrator {
			return newError(CatValue, "/recipients", "eine Delegation geht allein an den Orchestrator: nur er startet und reserviert")
		}
	}
	_, err := m.CanonicalPayload()
	return err
}

// wrapLocation setzt die Fundstelle eines ContractError, sofern er keine hat.
func wrapLocation(err error, loc string) error {
	if ce, ok := err.(*ContractError); ok && ce.Location == "" {
		cp := *ce
		cp.Location = loc
		return &cp
	}
	return err
}

// CanonicalPayload prüft die Nutzdaten und liefert ihre kanonische Form: kompakt, Schlüssel sortiert, ohne Maskierung
// von <, > und &, Zahlen unverändert. Leere Nutzdaten sind {}. Gleiche Nutzdaten ergeben dieselben Bytes; darauf
// beruht der Vergleich beim erneuten Anlegen einer Nachricht.
func (m Message) CanonicalPayload() ([]byte, error) {
	raw := bytes.TrimSpace(m.Payload)
	if len(raw) == 0 {
		return []byte("{}"), nil
	}
	if len(m.Payload) > MaxMessagePayloadBytes {
		return nil, newError(CatSize, "/payload", "Nutzdaten haben %d Bytes, erlaubt sind %d", len(m.Payload), MaxMessagePayloadBytes)
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

// SortedRecipients liefert die Empfänger sortiert (Kopie).
func (m Message) SortedRecipients() []string {
	out := append([]string(nil), m.Recipients...)
	sort.Strings(out)
	return out
}
