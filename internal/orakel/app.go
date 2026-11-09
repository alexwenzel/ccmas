package orakel

import (
	"bufio"
	"fmt"
	"io"
	"math/rand/v2"
	"strconv"
	"strings"

	"github.com/alexwenzel/ccmas/internal/menu"
)

// NewMainMenu baut den kompletten Menübaum des Büro-Orakels.
func NewMainMenu(r *rand.Rand) *menu.Menu {
	say := func(f func(*rand.Rand) string) func(*bufio.Scanner, io.Writer) {
		return func(_ *bufio.Scanner, out io.Writer) { fmt.Fprintln(out, "\n→", f(r)) }
	}

	return &menu.Menu{
		Title: "Büro-Orakel",
		Items: []menu.Item{
			{Label: "Orakel befragen", Sub: &menu.Menu{
				Title: "Orakel befragen",
				Items: []menu.Item{
					{Label: "Ja/Nein-Frage", Action: say(YesNo)},
					{Label: "Glückskeks", Action: say(Fortune)},
					{Label: "Ausrede des Tages", Action: say(Excuse)},
				},
			}},
			{Label: "Glücksspiel", Sub: &menu.Menu{
				Title: "Glücksspiel",
				Items: []menu.Item{
					{Label: "Münzwurf", Action: say(CoinFlip)},
					{Label: "W6 würfeln", Action: func(_ *bufio.Scanner, out io.Writer) {
						fmt.Fprintln(out, "\n→ Du würfelst eine", Roll(r, 6))
					}},
					{Label: "W20 (20 = kritischer Treffer!)", Action: func(_ *bufio.Scanner, out io.Writer) {
						n := Roll(r, 20)
						switch n {
						case 20:
							fmt.Fprintln(out, "\n→ 20! KRITISCHER TREFFER – das Meeting ist abgesagt.")
						case 1:
							fmt.Fprintln(out, "\n→ 1. Kritischer Patzer – du hast „Allen antworten“ geklickt.")
						default:
							fmt.Fprintln(out, "\n→ Du würfelst eine", n)
						}
					}},
				},
			}},
			{Label: "Büro-Überleben", Sub: &menu.Menu{
				Title: "Büro-Überleben",
				Items: []menu.Item{
					{Label: "Kaffee-Rechner", Action: coffeeCalculator},
					{Label: "Meeting-Bingo", Action: func(_ *bufio.Scanner, out io.Writer) {
						card := BingoCard(r)
						fmt.Fprintln(out)
						for _, row := range card {
							fmt.Fprintf(out, "  %-18.18s | %-18.18s | %-18.18s\n", row[0], row[1], row[2])
						}
					}},
					{Label: "Witz", Action: say(Joke)},
				},
			}},
		},
	}
}

func coffeeCalculator(in *bufio.Scanner, out io.Writer) {
	fmt.Fprint(out, "\nWie viele Stunden hast du geschlafen? ")
	if !in.Scan() {
		return
	}
	text := strings.ReplaceAll(strings.TrimSpace(in.Text()), ",", ".")
	hours, err := strconv.ParseFloat(text, 64)
	if err != nil || hours < 0 || hours > 24 {
		fmt.Fprintln(out, "→ Das ist keine realistische Schlafdauer.")
		return
	}
	fmt.Fprintf(out, "→ Empfehlung: %d Tasse(n) Kaffee.\n", CoffeeCups(hours))
}
