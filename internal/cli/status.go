package cli

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/alexwenzel/ccmas/internal/domain"
	"github.com/alexwenzel/ccmas/internal/persistence"
)

// Stufen des SQLite-Zustands (statusReport.State.Level). Sie folgen der Unterscheidung der Spezifikation (vorhanden,
// konfiguriert, angemeldet, funktional nachgewiesen), angewandt auf eine lokale Datei: „angemeldet“ gibt es hier nicht.
const (
	levelNotPresent     = "not_present"     // keine Datenbankdatei
	levelFilePresent    = "present"         // Datei da, aber nicht (vollständig) migriert oder mit Befund im Schema
	levelSchemaCurrent  = "configured"      // Schema auf der erwarteten Version, Historie und Prüfsummen stimmig
	levelReadVerified   = "functional_read" // zusätzlich: Integritätsprüfung und Grunddaten gelesen (nur Lesepfad)
	levelReadScopeNone  = "Es gibt keine Datenbank; der Dienst legt sie beim ersten Start an."
	levelReadScopeFile  = "Datei vorhanden; das Schema ist nicht aktuell oder auffällig."
	levelReadScopeSchem = "Schema aktuell, Historie und Prüfsummen stimmig; Integrität nicht nachgewiesen."
	levelReadScopeRead  = "Integritätsprüfung und Grunddaten über einen nur lesenden Zugriff nachgewiesen; der Schreibpfad (Dienst) wird von status nicht geprüft."
)

// notChecked benennt die Bereiche, die „ccmas status“ noch nicht prüft, und das Vorhaben, in dem sie folgen.
type notChecked struct {
	Area      string `json:"area"`
	Label     string `json:"label"`
	FollowsIn string `json:"follows_in"`
	Status    string `json:"status"`
}

// uncheckedAreas: alles, was die Spezifikation für status vorsieht und das noch fehlt. Nichts davon wird angedeutet.
var uncheckedAreas = []notChecked{
	{"service", "Dienstzustand (Prozess, Steuer-Socket)", "CC-51", "not_checked"},
	{"tools", "Werkzeuge (Git, GitHub CLI, Claude, uv, Obsidian CLI)", "M06", "not_checked"},
	{"authentication", "Anmeldung (Claude, GitHub, Asana)", "M06", "not_checked"},
	{"project_assignment", "Projektzuordnung (Repository, Asana-Projekt, Vault)", "M06", "not_checked"},
}

// stateStatus ist der Abschnitt „Zustand (SQLite)“.
type stateStatus struct {
	// Level ist die erreichte Stufe (levelNotPresent, levelFilePresent, levelSchemaCurrent, levelReadVerified).
	Level string `json:"level"`
	// LevelScope sagt, was die Stufe belegt und was nicht.
	LevelScope string             `json:"level_scope"`
	Database   persistence.Report `json:"database"`
}

// statusReport ist die JSON-Ausgabe von „ccmas status“. Die Feldnamen sind ein Vertrag.
type statusReport struct {
	CheckedAt    time.Time `json:"checked_at"`
	ExecutionDir string    `json:"execution_dir"`
	// OK: Die geprüften Bereiche (derzeit nur der SQLite-Zustand) haben keinen Befund. Es sagt nichts über NotChecked.
	OK         bool         `json:"ok"`
	State      stateStatus  `json:"state"`
	NotChecked []notChecked `json:"not_checked"`
}

// stateLevel leitet die Stufe aus dem Bericht ab.
func stateLevel(r persistence.Report) (level, scope string) {
	switch {
	case !r.Exists:
		return levelNotPresent, levelReadScopeNone
	case r.ReadMode == "" || r.HasFinding(persistence.FindingSchemaTooNew) || r.HasFinding(persistence.FindingChecksumMismatch) ||
		r.HasFinding(persistence.FindingHistoryMismatch) || len(r.Schema.Pending) > 0 || r.Schema.HighestApplied == 0:
		return levelFilePresent, levelReadScopeFile
	case !r.Integrity.Checked || r.Data == nil || r.HasFinding(persistence.FindingQuickCheck) || r.HasFinding(persistence.FindingForeignKeys) ||
		r.HasFinding(persistence.FindingUnreadable) || r.HasFinding(persistence.FindingBusy):
		return levelSchemaCurrent, levelReadScopeSchem
	}
	return levelReadVerified, levelReadScopeRead
}

type statusOpts struct {
	executionDir string
	root         string
	asJSON       bool
}

func newStatusCommand() *cobra.Command {
	var o statusOpts
	cmd := &cobra.Command{
		Use:     "status [--execution-dir <pfad>] [--root <pfad>] [--json]",
		GroupID: groupAvailable,
		Short:   "Zustand (SQLite) prüfen; Dienst, Werkzeuge und Anmeldung sind noch nicht geprüft",
		Long: "Prüft den SQLite-Zustand des Ausführungsbereichs, ohne etwas zu verändern: Datei, Rechte, Schemaversion und " +
			"Migrationshistorie, Integrität (PRAGMA quick_check und foreign_key_check) und Grunddaten (Projekte mit " +
			"Zustandsrevision, Aufträge je Zustand, aktive Ausführungen, offene Fragen, Outbox je Zustand, Zeit des letzten " +
			"Audit-Ereignisses).\n\n" +
			"status installiert nichts, startet nichts, legt keine Datei und kein Verzeichnis an, migriert nicht, ändert " +
			"keine Rechte und führt keine Agentenausführung aus. Die Datenbank wird nur lesend geöffnet; ein gleichzeitig " +
			"laufender Dienst wird nicht blockiert, der Bericht zeigt dann den festgeschriebenen Stand.\n\n" +
			"Der Ausführungsbereich ergibt sich aus --execution-dir, sonst " + EnvExecutionDir + ", sonst " +
			"<CCMAS-Softwareordner>/.ccmas; die Datenbank liegt fest unter <Ausführungsbereich>/state/ccmas.db. " +
			"Eine fehlende Datenbank ist kein Fehler (sie wird beim ersten Start des Dienstes angelegt). Ausstehende " +
			"Migrationen sind ein Hinweis, kein Befund. Befunde: Schemaversion zu neu (schema_too_new), geänderte " +
			"Migration (migration_checksum_mismatch), abweichende Historie oder fremde Datenbank " +
			"(migration_history_mismatch), quick_check_failed, foreign_key_violations, unsichere Rechte " +
			"(insecure_permissions), unlesbare oder fremde Datei (unreadable) und busy.\n\n" +
			"Die Stufen folgen der Spezifikation: vorhanden, konfiguriert, angemeldet, funktional nachgewiesen. Für den " +
			"SQLite-Zustand heißt „funktional nachgewiesen“ nur: Integrität und Grunddaten wurden lesend belegt; der " +
			"Schreibpfad bleibt ungeprüft. Dienstzustand (CC-51) sowie Werkzeuge, Anmeldung und Projektzuordnung (M06) " +
			"sind nicht geprüft und stehen in der Ausgabe ausdrücklich als solche.\n\n" +
			"Exit-Codes: 0 Datenbank fehlt oder ist ohne Befund, 1 mindestens ein Befund, 2 Bedienfehler.",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir, err := resolveExecutionDir(o.executionDir, o.root)
			if err != nil {
				return usageErrorf("%v", err)
			}
			rep, err := persistence.Inspect(cmd.Context(), stateDBPath(dir))
			if err != nil {
				return err
			}
			level, scope := stateLevel(rep)
			doc := statusReport{
				CheckedAt:    time.Now().UTC(),
				ExecutionDir: dir,
				OK:           rep.OK(),
				State:        stateStatus{Level: level, LevelScope: scope, Database: rep},
				NotChecked:   uncheckedAreas,
			}
			out := cmd.OutOrStdout()
			if o.asJSON {
				err = writeJSON(out, doc)
			} else {
				err = writeStatusText(out, doc)
			}
			if err != nil {
				return err
			}
			if n := len(rep.Findings); n > 0 {
				return fmt.Errorf("status: %d Befund(e) im SQLite-Zustand", n)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&o.executionDir, "execution-dir", "", "Ausführungsbereich (sonst "+EnvExecutionDir+", sonst <Softwareordner>/.ccmas)")
	cmd.Flags().StringVar(&o.root, "root", "", "CCMAS-Softwareordner (sonst "+EnvHome+", sonst Verzeichnis über dem Binary)")
	cmd.Flags().BoolVar(&o.asJSON, "json", false, "Ausgabe als JSON-Objekt")
	return cmd
}

var stateLevelNames = map[string]string{
	levelNotPresent:    "nicht vorhanden",
	levelFilePresent:   "vorhanden",
	levelSchemaCurrent: "konfiguriert",
	levelReadVerified:  "funktional nachgewiesen (nur Lesepfad)",
}

func writeStatusText(out io.Writer, doc statusReport) error {
	r := doc.State.Database
	p := func(format string, a ...any) { fmt.Fprintf(out, format, a...) }

	p("ccmas status (Ausführungsbereich: %s)\n\n", doc.ExecutionDir)
	p("Zustand (SQLite)\n")
	p("  Datenbank:      %s\n", r.Path)
	if !r.Exists {
		p("  Vorhanden:      nein (nicht vorhanden)\n")
	} else {
		p("  Vorhanden:      ja (%d Bytes)\n", r.SizeBytes)
	}
	p("  Stufe:          %s\n", stateLevelNames[doc.State.Level])
	p("                  %s\n", doc.State.LevelScope)
	p("  Anmeldestufe:   nicht zutreffend (lokale Datei, keine Anmeldung)\n")

	if r.Exists {
		p("  Leseweg:        %s\n", orDash(readModeText(r.ReadMode)))
		p("  Rechte:         Verzeichnis %s, Datei %s, -wal %s, -shm %s\n", modeText(r.Permissions.Directory), modeText(r.Permissions.Database),
			modeText(r.Permissions.WAL), modeText(r.Permissions.SHM))
		p("  Journal-Modus:  %s\n", orDash(r.JournalMode))
		pending := "keine"
		if n := len(r.Schema.Pending); n > 0 {
			var vs []string
			for _, m := range r.Schema.Pending {
				vs = append(vs, fmt.Sprintf("%d %s", m.Version, m.Name))
			}
			pending = fmt.Sprintf("%d (%s); Hinweis, kein Befund", n, strings.Join(vs, ", "))
		}
		p("  Schema:         user_version %d, angewandt bis %d, bekannt bis %d, ausstehend: %s\n",
			r.Schema.UserVersion, r.Schema.HighestApplied, r.Schema.HighestKnown, pending)
		if r.Integrity.Checked {
			p("  Integrität:     quick_check %s, Fremdschlüsselverstöße %d\n", r.Integrity.QuickCheck, r.Integrity.ForeignKeyViolations)
		} else {
			p("  Integrität:     nicht geprüft (Datenbank nicht lesbar)\n")
		}
	}
	if d := r.Data; d != nil {
		p("\n  Grunddaten\n")
		if len(d.Projects) == 0 {
			p("    Projekte: keine\n")
		} else {
			tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "    PROJEKT\tREVISION\tAUFTRÄGE\tAKTIVE AUSFÜHRUNGEN\tOFFENE FRAGEN\tOUTBOX")
			for _, pr := range d.Projects {
				fmt.Fprintf(tw, "    %s\t%d\t%s\t%d\t%d\t%s\n", pr.ProjectID, pr.StateRevision, countsText(pr.WorkOrders, workOrderOrder()),
					pr.ActiveExecutions, pr.OpenQuestions, countsText(pr.Outbox, outboxOrder()))
			}
			if err := tw.Flush(); err != nil {
				return err
			}
		}
		last := "kein Ereignis"
		if d.LastEventAt != nil {
			last = d.LastEventAt.UTC().Format(time.RFC3339)
		}
		p("    Letztes Audit-Ereignis: %s (%d Ereignisse)\n", last, d.Events)
	}
	for _, n := range r.Notes {
		p("\n  Hinweis: %s\n", n)
	}
	if len(r.Findings) > 0 {
		p("\n  Befunde\n")
		for _, f := range r.Findings {
			p("    [%s] %s\n", f.Code, f.Message)
		}
	}

	p("\nNicht geprüft (status prüft dies noch nicht)\n")
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	for _, n := range doc.NotChecked {
		fmt.Fprintf(tw, "  %s\tnicht geprüft\tfolgt mit %s\n", n.Label, n.FollowsIn)
	}
	if err := tw.Flush(); err != nil {
		return err
	}

	switch {
	case !doc.OK:
		p("\nErgebnis: %d Befund(e) im SQLite-Zustand; übrige Bereiche nicht geprüft\n", len(r.Findings))
	case !r.Exists:
		p("\nErgebnis: Datenbank nicht vorhanden (kein Befund); übrige Bereiche nicht geprüft\n")
	default:
		p("\nErgebnis: SQLite-Zustand ohne Befund; übrige Bereiche nicht geprüft\n")
	}
	return nil
}

func readModeText(m string) string {
	switch m {
	case persistence.ReadImmutable:
		return "nur lesend, ohne Sperren (immutable): keine Schreiber aktiv"
	case persistence.ReadReadOnly:
		return "nur lesend (mode=ro): festgeschriebener Stand, Schreiber bleibt ungestört"
	case persistence.ReadSnapshotCopy:
		return "nur lesend auf privater Kopie (WAL-Reste ohne -shm)"
	}
	return m
}

func modeText(f persistence.FileInfo) string {
	switch {
	case !f.Exists:
		return "-"
	case f.Mode == "":
		return "vorhanden"
	case f.Insecure:
		return f.Mode + " (unsicher)"
	}
	return f.Mode
}

func workOrderOrder() []string {
	var out []string
	for _, s := range domain.WorkOrderStates() {
		out = append(out, string(s))
	}
	return out
}

func outboxOrder() []string {
	var out []string
	for _, s := range persistence.OutboxStates() {
		out = append(out, string(s))
	}
	return out
}

// countsText zeigt die von 0 verschiedenen Anzahlen in der Reihenfolge der Zustände; sonst „-“.
func countsText(m map[string]int, order []string) string {
	var parts []string
	seen := map[string]bool{}
	for _, k := range order {
		seen[k] = true
		if m[k] > 0 {
			parts = append(parts, fmt.Sprintf("%s %d", k, m[k]))
		}
	}
	var extra []string
	for k, v := range m {
		if !seen[k] && v > 0 {
			extra = append(extra, fmt.Sprintf("%s %d", k, v))
		}
	}
	sort.Strings(extra)
	parts = append(parts, extra...)
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, ", ")
}
