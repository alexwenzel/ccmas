package runner

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexwenzel/ccmas/internal/skills"
)

// Test-Skills des synthetischen Softwareordners: der Test hängt so nicht vom Stand des echten Bestands ab.
const (
	skillImpl = "ccmas-t-impl" // freigegeben; Rolle implementer
	skillDoc  = "ccmas-t-doc"  // freigegeben; Rolle documentation
)

func syntheticCatalog(t *testing.T) *skills.Catalog {
	t.Helper()
	root := t.TempDir()
	type sk struct{ id, roles string }
	list := []sk{{skillImpl, "[implementer]"}, {skillDoc, "[documentation]"}}
	var b strings.Builder
	b.WriteString("schema_version: 1\nskills:\n")
	for _, s := range list {
		content := fmt.Sprintf("---\nname: %s\ndescription: Beschreibung von %s\n---\n\n# %s\n", s.id, s.id, s.id)
		dir := filepath.Join(root, ".claude", "skills", s.id)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256([]byte(content))
		fh := hex.EncodeToString(sum[:])
		tree := sha256.Sum256([]byte(fh + "  SKILL.md\n"))
		fmt.Fprintf(&b, "  - id: %s\n    version: 1\n    status: approved\n    adaptation: \"\"\n    category: knowledge\n    roles: %s\n    default_for: []\n", s.id, s.roles)
		fmt.Fprintf(&b, "    external_writes: []\n    tree_sha256: %s\n    files:\n      - path: SKILL.md\n        sha256: %s\n", hex.EncodeToString(tree[:]), fh)
		fmt.Fprintf(&b, "    source:\n      package: Skills/x.skill\n      package_sha256: %s\n      derived: \"2026-11-09\"\n      changes: Test\n", strings.Repeat("ab", 32))
	}
	if err := os.WriteFile(filepath.Join(root, ".claude", "skills", "manifest.yaml"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	cat, err := skills.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	return cat
}
