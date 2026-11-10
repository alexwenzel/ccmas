// Package skills verwaltet den führenden CCMAS-Skillbestand: Katalog aus dem
// CCMAS-Softwareordner, rollenbezogene Auswahl und unveränderliche, versionierte
// Skill-Snapshots.
//
// Quelle ist immer der CCMAS-Softwareordner (Wurzel), niemals das Zielprojekt oder das
// Arbeitsverzeichnis: Load liest ausschließlich <wurzel>/.claude/skills/manifest.yaml und
// die dort eingetragenen Skill-Verzeichnisse.
//
// Hash-Definitionen (in Python identisch nachrechenbar):
//
//   - Datei: SHA-256 der rohen Bytes, hexadezimal in Kleinbuchstaben.
//   - tree_sha256 je Skill: SHA-256 über die Zeilen "<datei-sha256>␠␠<relativer Pfad>\n",
//     bytewise (UTF-8) nach Pfad sortiert, Pfade relativ zum Skill-Verzeichnis mit "/".
//     Das entspricht der Ausgabe von "shasum -a 256" über die Dateien.
//   - snapshot_id: "sha256:" + SHA-256 über "ccmas-skill-snapshot v1\n", "role <rolle>\n" und je
//     Skill (nach ID sortiert) "skill <id> <version> <tree_sha256>\n".
//
// Die Datei .DS_Store (macOS) wird im Skill-Verzeichnis ignoriert; Symlinks und andere
// Nicht-Dateien sind Fehler. Das Format des Manifests und der Ablauf bei einer
// Skill-Änderung stehen in docs/architecture/skill-migration.md.
package skills
