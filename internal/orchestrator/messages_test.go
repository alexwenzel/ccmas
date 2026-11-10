package orchestrator

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/alexwenzel/ccmas/internal/domain"
	"github.com/alexwenzel/ccmas/internal/persistence"
	"github.com/alexwenzel/ccmas/internal/persistence/persistencetest"
)

const msgProject = persistencetest.ProjectID

// msgClock ist eine Uhr, die bei jedem Aufruf eine Sekunde weiterläuft (nebenläufig nutzbar).
type msgClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *msgClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(time.Second)
	return c.t
}

func msgCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func msgOpen(t *testing.T, path string, clock *msgClock) *persistence.Store {
	t.Helper()
	s, err := persistence.Open(msgCtx(t), path, persistence.WithClock(clock.now))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// msgTestStore legt eine echte, migrierte SQLite-Datei in t.TempDir() an und bestückt sie über die Repositories
// (Projekt ccmas, Aufträge wo-1 bis wo-3, Ausführung exec-1 mit Versuch 1 zu wo-2).
func msgTestStore(t *testing.T) (*persistence.Store, *msgClock) {
	t.Helper()
	clock := &msgClock{t: time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)}
	s := msgOpen(t, filepath.Join(t.TempDir(), "state", "ccmas.db"), clock)
	if err := s.Migrate(msgCtx(t)); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	persistencetest.Seed(t, s)
	return s, clock
}

// msgRestart schließt den Store und öffnet dieselbe Datei neu (Neustart des Dienstes).
func msgRestart(t *testing.T, s *persistence.Store, clock *msgClock) *persistence.Store {
	t.Helper()
	path := s.Path()
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return msgOpen(t, path, clock)
}

func msgFixture(id string, recipients ...string) domain.Message {
	if len(recipients) == 0 {
		recipients = []string{domain.AddressOrchestrator}
	}
	return domain.Message{
		MessageID: id, ProjectID: msgProject, WorkOrderID: "wo-2", ExecutionID: "exec-1", Attempt: 1,
		Sender: domain.ExecutionAddress("exec-1"), Recipients: recipients, Kind: domain.MessageTaskResult,
		Payload: json.RawMessage(`{"outcome":"success"}`),
	}
}

func msgSend(t *testing.T, m *Messages, msg domain.Message) bool {
	t.Helper()
	created, err := m.Send(msgCtx(t), msg)
	if err != nil {
		t.Fatalf("Send(%s): %v", msg.MessageID, err)
	}
	return created
}

func msgDeliver(t *testing.T, m *Messages, id, recipient string) DeliveredMessage {
	t.Helper()
	d, err := m.Deliver(msgCtx(t), msgProject, id, recipient)
	if err != nil {
		t.Fatalf("Deliver(%s, %s): %v", id, recipient, err)
	}
	return d
}

func msgAck(t *testing.T, m *Messages, id, recipient string) bool {
	t.Helper()
	changed, err := m.Acknowledge(msgCtx(t), msgProject, id, recipient)
	if err != nil {
		t.Fatalf("Acknowledge(%s, %s): %v", id, recipient, err)
	}
	return changed
}

func msgPending(t *testing.T, m *Messages, recipient string) []persistence.PendingDelivery {
	t.Helper()
	ps, err := m.Pending(msgCtx(t), msgProject, recipient, 0)
	if err != nil {
		t.Fatalf("Pending(%s): %v", recipient, err)
	}
	return ps
}

func msgSummary(ps []persistence.PendingDelivery) []string {
	var out []string
	for _, p := range ps {
		out = append(out, p.Message.MessageID+":"+string(p.Delivery.State)+":"+string(rune('0'+p.Delivery.Attempts)))
	}
	return out
}

// msgEventKinds liefert die Arten der Nachrichten-Ereignisse in Schreibreihenfolge.
func msgEventKinds(t *testing.T, s *persistence.Store) []string {
	t.Helper()
	var kinds []string
	err := s.View(msgCtx(t), func(r *persistence.ReadTx) error {
		evs, err := r.Events(msgProject, persistence.EventFilter{Level: domain.AuditLevelMessage})
		for _, e := range evs {
			kinds = append(kinds, e.Kind)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return kinds
}

func msgRevision(t *testing.T, s *persistence.Store) int64 {
	t.Helper()
	var rev int64
	err := s.View(msgCtx(t), func(r *persistence.ReadTx) error {
		var err error
		rev, err = r.Revision(msgProject)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return rev
}

// TestMessageAcknowledgement: Zustellung ist quittierbar und wiederholbar; eine bestätigte Nachricht wird nicht erneut
// verarbeitet, die zweite Bestätigung ist ein No-op ohne zweites Ereignis, und unbestätigte Nachrichten sind nach einem
// Neustart kontrolliert erneut zustellbar (mit Versuchszähler).
func TestMessageAcknowledgement(t *testing.T) {
	s, clock := msgTestStore(t)
	m := NewMessages(s, "orchestrator")

	// Anlegen, zustellen, bestätigen.
	if !msgSend(t, m, msgFixture("m-1")) {
		t.Fatal("neue Nachricht gilt nicht als angelegt")
	}
	if got := msgSummary(msgPending(t, m, "orchestrator")); !slices.Equal(got, []string{"m-1:pending:0"}) {
		t.Fatalf("ausstehend = %v", got)
	}
	d := msgDeliver(t, m, "m-1", "orchestrator")
	if d.Duplicate || d.Redelivery || d.Delivery.State != domain.DeliveryDelivered || d.Delivery.Attempts != 1 ||
		d.Message.MessageID != "m-1" || d.Message.Kind != domain.MessageTaskResult || string(d.Message.Payload) != `{"outcome":"success"}` {
		t.Fatalf("Zustellung = %+v", d)
	}
	if !msgAck(t, m, "m-1", "orchestrator") {
		t.Fatal("erste Bestätigung ohne Wirkung")
	}
	want := []string{"message.created", "message.delivered", "message.acknowledged"}
	if got := msgEventKinds(t, s); !slices.Equal(got, want) {
		t.Fatalf("Ereignisse = %v", got)
	}

	// Zweite Bestätigung: No-op, kein zweites Ereignis.
	if msgAck(t, m, "m-1", "orchestrator") {
		t.Error("zweite Bestätigung meldet eine Änderung")
	}
	if got := msgEventKinds(t, s); !slices.Equal(got, want) {
		t.Errorf("zweite Bestätigung schrieb Ereignisse: %v", got)
	}

	// Eine bestätigte Nachricht wird nicht erneut ausgeliefert: weder einzeln noch im Sammelaufruf noch nach Neustart.
	d = msgDeliver(t, m, "m-1", "orchestrator")
	if !d.Duplicate || d.Delivery.State != domain.DeliveryAcknowledged || d.Delivery.Attempts != 1 || d.Redelivery {
		t.Errorf("erneute Auslieferung der bestätigten Nachricht = %+v", d)
	}
	if got := msgPending(t, m, "orchestrator"); len(got) != 0 {
		t.Errorf("bestätigte Nachricht ausstehend: %v", msgSummary(got))
	}
	if got, err := m.DeliverPending(msgCtx(t), msgProject, "orchestrator", 0); err != nil || len(got) != 0 {
		t.Errorf("DeliverPending lieferte bestätigte Nachrichten: %+v (%v)", got, err)
	}
	if created := msgSend(t, m, msgFixture("m-1")); created {
		t.Error("erneutes Senden derselben Nachricht gilt als angelegt")
	}
	if got := msgEventKinds(t, s); !slices.Equal(got, want) {
		t.Errorf("Duplikate schrieben Ereignisse: %v", got)
	}

	// Neustart mit einer zugestellten (m-2) und einer unzugestellten (m-3) Nachricht.
	msgSend(t, m, msgFixture("m-2"))
	msgSend(t, m, msgFixture("m-3"))
	msgDeliver(t, m, "m-2", "orchestrator")
	s = msgRestart(t, s, clock)
	m = NewMessages(s, "orchestrator")
	if got := msgSummary(msgPending(t, m, "orchestrator")); !slices.Equal(got, []string{"m-2:delivered:1", "m-3:pending:0"}) {
		t.Fatalf("ausstehend nach Neustart = %v", got)
	}

	// Kontrolliert erneut zustellen: in fester Reihenfolge, mit Zähler; die zugestellte Nachricht ist als Wiederholung markiert.
	got, err := m.DeliverPending(msgCtx(t), msgProject, "orchestrator", 0)
	if err != nil || len(got) != 2 {
		t.Fatalf("DeliverPending = %+v (%v)", got, err)
	}
	if got[0].Message.MessageID != "m-2" || !got[0].Redelivery || got[0].Delivery.Attempts != 2 || got[0].Duplicate {
		t.Errorf("m-2 = %+v", got[0])
	}
	if got[1].Message.MessageID != "m-3" || got[1].Redelivery || got[1].Delivery.Attempts != 1 || got[1].Duplicate {
		t.Errorf("m-3 = %+v", got[1])
	}
	msgAck(t, m, "m-2", "orchestrator")

	// Zweiter Neustart: nur noch m-3 ist offen; m-1 und m-2 kommen nicht wieder.
	s = msgRestart(t, s, clock)
	m = NewMessages(s, "orchestrator")
	if got := msgSummary(msgPending(t, m, "orchestrator")); !slices.Equal(got, []string{"m-3:delivered:1"}) {
		t.Fatalf("ausstehend nach zweitem Neustart = %v", got)
	}
	again, err := m.DeliverPending(msgCtx(t), msgProject, "orchestrator", 0)
	if err != nil || len(again) != 1 || again[0].Message.MessageID != "m-3" || !again[0].Redelivery || again[0].Delivery.Attempts != 2 {
		t.Fatalf("Zustellung nach zweitem Neustart = %+v (%v)", again, err)
	}
	wantAll := []string{
		"message.created", "message.delivered", "message.acknowledged", // m-1
		"message.created", "message.created", "message.delivered", // m-2, m-3 angelegt, m-2 zugestellt
		"message.redelivered", "message.delivered", "message.acknowledged", // nach Neustart: m-2 wiederholt, m-3 erstmals, m-2 bestätigt
		"message.redelivered", // m-3 nach zweitem Neustart
	}
	if got := msgEventKinds(t, s); !slices.Equal(got, wantAll) {
		t.Errorf("Ereignisse gesamt = %v\nerwartet           %v", got, wantAll)
	}
}

func TestAcknowledgeWithoutDeliveryIsRejected(t *testing.T) {
	s, _ := msgTestStore(t)
	m := NewMessages(s, "orchestrator")
	msgSend(t, m, msgFixture("m-1"))
	_, err := m.Acknowledge(msgCtx(t), msgProject, "m-1", "orchestrator")
	if persistence.CodeOf(err) != persistence.CodeState {
		t.Fatalf("Bestätigung ohne Zustellung: %v, erwartet state", err)
	}
	if got := msgSummary(msgPending(t, m, "orchestrator")); !slices.Equal(got, []string{"m-1:pending:0"}) {
		t.Errorf("ausstehend = %v", got)
	}
	// Unbekannte Nachricht, unbekannter Empfänger, Freitext als Empfänger.
	if _, err := m.Acknowledge(msgCtx(t), msgProject, "m-9", "orchestrator"); persistence.CodeOf(err) != persistence.CodeNotFound {
		t.Errorf("unbekannte Nachricht: %v", err)
	}
	if _, err := m.Acknowledge(msgCtx(t), msgProject, "m-1", "cli"); persistence.CodeOf(err) != persistence.CodeNotFound {
		t.Errorf("fremder Empfänger: %v", err)
	}
	if _, err := m.Deliver(msgCtx(t), msgProject, "m-1", "alle"); persistence.CodeOf(err) != persistence.CodeInvalid {
		t.Errorf("Freitext als Empfänger: %v", err)
	}
	if got := msgEventKinds(t, s); !slices.Equal(got, []string{"message.created"}) {
		t.Errorf("Ereignisse = %v", got)
	}
}

func TestMessageRecipientsAreIndependent(t *testing.T) {
	s, _ := msgTestStore(t)
	m := NewMessages(s, "orchestrator")
	msgSend(t, m, msgFixture("m-1", "orchestrator", "supervisor", "cli"))
	msgDeliver(t, m, "m-1", "orchestrator")
	msgAck(t, m, "m-1", "orchestrator")
	msgDeliver(t, m, "m-1", "supervisor")

	if got := msgPending(t, m, "orchestrator"); len(got) != 0 {
		t.Errorf("orchestrator: %v", msgSummary(got))
	}
	if got := msgSummary(msgPending(t, m, "supervisor")); !slices.Equal(got, []string{"m-1:delivered:1"}) {
		t.Errorf("supervisor: %v", got)
	}
	if got := msgSummary(msgPending(t, m, "cli")); !slices.Equal(got, []string{"m-1:pending:0"}) {
		t.Errorf("cli: %v", got)
	}
	// Die Auslieferung an supervisor ist für orchestrator kein Duplikat und umgekehrt.
	if d := msgDeliver(t, m, "m-1", "supervisor"); d.Duplicate || !d.Redelivery || d.Delivery.Attempts != 2 {
		t.Errorf("supervisor erneut = %+v", d)
	}
	if d := msgDeliver(t, m, "m-1", "orchestrator"); !d.Duplicate {
		t.Errorf("orchestrator erneut = %+v", d)
	}
}

// TestDelegationRequestIsOnlyAMessage: Ein Agent fordert Delegation über eine Nachricht an. Daraus entsteht weder eine
// Ausführung noch eine Änderung des Planungszustands; Starten und Reservieren bleibt Sache des Orchestrators (CC-42).
func TestDelegationRequestIsOnlyAMessage(t *testing.T) {
	s, _ := msgTestStore(t)
	m := NewMessages(s, "orchestrator")
	execsBefore := msgExecutions(t, s)
	revBefore := msgRevision(t, s)

	req := domain.Message{
		MessageID: "m-deleg", ProjectID: msgProject, WorkOrderID: "wo-2", ExecutionID: "exec-1", Attempt: 1,
		Sender: domain.ExecutionAddress("exec-1"), Recipients: []string{domain.AddressOrchestrator}, Kind: domain.MessageDelegationRequest,
		Payload: json.RawMessage(`{"role":"reviewer","reason":"Zweitmeinung"}`),
	}
	msgSend(t, m, req)
	d := msgDeliver(t, m, "m-deleg", "orchestrator")
	if d.Message.Kind != domain.MessageDelegationRequest || d.Delivery.State != domain.DeliveryDelivered {
		t.Fatalf("Zustellung = %+v", d)
	}
	msgAck(t, m, "m-deleg", "orchestrator")

	if got := msgExecutions(t, s); !slices.Equal(got, execsBefore) {
		t.Errorf("Ausführungen vorher %v, nachher %v: die Nachricht hat etwas gestartet", execsBefore, got)
	}
	if got := msgRevision(t, s); got != revBefore {
		t.Errorf("Revision %d -> %d", revBefore, got)
	}

	// Eine Delegation geht nur an den Orchestrator und kommt nur von einem Agenten oder einer Ausführung.
	for name, mut := range map[string]func(*domain.Message){
		"an einen Agenten":  func(x *domain.Message) { x.Recipients = []string{"agent:agent-1"} },
		"an den Supervisor": func(x *domain.Message) { x.Recipients = []string{domain.AddressSupervisor} },
		"von der CLI":       func(x *domain.Message) { x.Sender = domain.AddressCLI },
	} {
		bad := req
		bad.MessageID = "m-bad"
		mut(&bad)
		if _, err := m.Send(msgCtx(t), bad); persistence.CodeOf(err) != persistence.CodeInvalid {
			t.Errorf("Delegation %s: %v, erwartet invalid", name, err)
		}
	}
	if got := msgExecutions(t, s); !slices.Equal(got, execsBefore) {
		t.Errorf("Ausführungen nach abgewiesenen Anforderungen: %v", got)
	}
}

func msgExecutions(t *testing.T, s *persistence.Store) []string {
	t.Helper()
	var out []string
	err := s.View(msgCtx(t), func(r *persistence.ReadTx) error {
		for _, wo := range []string{"wo-1", "wo-2", "wo-3"} {
			es, err := r.Executions(msgProject, wo)
			if err != nil {
				return err
			}
			for _, e := range es {
				out = append(out, e.WorkOrderID+"/"+e.ExecutionID)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestMessagesDoNotChangeRevisionButWriteAudit(t *testing.T) {
	s, _ := msgTestStore(t)
	m := NewMessages(s, "orchestrator")
	rev := msgRevision(t, s)
	msgSend(t, m, msgFixture("m-1"))
	msgDeliver(t, m, "m-1", "orchestrator")
	msgAck(t, m, "m-1", "orchestrator")
	if got := msgRevision(t, s); got != rev {
		t.Errorf("Revision %d -> %d", rev, got)
	}
	err := s.View(msgCtx(t), func(r *persistence.ReadTx) error {
		evs, err := r.Events(msgProject, persistence.EventFilter{Level: domain.AuditLevelMessage})
		if err != nil {
			return err
		}
		if len(evs) != 3 {
			t.Fatalf("%d Ereignisse", len(evs))
		}
		for _, e := range evs {
			if e.Actor != "orchestrator" || e.StateRevision != rev {
				t.Errorf("Ereignis = %+v", e)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSendRejectsWrongCorrelation(t *testing.T) {
	s, _ := msgTestStore(t)
	m := NewMessages(s, "orchestrator")
	bad := msgFixture("m-1")
	bad.WorkOrderID = "wo-1" // exec-1 gehört zu wo-2
	if _, err := m.Send(msgCtx(t), bad); persistence.CodeOf(err) != persistence.CodeInvalid {
		t.Fatalf("Versuch eines anderen Auftrags: %v", err)
	}
	if got := msgPending(t, m, "orchestrator"); len(got) != 0 {
		t.Errorf("ausstehend: %v", msgSummary(got))
	}
	if got := msgEventKinds(t, s); len(got) != 0 {
		t.Errorf("Ereignisse: %v", got)
	}
}

// TestConcurrentDeliveryAndAcknowledgement: Zustellung und Bestätigung sind serialisiert; die Zähler und Ereignisse
// stimmen auch unter Last.
func TestConcurrentDeliveryAndAcknowledgement(t *testing.T) {
	s, _ := msgTestStore(t)
	m := NewMessages(s, "orchestrator")
	msgSend(t, m, msgFixture("m-1"))
	const n = 8

	run := func(fn func(i int)) {
		var wg sync.WaitGroup
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				fn(i)
			}()
		}
		wg.Wait()
	}

	delivered := make([]DeliveredMessage, n)
	run(func(i int) {
		d, err := m.Deliver(msgCtx(t), msgProject, "m-1", "orchestrator")
		if err != nil {
			t.Errorf("Deliver: %v", err)
		}
		delivered[i] = d
	})
	attempts, fresh := map[int]bool{}, 0
	for _, d := range delivered {
		attempts[d.Delivery.Attempts] = true
		if !d.Redelivery {
			fresh++
		}
		if d.Duplicate {
			t.Errorf("Duplikat vor der Bestätigung: %+v", d)
		}
	}
	if len(attempts) != n || fresh != 1 {
		t.Errorf("Versuche %v, davon %d erste Zustellungen", attempts, fresh)
	}

	changed := make([]bool, n)
	run(func(i int) {
		c, err := m.Acknowledge(msgCtx(t), msgProject, "m-1", "orchestrator")
		if err != nil {
			t.Errorf("Acknowledge: %v", err)
		}
		changed[i] = c
	})
	count := 0
	for _, c := range changed {
		if c {
			count++
		}
	}
	if count != 1 {
		t.Errorf("%d Bestätigungen mit Wirkung, erwartet 1", count)
	}

	run(func(i int) {
		d, err := m.Deliver(msgCtx(t), msgProject, "m-1", "orchestrator")
		if err != nil || !d.Duplicate {
			t.Errorf("Zustellung nach Bestätigung = %+v (%v)", d, err)
		}
	})
	kinds := msgEventKinds(t, s)
	counts := map[string]int{}
	for _, k := range kinds {
		counts[k]++
	}
	if counts["message.created"] != 1 || counts["message.delivered"] != 1 || counts["message.redelivered"] != n-1 || counts["message.acknowledged"] != 1 || len(kinds) != n+2 {
		t.Errorf("Ereignisse = %v", counts)
	}
}
