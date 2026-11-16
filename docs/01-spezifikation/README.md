# Spezifikation

Das Konzept von CCMAS: KI-Agents mit festen Rollen planen ein Projekt und arbeiten sein Board Ticket für Ticket ab.

Das Plugin unter [`claude-plugin/`](../../claude-plugin/README.md) setzt diese Spezifikation um. Wer das Verhalten ändert, ändert Spezifikation und Plugin im selben PR.

| Datei | Inhalt |
| --- | --- |
| [konzept.md](konzept.md) | Bausteine und Grundsätze |
| [rollen.md](rollen.md) | Die acht Rollen und die Risikoklassen R0 bis R3 |
| [board.md](board.md) | Spalten, Felder, Übergaben, Rückfragen, wer ins Ticket schreibt |
| [planung.md](planung.md) | Vom Projekt über Milestones zu Tasks: Project Planner und Feature Planner |
| [ablauf.md](ablauf.md) | Supervisor-Loop, Ticket-Loop, Merge-Phase, Worktrees aufräumen |
| [claude-code.md](claude-code.md) | Wie das Konzept in Claude Code umgesetzt ist |
| [offene-punkte.md](offene-punkte.md) | Was noch nicht entschieden oder erprobt ist |

Diese Spezifikation beschreibt den Ablauf mit Claude Code allein. Andere Runner haben eigene Dokumente, etwa [Orchestrator](../02-orchestrator/README.md).
