# CCMAS – Profile: Modell, Effort und Skills (CC-46)

Stand: Ticket CC-46, Meilenstein M02. **Führend ist die Spezifikation** „Multi Agent Setup“,
Abschnitte „Aufwand, Modell und Effort“, „Projekt, Profile und Arbeitsauftrag“, „Parameter und Profile“ und
„Vollständige CLI-Steuerung“. Diese Datei hält fest, was `internal/profiles`, `config/` und die Befehle
`ccmas profile` und `ccmas agent` heute tun. Seit CC-46 übergibt der Go-Adapter `internal/workers/runner` das aufgelöste Profil an
den Runner, und `ccmas profile verify [--live]` prüft Profile, Runner und Claude-Binary (Abschnitt „Prüfung mit `profile verify`“).

## Überblick

| Teil | Ort |
| --- | --- |
| Auflösung, Konfiguration, Speicher | `internal/profiles` |
| Beispielkonfigurationen (Startprofile), eingebettet in das Binary | `config/models.example.yaml`, `config/agents.example.yaml`, Paket `config` |
| Vertrag `AgentProfile` (Schema, Typ, Fixtures) | `contracts/project-v1.schema.json`, `internal/domain`, [`contracts.md`](contracts.md) |
| Befehle | `internal/cli` (`profile.go`, `agent.go`, `verify.go`) |
| Übergabe des Profils an den Runner (Adapter) | `internal/workers/runner`, [`runtime-capabilities.md`](runtime-capabilities.md) Abschnitt 10 |

Modell-IDs stehen **nur** in Konfiguration (`models.yaml`, `agents.yaml`), nie im Go-Code. Die Beispiele enthalten die
vollen IDs `claude-opus-5-5`, `claude-sonnet-5-5` und `claude-haiku-5-5`. Es gibt keine zweite Modellliste: der
`domain.Catalog` für `Decision.Check` wird aus den Capabilities abgeleitet (`profiles.BuildCatalog`).

## Rangfolge und Erlaubnis

Je Feld **einzeln** (Modell, Effort, Skills) gilt die erste gesetzte Stufe:

1. ausdrücklicher Auftragswert (`Overrides.Order`, CLI: `profile resolve --model/--effort/--skill`)
2. individueller Agentenwert (`agents.<id>` in `agents.yaml`)
3. Wahl des Supervisors (`Overrides.Supervisor`; CLI seit CC-14: `profile resolve --supervisor-model/--supervisor-effort/--supervisor-skill`)
4. Rollenstandard (`roles.<rolle>` in `agents.yaml`)

Alles nur innerhalb der Rollenerlaubnis: `allowed_models` und `allowed_efforts` des Rollenprofils; fehlt die Angabe,
sind alle konfigurierten erlaubt. Die Erlaubnis gilt für jede Stufe, auch für den Rollenstandard selbst; bei einer
genehmigten Reduktion (unten) müssen angeforderte **und** wirksame Stufe erlaubt sein. Fehlerreihenfolge: unbekanntes
Modell (`unknown_model`) vor Erlaubnis (`not_allowed_for_role`) vor Unterstützung (`unsupported_effort`).

Skills: eine nicht leere Liste der höchsten Stufe, die Skills setzt; sonst die Standardausstattung der Rolle aus dem
Skill-Katalog (`default_for`). Jeder Skill muss im Katalog freigegeben (`approved`) und für die Rolle erlaubt sein;
Fehler sind `*skills.Error` mit den Codes `unknown_skill`, `skill_not_approved`, `skill_not_allowed_for_role`. Das
Ergebnis ist aufsteigend sortiert und nie `nil` (JSON `[]`). Die Herkunft `role` umfasst auch die Standardausstattung
des Katalogs. Das Auflösen liest keine Skill-Dateien; Dateien und Hashes prüft erst der Snapshot (`ccmas skills snapshot`,
[`skill-migration.md`](skill-migration.md)).

## Kein unsichtbarer Fallback

Unterstützt das Modell den angeforderten Effort nicht, wird abgelehnt (`unsupported_effort`; auch bei unbekannter
Stufe). Ausnahme: `models.yaml` genehmigt die Reduktion ausdrücklich.

- Ein Eintrag unter `fallbacks` gilt nur für **genau dieses Modell und genau diese angeforderte Stufe**. Ein
  Fallback eines anderen Modells oder einer anderen Stufe wird nicht angewendet.
- Ein Eintrag ist nur gültig, wenn das Modell die angeforderte Stufe nicht unterstützt und `effective` genau der
  dokumentierten Regel entspricht: die höchste unterstützte Stufe auf oder unter der angeforderten („If you set a level
  the active model does not support, Claude Code falls back to the highest supported level at or below the one you
  set.“, <https://code.claude.com/docs/en/model-config>). Sonst ist die Datei `invalid_config`.
- Die Reduktion steht im aufgelösten Profil: `requested_effort` (angefordert), `effort` (wirksam) und `fallback`
  `{kind: effort_reduction, from, to, source}`; `source` ist die Quelle der Genehmigung.

**Der wirksame Effort ist abgeleitet, nicht gemessen.** Das SDK meldet den Effort nicht
([`runtime-capabilities.md`](runtime-capabilities.md)); der Wert folgt aus Konfiguration und dokumentierter Regel.
Das Beispiel genehmigt keinen Fallback: Opus 5.5, Sonnet 5.5 und Haiku 5.5 unterstützen laut Tabelle „Effort levels by
model“ (geprüft am 2026-11-10) alle fünf Stufen.

## Konfigurationsdateien

Verzeichnis: `--config-dir`, sonst `CCMAS_CONFIG_DIR`, sonst `<CCMAS-Softwareordner>/.ccmas/config`. Den Softwareordner
bestimmen `--root`, `CCMAS_HOME` oder das Verzeichnis über dem Binary wie bei `ccmas skills`; das Arbeitsverzeichnis
wird nie verwendet. `.ccmas/` ist ignoriert, die Dateien werden nicht eingecheckt. Sie enthalten **keine Zugangsdaten**;
unbekannte Schlüssel sind Fehler.

**`models.yaml`** (`schema_version: 1`, `revision`, `source`, `checked_on`, `policies`, `models.<id>.efforts`,
optional `fallbacks[]` mit `model`, `requested`, `effective`, `source`): Quelle und Prüfdatum sind Pflicht. Die bekannten
Policy-Profile sind `readonly` und `scoped_write` (Prüfprofile des Runners; ein unbeaufsichtigter Modus ist nicht
freigegeben).

**`agents.yaml`** (`schema_version: 1`, `revision`, `roles.<rolle>` für jede der neun Rollen, `agents.<id>`):
Rollenprofil mit `runtime`, `model`, `effort`, `skills` (leer: Standardausstattung), `policy`, optional
`allowed_models`, `allowed_efforts`. Ein Agent hat `role` und optional `model`, `effort`, `skills`; leere Felder erben
vom Rollenprofil. Agenten-IDs folgen dem ID-Format, höchstens 64 Zeichen.

Beim Schreiben normalisiert `ccmas` die Datei (sortiert, Kopfkommentar); **Kommentare in der Datei gehen dabei
verloren**. Die Beispieldateien selbst werden unverändert kopiert.

### Startprofile

| Rolle | Modell | Effort | Policy |
| --- | --- | --- | --- |
| `supervisor`, `reviewer`, `concept_reviewer` | `claude-opus-5-5` | `high` | `readonly` |
| `implementer` | `claude-sonnet-5-5` | `high` | `scoped_write` |
| `concept` | `claude-sonnet-5-5` | `high` | `readonly` |
| `documentation` | `claude-sonnet-5-5` | `medium` | `readonly` |
| `project_setup`, `completion`, `question_handler` | `claude-sonnet-5-5` | `high` | `readonly` |

Festlegung des Projektinhabers vom 10.11.2026. Ein Test löst alle neun Startprofile aus den Beispieldateien gegen den
echten Skill-Katalog auf.

## Auflösung (`internal/profiles`)

```go
r, err := profiles.NewResolver(cfg, skillCatalog)                // prüft cfg, kopiert sie tief
p, err := r.ResolveProfile(role, agentID, profiles.Overrides{Order: …, Supervisor: …}, caps)
cat := profiles.BuildCatalog(caps, skillCatalog)                  // domain.Catalog für Decision.Check (seit CC-14 mit Fallbacks und Skill-Rollen)
```

- **Empfänger.** `ResolveProfile` hat einen Zeiger-Empfänger auf `*Resolver`. Der Resolver hält den Skill-Katalog (selbst
  ein Zeigertyp) und eine eigene tiefe Kopie der Konfiguration und ist nach `NewResolver` unveränderlich; er kann
  nebenläufig benutzt werden. Die Signatur entspricht dem Ticket.
- **Unveränderlicher Wert.** Das Ergebnis teilt keinen Speicher mit dem Resolver oder der übergebenen Konfiguration
  (Kopien, auch der Skill-Liste). Eine später geänderte Konfiguration (`set`) ändert kein bereits aufgelöstes Profil;
  neue Werte gelten nur für neue Ausführungen und brauchen einen neuen Resolver (neue Revision, neue `version`).
- **`profile_id`** ist `<rolle>` oder `<rolle>:<agent>`, **`version`** die Konfigurationsrevision: Revision von
  `agents.yaml` plus Revision von `models.yaml` minus 1. Sie ist 1 nach `profile init` und wächst mit jeder Änderung
  einer der beiden Dateien. `requested_model` gleicht immer `model`; `value_sources` benennt je Feld die Herkunft.
- **Absicherung.** Jedes Ergebnis besteht `Catalog.CheckProfile` aus denselben Quellen.
- **Domain-Katalog und Fallbacks (seit CC-14).** `BuildCatalog` übernimmt die genehmigten Reduktionen aus `models.yaml`
  (`Catalog.Fallbacks`) und je freigegebenem Skill die Rollen, die er erhalten darf (`Catalog.SkillRoles`). `Decision.Check`
  erkennt deshalb eine genehmigte Reduktion an (`CheckEffective` liefert die wirksame Stufe und `EffortFallback`) und weist einen
  Skill zurück, der für die Rolle der Auswahl nicht erlaubt ist. Die Wahl des Supervisors darf Fallbacks wie jede andere Stufe
  nutzen; die Reduktion steht sichtbar im aufgelösten Profil.

### Fehlercodes

Stabile Codes, per `errors.Is(err, &profiles.Error{Code: …})` prüfbar (optional mit `Field`), Meldungen deutsch:

| Code | Bedeutung |
| --- | --- |
| `unknown_model` | Modell nicht in `models.yaml` |
| `unsupported_effort` | Stufe vom Modell nicht unterstützt (oder unbekannt) und kein genehmigter Fallback |
| `not_allowed_for_role` | Modell oder Effort außerhalb der Rollenerlaubnis |
| `unknown_agent`, `agent_role_mismatch` | Agent nicht angelegt beziehungsweise gehört zu anderer Rolle |
| `invalid_config` | Datei verletzt Format oder Invarianten; unbekannte Policy |
| `invalid_request` | unbekannte Rolle, Skill doppelt angefordert |
| `not_initialized` | Dateien fehlen (`ccmas profile init`) |
| `conflict` | Schreibkonflikt (Revision geändert oder Sperrdatei vorhanden) |
| `storage` | Dateisystemfehler |

Skill-Fehler tragen die Codes des Pakets `skills` (siehe oben).

## Speicher

Die Konfiguration bleibt in den beiden Dateien (`profiles.Store`); SQLite ersetzt sie nicht. Seit CC-27 speichert SQLite die
**aufgelösten Profilversionen je Ausführung** (`profile_versions`, unveränderlich; ein Versuch verweist mit `profile_id` und `profile_version` darauf,
[`persistence.md`](persistence.md)).

- **Revision.** Jede Datei trägt `revision` (ab 1). Jedes Speichern erhöht sie um 1.
- **Atomar.** Schreiben in eine Temp-Datei im selben Verzeichnis (Modus 0600), `fsync`, dann `rename`.
- **Schreibkonflikt statt Überschreiben.** Der Aufrufer übergibt die gelesene Revision. Unter einer exklusiv angelegten
  Sperrdatei (`<datei>.lock`) wird die Revision auf der Platte verglichen; weicht sie ab, wird **nichts geschrieben**
  (`conflict`). Bricht ein Prozess zwischen Anlegen und Entfernen der Sperrdatei ab, bleibt sie liegen; der nächste
  Schreibversuch nennt sie und scheitert, sie ist nach Prüfung von Hand zu entfernen.
- **`profile init` überschreibt nie.** Eine vorhandene Datei bleibt unangetastet (Anlage per Hardlink einer fertig
  geschriebenen Temp-Datei, der bei vorhandener Datei scheitert). Beide Beispiele werden vor dem ersten Schreiben geprüft.
  Das Verfahren setzt ein Dateisystem mit Hardlink-Unterstützung voraus; ohne sie endet `init` mit einem Fehler
  (`storage`), statt eine Datei zu überschreiben.
- `profile list|show`, `agent list|show` lesen nur `agents.yaml`; `profile resolve`, `profile set` und `agent set|unset`
  brauchen zusätzlich `models.yaml` und den Skill-Katalog.

## CLI

Exit-Codes wie im Repository: 0 Erfolg, 1 Laufzeitfehler oder unzulässiger Wert (nichts geschrieben), 2 Bedienfehler.
Fehler nennen den Code in eckigen Klammern. Alle Befehle außer den Hilfen haben `--json`: die lesenden (`list`, `show`, `resolve`,
`verify`) liefern ihren Inhalt, die ändernden (`init`, `profile set`, `agent set`, `agent unset`) ein Ergebnisobjekt. Bei einem Fehler
steht nichts auf stdout; die Meldung geht wie bisher nach stderr, der Exit-Code bleibt 1 oder 2.

| Befehl | Wirkung |
| --- | --- |
| `profile init [--json]` | Startprofile aus den Beispielen anlegen, nie überschreiben; Ergebnis: `config_dir`, `files` (je Datei `file`, `created`) |
| `profile list`, `profile show <rolle>` | gespeicherte Rollenprofile (`--json`) |
| `profile set <rolle> [--model] [--effort] [--skill …] [--clear-skills] [--policy]` | Rollenstandard ändern; vor dem Speichern wie bei einer Ausführung aufgelöst, ebenso jeder vorhandene Agent der Rolle (ein Agent, der danach unauflösbar wäre, bricht mit Exit 1 ab und wird genannt; nichts wird geschrieben); mit `--json` Ergebnis: `config_dir`, `file`, `role`, `revision` (neue Revision von `agents.yaml`), `changed` (`model`, `effort`, `skills`, `policy`), `profile` (das aufgelöste `AgentProfile`, dessen `version` die Profilversion ist) |
| `profile resolve --role R [--agent A] [--model] [--effort] [--skill …]` | aufgelöstes Profil mit Herkunft und Reduktion; mit `--json` ein gültiges `AgentProfile`; schreibt nichts |
| `profile verify [--live] [--role R …] [--claude-binary P] [--json]` | Konfiguration, Auflösbarkeit, Runner-Projekt, `uv` und Claude-Binary prüfen; mit `--live` je Modell ein Minimallauf (verbraucht Kontingent); Exit 1 bei einem Befund |
| `agent list`, `agent show <id>` | individuelle Agenten (`--json`; leeres Feld erbt) |
| `agent set <id> --role R [--model] [--effort] [--skill …] [--change-role]` | Agent anlegen oder nur die genannten Felder ändern; die Rolle eines vorhandenen Agenten wechselt nur mit `--change-role`, sonst Exit 1 (`agent_role_mismatch`); mit `--json` Ergebnis: `config_dir`, `file`, `action` (`created` oder `updated`), `revision`, `agent` (`id`, `role`, `model`, `effort`, `skills`; leer = erbt), `profile` (aufgelöstes `AgentProfile`) |
| `agent unset <id> [--model] [--effort] [--skills] [--all]` | Werte entfernen (erben wieder) oder den Agenten löschen; mit `--json` Ergebnis: `config_dir`, `file`, `action` (`updated` oder `deleted`), `revision`, `agent`; bei `updated` zusätzlich `unset` (entfernte Felder) und `profile`, bei `deleted` nur `id` und `role` des gelöschten Agenten |

Gemeinsame Optionen von `profile` und `agent`: `--config-dir`, `--root`.
`models.yaml` wird nicht über die CLI geändert, sondern von Hand gepflegt. Wer sie ändert, erhöht `revision` selbst, damit
`version` wächst; eine Änderung ohne höhere Revision erkennt das Programm nicht.

## Übergabe an den Runner

Das aufgelöste `AgentProfile` ist die Eingabe des Adapters `internal/workers/runner`. Daraus entstehen im `RunSpec` Modell, Effort,
Rolle, Policy-Profil, `profile_id` und `profile_version`. Hat das Profil Skills, schreibt der Adapter den Snapshot (`Resolve` plus
`Write` in ein angegebenes Verzeichnis) und setzt `RunSpec.skill_snapshot`; bei leeren `skill_ids` gibt es keinen Snapshot. Der
Adapter akzeptiert nur `readonly`; `scoped_write` (Startprofil `implementer`) nur mit einer ausdrücklichen Option für Prüfläufe, sonst
Fehler vor dem Start. Der Ausführungsdatensatz hält das Profil als unveränderliche Kopie. Aufbau und Verhalten:
[`runtime-capabilities.md`](runtime-capabilities.md), Abschnitt 10.

Seit CC-14 (W14.2) kann die Anfrage zusätzlich `SystemPrompt`, `OutputSchema` (strukturierte Ausgabe), `MaxTurns` je Ausführung und einen bereits
geschriebenen `SkillSnapshot` tragen ([`contracts.md`](contracts.md)). Das Profil der **Rolle supervisor** löst der Planer mit demselben Resolver auf
(Rollenstandard, individueller Agent des Rollenangebots, ausdrückliche Auftragswerte nur für Prüfläufe; [`supervisor.md`](supervisor.md)); die „Wahl des Supervisors“
(`Overrides.Supervisor`) gilt für die Profile der Rollen, die er beauftragt, nicht für sein eigenes.

## Prüfung mit `profile verify`

```bash
ccmas profile verify                 # kein Prozessstart, kein Kontingent
ccmas profile verify --live          # je Modell ein Minimallauf (verbraucht Abo-Kontingent)
ccmas profile verify --role reviewer --json
```

- **Ohne `--live`** (Stufen „vorhanden“ und „konfiguriert“ im Sinne von „Prüfbefehl status“): `agents.yaml` und `models.yaml` gültig, jede
  Rolle (und jeder Agent der gewählten Rollen) löst sich auf, das Runner-Projekt `runners/claude_sdk` (mit `pyproject.toml` und `uv.lock`)
  ist da, `uv` ist im PATH, das Claude-Binary ist gefunden (`--claude-binary`, dann `CCMAS_CLAUDE_BINARY`, dann `claude` im PATH, zum
  echten Pfad aufgelöst). Es wird kein Prozess gestartet. Die Stufe „funktional nachgewiesen“ steht als „nicht geprüft“ in der Ausgabe.
- **Mit `--live`**: nur wenn alles Obige in Ordnung ist, genau ein Lauf je eindeutigem Modell der aufgelösten Rollenprofile. Der Lauf
  ist ein Minimallauf: Policy `readonly`, keine Skills, kurzer Prompt, `--max-turns 3`, Effort `low` (der niedrigste vom Modell
  unterstützte, schont das Kontingent). Er belegt **Verfügbarkeit, Anmeldung (`claude_ai`) und das wirksame Modell**; weicht
  `session_started.model` vom konfigurierten ab, ist das ein Befund (`model_mismatch`). Er belegt **nicht den Effort**, weil das
  SDK ihn nicht meldet. Abo-Limit, fehlende Anmeldung und andere terminale Fehler des Runners erscheinen als Diagnose; es gibt
  keinen Fallback und keine API-Abrechnung. Modelle, die nur ein individueller Agent trägt, werden nicht eigens geprüft.
- Die Ausgabe nennt `checked_at`: der Nachweis gilt nur für diesen Zeitpunkt und diese Modelle. `--live` ist der ausdrückliche Wunsch,
  Kontingent zu verbrauchen; es gibt keinen Zwang über eine Umgebungsvariable (die Go-Live-Tests laufen dagegen nur mit `CCMAS_LIVE_TESTS=1`).
- Exit-Code 0 ohne Befund, 1 bei einem Befund (Konfiguration, fehlende Werkzeuge, Diagnose eines Live-Laufs), 2 bei einem Bedienfehler.
- Belegte Läufe mit den Startprofilen (Modell-IDs bestätigt): [`runtime-capabilities.md`](runtime-capabilities.md), Abschnitt 10.
