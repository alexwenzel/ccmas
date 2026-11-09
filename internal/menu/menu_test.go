package menu

import (
	"bufio"
	"fmt"
	"io"
	"strings"
	"testing"
)

func testMenu(calls *[]string) *Menu {
	act := func(name string) func(*bufio.Scanner, io.Writer) {
		return func(_ *bufio.Scanner, out io.Writer) {
			*calls = append(*calls, name)
			fmt.Fprintln(out, "ausgeführt:", name)
		}
	}
	return &Menu{Title: "Haupt", Items: []Item{
		{Label: "Eins", Sub: &Menu{Title: "Unter", Items: []Item{
			{Label: "A", Action: act("A")},
			{Label: "B", Action: act("B")},
		}}},
		{Label: "Zwei", Action: act("Zwei")},
	}}
}

func TestRun(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantCalls []string
		wantOut   []string
	}{
		{"direkt beenden", "0\n", nil, []string{"=== Haupt ===", "0) Beenden"}},
		{"Untermenü und zurück", "1\n2\n0\n2\n0\n", []string{"B", "Zwei"}, []string{"=== Unter ===", "0) Zurück"}},
		{"ungültige Eingabe", "x\n9\n-1\n0\n", nil, []string{"nicht verstanden"}},
		{"EOF beendet sauber", "1\n", nil, []string{"=== Unter ==="}},
		{"EOF im Hauptmenü", "", nil, []string{"=== Haupt ==="}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls []string
			var out strings.Builder
			testMenu(&calls).Run(strings.NewReader(tt.input), &out)

			if strings.Join(calls, ",") != strings.Join(tt.wantCalls, ",") {
				t.Errorf("Aktionen = %v, erwartet %v", calls, tt.wantCalls)
			}
			for _, want := range tt.wantOut {
				if !strings.Contains(out.String(), want) {
					t.Errorf("Ausgabe enthält %q nicht:\n%s", want, out.String())
				}
			}
		})
	}
}
