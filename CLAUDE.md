# CCMAS – Projektregeln

Dieses Repository enthält CCMAS (Claude Code Multi Agent Setup): die Spezifikation und das Plugin `ccmas` für Claude Code, das sie umsetzt. Hier wird CCMAS selbst entwickelt. Zielprojekte, die mit CCMAS arbeiten, bekommen ihre eigene `CLAUDE.md` über `/ccmas:init`.

## Aufbau

| Pfad | Inhalt |
| --- | --- |
| `docs/01-spezifikation/` | Das Konzept. Maßgeblich dafür, was das Plugin tun soll |
| `docs/02-orchestrator/` | CCMAS mit Orchestrator als Runner |
| `claude-plugin/agents/` | Agent-Definitionen für Subagents der Skills, derzeit `reviewer` |
| `claude-plugin/skills/<name>/SKILL.md` | Je Ablauf ein Skill |
| `claude-plugin/skills/init/*.vorlage.md` | Vorlagen, die `/ccmas:init` in ein Zielprojekt kopiert |
| `orchestrator/` | Regeldateien für Orchestrator |
| `.claude-plugin/marketplace.json` | Quelle `alexwenzel` für die Installation |
| `RELEASE.md`, `CHANGELOG.md` | Ablauf der Veröffentlichung und was sich geändert hat |
| `.claude/skills/release/` | Skill `/release` nur für dieses Repository |
| `scripts/`, `.github/workflows/` | Prüfungen für lokal und CI |

Vor einer Änderung die betroffenen Teile der Spezifikation lesen, beginnend bei `docs/01-spezifikation/README.md`.

## Regeln

- **Spezifikation und Plugin gehören zusammen.** Wer das Verhalten ändert, ändert beide im selben Commit. Die Spezifikation beschreibt, was gilt; das Plugin setzt es um. Widersprechen sie sich, ist das ein Fehler, der gemeldet und behoben wird.
- **Jede Aussage steht an einer Stelle.** Rollen, Startbefehle und Skills stehen in `claude-plugin/README.md`, das Konzept in `docs/`. Andere Stellen verlinken, statt zu wiederholen.
- **Nur der Skill `ticket` kennt das Ticketsystem.** Kein anderer Skill nennt Asana oder dessen Werkzeuge.
- **Orchestrator steht nur in `docs/02-orchestrator/` und `orchestrator/`.** Die Spezifikation beschreibt den Ablauf mit Claude Code allein.
- **Version:** Die `version` in `claude-plugin/.claude-plugin/plugin.json` ändert nur ein Release-Branch, nie ein Feature-PR ([RELEASE.md](RELEASE.md)). Wer Plugin oder Spezifikation inhaltlich ändert, trägt stattdessen eine Zeile in `CHANGELOG.md` unter „Unveröffentlicht“ ein. Dokumente tragen keine eigene Version und kein YAML-Frontmatter; ihre Version ist Git.
- **Nichts als geprüft darstellen, was nicht geprüft ist.** Was nur nach der Dokumentation von Claude Code geschrieben und nicht ausprobiert wurde, steht in der README unter „Noch nicht geprüft“.

## Agents und Skills schreiben

- Ein Agent ist eine schmale Hülle: Rolle, Modell, was er tut, was er nicht tut, Verweis auf seinen Skill. Der Ablauf steht im Skill.
- Frontmatter: `name` wie der Ordner oder die Datei, `description` in einer Zeile ohne die Zeichenfolge Doppelpunkt-Leerzeichen, bei Skills mit Auftrag `argument-hint`.
- Ein Skill hat diese Abschnitte, soweit sie zutreffen: Voraussetzungen, Schritte, Wenn es nicht weitergeht, Grenzen, Abschluss.
- Jeder Skill prüft zuerst seine Voraussetzungen und ändert nichts, wenn eine nicht zutrifft.
- Skills verweisen aufeinander mit `ccmas:<name>`. Projektspezifisches (Kennungen, Prüfbefehle) steht nie im Skill, sondern in der `CLAUDE.md` des Zielprojekts.
- Angaben zu Claude Code (Schalter, Einstellungen, Felder) gegen die aktuelle Dokumentation prüfen, nicht aus dem Gedächtnis schreiben.

## Schreibweise

- Deutsch, sachlich, knapp. Bezeichner und Befehle im Original.
- Feste Begriffe: Milestone (auch Epic, Feature), Task, Rückfrage, Runner, Integrationsbranch, Hauptverzeichnis, Worktree.
- In Dokumenten Markdown-Links mit relativem Pfad, keine Obsidian-Wikilinks. Diagramme als Mermaid.

## Git

- `develop` ist der Integrationsbranch, `main` der veröffentlichte Stand und der Standardbranch auf GitHub.
- Gearbeitet wird auf einem Branch von `develop`. Kein `--force`, kein Push auf `main`.
- Auf `main` kommt nur ein Release (`release/X.Y.Z`) oder ein Hotfix (`hotfix/X.Y.Z`), jeweils per PR. Tags heißen `ccmas--vX.Y.Z`. Der Ablauf steht in [RELEASE.md](RELEASE.md), ausgeführt wird er mit `/release`.
- Commit-Nachrichten auf Deutsch, die erste Zeile sagt, was sich ändert.

## Prüfen vor einem Commit

```bash
claude plugin validate claude-plugin
claude plugin validate .
python3 -I scripts/links-pruefen.py
```

Das Skript prüft, dass jeder relative Link in den Markdown-Dateien auf eine vorhandene Datei und Überschrift zeigt. Wer eine Überschrift umbenennt oder eine Datei verschiebt, zieht die Links nach. Dieselben Prüfungen laufen in der CI.
