package cli

import (
	"strings"

	"github.com/spf13/cobra"
)

// usageTemplate ist die deutsche Fassung der Cobra-Standardvorlage (cobra v1.10).
// Befehlsgruppen werden mit ihren Titeln aufgeführt.
const usageTemplate = `Aufruf:{{if .Runnable}}
  {{.UseLine}}{{end}}{{if .HasAvailableSubCommands}}
  {{.CommandPath}} [befehl]{{end}}{{if .HasAvailableSubCommands}}{{$cmds := .Commands}}{{range $group := .Groups}}

{{.Title}}{{range $cmds}}{{if (and (eq .GroupID $group.ID) (or .IsAvailableCommand (eq .Name "help")))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{end}}{{if .HasAvailableLocalFlags}}

Optionen:
{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableInheritedFlags}}

Globale Optionen:
{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableSubCommands}}

Mit "{{.CommandPath}} [befehl] --help" gibt es Hilfe zu einem Befehl.{{end}}
`

// localize ersetzt die englischen Standardtexte von Cobra durch deutsche.
func localize(root *cobra.Command) {
	root.SetUsageTemplate(usageTemplate)
	root.InitDefaultHelpFlag()
	root.Flags().Lookup("help").Usage = "Hilfe anzeigen"
	root.InitDefaultVersionFlag()
	root.Flags().Lookup("version").Usage = "Version ausgeben"
	root.SetHelpCommand(&cobra.Command{
		Use:     "help [befehl]",
		Short:   "Hilfe zu einem Befehl anzeigen",
		GroupID: groupAvailable,
		RunE: func(cmd *cobra.Command, args []string) error {
			target, _, err := cmd.Root().Find(args)
			if err != nil || target == nil || (target == cmd.Root() && len(args) > 0) {
				return usageErrorf("unbekanntes Hilfethema %q", strings.Join(args, " "))
			}
			return target.Help()
		},
	})
}

// localizeSubcommandHelp setzt den deutschen Text des Hilfe-Flags an einem Unterbefehl.
func localizeSubcommandHelp(cmd *cobra.Command) {
	cmd.InitDefaultHelpFlag()
	cmd.Flags().Lookup("help").Usage = "Hilfe anzeigen"
}
