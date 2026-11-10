package profiles

import (
	"bytes"
	"errors"
	"fmt"
	"io"

	"go.yaml.in/yaml/v3"
)

// maxFileSize begrenzt die Konfigurationsdateien (sie sind klein; alles andere ist ein Fehler).
const maxFileSize = 1 << 20

// decodeStrict dekodiert genau ein YAML-Dokument; unbekannte und doppelte Schlüssel sowie
// weitere Dokumente sind Fehler.
func decodeStrict(raw []byte, into any) error {
	if len(raw) > maxFileSize {
		return fmt.Errorf("Datei ist mit %d Bytes größer als erlaubt (%d)", len(raw), maxFileSize)
	}
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(into); err != nil {
		if errors.Is(err, io.EOF) {
			return errors.New("Datei ist leer")
		}
		return err
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("Datei enthält mehr als ein YAML-Dokument")
	}
	return nil
}

func encodeYAML(header string, v any) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString(header)
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
