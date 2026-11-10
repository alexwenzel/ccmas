package persistence

import (
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/alexwenzel/ccmas/internal/domain"
	"github.com/alexwenzel/ccmas/internal/supervisor"
)

// RecordReview speichert einen Review-Bericht (geprüft gegen das Schema und seine Invarianten). Reviewer- und
// Implementierer-Ausführung (optional) müssen gespeichert sein und zum Auftrag des Berichts gehören. Die Rolle der
// Reviewer-Ausführung wird nicht zusätzlich gespeichert, sondern aus der Ausführung gelesen (LastReview).
// Die Konzeptversion des Berichts darf supervisor.NoConceptVersion tragen (Aufträge ohne Konzeptbezug).
func (t *Tx) RecordReview(projectID string, r domain.ReviewReport, implementerExecutionID string) error {
	return t.write(func() error {
		if err := checkIDs(projectID); err != nil {
			return err
		}
		if err := checkOptionalID(implementerExecutionID); err != nil {
			return err
		}
		raw, err := json.Marshal(r)
		if err != nil {
			return wrapf(CodeInternal, err, "Review-Bericht")
		}
		parsed, err := domain.ParseReviewReport(raw)
		if err != nil {
			return invalid(err)
		}
		canon, err := json.Marshal(parsed)
		if err != nil {
			return wrapf(CodeInternal, err, "Review-Bericht")
		}
		findings, err := jsonText(parsed.Findings)
		if err != nil {
			return err
		}
		if _, err := t.workOrderRevision(projectID, r.WorkOrderID); err != nil {
			return err
		}
		for _, id := range []string{r.ReviewerExecutionID, implementerExecutionID} {
			if id == "" {
				continue
			}
			e, err := t.Execution(projectID, id)
			if err != nil {
				return err
			}
			if e.WorkOrderID != "" && e.WorkOrderID != r.WorkOrderID {
				return invalidf("die Ausführung %q gehört zu Auftrag %q, der Bericht zu %q", id, e.WorkOrderID, r.WorkOrderID)
			}
		}
		if _, err := t.bump(projectID); err != nil {
			return err
		}
		if _, err := t.q.ExecContext(t.ctx, `INSERT INTO review_reports (report_id, project_id, work_order_id, reviewer_execution_id,
			implementer_execution_id, commit_sha, concept_version, verdict, findings_json, report_json, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			parsed.ReportID, projectID, parsed.WorkOrderID, parsed.ReviewerExecutionID, orNil(implementerExecutionID), parsed.Commit,
			parsed.ConceptVersion, string(parsed.Verdict), findings, string(canon), fmtTime(parsed.CreatedAt)); err != nil {
			return err
		}
		return t.emit(projectID, eventSpec{level: domain.AuditLevelReview, kind: domain.AuditReviewRecorded, entityType: "review",
			entityID: parsed.ReportID, details: map[string]string{"work_order_id": parsed.WorkOrderID, "verdict": string(parsed.Verdict),
				"commit": parsed.Commit, "concept_version": parsed.ConceptVersion, "reviewer_execution_id": parsed.ReviewerExecutionID,
				"findings": itoa(int64(len(parsed.Findings)))}})
	})
}

const reviewSelect = `SELECT r.report_json, e.role, r.implementer_execution_id FROM review_reports r
	JOIN executions e ON e.project_id = r.project_id AND e.execution_id = r.reviewer_execution_id`

func scanReview(sc interface{ Scan(...any) error }) (supervisor.ReviewRecord, error) {
	var (
		rec  supervisor.ReviewRecord
		raw  string
		role string
		impl sql.NullString
	)
	if err := sc.Scan(&raw, &role, &impl); err != nil {
		return rec, err
	}
	rep, err := domain.ParseReviewReport([]byte(raw))
	if err != nil {
		return rec, wrapf(CodeInternal, err, "gespeicherter Review-Bericht")
	}
	rec.Report, rec.ReviewerRole, rec.ImplementerExecutionID = rep, domain.Role(role), str(impl)
	return rec, nil
}

// Reviews liefert die Berichte zu einem Auftrag nach Erstellung (älteste zuerst) in der Form des Snapshots: Bericht,
// Rolle der Reviewer-Ausführung (per Join) und Implementierer-Ausführung.
func (r *reader) Reviews(projectID, workOrderID string) ([]supervisor.ReviewRecord, error) {
	rows, err := r.q.QueryContext(r.ctx, reviewSelect+" WHERE r.project_id = ? AND r.work_order_id = ? ORDER BY r.created_at, r.rowid", projectID, workOrderID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	var out []supervisor.ReviewRecord
	for rows.Next() {
		rec, err := scanReview(rows)
		if err != nil {
			return nil, mapErr(err)
		}
		out = append(out, rec)
	}
	return out, mapErr(rows.Err())
}

// LastReview liefert den zuletzt erstellten Bericht des Auftrags (nil, wenn es keinen gibt).
func (r *reader) LastReview(projectID, workOrderID string) (*supervisor.ReviewRecord, error) {
	rec, err := scanReview(r.q.QueryRowContext(r.ctx, reviewSelect+" WHERE r.project_id = ? AND r.work_order_id = ? ORDER BY r.created_at DESC, r.rowid DESC LIMIT 1",
		projectID, workOrderID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, mapErr(err)
	}
	return &rec, nil
}
