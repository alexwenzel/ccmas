package profiles

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// Dateien im Konfigurationsverzeichnis.
const (
	ModelsFile = "models.yaml"
	AgentsFile = "agents.yaml"
)

// Store liest und schreibt die Konfigurationsdateien in einem Verzeichnis.
//
// Schreiben ist atomar (Temp-Datei im selben Verzeichnis, fsync, rename) und konfliktsicher:
// Der Aufrufer übergibt die Konfiguration mit der Revision, die er gelesen hat. Unter einer
// Sperrdatei (<datei>.lock, exklusiv angelegt) wird die Revision auf der Platte verglichen;
// weicht sie ab, ist das ein Schreibkonflikt (CodeConflict) und nichts wird geschrieben.
// Beim Speichern steigt die Revision um 1. Bricht ein Prozess zwischen Anlegen und Entfernen der
// Sperrdatei ab, bleibt sie liegen: der nächste Schreibversuch meldet einen Konflikt, der die
// Datei nennt, und sie kann nach Prüfung von Hand entfernt werden.
//
// Persistenz in SQLite folgt in M03; dieser Store ist bis dahin der Speicher.
type Store struct{ dir string }

// NewStore liefert einen Store für das Verzeichnis (es wird erst beim Schreiben angelegt).
func NewStore(dir string) *Store { return &Store{dir: dir} }

// Dir liefert das Konfigurationsverzeichnis.
func (s *Store) Dir() string { return s.dir }

func (s *Store) path(name string) string { return filepath.Join(s.dir, name) }

func storageErr(format string, args ...any) *Error {
	err := fmt.Errorf(format, args...)
	return &Error{Code: CodeStorage, Message: err.Error(), cause: errors.Unwrap(err)}
}

func (s *Store) read(name string) ([]byte, error) {
	f, err := os.Open(s.path(name))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, newErr(CodeNotInitialized, "", "%s nicht gefunden in %s: 'ccmas profile init' legt die Startprofile an", name, s.dir)
		}
		return nil, storageErr("%s nicht lesbar: %w", name, err)
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, maxFileSize+1))
	if err != nil {
		return nil, storageErr("%s nicht lesbar: %w", name, err)
	}
	return raw, nil
}

// LoadCapabilities liest und prüft models.yaml.
func (s *Store) LoadCapabilities() (Capabilities, error) {
	raw, err := s.read(ModelsFile)
	if err != nil {
		return Capabilities{}, err
	}
	c, err := ParseCapabilities(raw)
	if err != nil {
		return Capabilities{}, err
	}
	return c, nil
}

// LoadConfig liest und prüft agents.yaml.
func (s *Store) LoadConfig() (Config, error) {
	raw, err := s.read(AgentsFile)
	if err != nil {
		return Config{}, err
	}
	c, err := ParseConfig(raw)
	if err != nil {
		return Config{}, err
	}
	return c, nil
}

// SaveConfig schreibt cfg als neue Revision von agents.yaml. cfg.Revision muss die Revision sein,
// die der Aufrufer gelesen hat (sonst CodeConflict). Zurückgegeben wird die gespeicherte
// Konfiguration mit der erhöhten Revision; cfg selbst bleibt unverändert.
func (s *Store) SaveConfig(cfg Config) (Config, error) {
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	next := cfg.Clone()
	next.Revision = cfg.Revision + 1
	raw, err := next.Marshal()
	if err != nil {
		return Config{}, storageErr("agents.yaml nicht erzeugbar: %w", err)
	}
	err = s.save(AgentsFile, cfg.Revision, raw, func(cur []byte) (int, error) {
		c, err := ParseConfig(cur)
		return c.Revision, err
	})
	if err != nil {
		return Config{}, err
	}
	return next, nil
}

// SaveCapabilities schreibt c als neue Revision von models.yaml (Regeln wie SaveConfig).
func (s *Store) SaveCapabilities(c Capabilities) (Capabilities, error) {
	if err := c.Validate(); err != nil {
		return Capabilities{}, err
	}
	next := c.Clone()
	next.Revision = c.Revision + 1
	raw, err := next.Marshal()
	if err != nil {
		return Capabilities{}, storageErr("models.yaml nicht erzeugbar: %w", err)
	}
	err = s.save(ModelsFile, c.Revision, raw, func(cur []byte) (int, error) {
		cc, err := ParseCapabilities(cur)
		return cc.Revision, err
	})
	if err != nil {
		return Capabilities{}, err
	}
	return next, nil
}

// save ersetzt die Datei atomar, wenn ihre Revision auf der Platte expected entspricht.
func (s *Store) save(name string, expected int, data []byte, revisionOf func([]byte) (int, error)) (err error) {
	lock := s.path(name + ".lock")
	lf, lerr := os.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if lerr != nil {
		if errors.Is(lerr, fs.ErrExist) {
			return newErr(CodeConflict, "", "%s wird gerade geschrieben oder ein früherer Schreibvorgang brach ab: Sperrdatei %s; nach Prüfung entfernen und erneut versuchen", name, lock)
		}
		if errors.Is(lerr, fs.ErrNotExist) {
			return newErr(CodeNotInitialized, "", "%s nicht gefunden in %s: 'ccmas profile init' legt die Startprofile an", name, s.dir)
		}
		return storageErr("Sperrdatei %s nicht anlegbar: %w", lock, lerr)
	}
	lf.Close()
	defer func() {
		if rerr := os.Remove(lock); rerr != nil && err == nil {
			err = storageErr("Sperrdatei %s nicht entfernbar: %w", lock, rerr)
		}
	}()

	cur, err := s.read(name)
	if err != nil {
		return err
	}
	have, err := revisionOf(cur)
	if err != nil {
		return err
	}
	if have != expected {
		return newErr(CodeConflict, "revision", "%s wurde zwischenzeitlich geändert (Revision %d statt %d); nichts geschrieben, bitte neu lesen und wiederholen", name, have, expected)
	}
	return s.writeAtomic(name, data)
}

// writeAtomic schreibt in eine Temp-Datei im selben Verzeichnis und benennt sie um.
func (s *Store) writeAtomic(name string, data []byte) error {
	tmp, err := s.writeTemp(name, data)
	if err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path(name)); err != nil {
		os.Remove(tmp)
		return storageErr("%s nicht ersetzbar: %w", name, err)
	}
	return nil
}

func (s *Store) writeTemp(name string, data []byte) (string, error) {
	f, err := os.CreateTemp(s.dir, name+".tmp-*")
	if err != nil {
		return "", storageErr("temporäre Datei für %s nicht anlegbar: %w", name, err)
	}
	tmp := f.Name()
	fail := func(err error) (string, error) {
		f.Close()
		os.Remove(tmp)
		return "", storageErr("%s nicht schreibbar: %w", name, err)
	}
	if _, err := f.Write(data); err != nil {
		return fail(err)
	}
	if err := f.Chmod(0o600); err != nil {
		return fail(err)
	}
	if err := f.Sync(); err != nil {
		return fail(err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return "", storageErr("%s nicht schreibbar: %w", name, err)
	}
	return tmp, nil
}

// InitResult meldet je Datei, ob Init sie angelegt hat.
type InitResult struct {
	File    string
	Created bool
}

// Init legt models.yaml und agents.yaml aus den übergebenen Beispielinhalten an. Eine vorhandene
// Datei wird nie überschrieben, auch nicht bei gleichzeitigem Aufruf (die Datei entsteht per
// Hardlink einer fertig geschriebenen Temp-Datei und scheitert, wenn sie existiert). Beide
// Inhalte werden vor dem ersten Schreiben geprüft.
func (s *Store) Init(models, agents []byte) ([]InitResult, error) {
	if _, err := ParseCapabilities(models); err != nil {
		return nil, err
	}
	if _, err := ParseConfig(agents); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return nil, storageErr("Konfigurationsverzeichnis %s nicht anlegbar: %w", s.dir, err)
	}
	var out []InitResult
	for _, f := range []struct {
		name string
		data []byte
	}{{ModelsFile, models}, {AgentsFile, agents}} {
		created, err := s.createExclusive(f.name, f.data)
		if err != nil {
			return out, err
		}
		out = append(out, InitResult{File: f.name, Created: created})
	}
	return out, nil
}

func (s *Store) createExclusive(name string, data []byte) (bool, error) {
	tmp, err := s.writeTemp(name, data)
	if err != nil {
		return false, err
	}
	defer os.Remove(tmp)
	if err := os.Link(tmp, s.path(name)); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return false, nil
		}
		return false, storageErr("%s nicht anlegbar: %w", name, err)
	}
	return true, nil
}
