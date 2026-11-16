# CCMAS

CCMAS (Claude Code Multi Agent Setup) lässt KI-Agents mit festen Rollen ein Projekt planen und sein Board Ticket für Ticket abarbeiten: Project Planner, Feature Planner, Supervisor, Planner, Worker, Reviewer und Merger. Dieses Repository enthält das Verhalten dafür als Plugin für Claude Code. Die Sitzungen führt Claude Code selbst oder ein Werkzeug wie Orchestrator oder Herdr.

**Stand:** Plugin 0.3.0. Die Fassung 0.3.0 ist nach der Spezifikation 2.4.0 geschrieben und noch nicht mit `claude plugin validate` geprüft. Manifest, Installation und alle Befehle im Abschnitt „Plugin aktivieren und deaktivieren“ sind mit Claude Code 2.1.325 geprüft. In einer echten Sitzung mit Board ist das Plugin noch nicht erprobt.

## Aufbau

| Pfad | Inhalt |
| --- | --- |
| `claude-plugin/` | Das Plugin `ccmas`: sieben Agents, neun Skills ([Inhalt](claude-plugin/README.md)) |
| `orchestrator/` | Zwei Regeldateien, die das Plugin mit Orchestrator verbinden, und ihre Einrichtung ([Anleitung](orchestrator/README.md)) |
| `.claude-plugin/marketplace.json` | Macht dieses Repository zur Quelle, aus der Claude Code das Plugin installiert. Die Quelle heißt `alexwenzel` |
| `README.md` | Diese Datei |

Konzept und Spezifikation liegen nicht hier, sondern im Obsidian-Vault.

## Voraussetzungen

- Claude Code, angemeldet.
- Die Kommandozeile `gh`, angemeldet.
- In Claude Code eine Verbindung zum Ticketsystem (Asana).
- Im Zielprojekt: ein Git-Repository mit dem Integrationsbranch `develop` und ein Board mit den Spalten Backlog, To Plan, To Do, In Progress, To Review, Done und den Feldern aus der Spezifikation.
- Alle Rollen schreiben unter dem Konto ins Ticketsystem, mit dem die Verbindung angemeldet ist.

## Plugin aktivieren und deaktivieren

Das Plugin heißt `ccmas`, die Quelle `alexwenzel`. In den Befehlen steht deshalb `ccmas@alexwenzel`.

### Einmalig: die Quelle bekannt machen

```bash
claude plugin marketplace add /Users/alex/Projekte/alexwenzel/ccmas
```

Bei einem lokalen Pfad liest Claude Code das Plugin direkt aus diesem Ordner. Eine Änderung an den Dateien wirkt in der nächsten Sitzung, ohne neue Installation.

### Wo es gilt

| Geltung | Schalter | Gespeichert in |
| --- | --- | --- |
| Global: in jedem Verzeichnis | `--scope user` | `~/.claude/settings.json` |
| Ein Verzeichnis, nur für dich | `--scope local` | `.claude/settings.local.json` im Verzeichnis |
| Ein Verzeichnis, für alle im Repository | `--scope project` | `.claude/settings.json` im Verzeichnis, wird committet |

### Global

```bash
claude plugin install   ccmas@alexwenzel --scope user   # aktivieren
claude plugin disable   ccmas@alexwenzel --scope user   # deaktivieren, bleibt installiert
claude plugin enable    ccmas@alexwenzel --scope user   # wieder aktivieren
claude plugin uninstall ccmas@alexwenzel --scope user   # entfernen
```

### Nur in einem Verzeichnis

Die Befehle im Verzeichnis des Projekts ausführen:

```bash
cd /pfad/zum/projekt
claude plugin install   ccmas@alexwenzel --scope local   # aktivieren
claude plugin disable   ccmas@alexwenzel --scope local   # deaktivieren
claude plugin enable    ccmas@alexwenzel --scope local   # wieder aktivieren
claude plugin uninstall ccmas@alexwenzel --scope local   # entfernen
```

In allen anderen Verzeichnissen bleibt das Plugin aus. Mit `--scope project` statt `--scope local` gilt die Einstellung für alle, die im Repository arbeiten; jeder muss die Quelle dann einmal selbst bekannt machen.

### Global an, in einem Verzeichnis aus

```bash
cd /pfad/zum/projekt
claude plugin disable ccmas@alexwenzel --scope local   # hier aus, überall sonst an
claude plugin enable  ccmas@alexwenzel --scope local   # hier wieder an
```

### Nur für eine Sitzung, ohne Installation

```bash
claude --plugin-dir /Users/alex/Projekte/alexwenzel/ccmas/claude-plugin
```

### Prüfen

```bash
claude plugin list                       # Stand im aktuellen Verzeichnis: Geltung, an oder aus
claude plugin details ccmas@alexwenzel   # Skills, Agents, Kosten im Kontext
claude plugin validate claude-plugin     # Manifest und Dateien prüfen
```

In einer laufenden Sitzung zeigt `/plugin` dasselbe; `/reload-plugins` lädt geänderte Dateien neu. Eine laufende Sitzung merkt ein An- oder Abschalten erst danach oder nach einem Neustart.

Ist das Plugin an, kostet es in jeder Sitzung rund 600 Tokens für die Namen und Beschreibungen. Der volle Text eines Skills oder Agents wird erst geladen, wenn er gebraucht wird.

## Ein Projekt einrichten

Im Hauptverzeichnis des Zielprojekts, auf dem Integrationsbranch:

```bash
claude "/ccmas:init <Name oder Link des Boards>"
```

Der Skill legt `CLAUDE.md`, `docs/` und die Einstellungen für Worktrees an, aktiviert das Plugin für das Projekt, trägt die Kennungen von Board, Spalten und Feldern ein und überschreibt nichts. Danach die Dateien prüfen und committen.

## Rollen starten

Direkt mit Claude Code. `R` steht für `--permission-mode bypassPermissions`: Die Sitzung fragt dann vor keinem Schritt nach.

| Rolle | Start |
| --- | --- |
| Project Planner | `claude R --worktree roadmap --agent ccmas:project-planner "/ccmas:project-planning <Beschreibung oder Datei>"` |
| Feature Planner | `claude R --worktree <Milestone> --agent ccmas:feature-planner "/ccmas:feature-planning <Milestone oder Beschreibung>"` |
| Supervisor | `claude R --agent ccmas:supervisor "/ccmas:supervisor"` |
| Planner | `claude R --agent ccmas:planner "/ccmas:task-planning <Ticket oder Beschreibung>"` |
| Worker | `claude R --worktree <Ticket> --agent ccmas:worker "/ccmas:feature-parallel <Ticket>"` |
| Reviewer | `claude R --worktree <Ticket> --agent ccmas:reviewer "/ccmas:code-review <Ticket>"` |
| Merger | `claude R --worktree <Ticket> --agent ccmas:merger "/ccmas:merge <Ticket>"` |

- `--worktree <Ticket>` legt einen Worktree unter `.claude/worktrees/<Ticket>` mit dem Branch `worktree-<Ticket>` an. Gibt es ihn schon, wird er wieder geöffnet. So arbeiten Worker, Reviewer und Merger im selben Worktree.
- Der Worktree zweigt vom Stand des Hauptverzeichnisses ab, wenn `worktree.baseRef` auf `"head"` steht. Das setzt `/ccmas:init`. Das Hauptverzeichnis muss dafür auf dem Integrationsbranch stehen.
- Der Supervisor nennt nach jeder Runde den nächsten Schritt als Rolle und Ticket.
- Roadmap und Konzept entstehen als Doku-PR mit eigenem Ticket. Reviewer und Merger starten dafür im Worktree des Planners: `--worktree roadmap` oder `--worktree <Milestone>`. Der Name steht im Übergabe-Kommentar des Tickets.
- Worktree und Branch eines Tickets entfernt der Supervisor in der Runde nach dem Merge. Fragt Claude Code beim Beenden einer Sitzung, ob der Worktree bleiben soll, bleibt er bis dahin stehen.

## Mit Orchestrator oder Herdr

Beide Werkzeuge starten Claude Code selbst. Damit ihre Sitzungen das Plugin laden, muss es global aktiviert sein (`--scope user`) oder im Projekt (`--scope project`).

- **Orchestrator:** Zwei Regeldateien unter `orchestrator/` sagen den Sitzungen, wie sie mit dem Plugin arbeiten. Sie werden in den Projekteinstellungen von Orchestrator eingetragen; Einrichtung und Start stehen in der [Anleitung](orchestrator/README.md).
- **Herdr:** Noch nicht ausgearbeitet.

## Noch nicht geprüft

- Start mit `--agent ccmas:<rolle>` und einem Skill-Aufruf als erstem Auftrag.
- Ob der Text eines Agents als Hauptsitzung ausreicht; er ersetzt dort den Standard-Systemprompt von Claude Code.
- Ob der Skill `init` seine Vorlagen im Ordner des Plugins findet.
- Lesen und Schreiben im Ticketsystem aus den Skills.
- Das Zusammenspiel mit Orchestrator (Regeldateien unter `orchestrator/`) und mit Herdr.

## Pflege

- Dieses Repository ist die einzige Stelle, an der das Plugin gepflegt wird.
- Bei jeder inhaltlichen Änderung die `version` in `claude-plugin/.claude-plugin/plugin.json` erhöhen.
- Vor einem Commit: `claude plugin validate claude-plugin` und `claude plugin validate .`.
