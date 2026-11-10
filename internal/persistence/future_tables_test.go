package persistence

import (
	"strings"
	"testing"
	"time"

	"github.com/alexwenzel/ccmas/internal/domain"
	"github.com/alexwenzel/ccmas/internal/supervisor"
)

const ts = "2026-11-10T12:00:00.000000000Z"

func execSQL(t *testing.T, s *Store, stmt string, args ...any) error {
	t.Helper()
	_, err := s.w.ExecContext(ctxT(t), stmt, args...)
	return err
}

// TestLeaseConstraints: die Tabelle leases trägt die Regeln, auf denen CC-42 aufbaut: höchstens eine aktive Lease je
// Auftrag, je Agent und je Worktree; Generationen je Auftrag eindeutig.
func TestLeaseConstraints(t *testing.T) {
	s := newStore(t)
	seedBase(t, s)
	mustUpdate(t, s, func(tx *Tx) error {
		if _, err := tx.CreateWorkOrder(WorkOrderRecord{WorkOrder: fxWorkOrder(t, "wo-2"), GoalID: "ms-1", Kind: supervisor.OrderBug}); err != nil {
			return err
		}
		_, err := tx.CreateAgent(projectID, "agent-2", domain.RoleImplementer)
		return err
	})
	seedExecutionFor(t, s, "wo-1", "exec-1", domain.RoleImplementer)

	lease := func(id, order, agent string, gen int, state, worktree string, exec bool) error {
		var released any
		if state != "active" {
			released = ts
		}
		var e, a any
		if exec {
			e, a = "exec-1", 1
		}
		return execSQL(t, s, `INSERT INTO leases (lease_id, project_id, work_order_id, execution_id, attempt, agent_id, generation, state,
			worktree_path, acquired_at, expires_at, released_at) VALUES (?, 'ccmas', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, order, e, a, agent, gen, state, worktree, ts, ts, released)
	}
	if err := lease("l1", "wo-1", "agent-1", 1, "active", "/w/a", true); err != nil {
		t.Fatalf("erste Lease: %v", err)
	}
	for name, err := range map[string]error{
		"zweite aktive Lease je Auftrag":  lease("l2", "wo-1", "agent-2", 2, "active", "/w/b", false),
		"zweite aktive Lease je Agent":    lease("l3", "wo-2", "agent-1", 1, "active", "/w/c", false),
		"zweite aktive Lease je Worktree": lease("l4", "wo-2", "agent-2", 1, "active", "/w/a", false),
		"gleiche Generation":              lease("l5", "wo-1", "agent-2", 1, "released", "/w/d", false),
		"unbekannter Agent":               lease("l6", "wo-2", "agent-9", 1, "active", "/w/e", false),
		"relativer Worktree":              lease("l7", "wo-2", "agent-2", 1, "active", "w/e", false),
	} {
		if err == nil {
			t.Errorf("%s: angenommen", name)
			continue
		}
		wantCode(t, mapErr(err), map[bool]Code{true: CodeConflict, false: CodeConstraint}[strings.Contains(name, "Lease je") || strings.Contains(name, "Generation")])
	}
	// Freigegebene Leases kollidieren nicht mit einer neuen aktiven.
	if err := lease("l8", "wo-1", "agent-1", 2, "released", "/w/a", false); err != nil {
		t.Fatalf("freigegebene Lease: %v", err)
	}
	if err := execSQL(t, s, "UPDATE leases SET state = 'released', released_at = ? WHERE lease_id = 'l1'", ts); err != nil {
		t.Fatal(err)
	}
	if err := lease("l9", "wo-1", "agent-1", 3, "active", "/w/a", true); err != nil {
		t.Fatalf("neue aktive Lease nach Freigabe: %v", err)
	}
}

// TestMessageAndOutboxTables: die Tabellen für CC-45 nehmen gültige Zeilen an und weisen Dubletten ab.
func TestMessageAndOutboxTables(t *testing.T) {
	s := newStore(t)
	seedBase(t, s)
	seedExecutionFor(t, s, "wo-1", "exec-1", domain.RoleImplementer)
	must(t, execSQL(t, s, `INSERT INTO messages (message_id, project_id, work_order_id, execution_id, attempt, sender, recipient, type, correlation_id,
		payload_json, created_at) VALUES ('m1', 'ccmas', 'wo-1', 'exec-1', 1, 'worker', 'orchestrator', 'task_result', 'c1', '{"ok":true}', ?)`, ts))
	must(t, execSQL(t, s, `INSERT INTO message_deliveries (message_id, recipient, state) VALUES ('m1', 'orchestrator', 'pending')`))
	if err := execSQL(t, s, `INSERT INTO message_deliveries (message_id, recipient, state) VALUES ('m1', 'orchestrator', 'delivered')`); err == nil {
		t.Error("doppelte Zustellung an denselben Empfänger angenommen")
	}
	if err := execSQL(t, s, `INSERT INTO message_deliveries (message_id, recipient, state) VALUES ('m1', 'x', 'lost')`); err == nil {
		t.Error("unbekannter Zustellzustand angenommen")
	}
	insertOutbox := func(id, key, state string) error {
		return execSQL(t, s, `INSERT INTO outbox (action_id, project_id, operation_key, kind, payload_json, state, created_at, updated_at)
			VALUES (?, 'ccmas', ?, 'asana.comment', '{}', ?, ?, ?)`, id, key, state, ts, ts)
	}
	// operation_key beginnt mit <projekt>/<art>/ (Migration 003) und ist global eindeutig.
	must(t, insertOutbox("a1", "ccmas/asana.comment/wo-1-1", "pending"))
	wantCode(t, mapErr(insertOutbox("a2", "ccmas/asana.comment/wo-1-1", "pending")), CodeConflict)
	wantCode(t, mapErr(insertOutbox("a3", "ccmas/asana.comment/k3", "halbfertig")), CodeConstraint)
	// last_error ist begrenzt (redigierte Kurzfassung, kein Rohtext).
	wantCode(t, mapErr(execSQL(t, s, "UPDATE outbox SET last_error = ? WHERE action_id = 'a1'", strings.Repeat("x", 1025))), CodeConstraint)
}

// TestReadAPI: Filter und Sortierungen der Lesemethoden.
func TestReadAPI(t *testing.T) {
	s := newStore(t)
	buildWorld(t, s)
	mustView(t, s, func(r *ReadTx) error {
		all, err := r.Events(projectID, EventFilter{})
		if err != nil {
			return err
		}
		if len(all) < 20 {
			t.Fatalf("nur %d Ereignisse", len(all))
		}
		for i := 1; i < len(all); i++ {
			if all[i].EventID <= all[i-1].EventID || all[i].StateRevision < all[i-1].StateRevision {
				t.Fatalf("Ereignisse nicht geordnet: %+v %+v", all[i-1], all[i])
			}
		}
		page, _ := r.Events(projectID, EventFilter{AfterID: all[3].EventID, Limit: 2})
		if len(page) != 2 || page[0].EventID != all[4].EventID {
			t.Errorf("Seite = %+v", page)
		}
		proc, _ := r.Events(projectID, EventFilter{Level: domain.AuditLevelProcess})
		if len(proc) != 1 || proc[0].Kind != domain.AuditProcessObserved || proc[0].Details["pid"] != "4711" {
			t.Errorf("Prozessereignisse = %+v", proc)
		}
		ent, _ := r.Events(projectID, EventFilter{EntityType: "work_order", EntityID: "wo-2"})
		if len(ent) < 6 {
			t.Errorf("%d Ereignisse zu wo-2", len(ent))
		}
		if none, _ := r.Events("unbekannt", EventFilter{}); len(none) != 0 {
			t.Errorf("Ereignisse eines unbekannten Projekts: %+v", none)
		}

		ps, err := r.Projects()
		if err != nil || len(ps) != 1 || ps[0].Config.ProjectID != projectID {
			t.Errorf("Projekte = %+v (%v)", ps, err)
		}
		if _, err := r.Project("unbekannt"); CodeOf(err) != CodeNotFound {
			t.Errorf("unbekanntes Projekt: %v", err)
		}
		p, err := r.LatestProfile(projectID, "impl-default")
		if err != nil || p.Version != 3 {
			t.Errorf("LatestProfile = %+v (%v)", p, err)
		}
		if _, err := r.LatestProfile(projectID, "gibt-es-nicht"); CodeOf(err) != CodeNotFound {
			t.Errorf("unbekanntes Profil: %v", err)
		}
		os, err := r.WorkOrdersOfGoal(projectID, "ms-1")
		if err != nil || len(os) != 2 || os[0].WorkOrderID != "wo-1" || len(os[0].DependsOn) != 0 || !reflectEqual(os[1].DependsOn, []string{"wo-1"}) {
			t.Errorf("Aufträge des Ziels = %+v (%v)", os, err)
		}
		k, err := r.TaskByGID(projectID, "1220366216593996")
		if err != nil || k.TaskID != "task-1" {
			t.Errorf("TaskByGID = %+v (%v)", k, err)
		}
		qs, _ := r.Questions(projectID, domain.QuestionOpen)
		if len(qs) != 1 || qs[0].QuestionID != "q-2" {
			t.Errorf("offene Fragen = %+v", qs)
		}
		if _, err := r.Answer(projectID, "q-2"); CodeOf(err) != CodeNotFound {
			t.Errorf("Antwort auf unbeantwortete Frage: %v", err)
		}
		exs, _ := r.Executions(projectID, "wo-1")
		if len(exs) != 2 {
			t.Errorf("Ausführungen von wo-1 = %+v", exs)
		}
		return nil
	})
}

func reflectEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestTimestampsAreFixedWidthUTC: die Textordnung der Zeitstempel ist die Zeitordnung, auch mit Zeitzone und ohne
// Nachkommastellen in der Eingabe.
func TestTimestampsAreFixedWidthUTC(t *testing.T) {
	zone := time.FixedZone("CET", 3600)
	a := time.Date(2026, 11, 10, 13, 0, 0, 0, zone)       // 12:00:00 UTC, ohne Bruchteil
	b := time.Date(2026, 11, 10, 12, 0, 0, 100, time.UTC) // 100 ns später
	fa, fb := fmtTime(a), fmtTime(b)
	if len(fa) != 30 || len(fb) != 30 || !strings.HasSuffix(fa, "Z") {
		t.Fatalf("Format = %q / %q", fa, fb)
	}
	if !(fa < fb) {
		t.Fatalf("Textordnung %q < %q verletzt die Zeitordnung", fa, fb)
	}
	back, err := parseTime(fa)
	if err != nil || !back.Equal(a) {
		t.Fatalf("Rundreise = %v (%v)", back, err)
	}
}
