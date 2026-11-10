package persistence

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alexwenzel/ccmas/internal/domain"
	"github.com/alexwenzel/ccmas/internal/supervisor"
)

// fakeClock liefert bei jedem Aufruf eine neue, streng steigende Zeit (eine Sekunde Abstand), damit Zeitstempel
// in Tests unterscheidbar und reproduzierbar sind.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 11, 10, 12, 0, 0, 123456789, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(time.Second)
	return c.t
}

func ctxT(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// dbPath liefert einen frischen Pfad in einem Testverzeichnis.
func dbPath(t *testing.T) string { return filepath.Join(t.TempDir(), "state", "ccmas.db") }

// openStore öffnet (ohne Migration) und schließt am Testende.
func openStore(t *testing.T, path string, opts ...Option) *Store {
	t.Helper()
	opts = append([]Option{WithClock(newClock().Now)}, opts...)
	s, err := Open(ctxT(t), path, opts...)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// newStore öffnet und migriert.
func newStore(t *testing.T, opts ...Option) *Store {
	t.Helper()
	s := openStore(t, dbPath(t), opts...)
	if err := s.Migrate(ctxT(t)); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return s
}

// closeAndReopen schließt s und öffnet dieselbe Datei neu (Neustart des Dienstes).
func closeAndReopen(t *testing.T, s *Store, opts ...Option) *Store {
	t.Helper()
	path := s.Path()
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return openStore(t, path, opts...)
}

func mustUpdate(t *testing.T, s *Store, fn func(tx *Tx) error) UpdateResult {
	t.Helper()
	res, err := s.Update(ctxT(t), "tester", fn)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	return res
}

func mustView(t *testing.T, s *Store, fn func(r *ReadTx) error) {
	t.Helper()
	if err := s.View(ctxT(t), fn); err != nil {
		t.Fatalf("View: %v", err)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func mustVal[T any](t *testing.T, v T, err error) T {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// wantCode prüft den stabilen Fehlercode.
func wantCode(t *testing.T, err error, want Code) {
	t.Helper()
	if err == nil {
		t.Fatalf("kein Fehler, erwartet Code %q", want)
	}
	if got := CodeOf(err); got != want {
		t.Fatalf("Fehlercode %q (%v), erwartet %q", got, err, want)
	}
}

// ---- Fixtures: gültige Vertragsdokumente aus contracts/fixtures/valid ------------------------------------

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "contracts", "fixtures", "valid", name))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

const projectID = "ccmas"

func fxProject(t *testing.T) domain.ProjectConfig {
	t.Helper()
	cfg, err := domain.ParseProjectConfig(fixture(t, "project-config--full.json"))
	must(t, err)
	return cfg
}

func fxProfile(t *testing.T) domain.AgentProfile {
	t.Helper()
	p, err := domain.ParseAgentProfile(fixture(t, "agent-profile--implementer.json"))
	must(t, err)
	return p
}

func fxReviewerProfile(t *testing.T) domain.AgentProfile {
	t.Helper()
	p, err := domain.ParseAgentProfile(fixture(t, "agent-profile--effort_fallback.json"))
	must(t, err)
	return p
}

func fxSnapshot(t *testing.T) domain.SkillSnapshot {
	t.Helper()
	s, err := domain.ParseSkillSnapshot(fixture(t, "skill-snapshot--two_skills.json"))
	must(t, err)
	return s
}

func fxBatch(t *testing.T) domain.DecisionBatch {
	t.Helper()
	b, err := domain.ParseDecisionBatch(fixture(t, "decision-batch--three_decisions.json"))
	must(t, err)
	return b
}

func fxWorkOrder(t *testing.T, id string) domain.WorkOrder {
	t.Helper()
	w, err := domain.ParseWorkOrder(fixture(t, "work-order--pr_waiting.json"))
	must(t, err)
	w.WorkOrderID = id
	return w
}

const (
	commitA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	commitB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

var snapshotPath = "/work/exec/ccmas/skills/1c27324f013705cbf0c49f3d3bb103c7069ed5988889d426c10cfc25684f0a31"

// seedBase legt Projekt, Profil, Skill-Snapshot, Agent, Ziel und einen Auftrag an (eine Transaktion je Schritt).
func seedBase(t *testing.T, s *Store) {
	t.Helper()
	mustUpdate(t, s, func(tx *Tx) error {
		_, err := tx.CreateProject(fxProject(t))
		return err
	})
	mustUpdate(t, s, func(tx *Tx) error {
		if err := tx.RecordProfile(projectID, fxProfile(t)); err != nil {
			return err
		}
		if err := tx.RecordProfile(projectID, fxReviewerProfile(t)); err != nil {
			return err
		}
		if err := tx.RecordSkillSnapshot(projectID, fxSnapshot(t), snapshotPath); err != nil {
			return err
		}
		if _, err := tx.CreateAgent(projectID, "agent-1", domain.RoleImplementer); err != nil {
			return err
		}
		if _, err := tx.CreateGoal(GoalRecord{
			ProjectID: projectID, Goal: supervisor.Goal{Kind: domain.KindMilestone, ID: "ms-1", Title: "Meilenstein 1",
				AcceptanceCriteria: []string{"A1"}, Scope: []string{"CC-1"}, OutOfScope: []string{"CC-9"}},
			IntegrationMode: domain.ModePR,
		}); err != nil {
			return err
		}
		return nil
	})
	mustUpdate(t, s, func(tx *Tx) error {
		_, err := tx.CreateWorkOrder(WorkOrderRecord{WorkOrder: fxWorkOrder(t, "wo-1"), GoalID: "ms-1", Kind: supervisor.OrderImplementation, Title: "Auftrag 1"})
		return err
	})
}

// countEvents zählt die Ereignisse (optional eines Projekts) über die Lese-API.
func countEvents(t *testing.T, s *Store) int {
	t.Helper()
	var n int
	mustView(t, s, func(r *ReadTx) error {
		evs, err := r.Events(projectID, EventFilter{})
		n = len(evs)
		return err
	})
	return n
}

func revisionOf(t *testing.T, s *Store, project string) int64 {
	t.Helper()
	var rev int64
	mustView(t, s, func(r *ReadTx) error {
		var err error
		rev, err = r.Revision(project)
		return err
	})
	return rev
}

func lastEvent(t *testing.T, s *Store) domain.AuditEvent {
	t.Helper()
	var evs []domain.AuditEvent
	mustView(t, s, func(r *ReadTx) error {
		var err error
		evs, err = r.Events(projectID, EventFilter{})
		return err
	})
	if len(evs) == 0 {
		t.Fatal("keine Ereignisse")
	}
	return evs[len(evs)-1]
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }

func goalOf(id string) supervisor.Goal {
	return supervisor.Goal{Kind: domain.KindTicket, ID: id, Title: "Ziel " + id, AcceptanceCriteria: []string{"ok"}, Scope: []string{}, OutOfScope: []string{}}
}

func fxConceptProfile(t *testing.T) domain.AgentProfile {
	t.Helper()
	p := fxProfile(t)
	p.ProfileID, p.Role, p.SkillIDs = "concept-default", domain.RoleConcept, []string{"ccmas-concept-ticket"}
	return p
}
