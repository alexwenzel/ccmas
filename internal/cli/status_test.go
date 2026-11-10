package cli

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alexwenzel/ccmas/internal/domain"
	"github.com/alexwenzel/ccmas/internal/persistence"
	"github.com/alexwenzel/ccmas/internal/persistence/persistencetest"
)

// statusEnv ist eine isolierte Umgebung für "ccmas status": ein eigener Ausführungsbereich, keine Umgebungsvariablen.
type statusEnv struct {
	t    *testing.T
	exec string // Ausführungsbereich (existiert noch nicht)
}

func newStatusEnv(t *testing.T) *statusEnv {
	t.Helper()
	t.Setenv(EnvHome, "")
	t.Setenv(EnvConfigDir, "")
	t.Setenv(EnvExecutionDir, "")
	oldExe, oldEnv := executablePath, lookupEnv
	executablePath = func() (string, error) { return filepath.Join(t.TempDir(), "bin", "ccmas"), nil }
	lookupEnv = os.Getenv
	t.Cleanup(func() { executablePath, lookupEnv = oldExe, oldEnv })
	return &statusEnv{t: t, exec: filepath.Join(t.TempDir(), "exec")}
}

func (e *statusEnv) db() string { return filepath.Join(e.exec, "state", "ccmas.db") }

// open öffnet (und legt gegebenenfalls an) die Datenbank des Bereichs über das Paket persistence.
func (e *statusEnv) open(migrate bool) *persistence.Store {
	e.t.Helper()
	s, err := persistence.Open(context.Background(), e.db())
	if err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(func() { _ = s.Close() })
	if migrate {
		if err := s.Migrate(context.Background()); err != nil {
			e.t.Fatal(err)
		}
	}
	return s
}

// seeded legt eine migrierte Datenbank mit Inhalt an und schließt sie.
func (e *statusEnv) seeded() persistencetest.Seeded {
	e.t.Helper()
	s := e.open(true)
	got := persistencetest.Seed(e.t, s)
	if err := s.Close(); err != nil {
		e.t.Fatal(err)
	}
	return got
}

func (e *statusEnv) status(args ...string) (int, string, string) {
	e.t.Helper()
	return run(e.t, append([]string{"status", "--execution-dir", e.exec}, args...)...)
}

// statusJSON führt status --json aus und liefert Exit-Code und gelesenes Objekt.
func (e *statusEnv) statusJSON() (int, statusDoc) {
	e.t.Helper()
	code, out, errOut := e.status("--json")
	var doc statusDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		e.t.Fatalf("keine gültige JSON-Ausgabe (Exit %d, stderr %q): %v\n%s", code, errOut, err, out)
	}
	return code, doc
}

// statusDoc ist die gelesene JSON-Ausgabe; die Feldnamen sind der Vertrag.
type statusDoc struct {
	CheckedAt    time.Time `json:"checked_at"`
	ExecutionDir string    `json:"execution_dir"`
	OK           bool      `json:"ok"`
	State        struct {
		Level      string             `json:"level"`
		LevelScope string             `json:"level_scope"`
		Database   persistence.Report `json:"database"`
	} `json:"state"`
	NotChecked []struct {
		Area      string `json:"area"`
		Label     string `json:"label"`
		FollowsIn string `json:"follows_in"`
		Status    string `json:"status"`
	} `json:"not_checked"`
}

func (d statusDoc) hasFinding(code string) bool { return d.State.Database.HasFinding(code) }

// tree liefert alle Einträge unter dir (relativ), Dateien mit SHA-256.
func tree(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		if d.IsDir() {
			out[rel+"/"] = "dir"
			return nil
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(raw)
		out[rel] = hex.EncodeToString(sum[:])
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func keysOf(m map[string]string) []string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

func rawExec(t *testing.T, path string, stmts ...string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
}

// ---- Ausführungsbereich -------------------------------------------------------------------------------------

func TestResolveExecutionDirPrecedence(t *testing.T) {
	e := newStatusEnv(t)
	root := t.TempDir()
	flagDir, envDir := filepath.Join(t.TempDir(), "flag"), filepath.Join(t.TempDir(), "env")

	got, err := resolveExecutionDir("", root)
	if err != nil || got != filepath.Join(root, ".ccmas") {
		t.Errorf("Standard = %q (%v)", got, err)
	}
	t.Setenv(EnvExecutionDir, envDir)
	if got, _ := resolveExecutionDir("", root); got != envDir {
		t.Errorf("Umgebung = %q, erwartet %q", got, envDir)
	}
	if got, _ := resolveExecutionDir(flagDir, root); got != flagDir {
		t.Errorf("Flag = %q, erwartet %q", got, flagDir)
	}
	// Relative Angaben werden absolut.
	if got, err := resolveExecutionDir("rel/exec", root); err != nil || !filepath.IsAbs(got) {
		t.Errorf("relativ = %q (%v)", got, err)
	}
	// Ohne Flag, Umgebung und Softwareordner gibt es keinen stillen Ersatz durch das Arbeitsverzeichnis.
	t.Setenv(EnvExecutionDir, "")
	if _, err := resolveExecutionDir("", ""); err == nil || !strings.Contains(err.Error(), "--execution-dir") {
		t.Errorf("Fehler = %v, erwartet Hinweis auf --execution-dir", err)
	}
	if got := stateDBPath(e.exec); got != filepath.Join(e.exec, "state", "ccmas.db") {
		t.Errorf("stateDBPath = %q", got)
	}
}

func TestStatusUsesEnvExecutionDir(t *testing.T) {
	e := newStatusEnv(t)
	t.Setenv(EnvExecutionDir, e.exec)
	code, out, errOut := run(t, "status", "--json")
	if code != ExitOK {
		t.Fatalf("Exit %d, stderr %q", code, errOut)
	}
	var doc statusDoc
	decode(t, out, &doc)
	if doc.ExecutionDir != e.exec || doc.State.Database.Path != e.db() {
		t.Errorf("Pfade: %q, %q", doc.ExecutionDir, doc.State.Database.Path)
	}
}

func TestStatusUsesRootDefault(t *testing.T) {
	newStatusEnv(t)
	root := t.TempDir()
	code, out, errOut := run(t, "status", "--root", root, "--json")
	if code != ExitOK {
		t.Fatalf("Exit %d, stderr %q", code, errOut)
	}
	var doc statusDoc
	decode(t, out, &doc)
	if doc.ExecutionDir != filepath.Join(root, ".ccmas") {
		t.Errorf("Ausführungsbereich = %q", doc.ExecutionDir)
	}
	if entries, _ := os.ReadDir(root); len(entries) != 0 {
		t.Errorf("status hat im Softwareordner etwas angelegt: %v", entries)
	}
}

func TestStatusUsageErrors(t *testing.T) {
	e := newStatusEnv(t)
	for name, args := range map[string][]string{
		"unbekanntes Flag":      {"status", "--gibtsnicht"},
		"unerwartetes Argument": {"status", "extra", "--execution-dir", e.exec},
		"kein Bereich":          {"status"},
	} {
		t.Run(name, func(t *testing.T) {
			code, out, errOut := run(t, args...)
			if code != ExitUsage || out != "" || strings.TrimSpace(errOut) == "" {
				t.Errorf("Exit %d, stdout %q, stderr %q", code, out, errOut)
			}
		})
	}
	if _, err := os.Stat(e.exec); !os.IsNotExist(err) {
		t.Errorf("status hat %s angelegt", e.exec)
	}
}

// ---- Nur lesend ---------------------------------------------------------------------------------------------

func TestStatusReadOnly(t *testing.T) {
	t.Run("geschlossene Datenbank", func(t *testing.T) {
		e := newStatusEnv(t)
		e.seeded()
		before := tree(t, e.exec)
		for _, args := range [][]string{{}, {"--json"}} {
			if code, _, errOut := e.status(args...); code != ExitOK {
				t.Fatalf("Exit %d, stderr %q", code, errOut)
			}
		}
		if after := tree(t, e.exec); strings.Join(keysOf(before), ",") != strings.Join(keysOf(after), ",") || before["state/ccmas.db"] != after["state/ccmas.db"] {
			t.Errorf("vorher %v, nachher %v", keysOf(before), keysOf(after))
		}
	})

	t.Run("Schema und user_version unverändert", func(t *testing.T) {
		e := newStatusEnv(t)
		e.seeded()
		read := func() (int, string) {
			db, err := sql.Open("sqlite", "file:"+e.db()+"?mode=ro")
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			var v int
			if err := db.QueryRow("PRAGMA user_version").Scan(&v); err != nil {
				t.Fatal(err)
			}
			rows, err := db.Query("SELECT version || name || sha256 || applied_at FROM schema_migrations ORDER BY version")
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			var all []string
			for rows.Next() {
				var s string
				_ = rows.Scan(&s)
				all = append(all, s)
			}
			return v, strings.Join(all, "|")
		}
		v0, m0 := read()
		if v0 == 0 || m0 == "" {
			t.Fatalf("Ausgangslage: %d %q", v0, m0)
		}
		before := tree(t, e.exec)
		if code, _, errOut := e.status(); code != ExitOK {
			t.Fatalf("Exit %d, stderr %q", code, errOut)
		}
		if v1, m1 := read(); v1 != v0 || m1 != m0 {
			t.Errorf("Schema verändert: %d/%q -> %d/%q", v0, m0, v1, m1)
		}
		_ = before
	})

	t.Run("offener Schreiber", func(t *testing.T) {
		e := newStatusEnv(t)
		s := e.open(true)
		persistencetest.Seed(t, s)
		before := tree(t, e.exec)
		if _, ok := before["state/ccmas.db-wal"]; !ok {
			t.Fatalf("Ausgangslage ohne -wal: %v", keysOf(before))
		}
		code, doc := e.statusJSON()
		if code != ExitOK || !doc.OK {
			t.Fatalf("Exit %d, ok=%v, Befunde %+v", code, doc.OK, doc.State.Database.Findings)
		}
		after := tree(t, e.exec)
		if strings.Join(keysOf(before), ",") != strings.Join(keysOf(after), ",") {
			t.Errorf("Dateien vorher %v, nachher %v", keysOf(before), keysOf(after))
		}
		for _, f := range []string{"state/ccmas.db", "state/ccmas.db-wal"} {
			if before[f] != after[f] {
				t.Errorf("%s hat sich geändert", f)
			}
		}
	})

	t.Run("fehlende Datenbank", func(t *testing.T) {
		e := newStatusEnv(t)
		for _, args := range [][]string{{}, {"--json"}} {
			if code, _, errOut := e.status(args...); code != ExitOK {
				t.Fatalf("Exit %d, stderr %q", code, errOut)
			}
		}
		if _, err := os.Stat(e.exec); !os.IsNotExist(err) {
			t.Errorf("status hat den Ausführungsbereich angelegt (%v)", err)
		}
		// Auch wenn der Bereich schon besteht: weder state/ noch die Datei entstehen.
		must0(t, os.MkdirAll(e.exec, 0o700))
		e.status()
		if got := tree(t, e.exec); len(got) != 1 {
			t.Errorf("status hat etwas angelegt: %v", keysOf(got))
		}
	})
}

func must0(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// ---- Normalfälle --------------------------------------------------------------------------------------------

func TestStatusMissingDB(t *testing.T) {
	e := newStatusEnv(t)
	code, out, errOut := e.status()
	if code != ExitOK || errOut != "" {
		t.Fatalf("Exit %d, stderr %q", code, errOut)
	}
	for _, want := range []string{"Zustand (SQLite)", "nicht vorhanden", "nicht geprüft"} {
		if !strings.Contains(out, want) {
			t.Errorf("Ausgabe enthält %q nicht:\n%s", want, out)
		}
	}
	code, doc := e.statusJSON()
	if code != ExitOK || !doc.OK || doc.State.Database.Exists || doc.State.Level != "not_present" || len(doc.State.Database.Findings) != 0 {
		t.Errorf("Exit %d, %+v", code, doc)
	}
}

func TestStatusHealthyDB(t *testing.T) {
	e := newStatusEnv(t)
	seeded := e.seeded()
	code, doc := e.statusJSON()
	if code != ExitOK || !doc.OK {
		t.Fatalf("Exit %d, Befunde %+v", code, doc.State.Database.Findings)
	}
	db := doc.State.Database
	if !db.Exists || db.Data == nil || len(db.Data.Projects) != 1 {
		t.Fatalf("Bericht: %+v", db)
	}
	p := db.Data.Projects[0]
	if p.ProjectID != seeded.ProjectID || p.StateRevision != seeded.Revision || seeded.Revision == 0 {
		t.Errorf("Projekt %q Revision %d, erwartet %q %d", p.ProjectID, p.StateRevision, seeded.ProjectID, seeded.Revision)
	}
	for st, n := range seeded.WorkOrders {
		if p.WorkOrders[string(st)] != n {
			t.Errorf("Aufträge %s: %d, erwartet %d", st, p.WorkOrders[string(st)], n)
		}
	}
	if p.WorkOrders[string(domain.WOCompleted)] != 0 {
		t.Errorf("abgeschlossene Aufträge: %d", p.WorkOrders[string(domain.WOCompleted)])
	}
	if p.ActiveExecutions != seeded.ActiveExecutions || p.OpenQuestions != seeded.OpenQuestions {
		t.Errorf("aktiv %d, offene Fragen %d", p.ActiveExecutions, p.OpenQuestions)
	}
	if db.Data.Events != int64(seeded.Events) || db.Data.LastEventAt == nil {
		t.Errorf("Ereignisse %d (erwartet %d), letztes %v", db.Data.Events, seeded.Events, db.Data.LastEventAt)
	}
	if db.Schema.UserVersion != 1 || len(db.Schema.Pending) != 0 || db.Integrity.QuickCheck != "ok" {
		t.Errorf("Schema %+v, Integrität %+v", db.Schema, db.Integrity)
	}
	if doc.State.Level != "functional_read" || doc.State.LevelScope == "" {
		t.Errorf("Stufe %q (%q)", doc.State.Level, doc.State.LevelScope)
	}

	code, out, errOut := e.status()
	if code != ExitOK || errOut != "" {
		t.Fatalf("Exit %d, stderr %q", code, errOut)
	}
	for _, want := range []string{
		"Zustand (SQLite)", e.db(), "ccmas", "REVISION", "in_progress 1", "blocked 1", "OFFENE FRAGEN",
		"OUTBOX", "Letztes Audit-Ereignis", "ohne Befund",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("Ausgabe enthält %q nicht:\n%s", want, out)
		}
	}
}

// TestStatusNamesWhatIsNotChecked: Die Ausgabe tut nicht so, als prüfe sie mehr als den SQLite-Zustand.
func TestStatusNamesWhatIsNotChecked(t *testing.T) {
	e := newStatusEnv(t)
	e.seeded()
	_, doc := e.statusJSON()
	want := map[string]string{"service": "CC-51", "tools": "M06", "authentication": "M06", "project_assignment": "M06"}
	if len(doc.NotChecked) != len(want) {
		t.Fatalf("nicht geprüft: %+v", doc.NotChecked)
	}
	for _, n := range doc.NotChecked {
		if want[n.Area] != n.FollowsIn || n.Status != "not_checked" || n.Label == "" {
			t.Errorf("Eintrag %+v", n)
		}
	}
	_, out, _ := e.status()
	for _, w := range []string{"Dienstzustand", "CC-51", "Werkzeuge", "Anmeldung", "Projektzuordnung", "M06"} {
		if !strings.Contains(out, w) {
			t.Errorf("Ausgabe nennt %q nicht:\n%s", w, out)
		}
	}
	if !strings.Contains(out, "nicht geprüft") {
		t.Errorf("Ausgabe sagt nicht „nicht geprüft“:\n%s", out)
	}
}

func TestStatusPendingMigrations(t *testing.T) {
	e := newStatusEnv(t)
	e.open(false) // angelegt, nicht migriert
	must0(t, closeStore(e))
	before := tree(t, e.exec)
	code, doc := e.statusJSON()
	if code != ExitOK || !doc.OK {
		t.Fatalf("Exit %d, Befunde %+v", code, doc.State.Database.Findings)
	}
	db := doc.State.Database
	if len(db.Schema.Pending) == 0 || db.Schema.UserVersion != 0 || db.Data != nil || doc.State.Level != "present" {
		t.Errorf("Schema %+v, Stufe %q", db.Schema, doc.State.Level)
	}
	_, out, _ := e.status()
	if !strings.Contains(out, "ausstehend") || !strings.Contains(out, "kein Befund") {
		t.Errorf("Hinweis auf ausstehende Migrationen fehlt:\n%s", out)
	}
	if after := tree(t, e.exec); strings.Join(keysOf(before), ",") != strings.Join(keysOf(after), ",") || before["state/ccmas.db"] != after["state/ccmas.db"] {
		t.Errorf("vorher %v, nachher %v", keysOf(before), keysOf(after))
	}
}

func closeStore(e *statusEnv) error {
	s, err := persistence.Open(context.Background(), e.db())
	if err != nil {
		return err
	}
	return s.Close()
}

// ---- Befunde (Exit 1) ---------------------------------------------------------------------------------------

type findingCase struct {
	prepare func(t *testing.T, e *statusEnv)
	code    string // erwarteter Befundcode
	level   string // erwartete Stufe des Zustands
}

var (
	caseTooNew = findingCase{func(t *testing.T, e *statusEnv) {
		e.seeded()
		rawExec(t, e.db(), "PRAGMA user_version = 99")
	}, "schema_too_new", "present"}
	caseChecksum = findingCase{func(t *testing.T, e *statusEnv) {
		e.seeded()
		rawExec(t, e.db(), "UPDATE schema_migrations SET sha256 = '"+strings.Repeat("0", 64)+"'")
	}, "migration_checksum_mismatch", "present"}
	caseHistory = findingCase{func(t *testing.T, e *statusEnv) {
		e.seeded()
		rawExec(t, e.db(), "UPDATE schema_migrations SET name = 'andere'")
	}, "migration_history_mismatch", "present"}
	caseForeignDB = findingCase{func(t *testing.T, e *statusEnv) {
		must0(t, os.MkdirAll(filepath.Dir(e.db()), 0o700))
		rawExec(t, e.db(), "CREATE TABLE fremd (a INTEGER)")
	}, "foreign_database", "present"}
	// Die Fremdschlüssel sind in der rohen Verbindung aus: das Ziel der Aufträge verschwindet.
	caseForeignKey = findingCase{func(t *testing.T, e *statusEnv) {
		e.seeded()
		rawExec(t, e.db(), "PRAGMA foreign_keys = OFF", "DELETE FROM goals")
	}, "foreign_key_violations", "configured"}
	caseCorrupt = findingCase{func(t *testing.T, e *statusEnv) {
		must0(t, os.MkdirAll(filepath.Dir(e.db()), 0o700))
		must0(t, os.WriteFile(e.db(), []byte(strings.Repeat("kein sqlite ", 300)), 0o600))
	}, "unreadable", "present"}
)

// checkFinding stellt den Zustand her und verlangt Exit 1 mit dem Befundcode im JSON und im Text, ohne dass status
// etwas verändert.
func checkFinding(t *testing.T, c findingCase) {
	t.Helper()
	e := newStatusEnv(t)
	c.prepare(t, e)
	before := tree(t, e.exec)
	code, doc := e.statusJSON()
	if code != ExitError || doc.OK || !doc.hasFinding(c.code) {
		t.Fatalf("Exit %d, ok=%v, erwartet Befund %s: %+v", code, doc.OK, c.code, doc.State.Database.Findings)
	}
	if doc.State.Level != c.level {
		t.Errorf("Stufe %q, erwartet %q", doc.State.Level, c.level)
	}
	for _, f := range doc.State.Database.Findings {
		if f.Message == "" {
			t.Errorf("Befund ohne Diagnose: %+v", f)
		}
	}
	code, out, errOut := e.status()
	if code != ExitError || !strings.Contains(out, c.code) || !strings.Contains(errOut, "Befund") {
		t.Errorf("Text: Exit %d, stdout:\n%s\nstderr %q", code, out, errOut)
	}
	after := tree(t, e.exec)
	if strings.Join(keysOf(before), ",") != strings.Join(keysOf(after), ",") || before["state/ccmas.db"] != after["state/ccmas.db"] {
		t.Errorf("status hat die Ablage verändert: %v -> %v", keysOf(before), keysOf(after))
	}
}

func TestStatusTooNew(t *testing.T)              { checkFinding(t, caseTooNew) }
func TestStatusChecksumMismatch(t *testing.T)    { checkFinding(t, caseChecksum) }
func TestStatusHistoryMismatch(t *testing.T)     { checkFinding(t, caseHistory) }
func TestStatusForeignDatabase(t *testing.T)     { checkFinding(t, caseForeignDB) }
func TestStatusCorruptFile(t *testing.T)         { checkFinding(t, caseCorrupt) }
func TestStatusForeignKeyViolation(t *testing.T) { checkFinding(t, caseForeignKey) }

// ---- Nebenläufigkeit ----------------------------------------------------------------------------------------

// TestStatusWhileWriterOpen: ein offener Schreib-Store mit laufenden Transaktionen. status liefert einen
// festgeschriebenen, in sich stimmigen Stand ohne Fehler, und der Schreiber wird nicht blockiert.
func TestStatusWhileWriterOpen(t *testing.T) {
	e := newStatusEnv(t)
	s := e.open(true)
	seeded := persistencetest.Seed(t, s)

	stop := make(chan struct{})
	var wg sync.WaitGroup
	var commits int
	var werr error
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			// Jede Transaktion: eine Frage mehr (Planungszustand, Revision +1).
			_, err := s.Update(context.Background(), "test", func(tx *persistence.Tx) error {
				return tx.CreateQuestion(seeded.ProjectID, domain.Question{
					QuestionID:  "q-live-" + string(rune('a'+i%26)) + string(rune('a'+(i/26)%26)) + string(rune('a'+(i/676)%26)),
					WorkOrderID: "wo-2", ExecutionID: "exec-1", Attempt: 1, Text: "?", Context: "c", State: domain.QuestionOpen, CreatedAt: time.Now(),
				})
			})
			if err != nil {
				werr = err
				return
			}
			commits++
		}
	}()
	for i := 0; i < 15; i++ {
		code, doc := e.statusJSON()
		if code != ExitOK || !doc.OK {
			t.Fatalf("Lauf %d: Exit %d, Befunde %+v", i, code, doc.State.Database.Findings)
		}
		p := doc.State.Database.Data.Projects[0]
		// Konsistenz: je Transaktion eine Frage mehr und Revision +1.
		if got, want := int64(p.OpenQuestions), p.StateRevision-seeded.Revision+int64(seeded.OpenQuestions); got != want {
			t.Fatalf("Lauf %d: %d offene Fragen bei Revision %d (erwartet %d): kein einheitlicher Stand", i, got, p.StateRevision, want)
		}
	}
	close(stop)
	wg.Wait()
	if werr != nil {
		t.Fatalf("der Schreiber scheiterte: %v", werr)
	}
	if commits < 5 {
		t.Errorf("nur %d Transaktionen während der Läufe", commits)
	}
}

// ---- Ausgabe ------------------------------------------------------------------------------------------------

func TestStatusJSONFields(t *testing.T) {
	e := newStatusEnv(t)
	e.seeded()
	_, out, _ := e.status("--json")
	var m map[string]any
	decode(t, out, &m)
	keys := func(v any) string {
		o, ok := v.(map[string]any)
		if !ok {
			t.Fatalf("kein Objekt: %v", v)
		}
		var ks []string
		for k := range o {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		return strings.Join(ks, ",")
	}
	state := m["state"].(map[string]any)
	db := state["database"].(map[string]any)
	for _, c := range []struct{ got, want string }{
		{keys(m), "checked_at,execution_dir,not_checked,ok,state"},
		{keys(state), "database,level,level_scope"},
		{keys(db), "data,exists,findings,integrity,journal_mode,notes,path,permissions,read_mode,schema,size_bytes"},
		{keys(m["not_checked"].([]any)[0]), "area,follows_in,label,status"},
		{keys(db["data"]), "events,last_event_at,projects"},
		{keys(db["data"].(map[string]any)["projects"].([]any)[0]), "active_executions,open_questions,outbox,project_id,state_revision,work_orders"},
		{keys(db["schema"]), "applied,highest_applied,highest_known,pending,user_version"},
		{keys(db["integrity"]), "checked,foreign_key_violations,quick_check,quick_check_messages"},
		{keys(db["permissions"]), "database,directory,shm,wal"},
	} {
		if c.got != c.want {
			t.Errorf("Felder %q, erwartet %q", c.got, c.want)
		}
	}
	// Fehlende Datenbank: dieselben Felder, Listen leer.
	e2 := newStatusEnv(t)
	_, out, _ = e2.status("--json")
	for _, want := range []string{`"findings": []`, `"notes": [`, `"data": null`, `"level": "not_present"`, `"ok": true`} {
		if !strings.Contains(out, want) {
			t.Errorf("JSON enthält %s nicht:\n%s", want, out)
		}
	}
}

func TestStatusICloudHint(t *testing.T) {
	newStatusEnv(t)
	exec := filepath.Join(t.TempDir(), "Mobile Documents", "ccmas")
	code, out, _ := run(t, "status", "--execution-dir", exec)
	if code != ExitOK || !strings.Contains(out, "iCloud") {
		t.Errorf("Exit %d, Ausgabe:\n%s", code, out)
	}
}

func TestStatusHelp(t *testing.T) {
	code, out, _ := run(t, "status", "--help")
	if code != ExitOK {
		t.Fatalf("Exit %d", code)
	}
	for _, want := range []string{"--execution-dir", "--root", "--json", EnvExecutionDir, "Exit-Codes", "nicht geprüft"} {
		if !strings.Contains(out, want) {
			t.Errorf("Hilfe nennt %q nicht:\n%s", want, out)
		}
	}
	if strings.Contains(out, "noch nicht verfügbar") {
		t.Errorf("status gilt noch als geplant:\n%s", out)
	}
}
