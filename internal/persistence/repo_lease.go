package persistence

import (
	"database/sql"
	"errors"

	"github.com/alexwenzel/ccmas/internal/domain"
)

// Reservierungen (Leases). Eine Lease ordnet einen Arbeitsauftrag exklusiv einem Agenten zu (domain.ExecutionLease).
// Das Paket legt sie an, bindet sie an einen Versuch und führt sie in einen Endzustand; WER reservieren darf und WANN
// (angenommener Batch, Validator, Kapazität, Revision), entscheidet der Orchestrator in derselben Transaktion.
//
// Lease-Änderungen sind Planungszustand: Sie ändern, was startbar ist und wie viele Plätze belegt sind, und erhöhen die
// Zustandsrevision. Ein Plan, der vor der Reservierung geprüft wurde, ist danach veraltet.

// maxLeaseWorktreeBytes begrenzt den Pfad eines Worktrees an einer Lease.
const maxLeaseWorktreeBytes = 1024

// LeaseOrigin nennt die Entscheidung eines gespeicherten Batches, aus der eine Lease stammt. Eine Entscheidung
// reserviert höchstens einmal (auch nicht nach Freigabe oder Ablauf der ersten Lease): Das ist die Idempotenz der
// Ausführung eines angenommenen Batches.
type LeaseOrigin struct {
	BatchID    string
	DecisionID string
}

func (t *Tx) activeLeaseOf(column string, args ...any) (string, error) {
	var id string
	err := t.q.QueryRowContext(t.ctx, "SELECT lease_id FROM leases WHERE state = 'active' AND "+column, args...).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return id, mapErr(err)
}

// checkOrigin prüft die Herkunft einer Lease: Die Entscheidung existiert, gehört zu einem angenommenen Batch, ist selbst
// angenommen, zielt auf genau den Auftrag der Lease und hat noch nicht reserviert.
func (t *Tx) checkOrigin(projectID, orderID string, o *LeaseOrigin) error {
	if o.BatchID == "" || o.DecisionID == "" {
		return invalidf("die Herkunft einer Lease nennt Batch und Entscheidung")
	}
	if err := checkIDs(o.BatchID, o.DecisionID); err != nil {
		return err
	}
	var verdict, validation, targetKind, targetID string
	err := t.q.QueryRowContext(t.ctx, `SELECT p.verdict, d.validation, d.target_kind, d.target_id FROM decisions d
		JOIN plans p ON p.project_id = d.project_id AND p.batch_id = d.batch_id
		WHERE d.project_id = ? AND d.batch_id = ? AND d.decision_id = ?`, projectID, o.BatchID, o.DecisionID).
		Scan(&verdict, &validation, &targetKind, &targetID)
	if errors.Is(err, sql.ErrNoRows) {
		return errf(CodeNotFound, "Entscheidung %s/%s existiert nicht", o.BatchID, o.DecisionID)
	}
	if err != nil {
		return err
	}
	switch {
	case verdict != "accepted":
		return stateErr("Batch %q ist nicht angenommen (%s); nur ein angenommener Batch reserviert", o.BatchID, verdict)
	case validation != "accepted":
		return stateErr("Entscheidung %s/%s ist nicht angenommen (%s)", o.BatchID, o.DecisionID, validation)
	case targetKind != "work_order" || targetID != orderID:
		return invalidf("Entscheidung %s/%s zielt auf %s %q, die Lease auf den Auftrag %q", o.BatchID, o.DecisionID, targetKind, targetID, orderID)
	}
	prev, err := t.leaseOfDecision(projectID, o.BatchID, o.DecisionID)
	if err != nil {
		return err
	}
	if prev != nil {
		return errf(CodeConflict, "Entscheidung %s/%s hat schon reserviert (Lease %q)", o.BatchID, o.DecisionID, prev.LeaseID)
	}
	return nil
}

// checkLeaseAttempt prüft, dass der Versuch existiert und zu Auftrag und Agent der Lease gehört. Das Schema bindet nur an
// das Projekt; dass die Ausführung zum selben Auftrag gehört, prüft diese Stelle (und ein Trigger als zweite Absicherung).
func (t *Tx) checkLeaseAttempt(projectID, orderID, agentID, execID string, attempt int) error {
	exec, err := t.Execution(projectID, execID)
	if err != nil {
		return err
	}
	if _, err := t.Attempt(projectID, execID, attempt); err != nil {
		return err
	}
	if exec.WorkOrderID != orderID {
		return invalidf("die Ausführung %q gehört zu Auftrag %q, die Lease zu %q", execID, exec.WorkOrderID, orderID)
	}
	if exec.AgentID != "" && exec.AgentID != agentID {
		return invalidf("die Ausführung %q gehört zu Agent %q, die Lease zu %q", execID, exec.AgentID, agentID)
	}
	return nil
}

// CreateLease legt eine aktive Lease an und liefert sie, wie sie gespeichert ist.
//
// Die Generation vergibt die Transaktion: höchste Generation des Auftrags plus eins, lückenlos ab 1; l.Generation muss 0
// sein. Der Beginn ist, wenn nicht genannt, die Zeit der Transaktion. Die Lease beginnt aktiv. Auftrag und Agent müssen
// im Projekt existieren; ein genannter Versuch muss existieren und zum Auftrag (und, falls die Ausführung einen Agenten
// nennt, zum Agenten) der Lease gehören, sonst invalid. Höchstens eine aktive Lease je Auftrag, je Agent und je Worktree:
// Ein Verstoß ist conflict, und nichts wird geschrieben (die Datenbank hält dieselben Regeln als eindeutige Indizes).
//
// Mit origin stammt die Lease aus einer Entscheidung eines angenommenen Batches (siehe LeaseOrigin).
//
// Eine Lease ändert den Planungszustand: Die Revision steigt, das Ereignis lease.acquired trägt die neue Revision.
func (t *Tx) CreateLease(l domain.ExecutionLease, origin *LeaseOrigin) (domain.ExecutionLease, error) {
	var out domain.ExecutionLease
	err := t.write(func() error {
		switch {
		case l.Generation != 0:
			return invalidf("die Generation einer Lease vergibt die Transaktion (angegeben: %d)", l.Generation)
		case l.State != domain.LeaseActive:
			return stateErr("eine Lease beginnt im Zustand %q, nicht in %q", domain.LeaseActive, l.State)
		}
		if l.AcquiredAt.IsZero() {
			l.AcquiredAt = t.at
		}
		l.AcquiredAt, l.ExpiresAt = l.AcquiredAt.UTC(), l.ExpiresAt.UTC()
		probe := l
		probe.Generation = 1
		if err := probe.Check(); err != nil {
			return invalid(err)
		}
		if err := checkText("Worktree", l.WorktreePath, maxLeaseWorktreeBytes, false, false); err != nil {
			return err
		}
		if _, err := t.workOrderRevision(l.ProjectID, l.WorkOrderID); err != nil {
			return err
		}
		if _, err := t.Agent(l.ProjectID, l.AgentID); err != nil {
			return err
		}
		if origin != nil {
			if err := t.checkOrigin(l.ProjectID, l.WorkOrderID, origin); err != nil {
				return err
			}
		}
		if l.ExecutionID != "" {
			if err := t.checkLeaseAttempt(l.ProjectID, l.WorkOrderID, l.AgentID, l.ExecutionID, l.Attempt); err != nil {
				return err
			}
		}
		if id, err := t.activeLeaseOf("work_order_id = ?", l.WorkOrderID); err != nil {
			return err
		} else if id != "" {
			return errf(CodeConflict, "Auftrag %q ist schon durch die Lease %q reserviert", l.WorkOrderID, id)
		}
		if id, err := t.activeLeaseOf("project_id = ? AND agent_id = ?", l.ProjectID, l.AgentID); err != nil {
			return err
		} else if id != "" {
			return errf(CodeConflict, "Agent %q hält schon die Lease %q", l.AgentID, id)
		}
		if l.WorktreePath != "" {
			if id, err := t.activeLeaseOf("worktree_path = ?", l.WorktreePath); err != nil {
				return err
			} else if id != "" {
				return errf(CodeConflict, "Worktree %q gehört schon der Lease %q", l.WorktreePath, id)
			}
		}
		if err := t.q.QueryRowContext(t.ctx, "SELECT COALESCE(MAX(generation), 0) + 1 FROM leases WHERE work_order_id = ?", l.WorkOrderID).Scan(&l.Generation); err != nil {
			return err
		}
		if err := l.Check(); err != nil {
			return invalid(err)
		}

		var execID, attempt, worktree, batch, decision any
		if l.ExecutionID != "" {
			execID, attempt = l.ExecutionID, l.Attempt
		}
		worktree = orNil(l.WorktreePath)
		details := map[string]string{"work_order_id": l.WorkOrderID, "agent_id": l.AgentID, "generation": itoa(l.Generation),
			"expires_at": fmtTime(l.ExpiresAt), "worktree": boolStr(l.WorktreePath != "")}
		if l.ExecutionID != "" {
			details["execution"] = attemptEntity(l.ExecutionID, l.Attempt)
		}
		if origin != nil {
			batch, decision = origin.BatchID, origin.DecisionID
			details["batch_id"], details["decision_id"] = origin.BatchID, origin.DecisionID
		}
		if _, err := t.bump(l.ProjectID); err != nil {
			return err
		}
		if _, err := t.q.ExecContext(t.ctx, `INSERT INTO leases (lease_id, project_id, work_order_id, execution_id, attempt, agent_id, generation, state,
			worktree_path, acquired_at, expires_at, batch_id, decision_id) VALUES (?, ?, ?, ?, ?, ?, ?, 'active', ?, ?, ?, ?, ?)`,
			l.LeaseID, l.ProjectID, l.WorkOrderID, execID, attempt, l.AgentID, l.Generation, worktree, fmtTime(l.AcquiredAt), fmtTime(l.ExpiresAt),
			batch, decision); err != nil {
			return err
		}
		if err := t.emit(l.ProjectID, eventSpec{level: domain.AuditLevelLease, kind: domain.AuditLeaseAcquired, entityType: "lease",
			entityID: l.LeaseID, to: string(domain.LeaseActive), details: details}); err != nil {
			return err
		}
		var err error
		out, err = t.Lease(l.ProjectID, l.LeaseID)
		return err
	})
	return out, err
}

// BindLeaseAttempt bindet eine aktive, noch ungebundene Lease an einen Versuch (execution_id, attempt). Der Versuch muss
// zum Auftrag der Lease gehören (sonst invalid). Dieselbe Bindung noch einmal ist keine Änderung, eine andere Bindung ein
// Konflikt; an eine beendete Lease lässt sich nichts mehr binden (Code state).
func (t *Tx) BindLeaseAttempt(projectID, leaseID, execID string, attempt int) error {
	return t.write(func() error {
		if err := checkIDs(projectID, leaseID, execID); err != nil {
			return err
		}
		if attempt < 1 {
			return invalidf("Versuch %d ungültig", attempt)
		}
		cur, err := t.Lease(projectID, leaseID)
		if err != nil {
			return err
		}
		if cur.State != domain.LeaseActive {
			return stateErr("Lease %q ist %q; an eine beendete Lease wird kein Versuch gebunden", leaseID, cur.State)
		}
		if cur.ExecutionID == execID && cur.Attempt == attempt {
			return nil
		}
		if cur.ExecutionID != "" {
			return errf(CodeConflict, "Lease %q ist schon an den Versuch %s gebunden", leaseID, attemptEntity(cur.ExecutionID, cur.Attempt))
		}
		if err := t.checkLeaseAttempt(projectID, cur.WorkOrderID, cur.AgentID, execID, attempt); err != nil {
			return err
		}
		if _, err := t.q.ExecContext(t.ctx, "UPDATE leases SET execution_id = ?, attempt = ? WHERE project_id = ? AND lease_id = ? AND state = 'active' AND execution_id IS NULL",
			execID, attempt, projectID, leaseID); err != nil {
			return err
		}
		return t.change(projectID, eventSpec{level: domain.AuditLevelLease, kind: domain.AuditLeaseAttemptBound, entityType: "lease", entityID: leaseID,
			details: map[string]string{"work_order_id": cur.WorkOrderID, "agent_id": cur.AgentID, "generation": itoa(cur.Generation),
				"execution": attemptEntity(execID, attempt)}})
	})
}

var leaseEndKinds = map[domain.LeaseState]string{
	domain.LeaseReleased: domain.AuditLeaseReleased,
	domain.LeaseExpired:  domain.AuditLeaseExpired,
	domain.LeaseRevoked:  domain.AuditLeaseRevoked,
}

// TransitionLease führt eine Lease von from nach to (freigeben, ablaufen lassen oder widerrufen). Nur Übergänge der
// Übergangstabelle sind erlaubt (sonst Code state); from muss der gespeicherte Zustand sein (sonst conflict), damit
// niemand einen Widerruf überschreibt, der zwischen Lesen und Schreiben geschah. Die Endzeit ist die Zeit der Transaktion.
// Agent, Auftrag und Ausführung bleiben unberührt (eigene Ebenen). Der Übergang ist Planungszustand: Die Revision steigt.
func (t *Tx) TransitionLease(projectID, leaseID string, from, to domain.LeaseState) error {
	return t.write(func() error {
		if err := checkIDs(projectID, leaseID); err != nil {
			return err
		}
		if !from.CanTransition(to) {
			return stateErr("Lease-Zustand %q -> %q ist nicht erlaubt", from, to)
		}
		cur, err := t.Lease(projectID, leaseID)
		if err != nil {
			return err
		}
		if cur.State != from {
			return errf(CodeConflict, "Lease %q ist %q, erwartet war %q", leaseID, cur.State, from)
		}
		if _, err := t.q.ExecContext(t.ctx, "UPDATE leases SET state = ?, released_at = ? WHERE project_id = ? AND lease_id = ? AND state = ?",
			string(to), fmtTime(t.at), projectID, leaseID, string(from)); err != nil {
			return err
		}
		return t.change(projectID, eventSpec{level: domain.AuditLevelLease, kind: leaseEndKinds[to], entityType: "lease", entityID: leaseID,
			from: string(from), to: string(to),
			details: map[string]string{"work_order_id": cur.WorkOrderID, "agent_id": cur.AgentID, "generation": itoa(cur.Generation)}})
	})
}

// ---- Lesen --------------------------------------------------------------------------------------------------

const leaseColumns = `lease_id, project_id, work_order_id, execution_id, attempt, agent_id, generation, state, worktree_path,
	acquired_at, expires_at, released_at`

func scanLease(sc interface{ Scan(...any) error }) (domain.ExecutionLease, error) {
	var (
		l                        domain.ExecutionLease
		exec, worktree, released sql.NullString
		attempt                  sql.NullInt64
		state, acquired, expires string
	)
	if err := sc.Scan(&l.LeaseID, &l.ProjectID, &l.WorkOrderID, &exec, &attempt, &l.AgentID, &l.Generation, &state, &worktree,
		&acquired, &expires, &released); err != nil {
		return l, err
	}
	l.ExecutionID, l.Attempt, l.State, l.WorktreePath = str(exec), int(attempt.Int64), domain.LeaseState(state), str(worktree)
	var err error
	if l.AcquiredAt, err = scanTime(acquired); err != nil {
		return l, err
	}
	if l.ExpiresAt, err = scanTime(expires); err != nil {
		return l, err
	}
	l.ReleasedAt, err = scanNullTime(released)
	return l, err
}

// oneLease liest höchstens eine Lease (nil, wenn es keine gibt).
func (r *reader) oneLease(where string, args ...any) (*domain.ExecutionLease, error) {
	l, err := scanLease(r.q.QueryRowContext(r.ctx, "SELECT "+leaseColumns+" FROM leases WHERE "+where, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, mapErr(err)
	}
	return &l, nil
}

func (r *reader) manyLeases(where string, args ...any) ([]domain.ExecutionLease, error) {
	rows, err := r.q.QueryContext(r.ctx, "SELECT "+leaseColumns+" FROM leases WHERE "+where, args...)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	var out []domain.ExecutionLease
	for rows.Next() {
		l, err := scanLease(rows)
		if err != nil {
			return nil, mapErr(err)
		}
		out = append(out, l)
	}
	return out, mapErr(rows.Err())
}

// Lease liefert eine Lease nach ID.
func (r *reader) Lease(projectID, leaseID string) (domain.ExecutionLease, error) {
	l, err := r.oneLease("project_id = ? AND lease_id = ?", projectID, leaseID)
	if err != nil {
		return domain.ExecutionLease{}, err
	}
	if l == nil {
		return domain.ExecutionLease{}, errf(CodeNotFound, "Lease %q existiert nicht", leaseID)
	}
	return *l, nil
}

// ActiveLease liefert die aktive Lease des Auftrags (nil, wenn er nicht reserviert ist).
func (r *reader) ActiveLease(projectID, workOrderID string) (*domain.ExecutionLease, error) {
	return r.oneLease("project_id = ? AND work_order_id = ? AND state = 'active'", projectID, workOrderID)
}

// Leases liefert alle Leases des Auftrags aufsteigend nach Generation (die jüngste zuletzt).
func (r *reader) Leases(projectID, workOrderID string) ([]domain.ExecutionLease, error) {
	return r.manyLeases("project_id = ? AND work_order_id = ? ORDER BY generation", projectID, workOrderID)
}

// ActiveLeases liefert alle aktiven Leases des Projekts nach ID.
func (r *reader) ActiveLeases(projectID string) ([]domain.ExecutionLease, error) {
	return r.manyLeases("project_id = ? AND state = 'active' ORDER BY lease_id", projectID)
}

// AttemptLease liefert die Lease, die an den Versuch gebunden ist; gibt es mehrere (eine abgelaufene und ihre Nachfolgerin
// für denselben Versuch), die mit der höchsten Generation. nil, wenn der Versuch an keine Lease gebunden ist.
func (r *reader) AttemptLease(projectID, executionID string, attempt int) (*domain.ExecutionLease, error) {
	return r.oneLease("project_id = ? AND execution_id = ? AND attempt = ? ORDER BY generation DESC LIMIT 1", projectID, executionID, attempt)
}

// DecisionLease liefert die Lease, die aus der Entscheidung des Batches reserviert hat (nil, wenn noch keine).
func (r *reader) DecisionLease(projectID, batchID, decisionID string) (*domain.ExecutionLease, error) {
	return r.leaseOfDecision(projectID, batchID, decisionID)
}

func (r *reader) leaseOfDecision(projectID, batchID, decisionID string) (*domain.ExecutionLease, error) {
	return r.oneLease("project_id = ? AND batch_id = ? AND decision_id = ?", projectID, batchID, decisionID)
}
