# Orchestrator einrichten

Zwei Regeldateien verbinden das Plugin `ccmas` mit Orchestrator. Sie enthalten nur die [Zuordnung](README.md#zuordnung), keinen Ablauf:

| Datei | Feld in den Projekteinstellungen | Gilt für |
| --- | --- | --- |
| [`orchestrator/agent-rules.md`](../../orchestrator/agent-rules.md) | `agentRulesFile` | Jede Worker-Sitzung |
| [`orchestrator/orchestrator-rules.md`](../../orchestrator/orchestrator-rules.md) | `orchestratorRulesFile` | Den Projekt-Orchestrator, also den Supervisor |

Orchestrator hängt den Inhalt beim Start einer Sitzung an ihren System-Prompt an. Eine Änderung wirkt erst in neuen Sitzungen.

## Voraussetzungen

1. Orchestrator ist installiert, `ao doctor` zeigt keine Zeile mit `FAIL`, und `ao agent ls --refresh` führt `claude-code` als installiert und angemeldet. Die Kommandozeile `gh` ist angemeldet.
2. Das Projekt ist eingerichtet: `/ccmas:init` hat die `CLAUDE.md` mit den Referenzen auf das Board angelegt, sie ist auf dem Integrationsbranch committet, und das Plugin ist global aktiviert ([Ein Projekt einrichten](../../README.md#ein-projekt-einrichten)).
3. Die beiden Regeldateien liegen im Repository des Projekts und sind auf dem Integrationsbranch committet. Für ein anderes Projekt als dieses werden sie dorthin kopiert; der Pfad gilt relativ zum Repository des Projekts.
4. Der Integrationsbranch verlangt keine Reviews bei GitHub. Die Freigabe des Reviewers von Orchestrator zählt dort nicht als Review.

## Projekt anlegen

```bash
ao project add --path <Pfad zum Repository> --name <Name> \
  --worker-agent claude-code --orchestrator-agent claude-code
ao project ls
```

Wird das Projekt in der App angelegt, startet sie den Projekt-Orchestrator gleich mit. `ao project add` tut das nicht.

## Projekteinstellungen

| Feld | Wert | Schalter von `ao project set-config` |
| --- | --- | --- |
| `defaultBranch` | Der Integrationsbranch | `--default-branch` |
| `agentRulesFile` | `orchestrator/agent-rules.md` | `--agent-rules-file` |
| `orchestratorRulesFile` | `orchestrator/orchestrator-rules.md` | `--orchestrator-rules-file` |
| `worker` | `claude-code`, Standardmodell | `--worker-agent`; das Modell in der App |
| `orchestrator` | `claude-code`, starkes Modell | `--orchestrator-agent`; das Modell in der App |
| `reviewers` | `claude-code`, starkes Modell | `--reviewer` |
| Rechte | `bypass-permissions`, damit die Sitzungen ohne Rückfragen laufen. Die Verbote aus `.claude/settings.json` des Projekts gelten weiter | `--permission` |
| `workersRequestReview` | `true` | In der App |
| `postCreate`, `preRemove` | Setup und Abbau je Worktree, siehe unten | `--post-create`, `--pre-remove` |

```bash
ao project set-config <Projekt> \
  --default-branch develop \
  --agent-rules-file orchestrator/agent-rules.md \
  --orchestrator-rules-file orchestrator/orchestrator-rules.md \
  --reviewer claude-code \
  --permission bypass-permissions
ao project get <Projekt> --json
```

Die Schalter ändern nur das Feld, das sie nennen. `--post-create`, `--pre-remove` und `--env` ersetzen jeweils die ganze Liste. `--config-json` ersetzt die gesamten Einstellungen und verwirft still, was im übergebenen JSON fehlt; nur verwenden, wenn das JSON alle vorhandenen Felder enthält.

## Setup und Abbau je Worktree

`postCreate` läuft nach dem Anlegen eines Worktrees, `preRemove` vor seinem Abbau, je mit `sh` im Worktree.

- **PATH.** Die Schritte laufen mit einem minimalen PATH; Werkzeuge aus nvm wie `node` und `pnpm` fehlen dort. Die Skripte setzen ihren PATH selbst.
- **Variablen.** Gesetzt sind `AO_WORKTREE_PATH` und `AO_SOURCE_TREE_PATH`, nicht die Kennung der Sitzung.
- **Ignorierte Dateien.** Orchestrator wertet `.worktreeinclude` nicht aus. Ein Schritt in `postCreate` kopiert die dort genannten Dateien aus dem Hauptverzeichnis.
- **Nichts Unversioniertes hinterlassen.** Was `postCreate` erzeugt, steht in `.gitignore`. Sonst gilt der Worktree als geändert, und `ao session cleanup` überspringt ihn.
- **`preRemove` gilt in der Fassung zum Zeitpunkt des Abbaus,** nicht des Starts. Scheitert ein Schritt, bleibt der Worktree stehen.

Ein Skript im Repository des Projekts, etwa `scripts/worktree-setup.sh`, eingetragen mit `--post-create 'sh scripts/worktree-setup.sh'`:

```sh
#!/bin/sh
# Werkzeuge, die der minimale PATH nicht kennt
export PATH="$HOME/.nvm/versions/node/<Version>/bin:$PATH"

# Die in .worktreeinclude genannten Dateien aus dem Hauptverzeichnis kopieren
list="$AO_SOURCE_TREE_PATH/.worktreeinclude"
if [ -f "$list" ]; then
  while IFS= read -r f || [ -n "$f" ]; do
    case "$f" in ''|'#'*) continue ;; esac
    [ -e "$AO_SOURCE_TREE_PATH/$f" ] || continue
    [ -e "$f" ] && continue
    mkdir -p "$(dirname "$f")"
    cp -pR "$AO_SOURCE_TREE_PATH/$f" "$f"
  done < "$list"
fi

# Danach das Setup des Projekts, etwa: pnpm install
```

Der Kopierschritt nimmt jede Zeile als Pfad einer Datei oder eines Ordners. Muster mit `*` löst er nicht auf.

## Benutzen

**Supervisor.** Den Orchestrator des Projekts in der App öffnen oder starten:

```bash
ao spawn --project <Projekt> --kind orchestrator --name supervisor
```

Läuft schon einer, liefert der Befehl diese Sitzung zurück. Der Auftrag kommt als Nachricht, in der App oder so:

```bash
ao send --session <Sitzung> --message "/ccmas:supervisor"
ao send --session <Sitzung> --message "/ccmas:supervisor <Milestone>"
```

Danach ist jede Meldung eines Workers eine neue Runde. Eine Antwort im Ticketsystem löst keine Runde aus; dafür schreibt der Mensch dem Supervisor oder nutzt einen der Wege unter [Zusätzlich nutzbar](README.md#zusätzlich-nutzbar).

**Worker von Hand.**

```bash
ao spawn --project <Projekt> --kind worker --name "<Ticket>" \
  --branch ticket/<Ticket> --prompt "/ccmas:worker <Ticket>"
```

**Planner.** Die Planung läuft im Gespräch mit dem Menschen. Sie startet mit Claude Code wie in der [README des Plugins](../../claude-plugin/README.md#start-einer-rolle) oder als Worker-Sitzung von Orchestrator:

```bash
ao spawn --project <Projekt> --kind worker --name "Konzept <Milestone>" \
  --prompt "/ccmas:feature-planning <Milestone>"
```

## Betrieb

| Zweck | Befehl |
| --- | --- |
| Sitzungen mit Status, PR und offenen Kommentaren | `ao session ls -p <Projekt>` |
| Einer Sitzung schreiben | `ao send --session <Sitzung> --message "…"` |
| Urteil zu einem PR | `ao review ls <Sitzung>` |
| Beendete Sitzung zurückholen | `ao session restore <Sitzung>` |
| Reste beendeter Sitzungen ansehen und entfernen | `ao session cleanup --dry-run -p <Projekt>`, dann ohne `--dry-run` |
| Bereitschaft prüfen | `ao doctor` |

- Was auf den Menschen wartet, steht im Board und in der Benachrichtigungsliste der App.
- Nach einer Änderung an einer Regeldatei den Supervisor neu starten; laufende Sitzungen behalten die alte Fassung.
- Vor dem Einsatz die Befehle mit `ao <Befehl> --help` gegen die installierte Fassung prüfen.
