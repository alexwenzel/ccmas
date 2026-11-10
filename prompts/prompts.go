// Package prompts bettet die versionierten Systemprompts der Rollen in das Go-Binary ein. Die Markdown-Dateien in diesem
// Verzeichnis sind die einzige Quelle; es gibt keine Kopien. Ein Prompt beschreibt Rolle, Grenzen und Ausgabepflicht und
// bleibt kurz: die Fachlogik steht in den Skills (.claude/skills/). Der Prompt wird als RunSpec.system_prompt an die
// Sitzung übergeben; Länge und SHA-256 halten die Datensätze je Versuch fest, der Hash ist damit die Version des Stands.
package prompts

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
)

//go:embed supervisor.md
var files embed.FS

// Supervisor liefert den Systemprompt der Rolle supervisor (prompts/supervisor.md).
func Supervisor() string { return mustRead("supervisor.md") }

// SHA256 liefert den Hash eines Prompts in Hexschreibweise (Kleinbuchstaben).
func SHA256(prompt string) string {
	sum := sha256.Sum256([]byte(prompt))
	return hex.EncodeToString(sum[:])
}

func mustRead(name string) string {
	raw, err := files.ReadFile(name)
	if err != nil {
		panic(fmt.Sprintf("prompts: %s nicht eingebettet: %v", name, err)) // Fehler im Build, nicht zur Laufzeit
	}
	return string(raw)
}
