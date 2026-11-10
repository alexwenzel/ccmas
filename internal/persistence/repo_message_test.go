package persistence

import (
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/alexwenzel/ccmas/internal/domain"
	"github.com/alexwenzel/ccmas/internal/supervisor"
)

// msgStore liefert einen migrierten Store mit Projekt, Auftrag wo-1 samt Ausführung exec-1 (Versuch 1) und Auftrag wo-2
// samt Ausführung exec-2 (Versuch 1).
func msgStore(t *testing.T) *Store {
	t.Helper()
	s := newStore(t)
	seedBase(t, s)
	mustUpdate(t, s, func(tx *Tx) error {
		_, err := tx.CreateWorkOrder(WorkOrderRecord{WorkOrder: fxWorkOrder(t, "wo-2"), GoalID: "ms-1", Kind: supervisor.OrderBug})
		return err
	})
	seedExecutionFor(t, s, "wo-1", "exec-1", domain.RoleImplementer)
	seedExecutionFor(t, s, "wo-2", "exec-2", domain.RoleImplementer)
	return s
}

func msgFixture(id string, recipients ...string) domain.Message {
	if len(recipients) == 0 {
		recipients = []string{domain.AddressOrchestrator}
	}
	return domain.Message{
		MessageID: id, ProjectID: projectID, WorkOrderID: "wo-1", ExecutionID: "exec-1", Attempt: 1,
		Sender: domain.ExecutionAddress("exec-1"), Recipients: recipients, Kind: domain.MessageTaskProgress,
		CorrelationID: "corr-" + id, Payload: json.RawMessage(`{"percent":50,"note":"läuft"}`),
	}
}

func msgCreate(t *testing.T, s *Store, m domain.Message) UpdateResult {
	t.Helper()
	return mustUpdate(t, s, func(tx *Tx) error { return tx.CreateMessage(m) })
}

func msgTransition(t *testing.T, s *Store, id, recipient string, from, to domain.DeliveryState) domain.MessageDelivery {
	t.Helper()
	var d domain.MessageDelivery
	mustUpdate(t, s, func(tx *Tx) error {
		var err error
		d, err = tx.TransitionDelivery(projectID, id, recipient, from, to)
		return err
	})
	return d
}

func msgCount(t *testing.T, s *Store, table string) int {
	t.Helper()
	var n int
	must(t, s.r.QueryRowContext(ctxT(t), "SELECT count(*) FROM "+table).Scan(&n))
	return n
}

func msgEvents(t *testing.T, s *Store) []domain.AuditEvent {
	t.Helper()
	var evs []domain.AuditEvent
	mustView(t, s, func(r *ReadTx) error {
		var err error
		evs, err = r.Events(projectID, EventFilter{Level: domain.AuditLevelMessage})
		return err
	})
	return evs
}

func msgPending(t *testing.T, s *Store, recipient string, limit int) []PendingDelivery {
	t.Helper()
	var out []PendingDelivery
	mustView(t, s, func(r *ReadTx) error {
		var err error
		out, err = r.PendingDeliveries(projectID, recipient, limit)
		return err
	})
	return out
}

func msgIDs(ps []PendingDelivery) []string {
	var out []string
	for _, p := range ps {
		out = append(out, p.Message.MessageID)
	}
	return out
}

func TestCreateMessage(t *testing.T) {
	s := msgStore(t)
	revBefore := revisionOf(t, s, projectID)
	m := msgFixture("m-1", "supervisor", "orchestrator")
	m.Payload = json.RawMessage(` { "note": "läuft", "percent": 50, "token_hint": "geheim-marker" } `)
	res := msgCreate(t, s, m)
	if len(res.EventIDs) != 1 || len(res.Revisions) != 0 {
		t.Fatalf("Ergebnis = %+v: ein Ereignis, keine Revisionserhöhung erwartet", res)
	}
	if got := revisionOf(t, s, projectID); got != revBefore {
		t.Fatalf("Revision %d -> %d: Nachrichten sind Journal", revBefore, got)
	}
	mustView(t, s, func(r *ReadTx) error {
		got, err := r.Message(projectID, "m-1")
		if err != nil {
			return err
		}
		if got.CreatedAt.IsZero() || got.ProjectID != projectID || got.WorkOrderID != "wo-1" || got.ExecutionID != "exec-1" || got.Attempt != 1 ||
			got.Sender != "execution:exec-1" || got.Kind != domain.MessageTaskProgress || got.CorrelationID != "corr-m-1" {
			t.Errorf("Nachricht = %+v", got)
		}
		if !slices.Equal(got.Recipients, []string{"orchestrator", "supervisor"}) {
			t.Errorf("Empfänger = %v (sortiert erwartet)", got.Recipients)
		}
		if string(got.Payload) != `{"note":"läuft","percent":50,"token_hint":"geheim-marker"}` {
			t.Errorf("Nutzdaten = %s (kanonisch erwartet)", got.Payload)
		}
		ds, err := r.MessageDeliveries(projectID, "m-1")
		if err != nil {
			return err
		}
		if len(ds) != 2 || ds[0].Recipient != "orchestrator" || ds[1].Recipient != "supervisor" {
			t.Fatalf("Zustellungen = %+v", ds)
		}
		for _, d := range ds {
			if d.State != domain.DeliveryPending || d.Attempts != 0 || !d.DeliveredAt.IsZero() || !d.AcknowledgedAt.IsZero() || d.MessageID != "m-1" {
				t.Errorf("Zustellung = %+v", d)
			}
		}
		return nil
	})
	evs := msgEvents(t, s)
	if len(evs) != 1 {
		t.Fatalf("%d Ereignisse", len(evs))
	}
	e := evs[0]
	if e.Kind != domain.AuditMessageCreated || e.Level != domain.AuditLevelMessage || e.EntityType != "message" || e.EntityID != "m-1" ||
		e.Actor != "tester" || e.StateRevision != revBefore || e.ToState != "pending" {
		t.Errorf("Ereignis = %+v", e)
	}
	// Nutzdaten gehören nicht in den Auditverlauf.
	for k, v := range e.Details {
		if strings.Contains(v, "geheim-marker") || strings.Contains(v, "läuft") {
			t.Errorf("Detail %s=%q enthält Nutzdaten", k, v)
		}
	}
	// Das Zeitfeld bleibt, wenn der Aufrufer es setzt.
	m2 := msgFixture("m-2")
	m2.CreatedAt = e.OccurredAt.AddDate(0, 0, -1)
	msgCreate(t, s, m2)
	mustView(t, s, func(r *ReadTx) error {
		got, err := r.Message(projectID, "m-2")
		if err == nil && !got.CreatedAt.Equal(m2.CreatedAt) {
			t.Errorf("CreatedAt = %v, erwartet %v", got.CreatedAt, m2.CreatedAt)
		}
		return err
	})
	// Ohne Auftrag und Ausführung sind nur die Arten möglich, die keine Pflichtbezüge haben (siehe RequiredLinks).
	msgCreate(t, s, domain.Message{MessageID: "m-free", ProjectID: projectID, Sender: "cli", Recipients: []string{"supervisor"}, Kind: domain.MessageAgentMessage})
	msgCreate(t, s, domain.Message{MessageID: "m-decision", ProjectID: projectID, Sender: "supervisor", Recipients: []string{"orchestrator"}, Kind: domain.MessageSupervisorDecision})
	// Eine Frage nennt den Auftrag; die Ausführung nur, wo es sie gibt.
	msgCreate(t, s, domain.Message{MessageID: "m-question", ProjectID: projectID, WorkOrderID: "wo-1", Sender: "orchestrator", Recipients: []string{"cli"}, Kind: domain.MessageQuestion})
}

func TestCreateMessageSameIDIsNoopOrConflict(t *testing.T) {
	s := msgStore(t)
	seedOtherProject(t, s)
	m := msgFixture("m-1", "orchestrator", "supervisor")
	msgCreate(t, s, m)
	msgTransition(t, s, "m-1", "orchestrator", domain.DeliveryPending, domain.DeliveryDelivered)
	events := countEvents(t, s)

	// Gleicher Inhalt: Reihenfolge der Empfänger, Leerraum und Schlüsselfolge der Nutzdaten spielen keine Rolle,
	// der Zustellzustand auch nicht. Kein Ereignis, keine Änderung.
	same := m
	same.Recipients = []string{"supervisor", "orchestrator"}
	same.Payload = json.RawMessage(`{"note": "läuft",  "percent": 50}`)
	res := msgCreate(t, s, same)
	if len(res.EventIDs) != 0 || countEvents(t, s) != events {
		t.Errorf("gleicher Inhalt schrieb Ereignisse: %+v", res)
	}
	mustView(t, s, func(r *ReadTx) error {
		d, err := r.MessageDelivery(projectID, "m-1", "orchestrator")
		if err == nil && (d.State != domain.DeliveryDelivered || d.Attempts != 1) {
			t.Errorf("Zustellung nach No-op = %+v", d)
		}
		return err
	})

	mutations := map[string]func(*domain.Message){
		"Nutzdaten":     func(x *domain.Message) { x.Payload = json.RawMessage(`{"percent":51}`) },
		"Art":           func(x *domain.Message) { x.Kind = domain.MessageTaskResult },
		"Absender":      func(x *domain.Message) { x.Sender = "agent:agent-1" },
		"Empfänger":     func(x *domain.Message) { x.Recipients = []string{"orchestrator"} },
		"Empfänger neu": func(x *domain.Message) { x.Recipients = []string{"orchestrator", "cli"} },
		"Korrelation":   func(x *domain.Message) { x.CorrelationID = "anders" },
		"Auftrag": func(x *domain.Message) {
			x.Sender, x.WorkOrderID, x.ExecutionID, x.Attempt = domain.ExecutionAddress("exec-2"), "wo-2", "exec-2", 1
		},
		"Versuch": func(x *domain.Message) { x.Attempt = 2 },
	}
	for name, mut := range mutations {
		other := m
		other.Recipients = slices.Clone(m.Recipients)
		mut(&other)
		_, err := s.Update(ctxT(t), "tester", func(tx *Tx) error { return tx.CreateMessage(other) })
		if CodeOf(err) != CodeConflict {
			t.Errorf("%s: %v, erwartet conflict", name, err)
		}
	}
	// Eine gesetzte, abweichende Zeit ist ein anderer Inhalt.
	timed := m
	timed.CreatedAt = s.now()
	_, err := s.Update(ctxT(t), "tester", func(tx *Tx) error { return tx.CreateMessage(timed) })
	wantCode(t, err, CodeConflict)

	// Dieselbe ID in einem anderen Projekt: Konflikt (message_id ist global eindeutig), das Projekt der Nachricht bleibt.
	cross := domain.Message{MessageID: "m-1", ProjectID: otherProject, Sender: "cli", Recipients: []string{"supervisor"}, Kind: domain.MessageAgentMessage}
	_, err = s.Update(ctxT(t), "tester", func(tx *Tx) error { return tx.CreateMessage(cross) })
	wantCode(t, err, CodeConflict)
	if countEvents(t, s) != events || msgCount(t, s, "messages") != 1 || msgCount(t, s, "message_deliveries") != 2 {
		t.Error("ein abgewiesener Aufruf hat geschrieben")
	}
	mustView(t, s, func(r *ReadTx) error {
		got, err := r.Message(projectID, "m-1")
		if err == nil && got.Kind != domain.MessageTaskProgress {
			t.Errorf("Nachricht verändert: %+v", got)
		}
		_, err2 := r.Message(otherProject, "m-1")
		wantCode(t, err2, CodeNotFound)
		return err
	})
}

// TestCreateMessageCorrelationIsEnforced: Gehört eine Nachricht zu einem Versuch, muss dessen Ausführung zum genannten
// Auftrag gehören. Das prüft Go; die Datenbank bindet nur das Projekt (Fremdschlüssel). Abgewiesen wird ohne Schreibwirkung.
func TestCreateMessageCorrelationIsEnforced(t *testing.T) {
	s := msgStore(t)
	seedOtherProject(t, s)
	events := countEvents(t, s)

	wrongOrder := msgFixture("m-wrong")
	wrongOrder.WorkOrderID = "wo-2" // exec-1 gehört zu wo-1
	cases := map[string]struct {
		msg  domain.Message
		code Code
	}{
		"Versuch eines anderen Auftrags": {wrongOrder, CodeInvalid},
		"unbekannter Versuch":            {func() domain.Message { m := msgFixture("m-a"); m.Attempt = 2; return m }(), CodeNotFound},
		"unbekannte Ausführung": {func() domain.Message {
			m := msgFixture("m-b")
			m.ExecutionID, m.Sender = "exec-9", "execution:exec-9"
			return m
		}(), CodeNotFound},
		"unbekannter Auftrag": {func() domain.Message {
			m := msgFixture("m-c")
			m.Kind, m.Sender, m.WorkOrderID, m.ExecutionID, m.Attempt = domain.MessageReviewRequest, domain.AddressOrchestrator, "wo-9", "", 0
			return m
		}(), CodeNotFound},
		"Ausführung eines anderen Projekts": {func() domain.Message {
			m := msgFixture("m-d")
			m.ExecutionID, m.Sender = "exec-o", "execution:exec-o"
			return m
		}(), CodeNotFound},
		"Auftrag eines anderen Projekts": {func() domain.Message {
			m := msgFixture("m-e")
			m.Kind, m.Sender, m.WorkOrderID, m.ExecutionID, m.Attempt = domain.MessageReviewRequest, domain.AddressOrchestrator, "wo-o", "", 0
			return m
		}(), CodeNotFound},
		"unbekanntes Projekt": {func() domain.Message { m := msgFixture("m-f"); m.ProjectID = "gibt-es-nicht"; return m }(), CodeNotFound},
	}
	for name, c := range cases {
		_, err := s.Update(ctxT(t), "tester", func(tx *Tx) error { return tx.CreateMessage(c.msg) })
		if CodeOf(err) != c.code {
			t.Errorf("%s: %v, erwartet %s", name, err, c.code)
		}
		if n := msgCount(t, s, "messages") + msgCount(t, s, "message_deliveries"); n != 0 {
			t.Fatalf("%s: %d Zeilen geschrieben", name, n)
		}
	}
	if countEvents(t, s) != events {
		t.Errorf("abgewiesene Nachrichten schrieben Ereignisse")
	}
	// Die Datenbank allein erzwingt nur das Projekt, nicht den Auftrag der Ausführung: Dieser Bezug bleibt Sache von Go.
	must(t, execSQL(t, s, `INSERT INTO messages (message_id, project_id, work_order_id, execution_id, attempt, sender, recipient, type, payload_json, created_at)
		VALUES ('m-raw', 'ccmas', 'wo-2', 'exec-1', 1, 'cli', 'cli', 'task_result', '{}', ?)`, ts))
	wantFKViolation(t, s, "Nachricht: Ausführung eines anderen Projekts", `INSERT INTO messages (message_id, project_id, work_order_id, execution_id, attempt, sender, recipient,
		type, payload_json, created_at) VALUES ('m-raw2', 'ccmas', 'wo-1', 'exec-o', 1, 'cli', 'cli', 'task_result', '{}', ?)`, ts)
	wantFKViolation(t, s, "Nachricht: Auftrag eines anderen Projekts", `INSERT INTO messages (message_id, project_id, work_order_id, sender, recipient,
		type, payload_json, created_at) VALUES ('m-raw3', 'ccmas', 'wo-o', 'cli', 'cli', 'task_result', '{}', ?)`, ts)

	// Gegenprobe: stimmt der Bezug, wird die Nachricht angenommen.
	ok := msgFixture("m-ok")
	ok.WorkOrderID, ok.ExecutionID, ok.Sender = "wo-2", "exec-2", domain.ExecutionAddress("exec-2")
	msgCreate(t, s, ok)
}

func TestCreateMessageInvalidInputWritesNothing(t *testing.T) {
	s := msgStore(t)
	events := countEvents(t, s)
	cases := map[string]func(*domain.Message){
		"Art unbekannt":       func(m *domain.Message) { m.Kind = "task_done" },
		"Art des Runner-Typs": func(m *domain.Message) { m.Kind = "result" },
		"Absender Freitext":   func(m *domain.Message) { m.Sender = "der Worker" },
		"Absender worker":     func(m *domain.Message) { m.Sender = "worker" },
		"Empfänger Freitext":  func(m *domain.Message) { m.Recipients = []string{"alle Agenten"} },
		"kein Empfänger":      func(m *domain.Message) { m.Recipients = nil },
		"doppelter Empfänger": func(m *domain.Message) { m.Recipients = []string{"cli", "cli"} },
		"Nutzdaten Array":     func(m *domain.Message) { m.Payload = json.RawMessage(`[]`) },
		"Nutzdaten zu groß": func(m *domain.Message) {
			m.Payload = json.RawMessage(`{"x":"` + strings.Repeat("a", domain.MaxMessagePayloadBytes) + `"}`)
		},
		"Ausführung ohne Versuch": func(m *domain.Message) { m.Attempt = 0 },
		// Pflichtbezüge je Art (domain.MessageKind.RequiredLinks): fehlt einer, wird nichts geschrieben.
		"Fortschritt ohne Bezüge": func(m *domain.Message) { m.WorkOrderID, m.ExecutionID, m.Attempt, m.Sender = "", "", 0, "cli" },
		"Ergebnis ohne Ausführung": func(m *domain.Message) {
			m.Kind, m.Sender, m.ExecutionID, m.Attempt = domain.MessageTaskResult, "cli", "", 0
		},
		"Start ohne Auftrag": func(m *domain.Message) {
			m.Kind, m.Sender, m.WorkOrderID, m.ExecutionID, m.Attempt = domain.MessageTaskStart, "orchestrator", "", "", 0
		},
		"Delegation ohne Ausführung": func(m *domain.Message) {
			m.Kind, m.Sender, m.ExecutionID, m.Attempt = domain.MessageDelegationRequest, "agent:agent-1", "", 0
			m.Recipients = []string{domain.AddressOrchestrator}
		},
		"Frage ohne Auftrag": func(m *domain.Message) {
			m.Kind, m.Sender, m.WorkOrderID, m.ExecutionID, m.Attempt = domain.MessageQuestion, "orchestrator", "", "", 0
		},
		"Review-Anforderung ohne Auftrag": func(m *domain.Message) {
			m.Kind, m.Sender, m.WorkOrderID, m.ExecutionID, m.Attempt = domain.MessageReviewRequest, "orchestrator", "", "", 0
		},
		"Abhängigkeitswunsch ohne Auftrag": func(m *domain.Message) {
			m.Kind, m.Sender, m.WorkOrderID, m.ExecutionID, m.Attempt = domain.MessageDependencyRequest, "orchestrator", "", "", 0
		},
		"Antwort ohne Auftrag": func(m *domain.Message) {
			m.Kind, m.Sender, m.WorkOrderID, m.ExecutionID, m.Attempt = domain.MessageAnswer, "cli", "", "", 0
		},
		"Agentennachricht eines Agenten ohne Ausführung": func(m *domain.Message) {
			m.Kind, m.Sender, m.WorkOrderID, m.ExecutionID, m.Attempt = domain.MessageAgentMessage, "agent:agent-1", "", "", 0
		},
		"Absender execution:exec-1 mit fremder Ausführung": func(m *domain.Message) {
			m.WorkOrderID, m.ExecutionID = "wo-2", "exec-2" // gültiger Versuch, aber nicht der des Absenders
		},
		"ungültige ID": func(m *domain.Message) { m.MessageID = "m 1" },
		"Delegation an Agent": func(m *domain.Message) {
			m.Kind, m.Recipients = domain.MessageDelegationRequest, []string{"agent:agent-1"}
		},
	}
	for name, mut := range cases {
		m := msgFixture("m-bad")
		mut(&m)
		_, err := s.Update(ctxT(t), "tester", func(tx *Tx) error { return tx.CreateMessage(m) })
		wantCode(t, err, CodeInvalid)
		if CodeOf(err) == CodeInvalid {
			var ce *domain.ContractError
			if !errorsAs(err, &ce) {
				t.Errorf("%s: Ursache ist kein ContractError", name)
			}
		}
	}
	if msgCount(t, s, "messages") != 0 || msgCount(t, s, "message_deliveries") != 0 || countEvents(t, s) != events {
		t.Error("ungültige Nachrichten haben geschrieben")
	}
}

func TestMessageTypeIsCheckedByTheDatabase(t *testing.T) {
	s := msgStore(t)
	insert := func(id, typ string) error {
		return execSQL(t, s, `INSERT INTO messages (message_id, project_id, sender, recipient, type, payload_json, created_at)
			VALUES (?, 'ccmas', 'cli', 'supervisor', ?, '{}', ?)`, id, typ, ts)
	}
	for i, k := range domain.MessageKinds() {
		if err := insert("ok-"+string(rune('a'+i)), string(k)); err != nil {
			t.Errorf("Art %q: %v", k, err)
		}
	}
	for _, bad := range []string{"", "result", "TASK_START", "task_start ", "task_done", "start"} {
		err := insert("bad", bad)
		if err == nil {
			t.Errorf("Art %q angenommen", bad)
			continue
		}
		wantCode(t, mapErr(err), CodeConstraint)
	}
	// Eine Änderung weist die Datenbank ebenfalls ab (Nachrichten sind unveränderlich).
	wantCode(t, mapErr(execSQL(t, s, "UPDATE messages SET type = 'task_done' WHERE message_id = 'ok-a'")), CodeConstraint)

	// Die Liste im Trigger entspricht genau der Go-Liste.
	var ddl string
	must(t, s.r.QueryRowContext(ctxT(t), "SELECT sql FROM sqlite_master WHERE type = 'trigger' AND name = 'messages_type_insert'").Scan(&ddl))
	var got []string
	for _, m := range regexp.MustCompile(`'([a-z_]+)'`).FindAllStringSubmatch(ddl, -1) {
		got = append(got, m[1])
	}
	// Die Meldung des Triggers enthält Leerzeichen und passt nicht auf das Muster der Listenelemente.
	want := strs(domain.MessageKinds())
	slices.Sort(want)
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("Trigger-Liste %v\nGo-Liste      %v", got, want)
	}
}

func TestOutboxKindIsCheckedByTheDatabase(t *testing.T) {
	s := msgStore(t)
	n := 0
	insert := func(kind string) error {
		n++
		return execSQL(t, s, `INSERT INTO outbox (action_id, project_id, operation_key, kind, payload_json, state, created_at, updated_at)
			VALUES (?, 'ccmas', ?, ?, '{}', 'pending', ?, ?)`, "a"+itoa(int64(n)), "ccmas/"+kind+"/k"+itoa(int64(n)), kind, ts, ts)
	}
	for _, ok := range []string{"asana.comment", "asana.create_ticket", "github.create_pr", "a.b", "x1.y2_z"} {
		if err := insert(ok); err != nil {
			t.Errorf("Art %q: %v", ok, err)
		}
		if domain.CheckOutboxKind(ok) != nil {
			t.Errorf("Go weist %q ab, die Datenbank nicht", ok)
		}
	}
	for _, bad := range []string{"", "asana", "asana_comment", "Asana.Comment", ".comment", "asana.", "a.b.c", "1a.b", "a.1b", "a b.c", "a.b-c", "a.b\n", "ä.b", "a." + strings.Repeat("x", 70)} {
		err := insert(bad)
		if err == nil {
			t.Errorf("Art %q angenommen", bad)
		} else {
			wantCode(t, mapErr(err), CodeConstraint)
		}
		if domain.CheckOutboxKind(bad) == nil {
			t.Errorf("Go lässt %q zu, die Datenbank nicht", bad)
		}
	}
	// Die Art einer Aktion ändert sich nie (Migration 003, outbox_immutable); auch eine ungültige Art wird abgewiesen.
	wantCode(t, mapErr(execSQL(t, s, "UPDATE outbox SET kind = 'kaputt' WHERE action_id = 'a1'")), CodeConstraint)
	wantCode(t, mapErr(execSQL(t, s, "UPDATE outbox SET kind = 'asana.update' WHERE action_id = 'a1'")), CodeConstraint)
}

func TestTransitionDelivery(t *testing.T) {
	s := msgStore(t)
	revBefore := revisionOf(t, s, projectID)
	msgCreate(t, s, msgFixture("m-1", "orchestrator", "supervisor"))

	d := msgTransition(t, s, "m-1", "orchestrator", domain.DeliveryPending, domain.DeliveryDelivered)
	if d.State != domain.DeliveryDelivered || d.Attempts != 1 || d.DeliveredAt.IsZero() || !d.AcknowledgedAt.IsZero() {
		t.Fatalf("erste Zustellung = %+v", d)
	}
	first := d.DeliveredAt
	// Wiederholte Zustellung: gleicher Zustand, Versuchszähler steigt, Zeit rückt vor.
	d = msgTransition(t, s, "m-1", "orchestrator", domain.DeliveryDelivered, domain.DeliveryDelivered)
	if d.State != domain.DeliveryDelivered || d.Attempts != 2 || !d.DeliveredAt.After(first) {
		t.Fatalf("zweite Zustellung = %+v", d)
	}
	d = msgTransition(t, s, "m-1", "orchestrator", domain.DeliveryDelivered, domain.DeliveryAcknowledged)
	if d.State != domain.DeliveryAcknowledged || d.Attempts != 2 || d.AcknowledgedAt.IsZero() || d.DeliveredAt.IsZero() {
		t.Fatalf("Bestätigung = %+v", d)
	}
	// Der andere Empfänger ist unberührt.
	mustView(t, s, func(r *ReadTx) error {
		o, err := r.MessageDelivery(projectID, "m-1", "supervisor")
		if err == nil && (o.State != domain.DeliveryPending || o.Attempts != 0) {
			t.Errorf("anderer Empfänger = %+v", o)
		}
		return err
	})

	evs := msgEvents(t, s)
	var kinds, from, to []string
	for _, e := range evs {
		kinds = append(kinds, e.Kind)
		from, to = append(from, e.FromState), append(to, e.ToState)
		if e.StateRevision != revBefore {
			t.Errorf("Ereignis %s trägt Revision %d, erwartet %d", e.Kind, e.StateRevision, revBefore)
		}
	}
	if want := []string{"message.created", "message.delivered", "message.redelivered", "message.acknowledged"}; !slices.Equal(kinds, want) {
		t.Fatalf("Ereignisarten = %v", kinds)
	}
	if want := []string{"", "pending", "delivered", "delivered"}; !slices.Equal(from, want) {
		t.Errorf("von = %v", from)
	}
	if want := []string{"pending", "delivered", "delivered", "acknowledged"}; !slices.Equal(to, want) {
		t.Errorf("nach = %v", to)
	}
	for _, e := range evs[1:] {
		if e.EntityType != "message_delivery" || e.EntityID != "m-1/orchestrator" || e.Details["recipient"] != "orchestrator" {
			t.Errorf("Ereignis = %+v", e)
		}
	}
	if evs[2].Details["attempts"] != "2" {
		t.Errorf("Details der Wiederholung = %v", evs[2].Details)
	}
	if got := revisionOf(t, s, projectID); got != revBefore {
		t.Errorf("Revision %d -> %d", revBefore, got)
	}
}

func TestTransitionDeliveryRejectsWrongTransitions(t *testing.T) {
	s := msgStore(t)
	msgCreate(t, s, msgFixture("m-1"))
	try := func(id, rcpt string, from, to domain.DeliveryState) error {
		_, err := s.Update(ctxT(t), "tester", func(tx *Tx) error {
			_, err := tx.TransitionDelivery(projectID, id, rcpt, from, to)
			return err
		})
		return err
	}
	events := countEvents(t, s)
	// Nach der Übergangstabelle nicht erlaubt: state.
	wantCode(t, try("m-1", "orchestrator", domain.DeliveryPending, domain.DeliveryAcknowledged), CodeState)
	wantCode(t, try("m-1", "orchestrator", domain.DeliveryPending, domain.DeliveryPending), CodeState)
	wantCode(t, try("m-1", "orchestrator", domain.DeliveryAcknowledged, domain.DeliveryDelivered), CodeState)
	wantCode(t, try("m-1", "orchestrator", domain.DeliveryState("lost"), domain.DeliveryDelivered), CodeState)
	// Erlaubt, aber der Ist-Zustand ist ein anderer: conflict.
	wantCode(t, try("m-1", "orchestrator", domain.DeliveryDelivered, domain.DeliveryAcknowledged), CodeConflict)
	wantCode(t, try("m-1", "orchestrator", domain.DeliveryDelivered, domain.DeliveryDelivered), CodeConflict)
	// Unbekannt: not_found; ungültige IDs: invalid.
	wantCode(t, try("m-9", "orchestrator", domain.DeliveryPending, domain.DeliveryDelivered), CodeNotFound)
	wantCode(t, try("m-1", "cli", domain.DeliveryPending, domain.DeliveryDelivered), CodeNotFound)
	wantCode(t, try("m 1", "orchestrator", domain.DeliveryPending, domain.DeliveryDelivered), CodeInvalid)
	wantCode(t, try("m-1", "Freitext", domain.DeliveryPending, domain.DeliveryDelivered), CodeInvalid)
	if countEvents(t, s) != events {
		t.Error("abgewiesene Übergänge schrieben Ereignisse")
	}

	// Bestätigt ist Ende.
	msgTransition(t, s, "m-1", "orchestrator", domain.DeliveryPending, domain.DeliveryDelivered)
	msgTransition(t, s, "m-1", "orchestrator", domain.DeliveryDelivered, domain.DeliveryAcknowledged)
	wantCode(t, try("m-1", "orchestrator", domain.DeliveryAcknowledged, domain.DeliveryDelivered), CodeState)
	wantCode(t, try("m-1", "orchestrator", domain.DeliveryDelivered, domain.DeliveryAcknowledged), CodeConflict)

	// Fremdes Projekt: die Zustellung ist dort unsichtbar.
	seedOtherProject(t, s)
	_, err := s.Update(ctxT(t), "tester", func(tx *Tx) error {
		_, err := tx.TransitionDelivery(otherProject, "m-1", "orchestrator", domain.DeliveryPending, domain.DeliveryDelivered)
		return err
	})
	wantCode(t, err, CodeNotFound)
}

func TestTransitionDeliveryRollsBackWithItsEvent(t *testing.T) {
	s := msgStore(t)
	msgCreate(t, s, msgFixture("m-1"))
	events := countEvents(t, s)
	_, err := s.Update(ctxT(t), "tester", func(tx *Tx) error {
		if _, err := tx.TransitionDelivery(projectID, "m-1", "orchestrator", domain.DeliveryPending, domain.DeliveryDelivered); err != nil {
			return err
		}
		return errTest
	})
	if err != errTest {
		t.Fatalf("Fehler = %v", err)
	}
	mustView(t, s, func(r *ReadTx) error {
		d, err := r.MessageDelivery(projectID, "m-1", "orchestrator")
		if err == nil && (d.State != domain.DeliveryPending || d.Attempts != 0) {
			t.Errorf("nach Rollback = %+v", d)
		}
		return err
	})
	if countEvents(t, s) != events {
		t.Error("Ereignis blieb nach Rollback")
	}
}

func TestPendingDeliveries(t *testing.T) {
	s := msgStore(t)
	seedOtherProject(t, s)
	// Eine Transaktion, gleiche Zeit: feste Reihenfolge nach Zeit, dann ID.
	mustUpdate(t, s, func(tx *Tx) error {
		for _, id := range []string{"m-c", "m-a", "m-b"} {
			if err := tx.CreateMessage(msgFixture(id, "orchestrator", "supervisor")); err != nil {
				return err
			}
		}
		return nil
	})
	late := msgFixture("m-0", "orchestrator")
	late.CreatedAt = s.now().AddDate(0, 0, 1)
	early := msgFixture("m-z", "orchestrator")
	early.CreatedAt = s.now().AddDate(0, 0, -1)
	msgCreate(t, s, late)
	msgCreate(t, s, early)
	msgCreate(t, s, domain.Message{MessageID: "m-other", ProjectID: otherProject, Sender: "cli", Recipients: []string{"orchestrator"}, Kind: domain.MessageAgentMessage})

	if got := msgIDs(msgPending(t, s, "orchestrator", 0)); !slices.Equal(got, []string{"m-z", "m-a", "m-b", "m-c", "m-0"}) {
		t.Errorf("ausstehend (orchestrator) = %v", got)
	}
	if got := msgIDs(msgPending(t, s, "supervisor", 0)); !slices.Equal(got, []string{"m-a", "m-b", "m-c"}) {
		t.Errorf("ausstehend (supervisor) = %v", got)
	}
	if got := msgPending(t, s, "cli", 0); len(got) != 0 {
		t.Errorf("ausstehend (cli) = %v", msgIDs(got))
	}
	if got := msgIDs(msgPending(t, s, "orchestrator", 2)); !slices.Equal(got, []string{"m-z", "m-a"}) {
		t.Errorf("mit Limit = %v", got)
	}
	// Zugestellte, nicht bestätigte Nachrichten bleiben ausstehend; bestätigte nicht.
	msgTransition(t, s, "m-a", "orchestrator", domain.DeliveryPending, domain.DeliveryDelivered)
	msgTransition(t, s, "m-b", "orchestrator", domain.DeliveryPending, domain.DeliveryDelivered)
	msgTransition(t, s, "m-b", "orchestrator", domain.DeliveryDelivered, domain.DeliveryAcknowledged)
	ps := msgPending(t, s, "orchestrator", 0)
	if got := msgIDs(ps); !slices.Equal(got, []string{"m-z", "m-a", "m-c", "m-0"}) {
		t.Errorf("nach Zustellung/Bestätigung = %v", got)
	}
	for _, p := range ps {
		if p.Delivery.MessageID != p.Message.MessageID || p.Delivery.Recipient != "orchestrator" || p.Delivery.State == domain.DeliveryAcknowledged {
			t.Errorf("Eintrag = %+v", p)
		}
		if p.Message.MessageID == "m-a" && (p.Delivery.State != domain.DeliveryDelivered || p.Delivery.Attempts != 1) {
			t.Errorf("m-a = %+v", p.Delivery)
		}
		if !slices.Equal(p.Message.Recipients, p.Message.SortedRecipients()) {
			t.Errorf("Empfänger nicht sortiert: %v", p.Message.Recipients)
		}
	}
	// Das andere Projekt sieht nur seine Nachricht.
	mustView(t, s, func(r *ReadTx) error {
		o, err := r.PendingDeliveries(otherProject, "orchestrator", 0)
		if err != nil || len(o) != 1 || o[0].Message.MessageID != "m-other" {
			t.Errorf("anderes Projekt = %+v (%v)", o, err)
		}
		if _, err := r.PendingDeliveries(projectID, "Freitext", 0); CodeOf(err) != CodeInvalid {
			t.Errorf("Freitext als Empfänger: %v", err)
		}
		return nil
	})
}

func TestMessageRecipientsAreAcknowledgedIndependently(t *testing.T) {
	s := msgStore(t)
	msgCreate(t, s, msgFixture("m-1", "orchestrator", "supervisor", "cli"))
	msgTransition(t, s, "m-1", "orchestrator", domain.DeliveryPending, domain.DeliveryDelivered)
	msgTransition(t, s, "m-1", "orchestrator", domain.DeliveryDelivered, domain.DeliveryAcknowledged)
	msgTransition(t, s, "m-1", "supervisor", domain.DeliveryPending, domain.DeliveryDelivered)
	if n := len(msgPending(t, s, "orchestrator", 0)); n != 0 {
		t.Errorf("orchestrator hat bestätigt, %d ausstehend", n)
	}
	if got := msgPending(t, s, "supervisor", 0); len(got) != 1 || got[0].Delivery.State != domain.DeliveryDelivered {
		t.Errorf("supervisor = %+v", got)
	}
	if got := msgPending(t, s, "cli", 0); len(got) != 1 || got[0].Delivery.State != domain.DeliveryPending || got[0].Delivery.Attempts != 0 {
		t.Errorf("cli = %+v", got)
	}
	mustView(t, s, func(r *ReadTx) error {
		ds, err := r.MessageDeliveries(projectID, "m-1")
		if err != nil || len(ds) != 3 {
			t.Fatalf("Zustellungen = %+v (%v)", ds, err)
		}
		states := map[string]domain.DeliveryState{}
		for _, d := range ds {
			states[d.Recipient] = d.State
		}
		want := map[string]domain.DeliveryState{"orchestrator": domain.DeliveryAcknowledged, "supervisor": domain.DeliveryDelivered, "cli": domain.DeliveryPending}
		for k, v := range want {
			if states[k] != v {
				t.Errorf("%s = %q, erwartet %q", k, states[k], v)
			}
		}
		return nil
	})
}

func TestMessageDeliveriesSurviveRestart(t *testing.T) {
	s := msgStore(t)
	msgCreate(t, s, msgFixture("m-1", "orchestrator", "supervisor"))
	msgCreate(t, s, msgFixture("m-2"))
	msgTransition(t, s, "m-1", "orchestrator", domain.DeliveryPending, domain.DeliveryDelivered)
	msgTransition(t, s, "m-1", "supervisor", domain.DeliveryPending, domain.DeliveryDelivered)
	msgTransition(t, s, "m-1", "supervisor", domain.DeliveryDelivered, domain.DeliveryAcknowledged)
	before := msgPending(t, s, "orchestrator", 0)

	s = closeAndReopen(t, s)
	after := msgPending(t, s, "orchestrator", 0)
	if len(after) != 2 || !slices.Equal(msgIDs(before), msgIDs(after)) {
		t.Fatalf("vorher %v, nachher %v", msgIDs(before), msgIDs(after))
	}
	if after[0].Delivery.State != domain.DeliveryDelivered || after[0].Delivery.Attempts != 1 || after[1].Delivery.State != domain.DeliveryPending {
		t.Errorf("Zustellungen nach Neustart = %+v / %+v", after[0].Delivery, after[1].Delivery)
	}
	if got := msgPending(t, s, "supervisor", 0); len(got) != 0 {
		t.Errorf("bestätigte Nachricht nach Neustart ausstehend: %v", msgIDs(got))
	}
	// Die Wiederholung nach dem Neustart zählt weiter.
	d := msgTransition(t, s, "m-1", "orchestrator", domain.DeliveryDelivered, domain.DeliveryDelivered)
	if d.Attempts != 2 {
		t.Errorf("Versuche = %d", d.Attempts)
	}
}

func TestMessagesDoNotChangeStateRevision(t *testing.T) {
	s := msgStore(t)
	rev := revisionOf(t, s, projectID)
	res := mustUpdate(t, s, func(tx *Tx) error {
		if err := tx.CreateMessage(msgFixture("m-1", "orchestrator", "supervisor")); err != nil {
			return err
		}
		for _, r := range []string{"orchestrator", "supervisor"} {
			if _, err := tx.TransitionDelivery(projectID, "m-1", r, domain.DeliveryPending, domain.DeliveryDelivered); err != nil {
				return err
			}
			if _, err := tx.TransitionDelivery(projectID, "m-1", r, domain.DeliveryDelivered, domain.DeliveryAcknowledged); err != nil {
				return err
			}
		}
		return nil
	})
	if len(res.Revisions) != 0 || len(res.EventIDs) != 5 {
		t.Errorf("Ergebnis = %+v: keine Revision, fünf Ereignisse erwartet", res)
	}
	if got := revisionOf(t, s, projectID); got != rev {
		t.Errorf("Revision %d -> %d", rev, got)
	}
	// Planungszustand in derselben Transaktion erhöht sie weiterhin genau einmal.
	res = mustUpdate(t, s, func(tx *Tx) error {
		if err := tx.CreateMessage(msgFixture("m-2")); err != nil {
			return err
		}
		return tx.TransitionWorkOrder(projectID, "wo-1", domain.WOWaiting, domain.WOInProgress)
	})
	if res.Revisions[projectID] != rev+1 {
		t.Errorf("Revisionen = %v", res.Revisions)
	}
}

// TestDeliveryTriggers: die Datenbank erzwingt die Übergangstabelle der Zustellung auch gegen direktes SQL.
func TestDeliveryTriggers(t *testing.T) {
	s := msgStore(t)
	msgCreate(t, s, msgFixture("m-1", "orchestrator", "supervisor"))
	set := func(recipient, assign string) error {
		return execSQL(t, s, "UPDATE message_deliveries SET "+assign+" WHERE message_id = 'm-1' AND recipient = '"+recipient+"'")
	}
	wantReject := func(name string, err error) {
		t.Helper()
		if err == nil {
			t.Errorf("%s: angenommen", name)
			return
		}
		wantCode(t, mapErr(err), CodeConstraint)
	}
	// Neue Zeilen beginnen als pending mit 0 Versuchen und ohne Zeiten.
	insert := func(id, state string, attempts int, deliveredAt, ackAt any) error {
		return execSQL(t, s, `INSERT INTO message_deliveries (message_id, recipient, state, attempts, delivered_at, acknowledged_at) VALUES (?, ?, ?, ?, ?, ?)`,
			"m-1", id, state, attempts, deliveredAt, ackAt)
	}
	wantReject("Einfügen als delivered", insert("cli", "delivered", 1, ts, nil))
	wantReject("Einfügen mit Versuchen", insert("cli", "pending", 1, nil, nil))
	wantReject("Einfügen mit Zeit", insert("cli", "pending", 0, ts, nil))
	if err := insert("cli", "pending", 0, nil, nil); err != nil {
		t.Errorf("Einfügen als pending: %v", err)
	}

	wantReject("pending -> acknowledged", set("orchestrator", "state = 'acknowledged', delivered_at = '"+ts+"', acknowledged_at = '"+ts+"', attempts = 1"))
	wantReject("delivered ohne Zeit", set("orchestrator", "state = 'delivered', attempts = 1"))
	wantReject("delivered ohne Versuch", set("orchestrator", "state = 'delivered', delivered_at = '"+ts+"'"))
	wantReject("Versuche ohne Zustandswechsel", set("orchestrator", "attempts = 1"))
	if err := set("orchestrator", "state = 'delivered', delivered_at = '"+ts+"', attempts = 1"); err != nil {
		t.Fatalf("pending -> delivered: %v", err)
	}
	wantReject("Rückfall nach pending", set("orchestrator", "state = 'pending', delivered_at = NULL, attempts = 0"))
	wantReject("Wiederholung ohne höheren Zähler", set("orchestrator", "state = 'delivered'"))
	wantReject("Zähler sinkt", set("orchestrator", "state = 'delivered', attempts = 0"))
	wantReject("bestätigt ohne Zeit", set("orchestrator", "state = 'acknowledged'"))
	if err := set("orchestrator", "state = 'delivered', attempts = 2, delivered_at = '"+ts+"'"); err != nil {
		t.Errorf("Wiederholung: %v", err)
	}
	wantReject("Empfänger ändern", set("orchestrator", "recipient = 'cli2'"))
	wantReject("Nachricht ändern", set("orchestrator", "message_id = 'm-2'"))
	if err := set("orchestrator", "state = 'acknowledged', acknowledged_at = '"+ts+"'"); err != nil {
		t.Errorf("Bestätigung: %v", err)
	}
	wantReject("bestätigt -> delivered", set("orchestrator", "state = 'delivered', attempts = 3, delivered_at = '"+ts+"', acknowledged_at = NULL"))
	wantReject("bestätigt -> pending", set("orchestrator", "state = 'pending'"))
	wantReject("Bestätigungszeit ändern", set("orchestrator", "acknowledged_at = '2026-11-11T00:00:00.000000000Z'"))

	// Nachrichten sind unveränderlich; das Nutzdatenfeld inbegriffen.
	wantReject("Nachricht ändern: Nutzdaten", execSQL(t, s, `UPDATE messages SET payload_json = '{"x":1}' WHERE message_id = 'm-1'`))
	wantReject("Nachricht ändern: Absender", execSQL(t, s, `UPDATE messages SET sender = 'cli' WHERE message_id = 'm-1'`))
}

func TestMigration003IsIdempotent(t *testing.T) {
	s := msgStore(t)
	ms := Migrations()
	var found bool
	for _, m := range ms {
		found = found || m.Version == 3
	}
	if !found {
		t.Fatalf("Migration 003 fehlt: %+v", ms)
	}
	msgCreate(t, s, msgFixture("m-1"))
	ctx := ctxT(t)
	before, err := s.AppliedMigrations(ctx)
	must(t, err)
	must(t, s.Migrate(ctx))
	must(t, s.Migrate(ctx))
	after, err := s.AppliedMigrations(ctx)
	must(t, err)
	if !slices.Equal(after, before) {
		t.Errorf("Historie nach erneutem Migrate: %+v, vorher %+v", after, before)
	}
	if v, _ := s.SchemaVersion(ctx); v != ms[len(ms)-1].Version {
		t.Errorf("user_version = %d", v)
	}
	var has003 bool
	for _, a := range after {
		has003 = has003 || (a.Version == 3 && a.Name == "message_delivery_checks")
	}
	if !has003 {
		t.Errorf("003 nicht in der Historie: %+v", after)
	}
	// Der Datenbestand bleibt bei erneutem Migrate unverändert.
	if msgCount(t, s, "messages") != 1 {
		t.Error("Nachricht verschwunden")
	}
	// Eine migrierte Datenbank mit Nachrichten und Zustellungen hat bei Inspect keinen Befund.
	msgTransition(t, s, "m-1", "orchestrator", domain.DeliveryPending, domain.DeliveryDelivered)
	must(t, s.Close())
	rep := inspect(t, s.Path())
	wantNoFindings(t, rep)
	if !rep.Integrity.Checked || rep.Integrity.QuickCheck != "ok" || rep.Integrity.ForeignKeyViolations != 0 || len(rep.Schema.Pending) != 0 {
		t.Errorf("Bericht: %+v", rep)
	}
}

var errTest = &Error{Code: CodeInternal, Message: "Testfehler"}

func errorsAs(err error, target any) bool { return errors.As(err, target) }

// TestCreateMessageEveryKindWithItsRequiredLinks: Jede Art wird mit ihren Pflichtbezügen angenommen; ohne sie bleibt die
// Datenbank unverändert (die Prüfung steht in domain.Message.Check, hier belegt am Weg durch die Persistenz).
func TestCreateMessageEveryKindWithItsRequiredLinks(t *testing.T) {
	s := msgStore(t)
	senders := map[domain.MessageKind]string{
		domain.MessageTaskStart: domain.AddressOrchestrator, domain.MessageTaskProgress: domain.ExecutionAddress("exec-1"),
		domain.MessageTaskResult: domain.ExecutionAddress("exec-1"), domain.MessageDelegationRequest: domain.AgentAddress("agent-1"),
		domain.MessageReviewRequest: domain.AddressOrchestrator, domain.MessageDependencyRequest: domain.ExecutionAddress("exec-1"),
		domain.MessageSupervisorDecision: domain.AddressSupervisor, domain.MessageAgentMessage: domain.AddressCLI,
		domain.MessageQuestion: domain.AddressOrchestrator, domain.MessageAnswer: domain.AddressCLI,
	}
	for _, kind := range domain.MessageKinds() {
		sender, ok := senders[kind]
		if !ok {
			t.Fatalf("Art %q ohne Fall", kind)
		}
		m := msgFixture("m-" + string(kind))
		m.Kind, m.Sender = kind, sender
		if kind == domain.MessageDelegationRequest {
			m.Recipients = []string{domain.AddressOrchestrator}
		}
		msgCreate(t, s, m)

		before := msgCount(t, s, "messages")
		links := m.RequiredLinks()
		for label, strip := range map[string]func(*domain.Message){
			"Auftrag":    func(x *domain.Message) { x.WorkOrderID, x.ExecutionID, x.Attempt = "", "", 0 },
			"Ausführung": func(x *domain.Message) { x.ExecutionID, x.Attempt = "", 0 },
		} {
			if (label == "Auftrag" && !links.WorkOrder) || (label == "Ausführung" && !links.Execution) {
				continue
			}
			bad := m
			bad.MessageID = "m-bad-" + string(kind)
			strip(&bad)
			_, err := s.Update(ctxT(t), "tester", func(tx *Tx) error { return tx.CreateMessage(bad) })
			wantCode(t, err, CodeInvalid)
		}
		if msgCount(t, s, "messages") != before {
			t.Errorf("%s: eine Nachricht ohne Pflichtbezug wurde geschrieben", kind)
		}
	}
}
