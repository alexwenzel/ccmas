package persistence

import (
	"context"
	"database/sql"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/alexwenzel/ccmas/internal/domain"
	"github.com/alexwenzel/ccmas/internal/supervisor"
)

func tableNames(t *testing.T, s *Store) []string {
	t.Helper()
	rows, err := s.r.QueryContext(ctxT(t), "SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name")
	must(t, err)
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		must(t, rows.Scan(&n))
		out = append(out, n)
	}
	must(t, rows.Err())
	return out
}

func TestAllTablesExist(t *testing.T) {
	s := newStore(t)
	want := []string{
		"agents", "answers", "concept_states", "decisions", "events", "execution_attempts", "executions", "goals",
		"leases", "message_deliveries", "messages", "outbox", "plans", "profile_versions", "projects",
		"pull_requests", "questions", "review_reports", "schema_migrations", "skill_snapshot_skills",
		"skill_snapshots", "tasks", "work_order_dependencies", "work_orders",
	}
	if got := tableNames(t, s); !slices.Equal(got, want) {
		t.Fatalf("Tabellen = %v\nerwartet   %v", got, want)
	}
}

// ---- Aufzählungen: CHECK-Listen entsprechen genau den Go-Listen -------------------------------------------

func strs[T ~string](in []T) []string {
	out := make([]string, len(in))
	for i, v := range in {
		out[i] = string(v)
	}
	return out
}

var checkList = regexp.MustCompile(`(?s)\b%s\s+IN\s*\(([^)]*)\)`)

// checkValues liefert die Werte der ersten CHECK-Liste "<column> IN (...)" der Tabelle.
func checkValues(t *testing.T, s *Store, table, column string) []string {
	t.Helper()
	var ddl string
	must(t, s.r.QueryRowContext(ctxT(t), "SELECT sql FROM sqlite_master WHERE type = 'table' AND name = ?", table).Scan(&ddl))
	re := regexp.MustCompile(strings.Replace(checkList.String(), "%s", regexp.QuoteMeta(column), 1))
	m := re.FindStringSubmatch(ddl)
	if m == nil {
		t.Fatalf("%s.%s: keine CHECK-Liste gefunden", table, column)
	}
	var out []string
	for _, part := range strings.Split(m[1], ",") {
		part = strings.TrimSpace(part)
		if len(part) < 2 || part[0] != '\'' || part[len(part)-1] != '\'' {
			t.Fatalf("%s.%s: unerwartetes Listenelement %q", table, column, part)
		}
		out = append(out, part[1:len(part)-1])
	}
	return out
}

func sorted(in []string) []string {
	out := slices.Clone(in)
	sort.Strings(out)
	return out
}

func TestEnumChecksMatchDomain(t *testing.T) {
	s := newStore(t)
	goalKinds := []string{string(domain.KindTicket), string(domain.KindFeature), string(domain.KindMilestone)}
	roles := strs(domain.Roles())
	modes := strs(domain.IntegrationModes())
	planVerdicts := strs([]supervisor.Verdict{supervisor.VerdictAccepted, supervisor.VerdictRejected})

	cases := []struct {
		table, column string
		want          []string
	}{
		{"profile_versions", "role", roles},
		{"skill_snapshots", "role", roles},
		{"agents", "role", roles},
		{"agents", "availability", strs(domain.AgentAvailabilities())},
		{"executions", "role", roles},
		{"work_orders", "last_result_role", roles},
		{"goals", "kind", goalKinds},
		{"goals", "integration_mode", modes},
		{"work_orders", "target_kind", goalKinds},
		{"work_orders", "mode", modes},
		{"work_orders", "state", strs(domain.WorkOrderStates())},
		{"work_orders", "order_kind", strs(supervisor.OrderKinds())},
		{"concept_states", "class", append([]string{""}, strs(conceptClasses())...)},
		{"concept_states", "approver", strs(approvers())},
		{"pull_requests", "state", strs(prStates())},
		{"pull_requests", "ci", strs(ciStates())},
		{"execution_attempts", "state", strs(domain.ExecutionStates())},
		{"execution_attempts", "os_process_state", strs(domain.OSProcessStates())},
		{"execution_attempts", "terminal_source", domain.TerminalSources()},
		{"execution_attempts", "outcome", strs(domain.Outcomes())},
		{"leases", "state", strs(LeaseStates())},
		{"plans", "goal_kind", goalKinds},
		{"plans", "verdict", planVerdicts},
		{"decisions", "action", strs(domain.DecisionActions())},
		{"decisions", "target_kind", strs(domain.TargetKinds())},
		{"decisions", "validation", planVerdicts},
		{"questions", "state", strs(domain.QuestionStates())},
		{"review_reports", "verdict", strs(domain.Verdicts())},
		{"message_deliveries", "state", strs(DeliveryStates())},
		{"outbox", "state", strs(OutboxStates())},
		{"events", "level", strs(domain.AuditLevels())},
	}
	for _, tc := range cases {
		t.Run(tc.table+"."+tc.column, func(t *testing.T) {
			got := checkValues(t, s, tc.table, tc.column)
			if !slices.Equal(sorted(got), sorted(tc.want)) {
				t.Fatalf("CHECK-Liste %v\nGo-Liste     %v", sorted(got), sorted(tc.want))
			}
		})
	}
}

// TestEnumValuesAcceptedAndUnknownRejected: das Verhalten, nicht nur der Text: jeder Go-Wert wird über die
// Repositories angenommen, ein unbekannter schon in der Datenbank abgewiesen.
func TestEnumValuesAcceptedAndUnknownRejected(t *testing.T) {
	s := newStore(t)
	seedBase(t, s)
	// Jeder Auftragszustand, jede Auftragsart und jeder Modus ist speicherbar.
	i := 0
	for _, st := range domain.WorkOrderStates() {
		for _, k := range supervisor.OrderKinds() {
			i++
			id := "wo-enum-" + string(st) + "-" + string(k)
			w := fxWorkOrder(t, id)
			w.State = st
			mustUpdate(t, s, func(tx *Tx) error {
				_, err := tx.CreateWorkOrder(WorkOrderRecord{WorkOrder: w, GoalID: "ms-1", Kind: k})
				return err
			})
		}
	}
	for _, mode := range domain.IntegrationModes() {
		w := fxWorkOrder(t, "wo-mode-"+string(mode))
		w.Mode = mode
		mustUpdate(t, s, func(tx *Tx) error {
			_, err := tx.CreateWorkOrder(WorkOrderRecord{WorkOrder: w, GoalID: "ms-1", Kind: supervisor.OrderBug})
			return err
		})
	}
	// Rollen und Verfügbarkeiten.
	for _, role := range domain.Roles() {
		mustUpdate(t, s, func(tx *Tx) error {
			_, err := tx.CreateAgent(projectID, "agent-"+string(role), role)
			return err
		})
	}
	// Ein unbekannter Wert scheitert am CHECK der Datenbank (nicht erst in Go).
	for name, stmt := range map[string]string{
		"Auftragszustand": "UPDATE work_orders SET state = 'paused' WHERE work_order_id = 'wo-1'",
		"Auftragsart":     "UPDATE work_orders SET order_kind = 'epic' WHERE work_order_id = 'wo-1'",
		"Modus":           "UPDATE work_orders SET mode = 'manual' WHERE work_order_id = 'wo-1'",
		"Verfügbarkeit":   "UPDATE agents SET availability = 'away' WHERE agent_id = 'agent-1'",
		"Rolle":           "UPDATE agents SET role = 'boss' WHERE agent_id = 'agent-1'",
		"Ebene":           "INSERT INTO events (project_id, occurred_at, actor, kind, entity_type, entity_id, level, state_revision) VALUES ('ccmas', '2026-11-10T12:00:00.000000000Z', 'a', 'x.y', 'e', 'i', 'misc', 0)",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := s.w.ExecContext(ctxT(t), stmt)
			if err == nil {
				t.Fatal("unbekannter Wert angenommen")
			}
			wantCode(t, mapErr(err), CodeConstraint)
		})
	}
}

// ---- Fremdschlüssel -----------------------------------------------------------------------------------------

func TestForeignKeys(t *testing.T) {
	s := newStore(t, WithReadConns(3))
	seedBase(t, s)

	// Auf jedem Pool aktiv.
	ctx := ctxT(t)
	var fk int
	must(t, s.w.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fk))
	if fk != 1 {
		t.Fatal("foreign_keys im Schreibpool aus")
	}
	var conns []*sql.Conn
	for i := 0; i < 3; i++ {
		c, err := s.r.Conn(ctx)
		must(t, err)
		conns = append(conns, c)
		must(t, c.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fk))
		if fk != 1 {
			t.Fatalf("foreign_keys in Lesepool-Verbindung %d aus", i)
		}
	}
	for _, c := range conns {
		c.Close()
	}

	// Verwaiste Bezüge über die Repositories ...
	_, err := s.Update(ctx, "tester", func(tx *Tx) error {
		_, err := tx.CreateWorkOrder(WorkOrderRecord{WorkOrder: fxWorkOrder(t, "wo-orphan"), GoalID: "gibt-es-nicht", Kind: supervisor.OrderBug})
		return err
	})
	if err == nil {
		t.Fatal("Auftrag zu unbekanntem Ziel angenommen")
	}
	_, err = s.Update(ctx, "tester", func(tx *Tx) error {
		return tx.AddDependency(projectID, "wo-1", "wo-gibt-es-nicht")
	})
	if err == nil {
		t.Fatal("Abhängigkeit auf unbekannten Auftrag angenommen")
	}
	_, err = s.Update(ctx, "tester", func(tx *Tx) error {
		return tx.StartAttempt(projectID, domain.ExecutionAttempt{ExecutionID: "exec-nope", Attempt: 1, ProfileID: "impl-default", ProfileVersion: 3,
			State: domain.ExecInitializing, StartedAt: s.now()})
	})
	if err == nil {
		t.Fatal("Versuch zu unbekannter Ausführung angenommen")
	}

	// ... und direkt in der Datenbank.
	for name, stmt := range map[string]string{
		"Abhängigkeit":    "INSERT INTO work_order_dependencies (project_id, work_order_id, depends_on_id) VALUES ('ccmas', 'wo-1', 'wo-x')",
		"Frage":           "INSERT INTO questions (question_id, project_id, work_order_id, execution_id, attempt, text, context, state, created_at) VALUES ('q', 'ccmas', 'wo-1', 'exec-x', 1, 't', 'c', 'open', '2026-11-10T12:00:00.000000000Z')",
		"Ausführung":      "INSERT INTO executions (execution_id, project_id, work_order_id, role, created_at) VALUES ('e', 'ccmas', 'wo-x', 'implementer', '2026-11-10T12:00:00.000000000Z')",
		"anderes Projekt": "INSERT INTO executions (execution_id, project_id, work_order_id, role, created_at) VALUES ('e', 'anderes', 'wo-1', 'implementer', '2026-11-10T12:00:00.000000000Z')",
	} {
		_, err := s.w.ExecContext(ctx, stmt)
		if err == nil {
			t.Errorf("%s: verwaister Bezug angenommen", name)
			continue
		}
		wantCode(t, mapErr(err), CodeConstraint)
	}
}

// ---- Auditverlauf, Unveränderlichkeit, reservierte Kennung ---------------------------------------------------

func TestEventsAppendOnly(t *testing.T) {
	s := newStore(t)
	seedBase(t, s)
	if countEvents(t, s) == 0 {
		t.Fatal("keine Ereignisse")
	}
	for name, stmt := range map[string]string{
		"UPDATE": "UPDATE events SET actor = 'x'",
		"DELETE": "DELETE FROM events",
	} {
		_, err := s.w.ExecContext(ctxT(t), stmt)
		if err == nil {
			t.Errorf("%s auf events gelang", name)
			continue
		}
		wantCode(t, mapErr(err), CodeConstraint)
	}
	// Anhängen über Repositories bleibt möglich und die Folge ist lückenlos aufsteigend.
	var ids []int64
	mustView(t, s, func(r *ReadTx) error {
		evs, err := r.Events(projectID, EventFilter{})
		for _, e := range evs {
			ids = append(ids, e.EventID)
		}
		return err
	})
	for i := 1; i < len(ids); i++ {
		if ids[i] <= ids[i-1] {
			t.Fatalf("Ereignis-IDs nicht aufsteigend: %v", ids)
		}
	}
}

func TestProfileAndSnapshotImmutable(t *testing.T) {
	s := newStore(t)
	seedBase(t, s)
	for name, stmt := range map[string]string{
		"Profil ändern":           "UPDATE profile_versions SET sha256 = '" + strings.Repeat("0", 64) + "'",
		"Profil löschen":          "DELETE FROM profile_versions",
		"Snapshot ändern":         "UPDATE skill_snapshots SET path = '/x'",
		"Snapshot löschen":        "DELETE FROM skill_snapshots",
		"Snapshot-Skills ändern":  "UPDATE skill_snapshot_skills SET version = 9",
		"Snapshot-Skills löschen": "DELETE FROM skill_snapshot_skills",
	} {
		_, err := s.w.ExecContext(ctxT(t), stmt)
		if err == nil {
			t.Errorf("%s: gelang", name)
			continue
		}
		wantCode(t, mapErr(err), CodeConstraint)
	}

	// Dieselbe Version mit anderem Inhalt ist ein Konflikt; derselbe Inhalt ist ein stiller No-op ohne Ereignis.
	before := countEvents(t, s)
	changed := fxProfile(t)
	changed.Model = "model-z"
	_, err := s.Update(ctxT(t), "tester", func(tx *Tx) error { return tx.RecordProfile(projectID, changed) })
	wantCode(t, err, CodeConflict)
	mustUpdate(t, s, func(tx *Tx) error { return tx.RecordProfile(projectID, fxProfile(t)) })
	mustUpdate(t, s, func(tx *Tx) error { return tx.RecordSkillSnapshot(projectID, fxSnapshot(t), snapshotPath) })
	if got := countEvents(t, s); got != before {
		t.Fatalf("erneutes Speichern gleicher Inhalte erzeugte %d Ereignisse", got-before)
	}
	_, err = s.Update(ctxT(t), "tester", func(tx *Tx) error {
		return tx.RecordSkillSnapshot(projectID, fxSnapshot(t), "/work/exec/ccmas/anderswo")
	})
	wantCode(t, err, CodeConflict)

	// Die gespeicherte Version bleibt unverändert lesbar.
	mustView(t, s, func(r *ReadTx) error {
		p, err := r.Profile(projectID, "impl-default", 3)
		if err != nil {
			return err
		}
		if p.Model != "model-a" {
			t.Errorf("Profil verändert: %+v", p)
		}
		return nil
	})
}

func TestNoConceptVersionReserved(t *testing.T) {
	s := newStore(t)
	seedBase(t, s)

	// Repository ...
	for name, c := range map[string]*supervisor.ConceptState{
		"als Version": {Version: supervisor.NoConceptVersion, Class: "routine", Criteria: []string{}},
		"leer":        {Version: "", Criteria: []string{}},
	} {
		_, err := s.Update(ctxT(t), "tester", func(tx *Tx) error { return tx.SetConceptState(projectID, "wo-1", c) })
		if err == nil {
			t.Errorf("%s: angenommen", name)
			continue
		}
		wantCode(t, err, CodeInvalid)
	}
	_, err := s.Update(ctxT(t), "tester", func(tx *Tx) error {
		w := fxWorkOrder(t, "wo-nc")
		_, err := tx.CreateWorkOrder(WorkOrderRecord{WorkOrder: w, GoalID: "ms-1", Kind: supervisor.OrderConcept,
			Concept: &supervisor.ConceptState{Version: supervisor.NoConceptVersion, Criteria: []string{}}})
		return err
	})
	wantCode(t, err, CodeInvalid)

	// ... und CHECK der Datenbank.
	_, err = s.w.ExecContext(ctxT(t), `INSERT INTO concept_states (project_id, work_order_id, version, class, criteria_json, updated_at)
		VALUES ('ccmas', 'wo-1', ?, '', '[]', '2026-11-10T12:00:00.000000000Z')`, supervisor.NoConceptVersion)
	if err == nil {
		t.Fatal("CHECK ließ die reservierte Kennung als Konzeptversion zu")
	}
	wantCode(t, mapErr(err), CodeConstraint)

	// Ein Review-Bericht ohne Konzeptbezug trägt die Kennung dagegen vorgesehen.
	execID := seedExecutionFor(t, s, "wo-1", "exec-impl", domain.RoleImplementer)
	_ = execID
	seedExecutionFor(t, s, "wo-1", "exec-rev", domain.RoleReviewer)
	rep := domain.ReviewReport{ReportID: "rep-nc", WorkOrderID: "wo-1", ReviewerExecutionID: "exec-rev", Commit: commitA,
		ConceptVersion: supervisor.NoConceptVersion, Verdict: domain.VerdictApproved, Findings: []domain.Finding{}, CreatedAt: s.now()}
	mustUpdate(t, s, func(tx *Tx) error { return tx.RecordReview(projectID, rep, "exec-impl") })
}

// seedExecutionFor legt eine Ausführung samt erstem Versuch (initializing) an.
func seedExecutionFor(t *testing.T, s *Store, orderID, execID string, role domain.Role) string {
	t.Helper()
	profile := fxProfile(t)
	if role == domain.RoleReviewer {
		profile = fxReviewerProfile(t)
	}
	mustUpdate(t, s, func(tx *Tx) error {
		if err := tx.CreateExecution(domain.Execution{ExecutionID: execID, ProjectID: projectID, WorkOrderID: orderID, Role: role, CreatedAt: s.now()}); err != nil {
			return err
		}
		return tx.StartAttempt(projectID, domain.ExecutionAttempt{ExecutionID: execID, Attempt: 1, ProfileID: profile.ProfileID,
			ProfileVersion: profile.Version, State: domain.ExecInitializing, StartedAt: s.now()})
	})
	return execID
}

// ---- Leitplanke: keine Zugangsdaten in Spalten -------------------------------------------------------------

var forbiddenColumn = regexp.MustCompile(`(?i)secret|password|credential|api_key|apikey`)

// columnAllowed meldet, ob ein Spaltenname der Leitplanke genügt: keine Geheimnisse; Tokens nur als Hash.
func columnAllowed(name string) bool {
	if forbiddenColumn.MatchString(name) {
		return false
	}
	if strings.Contains(strings.ToLower(name), "token") && !strings.HasSuffix(strings.ToLower(name), "_sha256") {
		return false
	}
	return true
}

func TestNoSecretColumns(t *testing.T) {
	// Die Leitplanke selbst erkennt, was sie erkennen soll.
	for name, want := range map[string]bool{
		"api_key": false, "client_secret": false, "password_hash": false, "db_password": false, "credential_ref": false,
		"token": false, "access_token": false, "agent_token": false, "token_sha256": true, "agent_token_sha256": true,
		"work_order_id": true, "payload_json": true, "tokens_used": false,
	} {
		if got := columnAllowed(name); got != want {
			t.Errorf("columnAllowed(%q) = %v, erwartet %v", name, got, want)
		}
	}

	s := newStore(t)
	for _, table := range tableNames(t, s) {
		rows, err := s.r.QueryContext(context.Background(), "SELECT name FROM pragma_table_info(?)", table)
		must(t, err)
		for rows.Next() {
			var col string
			must(t, rows.Scan(&col))
			if !columnAllowed(col) {
				t.Errorf("Spalte %s.%s sieht nach einem Geheimnis aus", table, col)
			}
		}
		must(t, rows.Err())
		rows.Close()
	}
}
