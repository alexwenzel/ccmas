package persistence

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// Migration ist ein Schritt des Schemas. Version steigt streng (Lücken sind erlaubt), SHA256 ist die Prüfsumme
// des SQL-Texts (Zeilenenden normalisiert).
type Migration struct {
	Version int
	Name    string
	SQL     string
	SHA256  string
}

var migrationFile = regexp.MustCompile(`^(\d{3,})_([a-z0-9_]+)\.sql$`)

var knownMigrations = sync.OnceValues(func() ([]Migration, error) {
	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		return nil, err
	}
	var out []Migration
	for _, e := range entries {
		m := migrationFile.FindStringSubmatch(e.Name())
		if m == nil {
			return nil, fmt.Errorf("Migrationsdatei %q hat nicht die Form NNN_name.sql", e.Name())
		}
		v, _ := strconv.Atoi(m[1])
		raw, err := migrationFS.ReadFile("migrations/" + e.Name())
		if err != nil {
			return nil, err
		}
		text := strings.ReplaceAll(string(raw), "\r\n", "\n")
		sum := sha256.Sum256([]byte(text))
		out = append(out, Migration{Version: v, Name: m[2], SQL: text, SHA256: hex.EncodeToString(sum[:])})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	for i := 1; i < len(out); i++ {
		if out[i].Version == out[i-1].Version {
			return nil, fmt.Errorf("Migrationsversion %d kommt doppelt vor", out[i].Version)
		}
	}
	if len(out) == 0 || out[0].Version < 1 {
		return nil, fmt.Errorf("keine gültige Migration eingebettet")
	}
	return out, nil
})

// Migrations liefert die eingebetteten Migrationen aufsteigend. Ein Fehler im eingebetteten Bestand ist ein
// Programmierfehler und löst einen Panic aus (Test TestKnownMigrationsWellFormed).
func Migrations() []Migration {
	ms, err := knownMigrations()
	if err != nil {
		panic("persistence: " + err.Error())
	}
	out := make([]Migration, len(ms))
	copy(out, ms)
	return out
}

// AppliedMigration ist ein Eintrag der Tabelle schema_migrations.
type AppliedMigration struct {
	Version   int
	Name      string
	SHA256    string
	AppliedAt time.Time
}

const createMigrationsTable = `CREATE TABLE IF NOT EXISTS schema_migrations (
    version    INTEGER PRIMARY KEY CHECK (version >= 1),
    name       TEXT NOT NULL,
    sha256     TEXT NOT NULL CHECK (length(sha256) = 64),
    applied_at TEXT NOT NULL
) STRICT`

// dbState ist, was Migrate über eine Datenbank wissen muss.
type dbState struct {
	userVersion int
	hasTable    bool
	foreign     bool // Objekte (Tabellen) ohne schema_migrations: fremde Datenbank
	applied     []AppliedMigration
}

func readState(ctx context.Context, q querier) (dbState, error) {
	var st dbState
	v, err := userVersion(ctx, q)
	if err != nil {
		return st, err
	}
	st.userVersion = v
	var objects, hasMig int
	err = q.QueryRowContext(ctx, `SELECT count(*), coalesce(sum(type = 'table' AND name = 'schema_migrations'), 0)
		FROM sqlite_master WHERE name NOT LIKE 'sqlite_%'`).Scan(&objects, &hasMig)
	if err != nil {
		return st, mapErr(err)
	}
	st.hasTable = hasMig > 0
	st.foreign = objects > 0 && !st.hasTable
	if !st.hasTable {
		return st, nil
	}
	rows, err := q.QueryContext(ctx, "SELECT version, name, sha256, applied_at FROM schema_migrations ORDER BY version")
	if err != nil {
		return st, mapErr(err)
	}
	defer rows.Close()
	for rows.Next() {
		var a AppliedMigration
		var at string
		if err := rows.Scan(&a.Version, &a.Name, &a.SHA256, &at); err != nil {
			return st, mapErr(err)
		}
		if a.AppliedAt, err = scanTime(at); err != nil {
			return st, err
		}
		st.applied = append(st.applied, a)
	}
	return st, mapErr(rows.Err())
}

// planMigrations vergleicht die Datenbank mit der bekannten Liste und liefert die ausstehenden Migrationen.
// Regeln: höhere unbekannte Version -> schema_too_new; Tabellen ohne schema_migrations -> foreign_database; angewandte Versionen sind ein Präfix der bekannten
// (Version und Name), sonst migration_history_mismatch; geänderte Prüfsumme -> migration_checksum_mismatch;
// user_version entspricht der höchsten angewandten Version.
func planMigrations(known []Migration, st dbState) ([]Migration, error) {
	maxKnown := 0
	if len(known) > 0 {
		maxKnown = known[len(known)-1].Version
	}
	maxApplied := 0
	if n := len(st.applied); n > 0 {
		maxApplied = st.applied[n-1].Version
	}
	if st.userVersion > maxKnown || maxApplied > maxKnown {
		return nil, errf(CodeSchemaTooNew, "Datenbank hat Schemaversion %d (angewandt bis %d), diese Software kennt höchstens %d",
			st.userVersion, maxApplied, maxKnown)
	}
	if st.foreign {
		return nil, errf(CodeForeignDatabase, "die Datei enthält Tabellen, aber keine Migrationshistorie (schema_migrations): sie gehört nicht zu CCMAS")
	}
	for i, a := range st.applied {
		if i >= len(known) || a.Version != known[i].Version {
			return nil, errf(CodeHistoryMismatch, "angewandte Migration %d (%s) steht nicht an dieser Stelle der bekannten Migrationen", a.Version, a.Name)
		}
		if a.Name != known[i].Name {
			return nil, errf(CodeHistoryMismatch, "Migration %d heißt in der Datenbank %q, bekannt ist %q", a.Version, a.Name, known[i].Name)
		}
	}
	for i, a := range st.applied {
		if a.SHA256 != known[i].SHA256 {
			return nil, errf(CodeChecksumMismatch, "Migration %d (%s) wurde nach dem Anwenden geändert", a.Version, a.Name)
		}
	}
	if st.userVersion != maxApplied {
		return nil, errf(CodeHistoryMismatch, "user_version %d passt nicht zur höchsten angewandten Migration %d", st.userVersion, maxApplied)
	}
	return known[len(st.applied):], nil
}

// Migrate wendet ausstehende Migrationen an, jede in einer eigenen IMMEDIATE-Transaktion samt ihrem Eintrag in
// schema_migrations und dem Setzen von user_version. Bereits Angewandtes wird nicht erneut ausgeführt; ein
// zweiter Aufruf ändert nichts. Vor jedem Schreiben wird die Datei geprüft: eine Datenbank mit höherer
// Schemaversion (schema_too_new), eine fremde Datenbank (foreign_database), geänderte Prüfsumme oder abweichende
// Historie wird nicht angefasst, auch nicht ihr journal_mode. Erst nach bestandener Prüfung und vor dem ersten
// Schreiben stellt Migrate eine vorhandene Datei auf WAL um (eine von Open neu angelegte ist schon WAL).
func (s *Store) Migrate(ctx context.Context) error {
	if err := s.checkOpen(); err != nil {
		return err
	}
	// Erst lesen: ein Fehler hier hinterlässt keinerlei Schreibwirkung und nimmt keine Schreibsperre.
	st, err := readState(ctx, s.r)
	if err != nil {
		return err
	}
	pending, err := planMigrations(s.migrations, st)
	if err != nil {
		return err
	}
	// Die Datei gehört zu uns (Plan und Historie sind in Ordnung): jetzt, vor dem ersten Schreiben, auf WAL stellen.
	if err := setWAL(ctx, s.w); err != nil {
		return err
	}
	for len(pending) > 0 {
		if err := s.applyNext(ctx); err != nil {
			return err
		}
		if st, err = readState(ctx, s.r); err != nil {
			return err
		}
		if pending, err = planMigrations(s.migrations, st); err != nil {
			return err
		}
	}
	return nil
}

// applyNext wendet die erste ausstehende Migration in einer eigenen Transaktion an. Der Stand wird in der
// Transaktion erneut gelesen, damit gleichzeitige Migrate-Aufrufe nichts doppelt anwenden.
func (s *Store) applyNext(ctx context.Context) error {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return mapErr(err)
	}
	defer tx.Rollback()
	st, err := readState(ctx, tx)
	if err != nil {
		return err
	}
	pending, err := planMigrations(s.migrations, st)
	if err != nil {
		return err
	}
	if len(pending) == 0 {
		return nil // ein anderer Prozess war schneller
	}
	m := pending[0]
	if _, err := tx.ExecContext(ctx, createMigrationsTable); err != nil {
		return mapErr(err)
	}
	if _, err := tx.ExecContext(ctx, m.SQL); err != nil {
		return wrapf(CodeInternal, err, "Migration %d (%s)", m.Version, m.Name)
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations (version, name, sha256, applied_at) VALUES (?, ?, ?, ?)",
		m.Version, m.Name, m.SHA256, fmtTime(s.now())); err != nil {
		return mapErr(err)
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", m.Version)); err != nil {
		return mapErr(err)
	}
	return mapErr(tx.Commit())
}

// SchemaVersion liefert PRAGMA user_version der Datenbank.
func (s *Store) SchemaVersion(ctx context.Context) (int, error) {
	if err := s.checkOpen(); err != nil {
		return 0, err
	}
	return userVersion(ctx, s.r)
}

// AppliedMigrations liefert die Historie aus schema_migrations (leer, wenn nichts angewandt ist).
func (s *Store) AppliedMigrations(ctx context.Context) ([]AppliedMigration, error) {
	if err := s.checkOpen(); err != nil {
		return nil, err
	}
	st, err := readState(ctx, s.r)
	if err != nil {
		return nil, err
	}
	return st.applied, nil
}
