package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alexwenzel/ccmas/internal/domain"
	"github.com/alexwenzel/ccmas/internal/supervisor"
)

// ---- Hilfen -------------------------------------------------------------------------------------------------

// fileSet ist der Inhalt eines Verzeichnisses: Name -> SHA-256 (Dateien) beziehungsweise "dir".
func fileSet(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() {
			out[e.Name()] = "dir"
			continue
		}
		out[e.Name()] = fileSHA(t, filepath.Join(dir, e.Name()))
	}
	return out
}

func names(m map[string]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func inspect(t *testing.T, path string) Report {
	t.Helper()
	rep, err := Inspect(ctxT(t), path)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	return rep
}

// wantFinding verlangt genau diesen Befund (neben eventuellen weiteren).
func wantFinding(t *testing.T, rep Report, code string) {
	t.Helper()
	if !rep.HasFinding(code) {
		t.Fatalf("Befund %q fehlt; Befunde: %+v", code, rep.Findings)
	}
}

func wantNoFindings(t *testing.T, rep Report) {
	t.Helper()
	if !rep.OK() {
		t.Fatalf("unerwartete Befunde: %+v", rep.Findings)
	}
}

// rawExec führt SQL auf der Datei aus, an der Datenbank vorbei: ohne die Pragmas und Prüfungen des Pakets
// (zum Herstellen kaputter Zustände). Fremdschlüssel sind aus.
func rawExec(t *testing.T, path string, stmts ...string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	for _, s := range stmts {
		if _, err := db.ExecContext(ctxT(t), s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
}

// builtDB baut mit buildWorld eine reiche, geschlossene Datenbank und liefert Pfad und gelesenen Zustand.
func builtDB(t *testing.T) (string, world) {
	t.Helper()
	s := newStore(t)
	buildWorld(t, s)
	w := readWorld(t, s)
	must(t, s.Close())
	return s.Path(), w
}

// ---- Fehlende, leere und migrierte Datenbank ----------------------------------------------------------------

func TestInspectMissingDB(t *testing.T) {
	base := t.TempDir()
	path := filepath.Join(base, "neu", "state", "ccmas.db")
	rep := inspect(t, path)
	if rep.Exists || !rep.OK() || rep.Data != nil || rep.ReadMode != "" {
		t.Fatalf("Bericht einer fehlenden Datenbank: %+v", rep)
	}
	if len(rep.Notes) == 0 || !strings.Contains(rep.Notes[0], "nicht vorhanden") {
		t.Errorf("Hinweis fehlt: %v", rep.Notes)
	}
	// Weder Datei noch Verzeichnis entstehen.
	if _, err := os.Stat(filepath.Join(base, "neu")); !os.IsNotExist(err) {
		t.Errorf("Inspect hat ein Verzeichnis angelegt (%v)", err)
	}
	if entries, _ := os.ReadDir(base); len(entries) != 0 {
		t.Errorf("Inspect hat etwas in %s angelegt: %v", base, entries)
	}
}

func TestInspectRejectsBadPath(t *testing.T) {
	for _, p := range []string{"", "relativ/ccmas.db", "/a/../b.db", "/a/b.db?mode=rw", "/a//b.db"} {
		_, err := Inspect(ctxT(t), p)
		wantCode(t, err, CodeInvalid)
	}
}

func TestInspectCanceledContext(t *testing.T) {
	path, _ := builtDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Inspect(ctx, path); err == nil {
		t.Fatal("kein Fehler bei abgebrochenem Kontext")
	}
}

// TestInspectCreatedNotMigrated: Open ohne Migrate. Ausstehende Migrationen sind ein Hinweis, kein Befund.
func TestInspectCreatedNotMigrated(t *testing.T) {
	path := dbPath(t)
	must(t, openStore(t, path).Close())
	before := fileSet(t, filepath.Dir(path))
	rep := inspect(t, path)
	wantNoFindings(t, rep)
	if !rep.Exists || rep.Data != nil {
		t.Fatalf("Bericht: %+v", rep)
	}
	if rep.Schema.UserVersion != 0 || len(rep.Schema.Applied) != 0 || len(rep.Schema.Pending) != len(Migrations()) {
		t.Errorf("Schema: %+v", rep.Schema)
	}
	if rep.Schema.Pending[0].Version != 1 || rep.Schema.Pending[0].Name != "initial" {
		t.Errorf("ausstehend: %+v", rep.Schema.Pending)
	}
	if len(rep.Notes) == 0 || !strings.Contains(rep.Notes[0], "ausstehende Migration") {
		t.Errorf("Hinweis fehlt: %v", rep.Notes)
	}
	if after := fileSet(t, filepath.Dir(path)); !equalSets(before, after) {
		t.Errorf("Dateien vorher %v, nachher %v", before, after)
	}
}

func equalSets(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// TestInspectEmptyFile: eine leere Datei ist eine leere Datenbank; sie bleibt leer, und es entsteht nichts.
func TestInspectEmptyFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	must(t, os.MkdirAll(dir, 0o700))
	path := filepath.Join(dir, "ccmas.db")
	must(t, os.WriteFile(path, nil, 0o600))
	rep := inspect(t, path)
	wantNoFindings(t, rep)
	if rep.SizeBytes != 0 || rep.ReadMode != ReadImmutable || len(rep.Schema.Pending) == 0 {
		t.Errorf("Bericht: %+v", rep)
	}
	if got := fileSet(t, dir); len(got) != 1 {
		t.Errorf("Dateien nach Inspect: %v", names(got))
	}
	if st, _ := os.Stat(path); st.Size() != 0 {
		t.Errorf("Datei wurde verändert: %d Bytes", st.Size())
	}
}

// TestInspectClosedDB: eine geschlossene WAL-Datenbank ohne -wal/-shm wird gelesen, ohne dass Dateien entstehen, und
// der Bericht stimmt mit dem überein, was die Lese-API des Stores liefert.
func TestInspectClosedDB(t *testing.T) {
	path, w := builtDB(t)
	dir := filepath.Dir(path)
	before := fileSet(t, dir)
	if names(before)[0] != "ccmas.db" || len(before) != 1 {
		t.Fatalf("Ausgangslage: %v", names(before))
	}
	rep := inspect(t, path)
	wantNoFindings(t, rep)
	if rep.ReadMode != ReadImmutable || rep.JournalMode != "wal" {
		t.Errorf("Leseweg %q, Journal %q", rep.ReadMode, rep.JournalMode)
	}
	if after := fileSet(t, dir); !equalSets(before, after) {
		t.Fatalf("Dateien vorher %v, nachher %v", before, after)
	}
	checkWorldReport(t, rep, w)
	latest := Migrations()[len(Migrations())-1].Version
	if rep.Schema.UserVersion != latest || rep.Schema.HighestApplied != latest || rep.Schema.HighestKnown != latest ||
		len(rep.Schema.Applied) != len(Migrations()) || rep.Schema.Applied[0].Name != "initial" || rep.Schema.Applied[0].AppliedAt == nil || len(rep.Schema.Pending) != 0 {
		t.Errorf("Schema: %+v", rep.Schema)
	}
	if rep.SizeBytes <= 0 || !rep.Permissions.Database.Exists || rep.Permissions.WAL.Exists || rep.Permissions.SHM.Exists {
		t.Errorf("Größe/Dateien: %d %+v", rep.SizeBytes, rep.Permissions)
	}
	if !rep.Integrity.Checked || rep.Integrity.QuickCheck != "ok" || rep.Integrity.ForeignKeyViolations != 0 {
		t.Errorf("Integrität: %+v", rep.Integrity)
	}
}

// checkWorldReport vergleicht die Grunddaten des Berichts mit dem, was die Lese-API liefert.
func checkWorldReport(t *testing.T, rep Report, w world) {
	t.Helper()
	if rep.Data == nil || len(rep.Data.Projects) != 1 {
		t.Fatalf("Grunddaten: %+v", rep.Data)
	}
	p := rep.Data.Projects[0]
	if p.ProjectID != projectID || p.StateRevision != w.Revision || w.Revision == 0 {
		t.Errorf("Projekt %q Revision %d, erwartet %q %d", p.ProjectID, p.StateRevision, projectID, w.Revision)
	}
	want := map[string]int{}
	for _, s := range domain.WorkOrderStates() {
		want[string(s)] = 0
	}
	for _, o := range w.Orders {
		want[string(o.State)]++
	}
	if len(w.Orders) < 2 {
		t.Fatalf("zu wenige Aufträge für einen aussagekräftigen Test: %d", len(w.Orders))
	}
	for k, v := range want {
		if p.WorkOrders[k] != v {
			t.Errorf("Aufträge im Zustand %s: %d, erwartet %d", k, p.WorkOrders[k], v)
		}
	}
	if len(p.WorkOrders) != len(want) {
		t.Errorf("Zustände der Aufträge: %v", p.WorkOrders)
	}
	if p.ActiveExecutions != len(w.Active) || p.OpenQuestions != len(w.Open) {
		t.Errorf("aktive Ausführungen %d (erwartet %d), offene Fragen %d (erwartet %d)", p.ActiveExecutions, len(w.Active), p.OpenQuestions, len(w.Open))
	}
	if len(w.Open) == 0 || len(w.Active) == 0 {
		t.Errorf("der Testzustand enthält keine aktive Ausführung oder offene Frage")
	}
	if len(p.Outbox) != len(domain.OutboxStates()) {
		t.Errorf("Outbox-Zustände: %v", p.Outbox)
	}
	if rep.Data.Events != int64(len(w.Events)) || rep.Data.LastEventAt == nil || !rep.Data.LastEventAt.Equal(w.Events[len(w.Events)-1].OccurredAt) {
		t.Errorf("Ereignisse %d / %v, erwartet %d / %v", rep.Data.Events, rep.Data.LastEventAt, len(w.Events), w.Events[len(w.Events)-1].OccurredAt)
	}
}

// TestInspectOutboxCounts: die Outbox-Anzahlen je Zustand kommen aus der Tabelle (die Logik folgt in CC-45).
func TestInspectOutboxCounts(t *testing.T) {
	s := newStore(t)
	seedBase(t, s)
	for i, st := range []string{"pending", "pending", "needs_attention", "succeeded"} {
		must(t, execSQL(t, s, `INSERT INTO outbox (action_id, project_id, operation_key, kind, payload_json, state, created_at, updated_at)
			VALUES (?, 'ccmas', ?, 'asana.comment', '{}', ?, ?, ?)`, "a"+string(rune('0'+i)), "k"+string(rune('0'+i)), st, ts, ts))
	}
	must(t, s.Close())
	rep := inspect(t, s.Path())
	got := rep.Data.Projects[0].Outbox
	if got["pending"] != 2 || got["needs_attention"] != 1 || got["succeeded"] != 1 || got["claimed"] != 0 {
		t.Errorf("Outbox: %v", got)
	}
}

// ---- Schreiber offen ----------------------------------------------------------------------------------------

// TestInspectWhileWriterOpen: Während ein Schreib-Store offen ist (Daten nur im -wal), bleiben Hauptdatei und -wal
// byte-identisch, es entsteht keine Datei, und der Bericht zeigt den festgeschriebenen Stand.
func TestInspectWhileWriterOpen(t *testing.T) {
	s := newStore(t)
	buildWorld(t, s)
	w := readWorld(t, s)
	dir := filepath.Dir(s.Path())
	before := fileSet(t, dir)
	for _, f := range []string{"ccmas.db", "ccmas.db-wal", "ccmas.db-shm"} {
		if _, ok := before[f]; !ok {
			t.Fatalf("Ausgangslage ohne %s: %v", f, names(before))
		}
	}
	rep := inspect(t, s.Path())
	wantNoFindings(t, rep)
	if rep.ReadMode != ReadReadOnly || rep.JournalMode != "wal" {
		t.Errorf("Leseweg %q, Journal %q", rep.ReadMode, rep.JournalMode)
	}
	checkWorldReport(t, rep, w)
	after := fileSet(t, dir)
	if len(before) != len(after) {
		t.Fatalf("Dateien vorher %v, nachher %v", names(before), names(after))
	}
	for _, f := range []string{"ccmas.db", "ccmas.db-wal"} {
		if before[f] != after[f] {
			t.Errorf("%s hat sich geändert", f)
		}
	}
	if !rep.Permissions.WAL.Exists || !rep.Permissions.SHM.Exists {
		t.Errorf("-wal/-shm nicht erfasst: %+v", rep.Permissions)
	}
	// Der Schreiber arbeitet danach unverändert weiter.
	mustUpdate(t, s, func(tx *Tx) error {
		_, err := tx.CreateWorkOrder(WorkOrderRecord{WorkOrder: fxWorkOrder(t, "wo-after"), GoalID: "ms-1", Kind: supervisor.OrderBug})
		return err
	})
}

// TestInspectIgnoresUncommitted: Eine offene Schreibtransaktion ist weder sichtbar, noch blockiert sie die Prüfung.
func TestInspectIgnoresUncommitted(t *testing.T) {
	s := newStore(t)
	seedBase(t, s)
	r0 := revisionOf(t, s, projectID)
	entered, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := s.Update(context.Background(), "tester", func(tx *Tx) error {
			if _, err := tx.CreateWorkOrder(WorkOrderRecord{WorkOrder: fxWorkOrder(t, "wo-pending"), GoalID: "ms-1", Kind: supervisor.OrderBug}); err != nil {
				return err
			}
			close(entered)
			<-release
			return nil
		})
		done <- err
	}()
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	start := time.Now()
	rep, err := Inspect(ctx, s.Path())
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("Inspect hat auf die Schreibtransaktion gewartet (%v)", d)
	}
	wantNoFindings(t, rep)
	p := rep.Data.Projects[0]
	if p.StateRevision != r0 || sumCounts(p.WorkOrders) != 1 {
		t.Errorf("Revision %d (erwartet %d), Aufträge %v: die offene Transaktion ist sichtbar", p.StateRevision, r0, p.WorkOrders)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("Schreiber: %v", err)
	}
	rep = inspect(t, s.Path())
	if p := rep.Data.Projects[0]; p.StateRevision != r0+1 || sumCounts(p.WorkOrders) != 2 {
		t.Errorf("nach dem Festschreiben: Revision %d, Aufträge %v", p.StateRevision, p.WorkOrders)
	}
}

func sumCounts(m map[string]int) int {
	n := 0
	for _, v := range m {
		n += v
	}
	return n
}

// TestInspectConsistentSnapshotUnderWrites: Während ein Schreiber laufend festschreibt (jede Transaktion: ein Auftrag
// mehr, Revision +1), ist jeder Bericht in sich stimmig (Tabellen aus demselben Stand), der Schreiber scheitert
// nicht, und es entsteht keine Datei.
func TestInspectConsistentSnapshotUnderWrites(t *testing.T) {
	s := newStore(t)
	seedBase(t, s)
	dir := filepath.Dir(s.Path())
	base := inspect(t, s.Path()).Data.Projects[0]
	delta := sumCounts(base.WorkOrders) - int(base.StateRevision)
	filesBefore := names(fileSet(t, dir))

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
			id := "wo-w" + strings.Repeat("x", i%7) + string(rune('a'+i%26)) + string(rune('a'+(i/26)%26)) + string(rune('a'+(i/676)%26))
			_, err := s.Update(context.Background(), "tester", func(tx *Tx) error {
				_, err := tx.CreateWorkOrder(WorkOrderRecord{WorkOrder: fxWorkOrder(t, id), GoalID: "ms-1", Kind: supervisor.OrderBug})
				return err
			})
			if err != nil {
				werr = err
				return
			}
			commits++
		}
	}()
	seen := map[int64]bool{}
	for i := 0; i < 25; i++ {
		rep := inspect(t, s.Path())
		wantNoFindings(t, rep)
		p := rep.Data.Projects[0]
		if got := sumCounts(p.WorkOrders) - int(p.StateRevision); got != delta {
			t.Fatalf("Bericht %d ist nicht aus einem Stand: Revision %d, Aufträge %d", i, p.StateRevision, sumCounts(p.WorkOrders))
		}
		seen[p.StateRevision] = true
	}
	close(stop)
	wg.Wait()
	if werr != nil {
		t.Fatalf("der Schreiber scheiterte während der Prüfungen: %v", werr)
	}
	if commits < 5 {
		t.Errorf("nur %d Transaktionen während der Prüfungen: der Test prüft die Nebenläufigkeit nicht", commits)
	}
	if len(seen) < 2 {
		t.Logf("Hinweis: alle Berichte zeigten dieselbe Revision (%v)", seen)
	}
	if after := names(fileSet(t, dir)); strings.Join(after, ",") != strings.Join(filesBefore, ",") {
		t.Errorf("Dateien vorher %v, nachher %v", filesBefore, after)
	}
}

// TestInspectColdWAL: Ein -wal ohne -shm (Reste ohne offenen Schreiber, hier eine Kopie von Datei und -wal) wird über
// eine private Kopie gelesen; im Verzeichnis der Datenbank entsteht kein -shm, und die Kopie ist danach gelöscht.
func TestInspectColdWAL(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	s := newStore(t)
	buildWorld(t, s)
	w := readWorld(t, s)
	cold := filepath.Join(t.TempDir(), "state")
	must(t, os.MkdirAll(cold, 0o700))
	for _, suf := range []string{"", "-wal"} {
		raw, err := os.ReadFile(s.Path() + suf)
		must(t, err)
		must(t, os.WriteFile(filepath.Join(cold, "ccmas.db"+suf), raw, 0o600))
	}
	path := filepath.Join(cold, "ccmas.db")
	if raw, _ := os.ReadFile(path); len(raw) > 8192 {
		t.Fatalf("Hauptdatei hat schon alles: das Szenario trägt nicht (%d Bytes)", len(raw))
	}
	before := fileSet(t, cold)

	rep := inspect(t, path)
	wantNoFindings(t, rep)
	if rep.ReadMode != ReadSnapshotCopy || rep.JournalMode != "wal" {
		t.Errorf("Leseweg %q, Journal %q", rep.ReadMode, rep.JournalMode)
	}
	checkWorldReport(t, rep, w)
	if after := fileSet(t, cold); !equalSets(before, after) {
		t.Fatalf("Dateien vorher %v, nachher %v", names(before), names(after))
	}
	if left, _ := os.ReadDir(tmp); len(left) != 0 {
		t.Errorf("die Prüfkopie liegt noch im Temp-Verzeichnis: %v", left)
	}
}

// TestInspectReadOnlyDirectoryOfOpenWriter ist in inspect_unix_test.go (Verzeichnisrechte).

// ---- Befunde ------------------------------------------------------------------------------------------------

func TestInspectSchemaTooNew(t *testing.T) {
	path, _ := builtDB(t)
	rawExec(t, path, "PRAGMA user_version = 99")
	before := fileSHA(t, path)
	rep := inspect(t, path)
	wantFinding(t, rep, FindingSchemaTooNew)
	if rep.Schema.UserVersion != 99 || rep.Data != nil || len(rep.Schema.Pending) != 0 {
		t.Errorf("Schema: %+v data %v", rep.Schema, rep.Data)
	}
	if fileSHA(t, path) != before {
		t.Error("die Datei wurde verändert")
	}
}

func TestInspectChecksumMismatch(t *testing.T) {
	path, _ := builtDB(t)
	rawExec(t, path, "UPDATE schema_migrations SET sha256 = '"+strings.Repeat("a", 64)+"' WHERE version = 1")
	rep := inspect(t, path)
	wantFinding(t, rep, FindingChecksumMismatch)
	if rep.Data != nil {
		t.Error("Grunddaten trotz Befund im Schema")
	}
}

func TestInspectHistoryMismatch(t *testing.T) {
	// user_version passt nicht zur Historie.
	path, _ := builtDB(t)
	rawExec(t, path, "PRAGMA user_version = 0")
	wantFinding(t, inspect(t, path), FindingHistoryMismatch)

	// Name weicht von der bekannten Migration ab.
	path, _ = builtDB(t)
	rawExec(t, path, "UPDATE schema_migrations SET name = 'andere' WHERE version = 1")
	wantFinding(t, inspect(t, path), FindingHistoryMismatch)

	// Fremde Datenbank: Tabellen, aber keine Migrationshistorie (Rollback-Journal, nicht WAL).
	foreign := filepath.Join(t.TempDir(), "fremd.db")
	rawExec(t, foreign, "CREATE TABLE fremd (a INTEGER)", "INSERT INTO fremd VALUES (1)")
	rep := inspect(t, foreign)
	wantFinding(t, rep, FindingForeignDatabase)
	if rep.HasFinding(FindingHistoryMismatch) {
		t.Errorf("fremde Datenbank meldet zusätzlich %s", FindingHistoryMismatch)
	}
	if rep.JournalMode != "delete" || rep.ReadMode != ReadImmutable || rep.Data != nil {
		t.Errorf("fremde Datenbank: Journal %q, Leseweg %q", rep.JournalMode, rep.ReadMode)
	}
}

func TestInspectForeignKeyViolation(t *testing.T) {
	path, _ := builtDB(t)
	// Fremdschlüssel sind in der rohen Verbindung aus: das Ziel der Aufträge verschwindet.
	rawExec(t, path, "PRAGMA foreign_keys = OFF", "DELETE FROM goals WHERE goal_id = 'ms-1'")
	rep := inspect(t, path)
	wantFinding(t, rep, FindingForeignKeys)
	if rep.Integrity.ForeignKeyViolations == 0 || rep.Integrity.QuickCheck != "ok" {
		t.Errorf("Integrität: %+v", rep.Integrity)
	}
}

func TestInspectCorruptFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	must(t, os.MkdirAll(dir, 0o700))
	path := filepath.Join(dir, "ccmas.db")

	// Müll ohne SQLite-Kopfzeile.
	must(t, os.WriteFile(path, []byte(strings.Repeat("kein sqlite ", 500)), 0o600))
	rep := inspect(t, path)
	wantFinding(t, rep, FindingUnreadable)
	if !rep.Exists || rep.SizeBytes == 0 || rep.ReadMode != "" || rep.Data != nil {
		t.Errorf("Bericht: %+v", rep)
	}
	if got := fileSet(t, dir); len(got) != 1 {
		t.Errorf("Inspect hat Dateien angelegt: %v", names(got))
	}

	// Kurzer Müll.
	must(t, os.WriteFile(path, []byte("x"), 0o600))
	wantFinding(t, inspect(t, path), FindingUnreadable)

	// Gültiger Kopf, danach zerstörter Inhalt.
	good, _ := builtDB(t)
	raw, err := os.ReadFile(good)
	must(t, err)
	for i := 4096; i < len(raw); i++ {
		raw[i] = byte(i * 31)
	}
	must(t, os.WriteFile(path, raw, 0o600))
	rep = inspect(t, path)
	if rep.OK() || !(rep.HasFinding(FindingUnreadable) || rep.HasFinding(FindingQuickCheck)) {
		t.Errorf("zerstörter Inhalt ohne passenden Befund: %+v", rep.Findings)
	}
	for _, f := range rep.Findings {
		if f.Message == "" {
			t.Errorf("Befund ohne Diagnose: %+v", f)
		}
	}
	if got := fileSet(t, dir); len(got) != 1 {
		t.Errorf("Inspect hat Dateien angelegt: %v", names(got))
	}
}

// TestInspectDirectoryAsPath: ein Verzeichnis anstelle der Datei ist ein Befund, kein Absturz.
func TestInspectDirectoryAsPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ccmas.db")
	must(t, os.Mkdir(path, 0o700))
	rep := inspect(t, path)
	wantFinding(t, rep, FindingUnreadable)
}

// ---- iCloud-Hinweis, JSON -----------------------------------------------------------------------------------

func TestInspectICloudNote(t *testing.T) {
	base := filepath.Join(t.TempDir(), "Mobile Documents", "ccmas")
	has := func(rep Report) bool {
		for _, n := range rep.Notes {
			if strings.Contains(n, "iCloud") && strings.Contains(n, "Mobile Documents") {
				return true
			}
		}
		return false
	}
	// Fehlende Datenbank: der Hinweis betrifft den Pfad.
	if rep := inspect(t, filepath.Join(base, "state", "ccmas.db")); !has(rep) || !rep.OK() {
		t.Errorf("Hinweis fehlt bei fehlender Datenbank: %+v", rep.Notes)
	}
	// Vorhandene Datenbank dort: Hinweis, aber kein Befund.
	path := filepath.Join(base, "state", "ccmas.db")
	s := openStore(t, path)
	must(t, s.Migrate(ctxT(t)))
	must(t, s.Close())
	if rep := inspect(t, path); !has(rep) || !rep.OK() {
		t.Errorf("Hinweis fehlt oder Befund: %+v %+v", rep.Notes, rep.Findings)
	}
	// Normaler Pfad: kein Hinweis.
	if rep := inspect(t, dbPath(t)); has(rep) {
		t.Errorf("Hinweis ohne iCloud-Ordner: %v", rep.Notes)
	}
}

// TestInspectJSONShape: Feldnamen sind ein Vertrag; Listen sind nie null.
func TestInspectJSONShape(t *testing.T) {
	path, _ := builtDB(t)
	raw, err := json.Marshal(inspect(t, path))
	must(t, err)
	var m map[string]any
	must(t, json.Unmarshal(raw, &m))
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
	for _, c := range []struct{ got, want string }{
		{keys(m), "data,exists,findings,integrity,journal_mode,notes,path,permissions,read_mode,schema,size_bytes"},
		{keys(m["permissions"]), "database,directory,shm,wal"},
		{keys(m["permissions"].(map[string]any)["database"]), "exists,insecure,mode,path,size_bytes"},
		{keys(m["schema"]), "applied,highest_applied,highest_known,pending,user_version"},
		{keys(m["integrity"]), "checked,foreign_key_violations,quick_check,quick_check_messages"},
		{keys(m["data"]), "events,last_event_at,projects"},
		{keys(m["data"].(map[string]any)["projects"].([]any)[0]), "active_executions,open_questions,outbox,project_id,state_revision,work_orders"},
		{keys(m["schema"].(map[string]any)["applied"].([]any)[0]), "applied_at,name,version"},
	} {
		if c.got != c.want {
			t.Errorf("Felder %q, erwartet %q", c.got, c.want)
		}
	}
	for _, f := range []string{"findings", "notes"} {
		if _, ok := m[f].([]any); !ok {
			t.Errorf("%s ist keine Liste: %v", f, m[f])
		}
	}
	if _, ok := m["schema"].(map[string]any)["pending"].([]any); !ok {
		t.Error("schema.pending ist keine Liste")
	}
	// Eine fehlende Datenbank: Listen sind leer, nicht null; data ist null.
	raw, _ = json.Marshal(inspect(t, filepath.Join(t.TempDir(), "x", "ccmas.db")))
	for _, want := range []string{`"findings":[]`, `"applied":[]`, `"pending":[]`, `"data":null`, `"exists":false`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("JSON enthält %s nicht: %s", want, raw)
		}
	}
}

// ---- Schreiber wird während der Prüfung aktiv ---------------------------------------------------------------

// TestInspectRetriesWhenWriterAppears: Erscheint während des Lesens ein -wal (ein Schreiber hat angefangen), gilt
// der Versuch nicht; der nächste Versuch wählt den Leseweg neu.
func TestInspectRetriesWhenWriterAppears(t *testing.T) {
	path, _ := builtDB(t)
	calls := 0
	inspectAfterRead = func(p string) {
		calls++
		if calls == 1 {
			must(t, os.WriteFile(p+"-wal", nil, 0o600)) // Nachahmung: ein Schreiber legt das WAL an
		}
	}
	t.Cleanup(func() { inspectAfterRead = nil })
	rep := inspect(t, path)
	wantNoFindings(t, rep)
	if calls != 2 || rep.ReadMode != ReadSnapshotCopy || rep.Data == nil {
		t.Errorf("Versuche %d, Leseweg %q, Daten %v", calls, rep.ReadMode, rep.Data)
	}
}

// TestInspectGivesUpWhenUnstable: Ändert sich die Hauptdatei bei jedem Versuch, meldet der Bericht busy statt
// eines unsicheren Stands.
func TestInspectGivesUpWhenUnstable(t *testing.T) {
	path, _ := builtDB(t)
	calls := 0
	inspectAfterRead = func(p string) {
		calls++
		must(t, os.Chtimes(p, time.Now(), time.Now().Add(time.Duration(calls)*time.Hour)))
	}
	t.Cleanup(func() { inspectAfterRead = nil })
	rep := inspect(t, path)
	wantFinding(t, rep, FindingBusy)
	if calls != inspectAttempts || rep.Data != nil || rep.ReadMode != "" {
		t.Errorf("Versuche %d, Leseweg %q, Daten %v", calls, rep.ReadMode, rep.Data)
	}
}

// TestInspectRollbackJournalFile: Eine Datenbank im Rollback-Journal-Modus mit vorhandener -journal-Datei wird mit
// mode=ro gelesen (immutable würde ein heißes Journal übersehen); es entsteht nichts.
func TestInspectRollbackJournalFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	must(t, os.MkdirAll(dir, 0o700))
	path := filepath.Join(dir, "ccmas.db")
	rawExec(t, path, "CREATE TABLE fremd (a INTEGER)")
	must(t, os.WriteFile(path+"-journal", nil, 0o600)) // leer: kein heißes Journal
	before := fileSet(t, dir)
	rep := inspect(t, path)
	if rep.ReadMode != ReadReadOnly || rep.JournalMode != "delete" || !rep.HasFinding(FindingForeignDatabase) {
		t.Errorf("Leseweg %q, Journal %q, Befunde %+v", rep.ReadMode, rep.JournalMode, rep.Findings)
	}
	if after := fileSet(t, dir); !equalSets(before, after) {
		t.Errorf("Dateien vorher %v, nachher %v", names(before), names(after))
	}
}
