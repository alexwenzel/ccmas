package orakel

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
)

func newRand() *rand.Rand { return rand.New(rand.NewPCG(1, 2)) }

func TestCoffeeCups(t *testing.T) {
	tests := []struct {
		hours float64
		want  int
	}{
		{9, 1}, {8, 1}, {7.5, 2}, {6, 2}, {5, 4}, {4, 4}, {3, 6}, {2, 6}, {1, 8}, {0, 8},
	}
	for _, tt := range tests {
		if got := CoffeeCups(tt.hours); got != tt.want {
			t.Errorf("CoffeeCups(%v) = %d, erwartet %d", tt.hours, got, tt.want)
		}
	}
}

func TestRollStaysInRange(t *testing.T) {
	r := newRand()
	for range 1000 {
		if n := Roll(r, 20); n < 1 || n > 20 {
			t.Fatalf("Roll(20) = %d, außerhalb von 1..20", n)
		}
	}
}

func TestDeterministicWithSeed(t *testing.T) {
	if a, b := Fortune(newRand()), Fortune(newRand()); a != b {
		t.Errorf("gleicher Seed, unterschiedliche Sprüche: %q vs %q", a, b)
	}
}

func TestCoinFlipOnlyKnownSides(t *testing.T) {
	r := newRand()
	for range 100 {
		if s := CoinFlip(r); s != "Kopf" && s != "Zahl" {
			t.Fatalf("unerwartete Seite %q", s)
		}
	}
}

func TestBingoCard(t *testing.T) {
	card := BingoCard(newRand())
	if card[1][1] != "FREI" {
		t.Errorf("Mitte = %q, erwartet FREI", card[1][1])
	}
	var seen []string
	for _, row := range card {
		for _, cell := range row {
			if slices.Contains(seen, cell) {
				t.Errorf("Feld %q kommt doppelt vor", cell)
			}
			seen = append(seen, cell)
		}
	}
}

func TestMainMenuEndToEnd(t *testing.T) {
	var out strings.Builder
	// Büro-Überleben → Kaffee-Rechner → 3 Stunden Schlaf → zurück → beenden
	NewMainMenu(newRand()).Run(strings.NewReader("3\n1\n3\n0\n0\n"), &out)
	for _, want := range []string{"Büro-Orakel", "Büro-Überleben", "6 Tasse(n) Kaffee", "█", ".---."} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("Ausgabe enthält %q nicht:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "\x1b") {
		t.Errorf("Escape-Code in Nicht-Terminal-Ausgabe:\n%q", out.String())
	}
}

func TestEffectsMenuEntries(t *testing.T) {
	// Jeder Menüpunkt einmal: Orakel (3), Glücksspiel (3), Überleben (Bingo, Witz)
	input := "1\n1\n2\n3\n0\n2\n1\n2\n3\n0\n3\n2\n3\n0\n0\n"
	var out strings.Builder
	NewMainMenu(newRand()).Run(strings.NewReader(input), &out)
	for _, want := range []string{"→ ", "┌", "FREI", "Ba-dum-tss!"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("Ausgabe enthält %q nicht:\n%s", want, out.String())
		}
	}
}

// ExampleCoffeeCups wird von „go test“ ausgeführt und prüft die Ausgabe.
func ExampleCoffeeCups() {
	fmt.Println(CoffeeCups(3))
	fmt.Println(CoffeeCups(8))
	// Output:
	// 6
	// 1
}

func BenchmarkRoll(b *testing.B) {
	r := newRand()
	for b.Loop() {
		Roll(r, 20)
	}
}
