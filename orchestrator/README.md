# CCMAS mit Orchestrator

Zwei Regeldateien verbinden das Plugin `ccmas` mit Orchestrator:

| Datei | Feld in den Projekteinstellungen | Gilt für |
| --- | --- | --- |
| `orchestrator/agent-rules.md` | `agentRulesFile` | Jede Worker-Sitzung: Planner und Worker |
| `orchestrator/orchestrator-rules.md` | `orchestratorRulesFile` | Die Orchestrator-Sitzung: Supervisor und Integrieren |

Orchestrator hängt den Inhalt beim Start einer Sitzung an die Anweisungen der Rolle an. Eine Änderung wirkt erst in neuen Sitzungen.

**Stand:** Geschrieben nach der Dokumentation von Orchestrator, noch in keiner Sitzung erprobt.

## Voraussetzungen

1. Das Plugin ist global aktiv: `claude plugin install ccmas@alexwenzel --scope user` (siehe [README des Projekts](../README.md)).
2. Das Projekt ist eingerichtet: `/ccmas:init` hat die `CLAUDE.md` mit den Referenzen auf das Board angelegt, und sie ist committet.
3. Die beiden Regeldateien liegen im Repository des Projekts und sind auf dem Integrationsbranch committet. Für ein anderes Projekt als dieses werden sie dorthin kopiert; der Pfad gilt immer relativ zum Repository des Projekts.

## Projekteinstellungen

In der App unter Project Settings eintragen:

| Feld | Wert |
| --- | --- |
| `agentRulesFile` | `orchestrator/agent-rules.md` |
| `orchestratorRulesFile` | `orchestrator/orchestrator-rules.md` |
| `defaultBranch` | `develop` |
| `worker` | Agent `claude-code`, Rechte `bypass-permissions` |
| `orchestrator` | Agent `claude-code`, Rechte `bypass-permissions`, das stärkere Modell |
| `reviewers` | `claude-code` |
| `workersRequestReview` | `true` |

Über die Kommandozeile:

```bash
ao project ls                          # Kennung des Projekts
ao project get <Projekt> --json        # aktuelle Einstellungen ansehen
ao project set-config <Projekt> --help # die Schalter für einzelne Felder
```

`ao project set-config` hat Schalter für einzelne Felder; ihre Namen stehen in der Hilfe. `--config-json` dagegen ersetzt die gesamten Einstellungen des Projekts und verwirft still, was im übergebenen JSON fehlt. Nur verwenden, wenn das JSON alle vorhandenen Felder enthält.

## Benutzen

Die Orchestrator-Sitzung starten, in der App oder so:

```bash
ao spawn --name "Supervisor" --kind orchestrator --prompt "/ccmas:supervisor"
```

Sie liest das Board, bestimmt den nächsten Schritt, startet dafür Worker-Sitzungen, trägt Reviews ins Ticket ein und integriert PRs der Risikoklassen R0 bis R2. Bei R3 legt sie eine Freigabe-Rückfrage an und wartet.

Einen einzelnen Schritt von Hand starten:

```bash
ao spawn --name "CC-12 Planung" --kind worker --prompt "/ccmas:task-planning CC-12"
ao spawn --name "CC-12" --kind worker --branch ticket/CC-12 --prompt "/ccmas:feature-parallel CC-12"
```

## Abweichungen vom Ablauf ohne Orchestrator

- **Review:** Die Prüfung macht der Reviewer-Lauf von Orchestrator, nicht der Skill `/ccmas:code-review`. Das Urteil trägt die Orchestrator-Sitzung ins Ticket ein.
- **Merge:** Es gibt keine eigene Merger-Sitzung. Konflikte und fehlgeschlagene Prüfungen meldet Orchestrator an den Worker; den Merge löst die Orchestrator-Sitzung mit `ao pr merge` aus.
- **Supervisor:** Die Orchestrator-Sitzung läuft dauerhaft, nicht als frische Runde je Durchlauf.

## Noch nicht geprüft

- Ob eine von Orchestrator gestartete Sitzung das Plugin und die `CLAUDE.md` lädt und ob der Skill-Aufruf im ersten Auftrag wirkt.
- Ob Orchestrator die Regeldatei aus dem Hauptverzeichnis oder aus dem Worktree der Sitzung liest. Deshalb gehören die Dateien auf den Integrationsbranch.
- Die genaue Form von `ao report` mit mehreren Schaltern und die Art des Merges bei `ao pr merge`.
- Ob die Orchestrator-Sitzung Meldungen der Worker als neue Runde aufnimmt.
