package persistence

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"reflect"
	"sync"
	"testing"
)

func mig(version int, name, sql string) Migration {
	return Migration{Version: version, Name: name, SQL: sql, SHA256: sumSQL(sql)}
}

func sumSQL(sql string) string {
	h := sha256.Sum256([]byte(sql))
	return hex.EncodeToString(h[:])
}

func tableSQL(n int) string {
	return fmt.Sprintf("CREATE TABLE t%d (id INTEGER PRIMARY KEY, v TEXT NOT NULL) STRICT;", n)
}

func fileSHA(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	must(t, err)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func TestKnownMigrationsWellFormed(t *testing.T) {
	ms := Migrations()
	if len(ms) == 0 {
		t.Fatal("keine eingebettete Migration")
	}
	prev := 0
	for _, m := range ms {
		if m.Version <= prev {
			t.Errorf("Version %d nicht streng aufsteigend nach %d", m.Version, prev)
		}
		prev = m.Version
		if m.SQL == "" || len(m.SHA256) != 64 || m.Name == "" {
			t.Errorf("Migration %d unvollständig: %+v", m.Version, m)
		}
	}
	if ms[0].Version != 1 || ms[0].Name != "initial" {
		t.Errorf("erste Migration = %d %q", ms[0].Version, ms[0].Name)
	}
}

func TestMigrationIdempotent(t *testing.T) {
	s := newStore(t)
	ctx := ctxT(t)
	first, err := s.AppliedMigrations(ctx)
	must(t, err)
	v1, err := s.SchemaVersion(ctx)
	must(t, err)
	want := Migrations()
	if len(first) != len(want) || first[0].Version != 1 || first[0].SHA256 != want[0].SHA256 || first[0].AppliedAt.IsZero() {
		t.Fatalf("Historie = %+v, bekannt %+v", first, want)
	}
	if v1 != want[len(want)-1].Version {
		t.Fatalf("user_version = %d", v1)
	}

	must(t, s.Migrate(ctx))
	must(t, s.Migrate(ctx))
	second, err := s.AppliedMigrations(ctx)
	must(t, err)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("Historie änderte sich beim zweiten Migrate:\n%+v\n%+v", first, second)
	}
	if v2, _ := s.SchemaVersion(ctx); v2 != v1 {
		t.Fatalf("user_version %d -> %d", v1, v2)
	}
}

func TestMigrationGapsAllowed(t *testing.T) {
	list := []Migration{mig(1, "a", tableSQL(1)), mig(3, "c", tableSQL(3)), mig(7, "g", tableSQL(7))}
	s := openStore(t, dbPath(t), withMigrations(list))
	must(t, s.Migrate(ctxT(t)))
	if v, _ := s.SchemaVersion(ctxT(t)); v != 7 {
		t.Fatalf("user_version = %d, erwartet 7 (höchste angewandte Version)", v)
	}
	applied, err := s.AppliedMigrations(ctxT(t))
	must(t, err)
	if len(applied) != 3 || applied[1].Version != 3 || applied[1].Name != "c" {
		t.Fatalf("Historie = %+v", applied)
	}
}

func TestMigrationChecksumMismatch(t *testing.T) {
	path := dbPath(t)
	s := openStore(t, path, withMigrations([]Migration{mig(1, "a", tableSQL(1)), mig(2, "b", tableSQL(2))}))
	must(t, s.Migrate(ctxT(t)))
	before, err := s.AppliedMigrations(ctxT(t))
	must(t, err)
	must(t, s.Close())

	// Migration 1 wurde nach dem Anwenden geändert.
	changed := []Migration{mig(1, "a", tableSQL(1)+"\n-- nachträglich geändert"), mig(2, "b", tableSQL(2)), mig(3, "c", tableSQL(3))}
	s2 := openStore(t, path, withMigrations(changed))
	err = s2.Migrate(ctxT(t))
	wantCode(t, err, CodeChecksumMismatch)
	after, err := s2.AppliedMigrations(ctxT(t))
	must(t, err)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("Historie nach dem Fehler verändert: %+v", after)
	}
	// Auch ohne ausstehende Migration bleibt die Abweichung ein Fehler.
	s3 := openStore(t, path, withMigrations(changed[:2]))
	wantCode(t, s3.Migrate(ctxT(t)), CodeChecksumMismatch)
}

func TestSchemaTooNew(t *testing.T) {
	path := dbPath(t)
	s := openStore(t, path, withMigrations([]Migration{mig(1, "a", tableSQL(1)), mig(2, "b", tableSQL(2)), mig(3, "c", tableSQL(3))}))
	must(t, s.Migrate(ctxT(t)))
	must(t, s.Close())
	before := fileSHA(t, path)
	info, _ := os.Stat(path)

	old := openStore(t, path, withMigrations([]Migration{mig(1, "a", tableSQL(1)), mig(2, "b", tableSQL(2))}))
	wantCode(t, old.Migrate(ctxT(t)), CodeSchemaTooNew)
	// Auch der Zugriff über die Repositories ist verweigert, ohne Schreibwirkung.
	_, err := old.Update(ctxT(t), "tester", func(tx *Tx) error { return nil })
	wantCode(t, err, CodeSchemaTooNew)
	wantCode(t, old.View(ctxT(t), func(r *ReadTx) error { return nil }), CodeSchemaTooNew)
	must(t, old.Close())

	if got := fileSHA(t, path); got != before {
		t.Fatal("die Datenbankdatei hat sich beim Versuch mit zu neuem Schema verändert")
	}
	if after, _ := os.Stat(path); after.Size() != info.Size() {
		t.Fatalf("Dateigröße %d -> %d", info.Size(), after.Size())
	}
	if st, err := os.Stat(path + "-wal"); err == nil && st.Size() != 0 {
		t.Fatalf("WAL-Datei mit %d Bytes zurückgeblieben", st.Size())
	}
}

// TestSchemaTooNewByUserVersionOnly: eine fremde oder neuere Datenbank ohne Historie, aber mit höherer
// user_version, wird nicht angefasst.
func TestSchemaTooNewByUserVersionOnly(t *testing.T) {
	path := dbPath(t)
	s := openStore(t, path)
	_, err := s.w.ExecContext(ctxT(t), "PRAGMA user_version = 99")
	must(t, err)
	wantCode(t, s.Migrate(ctxT(t)), CodeSchemaTooNew)
	tables := 0
	must(t, s.r.QueryRowContext(ctxT(t), "SELECT count(*) FROM sqlite_master WHERE type = 'table'").Scan(&tables))
	if tables != 0 {
		t.Fatalf("%d Tabellen angelegt", tables)
	}
}

func TestMigrationHistoryMismatch(t *testing.T) {
	base := []Migration{mig(1, "a", tableSQL(1)), mig(2, "b", tableSQL(2)), mig(3, "c", tableSQL(3))}
	cases := map[string]struct {
		known []Migration
	}{
		"angewandte Version fehlt in der bekannten Liste": {[]Migration{base[0], base[2]}},
		"anderer Name": {[]Migration{base[0], mig(2, "anders", tableSQL(2)), base[2]}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			path := dbPath(t)
			s := openStore(t, path, withMigrations(base[:2]))
			must(t, s.Migrate(ctxT(t)))
			must(t, s.Close())
			s2 := openStore(t, path, withMigrations(tc.known))
			wantCode(t, s2.Migrate(ctxT(t)), CodeHistoryMismatch)
		})
	}

	t.Run("user_version passt nicht zur Historie", func(t *testing.T) {
		path := dbPath(t)
		s := openStore(t, path, withMigrations(base[:2]))
		must(t, s.Migrate(ctxT(t)))
		_, err := s.w.ExecContext(ctxT(t), "PRAGMA user_version = 1")
		must(t, err)
		wantCode(t, s.Migrate(ctxT(t)), CodeHistoryMismatch)
	})
	t.Run("fremde Datenbank ohne Historie", func(t *testing.T) {
		path := dbPath(t)
		s := openStore(t, path, withMigrations(base))
		_, err := s.w.ExecContext(ctxT(t), "CREATE TABLE fremd (x INTEGER)")
		must(t, err)
		wantCode(t, s.Migrate(ctxT(t)), CodeHistoryMismatch)
	})
}

// TestMigrationAtomic: jede Migration läuft in einer eigenen Transaktion samt Eintrag; eine fehlerhafte
// hinterlässt nichts, frühere bleiben angewandt.
func TestMigrationAtomic(t *testing.T) {
	broken := "CREATE TABLE halb (id INTEGER PRIMARY KEY) STRICT;\nINSERT INTO gibt_es_nicht VALUES (1);"
	s := openStore(t, dbPath(t), withMigrations([]Migration{mig(1, "a", tableSQL(1)), mig(2, "kaputt", broken), mig(3, "c", tableSQL(3))}))
	err := s.Migrate(ctxT(t))
	if err == nil {
		t.Fatal("kaputte Migration wurde angewandt")
	}
	applied, aerr := s.AppliedMigrations(ctxT(t))
	must(t, aerr)
	if len(applied) != 1 || applied[0].Version != 1 {
		t.Fatalf("Historie = %+v, erwartet nur Version 1", applied)
	}
	if v, _ := s.SchemaVersion(ctxT(t)); v != 1 {
		t.Fatalf("user_version = %d", v)
	}
	var n int
	must(t, s.r.QueryRowContext(ctxT(t), "SELECT count(*) FROM sqlite_master WHERE name IN ('halb','t3')").Scan(&n))
	if n != 0 {
		t.Fatalf("%d Tabellen der fehlgeschlagenen oder späteren Migration vorhanden", n)
	}
}

func TestConcurrentMigrate(t *testing.T) {
	path := dbPath(t)
	list := withMigrations([]Migration{mig(1, "a", tableSQL(1)), mig(2, "b", tableSQL(2))})
	stores := []*Store{openStore(t, path, list), openStore(t, path, list), openStore(t, path, list)}
	var wg sync.WaitGroup
	errs := make([]error, len(stores))
	for i, s := range stores {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = s.Migrate(ctxT(t))
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("Migrate %d: %v", i, err)
		}
	}
	applied, err := stores[0].AppliedMigrations(ctxT(t))
	must(t, err)
	if len(applied) != 2 {
		t.Fatalf("Historie = %+v", applied)
	}
}

func TestRepositoriesRequireMigratedSchema(t *testing.T) {
	s := openStore(t, dbPath(t))
	_, err := s.Update(ctxT(t), "tester", func(tx *Tx) error {
		t.Error("Callback darf auf nicht migrierter Datenbank nicht laufen")
		return nil
	})
	wantCode(t, err, CodeSchemaOutdated)
	wantCode(t, s.View(ctxT(t), func(r *ReadTx) error { t.Error("Callback darf nicht laufen"); return nil }), CodeSchemaOutdated)
	// Open hat nicht implizit migriert.
	if v, _ := s.SchemaVersion(ctxT(t)); v != 0 {
		t.Fatalf("user_version = %d nach Open", v)
	}
	must(t, s.Migrate(ctxT(t)))
	mustView(t, s, func(r *ReadTx) error { return nil })
}
