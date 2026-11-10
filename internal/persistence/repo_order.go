package persistence

import (
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/alexwenzel/ccmas/internal/domain"
	"github.com/alexwenzel/ccmas/internal/supervisor"
)

// WorkOrderRecord ist ein Arbeitsauftrag mit allem, was der ProjectSnapshot je Auftrag braucht. Der Vertragsteil
// ist domain.WorkOrder (Zustand = fachlicher Auftragszustand); Art, Titel, Abhängigkeiten, Konzeptstand, Kopf-
// Commit, Rollenergebnis, PR-Stand und Reparaturrunden sind die Felder von supervisor.Order. Revision ist die
// Zeilenrevision für Compare-and-Set (ab 1, steigt mit jeder Änderung des Auftrags und seiner Teile).
type WorkOrderRecord struct {
	domain.WorkOrder
	GoalID              string
	Kind                supervisor.OrderKind
	Title               string
	DependsOn           []string // aufsteigend nach ID
	Concept             *supervisor.ConceptState
	HeadCommit          string
	Branch              string
	LastResult          *supervisor.LastResult
	PR                  *supervisor.PRInfo
	CodeRepairRounds    int
	ConceptRepairRounds int
	Revision            int64
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

func checkConcept(c *supervisor.ConceptState) (*supervisor.ConceptState, error) {
	if c == nil {
		return nil, nil
	}
	out := *c
	switch {
	case c.Version == "":
		return nil, invalidf("die Konzeptversion darf nicht leer sein")
	case c.Version == supervisor.NoConceptVersion:
		return nil, invalidf("%q ist reserviert und kein Konzeptstand", supervisor.NoConceptVersion)
	case c.Class != "" && !has(conceptClasses(), c.Class):
		return nil, invalidf("unbekannte Konzeptklasse %q", c.Class)
	}
	if err := checkText("Konzeptversion", c.Version, 128, false, true); err != nil {
		return nil, err
	}
	out.Criteria = nonNil(c.Criteria)
	for _, k := range out.Criteria {
		if err := checkText("Kriterium", k, 256, false, true); err != nil {
			return nil, err
		}
	}
	if a := c.Approval; a != nil {
		if err := checkText("Freigabeversion", a.Version, 128, false, true); err != nil {
			return nil, err
		}
		if !has(approvers(), a.Approver) {
			return nil, invalidf("unbekannter Freigeber %q", a.Approver)
		}
		ap := *a
		out.Approval = &ap
	}
	return &out, nil
}

func checkPR(p *supervisor.PRInfo) (*supervisor.PRInfo, error) {
	if p == nil {
		return nil, nil
	}
	switch {
	case p.Number < 1:
		return nil, invalidf("PR-Nummer %d ungültig", p.Number)
	case !has(prStates(), p.State):
		return nil, invalidf("unbekannter PR-Stand %q", p.State)
	case !has(ciStates(), p.CI):
		return nil, invalidf("unbekannter CI-Stand %q", p.CI)
	case p.HeadCommit != "" && !isCommit(p.HeadCommit):
		return nil, invalidf("PR-Kopf-Commit ist kein vollständiger Hash")
	}
	out := *p
	return &out, nil
}

func checkLastResult(l *supervisor.LastResult) (*supervisor.LastResult, error) {
	if l == nil {
		return nil, nil
	}
	if !l.Role.Valid() {
		return nil, invalidf("unbekannte Rolle %q", l.Role)
	}
	if err := checkText("Ergebnis", l.Result, 64, false, true); err != nil {
		return nil, err
	}
	out := *l
	return &out, nil
}

func checkHead(commit, branch string) error {
	if commit != "" && !isCommit(commit) {
		return invalidf("Kopf-Commit %q ist kein vollständiger Hash", commit)
	}
	return checkText("Branch", branch, 255, false, false)
}

// CreateWorkOrder legt einen Auftrag mit seinen Teilen an (Abhängigkeiten, Konzeptstand, PR). Der Vertragsteil wird
// gegen das Schema work-order geprüft, bevor etwas geschrieben wird. Der Auftrag beginnt mit Revision 1.
func (t *Tx) CreateWorkOrder(o WorkOrderRecord) (WorkOrderRecord, error) {
	var out WorkOrderRecord
	err := t.write(func() error {
		raw, err := json.Marshal(o.WorkOrder)
		if err != nil {
			return wrapf(CodeInternal, err, "Auftrag")
		}
		if err := domain.Validate(domain.TargetWorkOrder, raw); err != nil {
			return invalid(err)
		}
		if err := checkIDs(o.GoalID); err != nil {
			return err
		}
		if !has(supervisor.OrderKinds(), o.Kind) {
			return invalidf("unbekannte Auftragsart %q", o.Kind)
		}
		if err := checkText("Titel", o.Title, 1024, false, false); err != nil {
			return err
		}
		if err := checkHead(o.HeadCommit, o.Branch); err != nil {
			return err
		}
		if o.CodeRepairRounds < 0 || o.ConceptRepairRounds < 0 {
			return invalidf("Reparaturrunden dürfen nicht negativ sein")
		}
		deps := slices.Clone(o.DependsOn)
		slices.Sort(deps)
		for i, d := range deps {
			if err := checkIDs(d); err != nil {
				return err
			}
			if d == o.WorkOrderID {
				return invalidf("Auftrag %q kann nicht von sich selbst abhängen", d)
			}
			if i > 0 && deps[i-1] == d {
				return invalidf("Abhängigkeit %q kommt mehrfach vor", d)
			}
		}
		concept, err := checkConcept(o.Concept)
		if err != nil {
			return err
		}
		pr, err := checkPR(o.PR)
		if err != nil {
			return err
		}
		last, err := checkLastResult(o.LastResult)
		if err != nil {
			return err
		}
		crit, err := jsonText(o.AcceptanceCriteria)
		if err != nil {
			return err
		}
		scope, err := jsonText(o.AllowedScope)
		if err != nil {
			return err
		}
		if _, err := t.bump(o.ProjectID); err != nil {
			return err
		}
		var lastRole, lastResult any
		if last != nil {
			lastRole, lastResult = string(last.Role), last.Result
		}
		at := fmtTime(t.at)
		if _, err := t.q.ExecContext(t.ctx, `INSERT INTO work_orders (work_order_id, project_id, goal_id, target_kind, target_ref, user_order, goal,
			acceptance_criteria_json, allowed_scope_json, mode, plan_version, state, order_kind, title, head_commit, branch,
			code_repair_rounds, concept_repair_rounds, last_result_role, last_result_result, revision, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?)`,
			o.WorkOrderID, o.ProjectID, o.GoalID, string(o.Target.Kind), o.Target.Ref, o.UserOrder, o.Goal, crit, scope, string(o.Mode),
			o.PlanVersion, string(o.State), string(o.Kind), o.Title, o.HeadCommit, o.Branch, o.CodeRepairRounds, o.ConceptRepairRounds,
			lastRole, lastResult, at, at); err != nil {
			return err
		}
		for _, d := range deps {
			if _, err := t.q.ExecContext(t.ctx, "INSERT INTO work_order_dependencies (project_id, work_order_id, depends_on_id) VALUES (?, ?, ?)",
				o.ProjectID, o.WorkOrderID, d); err != nil {
				return err
			}
		}
		if concept != nil {
			if err := t.putConcept(o.ProjectID, o.WorkOrderID, concept); err != nil {
				return err
			}
		}
		if pr != nil {
			if err := t.putPR(o.ProjectID, o.WorkOrderID, pr); err != nil {
				return err
			}
		}
		out = o
		out.DependsOn, out.Concept, out.PR, out.LastResult = nonNil(deps), concept, pr, last
		out.AcceptanceCriteria, out.AllowedScope = slices.Clone(o.AcceptanceCriteria), slices.Clone(o.AllowedScope)
		out.Revision, out.CreatedAt, out.UpdatedAt = 1, t.at, t.at
		return t.emit(o.ProjectID, eventSpec{level: domain.AuditLevelWorkOrder, kind: domain.AuditWorkOrderCreated, entityType: "work_order",
			entityID: o.WorkOrderID, to: string(o.State),
			details: map[string]string{"goal_id": o.GoalID, "kind": string(o.Kind), "mode": string(o.Mode),
				"plan_version": itoa(o.PlanVersion), "depends_on": itoa(int64(len(deps)))}})
	})
	return out, err
}

func (t *Tx) putConcept(project, id string, c *supervisor.ConceptState) error {
	crit, err := jsonText(c.Criteria)
	if err != nil {
		return err
	}
	var av, ap any
	if c.Approval != nil {
		av, ap = c.Approval.Version, string(c.Approval.Approver)
	}
	_, err = t.q.ExecContext(t.ctx, `INSERT INTO concept_states (project_id, work_order_id, version, class, criteria_json, approval_version, approver, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (work_order_id) DO UPDATE SET version = excluded.version, class = excluded.class, criteria_json = excluded.criteria_json,
			approval_version = excluded.approval_version, approver = excluded.approver, updated_at = excluded.updated_at`,
		project, id, c.Version, string(c.Class), crit, av, ap, fmtTime(t.at))
	return err
}

func (t *Tx) putPR(project, id string, p *supervisor.PRInfo) error {
	_, err := t.q.ExecContext(t.ctx, `INSERT INTO pull_requests (project_id, work_order_id, number, state, ci, head_commit, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (work_order_id) DO UPDATE SET number = excluded.number, state = excluded.state, ci = excluded.ci,
			head_commit = excluded.head_commit, updated_at = excluded.updated_at`,
		project, id, p.Number, string(p.State), string(p.CI), p.HeadCommit, fmtTime(t.at))
	return err
}

// touchOrder erhöht die Zeilenrevision des Auftrags. Es liefert not_found, wenn es den Auftrag nicht gibt.
func (t *Tx) touchOrder(project, id string) error {
	res, err := t.q.ExecContext(t.ctx, "UPDATE work_orders SET revision = revision + 1, updated_at = ? WHERE project_id = ? AND work_order_id = ?",
		fmtTime(t.at), project, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errf(CodeNotFound, "Auftrag %q existiert nicht", id)
	}
	return nil
}

// TransitionWorkOrder ändert den Auftragszustand von from nach to. Nur Übergänge der Übergangstabelle sind erlaubt
// (sonst Code state); from muss der gespeicherte Zustand sein (sonst conflict). Ausführungen, Prozesse und Agenten
// bleiben unberührt.
func (t *Tx) TransitionWorkOrder(projectID, id string, from, to domain.WorkOrderState) error {
	return t.write(func() error {
		if err := checkIDs(projectID, id); err != nil {
			return err
		}
		if !from.CanTransition(to) {
			return stateErr("Auftragszustand %q -> %q ist nicht erlaubt", from, to)
		}
		var cur string
		err := t.q.QueryRowContext(t.ctx, "SELECT state FROM work_orders WHERE project_id = ? AND work_order_id = ?", projectID, id).Scan(&cur)
		if errors.Is(err, sql.ErrNoRows) {
			return errf(CodeNotFound, "Auftrag %q existiert nicht", id)
		}
		if err != nil {
			return err
		}
		if cur != string(from) {
			return errf(CodeConflict, "Auftrag %q ist %q, erwartet war %q", id, cur, from)
		}
		if _, err := t.q.ExecContext(t.ctx, "UPDATE work_orders SET state = ?, revision = revision + 1, updated_at = ? WHERE project_id = ? AND work_order_id = ?",
			string(to), fmtTime(t.at), projectID, id); err != nil {
			return err
		}
		return t.change(projectID, eventSpec{level: domain.AuditLevelWorkOrder, kind: domain.AuditWorkOrderStateChanged,
			entityType: "work_order", entityID: id, from: string(from), to: string(to)})
	})
}

// RequireWorkOrderRevision verlangt, dass die Zeilenrevision des Auftrags want ist (Compare-and-Set für
// Reservierungen: innerhalb derselben Update-Transaktion folgt der Schreibvorgang ohne Lücke).
func (t *Tx) RequireWorkOrderRevision(projectID, id string, want int64) error {
	return t.write(func() error {
		var cur int64
		err := t.q.QueryRowContext(t.ctx, "SELECT revision FROM work_orders WHERE project_id = ? AND work_order_id = ?", projectID, id).Scan(&cur)
		if errors.Is(err, sql.ErrNoRows) {
			return errf(CodeNotFound, "Auftrag %q existiert nicht", id)
		}
		if err != nil {
			return err
		}
		if cur != want {
			return errf(CodeConflict, "Auftrag %q hat Revision %d, erwartet war %d", id, cur, want)
		}
		return nil
	})
}

// SetWorkOrderHead setzt Kopf-Commit und Branch des Auftrags.
func (t *Tx) SetWorkOrderHead(projectID, id, headCommit, branch string) error {
	return t.write(func() error {
		if err := checkIDs(projectID, id); err != nil {
			return err
		}
		if err := checkHead(headCommit, branch); err != nil {
			return err
		}
		var c, b string
		err := t.q.QueryRowContext(t.ctx, "SELECT head_commit, branch FROM work_orders WHERE project_id = ? AND work_order_id = ?", projectID, id).Scan(&c, &b)
		if errors.Is(err, sql.ErrNoRows) {
			return errf(CodeNotFound, "Auftrag %q existiert nicht", id)
		}
		if err != nil {
			return err
		}
		if c == headCommit && b == branch {
			return nil
		}
		if _, err := t.q.ExecContext(t.ctx, "UPDATE work_orders SET head_commit = ?, branch = ?, revision = revision + 1, updated_at = ? WHERE project_id = ? AND work_order_id = ?",
			headCommit, branch, fmtTime(t.at), projectID, id); err != nil {
			return err
		}
		return t.change(projectID, eventSpec{level: domain.AuditLevelWorkOrder, kind: domain.AuditWorkOrderHeadSet,
			entityType: "work_order", entityID: id, details: map[string]string{"head_commit": headCommit, "branch": branch}})
	})
}

// SetLastResult setzt das zuletzt gemeldete Rollenergebnis (nil: keines).
func (t *Tx) SetLastResult(projectID, id string, r *supervisor.LastResult) error {
	return t.write(func() error {
		if err := checkIDs(projectID, id); err != nil {
			return err
		}
		r, err := checkLastResult(r)
		if err != nil {
			return err
		}
		var role, result sql.NullString
		err = t.q.QueryRowContext(t.ctx, "SELECT last_result_role, last_result_result FROM work_orders WHERE project_id = ? AND work_order_id = ?", projectID, id).Scan(&role, &result)
		if errors.Is(err, sql.ErrNoRows) {
			return errf(CodeNotFound, "Auftrag %q existiert nicht", id)
		}
		if err != nil {
			return err
		}
		var nr, nres any
		details := map[string]string{"result": "cleared"}
		if r != nil {
			nr, nres = string(r.Role), r.Result
			details = map[string]string{"role": string(r.Role), "result": r.Result}
			if role.Valid && role.String == string(r.Role) && result.String == r.Result {
				return nil
			}
		} else if !role.Valid {
			return nil
		}
		if _, err := t.q.ExecContext(t.ctx, "UPDATE work_orders SET last_result_role = ?, last_result_result = ?, revision = revision + 1, updated_at = ? WHERE project_id = ? AND work_order_id = ?",
			nr, nres, fmtTime(t.at), projectID, id); err != nil {
			return err
		}
		return t.change(projectID, eventSpec{level: domain.AuditLevelWorkOrder, kind: domain.AuditWorkOrderResultSet,
			entityType: "work_order", entityID: id, details: details})
	})
}

// SetRepairRounds setzt die Reparaturrunden der Code- und der Konzeptschleife.
func (t *Tx) SetRepairRounds(projectID, id string, code, concept int) error {
	return t.write(func() error {
		if err := checkIDs(projectID, id); err != nil {
			return err
		}
		if code < 0 || concept < 0 {
			return invalidf("Reparaturrunden dürfen nicht negativ sein")
		}
		var c, k int
		err := t.q.QueryRowContext(t.ctx, "SELECT code_repair_rounds, concept_repair_rounds FROM work_orders WHERE project_id = ? AND work_order_id = ?", projectID, id).Scan(&c, &k)
		if errors.Is(err, sql.ErrNoRows) {
			return errf(CodeNotFound, "Auftrag %q existiert nicht", id)
		}
		if err != nil {
			return err
		}
		if c == code && k == concept {
			return nil
		}
		if _, err := t.q.ExecContext(t.ctx, "UPDATE work_orders SET code_repair_rounds = ?, concept_repair_rounds = ?, revision = revision + 1, updated_at = ? WHERE project_id = ? AND work_order_id = ?",
			code, concept, fmtTime(t.at), projectID, id); err != nil {
			return err
		}
		return t.change(projectID, eventSpec{level: domain.AuditLevelWorkOrder, kind: domain.AuditWorkOrderRepairRounds,
			entityType: "work_order", entityID: id, details: map[string]string{"code": itoa(int64(code)), "concept": itoa(int64(concept))}})
	})
}

// SetConceptState setzt den Konzeptstand des Auftrags (nil: keiner). Die Kennung supervisor.NoConceptVersion ist
// reserviert und kein Konzeptstand.
func (t *Tx) SetConceptState(projectID, id string, c *supervisor.ConceptState) error {
	return t.write(func() error {
		if err := checkIDs(projectID, id); err != nil {
			return err
		}
		c, err := checkConcept(c)
		if err != nil {
			return err
		}
		cur, err := t.conceptOf(projectID, id)
		if err != nil {
			return err
		}
		if equalConcept(cur, c) {
			return nil
		}
		details := map[string]string{"concept": "cleared"}
		if c == nil {
			if _, err := t.q.ExecContext(t.ctx, "DELETE FROM concept_states WHERE project_id = ? AND work_order_id = ?", projectID, id); err != nil {
				return err
			}
		} else {
			if err := t.putConcept(projectID, id, c); err != nil {
				return err
			}
			details = map[string]string{"version": c.Version, "class": string(c.Class), "approved": boolStr(c.Approval != nil)}
		}
		if err := t.touchOrder(projectID, id); err != nil {
			return err
		}
		return t.change(projectID, eventSpec{level: domain.AuditLevelWorkOrder, kind: domain.AuditWorkOrderConceptSet,
			entityType: "work_order", entityID: id, details: details})
	})
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func equalConcept(a, b *supervisor.ConceptState) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.Version != b.Version || a.Class != b.Class || !slices.Equal(a.Criteria, b.Criteria) {
		return false
	}
	if a.Approval == nil || b.Approval == nil {
		return a.Approval == b.Approval
	}
	return *a.Approval == *b.Approval
}

// SetPullRequest setzt den PR-Stand des Auftrags (nil: kein PR).
func (t *Tx) SetPullRequest(projectID, id string, p *supervisor.PRInfo) error {
	return t.write(func() error {
		if err := checkIDs(projectID, id); err != nil {
			return err
		}
		p, err := checkPR(p)
		if err != nil {
			return err
		}
		if _, err := t.workOrderRevision(projectID, id); err != nil {
			return err
		}
		cur, err := t.prOf(projectID, id)
		if err != nil {
			return err
		}
		if (cur == nil) == (p == nil) && (p == nil || *cur == *p) {
			return nil
		}
		details := map[string]string{"pull_request": "cleared"}
		if p == nil {
			if _, err := t.q.ExecContext(t.ctx, "DELETE FROM pull_requests WHERE project_id = ? AND work_order_id = ?", projectID, id); err != nil {
				return err
			}
		} else {
			if err := t.putPR(projectID, id, p); err != nil {
				return err
			}
			details = map[string]string{"number": itoa(p.Number), "state": string(p.State), "ci": string(p.CI)}
		}
		if err := t.touchOrder(projectID, id); err != nil {
			return err
		}
		return t.change(projectID, eventSpec{level: domain.AuditLevelWorkOrder, kind: domain.AuditWorkOrderPRSet,
			entityType: "work_order", entityID: id, details: details})
	})
}

// AddDependency fügt eine Abhängigkeit hinzu. Zyklen prüft der Validator in Go (nicht die Datenbank); die Datenbank
// verhindert Selbstabhängigkeit und verwaiste Bezüge. Eine vorhandene Abhängigkeit ist keine Änderung.
func (t *Tx) AddDependency(projectID, id, dependsOn string) error {
	return t.write(func() error {
		if err := checkIDs(projectID, id, dependsOn); err != nil {
			return err
		}
		if id == dependsOn {
			return invalidf("Auftrag %q kann nicht von sich selbst abhängen", id)
		}
		if _, err := t.workOrderRevision(projectID, id); err != nil {
			return err
		}
		var one int
		err := t.q.QueryRowContext(t.ctx, "SELECT 1 FROM work_order_dependencies WHERE project_id = ? AND work_order_id = ? AND depends_on_id = ?",
			projectID, id, dependsOn).Scan(&one)
		if err == nil {
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if _, err := t.q.ExecContext(t.ctx, "INSERT INTO work_order_dependencies (project_id, work_order_id, depends_on_id) VALUES (?, ?, ?)",
			projectID, id, dependsOn); err != nil {
			return err
		}
		if err := t.touchOrder(projectID, id); err != nil {
			return err
		}
		return t.change(projectID, eventSpec{level: domain.AuditLevelWorkOrder, kind: domain.AuditWorkOrderDependency,
			entityType: "work_order", entityID: id, details: map[string]string{"depends_on": dependsOn}})
	})
}

// workOrderRevision liefert die Zeilenrevision oder not_found.
func (r *reader) workOrderRevision(projectID, id string) (int64, error) {
	var rev int64
	err := r.q.QueryRowContext(r.ctx, "SELECT revision FROM work_orders WHERE project_id = ? AND work_order_id = ?", projectID, id).Scan(&rev)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, errf(CodeNotFound, "Auftrag %q existiert nicht", id)
	}
	return rev, mapErr(err)
}

func (r *reader) conceptOf(projectID, id string) (*supervisor.ConceptState, error) {
	if _, err := r.workOrderRevision(projectID, id); err != nil {
		return nil, err
	}
	var (
		c      supervisor.ConceptState
		class  string
		crit   string
		av, ap sql.NullString
	)
	err := r.q.QueryRowContext(r.ctx, "SELECT version, class, criteria_json, approval_version, approver FROM concept_states WHERE project_id = ? AND work_order_id = ?",
		projectID, id).Scan(&c.Version, &class, &crit, &av, &ap)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, mapErr(err)
	}
	c.Class = conceptClassOf(class)
	if err := decodeStrict(crit, &c.Criteria); err != nil {
		return nil, err
	}
	if av.Valid {
		c.Approval = &supervisor.ConceptApprovalInfo{Version: av.String, Approver: approverOf(ap.String)}
	}
	return &c, nil
}

func (r *reader) prOf(projectID, id string) (*supervisor.PRInfo, error) {
	var (
		p         supervisor.PRInfo
		state, ci string
	)
	err := r.q.QueryRowContext(r.ctx, "SELECT number, state, ci, head_commit FROM pull_requests WHERE project_id = ? AND work_order_id = ?",
		projectID, id).Scan(&p.Number, &state, &ci, &p.HeadCommit)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, mapErr(err)
	}
	p.State, p.CI = supervisor.PRStatus(state), supervisor.CIStatus(ci)
	return &p, nil
}

const orderSelect = `SELECT w.work_order_id, w.project_id, w.goal_id, w.target_kind, w.target_ref, w.user_order, w.goal,
	w.acceptance_criteria_json, w.allowed_scope_json, w.mode, w.plan_version, w.state, w.order_kind, w.title, w.head_commit, w.branch,
	w.code_repair_rounds, w.concept_repair_rounds, w.last_result_role, w.last_result_result, w.revision, w.created_at, w.updated_at,
	c.version, c.class, c.criteria_json, c.approval_version, c.approver,
	p.number, p.state, p.ci, p.head_commit
	FROM work_orders w
	LEFT JOIN concept_states c ON c.work_order_id = w.work_order_id
	LEFT JOIN pull_requests p ON p.work_order_id = w.work_order_id`

func scanOrder(sc interface{ Scan(...any) error }) (WorkOrderRecord, error) {
	var (
		o                                  WorkOrderRecord
		tk, mode, state, kind, crit, scope string
		created, updated                   string
		lastRole, lastResult               sql.NullString
		cVersion, cClass, cCrit, cAV, cAP  sql.NullString
		pNumber                            sql.NullInt64
		pState, pCI, pHead                 sql.NullString
	)
	if err := sc.Scan(&o.WorkOrderID, &o.ProjectID, &o.GoalID, &tk, &o.Target.Ref, &o.UserOrder, &o.Goal, &crit, &scope, &mode, &o.PlanVersion,
		&state, &kind, &o.Title, &o.HeadCommit, &o.Branch, &o.CodeRepairRounds, &o.ConceptRepairRounds, &lastRole, &lastResult,
		&o.Revision, &created, &updated, &cVersion, &cClass, &cCrit, &cAV, &cAP, &pNumber, &pState, &pCI, &pHead); err != nil {
		return o, err
	}
	o.Target.Kind, o.Mode, o.State, o.Kind = domain.TargetKind(tk), domain.IntegrationMode(mode), domain.WorkOrderState(state), supervisor.OrderKind(kind)
	if err := decodeStrict(crit, &o.AcceptanceCriteria); err != nil {
		return o, err
	}
	if err := decodeStrict(scope, &o.AllowedScope); err != nil {
		return o, err
	}
	if lastRole.Valid {
		o.LastResult = &supervisor.LastResult{Role: domain.Role(lastRole.String), Result: lastResult.String}
	}
	if cVersion.Valid {
		c := &supervisor.ConceptState{Version: cVersion.String, Class: conceptClassOf(cClass.String)}
		if err := decodeStrict(cCrit.String, &c.Criteria); err != nil {
			return o, err
		}
		if cAV.Valid {
			c.Approval = &supervisor.ConceptApprovalInfo{Version: cAV.String, Approver: approverOf(cAP.String)}
		}
		o.Concept = c
	}
	if pNumber.Valid {
		o.PR = &supervisor.PRInfo{Number: pNumber.Int64, State: supervisor.PRStatus(pState.String), CI: supervisor.CIStatus(pCI.String), HeadCommit: pHead.String}
	}
	var err error
	if o.CreatedAt, err = scanTime(created); err != nil {
		return o, err
	}
	o.UpdatedAt, err = scanTime(updated)
	return o, err
}

// WorkOrder liefert einen Auftrag mit allen Teilen.
func (r *reader) WorkOrder(projectID, id string) (WorkOrderRecord, error) {
	o, err := scanOrder(r.q.QueryRowContext(r.ctx, orderSelect+" WHERE w.project_id = ? AND w.work_order_id = ?", projectID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return o, errf(CodeNotFound, "Auftrag %q existiert nicht", id)
	}
	if err != nil {
		return o, mapErr(err)
	}
	deps, err := r.dependencies(projectID, id)
	if err != nil {
		return o, err
	}
	o.DependsOn = deps[id]
	if o.DependsOn == nil {
		o.DependsOn = []string{}
	}
	return o, nil
}

// WorkOrders liefert alle Aufträge des Projekts nach ID.
func (r *reader) WorkOrders(projectID string) ([]WorkOrderRecord, error) {
	return r.orders(orderSelect+" WHERE w.project_id = ? ORDER BY w.work_order_id", projectID)
}

// WorkOrdersOfGoal liefert die Aufträge eines Ziels nach ID.
func (r *reader) WorkOrdersOfGoal(projectID, goalID string) ([]WorkOrderRecord, error) {
	return r.orders(orderSelect+" WHERE w.project_id = ? AND w.goal_id = ? ORDER BY w.work_order_id", projectID, goalID)
}

func (r *reader) orders(query string, args ...any) ([]WorkOrderRecord, error) {
	rows, err := r.q.QueryContext(r.ctx, query, args...)
	if err != nil {
		return nil, mapErr(err)
	}
	var out []WorkOrderRecord
	for rows.Next() {
		o, err := scanOrder(rows)
		if err != nil {
			rows.Close()
			return nil, mapErr(err)
		}
		out = append(out, o)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, mapErr(err)
	}
	if len(out) == 0 {
		return out, nil
	}
	deps, err := r.dependencies(out[0].ProjectID, "")
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].DependsOn = deps[out[i].WorkOrderID]
		if out[i].DependsOn == nil {
			out[i].DependsOn = []string{}
		}
	}
	return out, nil
}

// dependencies liefert die Abhängigkeiten je Auftrag (id leer: alle des Projekts), aufsteigend sortiert.
func (r *reader) dependencies(projectID, id string) (map[string][]string, error) {
	q := "SELECT work_order_id, depends_on_id FROM work_order_dependencies WHERE project_id = ?"
	args := []any{projectID}
	if id != "" {
		q += " AND work_order_id = ?"
		args = append(args, id)
	}
	rows, err := r.q.QueryContext(r.ctx, q+" ORDER BY work_order_id, depends_on_id", args...)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := map[string][]string{}
	for rows.Next() {
		var a, b string
		if err := rows.Scan(&a, &b); err != nil {
			return nil, mapErr(err)
		}
		out[a] = append(out[a], b)
	}
	return out, mapErr(rows.Err())
}
