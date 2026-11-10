package persistence

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alexwenzel/ccmas/internal/domain"
	"github.com/alexwenzel/ccmas/internal/supervisor"
)

var leaseExpiry = time.Date(2026, 11, 10, 13, 0, 0, 0, time.UTC)

// leaseWorld: Projekt mit Aufträgen wo-1 und wo-2, Agenten agent-1 bis agent-3 und je einer Ausführung (exec-1 zu
// wo-1, exec-2 zu wo-2, Versuch 1 im Zustand initializing).
func leaseWorld(t *testing.T) *Store {
	t.Helper()
	s := newStore(t)
	seedBase(t, s)
	mustUpdate(t, s, func(tx *Tx) error {
		if _, err := tx.CreateWorkOrder(WorkOrderRecord{WorkOrder: fxWorkOrder(t, "wo-2"), GoalID: "ms-1", Kind: supervisor.OrderBug, Title: "Auftrag 2"}); err != nil {
			return err
		}
		for _, a := range []string{"agent-2", "agent-3"} {
			if _, err := tx.CreateAgent(projectID, a, domain.RoleImplementer); err != nil {
				return err
			}
		}
		return nil
	})
	seedExecutionFor(t, s, "wo-1", "exec-1", domain.RoleImplementer)
	seedExecutionFor(t, s, "wo-2", "exec-2", domain.RoleImplementer)
	return s
}

func leaseFor(id, order, agent string) domain.ExecutionLease {
	return domain.ExecutionLease{LeaseID: id, ProjectID: projectID, WorkOrderID: order, AgentID: agent, State: domain.LeaseActive, ExpiresAt: leaseExpiry}
}

func createLease(t *testing.T, s *Store, l domain.ExecutionLease, origin *LeaseOrigin) domain.ExecutionLease {
	t.Helper()
	var out domain.ExecutionLease
	mustUpdate(t, s, func(tx *Tx) error {
		var err error
		out, err = tx.CreateLease(l, origin)
		return err
	})
	return out
}

func moveLease(t *testing.T, s *Store, id string, from, to domain.LeaseState) {
	t.Helper()
	mustUpdate(t, s, func(tx *Tx) error { return tx.TransitionLease(projectID, id, from, to) })
}

func readLease(t *testing.T, s *Store, id string) domain.ExecutionLease {
	t.Helper()
	var l domain.ExecutionLease
	mustView(t, s, func(r *ReadTx) error {
		var err error
		l, err = r.Lease(projectID, id)
		return err
	})
	return l
}

func leasesOf(t *testing.T, s *Store, order string) []domain.ExecutionLease {
	t.Helper()
	var ls []domain.ExecutionLease
	mustView(t, s, func(r *ReadTx) error {
		var err error
		ls, err = r.Leases(projectID, order)
		return err
	})
	return ls
}

// leaseSnapshot hält fest, was eine fehlgeschlagene Änderung nicht berührt haben darf.
type leaseSnapshot struct {
	revision int64
	events   int
	leases   int
}

func takeLeaseSnapshot(t *testing.T, s *Store) leaseSnapshot {
	t.Helper()
	n := 0
	for _, o := range []string{"wo-1", "wo-2"} {
		n += len(leasesOf(t, s, o))
	}
	return leaseSnapshot{revision: revisionOf(t, s, projectID), events: countEvents(t, s), leases: n}
}

func TestLeaseGenerationsRiseWithoutGaps(t *testing.T) {
	s := leaseWorld(t)
	for i := 1; i <= 4; i++ {
		got := createLease(t, s, leaseFor(fmt.Sprintf("l%d", i), "wo-1", "agent-1"), nil)
		if got.Generation != int64(i) || got.State != domain.LeaseActive || got.ReleasedAt != (time.Time{}) {
			t.Fatalf("Lease %d: %+v", i, got)
		}
		if err := got.Check(); err != nil {
			t.Fatalf("zurückgegebene Lease ungültig: %v", err)
		}
		moveLease(t, s, got.LeaseID, domain.LeaseActive, domain.LeaseReleased)
	}
	// Die Zählung ist je Auftrag: wo-2 beginnt wieder bei 1.
	if g := createLease(t, s, leaseFor("m1", "wo-2", "agent-2"), nil).Generation; g != 1 {
		t.Errorf("erste Lease von wo-2 hat Generation %d", g)
	}
	var gens []int64
	for _, l := range leasesOf(t, s, "wo-1") {
		gens = append(gens, l.Generation)
	}
	if fmt.Sprint(gens) != "[1 2 3 4]" {
		t.Errorf("Generationen von wo-1 = %v", gens)
	}
}

func TestLeaseCreateAuditAndRevision(t *testing.T) {
	s := leaseWorld(t)
	before := takeLeaseSnapshot(t, s)
	l := leaseFor("l1", "wo-1", "agent-1")
	l.WorktreePath = "/work/wt/wo-1"
	got := createLease(t, s, l, nil)

	if rev := revisionOf(t, s, projectID); rev != before.revision+1 {
		t.Errorf("Revision %d, erwartet %d: eine Lease ist Planungszustand", rev, before.revision+1)
	}
	ev := lastEvent(t, s)
	if ev.Kind != domain.AuditLeaseAcquired || ev.Level != domain.AuditLevelLease || ev.EntityType != "lease" || ev.EntityID != "l1" ||
		ev.ToState != "active" || ev.StateRevision != before.revision+1 {
		t.Errorf("Ereignis = %+v", ev)
	}
	if ev.Details["work_order_id"] != "wo-1" || ev.Details["agent_id"] != "agent-1" || ev.Details["generation"] != "1" {
		t.Errorf("Details = %v", ev.Details)
	}
	if countEvents(t, s) != before.events+1 {
		t.Errorf("Ereignisse %d -> %d, erwartet genau eines mehr", before.events, countEvents(t, s))
	}
	// Beginn: Zeit der Transaktion, wenn nichts genannt ist; der gelesene Wert ist der zurückgegebene.
	if got.AcquiredAt.IsZero() || !got.AcquiredAt.Before(leaseExpiry) {
		t.Errorf("AcquiredAt = %v", got.AcquiredAt)
	}
	read := readLease(t, s, "l1")
	if read != got {
		t.Errorf("gelesen %+v\nzurückgegeben %+v", read, got)
	}
	if read.WorktreePath != "/work/wt/wo-1" {
		t.Errorf("Worktree = %q", read.WorktreePath)
	}
}

func TestLeaseCreateRejectsInvalidInput(t *testing.T) {
	s := leaseWorld(t)
	cases := map[string]struct {
		mut  func(*domain.ExecutionLease)
		code Code
	}{
		"Generation vorgegeben":   {func(l *domain.ExecutionLease) { l.Generation = 5 }, CodeInvalid},
		"Zustand nicht aktiv":     {func(l *domain.ExecutionLease) { l.State = domain.LeaseReleased; l.ReleasedAt = leaseExpiry }, CodeState},
		"Ablauf fehlt":            {func(l *domain.ExecutionLease) { l.ExpiresAt = time.Time{} }, CodeInvalid},
		"Ablauf vor Beginn":       {func(l *domain.ExecutionLease) { l.AcquiredAt = leaseExpiry.Add(time.Hour) }, CodeInvalid},
		"relativer Worktree":      {func(l *domain.ExecutionLease) { l.WorktreePath = "wt/wo-1" }, CodeInvalid},
		"ungültige Lease-ID":      {func(l *domain.ExecutionLease) { l.LeaseID = "a b" }, CodeInvalid},
		"Versuch ohne Ausführung": {func(l *domain.ExecutionLease) { l.Attempt = 1 }, CodeInvalid},
		"unbekannter Auftrag":     {func(l *domain.ExecutionLease) { l.WorkOrderID = "wo-9" }, CodeNotFound},
		"unbekannter Agent":       {func(l *domain.ExecutionLease) { l.AgentID = "agent-9" }, CodeNotFound},
		"Lease-ID schon vergeben": {func(l *domain.ExecutionLease) { l.LeaseID = "l0" }, CodeConflict},
		"Endzeit bei neuer Lease": {func(l *domain.ExecutionLease) { l.ReleasedAt = leaseExpiry }, CodeInvalid},
	}
	createLease(t, s, leaseFor("l0", "wo-2", "agent-3"), nil)
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			before := takeLeaseSnapshot(t, s)
			l := leaseFor("l1", "wo-1", "agent-1")
			c.mut(&l)
			_, err := s.Update(ctxT(t), "tester", func(tx *Tx) error {
				_, err := tx.CreateLease(l, nil)
				return err
			})
			wantCode(t, err, c.code)
			if after := takeLeaseSnapshot(t, s); after != before {
				t.Errorf("eine abgewiesene Lease hat etwas geschrieben: %+v -> %+v", before, after)
			}
		})
	}
}

func TestLeaseAtMostOneActive(t *testing.T) {
	s := leaseWorld(t)
	first := leaseFor("l1", "wo-1", "agent-1")
	first.WorktreePath = "/work/wt/a"
	createLease(t, s, first, nil)

	cases := map[string]domain.ExecutionLease{
		"zweite aktive Lease je Auftrag": leaseFor("l2", "wo-1", "agent-2"),
		"zweite aktive Lease je Agent":   leaseFor("l3", "wo-2", "agent-1"),
		"zweite aktive Lease je Worktree": func() domain.ExecutionLease {
			l := leaseFor("l4", "wo-2", "agent-2")
			l.WorktreePath = "/work/wt/a"
			return l
		}(),
	}
	for name, l := range cases {
		t.Run(name, func(t *testing.T) {
			before := takeLeaseSnapshot(t, s)
			_, err := s.Update(ctxT(t), "tester", func(tx *Tx) error {
				_, err := tx.CreateLease(l, nil)
				return err
			})
			wantCode(t, err, CodeConflict)
			// Go nennt den Halter (die Datenbank allein sagte nur „existiert bereits“).
			if !strings.Contains(err.Error(), `"l1"`) {
				t.Errorf("die Meldung nennt den Halter nicht: %v", err)
			}
			if after := takeLeaseSnapshot(t, s); after != before {
				t.Errorf("Ablehnung hat etwas geschrieben: %+v -> %+v", before, after)
			}
		})
	}

	// Nach der Freigabe kann dieselbe Ressource neu vergeben werden; die alte Lease bleibt als Historie.
	moveLease(t, s, "l1", domain.LeaseActive, domain.LeaseReleased)
	again := leaseFor("l5", "wo-1", "agent-1")
	again.WorktreePath = "/work/wt/a"
	if got := createLease(t, s, again, nil); got.Generation != 2 {
		t.Errorf("Generation nach Freigabe = %d", got.Generation)
	}
}

// TestLeaseDatabaseIndexesBackstopGo: Die Eindeutigkeit steht zusätzlich in der Datenbank. Der Go-Weg meldet conflict, bevor
// geschrieben wird; geht jemand an Go vorbei, weist der Index dieselbe Dublette mit demselben Code ab.
func TestLeaseDatabaseIndexesBackstopGo(t *testing.T) {
	s := leaseWorld(t)
	l := leaseFor("l1", "wo-1", "agent-1")
	l.WorktreePath = "/work/wt/a"
	createLease(t, s, l, nil)
	raw := func(id, order, agent string, gen int, worktree any) error {
		return execSQL(t, s, `INSERT INTO leases (lease_id, project_id, work_order_id, agent_id, generation, state, worktree_path, acquired_at, expires_at)
			VALUES (?, 'ccmas', ?, ?, ?, 'active', ?, ?, ?)`, id, order, agent, gen, worktree, ts, ts)
	}
	wantCode(t, mapErr(raw("x1", "wo-1", "agent-2", 2, nil)), CodeConflict)
	wantCode(t, mapErr(raw("x2", "wo-2", "agent-1", 1, nil)), CodeConflict)
	wantCode(t, mapErr(raw("x3", "wo-2", "agent-2", 1, "/work/wt/a")), CodeConflict)
	// Ohne Worktree gibt es keine Kollision: mehrere aktive Leases ohne Worktree sind zulässig.
	must(t, raw("x4", "wo-2", "agent-2", 1, nil))
}

func TestLeaseConcurrentStartsHaveAtMostOneOwner(t *testing.T) {
	s := leaseWorld(t)
	const n = 8
	mustUpdate(t, s, func(tx *Tx) error {
		for i := 4; i <= n; i++ {
			if _, err := tx.CreateAgent(projectID, fmt.Sprintf("agent-%d", i), domain.RoleImplementer); err != nil {
				return err
			}
		}
		return nil
	})
	var wg sync.WaitGroup
	results := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, results[i] = s.Update(ctxT(t), "tester", func(tx *Tx) error {
				_, err := tx.CreateLease(leaseFor(fmt.Sprintf("c%d", i), "wo-1", fmt.Sprintf("agent-%d", i+1)), nil)
				return err
			})
		}()
	}
	wg.Wait()
	won := 0
	for i, err := range results {
		switch {
		case err == nil:
			won++
		case CodeOf(err) != CodeConflict:
			t.Errorf("Start %d: %v (erwartet conflict)", i, err)
		}
	}
	if won != 1 {
		t.Fatalf("%d Gewinner, erwartet genau einer", won)
	}
	if ls := leasesOf(t, s, "wo-1"); len(ls) != 1 || ls[0].Generation != 1 {
		t.Errorf("Leases = %+v", ls)
	}
}

// TestLeaseAttemptMustBelongToTheSameOrder (offener Punkt aus dem CC-27-Review): Das Schema bindet eine Lease nur an einen
// Versuch desselben Projekts. Dass der Versuch zum Auftrag der Lease gehört, prüft Go, und nichts wird geschrieben.
func TestLeaseAttemptMustBelongToTheSameOrder(t *testing.T) {
	s := leaseWorld(t)
	for name, c := range map[string]struct {
		mut  func(*domain.ExecutionLease)
		code Code
	}{
		"Versuch eines anderen Auftrags": {func(l *domain.ExecutionLease) { l.ExecutionID, l.Attempt = "exec-2", 1 }, CodeInvalid},
		"unbekannter Versuch":            {func(l *domain.ExecutionLease) { l.ExecutionID, l.Attempt = "exec-1", 7 }, CodeNotFound},
		"unbekannte Ausführung":          {func(l *domain.ExecutionLease) { l.ExecutionID, l.Attempt = "exec-9", 1 }, CodeNotFound},
	} {
		t.Run(name, func(t *testing.T) {
			before := takeLeaseSnapshot(t, s)
			l := leaseFor("l1", "wo-1", "agent-1")
			c.mut(&l)
			_, err := s.Update(ctxT(t), "tester", func(tx *Tx) error {
				_, err := tx.CreateLease(l, nil)
				return err
			})
			wantCode(t, err, c.code)
			if after := takeLeaseSnapshot(t, s); after != before {
				t.Errorf("Ablehnung hat etwas geschrieben: %+v -> %+v", before, after)
			}
		})
	}

	t.Run("Ausführung ohne Auftrag", func(t *testing.T) {
		mustUpdate(t, s, func(tx *Tx) error {
			if err := tx.CreateExecution(domain.Execution{ExecutionID: "exec-free", ProjectID: projectID, Role: domain.RoleImplementer, CreatedAt: s.now()}); err != nil {
				return err
			}
			p := fxProfile(t)
			return tx.StartAttempt(projectID, domain.ExecutionAttempt{ExecutionID: "exec-free", Attempt: 1, ProfileID: p.ProfileID, ProfileVersion: p.Version,
				State: domain.ExecInitializing, StartedAt: s.now()})
		})
		l := leaseFor("l1", "wo-1", "agent-1")
		l.ExecutionID, l.Attempt = "exec-free", 1
		_, err := s.Update(ctxT(t), "tester", func(tx *Tx) error { _, err := tx.CreateLease(l, nil); return err })
		wantCode(t, err, CodeInvalid)
	})

	t.Run("Ausführung eines anderen Agenten", func(t *testing.T) {
		mustUpdate(t, s, func(tx *Tx) error {
			if err := tx.CreateExecution(domain.Execution{ExecutionID: "exec-a2", ProjectID: projectID, WorkOrderID: "wo-1", AgentID: "agent-2",
				Role: domain.RoleImplementer, CreatedAt: s.now()}); err != nil {
				return err
			}
			p := fxProfile(t)
			return tx.StartAttempt(projectID, domain.ExecutionAttempt{ExecutionID: "exec-a2", Attempt: 1, ProfileID: p.ProfileID, ProfileVersion: p.Version,
				State: domain.ExecInitializing, StartedAt: s.now()})
		})
		l := leaseFor("l1", "wo-1", "agent-1")
		l.ExecutionID, l.Attempt = "exec-a2", 1
		_, err := s.Update(ctxT(t), "tester", func(tx *Tx) error { _, err := tx.CreateLease(l, nil); return err })
		wantCode(t, err, CodeInvalid)
		// Mit dem Agenten der Ausführung geht es.
		l.AgentID = "agent-2"
		if got := createLease(t, s, l, nil); got.ExecutionID != "exec-a2" || got.Attempt != 1 {
			t.Errorf("Lease = %+v", got)
		}
	})

	t.Run("passender Versuch", func(t *testing.T) {
		l := leaseFor("l2", "wo-2", "agent-3")
		l.ExecutionID, l.Attempt = "exec-2", 1
		got := createLease(t, s, l, nil)
		if got.ExecutionID != "exec-2" || got.Attempt != 1 {
			t.Errorf("Lease = %+v", got)
		}
	})
}

func TestLeaseBindAttempt(t *testing.T) {
	s := leaseWorld(t)
	createLease(t, s, leaseFor("l1", "wo-1", "agent-1"), nil)

	before := takeLeaseSnapshot(t, s)
	mustUpdate(t, s, func(tx *Tx) error { return tx.BindLeaseAttempt(projectID, "l1", "exec-1", 1) })
	got := readLease(t, s, "l1")
	if got.ExecutionID != "exec-1" || got.Attempt != 1 || got.State != domain.LeaseActive {
		t.Fatalf("Lease = %+v", got)
	}
	if rev := revisionOf(t, s, projectID); rev != before.revision+1 {
		t.Errorf("Revision %d, erwartet %d", rev, before.revision+1)
	}
	if ev := lastEvent(t, s); ev.Kind != domain.AuditLeaseAttemptBound || ev.EntityID != "l1" || ev.Details["execution"] != "exec-1#1" {
		t.Errorf("Ereignis = %+v", ev)
	}

	// Dieselbe Bindung noch einmal ist keine Änderung.
	again := takeLeaseSnapshot(t, s)
	mustUpdate(t, s, func(tx *Tx) error { return tx.BindLeaseAttempt(projectID, "l1", "exec-1", 1) })
	if after := takeLeaseSnapshot(t, s); after != again {
		t.Errorf("wiederholte Bindung schrieb: %+v -> %+v", again, after)
	}

	createLease(t, s, leaseFor("l3", "wo-2", "agent-3"), nil) // ungebunden, Auftrag wo-2
	for name, c := range map[string]struct {
		lease, exec string
		attempt     int
		code        Code
	}{
		"andere Bindung":                 {"l1", "exec-1", 2, CodeConflict},
		"Versuch eines anderen Auftrags": {"l3", "exec-1", 1, CodeInvalid},
		"unbekannter Versuch":            {"l3", "exec-2", 5, CodeNotFound},
		"unbekannte Lease":               {"l9", "exec-1", 1, CodeNotFound},
	} {
		t.Run(name, func(t *testing.T) {
			b := takeLeaseSnapshot(t, s)
			_, err := s.Update(ctxT(t), "tester", func(tx *Tx) error { return tx.BindLeaseAttempt(projectID, c.lease, c.exec, c.attempt) })
			wantCode(t, err, c.code)
			if a := takeLeaseSnapshot(t, s); a != b {
				t.Errorf("Ablehnung schrieb: %+v -> %+v", b, a)
			}
		})
	}

	t.Run("beendete Lease", func(t *testing.T) {
		moveLease(t, s, "l3", domain.LeaseActive, domain.LeaseRevoked)
		_, err := s.Update(ctxT(t), "tester", func(tx *Tx) error { return tx.BindLeaseAttempt(projectID, "l3", "exec-2", 1) })
		wantCode(t, err, CodeState)
	})
}

func TestLeaseTransitions(t *testing.T) {
	s := leaseWorld(t)
	for _, to := range []domain.LeaseState{domain.LeaseReleased, domain.LeaseExpired, domain.LeaseRevoked} {
		t.Run(string(to), func(t *testing.T) {
			id := "l-" + string(to)
			createLease(t, s, leaseFor(id, "wo-1", "agent-1"), nil)
			before := takeLeaseSnapshot(t, s)
			moveLease(t, s, id, domain.LeaseActive, to)

			got := readLease(t, s, id)
			if got.State != to || got.ReleasedAt.IsZero() {
				t.Fatalf("Lease = %+v", got)
			}
			if err := got.Check(); err != nil {
				t.Errorf("beendete Lease ungültig: %v", err)
			}
			if rev := revisionOf(t, s, projectID); rev != before.revision+1 {
				t.Errorf("Revision %d, erwartet %d", rev, before.revision+1)
			}
			ev := lastEvent(t, s)
			wantKind := map[domain.LeaseState]string{domain.LeaseReleased: domain.AuditLeaseReleased, domain.LeaseExpired: domain.AuditLeaseExpired, domain.LeaseRevoked: domain.AuditLeaseRevoked}[to]
			if ev.Kind != wantKind || ev.FromState != "active" || ev.ToState != string(to) || ev.Level != domain.AuditLevelLease || ev.EntityID != id {
				t.Errorf("Ereignis = %+v", ev)
			}
			if countEvents(t, s) != before.events+1 {
				t.Error("genau ein Ereignis je Übergang erwartet")
			}
			// Aus einem Endzustand führt nichts heraus, auch nicht zurück nach „aktiv“.
			for _, again := range domain.LeaseStates() {
				b := takeLeaseSnapshot(t, s)
				_, err := s.Update(ctxT(t), "tester", func(tx *Tx) error { return tx.TransitionLease(projectID, id, to, again) })
				wantCode(t, err, CodeState)
				if a := takeLeaseSnapshot(t, s); a != b {
					t.Errorf("abgewiesener Übergang schrieb: %+v -> %+v", b, a)
				}
			}
		})
	}
}

func TestLeaseTransitionExpectsActualState(t *testing.T) {
	s := leaseWorld(t)
	createLease(t, s, leaseFor("l1", "wo-1", "agent-1"), nil)
	moveLease(t, s, "l1", domain.LeaseActive, domain.LeaseRevoked)

	// Wer glaubt, die Lease sei noch aktiv, scheitert mit conflict und überschreibt den Widerruf nicht.
	for _, to := range []domain.LeaseState{domain.LeaseReleased, domain.LeaseExpired, domain.LeaseRevoked} {
		before := takeLeaseSnapshot(t, s)
		_, err := s.Update(ctxT(t), "tester", func(tx *Tx) error { return tx.TransitionLease(projectID, "l1", domain.LeaseActive, to) })
		wantCode(t, err, CodeConflict)
		if after := takeLeaseSnapshot(t, s); after != before {
			t.Errorf("-> %s schrieb: %+v -> %+v", to, before, after)
		}
	}
	if got := readLease(t, s, "l1"); got.State != domain.LeaseRevoked {
		t.Errorf("Zustand = %s", got.State)
	}
	_, err := s.Update(ctxT(t), "tester", func(tx *Tx) error {
		return tx.TransitionLease(projectID, "nope", domain.LeaseActive, domain.LeaseReleased)
	})
	wantCode(t, err, CodeNotFound)
	_, err = s.Update(ctxT(t), "tester", func(tx *Tx) error { return tx.TransitionLease(projectID, "l1", "bogus", domain.LeaseReleased) })
	wantCode(t, err, CodeState)
}

func TestLeaseRollbackTakesEverythingBack(t *testing.T) {
	s := leaseWorld(t)
	before := takeLeaseSnapshot(t, s)
	_, err := s.Update(ctxT(t), "tester", func(tx *Tx) error {
		if _, err := tx.CreateLease(leaseFor("l1", "wo-1", "agent-1"), nil); err != nil {
			return err
		}
		return fmt.Errorf("Abbruch im Callback")
	})
	if err == nil {
		t.Fatal("kein Fehler")
	}
	if after := takeLeaseSnapshot(t, s); after != before {
		t.Errorf("Rollback unvollständig: %+v -> %+v", before, after)
	}
}

func TestLeaseReads(t *testing.T) {
	s := leaseWorld(t)
	mustView(t, s, func(r *ReadTx) error {
		if l, err := r.ActiveLease(projectID, "wo-1"); err != nil || l != nil {
			t.Errorf("ActiveLease ohne Lease = %+v, %v", l, err)
		}
		if l, err := r.AttemptLease(projectID, "exec-1", 1); err != nil || l != nil {
			t.Errorf("AttemptLease ohne Lease = %+v, %v", l, err)
		}
		_, err := r.Lease(projectID, "l1")
		wantCode(t, err, CodeNotFound)
		ls, err := r.Leases(projectID, "wo-1")
		if err != nil || len(ls) != 0 {
			t.Errorf("Leases = %+v, %v", ls, err)
		}
		return nil
	})

	old := leaseFor("l1", "wo-1", "agent-1")
	old.ExecutionID, old.Attempt = "exec-1", 1
	createLease(t, s, old, nil)
	moveLease(t, s, "l1", domain.LeaseActive, domain.LeaseExpired)
	cur := leaseFor("l2", "wo-1", "agent-2")
	cur.ExecutionID, cur.Attempt = "exec-1", 1
	createLease(t, s, cur, nil)
	createLease(t, s, leaseFor("l3", "wo-2", "agent-3"), nil)

	mustView(t, s, func(r *ReadTx) error {
		act, err := r.ActiveLease(projectID, "wo-1")
		if err != nil || act == nil || act.LeaseID != "l2" || act.Generation != 2 {
			t.Errorf("ActiveLease = %+v, %v", act, err)
		}
		// Zu einem Versuch kann es mehrere Leases geben (die alte ist abgelaufen); gefragt ist die jüngste Generation.
		al, err := r.AttemptLease(projectID, "exec-1", 1)
		if err != nil || al == nil || al.LeaseID != "l2" {
			t.Errorf("AttemptLease = %+v, %v", al, err)
		}
		ls, err := r.Leases(projectID, "wo-1")
		if err != nil || len(ls) != 2 || ls[0].LeaseID != "l1" || ls[1].LeaseID != "l2" {
			t.Errorf("Leases = %+v, %v", ls, err)
		}
		all, err := r.ActiveLeases(projectID)
		if err != nil || len(all) != 2 || all[0].LeaseID != "l2" || all[1].LeaseID != "l3" {
			t.Errorf("ActiveLeases = %+v, %v", all, err)
		}
		other, err := r.ActiveLeases("anderes")
		if err != nil || len(other) != 0 {
			t.Errorf("ActiveLeases eines anderen Projekts = %+v, %v", other, err)
		}
		return nil
	})
	// Dieselben Lesemethoden gibt es in der Änderungstransaktion (der Orchestrator liest und schreibt in einer).
	mustUpdate(t, s, func(tx *Tx) error {
		act, err := tx.ActiveLease(projectID, "wo-2")
		if err != nil || act == nil || act.LeaseID != "l3" {
			t.Errorf("Tx.ActiveLease = %+v, %v", act, err)
		}
		return nil
	})
}

func recordLeasePlan(t *testing.T, s *Store, batchID string, verdict supervisor.Verdict) {
	t.Helper()
	b := fxBatch(t)
	b.BatchID = batchID
	rec := PlanRecord{Batch: b, Verdict: verdict, PlanningSnapshotSHA256: hex64("1")}
	if verdict == supervisor.VerdictRejected {
		rec.Rejections = []supervisor.Rejection{{Code: supervisor.CodeStaleRevision, Reason: "Zustand veraltet"}}
	}
	mustUpdate(t, s, func(tx *Tx) error { return tx.RecordPlan(rec) })
}

func TestLeaseFromDecisionReservesOnlyOnce(t *testing.T) {
	s := leaseWorld(t)
	recordLeasePlan(t, s, "batch-ok", supervisor.VerdictAccepted)
	recordLeasePlan(t, s, "batch-no", supervisor.VerdictRejected)

	origin := &LeaseOrigin{BatchID: "batch-ok", DecisionID: "dec-1"}
	got := createLease(t, s, leaseFor("l1", "wo-1", "agent-1"), origin)
	mustView(t, s, func(r *ReadTx) error {
		l, err := r.DecisionLease(projectID, "batch-ok", "dec-1")
		if err != nil || l == nil || *l != got {
			t.Errorf("DecisionLease = %+v, %v", l, err)
		}
		if l, err := r.DecisionLease(projectID, "batch-ok", "dec-2"); err != nil || l != nil {
			t.Errorf("DecisionLease ohne Reservierung = %+v, %v", l, err)
		}
		return nil
	})
	if ev := lastEvent(t, s); ev.Details["batch_id"] != "batch-ok" || ev.Details["decision_id"] != "dec-1" {
		t.Errorf("Ereignis ohne Herkunft: %+v", ev)
	}

	// Dieselbe Entscheidung reserviert nie ein zweites Mal, auch nicht nach Freigabe der ersten Lease.
	for _, state := range []string{"aktiv", "freigegeben"} {
		if state == "freigegeben" {
			moveLease(t, s, "l1", domain.LeaseActive, domain.LeaseReleased)
		}
		before := takeLeaseSnapshot(t, s)
		_, err := s.Update(ctxT(t), "tester", func(tx *Tx) error {
			_, err := tx.CreateLease(leaseFor("l2", "wo-1", "agent-2"), origin)
			return err
		})
		wantCode(t, err, CodeConflict)
		if !strings.Contains(err.Error(), "hat schon reserviert") {
			t.Errorf("%s: die Meldung nennt den Grund nicht: %v", state, err)
		}
		if after := takeLeaseSnapshot(t, s); after != before {
			t.Errorf("%s: Ablehnung schrieb: %+v -> %+v", state, before, after)
		}
	}

	for name, c := range map[string]struct {
		order  string
		agent  string
		origin LeaseOrigin
		code   Code
	}{
		"Entscheidung eines anderen Auftrags": {"wo-1", "agent-2", LeaseOrigin{"batch-ok", "dec-2"}, CodeInvalid},
		"Entscheidung ohne Auftragsziel":      {"wo-1", "agent-2", LeaseOrigin{"batch-ok", "dec-3"}, CodeInvalid},
		"unbekannte Entscheidung":             {"wo-1", "agent-2", LeaseOrigin{"batch-ok", "dec-9"}, CodeNotFound},
		"unbekannter Batch":                   {"wo-1", "agent-2", LeaseOrigin{"batch-9", "dec-1"}, CodeNotFound},
		"abgelehnter Batch":                   {"wo-1", "agent-2", LeaseOrigin{"batch-no", "dec-1"}, CodeState},
		"Batch ohne Entscheidung":             {"wo-1", "agent-2", LeaseOrigin{"batch-ok", ""}, CodeInvalid},
		"Entscheidung ohne Batch":             {"wo-1", "agent-2", LeaseOrigin{"", "dec-1"}, CodeInvalid},
	} {
		t.Run(name, func(t *testing.T) {
			before := takeLeaseSnapshot(t, s)
			_, err := s.Update(ctxT(t), "tester", func(tx *Tx) error {
				_, err := tx.CreateLease(leaseFor("l9", c.order, c.agent), &c.origin)
				return err
			})
			wantCode(t, err, c.code)
			if after := takeLeaseSnapshot(t, s); after != before {
				t.Errorf("Ablehnung schrieb: %+v -> %+v", before, after)
			}
		})
	}
}

// TestLeaseTriggersBackstopGo: Was Go prüft, sichert die Datenbank ein zweites Mal ab (Auftrag des Versuchs, Endzustände,
// unveränderliche Identität, nur angenommene Entscheidungen, nichts wird gelöscht).
func TestLeaseTriggersBackstopGo(t *testing.T) {
	s := leaseWorld(t)
	recordLeasePlan(t, s, "batch-ok", supervisor.VerdictAccepted)
	recordLeasePlan(t, s, "batch-no", supervisor.VerdictRejected)
	createLease(t, s, leaseFor("l1", "wo-1", "agent-1"), nil)
	moveLease(t, s, "l1", domain.LeaseActive, domain.LeaseReleased)
	createLease(t, s, leaseFor("l2", "wo-1", "agent-1"), nil)

	// Die Proben schreiben beendete Leases („released“ mit Endzeit), damit sie die Eindeutigkeit der aktiven Lease nicht
	// berühren. Die Ausgangsproben ohne Verstoß (p0, p2) gehen durch: die Abweisung liegt am Verstoß, nicht an der Form
	// der Anweisung.
	probe := func(name string, err error) {
		t.Helper()
		if err == nil {
			t.Errorf("%s: angenommen", name)
			return
		}
		if CodeOf(mapErr(err)) != CodeConstraint {
			t.Errorf("%s: %v (erwartet constraint)", name, err)
		}
	}
	insertReleased := func(id, order, agent, exec string, attempt any, batch, decision any, gen int) error {
		var e any
		if exec != "" {
			e = exec
		}
		return execSQL(t, s, `INSERT INTO leases (lease_id, project_id, work_order_id, execution_id, attempt, agent_id, generation, state,
			acquired_at, expires_at, released_at, batch_id, decision_id) VALUES (?, 'ccmas', ?, ?, ?, ?, ?, 'released', ?, ?, ?, ?, ?)`,
			id, order, e, attempt, agent, gen, ts, ts, ts, batch, decision)
	}
	must(t, insertReleased("p0", "wo-1", "agent-2", "exec-1", 1, nil, nil, 10))
	probe("Versuch eines anderen Auftrags", insertReleased("p1", "wo-1", "agent-2", "exec-2", 1, nil, nil, 11))
	must(t, insertReleased("p2", "wo-1", "agent-2", "", nil, "batch-ok", "dec-1", 12))
	probe("abgelehnter Batch", insertReleased("p3", "wo-1", "agent-2", "", nil, "batch-no", "dec-1", 13))
	probe("Entscheidung eines anderen Auftrags", insertReleased("p4", "wo-1", "agent-2", "", nil, "batch-ok", "dec-2", 14))
	probe("Batch ohne Entscheidung", insertReleased("p5", "wo-1", "agent-2", "", nil, "batch-ok", nil, 15))
	wantCode(t, mapErr(insertReleased("p6", "wo-1", "agent-2", "", nil, "batch-ok", "dec-1", 16)), CodeConflict) // dieselbe Entscheidung zweimal

	probe("Endzustand wiederbeleben", execSQL(t, s, "UPDATE leases SET state = 'active', released_at = NULL WHERE lease_id = 'l1'"))
	probe("Endzeit einer beendeten Lease ändern", execSQL(t, s, "UPDATE leases SET released_at = ? WHERE lease_id = 'l1'", "2026-11-10T12:30:00.000000000Z"))
	probe("Agent wechseln", execSQL(t, s, "UPDATE leases SET agent_id = 'agent-3' WHERE lease_id = 'l2'"))
	probe("Auftrag wechseln", execSQL(t, s, "UPDATE leases SET work_order_id = 'wo-2' WHERE lease_id = 'l2'"))
	probe("Generation ändern", execSQL(t, s, "UPDATE leases SET generation = 9 WHERE lease_id = 'l2'"))
	probe("Versuch eines anderen Auftrags binden", execSQL(t, s, "UPDATE leases SET execution_id = 'exec-2', attempt = 1 WHERE lease_id = 'l2'"))
	probe("löschen", execSQL(t, s, "DELETE FROM leases WHERE lease_id = 'l2'"))
	must(t, execSQL(t, s, "UPDATE leases SET execution_id = 'exec-1', attempt = 1 WHERE lease_id = 'l2'"))
	probe("zweite Bindung", execSQL(t, s, "UPDATE leases SET attempt = 2 WHERE lease_id = 'l2'"))
}

func TestLeaseNeverCrossesProjects(t *testing.T) {
	s := leaseWorld(t)
	seedOtherProject(t, s)
	l := leaseFor("lx", "wo-o", "agent-o") // Auftrag und Agent gehören zu otherProject
	_, err := s.Update(ctxT(t), "tester", func(tx *Tx) error { _, err := tx.CreateLease(l, nil); return err })
	wantCode(t, err, CodeNotFound)
	// Auftrag des eigenen Projekts, Agent des fremden.
	l = leaseFor("ly", "wo-1", "agent-o")
	_, err = s.Update(ctxT(t), "tester", func(tx *Tx) error { _, err := tx.CreateLease(l, nil); return err })
	wantCode(t, err, CodeNotFound)
	// Versuch des fremden Projekts.
	l = leaseFor("lz", "wo-1", "agent-1")
	l.ExecutionID, l.Attempt = "exec-o", 1
	_, err = s.Update(ctxT(t), "tester", func(tx *Tx) error { _, err := tx.CreateLease(l, nil); return err })
	wantCode(t, err, CodeNotFound)
}
