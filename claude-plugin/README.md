# Plugin `ccmas`

Das Verhalten von CCMAS als Plugin für Claude Code: sieben Rollen als Agents, neun Skills. Das Konzept dahinter steht in der [Spezifikation](../docs/01-spezifikation/README.md), Aktivieren und Deaktivieren in der [README des Projekts](../README.md#plugin-aktivieren-und-deaktivieren).

Installieren aus diesem Repository:

```text
/plugin install ccmas --marketplace alexwenzel/ccmas
```

## Rollen

Im Plugin ist jede Rolle ein Agent mit genau einem Skill. Alle Rollen lesen und pflegen das Ticket zusätzlich über den Skill `ccmas:ticket` ([Wer ins Ticket schreibt](../docs/01-spezifikation/board.md#wer-ins-ticket-schreibt)).

| Rolle | Agent | Skill | Arbeitsort | Modell | Bewegt das Ticket |
| --- | --- | --- | --- | --- | --- |
| Project Planner | `ccmas:project-planner` | `/ccmas:project-planning` | Eigener Worktree | opus | legt Milestones im Backlog an |
| Feature Planner | `ccmas:feature-planner` | `/ccmas:feature-planning` | Eigener Worktree | opus | legt Tasks im Backlog an |
| Supervisor | `ccmas:supervisor` | `/ccmas:supervisor` | Hauptverzeichnis | opus | nach To Plan; beim Bereinigen nach Done; abgenommene Milestones nach Done |
| Planner | `ccmas:planner` | `/ccmas:task-planning` | Hauptverzeichnis | opus | nach To Do |
| Worker | `ccmas:worker` | `/ccmas:feature-parallel` | Worktree des Tickets | sonnet | nach In Progress, nach To Review |
| Reviewer | `ccmas:reviewer` | `/ccmas:code-review` | Worktree des Tickets | opus | gar nicht |
| Merger | `ccmas:merger` | `/ccmas:merge` | Worktree des Tickets | sonnet | nach Done |

Für alle sieben Agents gilt:

- Sie laufen in einer Sitzung im Terminal, in der der Mensch mitlesen und eingreifen kann.
- Sie starten keine Subagents und keine andere Rolle. Der Supervisor startet nur dann etwas, wenn die Regeln seiner Sitzung es ausdrücklich verlangen.
- Die Planner und der Supervisor laufen auf Opus, weil sie das Projekt überblicken und Entscheidungen vorbereiten. Der Reviewer läuft auf Opus, weil sein Urteil den Merge freigibt.

## Start einer Rolle

Mit Claude Code allein startet der Mensch jede Rolle selbst. `R` steht für `--permission-mode bypassPermissions`: Die Sitzung fragt vor keinem Schritt nach.

| Rolle | Start |
| --- | --- |
| Project Planner | `claude R --worktree roadmap --agent ccmas:project-planner "/ccmas:project-planning <Beschreibung oder Datei>"` |
| Feature Planner | `claude R --worktree <Milestone> --agent ccmas:feature-planner "/ccmas:feature-planning <Milestone oder Beschreibung>"` |
| Supervisor | `claude R --agent ccmas:supervisor "/ccmas:supervisor"` |
| Planner | `claude R --agent ccmas:planner "/ccmas:task-planning <Ticket oder Beschreibung>"` |
| Worker | `claude R --worktree <Ticket> --agent ccmas:worker "/ccmas:feature-parallel <Ticket>"` |
| Reviewer | `claude R --worktree <Ticket> --agent ccmas:reviewer "/ccmas:code-review <Ticket>"` |
| Merger | `claude R --worktree <Ticket> --agent ccmas:merger "/ccmas:merge <Ticket>"` |

- `--worktree <Ticket>` legt den Worktree unter `.claude/worktrees/<Ticket>` an. Gibt es ihn schon, wird er wieder geöffnet. So arbeiten Worker, Reviewer und Merger im selben Worktree.
- Der Worktree zweigt vom Stand des Hauptverzeichnisses ab, wenn `worktree.baseRef` auf `"head"` steht. Das setzt `/ccmas:init`. Das Hauptverzeichnis muss dafür auf dem Integrationsbranch stehen; den aktuellen Stand holt der Supervisor zu Beginn jeder Runde.
- Reviewer und Merger eines Doku-PR starten im Worktree des Planners (`--worktree roadmap` oder `--worktree <Milestone>`). Der Name steht im Übergabe-Kommentar des Tickets.

## Skills

Diese neun Skills trägt der Ablauf. Im Plugin heißen sie `ccmas:<name>`.

| Skill | Zweck | Benutzt von | Aufruf |
| --- | --- | --- | --- |
| `ticket` | Milestones, Tasks und Rückfragen lesen, anlegen, verschieben, Felder setzen, kommentieren. Einzige Stelle, die das Ticketsystem kennt; Fassung für Asana | alle Rollen | von den anderen Skills, nicht von Hand |
| `project-planning` | Projektbeschreibung in eine Roadmap aus Milestones zerlegen | Project Planner | `/ccmas:project-planning <Beschreibung oder Datei>` |
| `feature-planning` | Einen Milestone konzipieren und in Tasks zerlegen | Feature Planner | `/ccmas:feature-planning <Milestone oder Beschreibung>` |
| `supervisor` | Board lesen, bereinigen, Worktrees aufräumen, Milestones abschließen, den nächsten Schritt nennen | Supervisor | `/ccmas:supervisor` |
| `task-planning` | Einen Task umsetzungsreif machen oder ein Ticket aus einer Beschreibung anlegen; prüft die Risikoklasse | Planner | `/ccmas:task-planning <Ticket oder Beschreibung>` |
| `feature-parallel` | Ticket im Worktree test-driven umsetzen bis zum PR; Befunde beheben | Worker | `/ccmas:feature-parallel <Ticket>` |
| `code-review` | PR gegen Akzeptanzkriterien, Szenarien, technische Vorgaben und Doku prüfen | Reviewer | `/ccmas:code-review <Ticket>` |
| `merge` | Freigegebenen PR fertig machen und integrieren | Merger | `/ccmas:merge <Ticket>` |
| `init` | Ein Projekt einrichten ([Was im Projekt liegt](../README.md#was-im-projekt-liegt)) | Mensch | `/ccmas:init <Board>`; nur von Hand |

Für ein anderes Ticketsystem gibt es eine weitere Fassung von `ticket`; alle anderen Skills bleiben gleich.

Was jeder Skill erwartet und hinterlässt:

| Skill | Erwartet | Hinterlässt |
| --- | --- | --- |
| `project-planning` | Eine Projektbeschreibung; einen eigenen Worktree; den Menschen im Gespräch | `docs/roadmap.md` als Doku-PR, die Milestones im Backlog, das Ticket „Roadmap dokumentieren“ in To Review |
| `feature-planning` | Einen Milestone oder die Beschreibung eines Features; einen eigenen Worktree; den Menschen im Gespräch | `docs/milestones/<kurzname>.md` als Doku-PR, die Tasks mit Szenarien und Akzeptanzkriterien im Backlog, das Ticket „Konzept … dokumentieren“ in To Review |
| `supervisor` | Das Hauptverzeichnis auf dem Integrationsbranch, ohne offene Änderungen | Ein bereinigtes Board, aufgeräumte Worktrees, abgeschlossene Milestones und genau einen nächsten Schritt |
| `task-planning` | Einen Task im Backlog oder in To Plan, oder eine Beschreibung | Den Plan im Ticket, die Risikoklasse, das Ticket in To Do; oder eine Rückfrage |
| `feature-parallel` | Ein Ticket in To Do mit Plan, Szenarien und Akzeptanzkriterien; den Worktree des Tickets | Tests, Code, Doku und PR; das Ticket in To Review |
| `code-review` | Ein Ticket in To Review mit PR, „Am Zug“ Reviewer; den Worktree des PR | Das Urteil FREIGEGEBEN oder BEFUNDE für einen Commit, im Ticket und am PR |
| `merge` | Das Urteil FREIGEGEBEN, „Am Zug“ Merger; bei R3 die Freigabe des Menschen | Den gemergten PR, das Ticket in Done |
| `ticket` | Die Referenzen in der `CLAUDE.md` des Projekts | Wird von den anderen Skills aufgerufen: Tickets lesen, anlegen, verschieben, Felder setzen, kommentieren, Rückfragen |
| `init` | Ein Git-Repository mit dem Integrationsbranch `develop` und ein Board | `CLAUDE.md`, `docs/README.md`, `.gitignore`, `.claude/settings.json` mit Worktree-Einstellung und aktiviertem Plugin |

## Aufbau

| Bestandteil | Inhalt |
| --- | --- |
| `.claude-plugin/plugin.json` | Name `ccmas`, Version, Beschreibung |
| `agents/` | `project-planner`, `feature-planner`, `supervisor`, `planner`, `worker`, `reviewer`, `merger`: je eine schmale Rollendefinition mit Auftrag, Modell und Grenzen |
| `skills/ticket/` | Einzige Stelle, die das Ticketsystem kennt |
| `skills/project-planning/`, `feature-planning/`, `supervisor/`, `task-planning/`, `feature-parallel/`, `code-review/`, `merge/` | Ein Skill je Rolle |
| `skills/init/` | Richtet ein Projekt ein und enthält die Vorlagen für `CLAUDE.md` und `docs/README.md` |

- Gepflegt wird das Plugin nur im Code-Repository. Eine Änderung wirkt in jedem Projekt mit der nächsten Sitzung.
- Aktiviert wird es global (`--scope user`), für ein Verzeichnis (`--scope local`) oder für alle im Repository (`--scope project`). Ein Runner, der Claude Code selbst startet, braucht es global oder im Projekt. Die Befehle stehen in der README des Repositorys.

Alle Namen tragen das Präfix des Plugins: der Skill heißt `/ccmas:ticket`, der Agent `ccmas:worker`.

## Pflege

Bei jeder inhaltlichen Änderung die `version` in `.claude-plugin/plugin.json` erhöhen. Eine installierte Fassung bleibt sonst auf dem alten Stand. Ändert sich das Verhalten, wird die [Spezifikation](../docs/01-spezifikation/README.md) im selben PR angepasst.
