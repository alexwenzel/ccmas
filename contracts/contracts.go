// Package contracts bettet die versionierten JSON-Schemas der CCMAS-Verträge in das
// Go-Binary ein. Die Schema-Dateien in diesem Verzeichnis sind die einzige Quelle der
// Wahrheit: Go liest sie über Schemas, der Python-Runner liest dieselben Dateien relativ
// zum Repository-Wurzelverzeichnis. Es gibt keine Kopien.
//
// Typen und Prüflogik stehen in internal/domain; die Regeln stehen in
// docs/architecture/contracts.md.
package contracts

import "embed"

// Schemas enthält alle *.schema.json dieses Verzeichnisses (Draft 2020-12).
//
//go:embed *.schema.json
var Schemas embed.FS

// BaseURL ist das Präfix der $id-Kennungen. Die Kennungen sind reine Namen (.invalid ist
// nach RFC 6761 nicht auflösbar); Schemas werden nie über das Netzwerk geladen.
const BaseURL = "https://ccmas.invalid/contracts/"
