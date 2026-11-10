package cli

import (
	"fmt"
	"path/filepath"
)

// EnvExecutionDir ist die Umgebungsvariable, die den Ausführungsbereich festlegt.
const EnvExecutionDir = "CCMAS_EXECUTION_DIR"

// Der Ausführungsbereich (Spezifikation „Projektbezogene Arbeitsorte“) hält den persistenten Projektzustand und die
// kontrolliert angelegten Agenten-Worktrees. Seine Unterordner sind fest:
//
//	<Ausführungsbereich>/state/ccmas.db   SQLite-Zustand (internal/persistence)
//	<Ausführungsbereich>/run/             Steuer-Socket und Laufzeitdateien des Dienstes (CC-51)
const (
	stateDirName = "state"
	stateDBName  = "ccmas.db"
	runDirName   = "run"
)

// executionDirRel ist der Standardort unter dem CCMAS-Softwareordner (".ccmas/" ist ignoriert).
const executionDirRel = ".ccmas"

// resolveExecutionDir ermittelt den Ausführungsbereich: --execution-dir, dann CCMAS_EXECUTION_DIR, dann
// <CCMAS-Softwareordner>/.ccmas (Softwareordner wie bei „ccmas skills“; --root, CCMAS_HOME oder das Verzeichnis über
// dem Binary). Das Arbeitsverzeichnis wird nie herangezogen. Das Ergebnis ist absolut und bereinigt; der Ordner muss
// nicht existieren. Der Dienst (CC-51) verwendet dieselbe Auflösung.
func resolveExecutionDir(flagDir, flagRoot string) (string, error) {
	dir := flagDir
	if dir == "" {
		dir = lookupEnv(EnvExecutionDir)
	}
	if dir == "" {
		root, err := resolveSoftwareRoot(flagRoot)
		if err != nil {
			return "", fmt.Errorf("Ausführungsbereich nicht bestimmbar: %w; alternativ --execution-dir oder %s angeben", err, EnvExecutionDir)
		}
		dir = filepath.Join(root, executionDirRel)
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("Ausführungsbereich %q nicht auflösbar: %w", dir, err)
	}
	return abs, nil
}

// stateDBPath liefert den festen Ort der SQLite-Datenbank im Ausführungsbereich.
func stateDBPath(executionDir string) string {
	return filepath.Join(executionDir, stateDirName, stateDBName)
}

// runDirPath liefert den festen Ort der Laufzeitdateien des Dienstes (Socket) im Ausführungsbereich.
func runDirPath(executionDir string) string {
	return filepath.Join(executionDir, runDirName)
}
