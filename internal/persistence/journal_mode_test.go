package persistence

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

// Diese Tests belegen, dass Open und ein fehlgeschlagenes Migrate eine vorhandene Datei nicht anfassen (auch nicht
// ihren journal_mode) und dass erst Migrate nach bestandener Prüfung auf WAL umstellt. Die Dateien entstehen im Test
// selbst über den Treiber im Modus journal_mode=DELETE.

// rollbackDB legt eine Datenbank im Rollback-Journal-Modus an und liefert Pfad und Verzeichnis. Danach ist nichts
// offen und kein -journal übrig.
func rollbackDB(t *testing.T, stmts ...string) (path, dir string) {
	t.Helper()
	dir = filepath.Join(t.TempDir(), "state")
	must(t, os.MkdirAll(dir, 0o700))
	path = filepath.Join(dir, "ccmas.db")
	db, err := sql.Open("sqlite", path)
	must(t, err)
	defer db.Close()
	db.SetMaxOpenConns(1)
	var mode string
	must(t, db.QueryRowContext(ctxT(t), "PRAGMA journal_mode = DELETE").Scan(&mode))
	if mode != "delete" {
		t.Fatalf("journal_mode %q, erwartet delete", mode)
	}
	for _, s := range stmts {
		if _, err := db.ExecContext(ctxT(t), s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	must(t, db.Close())
	if got := headerMode(t, path); got != "delete" {
		t.Fatalf("Dateikopf zeigt %q, erwartet delete", got)
	}
	return path, dir
}

// headerMode liest den journal_mode aus dem Dateikopf (ohne die Datei zu öffnen).
func headerMode(t *testing.T, path string) string {
	t.Helper()
	h, err := readHeader(path)
	must(t, err)
	return journalModeFromHeader(h)
}

// TestOpenAndFailedMigrateLeaveExistingFileUntouched: (a) zu neue Datenbank, (b) fremde Datenbank, ferner abweichende
// Historie und geänderte Prüfsumme. Open und Migrate (und Update/View) schreiben nichts: Datei byte-identisch
// (SHA-256), kein -wal, kein -shm, kein -journal.
func TestOpenAndFailedMigrateLeaveExistingFileUntouched(t *testing.T) {
	known := Migrations()[0]
	history := func(name, sum string) []string {
		return []string{
			"CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, name TEXT NOT NULL, sha256 TEXT NOT NULL, applied_at TEXT NOT NULL) STRICT",
			"INSERT INTO schema_migrations VALUES (1, '" + name + "', '" + sum + "', '2026-11-10T12:00:00.000000000Z')",
			"PRAGMA user_version = 1",
		}
	}
	cases := []struct {
		name  string
		stmts []string
		want  Code
	}{
		{"zu neu (user_version 99 und fremde Tabelle)", []string{"CREATE TABLE fremd (a INTEGER)", "INSERT INTO fremd VALUES (1)", "PRAGMA user_version = 99"}, CodeSchemaTooNew},
		{"fremde Tabelle ohne schema_migrations", []string{"CREATE TABLE fremd (a INTEGER)", "INSERT INTO fremd VALUES (1)"}, CodeForeignDatabase},
		{"fremde Tabelle, user_version 1", []string{"CREATE TABLE fremd (a INTEGER)", "PRAGMA user_version = 1"}, CodeForeignDatabase},
		{"nur eine fremde Sicht (View)", []string{"CREATE VIEW fremd AS SELECT 1 AS a"}, CodeForeignDatabase},
		{"abweichender Name der Historie", history("andere", known.SHA256), CodeHistoryMismatch},
		{"geänderte Prüfsumme", history(known.Name, "0000000000000000000000000000000000000000000000000000000000000000"), CodeChecksumMismatch},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path, dir := rollbackDB(t, tc.stmts...)
			before := fileSet(t, dir)
			if len(before) != 1 {
				t.Fatalf("Ausgangsdateien %v", names(before))
			}

			s, err := Open(ctxT(t), path)
			must(t, err)
			wantCode(t, s.Migrate(ctxT(t)), tc.want)
			wantCode(t, s.Migrate(ctxT(t)), tc.want) // wiederholt: gleiches Ergebnis, weiterhin nichts geschrieben
			if _, err := s.Update(ctxT(t), "tester", func(tx *Tx) error { return nil }); err == nil {
				t.Error("Update auf einer nicht migrierten fremden oder zu neuen Datei gelang")
			}
			if err := s.View(ctxT(t), func(r *ReadTx) error { return nil }); err == nil {
				t.Error("View auf einer nicht migrierten fremden oder zu neuen Datei gelang")
			}
			must(t, s.Close())

			after := fileSet(t, dir)
			if !equalSets(before, after) {
				t.Fatalf("Dateien vorher %v, nachher %v (Inhalt oder Menge verändert)", before, after)
			}
			if got := headerMode(t, path); got != "delete" {
				t.Errorf("journal_mode im Dateikopf nach Open und Migrate: %q, erwartet delete", got)
			}
		})
	}
}

// TestInspectReportsSameCasesWithoutWriting: Inspect meldet die Fälle als Befund und schreibt ebenfalls nichts.
func TestInspectReportsSameCasesWithoutWriting(t *testing.T) {
	path, dir := rollbackDB(t, "CREATE TABLE fremd (a INTEGER)", "PRAGMA user_version = 99")
	before := fileSet(t, dir)
	wantFinding(t, inspect(t, path), FindingSchemaTooNew)
	if !equalSets(before, fileSet(t, dir)) {
		t.Error("Inspect hat die Ablage verändert")
	}
	path, dir = rollbackDB(t, "CREATE TABLE fremd (a INTEGER)")
	before = fileSet(t, dir)
	rep := inspect(t, path)
	wantFinding(t, rep, FindingForeignDatabase)
	if rep.OK() {
		t.Error("fremde Datenbank ohne Befund")
	}
	if !equalSets(before, fileSet(t, dir)) {
		t.Error("Inspect hat die Ablage verändert")
	}
}

// TestOpenNewFileIsWALImmediately: eine von Open neu angelegte Datei steht sofort im WAL-Modus, vor Migrate.
func TestOpenNewFileIsWALImmediately(t *testing.T) {
	path := dbPath(t)
	s := openStore(t, path)
	if got := headerMode(t, path); got != "wal" {
		t.Fatalf("Dateikopf nach Open: %q, erwartet wal", got)
	}
	var mode string
	must(t, s.w.QueryRowContext(ctxT(t), "PRAGMA journal_mode").Scan(&mode))
	if mode != "wal" {
		t.Fatalf("journal_mode %q", mode)
	}
	// Noch nicht migriert: Update und View verweigern wegen des Schemas, nicht wegen des Modus.
	_, err := s.Update(ctxT(t), "tester", func(tx *Tx) error { return nil })
	wantCode(t, err, CodeSchemaOutdated)
	wantCode(t, s.View(ctxT(t), func(r *ReadTx) error { return nil }), CodeSchemaOutdated)
}

// TestMigrateSwitchesExistingFileToWAL: (c) eine vorhandene, eigene, noch nicht migrierte Datei im Rollback-Modus
// bleibt nach Open unverändert, Update/View verweigern (schema_outdated), Migrate stellt nach bestandener Prüfung auf
// WAL um und migriert; danach tragen alle Verbindungen beider Pools WAL.
func TestMigrateSwitchesExistingFileToWAL(t *testing.T) {
	cases := map[string]func(t *testing.T) (path, dir string){
		"Datei mit Kopf, ohne Objekte": func(t *testing.T) (string, string) {
			return rollbackDB(t, "CREATE TABLE tmp (a INTEGER)", "DROP TABLE tmp")
		},
		"leere Datei (0 Bytes)": func(t *testing.T) (string, string) {
			dir := filepath.Join(t.TempDir(), "state")
			must(t, os.MkdirAll(dir, 0o700))
			path := filepath.Join(dir, "ccmas.db")
			must(t, os.WriteFile(path, nil, 0o600))
			return path, dir
		},
	}
	for name, mk := range cases {
		t.Run(name, func(t *testing.T) {
			path, dir := mk(t)
			before := fileSet(t, dir)

			s, err := Open(ctxT(t), path, WithReadConns(3))
			must(t, err)
			t.Cleanup(func() { _ = s.Close() })
			// Open hat nichts geschrieben.
			if after := fileSet(t, dir); !equalSets(before, after) {
				t.Fatalf("Open hat die vorhandene Datei verändert: %v -> %v", names(before), names(after))
			}
			if headerMode(t, path) == "wal" {
				t.Fatal("Open hat eine vorhandene Datei auf WAL gestellt")
			}
			_, err = s.Update(ctxT(t), "tester", func(tx *Tx) error { return nil })
			wantCode(t, err, CodeSchemaOutdated)
			wantCode(t, s.View(ctxT(t), func(r *ReadTx) error { return nil }), CodeSchemaOutdated)

			must(t, s.Migrate(ctxT(t)))
			if got := headerMode(t, path); got != "wal" {
				t.Fatalf("Dateikopf nach Migrate: %q, erwartet wal", got)
			}
			var conns []*sql.Conn
			for i := 0; i < 3; i++ {
				c, err := s.r.Conn(ctxT(t))
				must(t, err)
				conns = append(conns, c)
				readPragmas(t, c, []pragmaWant{{"journal_mode", "wal"}, {"query_only", "1"}, {"foreign_keys", "1"}})
			}
			for _, c := range conns {
				must(t, c.Close())
			}
			readPragmas(t, s.w, []pragmaWant{{"journal_mode", "wal"}, {"synchronous", "2"}, {"foreign_keys", "1"}})
			mustUpdate(t, s, func(tx *Tx) error { _, err := tx.CreateProject(fxProject(t)); return err })
			mustView(t, s, func(r *ReadTx) error { _, err := r.Project(projectID); return err })
		})
	}
}

// TestJournalModeNotWALRefusedUntilMigrate: eine vollständig migrierte, aber (von außen) auf Rollback-Modus gestellte Datei
// wird von Update und View verweigert, bis Migrate sie wieder auf WAL stellt.
func TestJournalModeNotWALRefusedUntilMigrate(t *testing.T) {
	s := newStore(t)
	path := s.Path()
	must(t, s.Close())
	rawExec(t, path, "PRAGMA journal_mode = DELETE")
	if got := headerMode(t, path); got != "delete" {
		t.Fatalf("Dateikopf %q", got)
	}

	s2 := openStore(t, path)
	_, err := s2.Update(ctxT(t), "tester", func(tx *Tx) error { return nil })
	wantCode(t, err, CodeSchemaOutdated)
	wantCode(t, s2.View(ctxT(t), func(r *ReadTx) error { return nil }), CodeSchemaOutdated)
	if headerMode(t, path) != "delete" {
		t.Fatal("Update/View haben den Modus verändert")
	}
	must(t, s2.Migrate(ctxT(t)))
	if got := headerMode(t, path); got != "wal" {
		t.Fatalf("Dateikopf nach Migrate: %q", got)
	}
	mustUpdate(t, s2, func(tx *Tx) error { return nil })
}
