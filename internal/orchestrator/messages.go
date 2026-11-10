package orchestrator

import (
	"context"

	"github.com/alexwenzel/ccmas/internal/domain"
	"github.com/alexwenzel/ccmas/internal/persistence"
)

// Messages ist der Nachrichtendienst des Orchestrators (Spezifikation „Nachrichten“; Ticketvertrag MessageStore mit
// Deliver und Acknowledge). Zustellung und Verarbeitung sind quittierbar und wiederholbar; die Verarbeitung erkennt
// doppelte Zustellung:
//
//   - Deliver stellt zu und zählt den Versuch. Eine zugestellte, nicht bestätigte Nachricht (zum Beispiel nach einem
//     Neustart) wird bei erneutem Deliver wiederholt zugestellt (Redelivery); der Zustand geht dabei nicht verloren.
//   - Acknowledge quittiert. Eine bereits bestätigte Nachricht wird nicht erneut ausgeliefert (Duplicate, ohne
//     Schreibwirkung), und eine zweite Bestätigung ist ein No-op ohne zweites Ereignis.
//   - Delegation: Ein Agent fordert sie mit einer Nachricht der Art delegation_request an den Orchestrator an. Die
//     Nachricht startet und reserviert nichts; das tut allein der Orchestrator (Reservierung, CC-42).
//
// Nachrichten und Zustellungen sind Journal: Sie erhöhen die Zustandsrevision nicht, schreiben aber je Änderung ein
// Audit-Ereignis in derselben Transaktion. Jeder Aufruf läuft in einer eigenen Transaktion, Zustellung und Bestätigung
// sind damit gegeneinander serialisiert.
type Messages struct {
	store *persistence.Store
	actor string
}

// NewMessages bindet den Dienst an einen Store. actor erscheint in jedem Audit-Ereignis (zum Beispiel „orchestrator“).
func NewMessages(store *persistence.Store, actor string) *Messages {
	return &Messages{store: store, actor: actor}
}

// DeliveredMessage ist das Ergebnis einer Zustellung.
type DeliveredMessage struct {
	Message domain.Message
	// Delivery ist die Zustellung nach dem Aufruf (Zustand, Versuchszähler).
	Delivery domain.MessageDelivery
	// Redelivery: Die Nachricht war schon zugestellt, aber nicht bestätigt; dies ist eine Wiederholung.
	Redelivery bool
	// Duplicate: Die Nachricht ist bereits bestätigt. Sie wurde nicht erneut zugestellt, es wurde nichts geschrieben;
	// der Empfänger verarbeitet sie nicht noch einmal.
	Duplicate bool
}

// Send legt eine Nachricht an. created ist false, wenn es sie mit gleichem Inhalt schon gab (No-op, auch nach
// Zustellung und Bestätigung); anderer Inhalt unter derselben ID ist ein Fehler mit dem Code conflict. Zu Prüfung und
// Korrelation siehe persistence.Tx.CreateMessage.
func (m *Messages) Send(ctx context.Context, msg domain.Message) (created bool, err error) {
	res, err := m.store.Update(ctx, m.actor, func(tx *persistence.Tx) error { return tx.CreateMessage(msg) })
	if err != nil {
		return false, err
	}
	return len(res.EventIDs) > 0, nil
}

// Pending liefert die noch nicht bestätigten Zustellungen an einen Empfänger (pending und delivered) in fester
// Reihenfolge, ohne etwas zu verändern. Limit 0 heißt unbegrenzt.
func (m *Messages) Pending(ctx context.Context, projectID, recipient string, limit int) ([]persistence.PendingDelivery, error) {
	var out []persistence.PendingDelivery
	err := m.store.View(ctx, func(r *persistence.ReadTx) error {
		var err error
		out, err = r.PendingDeliveries(projectID, recipient, limit)
		return err
	})
	return out, err
}

// Deliver stellt eine Nachricht einem Empfänger zu: pending -> delivered, oder als Wiederholung delivered -> delivered.
// Ist sie schon bestätigt, ist das Ergebnis ein Duplicate ohne Schreibwirkung.
func (m *Messages) Deliver(ctx context.Context, projectID, messageID, recipient string) (DeliveredMessage, error) {
	var out DeliveredMessage
	_, err := m.store.Update(ctx, m.actor, func(tx *persistence.Tx) error {
		var err error
		out, err = deliverOne(tx, projectID, messageID, recipient)
		return err
	})
	if err != nil {
		return DeliveredMessage{}, err
	}
	return out, nil
}

// DeliverPending stellt dem Empfänger alle noch nicht bestätigten Nachrichten zu (höchstens limit, 0 unbegrenzt), in der
// Reihenfolge von Pending, in einer Transaktion: ganz oder gar nicht. Das ist der kontrollierte Weg nach einem
// Neustart: zugestellte, nicht bestätigte Nachrichten kommen als Wiederholung mit höherem Zähler, bestätigte nie.
func (m *Messages) DeliverPending(ctx context.Context, projectID, recipient string, limit int) ([]DeliveredMessage, error) {
	var out []DeliveredMessage
	_, err := m.store.Update(ctx, m.actor, func(tx *persistence.Tx) error {
		out = nil
		pending, err := tx.PendingDeliveries(projectID, recipient, limit)
		if err != nil {
			return err
		}
		for _, p := range pending {
			d, err := deliverOne(tx, projectID, p.Message.MessageID, recipient)
			if err != nil {
				return err
			}
			out = append(out, d)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func deliverOne(tx *persistence.Tx, projectID, messageID, recipient string) (DeliveredMessage, error) {
	cur, err := tx.MessageDelivery(projectID, messageID, recipient)
	if err != nil {
		return DeliveredMessage{}, err
	}
	msg, err := tx.Message(projectID, messageID)
	if err != nil {
		return DeliveredMessage{}, err
	}
	if cur.State == domain.DeliveryAcknowledged {
		return DeliveredMessage{Message: msg, Delivery: cur, Duplicate: true}, nil
	}
	next, err := tx.TransitionDelivery(projectID, messageID, recipient, cur.State, domain.DeliveryDelivered)
	if err != nil {
		return DeliveredMessage{}, err
	}
	return DeliveredMessage{Message: msg, Delivery: next, Redelivery: cur.State == domain.DeliveryDelivered}, nil
}

// Acknowledge quittiert die Zustellung. changed ist false, wenn sie schon bestätigt war (No-op ohne Ereignis). Eine
// Bestätigung ohne vorherige Zustellung ist ein Fehler mit dem Code state: Sie schlösse eine Nachricht, die der
// Empfänger nie erhalten hat.
func (m *Messages) Acknowledge(ctx context.Context, projectID, messageID, recipient string) (changed bool, err error) {
	_, err = m.store.Update(ctx, m.actor, func(tx *persistence.Tx) error {
		cur, err := tx.MessageDelivery(projectID, messageID, recipient)
		if err != nil {
			return err
		}
		if cur.State == domain.DeliveryAcknowledged {
			return nil
		}
		if _, err := tx.TransitionDelivery(projectID, messageID, recipient, cur.State, domain.DeliveryAcknowledged); err != nil {
			return err
		}
		changed = true
		return nil
	})
	if err != nil {
		return false, err
	}
	return changed, nil
}
