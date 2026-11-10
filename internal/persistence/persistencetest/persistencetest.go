// Package persistencetest legt für Tests einen Zustand an, der ausschließlich über die Repositories des Pakets
// persistence geschrieben ist (kein rohes SQL): ein Projekt mit Profilen, Agent und Ziel, Aufträge in verschiedenen
// Zuständen, eine laufende Ausführung und eine offene Frage. Tests anderer Pakete (zum Beispiel der CLI) prüfen damit
// gegen echte, nicht erfundene Werte.
//
// Das Paket gehört in Tests; es bezieht seine Vertragsdokumente aus contracts/fixtures/valid des Repositories.
package persistencetest

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/alexwenzel/ccmas/internal/domain"
	"github.com/alexwenzel/ccmas/internal/persistence"
	"github.com/alexwenzel/ccmas/internal/supervisor"
)

// ProjectID ist die Projekt-ID des angelegten Projekts.
const ProjectID = "ccmas"

// Seeded beschreibt, was Seed geschrieben hat.
type Seeded struct {
	ProjectID string
	// Revision ist die Zustandsrevision nach dem Schreiben, gelesen aus dem Store.
	Revision int64
	// WorkOrders zählt die angelegten Aufträge je Zustand.
	WorkOrders map[domain.WorkOrderState]int
	// ActiveExecutions und OpenQuestions sind die angelegten Anzahlen.
	ActiveExecutions int
	OpenQuestions    int
	// Events ist die Zahl der Audit-Ereignisse des Projekts.
	Events int
}

// Seed schreibt den Zustand in den (migrierten) Store.
func Seed(t *testing.T, s *persistence.Store) Seeded {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	var clock = time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	tick := func() time.Time { clock = clock.Add(time.Second); return clock }
	update := func(fn func(tx *persistence.Tx) error) {
		t.Helper()
		if _, err := s.Update(ctx, "persistencetest", fn); err != nil {
			t.Fatalf("persistencetest: %v", err)
		}
	}

	profile := mustParse(t, "agent-profile--implementer.json", domain.ParseAgentProfile)
	update(func(tx *persistence.Tx) error {
		_, err := tx.CreateProject(mustParse(t, "project-config--full.json", domain.ParseProjectConfig))
		return err
	})
	update(func(tx *persistence.Tx) error {
		if err := tx.RecordProfile(ProjectID, profile); err != nil {
			return err
		}
		if _, err := tx.CreateAgent(ProjectID, "agent-1", domain.RoleImplementer); err != nil {
			return err
		}
		_, err := tx.CreateGoal(persistence.GoalRecord{
			ProjectID: ProjectID,
			Goal: supervisor.Goal{Kind: domain.KindMilestone, ID: "ms-1", Title: "Meilenstein 1",
				AcceptanceCriteria: []string{"A1"}, Scope: []string{"CC-1"}, OutOfScope: []string{"CC-9"}},
			IntegrationMode: domain.ModePR,
		})
		return err
	})
	order := func(id string) persistence.WorkOrderRecord {
		w := mustParse(t, "work-order--pr_waiting.json", domain.ParseWorkOrder)
		w.WorkOrderID = id
		return persistence.WorkOrderRecord{WorkOrder: w, GoalID: "ms-1", Kind: supervisor.OrderImplementation, Title: "Auftrag " + id}
	}
	update(func(tx *persistence.Tx) error {
		for _, id := range []string{"wo-1", "wo-2", "wo-3"} {
			if _, err := tx.CreateWorkOrder(order(id)); err != nil {
				return err
			}
		}
		return nil
	})
	update(func(tx *persistence.Tx) error {
		if err := tx.TransitionWorkOrder(ProjectID, "wo-2", domain.WOWaiting, domain.WOInProgress); err != nil {
			return err
		}
		return tx.TransitionWorkOrder(ProjectID, "wo-3", domain.WOWaiting, domain.WOBlocked)
	})
	update(func(tx *persistence.Tx) error {
		if err := tx.CreateExecution(domain.Execution{ExecutionID: "exec-1", ProjectID: ProjectID, WorkOrderID: "wo-2",
			Role: domain.RoleImplementer, CreatedAt: tick()}); err != nil {
			return err
		}
		if err := tx.StartAttempt(ProjectID, domain.ExecutionAttempt{ExecutionID: "exec-1", Attempt: 1, ProfileID: profile.ProfileID,
			ProfileVersion: profile.Version, State: domain.ExecInitializing, StartedAt: tick()}); err != nil {
			return err
		}
		return tx.TransitionExecution(ProjectID, "exec-1", 1, domain.ExecInitializing, domain.ExecRunning)
	})
	update(func(tx *persistence.Tx) error {
		return tx.CreateQuestion(ProjectID, domain.Question{QuestionID: "q-1", WorkOrderID: "wo-2", ExecutionID: "exec-1", Attempt: 1,
			Text: "Welche Variante?", Context: "Zwei Wege sind möglich.", State: domain.QuestionOpen, CreatedAt: tick()})
	})

	out := Seeded{
		ProjectID:        ProjectID,
		WorkOrders:       map[domain.WorkOrderState]int{domain.WOWaiting: 1, domain.WOInProgress: 1, domain.WOBlocked: 1},
		ActiveExecutions: 1,
		OpenQuestions:    1,
	}
	err := s.View(ctx, func(r *persistence.ReadTx) error {
		var err error
		if out.Revision, err = r.Revision(ProjectID); err != nil {
			return err
		}
		evs, err := r.Events(ProjectID, persistence.EventFilter{})
		out.Events = len(evs)
		return err
	})
	if err != nil {
		t.Fatalf("persistencetest: %v", err)
	}
	return out
}

func mustParse[T any](t *testing.T, name string, parse func([]byte) (T, error)) T {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(fixtureDir(t), name))
	if err != nil {
		t.Fatal(err)
	}
	v, err := parse(raw)
	if err != nil {
		t.Fatalf("Fixture %s: %v", name, err)
	}
	return v
}

func fixtureDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("Quellort nicht bestimmbar")
	}
	return filepath.Join(filepath.Dir(file), "..", "..", "..", "contracts", "fixtures", "valid")
}
