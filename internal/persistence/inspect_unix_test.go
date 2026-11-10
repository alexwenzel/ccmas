//go:build unix

package persistence

import (
	"os"
	"path/filepath"
	"testing"
)

// TestInspectPermissionFindings: Gruppen- oder Weltrechte an Datei, Verzeichnis, -wal und -shm sind Befunde; Inspect
// ändert die Rechte nicht.
func TestInspectPermissionFindings(t *testing.T) {
	path, _ := builtDB(t)
	dir := filepath.Dir(path)
	rep := inspect(t, path)
	wantNoFindings(t, rep)
	if rep.Permissions.Database.Mode != "0600" || rep.Permissions.Directory.Mode != "0700" || rep.Permissions.Database.Insecure {
		t.Errorf("Rechte des Normalfalls: %+v", rep.Permissions)
	}

	must(t, os.Chmod(path, 0o644))
	rep = inspect(t, path)
	wantFinding(t, rep, FindingInsecurePerms)
	if !rep.Permissions.Database.Insecure || rep.Permissions.Database.Mode != "0644" {
		t.Errorf("Rechte: %+v", rep.Permissions.Database)
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o644 {
		t.Errorf("Inspect hat die Rechte geändert: %v", st.Mode().Perm())
	}
	must(t, os.Chmod(path, 0o600))

	must(t, os.Chmod(dir, 0o755))
	wantFinding(t, inspect(t, path), FindingInsecurePerms)
	must(t, os.Chmod(dir, 0o700))
	wantNoFindings(t, inspect(t, path))
}

func TestInspectPermissionFindingsWALAndSHM(t *testing.T) {
	s := newStore(t)
	seedBase(t, s)
	for _, suf := range []string{"-wal", "-shm"} {
		p := s.Path() + suf
		must(t, os.Chmod(p, 0o666))
		rep := inspect(t, s.Path())
		wantFinding(t, rep, FindingInsecurePerms)
		must(t, os.Chmod(p, 0o600))
	}
	wantNoFindings(t, inspect(t, s.Path()))
}

// TestInspectReadOnlyDirectory: Auch in einem nicht beschreibbaren Verzeichnis liest Inspect (geschlossene Datenbank
// und offener Schreiber) und legt nichts an.
func TestInspectReadOnlyDirectory(t *testing.T) {
	path, w := builtDB(t)
	dir := filepath.Dir(path)
	must(t, os.Chmod(dir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	before := fileSet(t, dir)
	rep := inspect(t, path)
	wantNoFindings(t, rep)
	checkWorldReport(t, rep, w)
	if after := fileSet(t, dir); !equalSets(before, after) {
		t.Errorf("Dateien vorher %v, nachher %v", names(before), names(after))
	}
	must(t, os.Chmod(dir, 0o700))

	// Offener Schreiber, danach Verzeichnis sperren: -wal und -shm existieren schon.
	s := newStore(t)
	seedBase(t, s)
	wdir := filepath.Dir(s.Path())
	must(t, os.Chmod(wdir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(wdir, 0o700) })
	before = fileSet(t, wdir)
	rep = inspect(t, s.Path())
	wantNoFindings(t, rep)
	if rep.Data == nil || rep.Data.Projects[0].StateRevision == 0 {
		t.Errorf("Grunddaten: %+v", rep.Data)
	}
	if after := fileSet(t, wdir); len(after) != len(before) {
		t.Errorf("Dateien vorher %v, nachher %v", names(before), names(after))
	}
}

// TestInspectReadOnlyFile: eine schreibgeschützte Datei (0400) ist lesbar.
func TestInspectReadOnlyFile(t *testing.T) {
	path, w := builtDB(t)
	must(t, os.Chmod(path, 0o400))
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })
	before := fileSet(t, filepath.Dir(path))
	rep := inspect(t, path)
	wantNoFindings(t, rep)
	checkWorldReport(t, rep, w)
	if after := fileSet(t, filepath.Dir(path)); !equalSets(before, after) {
		t.Errorf("Dateien vorher %v, nachher %v", names(before), names(after))
	}
}
