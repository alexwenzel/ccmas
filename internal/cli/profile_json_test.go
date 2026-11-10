package cli

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/alexwenzel/ccmas/internal/domain"
)

// Ergebnisobjekte der ändernden Befehle mit --json (CC-46): stabile, maschinenlesbare Antwort
// statt Fließtext. Fehler bleiben unverändert (stderr, Exit-Code, kein JSON auf stdout).

type fileResult struct {
	File    string `json:"file"`
	Created bool   `json:"created"`
}

func TestProfileInitJSON(t *testing.T) {
	e := newProfileEnv(t)
	var got struct {
		ConfigDir string       `json:"config_dir"`
		Files     []fileResult `json:"files"`
	}
	decode(t, e.ok("profile", "init", "--json"), &got)
	if got.ConfigDir != e.dir || len(got.Files) != 2 ||
		got.Files[0] != (fileResult{"models.yaml", true}) || got.Files[1] != (fileResult{"agents.yaml", true}) {
		t.Fatalf("Ergebnis = %+v", got)
	}
	// Zweiter Aufruf: nichts angelegt, nichts überschrieben, trotzdem gültiges JSON.
	before := e.file("agents.yaml")
	decode(t, e.ok("profile", "init", "--json"), &got)
	if len(got.Files) != 2 || got.Files[0].Created || got.Files[1].Created {
		t.Errorf("zweiter Aufruf = %+v", got)
	}
	if e.file("agents.yaml") != before {
		t.Error("init hat agents.yaml überschrieben")
	}
}

func TestProfileInitJSONFailureHasNoJSON(t *testing.T) {
	e := newProfileEnv(t)
	// Das Konfigurationsverzeichnis ist eine Datei: init muss mit Exit 1 scheitern.
	if err := os.WriteFile(e.dir, []byte("keine Verzeichnis"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := e.run("profile", "init", "--json")
	if code != ExitError || out != "" || errOut == "" {
		t.Errorf("Exit-Code %d, stdout %q, stderr %q", code, out, errOut)
	}
}

func TestProfileSetJSON(t *testing.T) {
	e := newProfileEnv(t).initialized()
	var got struct {
		ConfigDir string          `json:"config_dir"`
		File      string          `json:"file"`
		Role      string          `json:"role"`
		Revision  int             `json:"revision"`
		Changed   []string        `json:"changed"`
		Profile   json.RawMessage `json:"profile"`
	}
	out := e.ok("profile", "set", "reviewer", "--model", "claude-sonnet-5-5", "--effort", "xhigh", "--json")
	decode(t, out, &got)
	if got.ConfigDir != e.dir || got.File != "agents.yaml" || got.Role != "reviewer" || got.Revision != 2 {
		t.Fatalf("Ergebnis = %s", out)
	}
	if strings.Join(got.Changed, ",") != "model,effort" {
		t.Errorf("changed = %v", got.Changed)
	}
	p, err := domain.ParseAgentProfile(got.Profile) // das aufgelöste Profil erfüllt den Vertrag
	if err != nil {
		t.Fatalf("profile verletzt den Vertrag: %v\n%s", err, got.Profile)
	}
	if p.Role != domain.RoleReviewer || p.Model != "claude-sonnet-5-5" || p.Effort != domain.EffortXHigh {
		t.Errorf("Profil = %+v", p)
	}
	// Die Änderung ist gespeichert.
	var list struct {
		Revision int `json:"revision"`
	}
	decode(t, e.ok("profile", "list", "--json"), &list)
	if list.Revision != 2 {
		t.Errorf("Revision nach set = %d", list.Revision)
	}

	// Weitere Felder erscheinen in changed (Reihenfolge fest: model, effort, skills, policy).
	decode(t, e.ok("profile", "set", "documentation", "--policy", "scoped_write", "--clear-skills", "--json"), &got)
	if strings.Join(got.Changed, ",") != "skills,policy" || got.Revision != 3 {
		t.Errorf("changed = %v, Revision %d", got.Changed, got.Revision)
	}
}

func TestProfileSetJSONFailureHasNoJSON(t *testing.T) {
	e := newProfileEnv(t).initialized()
	before := e.file("agents.yaml")
	code, out, errOut := e.run("profile", "set", "reviewer", "--model", "claude-gibts-nicht", "--json")
	wantFail(t, code, errOut, ExitError, "[unknown_model]")
	if out != "" {
		t.Errorf("stdout %q", out)
	}
	code, out, errOut = e.run("profile", "set", "reviewer", "--json") // keine Änderung: Bedienfehler
	wantFail(t, code, errOut, ExitUsage)
	if out != "" {
		t.Errorf("stdout %q", out)
	}
	if e.file("agents.yaml") != before {
		t.Error("agents.yaml wurde trotz Fehler verändert")
	}
}

func TestAgentSetUnsetJSON(t *testing.T) {
	e := newProfileEnv(t).initialized()
	var set struct {
		ConfigDir string `json:"config_dir"`
		File      string `json:"file"`
		Action    string `json:"action"`
		Revision  int    `json:"revision"`
		Agent     struct {
			ID     string `json:"id"`
			Role   string `json:"role"`
			Model  string `json:"model"`
			Effort string `json:"effort"`
		} `json:"agent"`
		Profile json.RawMessage `json:"profile"`
	}
	out := e.ok("agent", "set", "impl-schwer", "--role", "implementer", "--model", "claude-opus-5-5", "--effort", "xhigh", "--json")
	decode(t, out, &set)
	if set.ConfigDir != e.dir || set.File != "agents.yaml" || set.Action != "created" || set.Revision != 2 ||
		set.Agent.ID != "impl-schwer" || set.Agent.Role != "implementer" || set.Agent.Model != "claude-opus-5-5" || set.Agent.Effort != "xhigh" {
		t.Fatalf("Ergebnis = %s", out)
	}
	p, err := domain.ParseAgentProfile(set.Profile)
	if err != nil {
		t.Fatalf("profile verletzt den Vertrag: %v\n%s", err, set.Profile)
	}
	if p.ProfileID != "implementer:impl-schwer" || p.Model != "claude-opus-5-5" {
		t.Errorf("Profil = %+v", p)
	}

	decode(t, e.ok("agent", "set", "impl-schwer", "--role", "implementer", "--effort", "max", "--json"), &set)
	if set.Action != "updated" || set.Revision != 3 || set.Agent.Effort != "max" || set.Agent.Model != "claude-opus-5-5" {
		t.Errorf("zweites set = %+v", set)
	}

	// unset einzelner Werte.
	var unset struct {
		Action   string   `json:"action"`
		Revision int      `json:"revision"`
		Unset    []string `json:"unset"`
		Agent    struct {
			ID     string `json:"id"`
			Model  string `json:"model"`
			Effort string `json:"effort"`
		} `json:"agent"`
		Profile json.RawMessage `json:"profile"`
	}
	decode(t, e.ok("agent", "unset", "impl-schwer", "--effort", "--skills", "--json"), &unset)
	if unset.Action != "updated" || unset.Revision != 4 || strings.Join(unset.Unset, ",") != "effort,skills" ||
		unset.Agent.ID != "impl-schwer" || unset.Agent.Effort != "" || unset.Agent.Model != "claude-opus-5-5" {
		t.Errorf("unset = %+v", unset)
	}
	if _, err := domain.ParseAgentProfile(unset.Profile); err != nil {
		t.Errorf("profile verletzt den Vertrag: %v", err)
	}

	// unset --all: Agent gelöscht, kein aufgelöstes Profil.
	var del map[string]any
	decode(t, e.ok("agent", "unset", "impl-schwer", "--all", "--json"), &del)
	if del["action"] != "deleted" || del["revision"] != float64(5) || del["file"] != "agents.yaml" {
		t.Errorf("delete = %v", del)
	}
	if a, _ := del["agent"].(map[string]any); a["id"] != "impl-schwer" {
		t.Errorf("agent = %v", del["agent"])
	}
	if _, has := del["profile"]; has {
		t.Errorf("delete enthält ein Profil: %v", del)
	}
}

func TestAgentSetUnsetJSONFailureHasNoJSON(t *testing.T) {
	e := newProfileEnv(t).initialized()
	e.ok("agent", "set", "a1", "--role", "reviewer", "--effort", "low")
	before := e.file("agents.yaml")
	for name, tc := range map[string]struct {
		args []string
		code int
		part string
	}{
		"set unbekanntes Modell": {[]string{"set", "a1", "--role", "reviewer", "--model", "claude-gibts-nicht", "--json"}, ExitError, "[unknown_model]"},
		"set Rollenwechsel":      {[]string{"set", "a1", "--role", "implementer", "--json"}, ExitError, "--change-role"},
		"set ohne Rolle":         {[]string{"set", "a1", "--json"}, ExitUsage, "--role"},
		"unset unbekannt":        {[]string{"unset", "gibts-nicht", "--all", "--json"}, ExitError, "[unknown_agent]"},
		"unset ohne Option":      {[]string{"unset", "a1", "--json"}, ExitUsage, "--all"},
	} {
		t.Run(name, func(t *testing.T) {
			code, out, errOut := e.run("agent", tc.args...)
			wantFail(t, code, errOut, tc.code, tc.part)
			if out != "" {
				t.Errorf("stdout %q", out)
			}
			if e.file("agents.yaml") != before {
				t.Error("agents.yaml wurde trotz Fehler verändert")
			}
		})
	}
}

func TestProfileResolveHelpNamesFullPrecedence(t *testing.T) {
	code, out, errOut := run(t, "profile", "resolve", "--help")
	if code != ExitOK {
		t.Fatalf("Exit-Code %d: %s", code, errOut)
	}
	flat := strings.Join(strings.Fields(out), " ")
	want := "Auftragswert > Agentenwert > Wahl des Supervisors > Rollenstandard"
	// Seit CC-14 hat die Wahl des Supervisors eigene Schalter; der Hinweis auf das offene Ticket entfällt.
	if !strings.Contains(flat, want) || !strings.Contains(flat, "--supervisor-model") || strings.Contains(flat, "CC-14") {
		t.Errorf("Hilfetext nennt die Rangfolge nicht vollständig:\n%s", out)
	}
}
