// Package menu bietet ein kleines, verschachtelbares Textmenü.
//
// Ein- und Ausgabe laufen über io.Reader/io.Writer, damit sich ganze
// Menüsitzungen in Tests mit vorgefertigten Eingaben durchspielen lassen.
package menu

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Item ist ein Menüpunkt: entweder eine Aktion oder ein Untermenü.
type Item struct {
	Label  string
	Action func(in *bufio.Scanner, out io.Writer)
	Sub    *Menu
}

// Menu ist eine Liste nummerierter Menüpunkte mit Titel.
type Menu struct {
	Title string
	Items []Item
}

// Run zeigt das Menü, bis die Eingabe endet oder "0" im Hauptmenü gewählt wird.
func (m *Menu) Run(in io.Reader, out io.Writer) {
	m.run(bufio.NewScanner(in), out, true)
}

// run gibt false zurück, wenn die Eingabe zu Ende ist (dann bricht alles ab).
func (m *Menu) run(in *bufio.Scanner, out io.Writer, top bool) bool {
	back := "Zurück"
	if top {
		back = "Beenden"
	}
	for {
		fmt.Fprintf(out, "\n=== %s ===\n", m.Title)
		for i, it := range m.Items {
			fmt.Fprintf(out, "  %d) %s\n", i+1, it.Label)
		}
		fmt.Fprintf(out, "  0) %s\n> ", back)

		if !in.Scan() {
			fmt.Fprintln(out)
			return false
		}
		choice, err := strconv.Atoi(strings.TrimSpace(in.Text()))
		switch {
		case err != nil || choice < 0 || choice > len(m.Items):
			fmt.Fprintln(out, "Das habe ich nicht verstanden – bitte eine Zahl aus dem Menü wählen.")
		case choice == 0:
			return true
		default:
			it := m.Items[choice-1]
			if it.Action != nil {
				it.Action(in, out)
			}
			if it.Sub != nil && !it.Sub.run(in, out, false) {
				return false
			}
		}
	}
}
