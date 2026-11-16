# Plugin `ccmas`

Das Verhalten von CCMAS als Plugin für Claude Code: sieben Rollen als Agents, neun Skills. Aktivieren, deaktivieren und der Start der Rollen stehen in der [README des Projekts](../README.md).

Installieren aus diesem Repository:

```text
/plugin install ccmas --marketplace alexwenzel/ccmas
```

| Pfad | Inhalt |
| --- | --- |
| `.claude-plugin/plugin.json` | Name, Version, Beschreibung |
| `agents/` | `project-planner`, `feature-planner`, `supervisor`, `planner`, `worker`, `reviewer`, `merger` |
| `skills/ticket/` | Milestones, Tasks und Rückfragen lesen, anlegen, verschieben, Felder setzen, kommentieren. Einzige Stelle, die das Ticketsystem kennt (Fassung für Asana) |
| `skills/project-planning/` | Projektbeschreibung in eine Roadmap aus Milestones zerlegen |
| `skills/feature-planning/` | Einen Milestone konzipieren und in Tasks zerlegen |
| `skills/supervisor/` | Board lesen, bereinigen, Worktrees aufräumen, Milestones abschließen, nächsten Schritt nennen |
| `skills/task-planning/` | Task ausplanen oder Ticket aus einer Beschreibung anlegen |
| `skills/feature-parallel/` | Ticket im Worktree test-driven umsetzen bis zum PR |
| `skills/code-review/` | PR gegen die Akzeptanzkriterien prüfen |
| `skills/merge/` | Freigegebenen PR integrieren |
| `skills/init/` | Projekt einrichten: `CLAUDE.md`, `docs/`, Worktree-Einstellungen, Plugin-Aktivierung, Referenzen. Enthält die Vorlagen dafür |

Alle Namen tragen das Präfix des Plugins: der Skill heißt `/ccmas:ticket`, der Agent `ccmas:worker`.

Bei jeder inhaltlichen Änderung die `version` in `.claude-plugin/plugin.json` erhöhen. Eine installierte Fassung bleibt sonst auf dem alten Stand.
