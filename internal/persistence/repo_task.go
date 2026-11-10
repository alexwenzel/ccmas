package persistence

import (
	"database/sql"
	"errors"
	"time"

	"github.com/alexwenzel/ccmas/internal/domain"
)

// TaskRecord ist das Asana-Abbild eines Tickets: interne Task-ID, GID, optional der eine Arbeitsauftrag, Titel,
// Priorität, Section und Zeit der letzten Synchronisation. Der fachliche Zustand und die Abhängigkeiten liegen am
// Arbeitsauftrag, nicht hier (siehe doc.go).
type TaskRecord struct {
	TaskID      string
	ProjectID   string
	AsanaGID    string
	WorkOrderID string
	Title       string
	Priority    string
	Section     string
	SyncedAt    time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// SyncTask legt das Abbild eines Tickets an oder aktualisiert es. Ein identischer Stand ist keine Änderung. Das Abbild
// gehört nicht zum Planungszustand und ändert die Zustandsrevision nicht (Journaleintrag).
func (t *Tx) SyncTask(k TaskRecord) (TaskRecord, error) {
	var out TaskRecord
	err := t.write(func() error {
		if err := checkIDs(k.TaskID, k.ProjectID); err != nil {
			return err
		}
		if err := checkOptionalID(k.WorkOrderID); err != nil {
			return err
		}
		if err := checkText("Asana-GID", k.AsanaGID, 64, false, true); err != nil {
			return err
		}
		for _, f := range []struct {
			name, v string
			max     int
		}{{"Titel", k.Title, 1024}, {"Priorität", k.Priority, 64}, {"Section", k.Section, 200}} {
			if err := checkText(f.name, f.v, f.max, false, false); err != nil {
				return err
			}
		}
		if k.SyncedAt.IsZero() {
			return invalid(&domain.ContractError{Categories: []domain.Category{domain.CatTimestamp}, Message: "Synchronisationszeit fehlt"})
		}
		op := "updated"
		cur, err := t.Task(k.ProjectID, k.TaskID)
		switch {
		case err == nil:
			same := cur.AsanaGID == k.AsanaGID && cur.WorkOrderID == k.WorkOrderID && cur.Title == k.Title &&
				cur.Priority == k.Priority && cur.Section == k.Section && cur.SyncedAt.Equal(k.SyncedAt)
			if same {
				out = cur
				return nil
			}
			if _, err := t.q.ExecContext(t.ctx, `UPDATE tasks SET asana_gid = ?, work_order_id = ?, title = ?, priority = ?, section = ?,
				synced_at = ?, updated_at = ? WHERE project_id = ? AND task_id = ?`, k.AsanaGID, orNil(k.WorkOrderID), k.Title, k.Priority,
				k.Section, fmtTime(k.SyncedAt), fmtTime(t.at), k.ProjectID, k.TaskID); err != nil {
				return err
			}
			out = k
			out.SyncedAt, out.CreatedAt, out.UpdatedAt = k.SyncedAt.UTC(), cur.CreatedAt, t.at
		case CodeOf(err) == CodeNotFound:
			op = "created"
			at := fmtTime(t.at)
			if _, err := t.q.ExecContext(t.ctx, `INSERT INTO tasks (task_id, project_id, asana_gid, work_order_id, title, priority, section,
				synced_at, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, k.TaskID, k.ProjectID, k.AsanaGID,
				orNil(k.WorkOrderID), k.Title, k.Priority, k.Section, fmtTime(k.SyncedAt), at, at); err != nil {
				return err
			}
			out = k
			out.SyncedAt, out.CreatedAt, out.UpdatedAt = k.SyncedAt.UTC(), t.at, t.at
		default:
			return err
		}
		return t.journal(k.ProjectID, eventSpec{level: domain.AuditLevelTask, kind: domain.AuditTaskSynced, entityType: "task", entityID: k.TaskID,
			details: map[string]string{"asana_gid": k.AsanaGID, "work_order_id": k.WorkOrderID, "op": op}})
	})
	return out, err
}

const taskColumns = "task_id, project_id, asana_gid, work_order_id, title, priority, section, synced_at, created_at, updated_at"

func scanTask(sc interface{ Scan(...any) error }) (TaskRecord, error) {
	var (
		k                        TaskRecord
		wo                       sql.NullString
		synced, created, updated string
	)
	if err := sc.Scan(&k.TaskID, &k.ProjectID, &k.AsanaGID, &wo, &k.Title, &k.Priority, &k.Section, &synced, &created, &updated); err != nil {
		return k, err
	}
	k.WorkOrderID = str(wo)
	var err error
	if k.SyncedAt, err = scanTime(synced); err != nil {
		return k, err
	}
	if k.CreatedAt, err = scanTime(created); err != nil {
		return k, err
	}
	k.UpdatedAt, err = scanTime(updated)
	return k, err
}

// Task liefert ein Abbild nach interner Task-ID.
func (r *reader) Task(projectID, taskID string) (TaskRecord, error) {
	k, err := scanTask(r.q.QueryRowContext(r.ctx, "SELECT "+taskColumns+" FROM tasks WHERE project_id = ? AND task_id = ?", projectID, taskID))
	if errors.Is(err, sql.ErrNoRows) {
		return k, errf(CodeNotFound, "Task %q existiert nicht", taskID)
	}
	return k, mapErr(err)
}

// TaskByGID liefert ein Abbild nach Asana-GID.
func (r *reader) TaskByGID(projectID, gid string) (TaskRecord, error) {
	k, err := scanTask(r.q.QueryRowContext(r.ctx, "SELECT "+taskColumns+" FROM tasks WHERE project_id = ? AND asana_gid = ?", projectID, gid))
	if errors.Is(err, sql.ErrNoRows) {
		return k, errf(CodeNotFound, "Task mit GID %q existiert nicht", gid)
	}
	return k, mapErr(err)
}

// Tasks liefert alle Abbilder des Projekts nach Task-ID.
func (r *reader) Tasks(projectID string) ([]TaskRecord, error) {
	rows, err := r.q.QueryContext(r.ctx, "SELECT "+taskColumns+" FROM tasks WHERE project_id = ? ORDER BY task_id", projectID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	var out []TaskRecord
	for rows.Next() {
		k, err := scanTask(rows)
		if err != nil {
			return nil, mapErr(err)
		}
		out = append(out, k)
	}
	return out, mapErr(rows.Err())
}
