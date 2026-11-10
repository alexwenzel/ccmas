package cli

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/alexwenzel/ccmas/internal/domain"
	"github.com/alexwenzel/ccmas/internal/profiles"
)

const groupAgentCommands = "agent-commands"

func newAgentCommand() *cobra.Command {
	var opts profileOpts
	cmd := &cobra.Command{
		Use:     "agent",
		GroupID: groupAvailable,
		Short:   "Individuelle Agenten (eigenes Modell, Effort, Skills) verwalten",
		Long: "Ein individueller Agent gehört zu einer Rolle und überschreibt einzelne Felder des Rollenprofils " +
			"(Modell, Effort, Skills je einzeln) innerhalb der Rollenerlaubnis. Nicht gesetzte Felder erben vom " +
			"Rollenprofil. Ein ausdrücklicher Auftragswert hat Vorrang vor dem Agentenwert. Agenten liegen in " +
			"agents.yaml im Konfigurationsverzeichnis (siehe 'ccmas profile --help').\n\n" +
			"Exit-Codes: 0 Erfolg, 1 Befund oder Laufzeitfehler (auch unzulässige Werte; dann wird nichts " +
			"gespeichert), 2 Bedienfehler.",
		Args: unknownCommandArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	opts.addFlags(cmd)
	cmd.AddGroup(&cobra.Group{ID: groupAgentCommands, Title: "Befehle:"})
	cmd.AddCommand(
		newAgentListCommand(&opts),
		newAgentShowCommand(&opts),
		newAgentSetCommand(&opts),
		newAgentUnsetCommand(&opts),
	)
	for _, c := range cmd.Commands() {
		c.GroupID = groupAgentCommands
		localizeSubcommandHelp(c)
	}
	return cmd
}

// agentView ist die Darstellung eines Agenten in der JSON-Ausgabe. Leere Felder erben vom Rollenprofil.
type agentView struct {
	ID       string        `json:"id"`
	Role     domain.Role   `json:"role"`
	Model    string        `json:"model"`
	Effort   domain.Effort `json:"effort"`
	SkillIDs []string      `json:"skills"`
}

func viewOf(id string, a profiles.Agent) agentView {
	return agentView{ID: id, Role: a.Role, Model: a.Model, Effort: a.Effort, SkillIDs: nonNilList(a.SkillIDs)}
}

func newAgentListCommand(opts *profileOpts) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "Alle individuellen Agenten auflisten",
		Long:  "Listet die in agents.yaml angelegten Agenten. Ein leeres Feld (-) erbt vom Rollenprofil.",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, cfg, err := opts.loadConfigOnly()
			if err != nil {
				return err
			}
			views := make([]agentView, 0, len(cfg.Agents))
			for _, id := range cfg.AgentIDs() {
				views = append(views, viewOf(id, cfg.Agents[id]))
			}
			out := cmd.OutOrStdout()
			if asJSON {
				return writeJSON(out, map[string]any{"config_dir": st.Dir(), "revision": cfg.Revision, "inherit": inheritNote, "agents": views})
			}
			tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "AGENT\tROLLE\tMODELL\tEFFORT\tSKILLS")
			for _, v := range views {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", v.ID, v.Role, orDash(v.Model), orDash(string(v.Effort)), joinStrings(v.SkillIDs))
			}
			if err := tw.Flush(); err != nil {
				return err
			}
			if len(views) == 0 {
				fmt.Fprintln(out, "(keine individuellen Agenten angelegt)")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Ausgabe als JSON-Objekt")
	return cmd
}

func newAgentShowCommand(opts *profileOpts) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "show <id>",
		Short: "Einen individuellen Agenten anzeigen",
		Long:  "Zeigt die individuellen Werte eines Agenten. Das aufgelöste Profil zeigt 'ccmas profile resolve --role <rolle> --agent <id>'.",
		Args:  exactArgs(1, "eine Agenten-ID"),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, cfg, err := opts.loadConfigOnly()
			if err != nil {
				return err
			}
			a, ok := cfg.Agents[args[0]]
			if !ok {
				return codedProfileError(&profiles.Error{Code: profiles.CodeUnknownAgent, Field: "agent",
					Message: fmt.Sprintf("Agent %q ist nicht angelegt (ccmas agent list zeigt die vorhandenen)", args[0])})
			}
			v := viewOf(args[0], a)
			out := cmd.OutOrStdout()
			if asJSON {
				return writeJSON(out, map[string]any{"config_dir": st.Dir(), "revision": cfg.Revision, "inherit": inheritNote, "agent": v})
			}
			fmt.Fprintf(out, "Agent:   %s\n", v.ID)
			fmt.Fprintf(out, "Rolle:   %s\n", v.Role)
			fmt.Fprintf(out, "Modell:  %s\n", orInherit(v.Model))
			fmt.Fprintf(out, "Effort:  %s\n", orInherit(string(v.Effort)))
			fmt.Fprintf(out, "Skills:  %s\n", orInherit(strings.Join(v.SkillIDs, ",")))
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Ausgabe als JSON-Objekt")
	return cmd
}

func orInherit(s string) string {
	if s == "" {
		return "(erbt vom Rollenprofil)"
	}
	return s
}

func newAgentSetCommand(opts *profileOpts) *cobra.Command {
	var (
		role, model, effort string
		skillIDs            []string
		changeRole          bool
	)
	cmd := &cobra.Command{
		Use:   "set <id> --role <rolle> [--model M] [--effort E] [--skill ID …]",
		Short: "Einen individuellen Agenten anlegen oder ändern",
		Long: "Legt den Agenten an oder ändert ihn. --role ist immer anzugeben. Nur die genannten Felder werden gesetzt; " +
			"alle übrigen bleiben unverändert (bei einem neuen Agenten erben sie vom Rollenprofil). --skill ersetzt die " +
			"Skill-Liste des Agenten (wiederholbar). Vor dem Speichern wird der Agent wie bei einer Ausführung aufgelöst " +
			"und geprüft (Rollenerlaubnis, bekanntes Modell, unterstützter Effort oder genehmigter Fallback, freigegebene " +
			"und für die Rolle erlaubte Skills). Eine unzulässige Kombination endet mit Exit-Code 1; es wird nichts " +
			"geschrieben. Gehört ein vorhandener Agent zu einer anderen Rolle, wechselt die Rolle nur mit --change-role; " +
			"sonst endet der Befehl mit Exit-Code 1. Zum Entfernen einzelner Werte dient 'agent unset'.",
		Args: exactArgs(1, "eine Agenten-ID"),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			f := cmd.Flags()
			if role == "" {
				return usageErrorf("--role ist erforderlich")
			}
			r, err := parseRole(role)
			if err != nil {
				return err
			}
			if f.Changed("model") && strings.TrimSpace(model) == "" {
				return usageErrorf("--model braucht eine Modell-ID")
			}
			var eff domain.Effort
			if f.Changed("effort") {
				if eff, err = parseEffort(effort); err != nil {
					return err
				}
			}
			ids, err := skillValues(skillIDs)
			if err != nil {
				return err
			}

			s, err := opts.loadSession()
			if err != nil {
				return err
			}
			next := s.cfg.Clone()
			a, exists := next.Agents[id]
			if exists && a.Role != r && !changeRole {
				return codedProfileError(&profiles.Error{Code: profiles.CodeAgentRoleMismatch, Field: "role",
					Message: fmt.Sprintf("Agent %q gehört zur Rolle %s, nicht zu %s; zum Wechseln der Rolle --change-role angeben", id, a.Role, r)})
			}
			a.Role = r
			if f.Changed("model") {
				a.Model = model
			}
			if f.Changed("effort") {
				a.Effort = eff
			}
			if f.Changed("skill") {
				a.SkillIDs = ids
			}
			next.Agents[id] = a
			resolved, err := s.check(next, r, id)
			if err != nil {
				return err
			}
			saved, err := s.store.SaveConfig(next)
			if err != nil {
				return codedProfileError(err)
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Agent %s gespeichert (Rolle %s, %s, Revision %d)\n", id, r, profiles.AgentsFile, saved.Revision)
			printResolved(out, resolved)
			fmt.Fprintln(out, "Die neuen Werte gelten nur für neue Ausführungen.")
			return nil
		},
	}
	cmd.Flags().StringVar(&role, "role", "", "Rolle des Agenten (erforderlich)")
	cmd.Flags().StringVar(&model, "model", "", "Modell-ID (muss in models.yaml konfiguriert sein)")
	cmd.Flags().StringVar(&effort, "effort", "", "Effort-Stufe: low, medium, high, xhigh oder max")
	cmd.Flags().StringArrayVar(&skillIDs, "skill", nil, "Skill-ID (wiederholbar); ersetzt die bisherige Skill-Liste des Agenten")
	cmd.Flags().BoolVar(&changeRole, "change-role", false, "die Rolle eines vorhandenen Agenten wechseln (sonst Fehler)")
	return cmd
}

func newAgentUnsetCommand(opts *profileOpts) *cobra.Command {
	var model, effort, skillsFlag, all bool
	cmd := &cobra.Command{
		Use:   "unset <id> (--model | --effort | --skills | --all)",
		Short: "Einzelne Werte eines Agenten entfernen oder den Agenten löschen",
		Long: "Entfernt die genannten individuellen Werte; das Feld erbt danach wieder vom Rollenprofil. Mit --all wird " +
			"der Agent vollständig gelöscht (nicht mit den anderen Optionen kombinierbar). Es ist mindestens eine Option " +
			"anzugeben. Laufende Ausführungen behalten ihr Profil.",
		Args: exactArgs(1, "eine Agenten-ID"),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			if !model && !effort && !skillsFlag && !all {
				return usageErrorf("%s braucht --model, --effort, --skills oder --all", cmd.CommandPath())
			}
			if all && (model || effort || skillsFlag) {
				return usageErrorf("--all löscht den ganzen Agenten und lässt sich nicht mit --model, --effort oder --skills kombinieren")
			}
			s, err := opts.loadSession()
			if err != nil {
				return err
			}
			a, ok := s.cfg.Agents[id]
			if !ok {
				return codedProfileError(&profiles.Error{Code: profiles.CodeUnknownAgent, Field: "agent",
					Message: fmt.Sprintf("Agent %q ist nicht angelegt (ccmas agent list zeigt die vorhandenen)", id)})
			}
			next := s.cfg.Clone()
			out := cmd.OutOrStdout()
			if all {
				delete(next.Agents, id)
				saved, err := s.store.SaveConfig(next)
				if err != nil {
					return codedProfileError(err)
				}
				fmt.Fprintf(out, "Agent %s gelöscht (%s, Revision %d)\n", id, profiles.AgentsFile, saved.Revision)
				return nil
			}
			if model {
				a.Model = ""
			}
			if effort {
				a.Effort = ""
			}
			if skillsFlag {
				a.SkillIDs = nil
			}
			next.Agents[id] = a
			resolved, err := s.check(next, a.Role, id)
			if err != nil {
				return err
			}
			saved, err := s.store.SaveConfig(next)
			if err != nil {
				return codedProfileError(err)
			}
			fmt.Fprintf(out, "Agent %s aktualisiert (%s, Revision %d)\n", id, profiles.AgentsFile, saved.Revision)
			printResolved(out, resolved)
			fmt.Fprintln(out, "Die neuen Werte gelten nur für neue Ausführungen.")
			return nil
		},
	}
	cmd.Flags().BoolVar(&model, "model", false, "individuelles Modell entfernen")
	cmd.Flags().BoolVar(&effort, "effort", false, "individuellen Effort entfernen")
	cmd.Flags().BoolVar(&skillsFlag, "skills", false, "individuelle Skill-Liste entfernen")
	cmd.Flags().BoolVar(&all, "all", false, "den Agenten vollständig löschen")
	return cmd
}
