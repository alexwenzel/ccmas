package persistence

import (
	"database/sql"
	"errors"
	"time"

	"github.com/alexwenzel/ccmas/internal/domain"
	"github.com/alexwenzel/ccmas/internal/supervisor"
)

// ---- Agenten ------------------------------------------------------------------------------------------------

// CreateAgent legt einen Agenten an; er beginnt als verfügbar mit Revision 1.
func (t *Tx) CreateAgent(projectID, agentID string, role domain.Role) (domain.Agent, error) {
	var out domain.Agent
	err := t.write(func() error {
		a := domain.Agent{ProjectID: projectID, AgentID: agentID, Role: role, Availability: domain.AgentAvailable, Revision: 1, UpdatedAt: t.at}
		if err := a.Check(); err != nil {
			return invalid(err)
		}
		if _, err := t.bump(projectID); err != nil {
			return err
		}
		at := fmtTime(t.at)
		if _, err := t.q.ExecContext(t.ctx, `INSERT INTO agents (project_id, agent_id, role, availability, revision, created_at, updated_at)
			VALUES (?, ?, ?, ?, 1, ?, ?)`, projectID, agentID, string(role), string(a.Availability), at, at); err != nil {
			return err
		}
		out = a
		return t.emit(projectID, eventSpec{level: domain.AuditLevelAgent, kind: domain.AuditAgentCreated, entityType: "agent",
			entityID: agentID, to: string(a.Availability), details: map[string]string{"role": string(role)}})
	})
	return out, err
}

// SetAgentAvailability ändert die Verfügbarkeit eines Agenten von from nach to. Erlaubt ist der Übergang nur nach
// der Übergangstabelle der Ebene (sonst Code state), und from muss der gespeicherte Zustand sein (sonst conflict).
// Ausführungs- und Auftragszustand ändern sich dadurch nicht.
func (t *Tx) SetAgentAvailability(projectID, agentID string, from, to domain.AgentAvailability) error {
	return t.write(func() error {
		if err := checkIDs(projectID, agentID); err != nil {
			return err
		}
		if !from.CanTransition(to) {
			return stateErr("Agentenverfügbarkeit %q -> %q ist nicht erlaubt", from, to)
		}
		var cur string
		err := t.q.QueryRowContext(t.ctx, "SELECT availability FROM agents WHERE project_id = ? AND agent_id = ?", projectID, agentID).Scan(&cur)
		if errors.Is(err, sql.ErrNoRows) {
			return errf(CodeNotFound, "Agent %q existiert nicht", agentID)
		}
		if err != nil {
			return err
		}
		if cur != string(from) {
			return errf(CodeConflict, "Agent %q ist %q, erwartet war %q", agentID, cur, from)
		}
		if _, err := t.q.ExecContext(t.ctx, "UPDATE agents SET availability = ?, revision = revision + 1, updated_at = ? WHERE project_id = ? AND agent_id = ?",
			string(to), fmtTime(t.at), projectID, agentID); err != nil {
			return err
		}
		return t.change(projectID, eventSpec{level: domain.AuditLevelAgent, kind: domain.AuditAgentAvailability, entityType: "agent",
			entityID: agentID, from: string(from), to: string(to)})
	})
}

const agentColumns = "project_id, agent_id, role, availability, revision, updated_at"

func scanAgent(sc interface{ Scan(...any) error }) (domain.Agent, error) {
	var (
		a           domain.Agent
		role, avail string
		updated     string
	)
	if err := sc.Scan(&a.ProjectID, &a.AgentID, &role, &avail, &a.Revision, &updated); err != nil {
		return a, err
	}
	a.Role, a.Availability = domain.Role(role), domain.AgentAvailability(avail)
	var err error
	a.UpdatedAt, err = scanTime(updated)
	return a, err
}

// Agent liefert einen Agenten.
func (r *reader) Agent(projectID, agentID string) (domain.Agent, error) {
	a, err := scanAgent(r.q.QueryRowContext(r.ctx, "SELECT "+agentColumns+" FROM agents WHERE project_id = ? AND agent_id = ?", projectID, agentID))
	if errors.Is(err, sql.ErrNoRows) {
		return a, errf(CodeNotFound, "Agent %q existiert nicht", agentID)
	}
	return a, mapErr(err)
}

// Agents liefert alle Agenten des Projekts nach ID.
func (r *reader) Agents(projectID string) ([]domain.Agent, error) {
	rows, err := r.q.QueryContext(r.ctx, "SELECT "+agentColumns+" FROM agents WHERE project_id = ? ORDER BY agent_id", projectID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	var out []domain.Agent
	for rows.Next() {
		a, err := scanAgent(rows)
		if err != nil {
			return nil, mapErr(err)
		}
		out = append(out, a)
	}
	return out, mapErr(rows.Err())
}

// ---- Ziele --------------------------------------------------------------------------------------------------

// GoalRecord ist ein Ziel (Ticket, Feature oder Milestone) mit Kriterien, Umfang und Abschlussnachweisen. Die
// fachlichen Felder sind die des Snapshots (supervisor.Goal).
type GoalRecord struct {
	ProjectID       string
	Goal            supervisor.Goal
	IntegrationMode domain.IntegrationMode
	ParentGoalID    string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

func goalKinds() []domain.TargetKind {
	return []domain.TargetKind{domain.KindTicket, domain.KindFeature, domain.KindMilestone}
}

func checkEvidence(ev *supervisor.Evidence) error {
	if ev == nil {
		return nil
	}
	if !has(integrationStates(), ev.Integration) {
		return invalidf("unbekannter Integrationsstand %q", ev.Integration)
	}
	if !has(mergeActors(), ev.MergedBy) {
		return invalidf("unbekannter Merge-Akteur %q", ev.MergedBy)
	}
	return nil
}

// CreateGoal legt ein Ziel an und liefert es mit den Zeiten zurück.
func (t *Tx) CreateGoal(g GoalRecord) (GoalRecord, error) {
	var out GoalRecord
	err := t.write(func() error {
		if err := checkIDs(g.ProjectID, g.Goal.ID); err != nil {
			return err
		}
		if err := checkOptionalID(g.ParentGoalID); err != nil {
			return err
		}
		switch {
		case !has(goalKinds(), g.Goal.Kind):
			return invalidf("Zielart %q ist nicht ticket, feature oder milestone", g.Goal.Kind)
		case !has(domain.IntegrationModes(), g.IntegrationMode):
			return invalidf("unbekannter Integrationsmodus %q", g.IntegrationMode)
		case g.ParentGoalID == g.Goal.ID:
			return invalidf("ein Ziel kann nicht sein eigenes übergeordnetes Ziel sein")
		}
		if err := checkText("Titel", g.Goal.Title, 1024, false, false); err != nil {
			return err
		}
		if err := checkEvidence(g.Goal.Evidence); err != nil {
			return err
		}
		crit, err := jsonText(nonNil(g.Goal.AcceptanceCriteria))
		if err != nil {
			return err
		}
		scope, err := jsonText(nonNil(g.Goal.Scope))
		if err != nil {
			return err
		}
		oos, err := jsonText(nonNil(g.Goal.OutOfScope))
		if err != nil {
			return err
		}
		var ev any
		if g.Goal.Evidence != nil {
			text, err := jsonText(g.Goal.Evidence)
			if err != nil {
				return err
			}
			ev = text
		}
		if _, err := t.bump(g.ProjectID); err != nil {
			return err
		}
		at := fmtTime(t.at)
		if _, err := t.q.ExecContext(t.ctx, `INSERT INTO goals (project_id, goal_id, kind, title, acceptance_criteria_json, scope_json,
			out_of_scope_json, evidence_json, integration_mode, parent_goal_id, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, g.ProjectID, g.Goal.ID, string(g.Goal.Kind), g.Goal.Title, crit, scope, oos, ev,
			string(g.IntegrationMode), orNil(g.ParentGoalID), at, at); err != nil {
			return err
		}
		out = g
		out.Goal.AcceptanceCriteria, out.Goal.Scope, out.Goal.OutOfScope = nonNil(g.Goal.AcceptanceCriteria), nonNil(g.Goal.Scope), nonNil(g.Goal.OutOfScope)
		if g.Goal.Evidence != nil {
			e := *g.Goal.Evidence
			out.Goal.Evidence = &e
		}
		out.CreatedAt, out.UpdatedAt = t.at, t.at
		return t.emit(g.ProjectID, eventSpec{level: domain.AuditLevelGoal, kind: domain.AuditGoalCreated, entityType: "goal", entityID: g.Goal.ID,
			details: map[string]string{"kind": string(g.Goal.Kind), "mode": string(g.IntegrationMode), "parent": g.ParentGoalID}})
	})
	return out, err
}

// SetGoalEvidence setzt die Abschlussnachweise des Ziels (nil: keine). Gleiche Nachweise sind keine Änderung.
func (t *Tx) SetGoalEvidence(projectID, goalID string, ev *supervisor.Evidence) error {
	return t.write(func() error {
		if err := checkIDs(projectID, goalID); err != nil {
			return err
		}
		if err := checkEvidence(ev); err != nil {
			return err
		}
		var cur sql.NullString
		err := t.q.QueryRowContext(t.ctx, "SELECT evidence_json FROM goals WHERE project_id = ? AND goal_id = ?", projectID, goalID).Scan(&cur)
		if errors.Is(err, sql.ErrNoRows) {
			return errf(CodeNotFound, "Ziel %q existiert nicht", goalID)
		}
		if err != nil {
			return err
		}
		var next any
		nextText := ""
		if ev != nil {
			if nextText, err = jsonText(ev); err != nil {
				return err
			}
			next = nextText
		}
		if cur.Valid == (ev != nil) && cur.String == nextText {
			return nil
		}
		if _, err := t.q.ExecContext(t.ctx, "UPDATE goals SET evidence_json = ?, updated_at = ? WHERE project_id = ? AND goal_id = ?",
			next, fmtTime(t.at), projectID, goalID); err != nil {
			return err
		}
		state := "cleared"
		if ev != nil {
			state = "set"
		}
		return t.change(projectID, eventSpec{level: domain.AuditLevelGoal, kind: domain.AuditGoalEvidenceSet, entityType: "goal", entityID: goalID,
			details: map[string]string{"evidence": state}})
	})
}

const goalColumns = "project_id, goal_id, kind, title, acceptance_criteria_json, scope_json, out_of_scope_json, evidence_json, integration_mode, parent_goal_id, created_at, updated_at"

func scanGoal(sc interface{ Scan(...any) error }) (GoalRecord, error) {
	var (
		g                            GoalRecord
		kind, crit, scope, oos, mode string
		evidence, parent             sql.NullString
		created, updated             string
	)
	if err := sc.Scan(&g.ProjectID, &g.Goal.ID, &kind, &g.Goal.Title, &crit, &scope, &oos, &evidence, &mode, &parent, &created, &updated); err != nil {
		return g, err
	}
	g.Goal.Kind, g.IntegrationMode, g.ParentGoalID = domain.TargetKind(kind), domain.IntegrationMode(mode), str(parent)
	if err := decodeStrict(crit, &g.Goal.AcceptanceCriteria); err != nil {
		return g, err
	}
	if err := decodeStrict(scope, &g.Goal.Scope); err != nil {
		return g, err
	}
	if err := decodeStrict(oos, &g.Goal.OutOfScope); err != nil {
		return g, err
	}
	if evidence.Valid {
		g.Goal.Evidence = new(supervisor.Evidence)
		if err := decodeStrict(evidence.String, g.Goal.Evidence); err != nil {
			return g, err
		}
	}
	var err error
	if g.CreatedAt, err = scanTime(created); err != nil {
		return g, err
	}
	g.UpdatedAt, err = scanTime(updated)
	return g, err
}

// Goal liefert ein Ziel.
func (r *reader) Goal(projectID, goalID string) (GoalRecord, error) {
	g, err := scanGoal(r.q.QueryRowContext(r.ctx, "SELECT "+goalColumns+" FROM goals WHERE project_id = ? AND goal_id = ?", projectID, goalID))
	if errors.Is(err, sql.ErrNoRows) {
		return g, errf(CodeNotFound, "Ziel %q existiert nicht", goalID)
	}
	return g, mapErr(err)
}

// Goals liefert alle Ziele des Projekts nach ID.
func (r *reader) Goals(projectID string) ([]GoalRecord, error) {
	rows, err := r.q.QueryContext(r.ctx, "SELECT "+goalColumns+" FROM goals WHERE project_id = ? ORDER BY goal_id", projectID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	var out []GoalRecord
	for rows.Next() {
		g, err := scanGoal(rows)
		if err != nil {
			return nil, mapErr(err)
		}
		out = append(out, g)
	}
	return out, mapErr(rows.Err())
}
