# Runner

Der Runner führt die Sitzungen: Er startet sie, gibt jedem Ticket einen Worktree und stellt Ereignisse zu. Die Skills nennen dafür nur ein Verb. Was das Verb bedeutet, wenn Claude Code allein läuft, steht in dieser Tabelle und, für die Sitzung lesbar, im jeweiligen Skill. Jeder andere Runner hat ein eigenes Dokument mit derselben Tabelle und bringt seine Zuordnung als Regeln der Sitzung mit.

## Verben

| Verb | Bedeutung | Claude Code allein |
| --- | --- | --- |
| Starten | Eine Rolle mit ihrem Auftrag in einer eigenen Sitzung; der Worker in einem eigenen Worktree vom Integrationsbranch | Der Supervisor nennt den Befehl, der Mensch startet ihn: `claude --worktree <Ticket> "/ccmas:worker <Ticket>"` |
| Fortsetzen | Die Arbeit an einem begonnenen Ticket wieder aufnehmen, ohne zweite Sitzung | Derselbe Befehl wie beim Starten: Claude Code öffnet den vorhandenen Worktree mit seinen Änderungen |
| Laufendes erkennen | Welche Sitzung an welchem Ticket arbeitet | Der Sperrvermerk in `git worktree list --porcelain` nennt die PID der Sitzung. Sie läuft, wenn dieser Prozess lebt; der Vermerk allein bleibt nach einem Lauf mit `-p` und nach einem Abbruch stehen |
| Review holen | Ein Reviewer mit frischem Kontext auf dem Stand des PR | Der Worker ruft `ccmas:reviewer`; der Skill läuft als abgezweigter Subagent, und sein Ergebnis kommt beim Worker an |
| Urteil lesen | Urteil und geprüfter Commit | Der letzte Kommentar am PR, der mit `Urteil:` beginnt |
| Ereignisse | Rote Prüfung und Konflikt erreichen den Worker | Der Worker wartet selbst mit `gh pr checks --watch`; endet seine Sitzung vorher, setzt der Supervisor ihn fort |
| Melden | Der Supervisor erfährt das Ergebnis einer Sitzung | Die Ausgabe der Sitzung; der Stand steht in Ticket und PR |
| Mergen | Den geprüften Commit integrieren | `gh pr merge --squash --match-head-commit <Commit>` |
| Aufräumen | Sitzung, Worktree und Branch nach dem Merge | Läuft die Sitzung nicht mehr: `git worktree unlock`, dann `git worktree remove` ohne `--force`. Den Branch lokal und auf `origin` löschen, und nur, wenn der PR gemergt ist; lokal mit `git branch -D`, weil Git einen Squash-Merge nicht als gemergt erkennt |
| Runde auslösen | Der Supervisor wird aktiv | Der Mensch startet eine Runde |

Starten, Fortsetzen, Laufendes erkennen, Urteil lesen, Mergen und Aufräumen gehören dem Supervisor, Review holen, Ereignisse und Melden dem Worker.

## Mit Claude Code allein

- **Der Mensch startet jede Sitzung.** Die Befehle stehen in der [README des Plugins](../../claude-plugin/README.md#start-einer-rolle). Der Supervisor nennt nach jeder Runde, was zu starten ist.
- **Das Hauptverzeichnis steht auf dem Integrationsbranch.** `claude --worktree <Name>` legt den Worktree unter `.claude/worktrees/<Name>` auf dem Branch `worktree-<Name>` an. Mit `worktree.baseRef` auf `"head"` zweigt er vom ausgecheckten Stand des Hauptverzeichnisses ab, nicht vom Standardbranch des Repositorys. Der Supervisor holt deshalb zu Beginn jeder Runde den aktuellen Stand.
- **Ignorierte Dateien.** Was in `.worktreeinclude` steht und von Git ignoriert wird, kopiert Claude Code in jeden neuen Worktree, etwa `.env`.
- **Im Hauptverzeichnis wird nichts geändert.** Planner und Worker arbeiten in Worktrees, der Supervisor ändert keine Datei.

## Wie Claude Code das Verhalten lädt

| Baustein | Was es ist | Wann es wirkt |
| --- | --- | --- |
| Regeldatei (`CLAUDE.md`) | Dauerhafte Regeln des Projekts | In jeder Sitzung, von Anfang an |
| Skill | Anleitung (`SKILL.md`) für genau einen Ablauf | Wenn er beim Namen aufgerufen wird (`/ccmas:<name>`) oder die Aufgabe zu seiner Beschreibung passt |
| Agent | Eine Rollendefinition mit Modell und Werkzeugen | In CCMAS nur als Subagent des Skills `reviewer` |
| Plugin | Ein Paket aus Skills und Agents | Solange es aktiviert ist |

- **Eine Rolle ist ein Skill-Aufruf als erster Auftrag,** etwa `claude "/ccmas:supervisor"`. Die Sitzung behält den Systemprompt von Claude Code; ein Agent als Hauptsitzung würde ihn ersetzen.
- **Modell und Rechte kommen vom Start der Sitzung** (`--model`, `--permission-mode`). Das Feld `model` im Frontmatter eines Skills gilt nur für den laufenden Zug, und bei Agents aus einem Plugin ignoriert Claude Code `permissionMode`, `hooks` und `mcpServers`.
- **Der Skill `reviewer` läuft als abgezweigter Subagent** (`context: fork`) mit dem Agent `ccmas:reviewer`. Der Agent gibt ihm ein eigenes Modell und sperrt `Write`, `Edit` und `NotebookEdit`. Im Feld `agent:` ist der volle Name nötig; der kurze Name greift nicht und meldet keinen Fehler. Schreibwerkzeuge aus Verbindungen (MCP) sperrt das nicht.
- **Von jedem Skill ist zunächst nur die Beschreibung geladen,** der volle Text erst beim Aufruf.

## Was im Plugin und im Projekt liegt

| Ort | Inhalt |
| --- | --- |
| Plugin, Skills | `ticket`, `project-planning`, `feature-planning`, `supervisor`, `worker`, `reviewer`, `init` |
| Plugin, Agent | `reviewer`: Modell und gesperrte Schreibwerkzeuge für den Subagent des gleichnamigen Skills |
| Global, `~/.claude/settings.json` | Plugin aktiviert (`--scope user`); im Projekt wird es nicht aktiviert |
| Projekt, `CLAUDE.md` | Referenzen (Board, fünf Spalten, fünf Felder, Repository, Integrationsbranch, Mensch), Prüfbefehle, Arbeitsweise, Freigaben, der Maßstab für das Review in Kurzform, Grenzen |
| Projekt, `.claude/settings.json` | `worktree.baseRef` auf `"head"`, Verbote für `--force` und für Push auf `main` und den Integrationsbranch |
| Projekt, `.worktreeinclude` | Von Git ignorierte Dateien, die jeder Worktree braucht |
| Projekt, `docs/` | Roadmap, Konzepte der Milestones, Dokumentation des Codes |
| Repository auf GitHub | Die Labels `freigabe: nötig` und `freigabe: erteilt` |

Was `/ccmas:init` davon anlegt, steht in der [README des Projekts](../../README.md#was-im-projekt-liegt).

## Geprüft

Am 2026-11-22 mit Claude Code 2.1.323 an einem Wegwerf-Repository ausprobiert:

| Verhalten | Ergebnis |
| --- | --- |
| Skill-Aufruf als erster Auftrag, interaktiv und mit `-p` | Der Skill läuft |
| `agent:` im Skill mit einem Agent aus demselben Plugin | Wirkt nur mit vollem Namen; das Ergebnis des Subagenten kommt in der aufrufenden Sitzung an |
| `claude --worktree <Name>` ein zweites Mal | Öffnet denselben Worktree, nicht committete Änderungen bleiben |
| Sperrvermerk des Worktrees | Bleibt nach `-p` und nach `kill -9` stehen; nur das normale Ende einer interaktiven Sitzung entfernt ihn |
| `.worktreeinclude` | Die ignorierte Datei liegt im neuen Worktree |
| `gh pr merge --squash --match-head-commit` | Verweigert bei geändertem Commit, mergt beim aktuellen |

Die Skills selbst sind damit nicht erprobt. Was fehlt, steht in der [README](../../README.md#noch-nicht-geprüft).

## Andere Runner

- [Orchestrator](../02-orchestrator/README.md)
