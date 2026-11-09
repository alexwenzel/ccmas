package fx

import (
	"fmt"
	"io"
	"strings"
)

// Bildchen ohne Parameter.
const (
	CrystalBall = `
     _.-----._
   .'  . * .  '.
  /  *   .   *  \
 |  .  ~~~~~  .  |
  \  *  ~~~  .  /
   '._  *   _.'
    __'-----'__
   /___________\
`
	FortuneCookie = `
      .-~~~~~~-.
    .'   ____   '.
   /   .'    '.   \
   \_.'________'._/
      '~~~~~~~~'
`
	Dog = `
  / \__
 (    @\___
 /         O
/   (_____/
/_____/   U
`
	Drum = `
   ,-------.
  ( ======= )   Ba-dum-tss!
   '-------'
`
	Wave = `
 \o/
  |
 / \
`
)

var letters = map[rune][5]string{
	'O': {" ███ ", "█   █", "█   █", "█   █", " ███ "},
	'R': {"████ ", "█   █", "████ ", "█  █ ", "█   █"},
	'A': {" ███ ", "█   █", "█████", "█   █", "█   █"},
	'K': {"█   █", "█  █ ", "███  ", "█  █ ", "█   █"},
	'E': {"█████", "█    ", "████ ", "█    ", "█████"},
	'L': {"█    ", "█    ", "█    ", "█    ", "█████"},
}

// Banner ist der große ORAKEL-Schriftzug für den Start.
func Banner(w io.Writer) string {
	var b strings.Builder
	for row := range 5 {
		parts := make([]string, 0, 6)
		for _, c := range "ORAKEL" {
			parts = append(parts, letters[c][row])
		}
		b.WriteString("  " + Rainbow(w, strings.Join(parts, " "), row) + "\n")
	}
	b.WriteString(Paint(w, Dim, "  ~ die Kristallkugel für's Büro ~") + "\n")
	return b.String()
}

// Coin zeigt die geworfene Münze („Kopf“ oder „Zahl“).
func Coin(side string) string {
	return fmt.Sprintf(`
   .-----.
  /   %s   \
  \       /
   '-----'
`, side[:1])
}

// pips legt fest, welche der 3×3 Positionen eines Würfels Augen haben.
var pips = [7][3][3]bool{
	1: {{}, {false, true, false}, {}},
	2: {{true, false, false}, {}, {false, false, true}},
	3: {{true, false, false}, {false, true, false}, {false, false, true}},
	4: {{true, false, true}, {}, {true, false, true}},
	5: {{true, false, true}, {false, true, false}, {true, false, true}},
	6: {{true, false, true}, {true, false, true}, {true, false, true}},
}

// Die zeichnet einen Würfel mit n (1..6) Augen.
func Die(n int) string {
	var b strings.Builder
	b.WriteString("\n┌───────┐\n")
	for _, row := range pips[n] {
		b.WriteString("│")
		for _, on := range row {
			if on {
				b.WriteString(" ●")
			} else {
				b.WriteString("  ")
			}
		}
		b.WriteString(" │\n")
	}
	b.WriteString("└───────┘\n")
	return b.String()
}

// D20 zeichnet den Zwanzigseiter mit n; bei 20 und 1 gibt es eine Zugabe.
func D20(n int) string {
	s := fmt.Sprintf(`
      /\
     /  \
    / %2d \
   /------\
   \      /
    \    /
     \  /
      \/
`, n)
	switch n {
	case 20:
		s += "   ★ CRIT! ★\n"
	case 1:
		s += "   ☠ PATZER ☠\n"
	}
	return s
}

// Coffee zeichnet n Tassen Kaffee; bei acht zittert man schon.
func Coffee(n int) string {
	cup := [4]string{"  ) )  ", " .---. ", " |   |]", " '---' "}
	var b strings.Builder
	b.WriteString("\n")
	for row := range cup {
		b.WriteString(strings.TrimRight(strings.Repeat(cup[row]+" ", n), " ") + "\n")
	}
	if n >= 8 {
		b.WriteString("⚡ zitter ⚡\n")
	}
	return b.String()
}
