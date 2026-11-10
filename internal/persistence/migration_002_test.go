package persistence

import (
	"reflect"
	"slices"
	"testing"

	"github.com/alexwenzel/ccmas/internal/domain"
)

func migration002(t *testing.T) Migration {
	t.Helper()
	for _, m := range Migrations() {
		if m.Version == 2 {
			return m
		}
	}
	t.Fatal("Migration 002 fehlt")
	return Migration{}
}

func schemaObjects(t *testing.T, s *Store, typ, table string) []string {
	t.Helper()
	rows, err := s.r.QueryContext(ctxT(t), "SELECT name FROM sqlite_master WHERE type = ? AND tbl_name = ? AND name NOT LIKE 'sqlite_%' ORDER BY name", typ, table)
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

func foreignKeyViolations(t *testing.T, s *Store) int {
	t.Helper()
	rows, err := s.r.QueryContext(ctxT(t), "PRAGMA foreign_key_check")
	must(t, err)
	defer rows.Close()
	n := 0
	for rows.Next() {
		n++
	}
	must(t, rows.Err())
	return n
}

// TestMigration002: die Migration trägt ihre Nummer und ihren Namen, ist idempotent und lässt Schema und Fremdschlüssel
// stimmig zurück.
func TestMigration002(t *testing.T) {
	s := newStore(t)
	m := migration002(t)
	if m.Name != "reservation" || m.SHA256 == "" {
		t.Errorf("Migration 002 = %d %q", m.Version, m.Name)
	}
	ctx := ctxT(t)
	first, err := s.AppliedMigrations(ctx)
	must(t, err)
	if len(first) < 2 || first[1].Version != 2 || first[1].Name != "reservation" || first[1].SHA256 != m.SHA256 {
		t.Fatalf("Historie = %+v", first)
	}
	must(t, s.Migrate(ctx))
	must(t, s.Migrate(ctx))
	second, err := s.AppliedMigrations(ctx)
	must(t, err)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("Historie änderte sich:\n%+v\n%+v", first, second)
	}

	wantIdx := []string{"leases_by_attempt", "leases_by_state", "leases_one_active_per_agent", "leases_one_active_per_order", "leases_one_active_per_worktree"}
	if got := schemaObjects(t, s, "index", "leases"); !slices.Equal(got, wantIdx) {
		t.Errorf("Indizes = %v\nerwartet %v", got, wantIdx)
	}
	wantTrg := []string{"leases_attempt_bound_once", "leases_attempt_same_order_insert", "leases_attempt_same_order_update",
		"leases_decision_accepted", "leases_identity_fixed", "leases_no_delete", "leases_terminal_fixed"}
	if got := schemaObjects(t, s, "trigger", "leases"); !slices.Equal(got, wantTrg) {
		t.Errorf("Trigger = %v\nerwartet %v", got, wantTrg)
	}
	if n := foreignKeyViolations(t, s); n != 0 {
		t.Errorf("%d Fremdschlüsselverletzungen", n)
	}
}

// TestMigration002KeepsExistingLeases: Eine Datenbank im Schema 001 mit Leases wird verlustfrei auf 002 gehoben: dieselben
// Zeilen, dieselben Regeln (eindeutige aktive Lease), neue Generationen setzen die Zählung fort.
func TestMigration002KeepsExistingLeases(t *testing.T) {
	path := dbPath(t)
	v1 := openStore(t, path, withMigrations(Migrations()[:1]))
	must(t, v1.Migrate(ctxT(t)))
	seedBase(t, v1)
	seedExecutionFor(t, v1, "wo-1", "exec-1", domain.RoleImplementer)
	must(t, execSQL(t, v1, `INSERT INTO leases (lease_id, project_id, work_order_id, agent_id, generation, state, worktree_path, acquired_at, expires_at, released_at)
		VALUES ('old', 'ccmas', 'wo-1', 'agent-1', 1, 'released', '/w/old', ?, ?, ?)`, ts, ts, ts))
	must(t, execSQL(t, v1, `INSERT INTO leases (lease_id, project_id, work_order_id, execution_id, attempt, agent_id, generation, state, worktree_path, acquired_at, expires_at)
		VALUES ('cur', 'ccmas', 'wo-1', 'exec-1', 1, 'agent-1', 2, 'active', '/w/cur', ?, ?)`, ts, ts))
	rawLeases := func(s *Store) (l1, l2 domain.ExecutionLease) {
		mustView(t, s, func(r *ReadTx) error {
			var err error
			if l1, err = r.Lease(projectID, "old"); err != nil {
				return err
			}
			l2, err = r.Lease(projectID, "cur")
			return err
		})
		return
	}
	oldLease, curLease := rawLeases(v1)
	if oldLease.WorktreePath != "/w/old" || curLease.WorktreePath != "/w/cur" || curLease.ExecutionID != "exec-1" {
		t.Fatalf("Ausgangslage: %+v %+v", oldLease, curLease)
	}
	must(t, v1.Close())

	v2 := openStore(t, path)
	must(t, v2.Migrate(ctxT(t)))
	gotOld, gotCur := rawLeases(v2)
	if gotOld != oldLease || gotCur != curLease {
		t.Errorf("Zeilen nach dem Umbau verändert:\n%+v\n%+v\nvorher\n%+v\n%+v", gotOld, gotCur, oldLease, curLease)
	}
	if n := foreignKeyViolations(t, v2); n != 0 {
		t.Errorf("%d Fremdschlüsselverletzungen nach dem Umbau", n)
	}

	// Die Regeln gelten weiter: keine zweite aktive Lease des Auftrags, des Agenten und des Worktrees.
	mustUpdate(t, v2, func(tx *Tx) error {
		_, err := tx.CreateAgent(projectID, "agent-2", domain.RoleImplementer)
		return err
	})
	_, err := v2.Update(ctxT(t), "tester", func(tx *Tx) error {
		_, err := tx.CreateLease(leaseFor("n1", "wo-1", "agent-2"), nil)
		return err
	})
	wantCode(t, err, CodeConflict)
	wantCode(t, mapErr(execSQL(t, v2, `INSERT INTO leases (lease_id, project_id, work_order_id, agent_id, generation, state, worktree_path, acquired_at, expires_at)
		VALUES ('dup', 'ccmas', 'wo-1', 'agent-2', 3, 'active', '/w/cur', ?, ?)`, ts, ts)), CodeConflict)
	// Die Zählung setzt fort, und ein Worktree ist jetzt optional.
	moveLease(t, v2, "cur", domain.LeaseActive, domain.LeaseReleased)
	if got := createLease(t, v2, leaseFor("n2", "wo-1", "agent-2"), nil); got.Generation != 3 || got.WorktreePath != "" {
		t.Errorf("neue Lease = %+v", got)
	}
}

// TestInspectAfterLeases: eine migrierte Datenbank mit Leases besteht die nur lesende Prüfung ohne Befund.
func TestInspectAfterLeases(t *testing.T) {
	s := leaseWorld(t)
	l := leaseFor("l1", "wo-1", "agent-1")
	l.ExecutionID, l.Attempt, l.WorktreePath = "exec-1", 1, "/work/wt/a"
	createLease(t, s, l, nil)
	createLease(t, s, leaseFor("l2", "wo-2", "agent-2"), nil)
	moveLease(t, s, "l2", domain.LeaseActive, domain.LeaseExpired)
	path := s.Path()
	must(t, s.Close())

	rep := inspect(t, path)
	wantNoFindings(t, rep)
	latest := Migrations()[len(Migrations())-1].Version
	if rep.Schema.UserVersion != latest || len(rep.Schema.Pending) != 0 || rep.Integrity.QuickCheck != "ok" || rep.Integrity.ForeignKeyViolations != 0 {
		t.Errorf("Schema %+v, Integrität %+v", rep.Schema, rep.Integrity)
	}
}
