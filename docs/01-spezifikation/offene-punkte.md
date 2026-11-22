# Offene Punkte

Was noch nicht entschieden ist. Was noch nicht ausprobiert ist, steht in der [README](../../README.md#noch-nicht-geprüft).

- **Reifeprüfung auf dem Modell des Workers.** Der Worker prüft die Reife selbst, auf seinem Modell. Setzen Worker Tickets um, die nicht reif waren, läuft die Prüfung als Subagent auf dem starken Modell, weiter ohne eigene Sitzung.
- **Typ.** Das Feld dient der Übersicht im Board und steuert keinen Ablauf. Concept-, Bug- und Organisations-Tickets laufen wie Implementation.
- **Befunde als ein Kommentar.** Der Skill `reviewer` schreibt Urteil und Befunde in einen Kommentar am PR. Einzelne Kommentare an den betroffenen Zeilen wären genauer, brauchen aber die Schnittstelle von GitHub statt `gh pr comment`.
- **Sichtprüfungen im Abnahmetest.** Der Abnahmetest hält Befehle und Ausgaben fest. Wie ein Ergebnisziel geprüft wird, das nur am Bildschirm sichtbar ist, legt die Spezifikation nicht fest.
- **Weitere Runner.** Herdr ist nicht ausgearbeitet.
