package persistence

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/alexwenzel/ccmas/internal/domain"
)

// Nur lesende Prüfung einer bestehenden Datenbank (Inspect). Sie ist die Grundlage von „ccmas status“ und hat
// keinerlei Schreibwirkung: Sie legt weder Datei noch Verzeichnis an, setzt kein journal_mode, ändert keine Rechte,
// migriert nicht und lässt nach der Rückkehr keine neue Datei liegen. Warum dafür drei Lesewege nötig sind, steht
// in doc.go (Abschnitt „Nur lesende Prüfung“).

// Lesewege (Report.ReadMode).
const (
	// ReadImmutable: Die Datei wird mit immutable=1 geöffnet. SQLite nimmt dann keine Sperren, öffnet weder -wal noch
	// -shm und legt nichts an. Sicher nur, wenn kein Schreiber die Datei offen hat; das wird vor und nach dem Lesen
	// durch Prüfung der Nachbardateien und der Hauptdatei abgesichert.
	ReadImmutable = "immutable"
	// ReadReadOnly: mode=ro auf der Originaldatei. Nur dann gewählt, wenn -wal und -shm bereits existieren
	// (typisch: ein Schreiber ist offen); SQLite legt dann keine neue Datei an und liest den festgeschriebenen Stand.
	ReadReadOnly = "readonly"
	// ReadSnapshotCopy: Datei und -wal werden in ein privates, danach gelöschtes Temp-Verzeichnis kopiert und dort
	// mode=ro gelesen. Gewählt bei einem -wal ohne -shm (WAL-Reste ohne offenen Schreiber): mode=ro würde im
	// Verzeichnis der Datenbank ein -shm anlegen und liegen lassen.
	ReadSnapshotCopy = "snapshot_copy"
)

// Befundcodes (Finding.Code). Die Migrationscodes sind die Fehlercodes des Pakets.
const (
	FindingSchemaTooNew     = string(CodeSchemaTooNew)
	FindingChecksumMismatch = string(CodeChecksumMismatch)
	FindingHistoryMismatch  = string(CodeHistoryMismatch)
	FindingBusy             = string(CodeBusy)
	// FindingQuickCheck: PRAGMA quick_check meldet Schäden.
	FindingQuickCheck = "quick_check_failed"
	// FindingForeignKeys: PRAGMA foreign_key_check meldet Verstöße.
	FindingForeignKeys = "foreign_key_violations"
	// FindingInsecurePerms: Gruppen- oder Weltrechte an Datei, Verzeichnis, -wal oder -shm.
	FindingInsecurePerms = "insecure_permissions"
	// FindingUnreadable: Die Datei ist keine lesbare SQLite-Datenbank (kaputt oder fremd) oder nicht zugreifbar.
	FindingUnreadable = "unreadable"
)

const (
	maxQuickCheckMessages = 10
	inspectBusyTimeout    = 2 * time.Second
	inspectAttempts       = 3
	maxSnapshotCopyBytes  = 256 << 20
	sqliteHeaderMagic     = "SQLite format 3\x00"
	sqliteHeaderLen       = 100
	iCloudDocuments       = "Mobile Documents"
	iCloudDrive           = "com~apple~CloudDocs"
	insecureModeMask      = 0o077
	snapshotTmpPrefix     = "ccmas-inspect-"
)

// Finding ist ein Befund der Prüfung. Jeder Befund ist ein Mangel der Datenbank oder ihrer Ablage; ausstehende
// Migrationen sind keiner (sie stehen in Schema.Pending und in den Hinweisen).
type Finding struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// FileInfo beschreibt eine Datei oder ein Verzeichnis der Ablage.
type FileInfo struct {
	Path   string `json:"path"`
	Exists bool   `json:"exists"`
	// Mode sind die Rechte als vierstellige Oktalzahl ("0600"); leer, wenn es die Datei nicht gibt oder die
	// Plattform keine Unix-Rechte kennt.
	Mode string `json:"mode"`
	// Insecure: Gruppen- oder Weltrechte sind gesetzt.
	Insecure bool  `json:"insecure"`
	Size     int64 `json:"size_bytes"`
}

// Permissions fasst die Rechte von Verzeichnis, Datenbankdatei, -wal und -shm zusammen.
type Permissions struct {
	Directory FileInfo `json:"directory"`
	Database  FileInfo `json:"database"`
	WAL       FileInfo `json:"wal"`
	SHM       FileInfo `json:"shm"`
}

// MigrationInfo ist ein Eintrag der Migrationshistorie (Applied) oder eine ausstehende Migration (Pending).
type MigrationInfo struct {
	Version   int        `json:"version"`
	Name      string     `json:"name"`
	AppliedAt *time.Time `json:"applied_at"`
}

// SchemaReport beschreibt den Schemastand.
type SchemaReport struct {
	// UserVersion ist PRAGMA user_version.
	UserVersion int `json:"user_version"`
	// HighestApplied ist die höchste Version in schema_migrations (0, wenn keine).
	HighestApplied int `json:"highest_applied"`
	// HighestKnown ist die höchste Version, die diese Software kennt.
	HighestKnown int             `json:"highest_known"`
	Applied      []MigrationInfo `json:"applied"`
	// Pending sind die ausstehenden Migrationen; nur gesetzt, wenn die Historie ohne Befund ist.
	Pending []MigrationInfo `json:"pending"`
}

// IntegrityReport enthält die Ergebnisse von PRAGMA quick_check und PRAGMA foreign_key_check.
type IntegrityReport struct {
	// Checked: Die Prüfungen konnten laufen.
	Checked bool `json:"checked"`
	// QuickCheck ist "ok" oder "failed"; leer, wenn nicht geprüft.
	QuickCheck string `json:"quick_check"`
	// QuickCheckMessages sind die ersten Meldungen von quick_check, wenn es nicht "ok" war.
	QuickCheckMessages   []string `json:"quick_check_messages"`
	ForeignKeyViolations int      `json:"foreign_key_violations"`
}

// ProjectData sind die Grunddaten eines Projekts. Es sind nur Anzahlen und Zustandsnamen; Inhalte (Aufträge, Prompts,
// Nutzdaten) liest die Prüfung nicht.
type ProjectData struct {
	ProjectID     string `json:"project_id"`
	StateRevision int64  `json:"state_revision"`
	// WorkOrders zählt die Aufträge je fachlichem Zustand (alle bekannten Zustände, auch mit 0).
	WorkOrders map[string]int `json:"work_orders"`
	// ActiveExecutions sind die Ausführungsversuche in einem Nicht-Endzustand.
	ActiveExecutions int `json:"active_executions"`
	OpenQuestions    int `json:"open_questions"`
	// Outbox zählt die externen Aktionen je Zustand (alle bekannten Zustände, auch mit 0).
	Outbox map[string]int `json:"outbox"`
}

// DataReport sind die Grunddaten der Datenbank.
type DataReport struct {
	Projects []ProjectData `json:"projects"`
	// Events ist die Zahl der Audit-Ereignisse, LastEventAt die Zeit des letzten (nil, wenn keins).
	Events      int64      `json:"events"`
	LastEventAt *time.Time `json:"last_event_at"`
}

// Report ist das Ergebnis von Inspect. Die JSON-Namen sind stabil (Vertrag von „ccmas status --json“).
type Report struct {
	Path      string `json:"path"`
	Exists    bool   `json:"exists"`
	SizeBytes int64  `json:"size_bytes"`
	// ReadMode ist der Leseweg (ReadImmutable, ReadReadOnly, ReadSnapshotCopy); leer, wenn die Datenbank nicht
	// gelesen wurde.
	ReadMode    string          `json:"read_mode"`
	Permissions Permissions     `json:"permissions"`
	JournalMode string          `json:"journal_mode"`
	Schema      SchemaReport    `json:"schema"`
	Integrity   IntegrityReport `json:"integrity"`
	// Data ist nil, wenn die Grunddaten nicht lesbar sind (keine Datenbank, nicht migriert, Befund im Schema).
	Data     *DataReport `json:"data"`
	Notes    []string    `json:"notes"`
	Findings []Finding   `json:"findings"`
}

// OK meldet, dass die Prüfung keinen Befund ergab. Eine fehlende Datenbank ist kein Befund.
func (r *Report) OK() bool { return len(r.Findings) == 0 }

// HasFinding meldet, ob ein Befund mit diesem Code vorliegt.
func (r *Report) HasFinding(code string) bool {
	for _, f := range r.Findings {
		if f.Code == code {
			return true
		}
	}
	return false
}

func (r *Report) find(code, format string, args ...any) {
	r.Findings = append(r.Findings, Finding{Code: code, Message: fmt.Sprintf(format, args...)})
}

func (r *Report) note(format string, args ...any) {
	r.Notes = append(r.Notes, fmt.Sprintf(format, args...))
}

// Inspect prüft die Datenbank unter dem absoluten, kanonischen Pfad, ohne etwas zu schreiben. Ein Fehler ist nur
// ein ungültiger Pfad (CodeInvalid) oder ein Abbruch des Kontexts; alles andere, auch eine fehlende, kaputte oder
// fremde Datei, steht im Bericht (Exists, Findings). Der Bericht beruht auf einem einzigen festgeschriebenen Stand
// (eine Lesetransaktion); ein gleichzeitig offener Schreiber wird nicht blockiert.
func Inspect(ctx context.Context, path string) (Report, error) {
	if err := checkDBPath(path); err != nil {
		return Report{}, err
	}
	rep := Report{
		Path:     path,
		Notes:    []string{},
		Findings: []Finding{},
		Schema:   SchemaReport{Applied: []MigrationInfo{}, Pending: []MigrationInfo{}},
		Integrity: IntegrityReport{
			QuickCheckMessages: []string{},
		},
	}
	known := Migrations()
	rep.Schema.HighestKnown = known[len(known)-1].Version
	if cloudFolder(path) {
		rep.note("Der Pfad liegt in einem iCloud-Ordner (%s): SQLite-Dateien gehören nicht in synchronisierte Ordner "+
			"(-wal, -shm und Sperren lassen sich nicht synchronisieren, Konfliktkopien beschädigen die Datenbank). "+
			"Den Ausführungsbereich außerhalb von iCloud wählen.", iCloudDocuments)
	}
	if err := ctx.Err(); err != nil {
		return rep, err
	}

	st, err := os.Stat(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		rep.Permissions = readPermissions(path)
		rep.note("Die Datenbank ist nicht vorhanden; der Dienst legt sie beim ersten Start an.")
		return rep, nil
	case err != nil:
		rep.Exists = true
		rep.Permissions = readPermissions(path)
		rep.find(FindingUnreadable, "Datenbankdatei nicht lesbar: %v", err)
		return rep, nil
	}
	rep.Exists = true
	rep.SizeBytes = st.Size()
	// Rechte zuletzt melden, damit auch -wal und -shm erfasst sind, die ein Schreiber währenddessen angelegt hat.
	finish := func() (Report, error) {
		rep.Permissions = readPermissions(path)
		rep.permissionFindings()
		return rep, nil
	}
	if !st.Mode().IsRegular() {
		rep.find(FindingUnreadable, "Der Pfad ist keine reguläre Datei (%s)", st.Mode().Type())
		return finish()
	}

	header, err := readHeader(path)
	if err != nil {
		rep.find(FindingUnreadable, "Datenbankdatei nicht lesbar: %v", err)
		return finish()
	}
	if len(header) > 0 && !validHeader(header) {
		rep.find(FindingUnreadable, "Keine SQLite-Datenbank: die Kopfzeile fehlt oder ist beschädigt (fremde oder kaputte Datei)")
		return finish()
	}

	baseNotes, baseFindings := len(rep.Notes), len(rep.Findings)
	for attempt := 1; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return rep, err
		}
		stable, err := rep.readOnce(ctx, path, header)
		if err != nil {
			return rep, err
		}
		if stable {
			break
		}
		rep.resetReading(baseNotes, baseFindings)
		if attempt >= inspectAttempts {
			rep.find(FindingBusy, "Die Datenbank wurde während der Prüfung verändert (ein Schreiber wurde aktiv); die Prüfung ist nach %d Versuchen nicht stabil. Erneut aufrufen.", attempt)
			break
		}
	}
	return finish()
}

// resetReading verwirft, was ein nicht stabiler Leseversuch gesammelt hat.
func (r *Report) resetReading(notes, findings int) {
	r.ReadMode = ""
	r.JournalMode = ""
	r.Schema.UserVersion, r.Schema.HighestApplied = 0, 0
	r.Schema.Applied, r.Schema.Pending = []MigrationInfo{}, []MigrationInfo{}
	r.Integrity = IntegrityReport{QuickCheckMessages: []string{}}
	r.Data = nil
	r.Notes, r.Findings = r.Notes[:notes], r.Findings[:findings]
}

// inspectAfterRead ist ein Einhängepunkt für Tests: Er läuft nach dem Lesen und vor der Stabilitätsprüfung und
// erlaubt, einen Schreiber nachzuahmen, der währenddessen aktiv wird. Im Betrieb ist er nil.
var inspectAfterRead func(path string)

// fileStamp ist, woran eine Datei als unverändert erkannt wird.
type fileStamp struct {
	exists bool
	size   int64
	mtime  time.Time
}

func (a fileStamp) same(b fileStamp) bool {
	return a.exists == b.exists && a.size == b.size && a.mtime.Equal(b.mtime)
}

func stamp(p string) fileStamp {
	st, err := os.Lstat(p)
	if err != nil {
		return fileStamp{}
	}
	return fileStamp{exists: true, size: st.Size(), mtime: st.ModTime()}
}

// readOnce macht einen Leseversuch. stable=false heißt: Die Nachbardateien oder die Hauptdatei haben sich
// während des Lesens verändert (ein Schreiber wurde aktiv); der Versuch ist zu verwerfen.
func (r *Report) readOnce(ctx context.Context, path string, header []byte) (stable bool, err error) {
	mainBefore, walBefore, shmBefore, jrnBefore := stamp(path), stamp(path+"-wal"), stamp(path+"-shm"), stamp(path+"-journal")

	var (
		mode   string
		target = path
	)
	switch {
	case jrnBefore.exists, walBefore.exists && shmBefore.exists:
		mode = ReadReadOnly
	case walBefore.exists: // -wal ohne -shm
		mode = ReadSnapshotCopy
	default:
		mode = ReadImmutable
	}

	if mode == ReadSnapshotCopy {
		if mainBefore.size+walBefore.size > maxSnapshotCopyBytes {
			r.find(FindingUnreadable, "WAL-Reste ohne -shm und zu groß für eine Prüfkopie (%d Bytes): die Datenbank wurde nicht gelesen. Den Dienst starten (er stellt sie wieder her) oder sie von Hand prüfen.",
				mainBefore.size+walBefore.size)
			return true, nil
		}
		tmp, err := os.MkdirTemp("", snapshotTmpPrefix)
		if err != nil {
			r.find(FindingUnreadable, "Prüfkopie nicht anlegbar: %v", err)
			return true, nil
		}
		defer os.RemoveAll(tmp)
		target = filepath.Join(tmp, "ccmas.db")
		if err := copyFile(path, target); err != nil {
			r.find(FindingUnreadable, "Prüfkopie nicht anlegbar: %v", err)
			return true, nil
		}
		if err := copyFile(path+"-wal", target+"-wal"); err != nil {
			r.find(FindingUnreadable, "Prüfkopie der WAL-Datei nicht anlegbar: %v", err)
			return true, nil
		}
	}
	r.ReadMode = mode

	db, err := openForInspect(target, mode == ReadImmutable)
	if err != nil {
		r.fail(err)
		return true, nil
	}
	defer db.Close()
	if err := r.readDatabase(ctx, db, mode, header); err != nil {
		if cerr := ctx.Err(); cerr != nil {
			return true, cerr
		}
		r.fail(err)
	}

	// Stabilität: Hat ein Schreiber währenddessen angefangen, gilt das Gelesene nicht.
	if inspectAfterRead != nil {
		inspectAfterRead(path)
	}
	mainAfter, walAfter, shmAfter, jrnAfter := stamp(path), stamp(path+"-wal"), stamp(path+"-shm"), stamp(path+"-journal")
	switch mode {
	case ReadImmutable:
		if !mainAfter.same(mainBefore) || walAfter.exists || jrnAfter.exists {
			return false, nil
		}
	case ReadSnapshotCopy:
		if !mainAfter.same(mainBefore) || !walAfter.same(walBefore) || shmAfter.exists || jrnAfter.exists {
			return false, nil
		}
	}
	return true, nil
}

// fail macht aus einem Lesefehler einen Befund mit Diagnose.
func (r *Report) fail(err error) {
	switch CodeOf(mapErr(err)) {
	case CodeBusy:
		r.find(FindingBusy, "Die Datenbank ist gesperrt (%v); erneut versuchen", err)
	default:
		r.find(FindingUnreadable, "Die Datenbank ist nicht lesbar: %v", err)
	}
}

// openForInspect öffnet die Datei nur lesend: immutable=1 oder mode=ro, query_only, kurzer busy_timeout. Eine
// einzige Verbindung, damit alle Abfragen denselben Stand sehen.
func openForInspect(path string, immutable bool) (*sql.DB, error) {
	initDriver()
	q := url.Values{}
	if immutable {
		q.Set("immutable", "1")
	} else {
		q.Set("mode", "ro")
	}
	q.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", inspectBusyTimeout.Milliseconds()))
	q.Add("_pragma", "query_only(1)")
	u := url.URL{Scheme: "file", Path: path, RawQuery: q.Encode()}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	return db, nil
}

// readDatabase liest in einer Lesetransaktion: Journal, Schema, Integrität und Grunddaten.
func (r *Report) readDatabase(ctx context.Context, db *sql.DB, mode string, header []byte) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if mode == ReadImmutable {
		// immutable ignoriert das WAL und meldet "delete"; maßgeblich ist das Dateiformat im Kopf.
		r.JournalMode = journalModeFromHeader(header)
	} else {
		var jm string
		if err := tx.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&jm); err != nil {
			return err
		}
		r.JournalMode = jm
	}

	state, err := readState(ctx, tx)
	if err != nil {
		return err
	}
	r.Schema.UserVersion = state.userVersion
	for _, a := range state.applied {
		at := a.AppliedAt
		r.Schema.Applied = append(r.Schema.Applied, MigrationInfo{Version: a.Version, Name: a.Name, AppliedAt: &at})
		r.Schema.HighestApplied = a.Version
	}
	known := Migrations()
	pending, planErr := planMigrations(known, state)
	if planErr != nil {
		r.find(string(CodeOf(planErr)), "%s", messageOf(planErr))
	} else {
		for _, m := range pending {
			r.Schema.Pending = append(r.Schema.Pending, MigrationInfo{Version: m.Version, Name: m.Name})
		}
		if len(pending) > 0 {
			r.note("%d ausstehende Migration(en), zuletzt Version %d: Die Datenbank wird beim Start des Dienstes (oder durch Migrate) migriert; das ist kein Befund.",
				len(pending), pending[len(pending)-1].Version)
		}
	}

	if err := r.checkIntegrity(ctx, tx); err != nil {
		return err
	}
	if planErr == nil && len(state.applied) > 0 {
		data, err := readData(ctx, tx)
		if err != nil {
			return err
		}
		r.Data = data
	}
	return nil
}

// messageOf liefert die Meldung eines *Error ohne den Präfix mit dem Code.
func messageOf(err error) string {
	var pe *Error
	if errors.As(err, &pe) && pe.Message != "" {
		return pe.Message
	}
	return err.Error()
}

func (r *Report) checkIntegrity(ctx context.Context, q querier) error {
	rows, err := q.QueryContext(ctx, "PRAGMA quick_check")
	if err != nil {
		return err
	}
	var msgs []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			rows.Close()
			return err
		}
		msgs = append(msgs, s)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	r.Integrity.Checked = true
	if len(msgs) == 1 && msgs[0] == "ok" {
		r.Integrity.QuickCheck = "ok"
	} else {
		r.Integrity.QuickCheck = "failed"
		if len(msgs) > maxQuickCheckMessages {
			msgs = msgs[:maxQuickCheckMessages]
		}
		r.Integrity.QuickCheckMessages = msgs
		r.find(FindingQuickCheck, "PRAGMA quick_check meldet Schäden: %s", strings.Join(msgs, "; "))
	}

	rows, err = q.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return err
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		n++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	r.Integrity.ForeignKeyViolations = n
	if n > 0 {
		r.find(FindingForeignKeys, "PRAGMA foreign_key_check meldet %d Verstoß/Verstöße gegen Fremdschlüssel", n)
	}
	return nil
}

func readData(ctx context.Context, q querier) (*DataReport, error) {
	data := &DataReport{Projects: []ProjectData{}}
	idx := map[string]*ProjectData{}
	rows, err := q.QueryContext(ctx, "SELECT project_id, state_revision FROM projects ORDER BY project_id")
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		p := ProjectData{WorkOrders: map[string]int{}, Outbox: map[string]int{}}
		if err := rows.Scan(&p.ProjectID, &p.StateRevision); err != nil {
			rows.Close()
			return nil, err
		}
		for _, s := range domain.WorkOrderStates() {
			p.WorkOrders[string(s)] = 0
		}
		for _, s := range OutboxStates() {
			p.Outbox[string(s)] = 0
		}
		data.Projects = append(data.Projects, p)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	for i := range data.Projects {
		idx[data.Projects[i].ProjectID] = &data.Projects[i]
	}

	// Gruppierte Zählungen: (Projekt, Schlüssel, Anzahl).
	count := func(query string, args []any, set func(p *ProjectData, key string, n int)) error {
		rs, err := q.QueryContext(ctx, query, args...)
		if err != nil {
			return err
		}
		defer rs.Close()
		for rs.Next() {
			var project, key string
			var n int
			if err := rs.Scan(&project, &key, &n); err != nil {
				return err
			}
			if p := idx[project]; p != nil {
				set(p, key, n)
			}
		}
		return rs.Err()
	}
	if err := count("SELECT project_id, state, count(*) FROM work_orders GROUP BY project_id, state", nil,
		func(p *ProjectData, key string, n int) { p.WorkOrders[key] = n }); err != nil {
		return nil, err
	}
	if err := count("SELECT project_id, state, count(*) FROM outbox GROUP BY project_id, state", nil,
		func(p *ProjectData, key string, n int) { p.Outbox[key] = n }); err != nil {
		return nil, err
	}
	var active []any
	var marks []string
	for _, s := range domain.ExecutionStates() {
		if !s.IsTerminal() {
			active = append(active, string(s))
			marks = append(marks, "?")
		}
	}
	if err := count("SELECT project_id, 'active', count(*) FROM execution_attempts WHERE state IN ("+strings.Join(marks, ",")+") GROUP BY project_id",
		active, func(p *ProjectData, _ string, n int) { p.ActiveExecutions = n }); err != nil {
		return nil, err
	}
	if err := count("SELECT project_id, 'open', count(*) FROM questions WHERE state = ? GROUP BY project_id", []any{string(domain.QuestionOpen)},
		func(p *ProjectData, _ string, n int) { p.OpenQuestions = n }); err != nil {
		return nil, err
	}

	var last sql.NullString
	if err := q.QueryRowContext(ctx, "SELECT count(*), max(occurred_at) FROM events").Scan(&data.Events, &last); err != nil {
		return nil, err
	}
	if last.Valid {
		t, err := scanTime(last.String)
		if err != nil {
			return nil, err
		}
		data.LastEventAt = &t
	}
	return data, nil
}

// ---- Dateien und Rechte --------------------------------------------------------------------------

func readPermissions(path string) Permissions {
	return Permissions{
		Directory: fileInfo(filepath.Dir(path)),
		Database:  fileInfo(path),
		WAL:       fileInfo(path + "-wal"),
		SHM:       fileInfo(path + "-shm"),
	}
}

func fileInfo(p string) FileInfo {
	fi := FileInfo{Path: p}
	st, err := os.Stat(p)
	if err != nil {
		return fi
	}
	fi.Exists = true
	fi.Size = st.Size()
	if runtime.GOOS == "windows" {
		return fi // keine Unix-Rechte
	}
	perm := st.Mode().Perm()
	fi.Mode = fmt.Sprintf("%04o", perm)
	fi.Insecure = perm&insecureModeMask != 0
	return fi
}

func (r *Report) permissionFindings() {
	for _, e := range []struct {
		what string
		fi   FileInfo
	}{
		{"Verzeichnis", r.Permissions.Directory},
		{"Datenbankdatei", r.Permissions.Database},
		{"Datei -wal", r.Permissions.WAL},
		{"Datei -shm", r.Permissions.SHM},
	} {
		if e.fi.Insecure {
			r.find(FindingInsecurePerms, "%s %s hat die Rechte %s: Gruppen- oder Weltrechte sind gesetzt (erwartet: nur der Eigentümer, Datei 0600, Verzeichnis 0700). Die Prüfung ändert sie nicht.",
				e.what, e.fi.Path, e.fi.Mode)
		}
	}
}

func readHeader(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	buf := make([]byte, sqliteHeaderLen)
	n, err := io.ReadFull(f, buf)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, err
	}
	return buf[:n], nil
}

// validHeader: Kopfzeile "SQLite format 3\0". Eine leere Datei (Länge 0) ist eine gültige leere Datenbank und
// kommt hier nicht an; eine kürzere als 100 Byte ist keine.
func validHeader(h []byte) bool {
	return len(h) >= sqliteHeaderLen && bytes.HasPrefix(h, []byte(sqliteHeaderMagic))
}

// journalModeFromHeader liest das Dateiformat: Version 2 an Offset 18 und 19 heißt WAL, 1 Rollback-Journal.
func journalModeFromHeader(h []byte) string {
	if len(h) < sqliteHeaderLen {
		return ""
	}
	if h[18] == 2 && h[19] == 2 {
		return "wal"
	}
	return "delete"
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// cloudFolder meldet, ob der Pfad (oder der aufgelöste Pfad seines nächsten vorhandenen Vorfahren) in einem
// iCloud-Ordner liegt.
func cloudFolder(path string) bool {
	has := func(p string) bool {
		for _, c := range strings.Split(p, string(filepath.Separator)) {
			if c == iCloudDocuments || c == iCloudDrive {
				return true
			}
		}
		return false
	}
	if has(path) {
		return true
	}
	dir := filepath.Dir(path)
	for {
		if real, err := filepath.EvalSymlinks(dir); err == nil {
			return has(real)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
		dir = parent
	}
}
