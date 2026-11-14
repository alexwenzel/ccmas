# Plugin `ccmas`

Das Verhalten von CCMAS als Plugin für Claude Code: fünf Rollen als Agents, sieben Skills. Aktivieren, deaktivieren und der Start der Rollen stehen in der [README des Projekts](../README.md).

Installieren aus diesem Repository:

```text
/plugin install ccmas --marketplace alexwenzel/ccmas
```

| Pfad | Inhalt |
| --- | --- |
| `.claude-plugin/plugin.json` | Name, Version, Beschreibung |
| `agents/` | `supervisor`, `planner`, `worker`, `reviewer`, `merger` |
| `skills/ticket/` | Ticket lesen, verschieben, Felder setzen, kommentieren, Rückfragen. Einzige Stelle, die das Ticketsystem kennt (Fassung für Asana) |
| `skills/supervisor/` | Board lesen, bereinigen, nächsten Schritt nennen |
| `skills/task-planning/` | Ticket ausplanen oder aus einer Beschreibung anlegen |
| `skills/feature-parallel/` | Ticket im Worktree umsetzen bis zum PR |
| `skills/code-review/` | PR gegen die Akzeptanzkriterien prüfen |
| `skills/merge/` | Freigegebenen PR integrieren |
| `skills/init/` | Projekt einrichten: `CLAUDE.md`, `docs/`, Worktree-Einstellungen, Referenzen. Enthält die Vorlagen dafür |

Alle Namen tragen das Präfix des Plugins: der Skill heißt `/ccmas:ticket`, der Agent `ccmas:worker`.

Bei jeder inhaltlichen Änderung die `version` in `.claude-plugin/plugin.json` erhöhen. Eine installierte Fassung bleibt sonst auf dem alten Stand.
