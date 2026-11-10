package persistence

import (
	"database/sql"
	"encoding/json"
	"time"

	"github.com/alexwenzel/ccmas/internal/domain"
	"github.com/alexwenzel/ccmas/internal/supervisor"
)

// PlanRecord ist ein Planungsergebnis des Supervisors mit dem Urteil des Validators. Der Batch ist schemakonform
// (plan-v1); Pläne, die schon das Schema verletzen, lassen sich nicht als Batch festhalten. Rejections sind die
// strukturierten Ablehnungen (leer genau bei Verdict accepted). Delegations nennt je Entscheidung mit Auswahl das
// wirksame Profil der Delegation. Attempt ist das optionale Protokoll des Planungsversuchs.
type PlanRecord struct {
	Batch                  domain.DecisionBatch
	Verdict                supervisor.Verdict
	Rejections             []supervisor.Rejection
	PlanningSnapshotSHA256 string
	CurrentSnapshotSHA256  string
	Attempt                *supervisor.Attempt
	Delegations            map[string]domain.AgentProfile
	CreatedAt              time.Time // leer: Zeit der Transaktion
}

// DecisionRecord ist eine Entscheidung eines gespeicherten Batches. Validation sagt, ob die Entscheidung selbst
// zulässig war (accepted) oder abgelehnt wurde (rejected, auch durch eine Ablehnung des ganzen Batches).
type DecisionRecord struct {
	Decision   domain.Decision
	Position   int
	Validation supervisor.Verdict
	Delegation *domain.AgentProfile
}

func planVerdicts() []supervisor.Verdict {
	return []supervisor.Verdict{supervisor.VerdictAccepted, supervisor.VerdictRejected}
}

func hasCode(c supervisor.Code) bool { return has(supervisor.Codes(), c) }

// RecordPlan hält ein Planungsergebnis samt Urteil fest (Journal: ändert die Zustandsrevision nicht, denn ein Plan
// gilt für genau die Revision, auf der er beruht). Der Batch wird gegen das Schema geprüft, Urteil und Ablehnungen
// müssen zusammenpassen. decision_id ist nur im Batch eindeutig; ein Batch mit doppelter decision_id lässt sich nur
// als abgelehnt mit dem Code duplicate_decision_id festhalten, ohne Entscheidungszeilen.
func (t *Tx) RecordPlan(p PlanRecord) error {
	return t.write(func() error {
		raw, err := json.Marshal(p.Batch)
		if err != nil {
			return wrapf(CodeInternal, err, "Batch")
		}
		if err := domain.Validate(domain.TargetDecisionBatch, raw); err != nil {
			return invalid(err)
		}
		project := p.Batch.ProjectID
		if !has(planVerdicts(), p.Verdict) {
			return invalidf("Urteil %q ist für einen Plan nicht vorgesehen", p.Verdict)
		}
		if (p.Verdict == supervisor.VerdictAccepted) != (len(p.Rejections) == 0) {
			return invalidf("Urteil %q und %d Ablehnung(en) passen nicht zusammen", p.Verdict, len(p.Rejections))
		}
		if !isHex64(p.PlanningSnapshotSHA256) {
			return invalidf("die Prüfsumme des Planungs-Snapshots fehlt oder ist kein SHA-256")
		}
		if p.CurrentSnapshotSHA256 != "" && !isHex64(p.CurrentSnapshotSHA256) {
			return invalidf("die Prüfsumme des aktuellen Snapshots ist kein SHA-256")
		}
		byID := map[string]domain.Decision{}
		duplicate := false
		for _, d := range p.Batch.Decisions {
			if d.ProjectID != project || d.StateRevision != p.Batch.StateRevision {
				return invalidf("Entscheidung %q gehört nicht zu Projekt und Revision des Batches", d.DecisionID)
			}
			if _, dup := byID[d.DecisionID]; dup {
				duplicate = true
			}
			byID[d.DecisionID] = d
		}
		rejectedBatch := false
		rejectedDecision := map[string]bool{}
		hasDupCode := false
		for _, rj := range p.Rejections {
			if !hasCode(rj.Code) {
				return invalidf("unbekannter Ablehnungscode %q", rj.Code)
			}
			if err := checkText("Begründung der Ablehnung", rj.Reason, 4096, true, true); err != nil {
				return err
			}
			if rj.DecisionID == "" {
				rejectedBatch = true
			} else if _, ok := byID[rj.DecisionID]; !ok {
				return invalidf("Ablehnung bezieht sich auf die unbekannte Entscheidung %q", rj.DecisionID)
			} else {
				rejectedDecision[rj.DecisionID] = true
			}
			hasDupCode = hasDupCode || rj.Code == supervisor.CodeDuplicateDecisionID
		}
		if duplicate && !(p.Verdict == supervisor.VerdictRejected && hasDupCode) {
			return invalidf("der Batch enthält eine decision_id mehrfach; festzuhalten ist er nur als abgelehnt mit %q", supervisor.CodeDuplicateDecisionID)
		}
		profiles := map[string]string{}
		for id, prof := range p.Delegations {
			d, ok := byID[id]
			if !ok || duplicate {
				return invalidf("Delegationsprofil für die unbekannte oder mehrdeutige Entscheidung %q", id)
			}
			if d.Selection == nil {
				return invalidf("Entscheidung %q hat keine Auswahl und damit keine Delegation", id)
			}
			if prof.Role != d.Selection.Role || prof.Model != d.Selection.Model {
				return invalidf("Delegationsprofil für %q weicht von der Auswahl ab (Rolle %q/%q, Modell %q/%q); Rolle und Modell werden nie ersetzt",
					id, prof.Role, d.Selection.Role, prof.Model, d.Selection.Model)
			}
			praw, err := json.Marshal(prof)
			if err != nil {
				return wrapf(CodeInternal, err, "Delegationsprofil")
			}
			if err := domain.Validate(domain.TargetAgentProfile, praw); err != nil {
				return invalid(err)
			}
			profiles[id] = string(praw)
		}
		rejections, err := jsonText(append([]supervisor.Rejection{}, p.Rejections...))
		if err != nil {
			return err
		}
		var attempt any
		if p.Attempt != nil {
			text, err := jsonText(p.Attempt)
			if err != nil {
				return err
			}
			if len(text) > domain.MaxMessageBytes {
				return invalidf("das Protokoll des Planungsversuchs ist größer als %d Bytes", domain.MaxMessageBytes)
			}
			attempt = text
		}
		created := p.CreatedAt
		if created.IsZero() {
			created = t.at
		}
		if _, err := t.revision(project); err != nil {
			return err
		}
		if _, err := t.q.ExecContext(t.ctx, `INSERT INTO plans (project_id, batch_id, state_revision, goal_kind, goal_id, batch_json, verdict,
			rejections_json, planning_snapshot_sha256, current_snapshot_sha256, attempt_json, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			project, p.Batch.BatchID, p.Batch.StateRevision, string(p.Batch.Goal.Kind), p.Batch.Goal.ID, string(raw), string(p.Verdict), rejections,
			p.PlanningSnapshotSHA256, p.CurrentSnapshotSHA256, attempt, fmtTime(created)); err != nil {
			return err
		}
		if !duplicate {
			for i, d := range p.Batch.Decisions {
				draw, err := json.Marshal(d)
				if err != nil {
					return wrapf(CodeInternal, err, "Entscheidung")
				}
				validation := supervisor.VerdictAccepted
				if rejectedBatch || rejectedDecision[d.DecisionID] {
					validation = supervisor.VerdictRejected
				}
				var prof any
				if text, ok := profiles[d.DecisionID]; ok {
					prof = text
				}
				if _, err := t.q.ExecContext(t.ctx, `INSERT INTO decisions (project_id, batch_id, decision_id, position, action, target_kind, target_id,
					decision_json, validation, delegation_profile_json) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, project, p.Batch.BatchID, d.DecisionID, i,
					string(d.Action), string(d.Target.Kind), d.Target.ID, string(draw), string(validation), prof); err != nil {
					return err
				}
			}
		}
		return t.journal(project, eventSpec{level: domain.AuditLevelDecision, kind: domain.AuditPlanRecorded, entityType: "plan", entityID: p.Batch.BatchID,
			to: string(p.Verdict), details: map[string]string{"goal_kind": string(p.Batch.Goal.Kind), "goal_id": p.Batch.Goal.ID,
				"plan_revision": itoa(p.Batch.StateRevision), "decisions": itoa(int64(len(p.Batch.Decisions))), "rejections": itoa(int64(len(p.Rejections)))}})
	})
}

const planColumns = `batch_id, batch_json, verdict, rejections_json, planning_snapshot_sha256, current_snapshot_sha256, attempt_json, created_at`

// Plan liefert einen gespeicherten Plan mit Ablehnungen und Delegationsprofilen.
func (r *reader) Plan(projectID, batchID string) (PlanRecord, error) {
	rows, err := r.plans("WHERE project_id = ? AND batch_id = ?", projectID, batchID)
	if err != nil {
		return PlanRecord{}, err
	}
	if len(rows) == 0 {
		return PlanRecord{}, errf(CodeNotFound, "Plan %q existiert nicht", batchID)
	}
	return rows[0], nil
}

// Plans liefert alle Pläne des Projekts nach Erstellung.
func (r *reader) Plans(projectID string) ([]PlanRecord, error) {
	return r.plans("WHERE project_id = ?", projectID)
}

func (r *reader) plans(where string, args ...any) ([]PlanRecord, error) {
	rows, err := r.q.QueryContext(r.ctx, "SELECT "+planColumns+" FROM plans "+where+" ORDER BY created_at, batch_id", args...)
	if err != nil {
		return nil, mapErr(err)
	}
	var out []PlanRecord
	for rows.Next() {
		var (
			p                                  PlanRecord
			id, batch, verdict, rej, plan, cur string
			attempt                            sql.NullString
			created                            string
		)
		if err := rows.Scan(&id, &batch, &verdict, &rej, &plan, &cur, &attempt, &created); err != nil {
			rows.Close()
			return nil, mapErr(err)
		}
		if p.Batch, err = domain.ParseDecisionBatch([]byte(batch)); err != nil {
			rows.Close()
			return nil, wrapf(CodeInternal, err, "gespeicherter Batch %q", id)
		}
		p.Verdict, p.PlanningSnapshotSHA256, p.CurrentSnapshotSHA256 = supervisor.Verdict(verdict), plan, cur
		if err := decodeStrict(rej, &p.Rejections); err != nil {
			rows.Close()
			return nil, err
		}
		if attempt.Valid {
			p.Attempt = new(supervisor.Attempt)
			if err := decodeLoose(attempt.String, p.Attempt); err != nil {
				rows.Close()
				return nil, err
			}
		}
		if p.CreatedAt, err = scanTime(created); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, mapErr(err)
	}
	for i := range out {
		ds, err := r.Decisions(out[i].Batch.ProjectID, out[i].Batch.BatchID)
		if err != nil {
			return nil, err
		}
		for _, d := range ds {
			if d.Delegation != nil {
				if out[i].Delegations == nil {
					out[i].Delegations = map[string]domain.AgentProfile{}
				}
				out[i].Delegations[d.Decision.DecisionID] = *d.Delegation
			}
		}
	}
	return out, nil
}

// Decisions liefert die Entscheidungen eines Batches in der Reihenfolge des Batches (leer bei einem Batch, der nur
// als abgelehnt wegen doppelter decision_id gespeichert ist).
func (r *reader) Decisions(projectID, batchID string) ([]DecisionRecord, error) {
	rows, err := r.q.QueryContext(r.ctx, `SELECT position, decision_json, validation, delegation_profile_json FROM decisions
		WHERE project_id = ? AND batch_id = ? ORDER BY position`, projectID, batchID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	var out []DecisionRecord
	for rows.Next() {
		var (
			d          DecisionRecord
			raw, valid string
			prof       sql.NullString
		)
		if err := rows.Scan(&d.Position, &raw, &valid, &prof); err != nil {
			return nil, mapErr(err)
		}
		if d.Decision, err = domain.ParseDecision([]byte(raw)); err != nil {
			return nil, wrapf(CodeInternal, err, "gespeicherte Entscheidung")
		}
		d.Validation = supervisor.Verdict(valid)
		if prof.Valid {
			p, err := domain.ParseAgentProfile([]byte(prof.String))
			if err != nil {
				return nil, wrapf(CodeInternal, err, "gespeichertes Delegationsprofil")
			}
			d.Delegation = &p
		}
		out = append(out, d)
	}
	return out, mapErr(rows.Err())
}
