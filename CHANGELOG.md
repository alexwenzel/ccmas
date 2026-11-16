# Changelog

Alle Änderungen an Plugin und Spezifikation. Das Format folgt [Keep a Changelog](https://keepachangelog.com/de/1.1.0/), die Versionen folgen [RELEASE.md](RELEASE.md#versionsnummer).

## [Unveröffentlicht]

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

[Unveröffentlicht]: https://github.com/alexwenzel/ccmas/compare/main...develop
[0.3.0]: https://github.com/alexwenzel/ccmas/commits/develop
[0.2.0]: https://github.com/alexwenzel/ccmas/commits/develop
