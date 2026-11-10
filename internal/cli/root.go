// Package cli enthält die Kommandozeile von ccmas: Befehlsbaum, Ausgabe und die
// zentral festgelegten Exit-Codes.
package cli

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

// Version wird beim Bauen per -ldflags "-X github.com/alexwenzel/ccmas/internal/cli.Version=…"
// gesetzt. Ohne Angabe gilt "dev".
var Version = "dev"

const (
	groupAvailable = "available"
	groupPlanned   = "planned"
)

// Execute führt ccmas mit den Argumenten (ohne Programmnamen) aus und liefert den
// Exit-Code. Fehlermeldungen gehen nach errOut, reguläre Ausgaben nach out.
func Execute(args []string, out, errOut io.Writer) int {
	root := newRootCommand()
	root.SetArgs(args)
	root.SetOut(out)
	root.SetErr(errOut)

	err := root.Execute()
	if err != nil {
		fmt.Fprintf(errOut, "ccmas: %v\n", err)
		if _, ok := err.(*usageError); ok {
			fmt.Fprintln(errOut, "Siehe 'ccmas --help'.")
		}
	}
	return exitCode(err)
}

func newRootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:   "ccmas",
		Short: "Lokale Kommandozentrale für agentengestützte Softwareentwicklung",
		Long: "ccmas ist die lokale Kommandozentrale für die agentengestützte Softwareentwicklung.\n\n" +
			"Verfügbar sind derzeit version, skills (list, show, validate, snapshot) und help. Die übrigen\n" +
			"Befehlsgruppen sind als geplant gekennzeichnet und noch nicht verfügbar; ihr Aufruf endet mit\nExit-Code 3.\n\n" +
			"Exit-Codes: 0 Erfolg, 1 Laufzeitfehler, 2 Bedienfehler, 3 noch nicht verfügbar.",
		Version:       Version,
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          unknownCommandArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
		CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: true},
	}
	root.SetVersionTemplate("ccmas {{.Version}}\n")
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return &usageError{msg: err.Error()}
	})
	root.AddGroup(
		&cobra.Group{ID: groupAvailable, Title: "Verfügbare Befehle:"},
		&cobra.Group{ID: groupPlanned, Title: "Geplante Befehlsgruppen (noch nicht verfügbar):"},
	)
	localize(root)

	root.AddCommand(newVersionCommand(), newSkillsCommand())
	for _, g := range plannedCommandSpecs {
		root.AddCommand(newPlannedCommand(g))
	}
	for _, c := range root.Commands() {
		localizeSubcommandHelp(c)
	}
	return root
}

// unknownCommandArgs meldet unbekannte Befehle als Bedienfehler.
func unknownCommandArgs(_ *cobra.Command, args []string) error {
	if len(args) > 0 {
		return usageErrorf("unbekannter Befehl %q", args[0])
	}
	return nil
}

// noArgs lehnt Argumente ab und meldet sie als Bedienfehler.
func noArgs(cmd *cobra.Command, args []string) error {
	if len(args) > 0 {
		return usageErrorf("unerwartetes Argument %q für %q", args[0], cmd.CommandPath())
	}
	return nil
}
