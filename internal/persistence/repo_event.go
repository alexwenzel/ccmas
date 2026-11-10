package persistence

import (
	"strings"

	"github.com/alexwenzel/ccmas/internal/domain"
)

// EventFilter wählt Audit-Ereignisse. Leere Felder filtern nicht; Limit 0 heißt unbegrenzt.
type EventFilter struct {
	AfterID    int64 // nur Ereignisse mit größerer ID
	Limit      int
	Level      domain.AuditLevel
	EntityType string
	EntityID   string
}

// Events liefert die Audit-Ereignisse eines Projekts aufsteigend nach ID.
func (r *reader) Events(projectID string, f EventFilter) ([]domain.AuditEvent, error) {
	where := []string{"project_id = ?", "event_id > ?"}
	args := []any{projectID, f.AfterID}
	if f.Level != "" {
		where, args = append(where, "level = ?"), append(args, string(f.Level))
	}
	if f.EntityType != "" {
		where, args = append(where, "entity_type = ?"), append(args, f.EntityType)
	}
	if f.EntityID != "" {
		where, args = append(where, "entity_id = ?"), append(args, f.EntityID)
	}
	query := `SELECT event_id, project_id, occurred_at, actor, kind, entity_type, entity_id, level, from_state, to_state, state_revision, details_json
		FROM events WHERE ` + strings.Join(where, " AND ") + " ORDER BY event_id"
	if f.Limit > 0 {
		query += " LIMIT " + itoa(int64(f.Limit))
	}
	rows, err := r.q.QueryContext(r.ctx, query, args...)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	var out []domain.AuditEvent
	for rows.Next() {
		var (
			e               domain.AuditEvent
			at, level, text string
		)
		if err := rows.Scan(&e.EventID, &e.ProjectID, &at, &e.Actor, &e.Kind, &e.EntityType, &e.EntityID, &level, &e.FromState, &e.ToState, &e.StateRevision, &text); err != nil {
			return nil, mapErr(err)
		}
		e.Level = domain.AuditLevel(level)
		if e.OccurredAt, err = scanTime(at); err != nil {
			return nil, err
		}
		if text != "{}" {
			if err := decodeStrict(text, &e.Details); err != nil {
				return nil, err
			}
		}
		out = append(out, e)
	}
	return out, mapErr(rows.Err())
}
