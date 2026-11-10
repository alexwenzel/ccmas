package runner

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// EnvClaudeBinary ist die Umgebungsvariable, die das Claude-Binary festlegt.
const EnvClaudeBinary = "CCMAS_CLAUDE_BINARY"

// EnvContractsDir nennt dem Runner das Verzeichnis der JSON-Schemas (Vertrag mit contracts.py).
const EnvContractsDir = "CCMAS_CONTRACTS_DIR"

// EnvAllowlist sind die einzigen Variablen des Go-Prozesses, die an den Runner-Prozess gehen.
// Alles andere (insbesondere ANTHROPIC_*, CLAUDE_CODE_*, Tokens und Proxy-Variablen) bleibt draußen.
// Die Liste entspricht LIVE_ENV_ALLOWLIST der Live-Tests des Runners.
var EnvAllowlist = []string{
	"PATH",            // Binary-Suche (uv, node, Shell-Werkzeuge)
	"HOME",            // Konfigurationsverzeichnis der CLI und Schlüsselbund
	"USER", "LOGNAME", // Kontoname der Schlüsselbund-Suche und der Shell
	"SHELL",                   // Shell des Bash-Werkzeugs
	"TMPDIR",                  // temporäres Verzeichnis (macOS, Sandbox)
	"LANG",                    // Zeichenkodierung der Kindprozesse
	"__CF_USER_TEXT_ENCODING", // CoreFoundation unter macOS
}

// Environment baut die Umgebung des Runner-Prozesses: die Allowlist aus getenv plus
// CCMAS_CONTRACTS_DIR (leer: nicht gesetzt). Nichts sonst wird vererbt.
func Environment(getenv func(string) string, contractsDir string) []string {
	if getenv == nil {
		getenv = os.Getenv
	}
	var env []string
	for _, k := range EnvAllowlist {
		if v := getenv(k); v != "" {
			env = append(env, k+"="+v)
		}
	}
	if contractsDir != "" {
		env = append(env, EnvContractsDir+"="+contractsDir)
	}
	return env
}

// ResolveClaudeBinary bestimmt das Claude-Binary: ausdrücklicher Wert (--claude-binary), dann
// CCMAS_CLAUDE_BINARY, dann "claude" im PATH. Das Ergebnis ist ein absoluter Pfad ohne Symlinks
// (realpath) einer ausführbaren Datei. Es gibt keinen festen Pfad im Programm.
func ResolveClaudeBinary(explicit string, getenv func(string) string) (string, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	candidate, source := explicit, "--claude-binary"
	if candidate == "" {
		candidate, source = getenv(EnvClaudeBinary), EnvClaudeBinary
	}
	if candidate == "" {
		p, err := exec.LookPath("claude")
		if err != nil {
			return "", fmt.Errorf("Claude-Binary nicht gefunden: weder --claude-binary noch %s gesetzt und \"claude\" nicht im PATH", EnvClaudeBinary)
		}
		candidate, source = p, "PATH"
	}
	abs, err := filepath.Abs(candidate)
	if err != nil {
		return "", fmt.Errorf("Claude-Binary %q (%s) nicht auflösbar: %w", candidate, source, err)
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("Claude-Binary %q (%s) nicht gefunden: %w", candidate, source, err)
	}
	fi, err := os.Stat(real)
	switch {
	case err != nil:
		return "", fmt.Errorf("Claude-Binary %q (%s) nicht lesbar: %w", real, source, err)
	case !fi.Mode().IsRegular():
		return "", fmt.Errorf("Claude-Binary %q (%s) ist keine Datei", real, source)
	case runtime.GOOS != "windows" && fi.Mode().Perm()&0o111 == 0:
		return "", errors.New("Claude-Binary " + real + " (" + source + ") ist nicht ausführbar")
	}
	return real, nil
}
