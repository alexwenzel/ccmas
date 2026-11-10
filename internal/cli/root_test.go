package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// plannedGroups sind die Gruppen aus „Vollständige CLI-Steuerung“ der Spezifikation, die
// noch nicht implementiert sind. skills ist seit CC-35 verfügbar (siehe skills_test.go),
// profile und agent seit CC-46 (siehe profile_test.go).
var plannedGroups = []string{
	"init", "status", "start", "stop", "ui", "project", "rules",
	"task", "feature", "milestone", "question", "review",
	"merge", "processes", "events", "remote",
}

// availableGroups sind die implementierten Befehle auf der obersten Ebene.
var availableGroups = []string{"version", "skills", "profile", "agent"}

// run führt die CLI mit den Argumenten aus und liefert Exit-Code, stdout und stderr.
func run(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := Execute(args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestRootHelp(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"-h"}, {"help"}, {}} {
		code, out, errOut := run(t, args...)
		if code != ExitOK {
			t.Fatalf("%v: Exit-Code = %d, erwartet %d (stderr: %q)", args, code, ExitOK, errOut)
		}
		for _, g := range plannedGroups {
			if !containsWord(out, g) {
				t.Errorf("%v: Hilfe nennt Gruppe %q nicht:\n%s", args, g, out)
			}
		}
		for _, name := range availableGroups {
			if !containsWord(out, name) {
				t.Errorf("%v: Hilfe nennt %s nicht", args, name)
			}
		}
	}
}

func TestRootHelpMarksPlannedGroups(t *testing.T) {
	_, out, _ := run(t, "--help")
	if !strings.Contains(out, "noch nicht verfügbar") {
		t.Errorf("Hilfe kennzeichnet geplante Gruppen nicht:\n%s", out)
	}
	// version, skills, profile und agent sind implementiert und dürfen nicht als geplant gelten:
	// sie stehen vor dem Abschnitt der geplanten Gruppen.
	planned := strings.Index(out, "Geplante Befehlsgruppen")
	if planned < 0 {
		t.Fatalf("Abschnitt der geplanten Gruppen fehlt:\n%s", out)
	}
	for _, name := range availableGroups {
		i := strings.Index(out, "\n  "+name+" ")
		if i < 0 || i > planned {
			t.Errorf("%s steht nicht im Abschnitt der verfügbaren Befehle:\n%s", name, out)
		}
	}
	for _, g := range plannedGroups {
		if i := strings.Index(out, "\n  "+g+" "); i < planned {
			t.Errorf("geplante Gruppe %s steht nicht im Abschnitt der geplanten Gruppen", g)
		}
	}
}

func TestVersionPlain(t *testing.T) {
	old := Version
	Version = "1.2.3"
	t.Cleanup(func() { Version = old })

	code, out, errOut := run(t, "version")
	if code != ExitOK || errOut != "" {
		t.Fatalf("Exit-Code = %d, stderr = %q", code, errOut)
	}
	if out != "ccmas 1.2.3\n" {
		t.Errorf("stdout = %q, erwartet %q", out, "ccmas 1.2.3\n")
	}
}

func TestVersionDefaultIsDev(t *testing.T) {
	_, out, _ := run(t, "version")
	if out != "ccmas dev\n" {
		t.Errorf("stdout = %q, erwartet %q", out, "ccmas dev\n")
	}
}

func TestVersionJSON(t *testing.T) {
	old := Version
	Version = "1.2.3"
	t.Cleanup(func() { Version = old })

	code, out, _ := run(t, "version", "--json")
	if code != ExitOK {
		t.Fatalf("Exit-Code = %d", code)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("keine gültige JSON-Ausgabe %q: %v", out, err)
	}
	if got["version"] != "1.2.3" {
		t.Errorf("version = %v, erwartet 1.2.3", got["version"])
	}
}

func TestRootVersionFlag(t *testing.T) {
	code, out, _ := run(t, "--version")
	if code != ExitOK || out != "ccmas dev\n" {
		t.Errorf("Exit-Code = %d, stdout = %q", code, out)
	}
}

func TestUsageErrorsExitWithCode2(t *testing.T) {
	cases := map[string][]string{
		"unbekannter Befehl":           {"gibtsnicht"},
		"unbekanntes Root-Flag":        {"--gibtsnicht"},
		"unbekanntes Flag an version":  {"version", "--gibtsnicht"},
		"unerwartetes Argument":        {"version", "extra"},
		"unbekannter Befehl nach Flag": {"--json", "gibtsnicht"},
		"unbekanntes Hilfethema":       {"help", "gibtsnicht"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			code, out, errOut := run(t, args...)
			if code != ExitUsage {
				t.Errorf("Exit-Code = %d, erwartet %d", code, ExitUsage)
			}
			if out != "" {
				t.Errorf("stdout sollte leer sein, war %q", out)
			}
			if strings.TrimSpace(errOut) == "" {
				t.Errorf("stderr enthält keine Meldung")
			}
		})
	}
}

func TestUnknownCommandMessageNamesCommand(t *testing.T) {
	_, _, errOut := run(t, "gibtsnicht")
	if !strings.Contains(errOut, "gibtsnicht") || !strings.Contains(errOut, "--help") {
		t.Errorf("stderr = %q, erwartet Befehlsnamen und Hinweis auf --help", errOut)
	}
}

func TestPlannedGroupsAreNotAvailable(t *testing.T) {
	if ExitNotAvailable == ExitOK || ExitNotAvailable == ExitUsage || ExitNotAvailable == ExitError {
		t.Fatalf("ExitNotAvailable = %d kollidiert mit anderem Exit-Code", ExitNotAvailable)
	}
	for _, g := range plannedGroups {
		t.Run(g, func(t *testing.T) {
			for _, args := range [][]string{{g}, {g, "irgendwas", "--flag"}} {
				code, out, errOut := run(t, args...)
				if code != ExitNotAvailable {
					t.Errorf("%v: Exit-Code = %d, erwartet %d", args, code, ExitNotAvailable)
				}
				if out != "" {
					t.Errorf("%v: stdout sollte leer sein, war %q", args, out)
				}
				if !strings.Contains(errOut, g) || !strings.Contains(errOut, "noch nicht verfügbar") {
					t.Errorf("%v: stderr = %q, erwartet Gruppenname und „noch nicht verfügbar“", args, errOut)
				}
			}
		})
	}
}

func TestPlannedGroupHelpWorks(t *testing.T) {
	code, out, _ := run(t, "status", "--help")
	if code != ExitOK || !strings.Contains(out, "noch nicht verfügbar") {
		t.Errorf("Exit-Code = %d, stdout = %q", code, out)
	}
}

func TestExitCodesAreDistinct(t *testing.T) {
	// Die Werte sind ein dokumentierter Vertrag (docs/architecture/ccmas-design.md).
	want := map[string]int{"ok": 0, "error": 1, "usage": 2, "not-available": 3}
	got := map[string]int{"ok": ExitOK, "error": ExitError, "usage": ExitUsage, "not-available": ExitNotAvailable}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("Exit-Code %s = %d, erwartet %d", k, got[k], v)
		}
	}
}

// containsWord prüft, ob s das Wort w als eigenständiges Token enthält.
func containsWord(s, w string) bool {
	for _, f := range strings.FieldsFunc(s, func(r rune) bool {
		return r == ' ' || r == '\n' || r == '\t' || r == ',' || r == '(' || r == ')'
	}) {
		if f == w {
			return true
		}
	}
	return false
}
