package runner

import (
	"bufio"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func fakeExecutable(t *testing.T, dir, name string, mode os.FileMode) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\nexit 0\n"), mode); err != nil {
		t.Fatal(err)
	}
	return p
}

func realpath(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestResolveClaudeBinaryPrecedenceAndRealpath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Ausführbarkeit wird unter Windows nicht über Rechte bestimmt")
	}
	dir := t.TempDir()
	fromFlag := fakeExecutable(t, dir, "claude-flag", 0o755)
	fromEnv := fakeExecutable(t, dir, "claude-env", 0o755)
	pathDir := filepath.Join(dir, "bin")
	if err := os.Mkdir(pathDir, 0o755); err != nil {
		t.Fatal(err)
	}
	target := fakeExecutable(t, dir, "claude-real", 0o755)
	if err := os.Symlink(target, filepath.Join(pathDir, "claude")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", pathDir)
	env := func(k string) string {
		if k == EnvClaudeBinary {
			return fromEnv
		}
		return ""
	}

	if got, err := ResolveClaudeBinary(fromFlag, env); err != nil || got != realpath(t, fromFlag) {
		t.Errorf("Option vor Umgebung: %q, %v", got, err)
	}
	if got, err := ResolveClaudeBinary("", env); err != nil || got != realpath(t, fromEnv) {
		t.Errorf("Umgebung vor PATH: %q, %v", got, err)
	}
	got, err := ResolveClaudeBinary("", func(string) string { return "" })
	if err != nil || got != realpath(t, target) {
		t.Errorf("PATH, Symlink aufgelöst: %q, %v (erwartet %q)", got, err, realpath(t, target))
	}
	if !filepath.IsAbs(got) {
		t.Errorf("nicht absolut: %q", got)
	}
}

func TestResolveClaudeBinaryErrors(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Rechte unter Windows nicht aussagekräftig")
	}
	dir := t.TempDir()
	noExec := fakeExecutable(t, dir, "claude-noexec", 0o644)
	t.Setenv("PATH", t.TempDir()) // leeres Verzeichnis: kein "claude"
	none := func(string) string { return "" }
	for name, call := range map[string]func() (string, error){
		"kein Binary irgendwo": func() (string, error) { return ResolveClaudeBinary("", none) },
		"Pfad fehlt":           func() (string, error) { return ResolveClaudeBinary(filepath.Join(dir, "gibt-es-nicht"), none) },
		"nicht ausführbar":     func() (string, error) { return ResolveClaudeBinary(noExec, none) },
		"Verzeichnis":          func() (string, error) { return ResolveClaudeBinary(dir, none) },
	} {
		t.Run(name, func(t *testing.T) {
			if got, err := call(); err == nil {
				t.Errorf("erwartet: Fehler, erhalten %q", got)
			}
		})
	}
}

func TestDefaultCommandUsesUvWithTheRunnerProject(t *testing.T) {
	bin := t.TempDir()
	uv := fakeExecutable(t, bin, "uv", 0o755)
	t.Setenv("PATH", bin)
	r := New(Config{SoftwareRoot: "/opt/ccmas-test", MaxTurns: 3})
	argv, err := r.command()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{uv, "run", "--frozen", "--project", "/opt/ccmas-test/runners/claude_sdk", "python", "-m", "claude_sdk_runner", "--max-turns", "3"}
	if strings.Join(argv, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("argv = %q\nwant   %q", argv, want)
	}
	argv, _ = New(Config{SoftwareRoot: "/opt/ccmas-test"}).command()
	if slicesContain(argv, "--max-turns") {
		t.Errorf("--max-turns ohne MaxTurns: %q", argv)
	}
	if _, err := New(Config{}).command(); err == nil {
		t.Error("ohne Softwareordner und Befehl muss ein Fehler kommen")
	}
	t.Setenv("PATH", t.TempDir())
	if _, err := r.command(); err == nil || !strings.Contains(err.Error(), "uv") {
		t.Errorf("ohne uv: %v", err)
	}
}

func slicesContain(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func TestReadLine(t *testing.T) {
	big := strings.Repeat("a", 100)
	input := "kurz\r\n" + big + "\n" + "\n" + "gut\n" + strings.Repeat("b", 250) + "\nletzte-ohne-ende"
	br := bufio.NewReaderSize(strings.NewReader(input), 16) // kleiner Puffer: Zeilen über mehrere Lesevorgänge
	type item struct {
		line     string
		oversize bool
	}
	var got []item
	for {
		line, over, err := readLine(br, 100)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, item{string(line), over})
	}
	want := []item{{"kurz", false}, {big, false}, {"", false}, {"gut", false}, {"", true}, {"letzte-ohne-ende", false}}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Zeile %d: %v, erwartet %v", i, got[i], want[i])
		}
	}
}

func TestReadLineOversizeAtEOFAndMemoryBound(t *testing.T) {
	br := bufio.NewReader(bytes.NewReader(bytes.Repeat([]byte("x"), 5000)))
	line, over, err := readLine(br, 100)
	if err != nil || !over || line != nil {
		t.Errorf("übergroße letzte Zeile: %q, %v, %v", line, over, err)
	}
	if _, _, err := readLine(br, 100); err != io.EOF {
		t.Errorf("danach EOF erwartet, erhalten %v", err)
	}
}
