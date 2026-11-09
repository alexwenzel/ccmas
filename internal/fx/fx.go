// Package fx enthält Terminal-Effekte: Farben, Animationen und Bildchen.
//
// Farben und Animationen sind nur aktiv, wenn die Ausgabe ein echtes
// Terminal ist und NO_COLOR nicht gesetzt ist. Sonst (Pipes, Tests) wird
// schlichter Text ohne Escape-Codes und ohne Wartezeiten geschrieben.
package fx

import (
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"strings"
	"time"
)

// ANSI-Farben und -Stile für Paint und Type.
const (
	reset   = "\x1b[0m"
	Bold    = "\x1b[1m"
	Dim     = "\x1b[2m"
	Red     = "\x1b[31m"
	Green   = "\x1b[32m"
	Yellow  = "\x1b[33m"
	Blue    = "\x1b[34m"
	Magenta = "\x1b[35m"
	Cyan    = "\x1b[36m"
)

var rainbow = []int{196, 202, 226, 46, 51, 21, 129, 201}

// Fancy meldet, ob w ein Terminal ist, in dem Farben und Animationen laufen sollen.
func Fancy(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok || os.Getenv("NO_COLOR") != "" {
		return false
	}
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

// Paint färbt text mit color, sofern w ein Terminal ist.
func Paint(w io.Writer, color, text string) string {
	if !Fancy(w) {
		return text
	}
	return color + text + reset
}

// Rainbow färbt text zeichenweise im Regenbogen; offset verschiebt den Verlauf.
func Rainbow(w io.Writer, text string, offset int) string {
	if !Fancy(w) {
		return text
	}
	var b strings.Builder
	i := offset
	for _, c := range text {
		if c == ' ' {
			b.WriteRune(c)
			continue
		}
		fmt.Fprintf(&b, "\x1b[38;5;%dm%c", rainbow[i%len(rainbow)], c)
		i++
	}
	b.WriteString(reset)
	return b.String()
}

// Type schreibt text wie von einer Schreibmaschine getippt, in der Farbe color.
func Type(w io.Writer, color, text string) {
	if !Fancy(w) {
		fmt.Fprint(w, text)
		return
	}
	fmt.Fprint(w, color)
	for _, c := range text {
		fmt.Fprintf(w, "%c", c)
		time.Sleep(15 * time.Millisecond)
	}
	fmt.Fprint(w, reset)
}

// Spin zeigt für die Dauer d einen Mini-Spinner mit label und räumt die Zeile danach auf.
func Spin(w io.Writer, label string, frames []string, d time.Duration) {
	if !Fancy(w) {
		return
	}
	const step = 100 * time.Millisecond
	for i := 0; time.Duration(i)*step < d; i++ {
		fmt.Fprintf(w, "\r%s %s", Paint(w, Magenta, frames[i%len(frames)]), label)
		time.Sleep(step)
	}
	fmt.Fprint(w, "\r\x1b[2K")
}

// Suspense lässt das Orakel für die Dauer d „nachdenken“.
func Suspense(w io.Writer, label string, d time.Duration) {
	Spin(w, label, []string{"◐", "◓", "◑", "◒"}, d)
}

// Confetti lässt kurz buntes Konfetti regnen.
func Confetti(w io.Writer) {
	if !Fancy(w) {
		return
	}
	const rows, cols, frames = 4, 40, 8
	bits := []string{"*", "+", "•", "~", "✦", "o"}
	for f := range frames {
		if f > 0 {
			fmt.Fprintf(w, "\x1b[%dA", rows)
		}
		for range rows {
			var b strings.Builder
			for range cols {
				if rand.IntN(4) != 0 {
					b.WriteByte(' ')
					continue
				}
				fmt.Fprintf(&b, "\x1b[38;5;%dm%s", rainbow[rand.IntN(len(rainbow))], bits[rand.IntN(len(bits))])
			}
			fmt.Fprintln(w, b.String()+reset)
		}
		time.Sleep(120 * time.Millisecond)
	}
}

// cell kürzt und füllt s auf genau width Zeichen auf.
func cell(s string, width int) string { return fmt.Sprintf("%-*.*s", width, width, s) }

// Grid zeichnet die Bingo-Karte als Tabelle; „FREI“ wird hervorgehoben.
func Grid(w io.Writer, card [3][3]string) string {
	const width = 18
	line := func(l, m, r string) string {
		seg := strings.Repeat("─", width+2)
		return l + seg + m + seg + m + seg + r + "\n"
	}
	var b strings.Builder
	b.WriteString(line("┌", "┬", "┐"))
	for i, row := range card {
		b.WriteString("│")
		for _, c := range row {
			text := cell(c, width)
			if c == "FREI" {
				text = Paint(w, Bold+Green, text)
			}
			b.WriteString(" " + text + " │")
		}
		b.WriteString("\n")
		if i < len(card)-1 {
			b.WriteString(line("├", "┼", "┤"))
		}
	}
	b.WriteString(line("└", "┴", "┘"))
	return b.String()
}
