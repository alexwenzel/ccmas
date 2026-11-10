package persistencetest

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/alexwenzel/ccmas/internal/persistence"
)

// TestSeedWritesThroughRepositories: Seed liefert einen Zustand, der über die Lese-API genau so erscheint, wie er
// beschrieben ist.
func TestSeedWritesThroughRepositories(t *testing.T) {
	s, err := persistence.Open(context.Background(), filepath.Join(t.TempDir(), "state", "ccmas.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := Seed(t, s)
	if got.Revision < 3 || got.Events < 5 {
		t.Errorf("zu wenig Zustand: %+v", got)
	}
	err = s.View(context.Background(), func(r *persistence.ReadTx) error {
		orders, err := r.WorkOrders(ProjectID)
		if err != nil {
			return err
		}
		counts := map[string]int{}
		for _, o := range orders {
			counts[string(o.State)]++
		}
		for st, n := range got.WorkOrders {
			if counts[string(st)] != n {
				t.Errorf("Aufträge im Zustand %s: %d, erwartet %d", st, counts[string(st)], n)
			}
		}
		active, err := r.ActiveExecutions(ProjectID)
		if err != nil {
			return err
		}
		open, err := r.OpenQuestions(ProjectID)
		if err != nil {
			return err
		}
		if len(active) != got.ActiveExecutions || len(open) != got.OpenQuestions {
			t.Errorf("aktiv %d, offen %d", len(active), len(open))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
