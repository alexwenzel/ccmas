package prompts

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSupervisorPromptIsShortAndStatesRoleLimitsAndOutputDuty(t *testing.T) {
	p := Supervisor()
	// Der Prompt geht als Kommandozeilenargument an die CLI; der Vertrag begrenzt ihn auf 32000 Zeichen (runner-v1).
	if n := utf8.RuneCountInString(p); n == 0 || n > 4000 {
		t.Errorf("Länge %d Zeichen: der Prompt soll kurz sein (Fachlogik steht in den Skills)", n)
	}
	for _, want := range []string{"Supervisor", "Grenzen", "StructuredOutput", "ccmas-supervisor", "ccmas-work-assessment"} {
		if !strings.Contains(p, want) {
			t.Errorf("Prompt nennt %q nicht", want)
		}
	}
	if !strings.HasSuffix(p, "\n") || strings.Contains(p, "\r") {
		t.Error("Prompt endet nicht mit einem Zeilenende oder enthält CR")
	}
}

func TestSHA256IsStableAndSensitive(t *testing.T) {
	a, b := SHA256(Supervisor()), SHA256(Supervisor())
	if a != b || len(a) != 64 {
		t.Fatalf("Hash = %q / %q", a, b)
	}
	if SHA256(Supervisor()+" ") == a {
		t.Error("Hash reagiert nicht auf Änderungen")
	}
	// Bekannter Wert, damit eine andere Hash-Definition auffällt.
	if got := SHA256("abc"); got != "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" {
		t.Errorf("SHA256(abc) = %s", got)
	}
}
