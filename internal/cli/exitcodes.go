package cli

// Exit-Codes der ccmas-CLI. Sie sind ein dokumentierter Vertrag
// (docs/architecture/ccmas-design.md) und werden ausschließlich hier festgelegt.
const (
	// ExitOK: Befehl erfolgreich ausgeführt (auch --help und version).
	ExitOK = 0
	// ExitError: Laufzeitfehler bei einem gültig aufgerufenen Befehl.
	ExitError = 1
	// ExitUsage: Bedienfehler, z. B. unbekannter Befehl, unbekanntes Flag oder
	// unerwartetes Argument.
	ExitUsage = 2
	// ExitNotAvailable: Der Befehl gehört zum vorgesehenen Befehlsraum, ist aber
	// noch nicht implementiert.
	ExitNotAvailable = 3
)
