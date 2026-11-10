//go:build unix

package persistence

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// TestFilePermissions: Verzeichnis 0700, Datei 0600, -wal/-shm nicht weltlesbar, unabhängig von der umask.
func TestFilePermissions(t *testing.T) {
	old := syscall.Umask(0) // schärfster Fall: die umask nimmt nichts weg
	defer syscall.Umask(old)

	dir := filepath.Join(t.TempDir(), "neu", "state")
	path := filepath.Join(dir, "ccmas.db")
	s := openStore(t, path)
	must(t, s.Migrate(ctxT(t)))
	mustUpdate(t, s, func(tx *Tx) error { _, err := tx.CreateProject(fxProject(t)); return err })

	check := func(p string, want os.FileMode) {
		t.Helper()
		st, err := os.Stat(p)
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		if got := st.Mode().Perm(); got != want {
			t.Errorf("%s hat Rechte %o, erwartet %o", p, got, want)
		}
	}
	check(dir, 0o700)
	check(filepath.Dir(dir), 0o700) // auch die neu angelegten Eltern
	check(path, 0o600)
	for _, suffix := range []string{"-wal", "-shm"} {
		if st, err := os.Stat(path + suffix); err == nil && st.Mode().Perm()&0o077 != 0 {
			t.Errorf("%s ist für Gruppe oder andere zugänglich: %o", path+suffix, st.Mode().Perm())
		} else if err != nil {
			t.Logf("%s existiert nicht (nach Checkpoint)", suffix)
		}
	}
}

// TestExistingFileKeepsItsMode: Open ändert die Rechte einer vorhandenen Datei nicht stillschweigend; die
// Prüfung bestehender Datenbanken ist Sache von W27.2.
func TestExistingFileKeepsItsMode(t *testing.T) {
	path := dbPath(t)
	s := openStore(t, path)
	must(t, s.Close())
	must(t, os.Chmod(path, 0o640))
	s2 := openStore(t, path)
	_ = s2
	st, err := os.Stat(path)
	must(t, err)
	if st.Mode().Perm() != 0o640 {
		t.Fatalf("Rechte = %o", st.Mode().Perm())
	}
}
