package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/alexwenzel/ccmas/internal/domain"
	"github.com/alexwenzel/ccmas/internal/skills"
)

// EnvHome ist die Umgebungsvariable, die den CCMAS-Softwareordner festlegt.
const EnvHome = "CCMAS_HOME"

const groupSkillCommands = "skills-commands"

// Für Tests austauschbar: Ort des Binaries und Umgebung.
var (
	executablePath = os.Executable
	lookupEnv      = os.Getenv
)

// resolveSoftwareRoot ermittelt den CCMAS-Softwareordner, aus dem Skills gelesen werden:
// --root, dann CCMAS_HOME, dann das Verzeichnis über dem Binary (nur wenn dort
// .claude/skills/manifest.yaml liegt). Das Arbeitsverzeichnis und Zielprojekte werden nie
// herangezogen.
func resolveSoftwareRoot(flagRoot string) (string, error) {
	if flagRoot != "" {
		return flagRoot, nil
	}
	if env := lookupEnv(EnvHome); env != "" {
		return env, nil
	}
	if exe, err := executablePath(); err == nil {
		if real, err := filepath.EvalSymlinks(exe); err == nil {
			exe = real
		}
		candidate := filepath.Join(filepath.Dir(exe), "..")
		if fi, err := os.Stat(filepath.Join(candidate, ".claude", "skills", "manifest.yaml")); err == nil && fi.Mode().IsRegular() {
			return filepath.Clean(candidate), nil
		}
	}
	return "", fmt.Errorf("CCMAS-Softwareordner nicht gefunden: --root angeben oder %s setzen "+
		"(das Arbeitsverzeichnis wird bewusst nicht verwendet)", EnvHome)
}

// exactArgs verlangt genau n Argumente und meldet Abweichungen als Bedienfehler.
func exactArgs(n int, names string) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		switch {
		case len(args) < n:
			return usageErrorf("%s erwartet %s", cmd.CommandPath(), names)
		case len(args) > n:
			return usageErrorf("unerwartetes Argument %q für %q", args[n], cmd.CommandPath())
		}
		return nil
	}
}

func parseRole(s string) (domain.Role, error) {
	r := domain.Role(s)
	if !r.Valid() {
		names := make([]string, 0, len(domain.Roles()))
		for _, v := range domain.Roles() {
			names = append(names, string(v))
		}
		return "", usageErrorf("unbekannte Rolle %q (erlaubt: %s)", s, strings.Join(names, ", "))
	}
	return r, nil
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func join[T ~string](in []T) string {
	if len(in) == 0 {
		return "-"
	}
	parts := make([]string, len(in))
	for i, v := range in {
		parts[i] = string(v)
	}
	return strings.Join(parts, ",")
}

// skillSummary ist die Kurzfassung eines Skills für „skills list“.
type skillSummary struct {
	ID         string          `json:"id"`
	Version    int             `json:"version"`
	Status     skills.Status   `json:"status"`
	Adaptation string          `json:"adaptation,omitempty"`
	Category   skills.Category `json:"category"`
	Roles      []domain.Role   `json:"roles"`
	DefaultFor []domain.Role   `json:"default_for"`
	TreeSHA256 string          `json:"tree_sha256"`
}

func newSkillsCommand() *cobra.Command {
	var root string
	cmd := &cobra.Command{
		Use:     "skills",
		GroupID: groupAvailable,
		Short:   "Skill-Bestand prüfen, anzeigen und als Snapshot bereitstellen",
		Long: "Der führende Skill-Bestand liegt im CCMAS-Softwareordner unter .claude/skills/ " +
			"(Manifest: .claude/skills/manifest.yaml). Skills des Zielprojekts oder des Arbeitsverzeichnisses " +
			"werden nie gelesen.\n\n" +
			"Der Softwareordner ergibt sich aus --root, sonst aus " + EnvHome + ", sonst aus dem Verzeichnis " +
			"über dem Binary (wenn dort .claude/skills/manifest.yaml liegt).\n\n" +
			"Exit-Codes: 0 Erfolg, 1 Befund oder Laufzeitfehler, 2 Bedienfehler.",
		Args: unknownCommandArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	cmd.PersistentFlags().StringVar(&root, "root", "", "CCMAS-Softwareordner (sonst "+EnvHome+", sonst Verzeichnis über dem Binary)")
	cmd.AddGroup(&cobra.Group{ID: groupSkillCommands, Title: "Befehle:"})
	cmd.AddCommand(
		newSkillsListCommand(&root),
		newSkillsShowCommand(&root),
		newSkillsValidateCommand(&root),
		newSkillsSnapshotCommand(&root),
	)
	for _, c := range cmd.Commands() {
		c.GroupID = groupSkillCommands
		localizeSubcommandHelp(c)
	}
	return cmd
}

// codedError zeigt Fehler des Skill-Pakets mit ihrem stabilen Code; Exit-Code ExitError.
type codedError struct{ lines []string }

func (e *codedError) Error() string { return strings.Join(e.lines, "\n") }

// withCodes ergänzt jede Fehlermeldung des Skill-Pakets um den Code in eckigen Klammern.
// Andere Fehler bleiben unverändert.
func withCodes(err error) error {
	findings := skills.FindingsOf(err)
	if err == nil || len(findings) == 0 {
		return err
	}
	lines := make([]string, len(findings))
	for i, f := range findings {
		lines[i] = fmt.Sprintf("%s [%s]", f.Message, f.Code)
	}
	return &codedError{lines: lines}
}

// loadCatalog löst den Softwareordner auf und lädt den Katalog.
func loadCatalog(flagRoot string) (*skills.Catalog, error) {
	root, err := resolveSoftwareRoot(flagRoot)
	if err != nil {
		return nil, err
	}
	c, err := skills.Load(root)
	return c, withCodes(err)
}

func newSkillsListCommand(root *string) *cobra.Command {
	var (
		asJSON bool
		role   string
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "Skills des Bestands auflisten",
		Long:  "Listet alle Skills des Bestands mit Status, Kategorie, erlaubten Rollen und Standardausstattung. Mit --role nur die Skills, die die Rolle erhalten darf.",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var filter domain.Role
			if role != "" {
				r, err := parseRole(role)
				if err != nil {
					return err
				}
				filter = r
			}
			c, err := loadCatalog(*root)
			if err != nil {
				return err
			}
			list := c.Skills()
			if filter != "" {
				list = c.ForRole(filter)
			}
			sum := make([]skillSummary, len(list))
			for i, s := range list {
				sum[i] = skillSummary{
					ID: s.ID, Version: s.Version, Status: s.Status, Adaptation: s.Adaptation, Category: s.Category,
					Roles: s.Roles, DefaultFor: s.DefaultFor, TreeSHA256: s.TreeSHA256,
				}
			}
			out := cmd.OutOrStdout()
			if asJSON {
				return writeJSON(out, map[string]any{"root": c.Root(), "role": string(filter), "skills": sum})
			}
			tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "ID\tVERSION\tSTATUS\tKATEGORIE\tROLLEN\tSTANDARD FÜR")
			for _, s := range sum {
				fmt.Fprintf(tw, "%s\t%d\t%s\t%s\t%s\t%s\n", s.ID, s.Version, s.Status, s.Category, join(s.Roles), join(s.DefaultFor))
			}
			return tw.Flush()
		},
	}
	cmd.Flags().StringVar(&role, "role", "", "nur Skills, die diese Rolle erhalten darf")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Ausgabe als JSON-Objekt")
	return cmd
}

func newSkillsShowCommand(root *string) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "show <skill-id>",
		Short: "Einen Skill mit Herkunft, Dateien und Prüfsummen anzeigen",
		Args:  exactArgs(1, "eine Skill-ID"),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := loadCatalog(*root)
			if err != nil {
				return err
			}
			s, ok := c.Skill(args[0])
			if !ok {
				return withCodes(&skills.Error{Code: skills.CodeUnknownSkill, Skill: args[0],
					Message: fmt.Sprintf("Skill %s ist im Manifest nicht bekannt", args[0])})
			}
			out := cmd.OutOrStdout()
			if asJSON {
				return writeJSON(out, s)
			}
			fmt.Fprintf(out, "ID:             %s\n", s.ID)
			fmt.Fprintf(out, "Version:        %d\n", s.Version)
			fmt.Fprintf(out, "Status:         %s\n", s.Status)
			if s.Adaptation != "" {
				fmt.Fprintf(out, "Anpassung:      %s\n", s.Adaptation)
			}
			fmt.Fprintf(out, "Kategorie:      %s\n", s.Category)
			fmt.Fprintf(out, "Rollen:         %s\n", join(s.Roles))
			fmt.Fprintf(out, "Standard für:   %s\n", join(s.DefaultFor))
			fmt.Fprintf(out, "Externe Schreibzugriffe (Inventar, keine Berechtigung): %s\n", join(s.ExternalWrites))
			fmt.Fprintf(out, "Beschreibung:   %s\n", s.Description)
			if s.Source.Package == skills.NoPackage {
				fmt.Fprintln(out, "Herkunft:       kein Originalpaket (neu in CCMAS)")
			} else {
				fmt.Fprintf(out, "Herkunft:       %s (sha256 %s)\n", s.Source.Package, s.Source.PackageSHA256)
			}
			fmt.Fprintf(out, "Abgeleitet:     %s – %s\n", s.Source.Derived, s.Source.Changes)
			fmt.Fprintf(out, "tree_sha256:    %s\n", s.TreeSHA256)
			fmt.Fprintln(out, "Dateien:")
			for _, f := range s.Files {
				fmt.Fprintf(out, "  %s  %s\n", f.SHA256, f.Path)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Ausgabe als JSON-Objekt")
	return cmd
}

// findingsError meldet Befunde einer Prüfung; die Befunde selbst sind schon ausgegeben (Exit-Code 1).
type findingsError struct{ n int }

func (e *findingsError) Error() string {
	return fmt.Sprintf("%d Befund(e) im Skill-Bestand", e.n)
}

func newSkillsValidateCommand(root *string) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Manifest, Skill-Dateien, Workflow-Verträge und Rollenregeln prüfen",
		Long: "Prüft das Manifest und den Bestand: eindeutige IDs, Frontmatter, Dateien und Prüfsummen, " +
			"nicht registrierte ccmas-Verzeichnisse, die Rollenregeln (rules/roles.yaml) und den Workflow-Vertrag " +
			"(contract.yaml) jedes Workflow-Skills. Alle Befunde werden ausgegeben; " +
			"bei Befunden endet der Befehl mit Exit-Code 1.",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			rootDir, err := resolveSoftwareRoot(*root)
			if err != nil {
				return err
			}
			c, loadErr := skills.Load(rootDir)
			findings := skills.FindingsOf(loadErr)
			if loadErr != nil && len(findings) == 0 {
				return loadErr
			}
			out := cmd.OutOrStdout()
			if asJSON {
				report := map[string]any{"ok": loadErr == nil, "root": rootDir, "findings": nonNilFindings(findings)}
				if c != nil {
					approved := 0
					for _, s := range c.Skills() {
						if s.Status == skills.StatusApproved {
							approved++
						}
					}
					report["skills"] = len(c.Skills())
					report["approved"] = approved
					report["pending_adaptation"] = len(c.Skills()) - approved
					report["workflow_contracts"] = len(c.Contracts())
				}
				if err := writeJSON(out, report); err != nil {
					return err
				}
			} else if loadErr == nil {
				approved := 0
				for _, s := range c.Skills() {
					if s.Status == skills.StatusApproved {
						approved++
					}
				}
				fmt.Fprintf(out, "Skill-Bestand in %s ist gültig: %d Skills (%d approved, %d pending_adaptation), %d Workflow-Verträge, Rollenregeln rules/roles.yaml\n",
					c.Root(), len(c.Skills()), approved, len(c.Skills())-approved, len(c.Contracts()))
			} else {
				errOut := cmd.ErrOrStderr()
				for _, f := range findings {
					fmt.Fprintf(errOut, "  [%s] %s\n", f.Code, f.Message)
				}
			}
			if loadErr != nil {
				return &findingsError{n: len(findings)}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Ausgabe als JSON-Objekt (bei Befunden zusätzlich Exit-Code 1)")
	return cmd
}

func nonNilFindings(f []*skills.Error) []*skills.Error {
	if f == nil {
		return []*skills.Error{}
	}
	return f
}

func newSkillsSnapshotCommand(root *string) *cobra.Command {
	var (
		asJSON bool
		role   string
		ids    []string
		outDir string
	)
	cmd := &cobra.Command{
		Use:   "snapshot --role <rolle> --out <verzeichnis> [--skill <id> …]",
		Short: "Unveränderlichen Skill-Snapshot für eine Rolle schreiben",
		Long: "Löst die Skills einer Rolle auf und schreibt sie als unveränderlichen Snapshot nach " +
			"<verzeichnis>/<snapshot_id ohne sha256:>/ (Plugin-kompatibles Layout). Ohne --skill gilt die " +
			"Standardausstattung der Rolle, sonst genau die genannten Skills. Nur freigegebene (approved) Skills, " +
			"die die Rolle erlaubt, werden aufgenommen. Ein vorhandener gleicher Snapshot wird geprüft und " +
			"wiederverwendet, nie überschrieben. Ausgegeben werden snapshot_id und Pfad.",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if role == "" {
				return usageErrorf("--role ist erforderlich")
			}
			if outDir == "" {
				return usageErrorf("--out ist erforderlich")
			}
			r, err := parseRole(role)
			if err != nil {
				return err
			}
			c, err := loadCatalog(*root)
			if err != nil {
				return err
			}
			snap, err := c.Resolve(r, ids)
			if err != nil {
				return withCodes(err)
			}
			path, err := snap.Write(outDir)
			if err != nil {
				return withCodes(err)
			}
			out := cmd.OutOrStdout()
			if asJSON {
				doc := snap.Document()
				type skillRef struct {
					ID         string `json:"id"`
					Version    int    `json:"version"`
					TreeSHA256 string `json:"tree_sha256"`
				}
				refs := make([]skillRef, len(doc.Skills))
				for i, s := range doc.Skills {
					refs[i] = skillRef{s.ID, s.Version, s.TreeSHA256}
				}
				return writeJSON(out, map[string]any{
					"snapshot_id": snap.ID(), "path": path, "role": string(r), "skills": refs,
				})
			}
			fmt.Fprintf(out, "snapshot_id: %s\npfad: %s\n", snap.ID(), path)
			return nil
		},
	}
	cmd.Flags().StringVar(&role, "role", "", "Rolle, für die aufgelöst wird (erforderlich)")
	cmd.Flags().StringArrayVar(&ids, "skill", nil, "Skill-ID (wiederholbar); ohne Angabe gilt die Standardausstattung der Rolle")
	cmd.Flags().StringVar(&outDir, "out", "", "Zielverzeichnis, in dem der Snapshot-Ordner angelegt wird (erforderlich)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Ausgabe als JSON-Objekt")
	return cmd
}
