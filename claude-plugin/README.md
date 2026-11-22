# Plugin `ccmas`

Das Verhalten von CCMAS als Plugin für Claude Code: sieben Skills und ein Agent. Das Konzept dahinter steht in der [Spezifikation](../docs/01-spezifikation/README.md), Aktivieren und Deaktivieren in der [README des Projekts](../README.md#plugin-aktivieren-und-deaktivieren).

Installieren aus diesem Repository:

```bash
claude plugin marketplace add alexwenzel/ccmas
claude plugin install ccmas@alexwenzel
```

Das ist der veröffentlichte Stand von `main`. Entwicklungsstand und bestimmte Versionen: [Kanäle](../RELEASE.md#kanäle).

## Rollen

Eine Rolle ist eine Sitzung, deren erster Auftrag einen Skill aufruft. Alle Rollen lesen und pflegen das Ticket über den Skill `ccmas:ticket` ([Wer was darf](../docs/01-spezifikation/board.md#wer-was-darf)).

| Rolle | Aufruf | Arbeitsort | Modell | Bewegt das Ticket |
| --- | --- | --- | --- | --- |
| Planner | `/ccmas:project-planning`, `/ccmas:feature-planning` | Eigener Worktree | stark | legt Milestones im Backlog und Tasks in To Do an |
| Supervisor | `/ccmas:supervisor` | Hauptverzeichnis | stark | nach Done |
| Worker | `/ccmas:worker` | Worktree des Tickets | Standard | nach In Progress, nach To Review |
| Reviewer | `/ccmas:reviewer` | Worktree des Tickets, als Subagent des Workers | stark, aus dem Agent `ccmas:reviewer` | gar nicht |

- Planner, Supervisor und Reviewer laufen auf dem starken Modell, weil sie Entscheidungen vorbereiten und ihr Urteil den Merge freigibt. Der Worker läuft auf dem Standardmodell.
- Der Supervisor schreibt keinen Code und bewertet keinen PR. Der Worker mergt nicht. Der Reviewer ändert nichts.
- Stellt ein Runner den Reviewer, wird der Skill `reviewer` nicht gebraucht.

## Start einer Rolle

Mit Claude Code allein startet der Mensch jede Rolle selbst. `R` steht für `--permission-mode bypassPermissions`: Die Sitzung fragt vor keinem Schritt nach. Die Verbote aus `.claude/settings.json` des Projekts gelten trotzdem.

| Rolle | Start |
| --- | --- |
| Planner, Projekt | `claude R --model opus --worktree roadmap "/ccmas:project-planning <Beschreibung oder Datei>"` |
| Planner, Milestone | `claude R --model opus --worktree <Milestone> "/ccmas:feature-planning <Milestone oder Beschreibung>"` |
| Supervisor | `claude R --model opus "/ccmas:supervisor [Milestone]"` |
| Worker | `claude R --worktree <Ticket> "/ccmas:worker <Ticket>"` |
| Reviewer, von Hand | `claude R --worktree <Ticket> "/ccmas:reviewer <Ticket>"` |

- `--worktree <Ticket>` legt den Worktree unter `.claude/worktrees/<Ticket>` an. Gibt es ihn schon, wird er mit seinen Änderungen wieder geöffnet. Derselbe Befehl setzt deshalb ein begonnenes Ticket fort.
- Der Worktree zweigt vom ausgecheckten Stand des Hauptverzeichnisses ab, wenn `worktree.baseRef` auf `"head"` steht. Das setzt `/ccmas:init`. Das Hauptverzeichnis muss dafür auf dem Integrationsbranch stehen; den aktuellen Stand holt der Supervisor zu Beginn jeder Runde.
- Der Supervisor nennt nach jeder Runde unter „Zu starten“ die Befehle für die nächsten Tickets.
- Den Reviewer ruft der Worker selbst auf. Von Hand wird er nur gestartet, um ein Review zu wiederholen.

Mit einem Runner gelten dieselben Aufrufe; er startet die Sitzungen selbst ([Runner](../docs/01-spezifikation/runner.md)).

## Skills

Im Plugin heißen sie `ccmas:<name>`.

| Skill | Zweck | Aufruf |
| --- | --- | --- |
| `ticket` | Milestones, Tasks und Rückfragen lesen, anlegen, verschieben, Felder setzen, kommentieren. Einzige Stelle, die das Ticketsystem kennt; Fassung für Asana | Von den anderen Skills, nicht von Hand |
| `project-planning` | Eine Projektbeschreibung in eine Roadmap aus Milestones zerlegen | `/ccmas:project-planning <Beschreibung oder Datei>` |
| `feature-planning` | Einen Milestone konzipieren und in Tasks zerlegen, oder ein einzelnes Ticket ausplanen | `/ccmas:feature-planning <Milestone oder Beschreibung>` |
| `supervisor` | Freigegebene PRs mergen, Tickets starten und fortsetzen, Milestones zur Abnahme bringen, nennen, was auf den Menschen wartet | `/ccmas:supervisor [Milestone]` |
| `worker` | Ein Ticket von To Do bis zum freigegebenen PR bringen: Reife, Tests, Code, PR, Review, Befunde, rote Prüfungen, Konflikte | `/ccmas:worker <Ticket>` |
| `reviewer` | Den PR gegen seine Beschreibung prüfen und das Urteil für einen Commit an den PR schreiben | `/ccmas:reviewer <Ticket>`; sonst vom Worker |
| `init` | Ein Projekt einrichten ([Was im Projekt liegt](../README.md#was-im-projekt-liegt)) | `/ccmas:init <Board>`; nur von Hand |

Für ein anderes Ticketsystem gibt es eine weitere Fassung von `ticket`; alle anderen Skills bleiben gleich.

Was jeder Skill erwartet und hinterlässt:

| Skill | Erwartet | Hinterlässt |
| --- | --- | --- |
| `project-planning` | Eine Projektbeschreibung; einen eigenen Worktree; den Menschen im Gespräch | `docs/roadmap.md` im Integrationsbranch, die Milestones im Backlog |
| `feature-planning` | Einen Milestone oder eine Beschreibung; einen eigenen Worktree; den Menschen im Gespräch | `docs/milestones/<kurzname>.md` im Integrationsbranch, die Tasks mit Szenarien und Akzeptanzkriterien in To Do, als letzten den Abnahmetest |
| `supervisor` | Ohne Runner das Hauptverzeichnis auf dem Integrationsbranch, ohne offene Änderungen | Gemergte PRs, Tickets in Done, aufgeräumte Worktrees, gestartete oder genannte Schritte, Rückfragen zu Freigaben und Abnahmen |
| `worker` | Einen startbaren Task oder ein begonnenes Ticket; den Worktree des Tickets | Tests, Code, Doku und einen PR, der freigegeben und grün ist; oder eine Rückfrage |
| `reviewer` | Einen offenen PR zum Ticket; den Worktree auf dem Stand des PR | Das Urteil FREIGEGEBEN oder BEFUNDE für einen Commit, als Kommentar am PR |
| `ticket` | Die Referenzen in der `CLAUDE.md` des Projekts | Wird von den anderen Skills aufgerufen |
| `init` | Ein Git-Repository mit dem Integrationsbranch `develop` und ein Board | `CLAUDE.md`, `docs/README.md`, `.gitignore`, `.claude/settings.json`, bei Bedarf `.worktreeinclude`, die Labels für Freigaben |

Supervisor und Worker nennen für alles, was vom Runner abhängt, ein Verb und daneben den Weg ohne Runner. Ein Runner bringt seine Zuordnung als Regeln der Sitzung mit; dann gilt sie.

## Aufbau

| Bestandteil | Inhalt |
| --- | --- |
| `.claude-plugin/plugin.json` | Name `ccmas`, Version, Beschreibung |
| `skills/ticket/` | Einzige Stelle, die das Ticketsystem kennt |
| `skills/project-planning/`, `feature-planning/`, `supervisor/`, `worker/`, `reviewer/` | Die Abläufe der Rollen |
| `skills/init/` | Richtet ein Projekt ein und enthält die Vorlagen für `CLAUDE.md` und `docs/README.md` |
| `agents/reviewer.md` | Modell und gesperrte Schreibwerkzeuge für den Subagent des Skills `reviewer` |

- Gepflegt wird das Plugin nur in diesem Repository.
- Aktiviert wird es global (`--scope user`), für ein Verzeichnis (`--scope local`) oder für alle im Repository (`--scope project`). Ein Runner, der Claude Code selbst startet, braucht es global oder im Projekt. Die Befehle stehen in der README des Repositorys.

Alle Namen tragen das Präfix des Plugins: Der Skill heißt `/ccmas:ticket`, der Agent `ccmas:reviewer`.

## Pflege

Die `version` in `.claude-plugin/plugin.json` erhöht nur ein Release; Feature-PRs tragen ihre Änderung im [CHANGELOG](../CHANGELOG.md) ein ([Release und Versionierung](../RELEASE.md)). Eine installierte Fassung bleibt bis zum nächsten Release auf dem alten Stand. Ändert sich das Verhalten, wird die [Spezifikation](../docs/01-spezifikation/README.md) im selben PR angepasst.
