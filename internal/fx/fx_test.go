package fx

import (
	"strings"
	"testing"
	"time"
)

func TestNoEscapeCodesWithoutTerminal(t *testing.T) {
	var out strings.Builder
	Type(&out, Red, "Hallo")
	Spin(&out, "warte", []string{"|"}, time.Hour) // würde ewig laufen, wenn es nicht no-op wäre
	Confetti(&out)
	out.WriteString(Paint(&out, Red, "rot"))
	out.WriteString(Rainbow(&out, "bunt", 0))
	out.WriteString(Banner(&out))
	out.WriteString(Grid(&out, [3][3]string{{"FREI"}}))

	if strings.Contains(out.String(), "\x1b") {
		t.Errorf("Escape-Code in Nicht-Terminal-Ausgabe: %q", out.String())
	}
	if !strings.HasPrefix(out.String(), "Hallorotbunt") {
		t.Errorf("Ausgabe beginnt mit %q", out.String())
	}
}

func TestDie(t *testing.T) {
	for n := 1; n <= 6; n++ {
		if got := strings.Count(Die(n), "●"); got != n {
			t.Errorf("Die(%d) hat %d Augen", n, got)
		}
	}
}

func TestCoffee(t *testing.T) {
	for _, n := range []int{1, 2, 4, 6, 8} {
		if got := strings.Count(Coffee(n), ".---."); got != n {
			t.Errorf("Coffee(%d) zeigt %d Tassen", n, got)
		}
	}
	if !strings.Contains(Coffee(8), "zitter") || strings.Contains(Coffee(2), "zitter") {
		t.Error("Zittern nur bei acht Tassen erwartet")
	}
}

func TestD20(t *testing.T) {
	if !strings.Contains(D20(20), "CRIT") || !strings.Contains(D20(1), "PATZER") {
		t.Error("20 und 1 brauchen ihre Zugabe")
	}
	if strings.Contains(D20(7), "CRIT") || strings.Contains(D20(7), "PATZER") {
		t.Error("7 ist unspektakulär")
	}
}

func TestCoin(t *testing.T) {
	if !strings.Contains(Coin("Kopf"), "K") || !strings.Contains(Coin("Zahl"), "Z") {
		t.Error("Münze zeigt falschen Buchstaben")
	}
}

func TestGridLinesHaveEqualWidth(t *testing.T) {
	card := [3][3]string{
		{"Synergie", "Das nehmen wir offline", "Du bist stumm"},
		{"Agil", "FREI", "Circle back"},
		{"x", "y", "Wer ist da gerade dazugekommen?"},
	}
	var out strings.Builder
	lines := strings.Split(strings.TrimSpace(Grid(&out, card)), "\n")
	for _, l := range lines {
		if got, want := len([]rune(l)), len([]rune(lines[0])); got != want {
			t.Errorf("Zeile %q hat Breite %d, erwartet %d", l, got, want)
		}
	}
}
