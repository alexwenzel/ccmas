# Changelog

Alle Änderungen an Plugin und Spezifikation. Das Format folgt [Keep a Changelog](https://keepachangelog.com/de/1.1.0/), die Versionen folgen [RELEASE.md](RELEASE.md#versionsnummer).

## [Unveröffentlicht]

## [0.5.0] – 2026-11-22

Vereinfachung des Konzepts. Diese Fassung bricht mit 0.4.0; was in einem bestehenden Projekt zu tun ist, steht in der [README](README.md#umstieg-von-040).

### Hinzugefügt

- Skills `worker` und `reviewer`. Der Worker prüft zuerst die Reife des Tickets, setzt es test-driven um und besitzt es bis zum Merge. Der Reviewer läuft als abgezweigter Subagent mit dem Agent `ccmas:reviewer`.
- Verben für alles, was vom Runner abhängt: Starten, Fortsetzen, Laufendes erkennen, Review holen, Urteil lesen, Ereignisse, Melden, Mergen, Aufräumen. Die Skills nennen daneben den Weg ohne Runner; ein Runner bringt seine Zuordnung als Regeln der Sitzung mit ([Runner](docs/01-spezifikation/runner.md)).
- Freigaben in zwei Stufen als Labels am PR, `freigabe: nötig` und `freigabe: erteilt`. Wann eine nötig ist und wer entscheidet, steht in der `CLAUDE.md` des Projekts.
- Der Abnahmetest als letzter Task eines Milestones.
- `init` trägt Verbote für `--force` und für Push auf `main` und den Integrationsbranch ein und legt bei Bedarf `.worktreeinclude` und die Labels für Freigaben an.
- Abschnitte „Freigaben“ und „Review“ in der Vorlage der `CLAUDE.md`.

### Geändert

- Der Supervisor mergt den geprüften Commit, startet und setzt Tickets fort und meldet nur, was sich geändert hat. Den Stand des Reviews überträgt er nicht mehr ins Board.
- Eine Rolle startet als Skill-Aufruf im ersten Auftrag, ohne `--agent`.
- Die Planner mergen ihren Doku-PR nach dem Ja des Menschen selbst. Tasks entstehen ausgeplant in To Do.
- Board: fünf Spalten, fünf Felder, ein Kommentar je Übergabe in zwei Zeilen.
- Spezifikation neu gegliedert in Konzept, Board und Runner. Die Skills sind die Norm für den Ablauf.
- Orchestrator: neue Zuordnung und neue Regeldateien nach einer Prüfung mit Orchestrator 0.15.3 ([CCMAS mit Orchestrator](docs/02-orchestrator/README.md)).

### Entfernt

- Die Rollen Planner für einzelne Tickets und Merger mit den Skills `task-planning` und `merge`.
- Die Skills `feature-parallel` und `code-review`; an ihre Stelle treten `worker` und `reviewer`.
- Die Agents `project-planner`, `feature-planner`, `supervisor`, `planner`, `worker` und `merger`.
- Die Spalte To Plan, die Felder Risikoklasse, Am Zug, Ausgang und Review-Runde und die Risikoklassen R0 bis R3.
- Eigene Tickets für die Doku-PRs von Roadmap und Konzept.

## [0.4.0] – 2026-11-16

### Hinzugefügt

- Release- und Versionierungsablauf: [RELEASE.md](RELEASE.md), Skill `/release` im Repository, Prüfung per GitHub Actions (`.github/workflows/pruefen.yml`), `scripts/links-pruefen.py`.

### Geändert

- Die `version` in `plugin.json` steigt nur noch im Release-Branch, nicht mehr in jedem PR.
- README: Diagramme zur Planung und zum Abarbeiten eines Tasks, klarere Gliederung.

## [0.3.0] – 2026-11-16

Nicht als Release veröffentlicht, ohne Tag.

### Hinzugefügt

- Rollen Project Planner und Feature Planner mit den Skills `project-planning` und `feature-planning`.
- Milestones und deren Abnahme durch den Supervisor.
- Spezifikation und Orchestrator-Konzept im Repository (`docs/`, `orchestrator/`).

### Geändert

- Der Worker arbeitet test-driven: Tests aus den Szenarien des Tasks zuerst.
- Nach dem Merge räumt der Supervisor Worktree und Branch auf.

## [0.2.0] – 2026-11-15

Nicht als Release veröffentlicht, ohne Tag.

### Hinzugefügt

- Das Plugin `ccmas` mit Agents und Skills für Supervisor, Planner, Worker, Reviewer und Merger sowie `ticket` und `init`.

[Unveröffentlicht]: https://github.com/alexwenzel/ccmas/compare/ccmas--v0.5.0...develop
[0.5.0]: https://github.com/alexwenzel/ccmas/compare/ccmas--v0.4.0...ccmas--v0.5.0
[0.4.0]: https://github.com/alexwenzel/ccmas/commits/ccmas--v0.4.0
[0.3.0]: https://github.com/alexwenzel/ccmas/commits/develop
[0.2.0]: https://github.com/alexwenzel/ccmas/commits/develop
