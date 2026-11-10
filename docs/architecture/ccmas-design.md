# CCMAS – Architektur-Snapshot

Portabler Architekturstand für das Repository. **Führend ist die Spezifikation**, nicht diese Datei:
Vault „w3entwickler“, Ordner „Eigene Projekte/ccmas“, Seite „Multi Agent Setup“, Version 1.7.0
(Stand der Spezifikation: 2026-11-10, Status `review`). Dieser Snapshot enthält nur, was für den Einstieg und
den aktuellen Code nötig ist, und ist keine Kopie der Spezifikation. Bei Widersprüchen gilt die Spezifikation;
der Snapshot wird dann nachgezogen.

## Zweck

CCMAS ist die lokale Kommandozentrale für agentengestützte Softwareentwicklung. Ein Claude-Supervisor
organisiert die Arbeit zwischen Obsidian, Asana und Code; unabhängige Agenten konzipieren, implementieren
und prüfen. Der Nutzer behält Überblick und Entscheidungsmöglichkeiten. CCMAS läuft lokal, die Claude-Modelle
rechnen beim Anbieter.

Stand dieses Repositorys: **Grundgerüst** (Ticket CC-22, Meilenstein M01) mit dem Skill-Katalog (CC-35) und den Profilen für Modell
und Effort (CC-46) und dem Go-Adapter zum Runner samt `profile verify` (CC-46); seit CC-27 (Meilenstein M03) kommt der
SQLite-Zustand (`internal/persistence`) mit `ccmas status` hinzu. Vorhanden sind die CLI-Hülle
(`version`, `skills`, `profile`, `agent`, `status`, `--help`, stabile Exit-Codes), das Python-Runner-Paket (seit CC-17 mit Runner-Einstieg und Nachweisen, siehe
`runtime-capabilities.md`) und die Qualitätsprüfungen. Alles Übrige ist geplant und in der CLI als „noch nicht verfügbar“ gekennzeichnet.

## Festgelegte Produktentscheidungen

| Thema | Festlegung |
| --- | --- |
| Hauptanwendung | Go für Dienst, CLI und Steuerung |
| Terminaloberfläche | Bubble Tea; keine Weboberfläche im MVP |
| Lokaler Zustand | SQLite unter Kontrolle des Go-Dienstes |
| Agenten-Anbindung | Offizielles Claude Agent SDK in einem schlanken Python-Runner |
| Runtime | Unverändertes lokales Claude-Code-Binary für Supervisor und Worker |
| Anmeldung / Kosten | Eigener Claude-Max-Zugang ist das gewünschte Betriebsmodell; kein stiller Wechsel zu API-Abrechnung |
| Git | Ausgangspunkt immer `develop`; eigener Worktree und Branch pro Auftrag; unabhängiges Review vor jedem Push |
| Integration | Standard ist der geprüfte PR auf `develop`; Auto-Modus nur ausdrücklich je Auftrag |
| Erweiterbarkeit | Austauschbare Runtime-Adapter; zunächst nur Claude |

Ein allgemeiner API-Client oder ein selbst geschriebener reiner CLI-Wrapper ersetzt die SDK-Vorgabe nicht.
Eine Abweichung von diesen Entscheidungen braucht eine ausdrückliche Entscheidung des Projektinhabers.

## Modulstruktur

Zielstruktur laut Spezifikation („CCMAS-Softwarestruktur“); vorhanden ist bisher nur, was unten als
„vorhanden“ markiert ist.

```text
cmd/ccmas/            Einstieg (dünn)                                   vorhanden
internal/cli/         Befehlsbaum, Ausgabe, Exit-Codes                 vorhanden
internal/domain/      Gemeinsame Verträge, Zustände, Prüflogik          vorhanden
internal/skills/      Skill-Katalog, Manifest, Snapshots (CC-35)        vorhanden
internal/profiles/    Rollenprofile, Agenten, Modell/Effort (CC-46)     vorhanden
internal/workers/     Adapter zum Python-Runner: runner/ (CC-46)        vorhanden (minimal)
internal/supervisor/  ProjectSnapshot, Validator, Policy, Planer (CC-14) vorhanden
internal/persistence/ SQLite-Zustand, Migrationen, Audit, nur lesende
                      Prüfung (CC-27); persistencetest/ für Tests       vorhanden
internal/orchestrator/ git/ integrations/
         monitoring/ api/ tui/                                          geplant
runners/claude_sdk/   Python-Paket claude_sdk_runner (src-Layout)       vorhanden (Runner-Einstieg, Nachweise)
contracts/            JSON-Schemas und gemeinsame Fixtures              vorhanden
config/               Beispielkonfigurationen models/agents (CC-46)     vorhanden
rules/                Rollenregeln roles.yaml (CC-33)                   vorhanden
prompts/             Systemprompts der Rollen (supervisor.md, CC-14)   vorhanden (nur supervisor)
scripts/ tests/                                                         geplant
.claude/skills/       Führender CCMAS-Skillbestand (ccmas-<name>)       vorhanden
                      samt manifest.yaml; der Platzhalter skills/ entfällt
docs/architecture/    dieser Snapshot                                   vorhanden
```

Go-Modul: `github.com/alexwenzel/ccmas`, Go-Direktive `1.27.3`. Die CLI nutzt
`github.com/spf13/cobra`, der Skill-Katalog und die Profilkonfiguration `go.yaml.in/yaml/v3` (strikt dekodiert), der SQLite-Zustand
`modernc.org/sqlite` **v1.61.0** (SQLite 3.54.1, reines Go ohne cgo; Begründung und Auswahlregel in [`persistence.md`](persistence.md)), alle gepinnt in `go.mod`/`go.sum`.

Die Auswerter der Rollenregeln (`PushAllowed`, `ConceptApprover`, `RepairStep`, `FeatureComplete`, `IntegrationModeAllowed`,
`NextSteps`, …) sind **Bausteine des Orchestrators** ([`workflow-rules.md`](workflow-rules.md)). Seit CC-14 ruft sie für
Planungsergebnisse des Supervisors der Validator in `internal/supervisor` auf ([`supervisor.md`](supervisor.md)); den Validator
ruft erst der Orchestrator (M03, geplant) vor jeder Ausführung auf. `internal/supervisor` enthält nur Zulässigkeitsregeln, keine
Planung; die Planung macht der Claude-Supervisor: Der Planer (`internal/supervisor/planner.go`, W14.2) ruft ihn mit Systemprompt (`prompts/supervisor.md`), Skills
und strukturierter Ausgabe auf; die Live-Szenarien folgen in W14.3. Die Systemprompts der Rollen liegen unter `prompts/` (Go-Paket `prompts`, eingebettet),
bleiben kurz (Rolle, Grenzen, Ausgabepflicht) und enthalten keine Fachlogik: die steht in den Skills.

Der Skill-Bestand (Manifest, Statusbedeutung, Hash-Definitionen, Snapshot-Layout, Ablauf bei Änderungen) ist in
[`skill-migration.md`](skill-migration.md) beschrieben. Rollenprofile, individuelle Agenten, Rangfolge, Fallback-Regel
und Speicher der Konfiguration (`models.yaml`, `agents.yaml`) stehen in [`profiles.md`](profiles.md).

## CLI-Gruppen und Exit-Codes

Der vorgesehene Befehlsraum („Vollständige CLI-Steuerung“) umfasst diese Gruppen:

| Gruppe | Funktion |
| --- | --- |
| `init`, `status` | Einrichten und Prüfen (`status` **verfügbar** seit CC-27 für den SQLite-Zustand; Dienst, Werkzeuge, Anmeldung und Projektzuordnung folgen; `init` folgt) |
| `start`, `stop`, `ui` | Dienst und Anzeige steuern |
| `project` | Projektzuordnung, Arbeitsorte, Integrationen |
| `skills` | Bestand prüfen, anzeigen und als Snapshot bereitstellen (**verfügbar** seit CC-35; Import und Zuordnung folgen) |
| `rules` | Regeln prüfen, importieren, Zuordnung verwalten |
| `agent`, `profile` | Rollen, individuelle Modelle, Effort, Skills (**verfügbar** seit CC-46; Limits folgen) |
| `task`, `feature`, `milestone` | Aufträge planen, starten, pausieren, fortsetzen, abbrechen |
| `question` | Offene Fragen lesen und beantworten |
| `review`, `merge` | Prüfungen, Befunde, Integrationsentscheidungen |
| `processes`, `events` | Bäume, Metriken, Ereignisse beobachten |
| `remote` | Fernsteuerung verwalten |

**Heute implementiert:** `ccmas version` (`ccmas <version>`, mit `--json` ein JSON-Objekt mit `version`),
`ccmas --version`, `ccmas --help` und `ccmas help [befehl]` sowie, seit CC-35, `ccmas skills list|show|validate|snapshot`
(Ausgabe wahlweise als `--json`; Details, Root-Auflösung und Exit-Codes in [`skill-migration.md`](skill-migration.md)).
Ebenfalls seit CC-46: `ccmas profile init|list|show|set|resolve|verify` und `ccmas agent list|show|set|unset`
(alle Befehle mit `--json`, die ändernden mit einem Ergebnisobjekt; `set` prüft vor dem Speichern und schreibt bei Fehlern nichts; Konfigurationsverzeichnis
`--config-dir`, `CCMAS_CONFIG_DIR` oder `<Softwareordner>/.ccmas/config`, Einzelheiten in [`profiles.md`](profiles.md)).
`ccmas profile resolve` hat seit CC-14 die Schalter `--supervisor-model`, `--supervisor-effort` und `--supervisor-skill` für die Wahl des Supervisors.
`ccmas profile verify [--live]` (seit CC-46) prüft Konfiguration, Auflösbarkeit, Runner-Projekt, `uv` und Claude-Binary ohne Prozessstart und Kontingent (Skill-Bestand und Rollenregeln geprüft geladen, seit CC-14); mit `--live` läuft je Modell ein Minimallauf über den Runner-Adapter. Alle übrigen Gruppen oben sind in der Hilfe unter „Geplante Befehlsgruppen (noch nicht verfügbar)“ aufgeführt.
Ihr Aufruf täuscht kein Verhalten vor.

**`ccmas status [--execution-dir <pfad>] [--root <pfad>] [--json]`** (seit CC-27) prüft den SQLite-Zustand und verändert nichts: keine
Installation, kein Start, keine neue Datei und kein neues Verzeichnis, keine Migration, keine Rechteänderung, keine Agentenausführung.
Der Ausführungsbereich ergibt sich aus `--execution-dir`, sonst `CCMAS_EXECUTION_DIR`, sonst `<Softwareordner>/.ccmas` (`--root` wie bei `profile`);
die Datenbank liegt fest unter `<Ausführungsbereich>/state/ccmas.db`. Die Textausgabe hat den Abschnitt „Zustand (SQLite)“ (Datei, Stufe, Leseweg,
Rechte, Journal-Modus, Schema mit ausstehenden Migrationen, Integrität, Grunddaten je Projekt, Hinweise, Befunde) und danach „Nicht geprüft“:
Dienstzustand (folgt mit CC-51) sowie Werkzeuge, Anmeldung und Projektzuordnung (folgen mit M06). `--json` liefert ein stabiles Objekt
(`checked_at`, `execution_dir`, `ok`, `state`, `not_checked`). Die Stufen der Spezifikation (vorhanden, konfiguriert, angemeldet, funktional
nachgewiesen) gelten für den SQLite-Zustand bis „funktional nachgewiesen“ **nur für den Lesepfad**; der Schreibpfad bleibt ungeprüft, „angemeldet“ entfällt.
Exit-Codes: 0 Datenbank fehlt oder ohne Befund (ausstehende Migrationen sind nur ein Hinweis), 1 mindestens ein Befund (zu neue Schemaversion,
geänderte Migration, abweichende Historie, `quick_check`, Fremdschlüssel, unsichere Rechte, unlesbare oder fremde Datei), 2 Bedienfehler. Die nur
lesende Prüfung, die Befundcodes und das Verhalten bei offenem Schreiber stehen in [`persistence.md`](persistence.md).

Die Version wird beim Bauen per `-ldflags "-X github.com/alexwenzel/ccmas/internal/cli.Version=<v>"`
gesetzt (`make build` nimmt `git describe`), Standard ist `dev`.

**Exit-Codes** (zentral in `internal/cli/exitcodes.go` festgelegt und getestet):

| Code | Konstante | Bedeutung |
| --- | --- | --- |
| 0 | `ExitOK` | Erfolg (auch `--help`, `version`) |
| 1 | `ExitError` | Laufzeitfehler oder Prüfbefund bei gültig aufgerufenem Befehl (zum Beispiel `skills validate` mit Befunden, `profile set` mit unzulässiger Kombination, `status` mit Befund im SQLite-Zustand) |
| 2 | `ExitUsage` | Bedienfehler: unbekannter Befehl, unbekanntes Flag, unerwartetes Argument |
| 3 | `ExitNotAvailable` | Befehlsgruppe ist vorgesehen, aber noch nicht verfügbar |

Meldungen und Fehler gehen nach stderr, reguläre Ausgaben nach stdout. Die Spezifikation sieht vor, dass die
exakten Unterbefehle und Ausgabeformate im ersten versionierten CLI-Vertrag fixiert werden; die Codes 0 bis 3
sind bis dahin die Festlegung dieses Repositorys.

## Python-Runner

`runners/claude_sdk/` enthält das Paket `claude_sdk_runner` (Python ≥ 3.12, verwaltet mit uv). Der Go-Dienst
startet je Agentenausführung einen eigenen Runner mit explizitem Arbeitsverzeichnis, Argumenten und kontrollierter
Umgebung. Der Runner ruft das offizielle Claude Agent SDK auf und verbindet Sitzungen, Ereignisse, Fragen,
Ergebnisse und Abbruch mit Go. Er ist keine zweite Aufgabenverwaltung: Limits, SQLite, Worktree-Locks und externe
Schreibaktionen bleiben in Go. Der Go-Adapter `internal/workers/runner` (CC-46, minimal; M04 erweitert ihn) startet den Runner-Prozess mit Allowlist-Umgebung und neutralem Arbeitsverzeichnis, führt den `ExecutionTracker` und liefert einen Ausführungsdatensatz ([`runtime-capabilities.md`](runtime-capabilities.md), Abschnitt 10). Der Runner-Einstieg (`python -m claude_sdk_runner`) setzt das Protokoll in einem ersten Umfang um (Start, Ereignisse, Interrupt, genau ein terminales Ergebnis); Fragen, Teilnachrichten und vollständige Steuerung folgen in M04. Skills gelangen nur über einen von Go geschriebenen, vom Runner nachgerechneten und im Init-Frame bestätigten Snapshot (CC-35, [`skill-migration.md`](skill-migration.md)) in eine Sitzung. Die Profile `readonly` und `scoped_write` sind Prüfprofile; ein unbeaufsichtigter Modus ist nicht freigegeben (Isolationsarchitektur für M04: innere Grenze in der CLI plus Spike eines äußeren `sandbox-exec`-Profils). Nachweise, Diagnosen, Isolationsbefund und Remote-Vorprüfung: [`runtime-capabilities.md`](runtime-capabilities.md).

### Gepinnte Abhängigkeiten und Kompatibilität

Maßgeblich sind `runners/claude_sdk/pyproject.toml` (exakte Pins) und `runners/claude_sdk/uv.lock`.

| Paket | Version | Zweck |
| --- | --- | --- |
| `claude-agent-sdk` (PyPI, offiziell) | `==0.2.180` | Agent SDK (Stand der Recherche 2026-11-10: aktuelle stabile Version, veröffentlicht 2026-11-09) |
| `pytest` (Dev) | `==9.2.0` | Tests |

Kompatibilität mit Claude Code (Recherche zum Stand 2026-11-10, aus SDK-Quelltext und Changelog):

- Das SDK-Wheel **bündelt** eine eigene Claude-Code-CLI (hier 2.1.321); `ClaudeAgentOptions(cli_path=…)` wählt stattdessen
  ein anderes Binary. CCMAS nutzt laut Spezifikation das konfigurierte lokale Binary, also `cli_path`.
- Das SDK erzwingt keine exakte CLI-Version. Es prüft beim Verbinden `claude -v` und warnt nur (Log-Warnung), wenn die
  Version unter `2.0.0` liegt; für die Option `verbatim_prompts` nennt es zusätzlich die Mindestversion `2.1.248`.
  Die lokal installierte Claude-Code-CLI **2.1.311** liegt über beiden Schwellen.
- Es gibt keine offizielle Kompatibilitätsmatrix „SDK-Version ↔ CLI-Version“. Das gebündelte CLI (2.1.321) ist neuer als
  das lokale (2.1.311). Die funktionale Kompatibilität der Kombination SDK 0.2.180 mit CLI 2.1.311 und die Anmeldung mit dem
  Max-Abonnement sind seit CC-17 in echten Läufen belegt (`runtime-capabilities.md`, Belege unter `evidence/`); mit anderen CLI-Versionen nicht.
- Unterstützte Python-Versionen laut SDK-Metadaten: 3.10 bis 3.13 (Entwicklungsstatus „Alpha“); CCMAS verlangt ≥ 3.12 und nutzt lokal 3.13 (`.python-version`).

## Verträge

Die Daten- und Nachrichtenverträge (Runner-Protokoll, Decision, Projekt/Profile/Arbeitsauftrag, Zustandsmodell,
Fehlerkategorien, Kompatibilitätsregeln) stehen in [`contracts.md`](contracts.md). Spätere Pakete verwenden
ausschließlich die Typen aus `internal/domain`.

## Reproduzierbare Prüfung eines sauberen Checkouts

Voraussetzungen: Go 1.27.3, Python ≥ 3.12 (uv besorgt bei Bedarf eine passende Version), uv (getestet mit 0.13.7), make.

```bash
make check     # test, race, go vet, build, py-test
make test      # go test ./...
make race      # go test -race ./...
make build     # bin/ccmas
make py-test   # cd runners/claude_sdk && uv run --frozen pytest -q
```

`--frozen` verwendet `uv.lock` unverändert und schlägt fehl, wenn die Umgebung davon abwiche. Die Prüfungen führen
keine Agenten aus und brauchen kein Claude-Konto. Die CI (`.github/workflows/ci.yml`, PRs und Pushes auf `develop`)
führt dieselben Prüfungen ohne Secrets und mit `permissions: contents: read` aus.

## Nicht eingecheckt

Laufzeitdaten, persönliche Konfiguration, Credentials, Logs und Worktrees bleiben außerhalb des getrackten Bestands
(`.gitignore`): `.venv/`, Caches, `.ccmas/` (darin `.ccmas/config` und, im Standardfall, `.ccmas/state` mit der SQLite-Datenbank) und `*.local.*` für lokale Konfiguration, `.env.*` (außer `.env.example`),
`/var/` und SQLite-/Logdateien für Laufzeitdaten, `.claude/worktrees/` und `bin/`. Persönliche lokale Ressourcen
(Vault-Pfad, Asana-GIDs, Konto) stehen nur in ungetrackter Konfiguration oder in der Regeldatei, nicht im Code.

## Agenten-Startanweisung

Ein Coding-Agent steigt so ein:

1. **Regeln lesen.** `CLAUDE.md` im Repository (nur Repo-Spezifisches) verweist auf die verbindliche Regeldatei:
   Vault „w3entwickler“, Ordner „Eigene Projekte/ccmas“, Datei `CLAUDE.md`. Ohne Vault-Zugriff: Projektdokument
   `claude/CLAUDE.md` im Claude-Projekt „CCMAS“. Dort stehen auch Asana-Projekt, Felder und Workflow.
2. **Spezifikation lesen.** Seite „Multi Agent Setup“ im selben Vault-Ordner (Version 1.7.0), insbesondere die
   Abschnitte zu Produktentscheidungen, Architektur, CLI, Konfiguration/Projektstruktur und Umsetzungsplan.
3. **Auftrag aus Asana.** Das Asana-Projekt „ccmas“ ist der operative Plan. Echte Abhängigkeiten prüfen, den ersten
   startbereiten Auftrag nehmen, dessen Schritte und Abnahmekriterien abarbeiten. Vorhandene Arbeit erhalten.
4. **Branch.** Von `develop` einen Branch `feature|fix|docs/CC-<n>-<kurz>` anlegen (Worktree unter `.claude/worktrees/`);
   PRs gehen nach `develop`. Commit-Nachrichten beginnen mit `CC-<n>:`. Der Projektinhaber merged.
5. **Zuerst testen.** Aussagekräftige Tests schreiben und gegen den alten Stand fehlschlagen lassen, dann implementieren.
6. **Prüfen.** Vor jedem Commit `make check` (oder einzeln `make test`, `make race`, `go vet ./...`, `make build`,
   `make py-test`) und das echte Ergebnis berichten.
7. **Grenzen.** Kein Push ohne unabhängiges Review, keine Secrets im Repo, keine API-Abrechnung statt Abo,
   kein reiner CLI-Wrapper als SDK-Ersatz, Test-Doubles nur in Tests.
