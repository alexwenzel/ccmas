package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/alexwenzel/ccmas/config"
	"github.com/alexwenzel/ccmas/internal/domain"
	"github.com/alexwenzel/ccmas/internal/persistence"
	"github.com/alexwenzel/ccmas/internal/profiles"
	"github.com/alexwenzel/ccmas/internal/skills"
	"github.com/alexwenzel/ccmas/internal/supervisor"
)

// Testhelfer dieser Seite des Pakets beginnen mit „tasks“, weil sich mehrere Tickets das Paket teilen.

const (
	tasksProject = "ccmas"
	tasksCommitA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	tasksRepo    = "../.." // Wurzel des Repositories (Skill-Katalog, Beispielkonfiguration)
)

func tasksCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// tasksClock liefert bei jedem Aufruf eine neue, streng steigende Zeit.
type tasksClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *tasksClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(time.Second)
	return c.t
}

// tasksTestStore öffnet und migriert eine echte SQLite-Datei in einem Testverzeichnis.
func tasksTestStore(t *testing.T) *persistence.Store {
	t.Helper()
	clock := &tasksClock{t: time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)}
	s, err := persistence.Open(tasksCtx(t), filepath.Join(t.TempDir(), "state", "ccmas.db"), persistence.WithClock(clock.Now))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.Migrate(tasksCtx(t)); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return s
}

func tasksUpdate(t *testing.T, s *persistence.Store, fn func(tx *persistence.Tx) error) persistence.UpdateResult {
	t.Helper()
	res, err := s.Update(tasksCtx(t), "tester", fn)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	return res
}

func tasksView(t *testing.T, s *persistence.Store, fn func(r *persistence.ReadTx) error) {
	t.Helper()
	if err := s.View(tasksCtx(t), fn); err != nil {
		t.Fatalf("View: %v", err)
	}
}

func tasksMust(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func tasksFixture[T any](t *testing.T, name string, parse func([]byte) (T, error)) T {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(tasksRepo, "contracts", "fixtures", "valid", name))
	if err != nil {
		t.Fatal(err)
	}
	v, err := parse(raw)
	if err != nil {
		t.Fatalf("Fixture %s: %v", name, err)
	}
	return v
}

// tasksOrder baut einen Auftrag (Vertragsteil aus der Fixture) für das Ziel mit den Abhängigkeiten.
func tasksOrder(t *testing.T, id, goal string, kind supervisor.OrderKind, deps ...string) persistence.WorkOrderRecord {
	t.Helper()
	w := tasksFixture(t, "work-order--pr_waiting.json", domain.ParseWorkOrder)
	w.WorkOrderID = id
	return persistence.WorkOrderRecord{WorkOrder: w, GoalID: goal, Kind: kind, Title: "Auftrag " + id, DependsOn: deps}
}

func tasksApproved(version string, criteria ...string) *supervisor.ConceptState {
	if criteria == nil {
		criteria = []string{}
	}
	return &supervisor.ConceptState{Version: version, Class: skills.ConceptRoutine, Criteria: criteria,
		Approval: &supervisor.ConceptApprovalInfo{Version: version, Approver: skills.ApproverConceptReviewer}}
}

// tasksComplete führt einen Auftrag über die erlaubten Übergänge nach „abgeschlossen“.
func tasksComplete(tx *persistence.Tx, id string) error {
	path := []domain.WorkOrderState{domain.WOWaiting, domain.WOInProgress, domain.WOValidation, domain.WOReview, domain.WOWaitingIntegration, domain.WOCompleted}
	for i := 1; i < len(path); i++ {
		if err := tx.TransitionWorkOrder(tasksProject, id, path[i-1], path[i]); err != nil {
			return err
		}
	}
	return nil
}

// tasksBase legt Projekt (max_workers 5), Profile, Agenten und zwei Ziele (ms-1 beauftragt, ms-2 fremd) an.
func tasksBase(t *testing.T, s *persistence.Store) {
	t.Helper()
	tasksUpdate(t, s, func(tx *persistence.Tx) error {
		_, err := tx.CreateProject(tasksFixture(t, "project-config--full.json", domain.ParseProjectConfig))
		return err
	})
	tasksUpdate(t, s, func(tx *persistence.Tx) error {
		for _, f := range []string{"agent-profile--implementer.json", "agent-profile--effort_fallback.json"} {
			if err := tx.RecordProfile(tasksProject, tasksFixture(t, f, domain.ParseAgentProfile)); err != nil {
				return err
			}
		}
		for id, role := range map[string]domain.Role{"agent-1": domain.RoleImplementer, "rev-1": domain.RoleReviewer} {
			if _, err := tx.CreateAgent(tasksProject, id, role); err != nil {
				return err
			}
		}
		for _, g := range []persistence.GoalRecord{
			{ProjectID: tasksProject, IntegrationMode: domain.ModePR, Goal: supervisor.Goal{Kind: domain.KindMilestone, ID: "ms-1", Title: "Meilenstein 1",
				AcceptanceCriteria: []string{"A1", "A2"}, Scope: []string{"CC-1", "CC-2"}, OutOfScope: []string{"CC-9"}}},
			{ProjectID: tasksProject, IntegrationMode: domain.ModeAuto, Goal: supervisor.Goal{Kind: domain.KindMilestone, ID: "ms-2", Title: "Meilenstein 2",
				AcceptanceCriteria: []string{"B1"}, Scope: []string{}, OutOfScope: []string{}}},
		} {
			if _, err := tx.CreateGoal(g); err != nil {
				return err
			}
		}
		return nil
	})
}

// tasksStartExecution legt Ausführung und Versuch 1 an und führt ihn bis „running“.
func tasksStartExecution(tx *persistence.Tx, t *testing.T, execID, orderID, agentID string, role domain.Role, profile domain.AgentProfile, at time.Time) error {
	t.Helper()
	if err := tx.CreateExecution(domain.Execution{ExecutionID: execID, ProjectID: tasksProject, WorkOrderID: orderID, AgentID: agentID, Role: role, CreatedAt: at}); err != nil {
		return err
	}
	if err := tx.StartAttempt(tasksProject, domain.ExecutionAttempt{ExecutionID: execID, Attempt: 1, ProfileID: profile.ProfileID,
		ProfileVersion: profile.Version, State: domain.ExecInitializing, StartedAt: at}); err != nil {
		return err
	}
	return tx.TransitionExecution(tasksProject, execID, 1, domain.ExecInitializing, domain.ExecRunning)
}

// tasksPolicy baut die Policy und die Angebote aus der Beispielkonfiguration und dem echten, geprüften Skill-Katalog.
func tasksPolicy(t *testing.T) (supervisor.Policy, SnapshotOffers) {
	t.Helper()
	cat, rules, err := skills.LoadChecked(tasksRepo)
	if err != nil {
		t.Fatalf("LoadChecked: %v", err)
	}
	caps, err := profiles.ParseCapabilities(config.ModelsExample())
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := profiles.ParseConfig(config.AgentsExample())
	if err != nil {
		t.Fatal(err)
	}
	res, err := profiles.NewResolver(cfg, cat)
	if err != nil {
		t.Fatal(err)
	}
	pol, err := supervisor.NewPolicy(res, caps, cat, rules)
	if err != nil {
		t.Fatal(err)
	}
	return pol, SnapshotOffers{Roles: supervisor.BuildRoleOffers(cfg, caps, cat, nil), Skills: supervisor.BuildSkillOffers(cat)}
}
