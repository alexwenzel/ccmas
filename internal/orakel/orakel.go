// Package orakel enthält die Spaßfunktionen des Büro-Orakels.
//
// Alle Zufallsfunktionen bekommen den Zufallsgenerator übergeben,
// damit Tests mit festem Seed reproduzierbare Ergebnisse haben.
package orakel

import "math/rand/v2"

var (
	yesNoAnswers = []string{
		"Ja, auf jeden Fall.",
		"Nein. Und frag nicht nochmal.",
		"Vielleicht – nach dem ersten Kaffee.",
		"Die Zeichen stehen gut.",
		"Sehr zweifelhaft.",
		"Frag das Management, die wissen es auch nicht.",
	}
	fortunes = []string{
		"Heute wird dein Code beim ersten Versuch kompilieren.",
		"Ein unerwartetes Meeting wird sich als überflüssig erweisen.",
		"Wer zuletzt committet, hat den Merge-Konflikt.",
		"Du wirst bald etwas finden, das du schon lange suchst: den Mute-Knopf.",
		"Die Kaffeemaschine ist heute auf deiner Seite.",
	}
	excuses = []string{
		"Das war schon so, als ich kam.",
		"Auf meinem Rechner lief es noch.",
		"Mein Hund hat den Pull-Request gefressen.",
		"Das ist kein Bug, das ist ein undokumentiertes Feature.",
		"Der Praktikant war's. (Wir haben keinen.)",
	}
	jokes = []string{
		"Warum verwechseln Programmierer Halloween und Weihnachten? Weil Oct 31 == Dec 25.",
		"Es gibt 10 Arten von Menschen: Die, die Binärcode verstehen, und die anderen.",
		"Ein SQL-Query geht in eine Bar, geht zu zwei Tischen und fragt: „Darf ich mich zu euch JOINen?“",
		"Wie viele Entwickler braucht man, um eine Glühbirne zu wechseln? Keinen, das ist ein Hardware-Problem.",
	}
	buzzwords = []string{
		"Synergie", "Das nehmen wir offline", "Können wir kurz alignen?", "Low-hanging fruit",
		"Agil", "Ich muss gleich zum nächsten Call", "Du bist stumm", "Bitte Folie teilen",
		"Auf der Roadmap", "Circle back", "Skalierbar", "Wer ist da gerade dazugekommen?",
	}
)

func pick(r *rand.Rand, list []string) string { return list[r.IntN(len(list))] }

// YesNo beantwortet eine Ja/Nein-Frage.
func YesNo(r *rand.Rand) string { return pick(r, yesNoAnswers) }

// Fortune liefert einen Glückskeks-Spruch.
func Fortune(r *rand.Rand) string { return pick(r, fortunes) }

// Excuse liefert die Ausrede des Tages.
func Excuse(r *rand.Rand) string { return pick(r, excuses) }

// Joke liefert einen Programmierer-Witz.
func Joke(r *rand.Rand) string { return pick(r, jokes) }

// CoinFlip wirft eine Münze.
func CoinFlip(r *rand.Rand) string {
	if r.IntN(2) == 0 {
		return "Kopf"
	}
	return "Zahl"
}

// Roll würfelt mit einem Würfel mit der angegebenen Seitenzahl (1..sides).
func Roll(r *rand.Rand, sides int) int { return r.IntN(sides) + 1 }

// CoffeeCups schätzt, wie viele Tassen Kaffee man nach so viel Schlaf braucht.
func CoffeeCups(sleepHours float64) int {
	switch {
	case sleepHours >= 8:
		return 1
	case sleepHours >= 6:
		return 2
	case sleepHours >= 4:
		return 4
	case sleepHours >= 2:
		return 6
	default:
		return 8
	}
}

// BingoCard erzeugt eine 3×3-Meeting-Bingo-Karte mit "FREI" in der Mitte.
func BingoCard(r *rand.Rand) [3][3]string {
	words := r.Perm(len(buzzwords))
	var card [3][3]string
	n := 0
	for row := range card {
		for col := range card[row] {
			if row == 1 && col == 1 {
				card[row][col] = "FREI"
				continue
			}
			card[row][col] = buzzwords[words[n]]
			n++
		}
	}
	return card
}
