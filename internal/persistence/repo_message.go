package persistence

import (
	"database/sql"
	"encoding/json"
	"errors"
	"slices"

	"github.com/alexwenzel/ccmas/internal/domain"
)

// Nachrichten und Zustellung (CC-45). Nachrichten und Zustellungen sind Journal, kein Planungszustand: Sie stehen nicht
// im ProjectSnapshot und erhöhen state_revision nicht (Tx.journal); jede Änderung schreibt aber ihr Audit-Ereignis in
// derselben Transaktion. Die Ereignisse tragen nie die Nutzdaten.

// PendingDelivery ist eine noch nicht bestätigte Zustellung samt Nachricht.
type PendingDelivery struct {
	Message  domain.Message
	Delivery domain.MessageDelivery
}

// CreateMessage legt eine Nachricht mit je einer Zustellung (pending) pro Empfänger an. Gleiche ID mit gleichem Inhalt
// ist ein No-op ohne Ereignis; mit anderem Inhalt, auch in einem anderen Projekt, ein conflict (message_id ist global
// eindeutig). Gleicher Inhalt heißt: gleiche Felder, gleiche Empfängermenge (die Reihenfolge zählt nicht), gleiche
// kanonische Nutzdaten und, falls der Aufrufer eine Zeit setzt, dieselbe Zeit. Der Zustellzustand zählt nicht.
//
// Gehört die Nachricht zu einem Versuch, prüft diese Methode, dass die Ausführung zum genannten Auftrag gehört: Die
// Datenbank bindet nur das Projekt (Fremdschlüssel). Ein Verstoß ist invalid, ein unbekanntes Projekt, ein unbekannter
// Auftrag oder Versuch not_found; in beiden Fällen wird nichts geschrieben.
func (t *Tx) CreateMessage(m domain.Message) error {
	return t.write(func() error {
		if err := m.Check(); err != nil {
			return invalid(err)
		}
		payload, err := m.CanonicalPayload()
		if err != nil {
			return invalid(err)
		}
		recipients := m.SortedRecipients()

		var existingProject string
		err = t.q.QueryRowContext(t.ctx, "SELECT project_id FROM messages WHERE message_id = ?", m.MessageID).Scan(&existingProject)
		switch {
		case err == nil:
			if existingProject != m.ProjectID {
				return errf(CodeConflict, "Nachricht %q gehört zu einem anderen Projekt", m.MessageID)
			}
			cur, err := t.Message(m.ProjectID, m.MessageID)
			if err != nil {
				return err
			}
			if !sameMessage(cur, m, payload, recipients) {
				return errf(CodeConflict, "Nachricht %q existiert mit anderem Inhalt", m.MessageID)
			}
			return nil
		case !errors.Is(err, sql.ErrNoRows):
			return err
		}

		if _, err := t.revision(m.ProjectID); err != nil {
			return err
		}
		if m.WorkOrderID != "" {
			if _, err := t.WorkOrder(m.ProjectID, m.WorkOrderID); err != nil {
				return err
			}
		}
		if m.ExecutionID != "" {
			exec, err := t.Execution(m.ProjectID, m.ExecutionID)
			if err != nil {
				return err
			}
			if _, err := t.Attempt(m.ProjectID, m.ExecutionID, m.Attempt); err != nil {
				return err
			}
			if exec.WorkOrderID != m.WorkOrderID {
				return invalidf("die Ausführung %q gehört zu Auftrag %q, die Nachricht zu %q", m.ExecutionID, exec.WorkOrderID, m.WorkOrderID)
			}
		}
		created := m.CreatedAt
		if created.IsZero() {
			created = t.at
		}
		// messages.recipient hält den ersten Empfänger in sortierter Folge; maßgeblich sind die Zeilen in message_deliveries.
		if _, err := t.q.ExecContext(t.ctx, `INSERT INTO messages (message_id, project_id, work_order_id, execution_id, attempt, sender,
			recipient, type, correlation_id, payload_json, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			m.MessageID, m.ProjectID, orNil(m.WorkOrderID), orNil(m.ExecutionID), attemptOrNil(m.Attempt), m.Sender, recipients[0],
			string(m.Kind), m.CorrelationID, string(payload), fmtTime(created)); err != nil {
			return err
		}
		for _, r := range recipients {
			if _, err := t.q.ExecContext(t.ctx, "INSERT INTO message_deliveries (message_id, recipient, state) VALUES (?, ?, 'pending')", m.MessageID, r); err != nil {
				return err
			}
		}
		details := map[string]string{"kind": string(m.Kind), "sender": m.Sender, "recipients": itoa(int64(len(recipients)))}
		if m.WorkOrderID != "" {
			details["work_order_id"] = m.WorkOrderID
		}
		if m.ExecutionID != "" {
			details["execution"] = attemptEntity(m.ExecutionID, m.Attempt)
		}
		return t.journal(m.ProjectID, eventSpec{level: domain.AuditLevelMessage, kind: domain.AuditMessageCreated, entityType: "message",
			entityID: m.MessageID, to: string(domain.DeliveryPending), details: details})
	})
}

func attemptOrNil(attempt int) any {
	if attempt == 0 {
		return nil
	}
	return attempt
}

func sameMessage(cur, m domain.Message, payload []byte, recipients []string) bool {
	return cur.WorkOrderID == m.WorkOrderID && cur.ExecutionID == m.ExecutionID && cur.Attempt == m.Attempt &&
		cur.Sender == m.Sender && cur.Kind == m.Kind && cur.CorrelationID == m.CorrelationID &&
		string(cur.Payload) == string(payload) && slices.Equal(cur.Recipients, recipients) &&
		(m.CreatedAt.IsZero() || m.CreatedAt.Equal(cur.CreatedAt))
}

// TransitionDelivery führt die Zustellung einer Nachricht an einen Empfänger von from nach to (Übergangstabelle
// domain.DeliveryState; sonst state) und schreibt ihr Ereignis. Der erwartete Ist-Zustand muss stimmen (sonst
// conflict). Jedes Erreichen von „zugestellt“, auch die Wiederholung (delivered -> delivered), erhöht den Zähler der
// Versuche und setzt die Zustellzeit; das Bestätigen setzt die Bestätigungszeit. Gibt die Zustellung nach dem Übergang zurück.
func (t *Tx) TransitionDelivery(projectID, messageID, recipient string, from, to domain.DeliveryState) (domain.MessageDelivery, error) {
	var out domain.MessageDelivery
	err := t.write(func() error {
		if err := checkIDs(projectID, messageID); err != nil {
			return err
		}
		if err := domain.CheckAddress(recipient); err != nil {
			return invalid(err)
		}
		if !from.CanTransition(to) {
			return stateErr("Zustellzustand %q -> %q ist nicht erlaubt", from, to)
		}
		cur, err := t.MessageDelivery(projectID, messageID, recipient)
		if err != nil {
			return err
		}
		if cur.State != from {
			return errf(CodeConflict, "Zustellung %q an %q ist %q, erwartet war %q", messageID, recipient, cur.State, from)
		}
		next := cur
		next.State = to
		kind := domain.AuditMessageAcknowledged
		switch to {
		case domain.DeliveryDelivered:
			next.Attempts++
			next.DeliveredAt = t.at
			kind = domain.AuditMessageDelivered
			if from == domain.DeliveryDelivered {
				kind = domain.AuditMessageRedelivered
			}
		case domain.DeliveryAcknowledged:
			next.AcknowledgedAt = t.at
		}
		res, err := t.q.ExecContext(t.ctx, `UPDATE message_deliveries SET state = ?, attempts = ?, delivered_at = ?, acknowledged_at = ?
			WHERE message_id = ? AND recipient = ? AND state = ?`,
			string(to), next.Attempts, nullTime(next.DeliveredAt), nullTime(next.AcknowledgedAt), messageID, recipient, string(from))
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return errf(CodeConflict, "Zustellung %q an %q hat sich geändert", messageID, recipient)
		}
		out = next
		return t.journal(projectID, eventSpec{level: domain.AuditLevelMessage, kind: kind, entityType: "message_delivery",
			entityID: messageID + "/" + recipient, from: string(from), to: string(to),
			details: map[string]string{"recipient": recipient, "attempts": itoa(int64(next.Attempts))}})
	})
	if err != nil {
		return domain.MessageDelivery{}, err
	}
	return out, nil
}

const messageColumns = "m.message_id, m.project_id, m.work_order_id, m.execution_id, m.attempt, m.sender, m.type, m.correlation_id, m.payload_json, m.created_at"

func scanMessage(sc interface{ Scan(...any) error }, extra ...any) (domain.Message, error) {
	var (
		m                       domain.Message
		order, exec             sql.NullString
		attempt                 sql.NullInt64
		kind, payload, createdS string
	)
	dest := append([]any{&m.MessageID, &m.ProjectID, &order, &exec, &attempt, &m.Sender, &kind, &m.CorrelationID, &payload, &createdS}, extra...)
	if err := sc.Scan(dest...); err != nil {
		return m, err
	}
	m.WorkOrderID, m.ExecutionID, m.Attempt = str(order), str(exec), int(attempt.Int64)
	m.Kind, m.Payload = domain.MessageKind(kind), json.RawMessage(payload)
	var err error
	m.CreatedAt, err = scanTime(createdS)
	return m, err
}

const deliveryColumns = "d.message_id, d.recipient, d.state, d.attempts, d.delivered_at, d.acknowledged_at"

func scanDelivery(sc interface{ Scan(...any) error }) (domain.MessageDelivery, error) {
	var (
		d              domain.MessageDelivery
		state          string
		delivered, ack sql.NullString
	)
	if err := sc.Scan(&d.MessageID, &d.Recipient, &state, &d.Attempts, &delivered, &ack); err != nil {
		return d, err
	}
	d.State = domain.DeliveryState(state)
	var err error
	if d.DeliveredAt, err = scanNullTime(delivered); err != nil {
		return d, err
	}
	d.AcknowledgedAt, err = scanNullTime(ack)
	return d, err
}

// recipientsOf liefert die Empfänger einer Nachricht aus ihren Zustellungen, sortiert.
func (r *reader) recipientsOf(messageID string) ([]string, error) {
	rows, err := r.q.QueryContext(r.ctx, "SELECT recipient FROM message_deliveries WHERE message_id = ? ORDER BY recipient", messageID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var rc string
		if err := rows.Scan(&rc); err != nil {
			return nil, mapErr(err)
		}
		out = append(out, rc)
	}
	return out, mapErr(rows.Err())
}

// Message liefert eine Nachricht samt Empfängern (sortiert).
func (r *reader) Message(projectID, messageID string) (domain.Message, error) {
	m, err := scanMessage(r.q.QueryRowContext(r.ctx, "SELECT "+messageColumns+" FROM messages m WHERE m.project_id = ? AND m.message_id = ?", projectID, messageID))
	if errors.Is(err, sql.ErrNoRows) {
		return m, errf(CodeNotFound, "Nachricht %q existiert nicht", messageID)
	}
	if err != nil {
		return m, mapErr(err)
	}
	m.Recipients, err = r.recipientsOf(messageID)
	return m, err
}

// MessageDelivery liefert die Zustellung einer Nachricht an einen Empfänger.
func (r *reader) MessageDelivery(projectID, messageID, recipient string) (domain.MessageDelivery, error) {
	if err := domain.CheckAddress(recipient); err != nil {
		return domain.MessageDelivery{}, invalid(err)
	}
	d, err := scanDelivery(r.q.QueryRowContext(r.ctx, `SELECT `+deliveryColumns+` FROM message_deliveries d JOIN messages m ON m.message_id = d.message_id
		WHERE m.project_id = ? AND d.message_id = ? AND d.recipient = ?`, projectID, messageID, recipient))
	if errors.Is(err, sql.ErrNoRows) {
		return d, errf(CodeNotFound, "Zustellung der Nachricht %q an %q existiert nicht", messageID, recipient)
	}
	return d, mapErr(err)
}

// MessageDeliveries liefert die Zustellungen einer Nachricht, nach Empfänger sortiert (leer, wenn es die Nachricht nicht gibt).
func (r *reader) MessageDeliveries(projectID, messageID string) ([]domain.MessageDelivery, error) {
	rows, err := r.q.QueryContext(r.ctx, `SELECT `+deliveryColumns+` FROM message_deliveries d JOIN messages m ON m.message_id = d.message_id
		WHERE m.project_id = ? AND d.message_id = ? ORDER BY d.recipient`, projectID, messageID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	var out []domain.MessageDelivery
	for rows.Next() {
		d, err := scanDelivery(rows)
		if err != nil {
			return nil, mapErr(err)
		}
		out = append(out, d)
	}
	return out, mapErr(rows.Err())
}

// PendingDeliveries liefert die noch nicht bestätigten Zustellungen an einen Empfänger: pending und delivered, in
// fester Reihenfolge (Zeit der Nachricht, dann ID). Limit 0 heißt unbegrenzt. Zugestellte, nicht bestätigte Nachrichten
// gehören dazu: Nach einem Neustart sind sie kontrolliert erneut zustellbar.
func (r *reader) PendingDeliveries(projectID, recipient string, limit int) ([]PendingDelivery, error) {
	if err := domain.CheckAddress(recipient); err != nil {
		return nil, invalid(err)
	}
	query := `SELECT ` + messageColumns + `, d.message_id, d.recipient, d.state, d.attempts, d.delivered_at, d.acknowledged_at
		FROM message_deliveries d JOIN messages m ON m.message_id = d.message_id
		WHERE m.project_id = ? AND d.recipient = ? AND d.state <> 'acknowledged' ORDER BY m.created_at, m.message_id`
	if limit > 0 {
		query += " LIMIT " + itoa(int64(limit))
	}
	rows, err := r.q.QueryContext(r.ctx, query, projectID, recipient)
	if err != nil {
		return nil, mapErr(err)
	}
	var out []PendingDelivery
	for rows.Next() {
		var (
			d              domain.MessageDelivery
			state          string
			delivered, ack sql.NullString
		)
		m, err := scanMessage(rows, &d.MessageID, &d.Recipient, &state, &d.Attempts, &delivered, &ack)
		if err == nil {
			d.State = domain.DeliveryState(state)
			if d.DeliveredAt, err = scanNullTime(delivered); err == nil {
				d.AcknowledgedAt, err = scanNullTime(ack)
			}
		}
		if err != nil {
			rows.Close()
			return nil, mapErr(err)
		}
		out = append(out, PendingDelivery{Message: m, Delivery: d})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, mapErr(err)
	}
	rows.Close()
	for i := range out {
		if out[i].Message.Recipients, err = r.recipientsOf(out[i].Message.MessageID); err != nil {
			return nil, err
		}
	}
	return out, nil
}
