//go:build unix

package cli

import (
	"os"
	"strings"
	"testing"
)

// TestStatusPermissionsFinding: Gruppen- oder Weltrechte an der Datenbankdatei sind ein Befund (Exit 1); status ändert
// die Rechte nicht.
func TestStatusPermissionsFinding(t *testing.T) {
	e := newStatusEnv(t)
	e.seeded()
	if code, _ := e.statusJSON(); code != ExitOK {
		t.Fatalf("Ausgangslage: Exit %d", code)
	}
	must0(t, os.Chmod(e.db(), 0o644))
	code, doc := e.statusJSON()
	if code != ExitError || doc.OK || !doc.hasFinding("insecure_permissions") {
		t.Fatalf("Exit %d, Befunde %+v", code, doc.State.Database.Findings)
	}
	if m := doc.State.Database.Permissions.Database; m.Mode != "0644" || !m.Insecure {
		t.Errorf("Rechte im Bericht: %+v", m)
	}
	if st, _ := os.Stat(e.db()); st.Mode().Perm() != 0o644 {
		t.Errorf("status hat die Rechte geändert: %v", st.Mode().Perm())
	}
	code, out, errOut := e.status()
	if code != ExitError || errOut == "" || !(strings.Contains(out, "insecure_permissions") && strings.Contains(out, "0644")) {
		t.Errorf("Text: Exit %d, stdout:\n%s\nstderr %q", code, out, errOut)
	}
}
