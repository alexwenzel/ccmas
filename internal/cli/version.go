package cli

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

func newVersionCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:     "version",
		GroupID: groupAvailable,
		Short:   "Version von ccmas ausgeben",
		Args:    noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if asJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]string{"version": Version})
			}
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "ccmas %s\n", Version)
			return err
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Ausgabe als JSON-Objekt")
	return cmd
}
