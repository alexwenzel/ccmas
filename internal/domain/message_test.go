package domain

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestDeliveryStateTransitions(t *testing.T) {
	cases := []struct {
		from, to DeliveryState
		want     bool
	}{
		{DeliveryPending, DeliveryDelivered, true},
		{DeliveryDelivered, DeliveryAcknowledged, true},
		// Wiederholte Zustellung einer zugestellten, nicht bestätigten Nachricht (zum Beispiel nach einem Neustart).
		{DeliveryDelivered, DeliveryDelivered, true},
		// Bestätigen ohne Zustellung gibt es nicht; ein Ausgangszustand bleibt nicht stehen.
		{DeliveryPending, DeliveryAcknowledged, false},
		{DeliveryPending, DeliveryPending, false},
		{DeliveryDelivered, DeliveryPending, false},
		// Bestätigt ist Ende: keine erneute Zustellung, keine zweite Bestätigung als Übergang.
		{DeliveryAcknowledged, DeliveryDelivered, false},
		{DeliveryAcknowledged, DeliveryAcknowledged, false},
		{DeliveryAcknowledged, DeliveryPending, false},
		{DeliveryState("lost"), DeliveryDelivered, false},
		{DeliveryPending, DeliveryState("lost"), false},
	}
	for _, c := range cases {
		if got := c.from.CanTransition(c.to); got != c.want {
			t.Errorf("%q -> %q = %v, erwartet %v", c.from, c.to, got, c.want)
		}
	}
	for _, s := range DeliveryStates() {
		if want := s == DeliveryAcknowledged; s.IsTerminal() != want {
			t.Errorf("IsTerminal(%q) = %v", s, s.IsTerminal())
		}
	}
	if DeliveryState("lost").IsTerminal() {
		t.Error("ein unbekannter Zustand ist nicht terminal")
	}
}

func TestMessageKinds(t *testing.T) {
	want := []string{"task_start", "task_progress", "task_result", "review_request", "dependency_request",
		"supervisor_decision", "agent_message", "question", "answer", "delegation_request"}
	kinds := MessageKinds()
	if len(kinds) != len(want) {
		t.Fatalf("%d Arten, erwartet %d", len(kinds), len(want))
	}
	for i, k := range kinds {
		if string(k) != want[i] || !k.Valid() {
			t.Errorf("Art %d = %q, erwartet %q", i, k, want[i])
		}
	}
	for _, bad := range []MessageKind{"", "Task_Start", "task-start", "result", "start"} {
		if bad.Valid() {
			t.Errorf("Art %q gilt als gültig", bad)
		}
	}
	// MessageKind ist nicht der Typ des Runner-Protokolls: dessen Werte sind keine Nachrichtenarten.
	for _, mt := range []MessageType{MsgStart, MsgResult} {
		if MessageKind(mt).Valid() {
			t.Errorf("Runner-Typ %q gilt als Nachrichtenart", mt)
		}
	}
}

func TestCheckAddress(t *testing.T) {
	for _, ok := range []string{"orchestrator", "supervisor", "cli", "agent:agent-1", "execution:exec-1", "agent:a.b_c", "execution:" + strings.Repeat("x", 100)} {
		if err := CheckAddress(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "worker", "Orchestrator", "orchestrator:1", "agent", "agent:", "agent: x", "agent:ä", "agent:-x", "execution:", "user:alex",
		"agent:a/b", "agent:a\nb", " cli", "cli ", "execution:" + strings.Repeat("x", 120)} {
		err := CheckAddress(bad)
		if err == nil {
			t.Errorf("%q gilt als Adresse", bad)
			continue
		}
		var ce *ContractError
		if !errors.As(err, &ce) {
			t.Errorf("%q: Fehler ist kein ContractError: %v", bad, err)
		}
	}
	if AgentAddress("a1") != "agent:a1" || ExecutionAddress("e1") != "execution:e1" {
		t.Error("Adressbildung")
	}
}

func validMessage() Message {
	return Message{
		MessageID: "m-1", ProjectID: "ccmas", WorkOrderID: "wo-1", ExecutionID: "exec-1", Attempt: 1,
		Sender: "execution:exec-1", Recipients: []string{"orchestrator"}, Kind: MessageTaskProgress,
		CorrelationID: "corr-1", CreatedAt: time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC),
		Payload: json.RawMessage(`{"percent":50,"note":"läuft"}`),
	}
}

func TestMessageCheck(t *testing.T) {
	if err := validMessage().Check(); err != nil {
		t.Fatalf("gültige Nachricht: %v", err)
	}
	// Ohne Zeit, Korrelation und Nutzdaten ist die Nachricht gültig (Zeit setzt die Persistenz); ohne Auftrag und
	// Ausführung nur, wo die Art das erlaubt (Agentennachricht der CLI).
	minimal := Message{MessageID: "m-2", ProjectID: "ccmas", Sender: "cli", Recipients: []string{"supervisor"}, Kind: MessageAgentMessage}
	if err := minimal.Check(); err != nil {
		t.Fatalf("minimale Nachricht: %v", err)
	}

	big := `{"x":"` + strings.Repeat("a", MaxMessagePayloadBytes) + `"}`
	cases := map[string]struct {
		mutate func(*Message)
		cat    Category
	}{
		"Nachrichten-ID":                {func(m *Message) { m.MessageID = "m 1" }, CatID},
		"Projekt":                       {func(m *Message) { m.ProjectID = "" }, CatID},
		"Auftrags-ID":                   {func(m *Message) { m.WorkOrderID = "w o" }, CatID},
		"Ausführung ohne Versuch":       {func(m *Message) { m.Attempt = 0 }, CatField},
		"Versuch ohne Ausführung":       {func(m *Message) { m.ExecutionID = "" }, CatField},
		"negativer Versuch":             {func(m *Message) { m.Attempt = -1 }, CatValue},
		"Ausführung ohne Auftrag":       {func(m *Message) { m.WorkOrderID = "" }, CatField},
		"Ausführungs-ID":                {func(m *Message) { m.ExecutionID = "e e" }, CatID},
		"Absender leer":                 {func(m *Message) { m.Sender = "" }, CatValue},
		"Absender Freitext":             {func(m *Message) { m.Sender = "der Worker" }, CatValue},
		"keine Empfänger":               {func(m *Message) { m.Recipients = nil }, CatField},
		"Empfänger Freitext":            {func(m *Message) { m.Recipients = []string{"alle"} }, CatValue},
		"doppelter Empfänger":           {func(m *Message) { m.Recipients = []string{"cli", "cli"} }, CatValue},
		"zu viele Empfänger":            {func(m *Message) { m.Recipients = manyRecipients(MaxMessageRecipients + 1) }, CatLimit},
		"Art unbekannt":                 {func(m *Message) { m.Kind = "task_done" }, CatMessageType},
		"Art leer":                      {func(m *Message) { m.Kind = "" }, CatMessageType},
		"Art des Runner-Protokolls":     {func(m *Message) { m.Kind = MessageKind(MsgResult) }, CatMessageType},
		"Korrelations-ID":               {func(m *Message) { m.CorrelationID = "c c" }, CatID},
		"Nutzdaten Array":               {func(m *Message) { m.Payload = json.RawMessage(`[1]`) }, CatSyntax},
		"Nutzdaten Text":                {func(m *Message) { m.Payload = json.RawMessage(`"x"`) }, CatSyntax},
		"Nutzdaten null":                {func(m *Message) { m.Payload = json.RawMessage(`null`) }, CatSyntax},
		"Nutzdaten kein JSON":           {func(m *Message) { m.Payload = json.RawMessage(`{`) }, CatSyntax},
		"Nutzdaten doppelter Schlüssel": {func(m *Message) { m.Payload = json.RawMessage(`{"a":1,"a":2}`) }, CatSyntax},
		"Nutzdaten Kommazahl":           {func(m *Message) { m.Payload = json.RawMessage(`{"a":1.5}`) }, CatValue},
		"Nutzdaten kein UTF-8":          {func(m *Message) { m.Payload = json.RawMessage("{\"a\":\"\xff\"}") }, CatSyntax},
		"Nutzdaten Folgedaten":          {func(m *Message) { m.Payload = json.RawMessage(`{} {}`) }, CatSyntax},
		"Nutzdaten zu groß":             {func(m *Message) { m.Payload = json.RawMessage(big) }, CatSize},
		"Delegation von der CLI":        {func(m *Message) { m.Kind, m.Sender = MessageDelegationRequest, "cli" }, CatValue},
		"Delegation an einen Agenten":   {func(m *Message) { m.Kind, m.Recipients = MessageDelegationRequest, []string{"agent:a1"} }, CatValue},
		"Delegation an mehrere":         {func(m *Message) { m.Kind, m.Recipients = MessageDelegationRequest, []string{"orchestrator", "cli"} }, CatValue},
	}
	for name, c := range cases {
		m := validMessage()
		c.mutate(&m)
		err := m.Check()
		var ce *ContractError
		if !errors.As(err, &ce) {
			t.Errorf("%s: kein ContractError (%v)", name, err)
			continue
		}
		if ce.Category() != c.cat {
			t.Errorf("%s: Klasse %q, erwartet %q (%v)", name, ce.Category(), c.cat, err)
		}
	}

	// Grenzen genau: so viele Empfänger und so viele Bytes sind noch erlaubt.
	m := validMessage()
	m.Recipients = manyRecipients(MaxMessageRecipients)
	if err := m.Check(); err != nil {
		t.Errorf("%d Empfänger: %v", MaxMessageRecipients, err)
	}
	m = validMessage()
	pad := MaxMessagePayloadBytes - len(`{"x":""}`)
	m.Payload = json.RawMessage(`{"x":"` + strings.Repeat("a", pad) + `"}`)
	if err := m.Check(); err != nil {
		t.Errorf("Nutzdaten an der Grenze: %v", err)
	}
	// Eine gültige Delegationsanforderung: Agent oder Ausführung an den Orchestrator.
	for _, sender := range []string{"agent:a1", "execution:exec-1"} {
		d := validMessage()
		d.Kind, d.Sender, d.Recipients = MessageDelegationRequest, sender, []string{AddressOrchestrator}
		if err := d.Check(); err != nil {
			t.Errorf("Delegation von %q: %v", sender, err)
		}
	}
}

// kindLinkCase ist ein gültiger Fall je Nachrichtenart samt den Pflichtbezügen, die fehlen dürfen oder nicht.
type kindLinkCase struct {
	kind      MessageKind
	sender    string
	recipient string
	needOrder bool // Auftrag Pflicht
	needExec  bool // Ausführung und Versuch Pflicht
}

func kindLinkCases() []kindLinkCase {
	return []kindLinkCase{
		{MessageTaskStart, AddressOrchestrator, "execution:exec-1", true, true},
		{MessageTaskProgress, "execution:exec-1", AddressOrchestrator, true, true},
		{MessageTaskResult, "execution:exec-1", AddressOrchestrator, true, true},
		{MessageDelegationRequest, "execution:exec-1", AddressOrchestrator, true, true},
		{MessageDelegationRequest, "agent:a1", AddressOrchestrator, true, true},
		{MessageReviewRequest, AddressOrchestrator, "agent:a1", true, false},
		{MessageDependencyRequest, "execution:exec-1", AddressOrchestrator, true, true}, // Ausführung wegen des Absenders
		{MessageQuestion, AddressOrchestrator, AddressCLI, true, false},
		{MessageAnswer, AddressCLI, AddressOrchestrator, true, false},
		{MessageSupervisorDecision, AddressSupervisor, AddressOrchestrator, false, false},
		{MessageAgentMessage, AddressCLI, AddressSupervisor, false, false},
		{MessageAgentMessage, "agent:a1", AddressSupervisor, true, true},
		{MessageAgentMessage, "execution:exec-1", AddressSupervisor, true, true},
	}
}

func (c kindLinkCase) message(withOrder, withExec bool) Message {
	m := Message{MessageID: "m-k", ProjectID: "ccmas", Sender: c.sender, Recipients: []string{c.recipient}, Kind: c.kind}
	if withOrder {
		m.WorkOrderID = "wo-1"
	}
	if withExec {
		m.WorkOrderID, m.ExecutionID, m.Attempt = "wo-1", "exec-1", 1
	}
	return m
}

// Je Art ein gültiger Fall mit allen Bezügen und je Pflichtbezug ein fehlender Fall (Spezifikation „Nachrichten“:
// Mindestinhalt ist Auftrag/Task und Ausführung).
func TestMessageRequiredLinksPerKind(t *testing.T) {
	covered := map[MessageKind]bool{}
	for _, c := range kindLinkCases() {
		covered[c.kind] = true
		name := string(c.kind) + " von " + c.sender
		if err := c.message(true, true).Check(); err != nil {
			t.Errorf("%s mit allen Bezügen: %v", name, err)
		}
		// Ohne Bezüge, nur mit Auftrag und ohne Auftrag mit Ausführung: ungültig genau dann, wenn Pflicht fehlt.
		for _, probe := range []struct {
			label             string
			withOrder, withEx bool
		}{{"ohne Bezüge", false, false}, {"nur Auftrag", true, false}} {
			err := c.message(probe.withOrder, probe.withEx).Check()
			mustFail := (c.needOrder && !probe.withOrder) || (c.needExec && !probe.withEx)
			var ce *ContractError
			switch {
			case mustFail && !errors.As(err, &ce):
				t.Errorf("%s %s: angenommen (%v)", name, probe.label, err)
			case mustFail && ce.Category() != CatField:
				t.Errorf("%s %s: Klasse %q, erwartet %q", name, probe.label, ce.Category(), CatField)
			case !mustFail && err != nil:
				t.Errorf("%s %s: %v", name, probe.label, err)
			}
		}
		// Ein fehlender Auftrag bei vorhandener Ausführung ist immer ungültig; ein fehlender Versuch auch.
		m := c.message(true, true)
		m.WorkOrderID = ""
		if err := m.Check(); err == nil {
			t.Errorf("%s ohne Auftrag, mit Ausführung: angenommen", name)
		}
		m = c.message(true, true)
		m.Attempt = 0
		if err := m.Check(); err == nil {
			t.Errorf("%s ohne Versuch: angenommen", name)
		}
	}
	for _, k := range MessageKinds() {
		if !covered[k] {
			t.Errorf("Art %q fehlt in den Fällen", k)
		}
	}
	// Die Pflichttabelle selbst.
	want := map[MessageKind]MessageLinks{
		MessageTaskStart: {true, true}, MessageTaskProgress: {true, true}, MessageTaskResult: {true, true},
		MessageDelegationRequest: {true, true}, MessageReviewRequest: {true, false}, MessageDependencyRequest: {true, false},
		MessageQuestion: {true, false}, MessageAnswer: {true, false}, MessageSupervisorDecision: {}, MessageAgentMessage: {},
	}
	for _, k := range MessageKinds() {
		if got, ok := want[k]; !ok || k.RequiredLinks() != got {
			t.Errorf("RequiredLinks(%q) = %+v, erwartet %+v", k, k.RequiredLinks(), got)
		}
	}
}

// Ist der Absender execution:<id>, nennt die Nachricht genau diese Ausführung.
func TestMessageExecutionSenderMustMatchExecution(t *testing.T) {
	for _, kind := range MessageKinds() {
		if kind == MessageDelegationRequest {
			continue
		}
		recipient := AddressOrchestrator
		ok := validMessage()
		ok.Kind, ok.Recipients = kind, []string{recipient}
		if err := ok.Check(); err != nil {
			t.Errorf("%s mit eigener Ausführung: %v", kind, err)
		}
		foreign := ok
		foreign.ExecutionID = "exec-2"
		err := foreign.Check()
		var ce *ContractError
		if !errors.As(err, &ce) || ce.Category() != CatValue || ce.Location != "/execution_id" {
			t.Errorf("%s mit fremder Ausführung: %v", kind, err)
		}
		none := ok
		none.ExecutionID, none.Attempt = "", 0
		if err := none.Check(); err == nil {
			t.Errorf("%s von einer Ausführung ohne Ausführung: angenommen", kind)
		}
	}
	// Auch bei einem Absender, der nur sendet: die Delegation.
	d := validMessage()
	d.Kind, d.Recipients, d.ExecutionID = MessageDelegationRequest, []string{AddressOrchestrator}, "exec-9"
	if err := d.Check(); err == nil {
		t.Error("Delegation mit fremder Ausführung angenommen")
	}
	// Ein Agent als Absender darf für die Ausführung stehen, die er trägt (die Zuordnung prüft die Persistenz).
	a := validMessage()
	a.Sender = "agent:a1"
	if err := a.Check(); err != nil {
		t.Errorf("Agent mit Ausführung: %v", err)
	}
}

func manyRecipients(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = "agent:a" + strings.Repeat("x", i%3) + string(rune('a'+i%26)) + string(rune('a'+i/26))
	}
	return out
}

func TestMessageCanonicalPayload(t *testing.T) {
	a := validMessage()
	a.Payload = json.RawMessage(" {\n \"b\": 2,\t\"a\": [1, {\"z\":null,\"y\":\"<&>\"}] } ")
	b := validMessage()
	b.Payload = json.RawMessage(`{"a":[1,{"y":"<&>","z":null}],"b":2}`)
	ca, err := a.CanonicalPayload()
	if err != nil {
		t.Fatal(err)
	}
	cb, err := b.CanonicalPayload()
	if err != nil {
		t.Fatal(err)
	}
	if string(ca) != string(cb) || string(ca) != `{"a":[1,{"y":"<&>","z":null}],"b":2}` {
		t.Errorf("kanonisch = %s / %s", ca, cb)
	}
	// Große Ganzzahlen bleiben unverändert (keine Umwandlung in Gleitkommazahlen).
	c := validMessage()
	c.Payload = json.RawMessage(`{"n":9007199254740993}`)
	if cc, err := c.CanonicalPayload(); err != nil || string(cc) != `{"n":9007199254740993}` {
		t.Errorf("Ganzzahl = %s (%v)", cc, err)
	}
	empty := validMessage()
	empty.Payload = nil
	if ce, err := empty.CanonicalPayload(); err != nil || string(ce) != `{}` {
		t.Errorf("leere Nutzdaten = %s (%v)", ce, err)
	}
	bad := validMessage()
	bad.Payload = json.RawMessage(`[]`)
	if _, err := bad.CanonicalPayload(); err == nil {
		t.Error("Array als Nutzdaten angenommen")
	}
}

func TestMessageAuditKinds(t *testing.T) {
	seen := map[string]bool{}
	for _, k := range MessageAuditKinds() {
		if !auditKindPattern.MatchString(k) || seen[k] || !strings.HasPrefix(k, "message.") {
			t.Errorf("Ereignisart %q ungültig oder doppelt", k)
		}
		seen[k] = true
	}
	for _, k := range []string{AuditMessageCreated, AuditMessageDelivered, AuditMessageRedelivered, AuditMessageAcknowledged} {
		if !seen[k] {
			t.Errorf("%q fehlt in MessageAuditKinds", k)
		}
	}
}

func TestCheckOutboxKind(t *testing.T) {
	for _, ok := range []string{"asana.comment", "asana.create_ticket", "github.create_pr", "github.merge_pr", "a.b", "x1.y2_z"} {
		if err := CheckOutboxKind(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "asana", "asana_comment", "Asana.Comment", ".comment", "asana.", "a.b.c", "1a.b", "a.1b", "a b.c", "a.b-c", "asana.comment\n",
		"ä.b", "a." + strings.Repeat("x", 70)} {
		if err := CheckOutboxKind(bad); err == nil {
			t.Errorf("%q gilt als Aktionsart", bad)
		}
	}
}
