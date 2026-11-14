---
name: code-review
description: Den PR eines Tickets unabhängig gegen Akzeptanzkriterien, Tests und Doku prüfen und das Urteil FREIGEGEBEN oder BEFUNDE ins Ticket schreiben. Ändert keinen Code. Für die Rolle Reviewer.
argument-hint: <Ticket-ID>
---

# PR prüfen

Eingabe: die Ticket-ID aus dem Auftrag ($ARGUMENTS).

## Voraussetzung

Das Ticket liegt in To Review, das Feld PR ist gesetzt, „Am Zug“ ist Reviewer. Sonst ändere nichts und melde den Ausgang „Problem“ mit dem vorgefundenen Stand.

## Schritte

1. **Lesen.** Ticket nach `ccmas:ticket`: Plan, Akzeptanzkriterien, Risikoklasse, bisherige Review-Kommentare. Dazu der Diff des PR gegen den Integrationsbranch und der Commit, auf dem der PR steht.
2. **Stand abgleichen.** Der Worktree muss genau den Stand des PR zeigen: keine nicht committeten Änderungen, und der lokale Commit ist der Commit, auf dem der PR steht. Hole dazu den Branch vom Repository. Weicht der Worktree ab, ändere nichts und melde den Ausgang „Problem“ mit beiden Commits.
3. **Selbst prüfen.** Führe die Prüfbefehle aus der `CLAUDE.md` auf dem Stand des PR aus. Gemeldete Ergebnisse zählen nicht.
4. **Kriterium für Kriterium.** Für jedes Akzeptanzkriterium: erfüllt oder nicht, mit Beleg (Datei und Zeile, Testname oder Ausgabe eines Befehls).
5. **Vier weitere Fragen.**
   - Prüfen die Tests das Verhalten, oder spiegeln sie nur die Umsetzung?
   - Enthält der Diff etwas, das nicht zum Ticket gehört?
   - Stimmt `docs/` mit dem überein, was der Code jetzt tut?
   - Passt die Risikoklasse zum tatsächlichen Diff? Die Tabelle steht in `ccmas:task-planning`. Trifft eine höhere Klasse zu, stufe hoch.
6. **Urteil.**
   - **FREIGEGEBEN:** Alle Kriterien erfüllt, Prüfbefehle bestehen, keine der vier Fragen ergibt einen Mangel.
   - **BEFUNDE:** sonst. Jeder Befund nennt die Stelle, was falsch ist und woran die Behebung erkennbar wäre. Keine Geschmacksfragen.
7. **Ins Ticket schreiben.** Review-Runde um eins erhöhen. Dann der Übergabe-Kommentar nach `ccmas:ticket`, im Feld Ergebnis:

   ```
   Urteil: <FREIGEGEBEN | BEFUNDE>, geprüfter Commit <Hash>
   Kriterien: <n von m erfüllt>
   Befunde:
   1. <Datei:Zeile> – <was falsch ist> – <woran die Behebung erkennbar ist>
   ```

   - Bei FREIGEGEBEN: „Am Zug“ auf Merger, „Ausgang“ auf Fertig.
   - Bei BEFUNDE: „Am Zug“ auf Worker, „Ausgang“ auf Befunde.
   - Bei BEFUNDE in der dritten Runde oder später: Der Worker bekommt das Ticket nicht noch einmal. Lege nach `ccmas:ticket` die Rückfrage `Entscheidung: Wie weiter mit <Ticket-ID> nach drei Review-Runden` an, mit den offenen Befunden und einer Empfehlung. Damit steht „Am Zug“ auf Mensch und „Ausgang“ auf Rückfrage.
   - Die Spalte bleibt To Review.
8. **Am PR vermerken.** Schreibe das Urteil mit dem geprüften Commit als Kommentar an den PR.

## Grenzen

- Keinen Code ändern, nichts committen, nichts pushen.
- Die Risikoklasse nie herunterstufen.
- Das Urteil gilt nur für den geprüften Commit. Ändert sich der PR danach, ist ein neues Review nötig.

## Abschluss

Nenne am Ende: Ticket, Urteil, geprüfter Commit, die Befunde als Liste, Risikoklasse nach dem Review.
