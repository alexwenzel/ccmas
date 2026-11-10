package profiles

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexwenzel/ccmas/internal/domain"
	"github.com/alexwenzel/ccmas/internal/skills"
)

// Test-Modelle: bewusst erfundene IDs. Produktive Modell-IDs stehen nur in config/*.example.yaml.
const (
	mBig   = "model-big"   // alle fünf Stufen
	mMid   = "model-mid"   // low, medium, high (ohne xhigh und max)
	mSmall = "model-small" // low, medium
)

func testCaps() Capabilities {
	return Capabilities{
		Revision:  1,
		Source:    "Testquelle",
		CheckedOn: "2026-11-10",
		Models: map[string][]domain.Effort{
			mBig:   {domain.EffortLow, domain.EffortMedium, domain.EffortHigh, domain.EffortXHigh, domain.EffortMax},
			mMid:   {domain.EffortLow, domain.EffortMedium, domain.EffortHigh},
			mSmall: {domain.EffortLow, domain.EffortMedium},
		},
		Fallbacks: []ApprovedFallback{
			{Model: mMid, Requested: domain.EffortXHigh, Effective: domain.EffortHigh, Source: "Testdoku: xhigh läuft als high"},
		},
		Policies: []string{"readonly", "scoped_write"},
	}
}

// Test-Skills (siehe newTestSkills).
const (
	skKnow   = "ccmas-t-know"   // approved; concept, documentation; Standard für concept
	skDoc    = "ccmas-t-doc"    // approved; documentation; Standard für documentation
	skExtra  = "ccmas-t-extra"  // approved; documentation; kein Standard
	skImpl   = "ccmas-t-impl"   // approved; implementer; Standard für implementer
	skReview = "ccmas-t-review" // approved; reviewer, concept_reviewer; Standard für beide
	skPlan   = "ccmas-t-plan"   // pending_adaptation; supervisor
)

type testSkill struct {
	id, status, adaptation, category string
	roles, defaultFor                []domain.Role
}

// newTestSkills legt einen eigenen Softwareordner mit Test-Skills an (Hashes mit einfachem,
// eigenem Code gerechnet) und lädt ihn. So hängen die Tests nicht vom echten Bestand ab.
func newTestSkills(t *testing.T) *skills.Catalog {
	t.Helper()
	root := t.TempDir()
	list := []testSkill{
		{skKnow, "approved", "", "knowledge", []domain.Role{domain.RoleConcept, domain.RoleDocumentation}, []domain.Role{domain.RoleConcept}},
		{skDoc, "approved", "", "knowledge", []domain.Role{domain.RoleDocumentation}, []domain.Role{domain.RoleDocumentation}},
		{skExtra, "approved", "", "knowledge", []domain.Role{domain.RoleDocumentation}, nil},
		{skImpl, "approved", "", "delivery", []domain.Role{domain.RoleImplementer}, []domain.Role{domain.RoleImplementer}},
		{skReview, "approved", "", "review", []domain.Role{domain.RoleReviewer, domain.RoleConceptReviewer}, []domain.Role{domain.RoleReviewer, domain.RoleConceptReviewer}},
		{skPlan, "pending_adaptation", "wird angepasst", "planning", []domain.Role{domain.RoleSupervisor}, nil},
	}
	var b strings.Builder
	b.WriteString("schema_version: 1\nskills:\n")
	roles := func(rs []domain.Role) string {
		s := make([]string, len(rs))
		for i, r := range rs {
			s[i] = string(r)
		}
		return "[" + strings.Join(s, ", ") + "]"
	}
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
		fileHash := hex.EncodeToString(sum[:])
		tree := sha256.Sum256([]byte(fileHash + "  SKILL.md\n"))
		fmt.Fprintf(&b, "  - id: %s\n    version: 1\n    status: %s\n    adaptation: %q\n    category: %s\n", s.id, s.status, s.adaptation, s.category)
		fmt.Fprintf(&b, "    roles: %s\n    default_for: %s\n    external_writes: []\n    tree_sha256: %s\n", roles(s.roles), roles(s.defaultFor), hex.EncodeToString(tree[:]))
		fmt.Fprintf(&b, "    files:\n      - path: SKILL.md\n        sha256: %s\n", fileHash)
		fmt.Fprintf(&b, "    source:\n      package: Skills/%s.skill\n      package_sha256: %s\n      derived: \"2026-11-09\"\n      changes: Test\n",
			strings.TrimPrefix(s.id, "ccmas-"), strings.Repeat("ab", 32))
	}
	if err := os.WriteFile(filepath.Join(root, ".claude", "skills", "manifest.yaml"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := skills.Load(root)
	if err != nil {
		t.Fatalf("Test-Katalog nicht ladbar: %v", err)
	}
	return c
}

// testConfig deckt alle Rollen ab. Alle Rollen nutzen model-big/high, Implementierung model-mid.
func testConfig() Config {
	cfg := Config{Revision: 1, Roles: map[domain.Role]RoleProfile{}, Agents: map[string]Agent{}}
	for _, r := range domain.Roles() {
		cfg.Roles[r] = RoleProfile{Runtime: domain.RuntimeClaude, Model: mBig, Effort: domain.EffortHigh, PolicyProfile: "readonly"}
	}
	impl := cfg.Roles[domain.RoleImplementer]
	impl.Model, impl.PolicyProfile = mMid, "scoped_write"
	cfg.Roles[domain.RoleImplementer] = impl
	return cfg
}

func newResolver(t *testing.T, cfg Config) *Resolver {
	t.Helper()
	r, err := NewResolver(cfg, newTestSkills(t))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// wantCode verlangt einen *Error mit dem Code, geprüft über errors.Is.
func wantCode(t *testing.T, err error, code Code) {
	t.Helper()
	if err == nil {
		t.Fatalf("kein Fehler, erwartet [%s]", code)
	}
	if !isCode(err, code) {
		t.Fatalf("Fehler = %v, erwartet Code %s", err, code)
	}
	if CodeOf(err) != code {
		t.Fatalf("CodeOf = %q, erwartet %q", CodeOf(err), code)
	}
}
