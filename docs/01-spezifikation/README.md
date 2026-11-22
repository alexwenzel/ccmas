# Spezifikation

Das Konzept von CCMAS: KI-Agents mit festen Rollen planen ein Projekt und arbeiten sein Board Ticket für Ticket ab.

Das Plugin unter [`claude-plugin/`](../../claude-plugin/README.md) setzt diese Spezifikation um. Wer das Verhalten ändert, ändert Spezifikation und Plugin im selben PR.

| Datei | Inhalt |
| --- | --- |
| [konzept.md](konzept.md) | Bausteine, Grundsätze, Rollen, Planung, Ticket-Loop, Supervisor, Freigaben |
| [board.md](board.md) | Spalten, Felder, Rückfragen, was am PR steht, wer was darf |
| [runner.md](runner.md) | Die Verben, die ein Runner erfüllt, und ihre Umsetzung mit Claude Code allein |
| [offene-punkte.md](offene-punkte.md) | Was noch nicht entschieden ist |

Die Spezifikation beschreibt Grundsätze, Rollen, Board und Runner. Die Abläufe der Rollen stehen in den Skills des Plugins; sie sind die Norm, und die Spezifikation erzählt ihre Schritte nicht nach.

Sie beschreibt den Ablauf mit Claude Code allein. Der vorgesehene Runner ist Orchestrator; seine Zuordnung steht in einem eigenen Dokument: [CCMAS mit Orchestrator](../02-orchestrator/README.md).
