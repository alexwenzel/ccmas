package orakel

import (
	"bufio"
	"fmt"
	"io"
	"math/rand/v2"
	"strconv"
	"strings"
	"time"

	"github.com/alexwenzel/ccmas/internal/fx"
	"github.com/alexwenzel/ccmas/internal/menu"
)

const thinkTime = 900 * time.Millisecond

// show gibt ein Bildchen und darunter die Antwort aus.
func show(out io.Writer, art, text, color string) {
	if art != "" {
		fmt.Fprint(out, fx.Paint(out, color, art))
	}
	fmt.Fprint(out, "\n→ ")
	fx.Type(out, color, text)
	fmt.Fprintln(out)
}

// NewMainMenu baut den kompletten Menübaum des Büro-Orakels.
func NewMainMenu(r *rand.Rand) *menu.Menu {
	say := func(label, art, color string, f func(*rand.Rand) string) func(*bufio.Scanner, io.Writer) {
		return func(_ *bufio.Scanner, out io.Writer) {
			fx.Suspense(out, label, thinkTime)
			show(out, art, f(r), color)
		}
	}

	return &menu.Menu{
		Title: "Büro-Orakel",
		Intro: func(out io.Writer) { fmt.Fprint(out, "\n", fx.Banner(out)) },
		Style: func(out io.Writer, line string) string { return fx.Paint(out, fx.Bold+fx.Cyan, line) },
		Items: []menu.Item{
			{Label: "Orakel befragen", Sub: &menu.Menu{
				Title: "Orakel befragen",
				Items: []menu.Item{
					{Label: "Ja/Nein-Frage", Action: say("Die Kristallkugel trübt sich…", fx.CrystalBall, fx.Magenta, YesNo)},
					{Label: "Glückskeks", Action: say("Der Keks wird aufgebrochen…", fx.FortuneCookie, fx.Yellow, Fortune)},
					{Label: "Ausrede des Tages", Action: say("Der Hund sucht eine Ausrede…", fx.Dog, fx.Blue, Excuse)},
				},
			}},
			{Label: "Glücksspiel", Sub: &menu.Menu{
				Title: "Glücksspiel",
				Items: []menu.Item{
					{Label: "Münzwurf", Action: func(_ *bufio.Scanner, out io.Writer) {
						side := CoinFlip(r)
						fx.Spin(out, "Die Münze wirbelt…", []string{"|", "/", "—", "\\"}, thinkTime)
						show(out, fx.Coin(side), side, fx.Yellow)
					}},
					{Label: "W6 würfeln", Action: func(_ *bufio.Scanner, out io.Writer) {
						n := Roll(r, 6)
						fx.Spin(out, "Der Würfel rollt…", []string{"⚀", "⚁", "⚂", "⚃", "⚄", "⚅"}, thinkTime)
						show(out, fx.Die(n), "Du würfelst eine "+strconv.Itoa(n), fx.Green)
					}},
					{Label: "W20 (20 = kritischer Treffer!)", Action: func(_ *bufio.Scanner, out io.Writer) {
						n := Roll(r, 20)
						fx.Spin(out, "Der W20 rollt…", []string{"◢", "◣", "◤", "◥"}, thinkTime)
						text, color := "Du würfelst eine "+strconv.Itoa(n), fx.Green
						switch n {
						case 20:
							text, color = "20! KRITISCHER TREFFER – das Meeting ist abgesagt.", fx.Yellow
							fx.Confetti(out)
						case 1:
							text, color = "1. Kritischer Patzer – du hast „Allen antworten“ geklickt.", fx.Red
						}
						show(out, fx.D20(n), text, color)
					}},
				},
			}},
			{Label: "Büro-Überleben", Sub: &menu.Menu{
				Title: "Büro-Überleben",
				Items: []menu.Item{
					{Label: "Kaffee-Rechner", Action: coffeeCalculator},
					{Label: "Meeting-Bingo", Action: func(_ *bufio.Scanner, out io.Writer) {
						fmt.Fprintln(out)
						fmt.Fprint(out, fx.Grid(out, BingoCard(r)))
					}},
					{Label: "Witz", Action: func(_ *bufio.Scanner, out io.Writer) {
						fmt.Fprintln(out)
						fx.Type(out, fx.Cyan, Joke(r))
						fmt.Fprintln(out)
						fmt.Fprint(out, fx.Paint(out, fx.Yellow, fx.Drum))
					}},
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
	cups := CoffeeCups(hours)
	fmt.Fprintf(out, "→ Empfehlung: %d Tasse(n) Kaffee.\n", cups)
	fmt.Fprint(out, fx.Paint(out, fx.Yellow, fx.Coffee(cups)))
}
