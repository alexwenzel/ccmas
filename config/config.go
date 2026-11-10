// Package config bettet die versionierten Beispielkonfigurationen ein (models.example.yaml,
// agents.example.yaml). "ccmas profile init" legt daraus die Startprofile im
// Konfigurationsverzeichnis an. Die Dateien sind die einzige Quelle: in Go stehen keine
// Modell-IDs.
package config

import (
	"embed"
	"io/fs"
)

//go:embed models.example.yaml agents.example.yaml
var files embed.FS

func mustRead(name string) []byte {
	raw, err := fs.ReadFile(files, name)
	if err != nil {
		panic("config: " + err.Error()) // zur Bauzeit eingebettet; kann nicht fehlen
	}
	return raw
}

// ModelsExample liefert models.example.yaml.
func ModelsExample() []byte { return mustRead("models.example.yaml") }

// AgentsExample liefert agents.example.yaml.
func AgentsExample() []byte { return mustRead("agents.example.yaml") }
