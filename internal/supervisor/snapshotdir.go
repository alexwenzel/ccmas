package supervisor

import (
	"os"
	"path/filepath"

	"github.com/alexwenzel/ccmas/internal/skills"
)

// writeSnapshotDir schreibt einen Skill-Snapshot unter dir (inhaltsadressiert; ein vorhandener Snapshot gleicher Kennung
// wird geprüft und wiederverwendet) und liefert den kanonischen Pfad, ohne Symlinks, wie ihn der Runner erwartet. Das
// Verzeichnis wird bei Bedarf angelegt.
func writeSnapshotDir(s skills.Snapshot, dir string) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", err
	}
	return s.Write(real)
}
