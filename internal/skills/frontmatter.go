package skills

import (
	"bytes"
	"strings"

	"go.yaml.in/yaml/v3"
)

// frontmatter enthält die Felder der SKILL.md, die CCMAS prüft; weitere Schlüssel sind erlaubt.
type frontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

// parseFrontmatter liest den YAML-Block am Anfang einer SKILL.md (zwischen den Zeilen "---").
func parseFrontmatter(data []byte) (frontmatter, error) {
	var fm frontmatter
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	rest, ok := strings.CutPrefix(text, "---\n")
	if !ok {
		return fm, errFrontmatter("Datei beginnt nicht mit einer Zeile \"---\"")
	}
	block, _, found := strings.Cut(rest, "\n---\n")
	if !found {
		// Frontmatter ohne weiteren Inhalt: die schließende Zeile steht am Dateiende.
		block, found = strings.CutSuffix(rest, "\n---")
		if !found {
			return fm, errFrontmatter("schließende Zeile \"---\" fehlt")
		}
	}
	if err := yaml.NewDecoder(bytes.NewReader([]byte(block))).Decode(&fm); err != nil {
		return frontmatter{}, errFrontmatter("kein gültiges YAML: " + err.Error())
	}
	return fm, nil
}

type frontmatterError string

func (e frontmatterError) Error() string { return string(e) }

func errFrontmatter(msg string) error { return frontmatterError(msg) }
