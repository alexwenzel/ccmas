package profiles

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/alexwenzel/ccmas/internal/domain"
)

func seededStore(t *testing.T) *Store {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "cfg")
	caps, _ := testCaps().Marshal()
	cfg, _ := testConfig().Marshal()
	s := NewStore(dir)
	if _, err := s.Init(caps, cfg); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestStoreNotInitialized(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "leer"))
	_, err := s.LoadConfig()
	wantCode(t, err, CodeNotInitialized)
	if !strings.Contains(err.Error(), "profile init") {
		t.Errorf("Meldung nennt den Befehl nicht: %v", err)
	}
	_, err = s.LoadCapabilities()
	wantCode(t, err, CodeNotInitialized)
}

func TestStoreInitCreatesAndNeverOverwrites(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "neu", "cfg")
	s := NewStore(dir)
	caps, _ := testCaps().Marshal()
	cfg, _ := testConfig().Marshal()

	res, err := s.Init(caps, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 2 || !res[0].Created || !res[1].Created || res[0].File != ModelsFile || res[1].File != AgentsFile {
		t.Fatalf("Ergebnis = %+v", res)
	}
	for _, f := range []string{ModelsFile, AgentsFile} {
		fi, err := os.Stat(filepath.Join(dir, f))
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm()&0o077 != 0 {
			t.Errorf("%s ist für andere lesbar: %v", f, fi.Mode())
		}
	}

	// Eine veränderte Datei bleibt unangetastet.
	custom := []byte("# meine Änderung\n" + string(cfg))
	if err := os.WriteFile(filepath.Join(dir, AgentsFile), custom, 0o600); err != nil {
		t.Fatal(err)
	}
	res, err = s.Init(caps, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if res[0].Created || res[1].Created {
		t.Errorf("Init meldet Anlage trotz vorhandener Dateien: %+v", res)
	}
	got, _ := os.ReadFile(filepath.Join(dir, AgentsFile))
	if string(got) != string(custom) {
		t.Error("Init hat eine vorhandene Datei überschrieben")
	}

	// Fehlt nur eine Datei, wird nur diese angelegt.
	if err := os.Remove(filepath.Join(dir, ModelsFile)); err != nil {
		t.Fatal(err)
	}
	res, _ = s.Init(caps, cfg)
	if !res[0].Created || res[1].Created {
		t.Errorf("Ergebnis = %+v", res)
	}
	got, _ = os.ReadFile(filepath.Join(dir, AgentsFile))
	if string(got) != string(custom) {
		t.Error("Init hat die vorhandene agents.yaml verändert")
	}
}

func TestStoreInitRejectsInvalidExamplesBeforeWriting(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cfg")
	s := NewStore(dir)
	cfg, _ := testConfig().Marshal()
	_, err := s.Init([]byte("kaputt: ["), cfg)
	wantCode(t, err, CodeInvalidConfig)
	if _, statErr := os.Stat(dir); !os.IsNotExist(statErr) {
		t.Errorf("trotz Fehler wurde %s angelegt", dir)
	}
}

func TestStoreSaveConfigIncrementsRevision(t *testing.T) {
	s := seededStore(t)
	cfg, err := s.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Revision != 1 {
		t.Fatalf("Revision = %d", cfg.Revision)
	}
	rp := cfg.Roles[domain.RoleReviewer]
	rp.Effort = domain.EffortMedium
	cfg.Roles[domain.RoleReviewer] = rp
	saved, err := s.SaveConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Revision != 2 {
		t.Errorf("gespeicherte Revision = %d, erwartet 2", saved.Revision)
	}
	if cfg.Revision != 1 {
		t.Error("SaveConfig verändert das übergebene Argument")
	}
	reread, err := s.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if reread.Revision != 2 || reread.Roles[domain.RoleReviewer].Effort != domain.EffortMedium {
		t.Errorf("gelesen = %+v", reread)
	}
	// Nach dem Schreiben bleibt weder eine Temp- noch eine Sperrdatei zurück.
	entries, _ := os.ReadDir(s.Dir())
	for _, e := range entries {
		if e.Name() != ModelsFile && e.Name() != AgentsFile {
			t.Errorf("Rest im Konfigurationsverzeichnis: %s", e.Name())
		}
	}
}

func TestStoreDetectsConflict(t *testing.T) {
	s := seededStore(t)
	a, _ := s.LoadConfig()
	b, _ := s.LoadConfig()

	ra := a.Roles[domain.RoleReviewer]
	ra.Effort = domain.EffortMedium
	a.Roles[domain.RoleReviewer] = ra
	if _, err := s.SaveConfig(a); err != nil {
		t.Fatal(err)
	}

	rb := b.Roles[domain.RoleReviewer]
	rb.Effort = domain.EffortLow
	b.Roles[domain.RoleReviewer] = rb
	_, err := s.SaveConfig(b)
	wantCode(t, err, CodeConflict)

	now, _ := s.LoadConfig()
	if now.Revision != 2 || now.Roles[domain.RoleReviewer].Effort != domain.EffortMedium {
		t.Errorf("der Konflikt hat die Datei verändert: %+v", now.Roles[domain.RoleReviewer])
	}
}

func TestStoreConcurrentWritersExactlyOneWins(t *testing.T) {
	s := seededStore(t)
	const n = 8
	cfgs := make([]Config, n)
	for i := range cfgs {
		c, err := s.LoadConfig()
		if err != nil {
			t.Fatal(err)
		}
		cfgs[i] = c
	}
	var wins, conflicts atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c := cfgs[i]
			c.Agents["a"+string(rune('0'+i))] = Agent{Role: domain.RoleReviewer}
			_, err := s.SaveConfig(c)
			switch {
			case err == nil:
				wins.Add(1)
			case errors.Is(err, &Error{Code: CodeConflict}):
				conflicts.Add(1)
			default:
				t.Errorf("unerwarteter Fehler: %v", err)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 || conflicts.Load() != n-1 {
		t.Errorf("Gewinner = %d, Konflikte = %d", wins.Load(), conflicts.Load())
	}
	final, _ := s.LoadConfig()
	if final.Revision != 2 || len(final.Agents) != 1 {
		t.Errorf("Revision %d, Agenten %d: es darf genau ein Schreibvorgang wirken", final.Revision, len(final.Agents))
	}
}

func TestStoreSaveRejectsInvalidConfigWithoutWriting(t *testing.T) {
	s := seededStore(t)
	before, _ := os.ReadFile(filepath.Join(s.Dir(), AgentsFile))
	cfg, _ := s.LoadConfig()
	delete(cfg.Roles, domain.RoleReviewer)
	_, err := s.SaveConfig(cfg)
	wantCode(t, err, CodeInvalidConfig)
	after, _ := os.ReadFile(filepath.Join(s.Dir(), AgentsFile))
	if string(before) != string(after) {
		t.Error("ungültige Konfiguration wurde geschrieben")
	}
}

func TestStoreLeftoverLockBlocksWriting(t *testing.T) {
	s := seededStore(t)
	if err := os.WriteFile(filepath.Join(s.Dir(), AgentsFile+".lock"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, _ := s.LoadConfig()
	_, err := s.SaveConfig(cfg)
	wantCode(t, err, CodeConflict)
	if !strings.Contains(err.Error(), ".lock") {
		t.Errorf("Meldung nennt die Sperrdatei nicht: %v", err)
	}
}

func TestStoreRejectsCorruptFiles(t *testing.T) {
	s := seededStore(t)
	if err := os.WriteFile(filepath.Join(s.Dir(), AgentsFile), []byte("revision: ["), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := s.LoadConfig()
	wantCode(t, err, CodeInvalidConfig)
	if !strings.Contains(err.Error(), AgentsFile) {
		t.Errorf("Meldung nennt die Datei nicht: %v", err)
	}
}

func TestStoreSaveCapabilitiesIncrementsRevisionAndDetectsConflict(t *testing.T) {
	s := seededStore(t)
	a, _ := s.LoadCapabilities()
	b, _ := s.LoadCapabilities()
	saved, err := s.SaveCapabilities(a)
	if err != nil || saved.Revision != 2 {
		t.Fatalf("Revision = %d, Fehler = %v", saved.Revision, err)
	}
	_, err = s.SaveCapabilities(b)
	wantCode(t, err, CodeConflict)
}
