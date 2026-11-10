package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"modernc.org/sqlite"

	"github.com/alexwenzel/ccmas/internal/domain"
)

const (
	defaultBusyTimeout = 5 * time.Second
	defaultReadConns   = 4
)

type options struct {
	clock      func() time.Time
	readConns  int
	busy       time.Duration
	migrations []Migration
}

// Option konfiguriert Open.
type Option func(*options)

// WithClock setzt die Uhr, aus der alle Zeitstempel stammen (Standard: time.Now). Sie ist für Tests gedacht.
func WithClock(now func() time.Time) Option { return func(o *options) { o.clock = now } }

// WithReadConns setzt die Zahl der Verbindungen des Lesepools (Standard 4).
func WithReadConns(n int) Option { return func(o *options) { o.readConns = n } }

// WithBusyTimeout setzt die Wartezeit auf eine gesperrte Datenbank (Standard 5 s). Danach meldet das Paket
// CodeBusy.
func WithBusyTimeout(d time.Duration) Option { return func(o *options) { o.busy = d } }

// withMigrations ersetzt die eingebetteten Migrationen (nur Tests).
func withMigrations(m []Migration) Option { return func(o *options) { o.migrations = m } }

// Store ist die SQLite-Datenbank des Dienstes: ein Schreibpool mit genau einer Verbindung und ein getrennter
// Lesepool. Geschrieben wird nur über Update, gelesen über View; beides prüft vor jedem Zugriff, dass die
// Datenbank migriert ist. Open migriert nicht.
type Store struct {
	path       string
	w          *sql.DB
	r          *sql.DB
	clock      func() time.Time
	migrations []Migration
	latest     int
	closed     atomic.Bool
	closeOnce  sync.Once
	closeErr   error
}

var strictOnce sync.Once

// Open öffnet die Datenbank unter dem absoluten, kanonischen Pfad. Fehlt das Verzeichnis, wird es mit 0700
// angelegt; fehlt die Datei, wird sie vor dem ersten Öffnen mit 0600 angelegt (die Dateien -wal und -shm erben
// diese Rechte). Eine vorhandene Datei bleibt unverändert. Jede Verbindung trägt busy_timeout, foreign_keys,
// journal_mode=WAL und synchronous=FULL über den DSN; der Schreibpool beginnt Transaktionen mit IMMEDIATE.
func Open(ctx context.Context, path string, opts ...Option) (*Store, error) {
	o := options{clock: time.Now, readConns: defaultReadConns, busy: defaultBusyTimeout}
	for _, opt := range opts {
		opt(&o)
	}
	if o.readConns < 1 {
		return nil, errf(CodeInvalid, "mindestens eine Lese-Verbindung nötig")
	}
	if o.busy < 0 {
		return nil, errf(CodeInvalid, "negativer busy_timeout")
	}
	if o.migrations == nil {
		o.migrations = Migrations()
	}
	for i, m := range o.migrations {
		if m.Version < 1 || (i > 0 && m.Version <= o.migrations[i-1].Version) {
			return nil, errf(CodeInvalid, "Migrationsversionen müssen streng aufsteigend und ab 1 sein (Version %d)", m.Version)
		}
	}
	if err := checkDBPath(path); err != nil {
		return nil, err
	}
	// Mehrere Anweisungen in einem _pragma sind nie nötig. Der Schalter gilt prozessweit; der Dienst nutzt den
	// Treiber nur über dieses Paket.
	strictOnce.Do(func() { sqlite.StrictPragmas(true) })
	if err := prepareFile(path); err != nil {
		return nil, err
	}

	w, err := sql.Open("sqlite", writerDSN(path, o.busy))
	if err != nil {
		return nil, wrapf(CodeInternal, err, "Schreibpool")
	}
	w.SetMaxOpenConns(1)
	w.SetMaxIdleConns(1)
	// Die erste Verbindung schaltet auf WAL; erst danach öffnen die Leser.
	if err := w.PingContext(ctx); err != nil {
		_ = w.Close()
		return nil, mapErr(err)
	}
	r, err := sql.Open("sqlite", readerDSN(path, o.busy))
	if err != nil {
		_ = w.Close()
		return nil, wrapf(CodeInternal, err, "Lesepool")
	}
	r.SetMaxOpenConns(o.readConns)
	r.SetMaxIdleConns(o.readConns)
	if err := r.PingContext(ctx); err != nil {
		_ = r.Close()
		_ = w.Close()
		return nil, mapErr(err)
	}
	latest := 0
	if n := len(o.migrations); n > 0 {
		latest = o.migrations[n-1].Version
	}
	return &Store{path: path, w: w, r: r, clock: o.clock, migrations: o.migrations, latest: latest}, nil
}

// checkDBPath verlangt einen absoluten, kanonischen Pfad ohne Zeichen, die der DSN anders deuten würde.
func checkDBPath(path string) error {
	if err := domain.CheckCanonicalPath(path); err != nil {
		return invalid(err)
	}
	if strings.ContainsAny(path, "?#") || strings.HasSuffix(path, "/") {
		return invalidf("Datenbankpfad %q enthält ? oder #", path)
	}
	return nil
}

// prepareFile legt Verzeichnis (0700) und Datei (0600) an, falls sie fehlen.
func prepareFile(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return wrapf(CodeInternal, err, "Verzeichnis der Datenbank")
	}
	st, err := os.Stat(path)
	switch {
	case err == nil:
		if !st.Mode().IsRegular() {
			return invalidf("Datenbankpfad %q ist keine reguläre Datei", path)
		}
		return nil
	case !errors.Is(err, os.ErrNotExist):
		return wrapf(CodeInternal, err, "Datenbankdatei")
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil // ein anderer Prozess war schneller
		}
		return wrapf(CodeInternal, err, "Datenbankdatei anlegen")
	}
	return f.Close()
}

func pragma(q url.Values, p string) { q.Add("_pragma", p) }

// writerDSN: Schreibpool, Transaktionen mit IMMEDIATE, vollständige Dauerhaftigkeit.
func writerDSN(path string, busy time.Duration) string {
	q := url.Values{}
	pragma(q, fmt.Sprintf("busy_timeout(%d)", busy.Milliseconds()))
	pragma(q, "foreign_keys(1)")
	pragma(q, "journal_mode(WAL)")
	pragma(q, "synchronous(FULL)")
	q.Set("_txlock", "immediate")
	return path + "?" + q.Encode()
}

// readerDSN: Lesepool, nur lesend.
func readerDSN(path string, busy time.Duration) string {
	q := url.Values{}
	pragma(q, fmt.Sprintf("busy_timeout(%d)", busy.Milliseconds()))
	pragma(q, "foreign_keys(1)")
	pragma(q, "query_only(1)")
	return path + "?" + q.Encode()
}

// Path liefert den Pfad der Datenbankdatei.
func (s *Store) Path() string { return s.path }

// now liefert die aktuelle Zeit der Uhr in UTC ohne monotonen Anteil.
func (s *Store) now() time.Time { return s.clock().UTC().Round(0) }

// Close schließt beide Pools (die letzte Verbindung schreibt den WAL zurück). Mehrfaches Schließen ist erlaubt.
func (s *Store) Close() error {
	s.closeOnce.Do(func() {
		s.closed.Store(true)
		err1 := s.r.Close()
		err2 := s.w.Close()
		s.closeErr = errors.Join(err1, err2)
	})
	return s.closeErr
}

func (s *Store) checkOpen() error {
	if s.closed.Load() {
		return errf(CodeClosed, "Store ist geschlossen")
	}
	return nil
}

// querier ist, was *sql.DB, *sql.Conn und *sql.Tx gemeinsam können.
type querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// userVersion liest PRAGMA user_version.
func userVersion(ctx context.Context, q querier) (int, error) {
	var v int
	if err := q.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v); err != nil {
		return 0, mapErr(err)
	}
	return v, nil
}

// checkSchema verlangt, dass die Datenbank genau die neueste bekannte Schemaversion trägt.
func (s *Store) checkSchema(ctx context.Context, q querier) error {
	v, err := userVersion(ctx, q)
	if err != nil {
		return err
	}
	switch {
	case v > s.latest:
		return errf(CodeSchemaTooNew, "Datenbank hat Schemaversion %d, diese Software kennt höchstens %d", v, s.latest)
	case v < s.latest:
		return errf(CodeSchemaOutdated, "Datenbank hat Schemaversion %d, nötig ist %d: Migrate aufrufen", v, s.latest)
	}
	return nil
}
