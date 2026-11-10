package persistence

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"modernc.org/sqlite"
)

func TestOpenRejectsBadPaths(t *testing.T) {
	for name, p := range map[string]string{
		"relativ":                     "state/ccmas.db",
		"leer":                        "",
		"nicht kanonisch":             "/tmp/x/../ccmas.db",
		"mit Fragezeichen":            "/tmp/ccmas?mode=ro.db",
		"mit Steuerzeichen":           "/tmp/cc\nmas.db",
		"abschließender Schrägstrich": "/tmp/ccmas/",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Open(ctxT(t), p)
			wantCode(t, err, CodeInvalid)
		})
	}
}

func TestStrictPragmasEnabled(t *testing.T) {
	openStore(t, dbPath(t))
	if !sqlite.StrictPragmasEnabled() {
		t.Fatal("StrictPragmas ist nicht aktiv: mehrere Anweisungen in einem _pragma wären erlaubt")
	}
	db, err := sql.Open("sqlite", dbPath(t)+"?_pragma=foreign_keys(1);SELECT%201")
	must(t, err)
	defer db.Close()
	if err := db.PingContext(ctxT(t)); err == nil {
		t.Fatal("mehrere Anweisungen in _pragma wurden angenommen")
	}
}

type pragmaWant struct {
	name string
	want string
}

func readPragmas(t *testing.T, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, names []pragmaWant) {
	t.Helper()
	for _, p := range names {
		var got string
		must(t, q.QueryRowContext(ctxT(t), "PRAGMA "+p.name).Scan(&got))
		if !strings.EqualFold(got, p.want) {
			t.Errorf("PRAGMA %s = %q, erwartet %q", p.name, got, p.want)
		}
	}
}

// TestPragmasOnEveryConnection: jede Verbindung beider Pools trägt die Pragmas aus dem DSN, nicht nur die erste.
func TestPragmasOnEveryConnection(t *testing.T) {
	s := newStore(t, WithReadConns(4))
	ctx := ctxT(t)

	// Lesepool: alle erlaubten Verbindungen gleichzeitig belegen.
	var conns []*sql.Conn
	for i := 0; i < 4; i++ {
		c, err := s.r.Conn(ctx)
		must(t, err)
		conns = append(conns, c)
		readPragmas(t, c, []pragmaWant{
			{"busy_timeout", "5000"}, {"foreign_keys", "1"}, {"query_only", "1"}, {"journal_mode", "wal"},
		})
	}
	for _, c := range conns {
		must(t, c.Close())
	}

	// Schreibpool.
	readPragmas(t, s.w, []pragmaWant{
		{"busy_timeout", "5000"}, {"foreign_keys", "1"}, {"query_only", "0"}, {"journal_mode", "wal"}, {"synchronous", "2"},
	})
	if st := s.w.Stats(); st.MaxOpenConnections != 1 {
		t.Fatalf("Schreibpool MaxOpenConnections = %d, erwartet 1", st.MaxOpenConnections)
	}

	// Frische Verbindungen mit dem DSN des Schreibpools (etwa nach Verbindungsverlust) tragen sie ebenfalls.
	for i := 0; i < 3; i++ {
		db, err := sql.Open("sqlite", writerDSN(s.path, defaultBusyTimeout))
		must(t, err)
		readPragmas(t, db, []pragmaWant{{"busy_timeout", "5000"}, {"foreign_keys", "1"}, {"synchronous", "2"}, {"journal_mode", "wal"}})
		must(t, db.Close())
	}
}

// TestReadPoolCannotWrite: der Lesepool ist query_only; geschrieben wird nur über Update.
func TestReadPoolCannotWrite(t *testing.T) {
	s := newStore(t)
	_, err := s.r.ExecContext(ctxT(t), "CREATE TABLE x (a INTEGER)")
	if err == nil {
		t.Fatal("Schreiben über den Lesepool gelang")
	}
	err = s.View(ctxT(t), func(r *ReadTx) error {
		_, err := r.q.ExecContext(ctxT(t), "DELETE FROM projects")
		return err
	})
	if err == nil {
		t.Fatal("Schreiben in einer Lesetransaktion gelang")
	}
}

func TestClosedStore(t *testing.T) {
	s := newStore(t)
	must(t, s.Close())
	must(t, s.Close()) // idempotent
	_, err := s.Update(ctxT(t), "tester", func(tx *Tx) error { return nil })
	wantCode(t, err, CodeClosed)
	wantCode(t, s.View(ctxT(t), func(r *ReadTx) error { return nil }), CodeClosed)
	wantCode(t, s.Migrate(ctxT(t)), CodeClosed)
}

// TestTxUnusableAfterReturn: ein aus dem Callback herausgereichtes Tx schreibt nicht mehr.
func TestTxUnusableAfterReturn(t *testing.T) {
	s := newStore(t)
	var leaked *Tx
	mustUpdate(t, s, func(tx *Tx) error { leaked = tx; return nil })
	_, err := leaked.CreateProject(fxProject(t))
	wantCode(t, err, CodeClosed)
}

func TestUpdateRejectsBadActor(t *testing.T) {
	s := newStore(t)
	_, err := s.Update(ctxT(t), "not ok", func(tx *Tx) error { return nil })
	wantCode(t, err, CodeInvalid)
}

// TestBusyIsStableCode: Ablauf des busy_timeout ist ein stabiler Fehlercode, kein Panic und kein roher Treiberfehler.
func TestBusyIsStableCode(t *testing.T) {
	path := dbPath(t)
	holder := openStore(t, path, WithBusyTimeout(100*time.Millisecond))
	must(t, holder.Migrate(ctxT(t)))
	other := openStore(t, path, WithBusyTimeout(100*time.Millisecond))

	hold, err := holder.w.BeginTx(ctxT(t), nil) // IMMEDIATE: hält die Schreibsperre
	must(t, err)
	defer hold.Rollback()

	start := time.Now()
	_, err = other.Update(ctxT(t), "tester", func(tx *Tx) error { return nil })
	wantCode(t, err, CodeBusy)
	if d := time.Since(start); d < 50*time.Millisecond || d > 5*time.Second {
		t.Fatalf("busy nach %v, erwartet etwa der Timeout von 100ms", d)
	}
	must(t, hold.Rollback())
	// Nach Freigabe gelingt der Schreibvorgang wieder.
	if _, err := other.Update(ctxT(t), "tester", func(tx *Tx) error { return nil }); err != nil {
		t.Fatalf("Update nach Freigabe: %v", err)
	}
}

// TestConcurrentWriters: zwei Store-Instanzen auf derselben Datei, je mehrere Goroutinen. Alle Schreibvorgänge
// gelingen; die Revision am Ende entspricht der Zahl der Änderungstransaktionen.
func TestConcurrentWriters(t *testing.T) {
	path := dbPath(t)
	a := openStore(t, path)
	must(t, a.Migrate(ctxT(t)))
	b := openStore(t, path)
	mustUpdate(t, a, func(tx *Tx) error { _, err := tx.CreateProject(fxProject(t)); return err })
	base := revisionOf(t, a, projectID)

	const goroutines, perGoroutine = 4, 8
	var wg sync.WaitGroup
	errs := make(chan error, 2*goroutines*perGoroutine)
	for si, s := range []*Store{a, b} {
		for g := 0; g < goroutines; g++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := 0; i < perGoroutine; i++ {
					id := fmt.Sprintf("goal-%d-%d-%d", si, g, i)
					_, err := s.Update(context.Background(), "tester", func(tx *Tx) error {
						_, err := tx.CreateGoal(GoalRecord{ProjectID: projectID, IntegrationMode: "pr",
							Goal: goalOf(id)})
						return err
					})
					if err != nil {
						errs <- fmt.Errorf("%s: %w", id, err)
					}
				}
			}()
		}
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	total := int64(2 * goroutines * perGoroutine)
	if got := revisionOf(t, b, projectID); got != base+total {
		t.Fatalf("Revision = %d, erwartet %d (Basis %d + %d Transaktionen)", got, base+total, base, total)
	}
	var goals int
	mustView(t, a, func(r *ReadTx) error {
		gs, err := r.Goals(projectID)
		goals = len(gs)
		return err
	})
	if goals != int(total) {
		t.Fatalf("%d Ziele, erwartet %d", goals, total)
	}
}
