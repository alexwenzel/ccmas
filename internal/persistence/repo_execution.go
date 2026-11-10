package persistence

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/alexwenzel/ccmas/internal/domain"
	"github.com/alexwenzel/ccmas/internal/supervisor"
)

// CreateExecution legt eine logische Ausführung an (Rolle, optional Auftrag und Agent).
func (t *Tx) CreateExecution(e domain.Execution) error {
	return t.write(func() error {
		if err := e.Check(); err != nil {
			return invalid(err)
		}
		if _, err := t.bump(e.ProjectID); err != nil {
			return err
		}
		if _, err := t.q.ExecContext(t.ctx, `INSERT INTO executions (execution_id, project_id, work_order_id, agent_id, role, created_at)
			VALUES (?, ?, ?, ?, ?, ?)`, e.ExecutionID, e.ProjectID, orNil(e.WorkOrderID), orNil(e.AgentID), string(e.Role), fmtTime(e.CreatedAt)); err != nil {
			return err
		}
		return t.emit(e.ProjectID, eventSpec{level: domain.AuditLevelExecution, kind: domain.AuditExecutionCreated, entityType: "execution",
			entityID: e.ExecutionID, details: map[string]string{"role": string(e.Role), "work_order_id": e.WorkOrderID, "agent_id": e.AgentID}})
	})
}

func attemptEntity(execID string, attempt int) string { return execID + "#" + itoa(int64(attempt)) }

// StartAttempt beginnt einen Versuch einer Ausführung im Zustand initializing. Versuche zählen lückenlos ab 1; ein
// neuer Versuch (Resume) beginnt erst, wenn der vorige beendet ist. Das Profil muss gespeichert sein und zur Rolle
// der Ausführung passen; ein Skill-Snapshot muss gespeichert sein.
func (t *Tx) StartAttempt(projectID string, a domain.ExecutionAttempt) error {
	return t.write(func() error {
		if err := checkIDs(projectID); err != nil {
			return err
		}
		if err := a.Check(); err != nil {
			return invalid(err)
		}
		if a.State != domain.ExecInitializing {
			return invalidf("ein Versuch beginnt im Zustand %q, nicht in %q", domain.ExecInitializing, a.State)
		}
		exec, err := t.Execution(projectID, a.ExecutionID)
		if err != nil {
			return err
		}
		var profileRole string
		err = t.q.QueryRowContext(t.ctx, "SELECT role FROM profile_versions WHERE project_id = ? AND profile_id = ? AND version = ?",
			projectID, a.ProfileID, a.ProfileVersion).Scan(&profileRole)
		if errors.Is(err, sql.ErrNoRows) {
			return errf(CodeNotFound, "Profil %s Version %d ist nicht gespeichert", a.ProfileID, a.ProfileVersion)
		}
		if err != nil {
			return err
		}
		if profileRole != string(exec.Role) {
			return invalidf("Profil %s hat die Rolle %q, die Ausführung %q die Rolle %q", a.ProfileID, profileRole, a.ExecutionID, exec.Role)
		}
		var maxAttempt sql.NullInt64
		var lastState sql.NullString
		err = t.q.QueryRowContext(t.ctx, `SELECT attempt, state FROM execution_attempts WHERE execution_id = ? ORDER BY attempt DESC LIMIT 1`,
			a.ExecutionID).Scan(&maxAttempt, &lastState)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		switch next := int(maxAttempt.Int64) + 1; {
		case a.Attempt < next:
			return errf(CodeConflict, "Versuch %d der Ausführung %q existiert bereits", a.Attempt, a.ExecutionID)
		case a.Attempt > next:
			return invalidf("Versuch %d würde eine Lücke lassen; der nächste ist %d", a.Attempt, next)
		case maxAttempt.Valid && !domain.ExecutionState(lastState.String).IsTerminal():
			return invalidf("Versuch %d der Ausführung %q läuft noch (%s); ein neuer Versuch beginnt erst nach dessen Ende", maxAttempt.Int64, a.ExecutionID, lastState.String)
		}
		var pid, pstart any
		if a.Process != nil {
			pid, pstart = a.Process.PID, fmtTime(a.Process.StartTime)
		}
		var osState any
		if a.OSProcessState != "" {
			osState = string(a.OSProcessState)
		}
		var spec any
		if len(a.Spec) > 0 {
			spec = string(a.Spec)
		}
		if _, err := t.bump(projectID); err != nil {
			return err
		}
		if _, err := t.q.ExecContext(t.ctx, `INSERT INTO execution_attempts (project_id, execution_id, attempt, profile_id, profile_version,
			skill_snapshot_id, session_id, state, process_pid, process_start_time, os_process_state, os_observed_at, worktree_path, branch,
			started_at, spec_json) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			projectID, a.ExecutionID, a.Attempt, a.ProfileID, a.ProfileVersion, orNil(a.SkillSnapshotID), a.SessionID, string(a.State), pid, pstart,
			osState, nullTime(a.OSObservedAt), a.Worktree, a.Branch, fmtTime(a.StartedAt), spec); err != nil {
			return err
		}
		return t.emit(projectID, eventSpec{level: domain.AuditLevelExecution, kind: domain.AuditAttemptStarted, entityType: "execution_attempt",
			entityID: attemptEntity(a.ExecutionID, a.Attempt), to: string(a.State),
			details: map[string]string{"role": string(exec.Role), "profile": a.ProfileID + "#" + itoa(int64(a.ProfileVersion)), "work_order_id": exec.WorkOrderID}})
	})
}

// TransitionExecution ändert den Ausführungszustand eines Versuchs von from nach to (nicht in einen Endzustand;
// dafür gibt es FinishAttempt, das das terminale Ergebnis festhält). Nur Übergänge der Übergangstabelle sind erlaubt
// (sonst Code state); from muss der gespeicherte Zustand sein (sonst conflict). Auftrag, Prozess und Agent bleiben
// unberührt.
func (t *Tx) TransitionExecution(projectID, execID string, attempt int, from, to domain.ExecutionState) error {
	return t.write(func() error {
		if err := checkIDs(projectID, execID); err != nil {
			return err
		}
		if to.IsTerminal() {
			return invalidf("Endzustand %q wird mit FinishAttempt und terminalem Ergebnis erreicht", to)
		}
		if !from.CanTransition(to) {
			return stateErr("Ausführungszustand %q -> %q ist nicht erlaubt", from, to)
		}
		cur, err := t.Attempt(projectID, execID, attempt)
		if err != nil {
			return err
		}
		if cur.State != from {
			return errf(CodeConflict, "Versuch %s ist %q, erwartet war %q", attemptEntity(execID, attempt), cur.State, from)
		}
		if _, err := t.q.ExecContext(t.ctx, "UPDATE execution_attempts SET state = ? WHERE execution_id = ? AND attempt = ? AND state = ?",
			string(to), execID, attempt, string(from)); err != nil {
			return err
		}
		return t.change(projectID, eventSpec{level: domain.AuditLevelExecution, kind: domain.AuditExecutionStateChanged,
			entityType: "execution_attempt", entityID: attemptEntity(execID, attempt), from: string(from), to: string(to)})
	})
}

// AttemptResult ist das terminale Ergebnis eines Versuchs. Terminal.State ist der Endzustand; Quelle, Outcome und
// Endzustand müssen zusammenpassen (domain.ExecutionAttempt.Check). EndedAt leer: Zeit der Transaktion.
type AttemptResult struct {
	Terminal domain.TerminalResult
	Outcome  domain.Outcome
	Summary  string
	EndedAt  time.Time
}

// FinishAttempt beendet einen Versuch mit genau einem terminalen Ergebnis. from muss der gespeicherte Zustand sein
// und in den Endzustand übergehen dürfen. Ein beendeter Versuch ändert sein Ergebnis nicht mehr.
func (t *Tx) FinishAttempt(projectID, execID string, attempt int, from domain.ExecutionState, r AttemptResult) error {
	return t.write(func() error {
		if err := checkIDs(projectID, execID); err != nil {
			return err
		}
		cur, err := t.Attempt(projectID, execID, attempt)
		if err != nil {
			return err
		}
		to := r.Terminal.State
		if !to.IsTerminal() {
			return invalidf("%q ist kein Endzustand", to)
		}
		next := cur
		next.State, next.Terminal, next.Outcome, next.Summary = to, &r.Terminal, r.Outcome, r.Summary
		next.EndedAt = r.EndedAt
		if next.EndedAt.IsZero() {
			next.EndedAt = t.at
		}
		next.EndedAt = next.EndedAt.UTC()
		if err := next.Check(); err != nil {
			return invalid(err)
		}
		if !from.CanTransition(to) {
			return stateErr("Ausführungszustand %q -> %q ist nicht erlaubt", from, to)
		}
		if cur.State != from {
			return errf(CodeConflict, "Versuch %s ist %q, erwartet war %q", attemptEntity(execID, attempt), cur.State, from)
		}
		var exit, outcome any
		if r.Terminal.Source == domain.TerminalFromProcessExit {
			exit = r.Terminal.ExitCode
		}
		if r.Outcome != "" {
			outcome = string(r.Outcome)
		}
		if _, err := t.q.ExecContext(t.ctx, `UPDATE execution_attempts SET state = ?, ended_at = ?, terminal_source = ?, exit_code = ?, outcome = ?, summary = ?
			WHERE execution_id = ? AND attempt = ? AND state = ?`, string(to), fmtTime(next.EndedAt), r.Terminal.Source, exit, outcome, r.Summary,
			execID, attempt, string(from)); err != nil {
			return err
		}
		details := map[string]string{"source": r.Terminal.Source}
		if r.Outcome != "" {
			details["outcome"] = string(r.Outcome)
		}
		if r.Terminal.Source == domain.TerminalFromProcessExit {
			details["exit_code"] = itoa(int64(r.Terminal.ExitCode))
		}
		return t.change(projectID, eventSpec{level: domain.AuditLevelExecution, kind: domain.AuditAttemptFinished,
			entityType: "execution_attempt", entityID: attemptEntity(execID, attempt), from: string(from), to: string(to), details: details})
	})
}

// SetSession hält die Session-ID des Versuchs fest. Sie wird einmal gesetzt; dieselbe ID erneut ist keine Änderung,
// eine andere ein Konflikt. Die Session gehört nicht zum Planungszustand.
func (t *Tx) SetSession(projectID, execID string, attempt int, sessionID string) error {
	return t.write(func() error {
		if err := checkIDs(projectID, execID); err != nil {
			return err
		}
		if err := checkText("Session-ID", sessionID, 512, false, true); err != nil {
			return err
		}
		cur, err := t.Attempt(projectID, execID, attempt)
		if err != nil {
			return err
		}
		switch cur.SessionID {
		case sessionID:
			return nil
		case "":
		default:
			return errf(CodeConflict, "Versuch %s hat bereits eine Session", attemptEntity(execID, attempt))
		}
		if _, err := t.q.ExecContext(t.ctx, "UPDATE execution_attempts SET session_id = ? WHERE execution_id = ? AND attempt = ?", sessionID, execID, attempt); err != nil {
			return err
		}
		return t.journal(projectID, eventSpec{level: domain.AuditLevelExecution, kind: domain.AuditSessionSet,
			entityType: "execution_attempt", entityID: attemptEntity(execID, attempt)})
	})
}

// RecordProcess hält Prozessidentität und beobachteten Prozesszustand eines Versuchs fest. Die Identität (PID und
// Startzeit) gehört zum Versuch und ändert sich nicht (eine wiederverwendete PID hat eine andere Startzeit und ist
// ein anderer Prozess: Konflikt). Der Prozesszustand wird gemessen und hat keine Übergangstabelle; er ändert weder
// Ausführungs- noch Auftragszustand. Eine unveränderte Beobachtung ist keine Änderung; jede Änderung erzeugt ein
// Ereignis, deshalb melden Aufrufer Zustandswechsel. Beobachtungen gehören nicht zum Planungszustand.
func (t *Tx) RecordProcess(projectID, execID string, attempt int, id domain.ProcessIdentity, state domain.OSProcessState, observedAt time.Time) error {
	return t.write(func() error {
		if err := checkIDs(projectID, execID); err != nil {
			return err
		}
		cur, err := t.Attempt(projectID, execID, attempt)
		if err != nil {
			return err
		}
		next := cur
		next.Process, next.OSProcessState, next.OSObservedAt = &domain.ProcessIdentity{PID: id.PID, StartTime: id.StartTime.UTC()}, state, observedAt.UTC()
		if err := next.Check(); err != nil {
			return invalid(err)
		}
		if p := cur.Process; p != nil && (p.PID != id.PID || !p.StartTime.Equal(id.StartTime)) {
			return errf(CodeConflict, "Versuch %s gehört zu PID %d; eine andere Prozessidentität ist ein anderer Prozess", attemptEntity(execID, attempt), p.PID)
		}
		if cur.Process != nil && cur.OSProcessState == state && cur.OSObservedAt.Equal(observedAt) {
			return nil
		}
		if _, err := t.q.ExecContext(t.ctx, `UPDATE execution_attempts SET process_pid = ?, process_start_time = ?, os_process_state = ?, os_observed_at = ?
			WHERE execution_id = ? AND attempt = ?`, id.PID, fmtTime(id.StartTime), string(state), fmtTime(observedAt), execID, attempt); err != nil {
			return err
		}
		return t.journal(projectID, eventSpec{level: domain.AuditLevelProcess, kind: domain.AuditProcessObserved,
			entityType: "execution_attempt", entityID: attemptEntity(execID, attempt), from: string(cur.OSProcessState), to: string(state),
			details: map[string]string{"pid": itoa(int64(id.PID))}})
	})
}

// ---- Lesen --------------------------------------------------------------------------------------------------

// Execution liefert eine logische Ausführung.
func (r *reader) Execution(projectID, execID string) (domain.Execution, error) {
	e, err := scanExecution(r.q.QueryRowContext(r.ctx, "SELECT execution_id, project_id, work_order_id, agent_id, role, created_at FROM executions WHERE project_id = ? AND execution_id = ?",
		projectID, execID))
	if errors.Is(err, sql.ErrNoRows) {
		return e, errf(CodeNotFound, "Ausführung %q existiert nicht", execID)
	}
	return e, mapErr(err)
}

func scanExecution(sc interface{ Scan(...any) error }) (domain.Execution, error) {
	var (
		e        domain.Execution
		wo, ag   sql.NullString
		role, at string
	)
	if err := sc.Scan(&e.ExecutionID, &e.ProjectID, &wo, &ag, &role, &at); err != nil {
		return e, err
	}
	e.WorkOrderID, e.AgentID, e.Role = str(wo), str(ag), domain.Role(role)
	var err error
	e.CreatedAt, err = scanTime(at)
	return e, err
}

// Executions liefert die Ausführungen des Projekts nach Erstellung (workOrderID leer: alle).
func (r *reader) Executions(projectID, workOrderID string) ([]domain.Execution, error) {
	q := "SELECT execution_id, project_id, work_order_id, agent_id, role, created_at FROM executions WHERE project_id = ?"
	args := []any{projectID}
	if workOrderID != "" {
		q += " AND work_order_id = ?"
		args = append(args, workOrderID)
	}
	rows, err := r.q.QueryContext(r.ctx, q+" ORDER BY created_at, execution_id", args...)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	var out []domain.Execution
	for rows.Next() {
		e, err := scanExecution(rows)
		if err != nil {
			return nil, mapErr(err)
		}
		out = append(out, e)
	}
	return out, mapErr(rows.Err())
}

const attemptColumns = `execution_id, attempt, profile_id, profile_version, skill_snapshot_id, session_id, state, process_pid, process_start_time,
	os_process_state, os_observed_at, worktree_path, branch, started_at, ended_at, terminal_source, exit_code, outcome, summary, spec_json`

func scanAttempt(sc interface{ Scan(...any) error }) (domain.ExecutionAttempt, error) {
	var (
		a                                  domain.ExecutionAttempt
		snap, pstart, osState, osAt, ended sql.NullString
		source, outcome, spec              sql.NullString
		pid, exit                          sql.NullInt64
		state, started                     string
	)
	if err := sc.Scan(&a.ExecutionID, &a.Attempt, &a.ProfileID, &a.ProfileVersion, &snap, &a.SessionID, &state, &pid, &pstart, &osState, &osAt,
		&a.Worktree, &a.Branch, &started, &ended, &source, &exit, &outcome, &a.Summary, &spec); err != nil {
		return a, err
	}
	a.SkillSnapshotID, a.State = str(snap), domain.ExecutionState(state)
	var err error
	if pid.Valid {
		st, err := scanTime(pstart.String)
		if err != nil {
			return a, err
		}
		a.Process = &domain.ProcessIdentity{PID: int(pid.Int64), StartTime: st}
	}
	if osState.Valid {
		a.OSProcessState = domain.OSProcessState(osState.String)
		if a.OSObservedAt, err = scanTime(osAt.String); err != nil {
			return a, err
		}
	}
	if a.StartedAt, err = scanTime(started); err != nil {
		return a, err
	}
	if a.EndedAt, err = scanNullTime(ended); err != nil {
		return a, err
	}
	if source.Valid {
		a.Terminal = &domain.TerminalResult{Attempt: a.Attempt, State: a.State, Source: source.String, ExitCode: int(exit.Int64)}
	}
	a.Outcome = domain.Outcome(str(outcome))
	if spec.Valid {
		a.Spec = json.RawMessage(spec.String)
	}
	return a, nil
}

// Attempt liefert einen Versuch.
func (r *reader) Attempt(projectID, execID string, attempt int) (domain.ExecutionAttempt, error) {
	a, err := scanAttempt(r.q.QueryRowContext(r.ctx, "SELECT "+attemptColumns+" FROM execution_attempts WHERE project_id = ? AND execution_id = ? AND attempt = ?",
		projectID, execID, attempt))
	if errors.Is(err, sql.ErrNoRows) {
		return a, errf(CodeNotFound, "Versuch %s existiert nicht", attemptEntity(execID, attempt))
	}
	return a, mapErr(err)
}

// Attempts liefert die Versuche einer Ausführung aufsteigend.
func (r *reader) Attempts(projectID, execID string) ([]domain.ExecutionAttempt, error) {
	rows, err := r.q.QueryContext(r.ctx, "SELECT "+attemptColumns+" FROM execution_attempts WHERE project_id = ? AND execution_id = ? ORDER BY attempt", projectID, execID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	var out []domain.ExecutionAttempt
	for rows.Next() {
		a, err := scanAttempt(rows)
		if err != nil {
			return nil, mapErr(err)
		}
		out = append(out, a)
	}
	return out, mapErr(rows.Err())
}

// ActiveExecutions liefert die Ausführungen, deren letzter Versuch nicht beendet ist, in der Form des Snapshots
// (Ausführung, Rolle, Auftrag, Zustand) nach Ausführungs-ID.
func (r *reader) ActiveExecutions(projectID string) ([]supervisor.ActiveExecution, error) {
	var terminal []string
	for _, s := range domain.ExecutionStates() {
		if s.IsTerminal() {
			terminal = append(terminal, "'"+string(s)+"'")
		}
	}
	rows, err := r.q.QueryContext(r.ctx, `SELECT e.execution_id, e.role, e.work_order_id, a.state FROM executions e
		JOIN execution_attempts a ON a.execution_id = e.execution_id
		 AND a.attempt = (SELECT max(attempt) FROM execution_attempts WHERE execution_id = e.execution_id)
		WHERE e.project_id = ? AND a.state NOT IN (`+strings.Join(terminal, ",")+`) ORDER BY e.execution_id`, projectID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	var out []supervisor.ActiveExecution
	for rows.Next() {
		var (
			a           supervisor.ActiveExecution
			role, state string
			wo          sql.NullString
		)
		if err := rows.Scan(&a.ExecutionID, &role, &wo, &state); err != nil {
			return nil, mapErr(err)
		}
		a.Role, a.WorkOrderID, a.State = domain.Role(role), str(wo), domain.ExecutionState(state)
		out = append(out, a)
	}
	return out, mapErr(rows.Err())
}
