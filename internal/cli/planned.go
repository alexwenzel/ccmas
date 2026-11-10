package cli

import "github.com/spf13/cobra"

// plannedGroup beschreibt eine Gruppe aus „Vollständige CLI-Steuerung“ der
// Spezifikation, die noch nicht implementiert ist.
type plannedGroup struct {
	name  string
	short string
}

// plannedCommandSpecs listet den vorgesehenen Befehlsraum in Spezifikationsreihenfolge.
var plannedCommandSpecs = []plannedGroup{
	{"init", "Lokalen Projektkontext einrichten"},
	{"start", "Dienst starten"},
	{"stop", "Dienst stoppen"},
	{"ui", "Terminaloberfläche öffnen"},
	{"project", "Projektzuordnung, Arbeitsorte und Integrationen verwalten"},
	{"rules", "Regeln prüfen, importieren und zuordnen"},
	{"task", "Einzelne Aufträge planen, starten, pausieren, fortsetzen, abbrechen"},
	{"feature", "Feature-Aufträge planen, starten, pausieren, fortsetzen, abbrechen"},
	{"milestone", "Milestones planen, starten, pausieren, fortsetzen, abbrechen"},
	{"question", "Offene Fragen lesen und beantworten"},
	{"review", "Prüfungen und Befunde verwalten"},
	{"merge", "Integrationsentscheidungen verwalten"},
	{"processes", "Prozessbäume und Metriken beobachten"},
	{"events", "Ereignisse beobachten"},
	{"remote", "Fernsteuerung und deren Verfügbarkeit verwalten"},
}

// newPlannedCommand erzeugt den Platzhalter einer geplanten Gruppe. Der Aufruf
// täuscht kein Verhalten vor, sondern endet mit ExitNotAvailable.
func newPlannedCommand(g plannedGroup) *cobra.Command {
	return &cobra.Command{
		Use:     g.name,
		GroupID: groupPlanned,
		Short:   g.short + "",
		Long: g.short + ".\n\nDiese Befehlsgruppe ist vorgesehen, aber noch nicht verfügbar. " +
			"Ein Aufruf endet mit einer Meldung auf stderr und dem Exit-Code 3.",
		Args:               cobra.ArbitraryArgs,
		FParseErrWhitelist: cobra.FParseErrWhitelist{UnknownFlags: true},
		RunE: func(*cobra.Command, []string) error {
			return &notAvailableError{group: g.name}
		},
	}
}
