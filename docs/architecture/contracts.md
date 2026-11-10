# CCMAS – Daten- und Nachrichtenverträge (v1)

Stand: Ticket CC-24 (Meilenstein M01). **Führend ist die Spezifikation** „Multi Agent Setup“
(Version 1.2.0), Abschnitte „Verbindliche Ausführungsverträge“, „Intelligente Arbeitsverteilung“,
„Prozessübersicht“, „Unabhängiges Review vor dem Push“ und „Kommunikation und Persistenz“. Diese
Datei hält fest, wie das Repository diese Verträge umsetzt, und ist keine Kopie der Spezifikation.

## Quellen der Wahrheit

| Was | Wo |
| --- | --- |
| JSON-Schemas (Draft 2020-12), je Vertrag eine Datei | `contracts/*.schema.json` |
| Gemeinsame Test-Fixtures für Go und Python | `contracts/fixtures/valid/`, `contracts/fixtures/invalid/` |
| Go-Typen, Zustände, Prüflogik, Nachrichtenfolge | `internal/domain` (`types.go`, `states.go`, `schema.go`, `check.go`, `tracker.go`) |
| Python-Prüfung des Runners | `runners/claude_sdk/src/claude_sdk_runner/contracts.py` |

Es gibt **keine Kopien** der Schemas. Go bettet die Dateien über das kleine Paket `contracts`
(`//go:embed *.schema.json`) in das Binary ein, damit es autark bleibt. Python liest dieselben Dateien zur
Laufzeit relativ zum Repository-Wurzelverzeichnis (`contracts_dir()`: `<Repo>/contracts`; für andere
Ablageorte die Umgebungsvariable `CCMAS_CONTRACTS_DIR`). Das Python-Paket enthält keine zweite
Typdefinition, die vom Schema abweichen könnte.

Alle weiteren Pakete (Orchestrator, Persistenz, Monitoring, CLI, TUI, …) verwenden ausschließlich die
Typen aus `internal/domain` und definieren keine eigenen Varianten. `internal/cli` nutzt sie derzeit nur für die
Rollen von `ccmas skills`.

Bibliotheken: Go `github.com/santhosh-tekuri/jsonschema/v6` v6.0.3, Python `jsonschema` 4.26.0 (exakt
gepinnt in `go.mod` beziehungsweise `pyproject.toml` und `uv.lock`; Python als normale Laufzeitabhängigkeit).
`referencing` gehört als feste Abhängigkeit zu `jsonschema` und ist über `uv.lock` gesperrt.

## Dateien

| Datei | Inhalt |
| --- | --- |
| `common-v1.schema.json` | Gemeinsame Bausteine: ID, Pfade, Modell, Effort, Rolle, Zeitstempel, Commit, Textlängen; Liste der Fehlerklassen |
| `runner-v1.schema.json` | Runner-Umschlag beider Richtungen, alle Payloads, `ReviewReport` |
| `decision-v1.schema.json` | `Decision`, `IntegrationRequest` |
| `project-v1.schema.json` | `ProjectConfig`, `AgentProfile`, `WorkOrder` |
| `skill-snapshot-v1.schema.json` | `snapshot.json` eines Skill-Snapshots (`SkillSnapshot`, seit CC-35) |

Die `$id`-Kennungen (`https://ccmas.invalid/contracts/…`) sind reine Namen; `.invalid` ist nicht auflösbar.
Go verbietet das Laden von Schemas außerhalb der eingebetteten Dateien.

## Runner-Protokoll

Versioniertes JSON-Lines-Protokoll: eine Nachricht ist eine JSON-Zeile. stdout enthält ausschließlich
Protokoll, stderr redigierte Logs. Strict-Parsing: keine unbekannten Felder (`additionalProperties: false`),
keine doppelten Schlüssel, genau ein JSON-Objekt, gültiges UTF-8, keine Kommazahlen (alle Zahlen sind
Ganzzahlen; `1.0` und `1e0` sind Wertfehler).

### Umschlag

| Feld | Typ | Regel |
| --- | --- | --- |
| `protocol_version` | String | genau `"1"` |
| `project_id` | ID | Format siehe unten |
| `execution_id` | ID | |
| `attempt` | Ganzzahl | 1 bis 1 000 000 |
| `correlation_id` | ID | ordnet Frage und Antwort zu |
| `type` | String | je Richtung eine feste Menge |
| `timestamp` | String | RFC 3339 mit Zeitzone, Großbuchstaben `T`/`Z`, Jahr 1000–9999, gültiges Kalenderdatum (Schaltjahre), Sekunde 00–59, 1–9 Nachkommastellen optional |
| `payload` | Objekt | je Typ ein geschlossenes Schema |

**Größenlimit:** höchstens **1 048 576 Bytes (1 MiB) je Nachricht**, gemessen an der ganzen JSON-Zeile
(Umschlag und Payload). Das Limit steht als `x-max-message-bytes` in `runner-v1.schema.json` (Python liest es
von dort) und als Go-Konstante `MaxMessageBytes` (ein Test hält beide gleich). Eine Nachricht mit genau
dieser Länge ist gültig, ein Byte mehr ist ein Größenfehler. Der Größenfehler wird vor dem Parsen gemeldet.
Ein getrenntes Payload-Limit wäre in Go und Python nicht byte-identisch messbar; das Zeilenlimit begrenzt die
Payload mit. Zusätzlich begrenzen `maxLength`/`maxItems` im Schema die einzelnen Felder (ebenfalls
Größenfehler). Ausgabegröße, Pufferung und Rückstau des Runners sind Sache der Runtime (M04).

### Nachrichtentypen

Go → Python: `start`, `answer`, `interrupt`, `stop`. Python → Go: `session_started`, `activity`,
`tool_event`, `question`, `result`, `error`. Ein Typ der falschen Richtung ist ein Fehler der Klasse
`message_type`.

| Typ | Payload (Go-Typ) | Felder (Pflicht fett) |
| --- | --- | --- |
| `start` | `RunSpec` | **work_order_id, role, runtime, model, effort, skill_ids, policy_profile, cwd, claude_binary, prompt, max_runtime_seconds**; profile_id, profile_version, resume_session_id, skill_snapshot (ab CC-35, siehe unten) |
| `answer` | `Answer` | **answer_id, question_id, text, created_at**; delivery_confirmed_at (wird nie mitgesendet; Go lehnt es beim Senden ab) |
| `interrupt`, `stop` | `InterruptPayload`, `StopPayload` | **reason** (`user_request`, `timeout`, `shutdown`, `superseded`) |
| `session_started` | `SessionStarted` | **session_id, model, effort, claude_version**; permission_mode, claude_binary, auth_source (ab CC-17, optional); skill_snapshot_id, skills (ab CC-35, optional, siehe unten); model und permission_mode sind die von der CLI gemeldeten Werte, effort ist der übergebene Wert (die CLI meldet das wirksame Effort nicht); `auth_source` ist nur die Art der Anmeldung (`claude_ai`, `api_key`, `other`), nie Kontodaten |
| `activity` | `Activity` | **kind** (`assistant_text`, `thinking`, `waiting`, `idle`), **summary** (≤ 512 Zeichen); keine Fortschrittsprozente |
| `tool_event` | `ToolEvent` | **tool_use_id, tool, phase** (`started`, `finished`, `failed`); summary |
| `question` | `QuestionPayload` | **question_id, question, context**; recommendation |
| `result` | `Result` | **outcome** (`succeeded`, `controlled_failure`, `cancelled`), **summary**; review_report |
| `error` | `ErrorPayload` | **code, message, kind** (`runtime`, `subscription_limit`, `protocol`, `internal`), **terminal** |

`RunSpec` und `RunEvent`/`RunCommand` sind die Go-Namen für den Start-Payload beziehungsweise die validierten
Nachrichten (Umschlag plus typisierter Payload).

### Terminales Ergebnis (Akzeptanzkriterium 2)

Pro `(execution_id, attempt)` gibt es **höchstens ein terminales Ergebnis**: ein `result` oder ein `error` mit
`terminal=true`. Ein Prozessende ohne vorheriges terminales Ergebnis ist ein **Absturz** (auch bei Exit-Code 0).
Das Schema kann Nachrichtenfolgen nicht ausdrücken; Go erzwingt es im `ExecutionTracker`:

- zweites terminales Ergebnis und jede Nachricht nach dem terminalen Ergebnis → Klasse `sequence`, Zustand
  unverändert;
- Nachricht eines älteren Versuchs → `stale`, Zustand unverändert; Nachricht eines nicht begonnenen Versuchs →
  `sequence`;
- Nachricht mit fremdem Projekt oder fremder Ausführung → `id`;
- ein Resume erhält einen neuen Versuch (`BeginAttempt`, Nummer genau +1, nur nach beendetem Versuch);
- `error` mit `terminal=false` ist eine Warnung ohne Zustandswirkung;
- der Runner meldet sich nie selbst „abgebrochen“: `cancelled` ist nur nach einem gesendeten Interrupt/Stopp
  (`StopSent`) erreichbar.

Der Tracker leitet keinen Auftragszustand ab.

## Entscheidungen (Decision)

Der Supervisor liefert ein strukturiertes Ergebnis nach `decision-v1`.

| Feld | Regel |
| --- | --- |
| `schema_version` | genau `1` |
| `decision_id`, `project_id` | ID |
| `state_revision` | Ganzzahl ≥ 0 |
| `target` | `kind` (`ticket`, `feature`, `milestone`, `work_order`) und `id` |
| `action` | genau eine von acht Aktionen (unten) |
| `preconditions` | bis 32 Einträge `kind` (`work_order_completed`, `concept_version_approved`, `review_approved`, `ci_passed`, `capacity_available`), `ref` optional |
| `rationale` | 1–1000 Zeichen (kurze, überprüfbare Begründung) |
| `selection` | Rolle, `skill_ids`, `model`, `effort`; Pflicht bei `concept_work`, `implementation`, `review` |
| `review_target` | `work_order_id`, `commit`, `concept_version`; Pflicht bei `review` |
| `question` | `text`, `context`, `recommendation`; Pflicht bei `ask_question` |

Aktionen: `plan_extend` (Plan ergänzen), `concept_work` (Konzeptarbeit beauftragen), `implementation`
(Implementierung anfordern), `review` (Review anfordern), `ask_question` (Rückfrage stellen),
`request_integration` (Integration anfordern), `wait` (warten), `request_completion` (Zielabschluss anfordern).
Alles andere ist ein Fehler der Klasse `action`.

Eine formal gültige Decision ist **keine Ausführungsfreigabe**. `Decision.Check(DecisionContext)` prüft in Go:
Projektbezug (`id`), unveränderte Zustandsrevision (`revision`), bekannte und zur Aktion passende Rolle
(`role`), Modell und Effort gegen den konfigurierten `Catalog` (`model`, `effort`) und bekannte Skills
(`value`). Voraussetzungen, Zyklenfreiheit, Kapazität, Locks und Qualitätsbelege prüft der Orchestrator (M03)
mit denselben Typen. Ungültige oder veraltete Entscheidungen werden protokolliert und nicht ausgeführt.

## Projekt, Profile, Arbeitsauftrag

| Vertrag | Felder |
| --- | --- |
| `ProjectConfig` | `schema_version` (=1), `project_id`, `code_dir`, `execution_dir`, `repository` (`owner/name`), `base_branch` (fest `develop`), `vault` {`id`, `path`, `project_area`}, `asana_project_gid`; optional mit Standard: `max_workers` 1–16 (Standard 3), `default_mode` (Standard `pr`), `channels` (Standard `cli`, `tui`), `resources` {`max_parallel_builds` 1, `monitor_interval_seconds` 5, `planning_interval_seconds` 60, `attempt_runtime_minutes` 60} |
| `AgentProfile` | `profile_id`, `version` (≥ 1), `role`, `runtime` (`claude`), `model`, `effort`, `skill_ids`, `policy_profile`; optional (seit CC-46, siehe unten) `requested_model`, `requested_effort`, `fallback`, `value_sources` |
| `WorkOrder` | `work_order_id`, `project_id`, `target` {`kind` `ticket`/`feature`/`milestone`, `ref`}, `user_order`, `goal`, `acceptance_criteria` (1–64), `allowed_scope` (relative Pfade), `mode` (`pr`/`auto`), `plan_version`, `state` |
| `IntegrationRequest` | `request_id`, `decision_id`, `work_order_id`, `mode`, `base_branch` (=`develop`), `head_commit`, `branch`, `pr_number` (optional), `review_report_id`, `concept_version`, `created_at` |
| `ReviewReport` | `report_id`, `work_order_id`, `reviewer_execution_id`, `commit`, `concept_version`, `verdict` (`FREIGEGEBEN`/`BEFUNDE`), `findings`, `created_at` |

`ReviewReport`: `FREIGEGEBEN` verlangt eine leere, `BEFUNDE` eine nicht leere Befundliste (Schema und
`ReviewReport.Check`). Ein Bericht gilt nur für den genannten Commit und die genannte Konzeptversion:
`IntegrationRequest.CheckReview` weist einen Bericht mit anderem Bericht, Auftrag, Commit (`commit`),
Konzeptversion oder ohne Freigabe ab.

Rollen: `supervisor`, `project_setup`, `concept`, `concept_reviewer`, `implementer`, `reviewer`,
`documentation`, `completion`, `question_handler`. Effort-Stufen: `low`, `medium`, `high`, `xhigh`, `max`.
Modell-IDs stehen **nicht** im Code: das Schema verlangt einen nicht leeren String ohne Leer- und
Steuerzeichen; ob das Modell bekannt ist und welche Stufen es unterstützt, prüft Go gegen den konfigurierten
`Catalog` (`CheckModelEffort`: unbekanntes Modell → `model`, unbekannte oder nicht unterstützte Stufe →
`effort`). Eine dokumentierte Reduktion einer Effort-Stufe wird über das wirksame `effort` in
`session_started` sichtbar und seit CC-46 zusätzlich im `AgentProfile` selbst (nächster Abschnitt).

### AgentProfile: angefordert, Reduktion, Herkunft (ab CC-46, v1-kompatibel)

Vier neue **optionale** Felder; kein bisher gültiges Profil wird ungültig, es gibt keine neuen Pflichtfelder.
`model`, `effort` und `skill_ids` bleiben die **wirksamen** Werte, die der Runner erhält. Aufgelöst und befüllt
werden sie von `internal/profiles` ([`profiles.md`](profiles.md)).

| Feld | Inhalt |
| --- | --- |
| `requested_model` | angefordertes Modell. Modelle werden nie ersetzt: der Wert gleicht `model` (Go-Prüfung, Klasse `model`) |
| `requested_effort` | angeforderter Effort vor einer genehmigten Reduktion; ohne `fallback` gleich `effort` |
| `fallback` | `{kind, from, to, source}`: `kind` ist `effort_reduction`, `from` der angeforderte, `to` der wirksame Effort, `source` die Quelle der Genehmigung in `models.yaml` (1–512 Zeichen). Nur vorhanden, wenn reduziert wurde; verlangt `requested_effort` (Schema, Klasse `field`) |
| `value_sources` | `{model, effort, skill_ids}`, je eines von `order`, `agent`, `supervisor`, `role` (Rangfolge ausdrücklicher Auftragswert > Agentenwert > Wahl des Supervisors > Rollenstandard; bei Skills ist `role` auch die Standardausstattung des Skill-Katalogs). Alle drei Schlüssel sind Pflicht, wenn das Objekt vorhanden ist |

Das Schema prüft Form und Aufzählungen. `Catalog.CheckProfile` prüft zusätzlich die Widerspruchsfreiheit
(Klasse `model`, `effort` oder `value`): `requested_model` gleich `model`; ein abweichender `requested_effort` nur
mit `fallback`; `fallback.from` gleich `requested_effort`, `fallback.to` gleich `effort`, `to` unter `from`; das
Modell unterstützt `from` nicht (und `to` laut Katalog schon); jede Herkunft ist bekannt. Der wirksame Effort ist
**abgeleitet** (Konfiguration plus dokumentierte Regel), nicht gemessen: das SDK meldet ihn nicht
([`runtime-capabilities.md`](runtime-capabilities.md)).

`profile_id` benennt die Quelle (`<rolle>` oder `<rolle>:<agent>`), `version` die Konfigurationsrevision
(Revision von `agents.yaml` plus Revision von `models.yaml` minus 1; 1 nach der Einrichtung, mit jeder Änderung einer
Datei größer). Zusammen identifizieren sie den Konfigurationsstand eines Profils, nicht einen Prozess.

### Nur in Go definierte Typen

Diese Typen verlassen den Go-Prozess nicht als Schema-Vertrag und haben deshalb kein JSON-Schema, aber
dieselben Namen und Prüfmethoden (`Check`):

- `Question` – persistente Rückfrage (ID, Auftrag, Ausführung, Versuch, Frage, Kontext, Empfehlung, Zustand,
  Erstellungszeit, Antwort-ID, bestätigte Zustellung). Aus `QuestionPayload` und dem Umschlag gebildet.
  `Accept` ordnet eine Antwort zu (andere Frage → `id`, zweite Antwort → `state`), `ConfirmDelivery` bestätigt
  die Zustellung.
- `ProcessSample` – Messung: PID **und Startzeit** als Identität (`ProcessIdentity`), Elternbezug mit
  Startzeit, OS-Zustand (`OSProcessState`), CPU in Prozent (100 je ausgelastetem Kern), RSS in Bytes,
  Exit-Code, Messzeit. Unbekannte Werte sind `nil`, nie `0`. Eine wiederverwendete PID hat eine andere
  Startzeit und damit eine andere Identität. Aggregation über Identitätsmengen ist Sache des Monitorings (M04).
- `AgentSnapshot` – Übersicht eines Agenten mit **getrennten Feldern** für Verfügbarkeit,
  Ausführungszustand, Auftragszustand und den Prozess; `Check` weist widersprüchliche Kombinationen ab
  (zum Beispiel aktiver Agent ohne laufende Ausführung).

## Skill-Snapshot (`skill-snapshot-v1`, seit CC-35)

`snapshot.json` im Wurzelverzeichnis eines unveränderlichen Skill-Snapshots (Layout, Hash-Definitionen, Ablauf:
[`skill-migration.md`](skill-migration.md)). Pflichtfelder: `schema_version` (=1), `snapshot_id`
(`sha256:` + 64 Hexziffern), `role`, `created_at`, `manifest_sha256`, `skills[]` (höchstens 64, aufsteigend nach
`id`, ohne Wiederholung) mit `id`, `version` (≥ 1), `tree_sha256`, `files[]` (1–256; `path` relativ und kanonisch,
`sha256`, aufsteigend nach Pfad) und `source` (`package` vault-relativ, `package_sha256`). Keine absoluten Pfade und
keine persönlichen Daten. Das Schema prüft Form und Formate; Sortierung und Eindeutigkeit prüft Go
(`SkillSnapshot.Check`); die Hashes (Dateien, `tree_sha256`, `snapshot_id`) rechnet `skills.VerifySnapshotDir` gegen das
Verzeichnis nach, ebenso der Python-Runner vor jedem Sitzungsstart (`skill_snapshot.py`, CC-35). Ziel der Fixtures: `skill-snapshot`. Die vollständige Beispiel-
Verzeichnisstruktur liegt unter `contracts/fixtures/skill-snapshots/example/` und wird vom Go-Test
`TestExampleSnapshotFixture` aus einem Test-Katalog neu erzeugt und Byte für Byte verglichen.

### Skills im Protokoll (ab CC-35, v1-kompatibel)

Drei neue **optionale** Felder; kein bisher gültiges Dokument wird ungültig, es gibt keine neuen Pflichtfelder.

| Nachricht | Feld | Inhalt |
| --- | --- | --- |
| `start` | `skill_snapshot` | Objekt `{snapshot_id, path}`: Kennung (`sha256:` + 64 Hexziffern, Muster aus `skill-snapshot-v1`) und absoluter, kanonischer Pfad des Snapshot-Verzeichnisses (`abs_path`); beide Pflicht, keine weiteren Felder |
| `session_started` | `skill_snapshot_id` | Snapshot, aus dem die Sitzung ihre Skills lädt |
| `session_started` | `skills` | Skill-IDs **ohne Plugin-Präfix**, die die CLI laut Init-Meldung wirksam geladen hat (`skill_ids`-Form: höchstens 64, eindeutig); eine leere Liste heißt „keine Skills geladen“ |

Beide `session_started`-Felder stehen nur, wenn der Auftrag einen Snapshot trug. In Go ist `Skills` ein `[]string` mit
`omitempty`; eine leere Liste und ein fehlendes Feld sind dort nicht zu unterscheiden (die Bedeutung ist in beiden Fällen
„keine Skills“). Welche Kombinationen zulässig sind, regelt nicht das Schema, sondern der Runner in seiner Vorprüfung
(ohne Modellaufruf, jeweils **terminaler** `error`, Art `runtime`):

| Code | Auslöser |
| --- | --- |
| `skill_snapshot_missing` | `skill_ids` ist nicht leer, aber es gibt keinen `skill_snapshot` (die frühere Warnung `skills_not_applied` entfällt) |
| `skill_snapshot_invalid` | Snapshot-Verzeichnis oder `snapshot.json` fehlt, ist nicht lesbar, ist ein Symlink oder verletzt `skill-snapshot-v1` (inklusive Sortierung und Eindeutigkeit wie `SkillSnapshot.Check`) |
| `skill_snapshot_mismatch` | Das Verzeichnis weicht von der `snapshot.json` ab (Datei-Hash, `tree_sha256`, `snapshot_id`; fehlende oder zusätzliche Dateien und Verzeichnisse; Symlinks und Sonderdateien; falscher Verzeichnisname; geänderte `plugin.json`), oder der Snapshot passt nicht zum Auftrag (`snapshot_id`, Rolle, Menge der Skill-IDs) |
| `skills_not_loaded` | Im Init-Frame fehlt das Plugin `ccmas` mit dem Pfad des Snapshots, ein `ccmas:<id>` fehlt, ein nicht freigegebener `ccmas:`-Eintrag oder ein weiteres Plugin namens `ccmas` ist da, oder `plugins`/`skills` fehlen im Frame; Abbruch über den Weg von `api_billing_detected`, bevor ein Werkzeug läuft |

**Go setzt `RunSpec.skill_snapshot` (seit CC-46):** Der Adapter `internal/workers/runner` löst bei nicht leeren `skill_ids` des
`AgentProfile` die Skills der Rolle über `skills.Catalog.Resolve` auf, schreibt den Snapshot mit `Snapshot.Write` in ein angegebenes
Verzeichnis (kanonischer Pfad, ohne Symlinks) und setzt `skill_snapshot` auf Kennung und Pfad; `skill_ids` und Snapshot enthalten
dieselben Skills. Bei leeren `skill_ids` fehlt das Feld (kein Snapshot). Der Adapter prüft den `RunSpec` vor dem Senden mit
`EncodeRunCommand` gegen den Vertrag. Live belegt: [`runtime-capabilities.md`](runtime-capabilities.md), Abschnitt 10.

Meldungen sind deutsch, nennen Skill und Pfad **innerhalb** des Snapshots (`skills/<id>/<datei>`) und enthalten nie
Dateiinhalte oder Hashwerte. Ablauf in der Sitzung: [`runtime-capabilities.md`](runtime-capabilities.md), Abschnitt „Skills in der Sitzung (CC-35)“.

## Zustandsmodell (Akzeptanzkriterium 3)

Vier Ebenen mit je **eigenem Go-Typ** (kein gemeinsames String-Alias): `AgentAvailability`,
`ExecutionState`, `WorkOrderState`, `OSProcessState` (zusätzlich `QuestionState`). Die Tabellen stehen in
`states.go`; `CanTransition` fragt sie ab. Nichts wird zwischen den Ebenen abgeleitet: Ein Prozess-Exit mit
Code 0 macht den Versuch nicht erfolgreich (ohne Ergebnis ist er abgestürzt) und beendet nie den Auftrag. Ein
freier Worker kann andere Arbeit übernehmen, während ein früherer Auftrag auf CI oder Merge wartet.

| Ebene | Zustand → erlaubte Folgezustände |
| --- | --- |
| Agent | `available` → `reserved`, `unavailable`; `reserved` → `active`, `available`, `unavailable`; `active` → `available`, `unavailable`; `unavailable` → `available` |
| Ausführung | `initializing` → `running`, `stopping`, `controlled_failure`, `crashed`; `running` → `awaiting_answer`, `stopping`, `succeeded`, `controlled_failure`, `crashed`; `awaiting_answer` → `running`, `stopping`, `controlled_failure`, `crashed`; `stopping` → `succeeded`, `controlled_failure`, `crashed`, `cancelled`; `succeeded`, `controlled_failure`, `crashed`, `cancelled` sind Endzustände |
| Auftrag | `waiting` → `in_progress`, `blocked`, `cancelled`; `in_progress` → `waiting`, `validation`, `blocked`, `cancelled`; `validation` → `in_progress`, `review`, `blocked`, `cancelled`; `review` → `in_progress`, `waiting_integration`, `blocked`, `cancelled`; `waiting_integration` → `in_progress`, `completed`, `blocked`, `cancelled`; `blocked` → `waiting`, `in_progress`, `cancelled`; `completed`, `cancelled` sind Endzustände |
| Frage | `open` → `answered`, `superseded`, `cancelled`; `answered` → `delivered`, `superseded`, `cancelled`; Rest Endzustände (keine Antwort ist keine Freigabe) |
| OS-Prozess | `running`, `sleeping`, `stopped`, `zombie`, `exited`, `unknown` – beobachtet, keine Übergangstabelle |

Auftragszustände in der Spezifikation: wartet auf Voraussetzungen/Kapazität (`waiting`), in Bearbeitung,
Validierung, Review, wartet auf Integration, abgeschlossen, blockiert, abgebrochen. „Abgeschlossen“ ist nur
aus „wartet auf Integration“ erreichbar. Die Zehn-Absturz-Sperre (`blocked`) und die Persistenz der Zustände
gehören zu M03/M09; hier liegen nur Zustände und Übergänge.

## Fehlerkategorien

Jeder Verstoß ist ein `*ContractError` (Go) beziehungsweise `ContractError` (Python) mit einer oder mehreren
Klassen (`Categories`, alphabetisch, eindeutig; `Category()` ist die erste).

| Klasse | Bedeutung | Geprüft von |
| --- | --- | --- |
| `syntax` | kein einzelnes JSON-Objekt, Folgedaten, doppelter Schlüssel, ungültiges UTF-8, NaN | Go, Python |
| `size` | Nachricht über 1 MiB, Text, Liste oder Objekt über der Feldgrenze | Go, Python |
| `version` | Protokoll- oder Schema-Version nicht unterstützt | Go, Python |
| `field` | Pflichtfeld fehlt | Go, Python |
| `unknown_field` | unbekanntes Feld | Go, Python |
| `id` | ID-Format verletzt; in Go auch fremder Projekt-/Ausführungsbezug | Go, Python (Format) |
| `path` | Pfad nicht absolut/kanonisch oder mit `.`/`..`/Steuerzeichen | Go, Python |
| `model` | Modell leer oder mit Leer-/Steuerzeichen; in Go auch nicht konfiguriert | Go, Python (Format) |
| `effort` | Effort nicht in `low…max`; in Go auch vom Modell nicht unterstützt | Go, Python (Stufen) |
| `action` | unbekannte Supervisor-Aktion | Go, Python |
| `message_type` | unbekannter Nachrichtentyp oder Typ der falschen Richtung | Go, Python |
| `timestamp` | kein gültiger RFC-3339-Zeitstempel | Go, Python |
| `limit` | konfigurierbares Limit außerhalb des Bereichs (`max_workers`, `max_runtime_seconds`, …) | Go, Python |
| `role` | unbekannte oder zur Aktion unpassende Rolle | Go, Python (Aufzählung) |
| `commit` | kein voller Git-Commit-Hash; in Go auch Beleg gehört zu anderem Commit | Go, Python (Format) |
| `value` | sonstiger unzulässiger Wert: Typ, Aufzählung, Bereich, Kommazahl, `base_branch` ≠ `develop` | Go, Python |
| `sequence` | Nachricht an dieser Stelle der Folge unzulässig (zweites terminales Ergebnis, …) | nur Go |
| `stale` | Nachricht eines älteren Versuchs | nur Go |
| `revision` | veraltete Zustandsrevision einer Entscheidung | nur Go |
| `state` | Zustand erlaubt die Operation nicht | nur Go |

**Klassifizierung der Schemafehler** (in Go und Python identisch): Jeder Blattfehler der Schemaprüfung erhält
eine Klasse – `required` → `field`; `additionalProperties` → `unknown_field`; sonst die Annotation
`x-error-class` des verletzten Teilschemas (zum Beispiel an `id`, `abs_path`, `model`, `effort`); sonst
`maxLength`/`maxItems`/`maxProperties` → `size`; sonst `value`. Die Annotation ist ein im Schema gesetztes
Schlüsselwort ohne Wirkung auf die Gültigkeit. Die Fehlerklasse einer Nachricht ist die Menge der Klassen
aller Blattfehler.

Bekannte Grenzen der Gleichheit von Go und Python: Pathologisch tief verschachtelte Eingaben (> ca. 1000
Ebenen) werden in beiden Sprachen als `syntax` behandelt, die genaue Tiefe weicht aber ab. Ganzzahlen jenseits
von 2^53 sind durch `maximum` ausgeschlossen.

## Was Schema, Go und Python prüfen

| Prüfung | Schema | Go | Python |
| --- | --- | --- | --- |
| Pflichtfelder, unbekannte Felder, Typen, Aufzählungen, Textlängen | ja | über das Schema | über das Schema |
| ID-Format, Pfadform (absolut, kanonisch, ohne `..`), Zeitstempel, Commit-Format | ja (Muster) | über das Schema; zusätzlich `CheckID`, `CheckCanonicalPath`, `CheckRelativePath` für direkt gebaute Werte | über das Schema |
| Bedingte Pflichtfelder (zum Beispiel `review` verlangt `review_target`), Urteil ↔ Befunde | ja (`if`/`then`) | über das Schema | über das Schema |
| Nachrichtengröße 1 MiB, strikter JSON-Parser (Duplikate, Kommazahlen, UTF-8, Folgedaten) | nur der Grenzwert `x-max-message-bytes` | ja | ja |
| Richtung der Nachrichtentypen | ja (zwei Umschläge) | ja | ja |
| Typisiertes Dekodieren ohne unbekannte Felder (Schutz vor Drift zwischen Schema und Typ) | – | ja | – |
| Modell bekannt, Effort vom Modell unterstützt, Skills/Policies bekannt; Profil: angefordert/Fallback/Herkunft widerspruchsfrei | – (nur Format und Aufzählungen) | ja (`Catalog`, `CheckProfile`) | – |
| Projektbezug und Zustandsrevision einer Entscheidung, Rolle ↔ Aktion | – | ja (`Decision.Check`) | – |
| Nachrichtenfolge, terminales Ergebnis, veraltete Versuche | – | ja (`ExecutionTracker`) | – |
| Zustandsübergänge | – | ja (`CanTransition`) | – |
| Review-Beleg ↔ Commit/Konzeptversion, Antwort ↔ Frage, Snapshot-Konsistenz, Prozessidentität | – | ja (`Check`-Methoden) | – |
| Skill-Snapshot: Sortierung und Eindeutigkeit (`SkillSnapshot.Check`), Hashes gegen das Verzeichnis (`skills.VerifySnapshotDir`) | nur Form und Formate | ja | ja (`skill_snapshot.verify_snapshot_dir`, seit CC-35) |
| `skill_ids` ohne `skill_snapshot`; Snapshot ↔ Auftrag (Kennung, Rolle, Skill-IDs); geladene Skills ↔ Snapshot (Init-Frame) | – | – (Go setzt `skill_snapshot` seit CC-46, siehe unten) | ja (`skill_snapshot.verify_for_spec`, `runtime.check_skills_loaded`) |

Der Python-Runner prüft eingehende Kommandos und kann seine ausgehenden Meldungen vor dem Senden mit
`contracts.validate("from-runner", …)` prüfen. Die Reihenfolge der Meldungen und alle Zustände verantwortet Go.

Pfade sind POSIX-Pfade (v1 gilt für macOS und Linux); Windows-Pfade sind nicht vorgesehen.

## Gemeinsame Fixtures

`contracts/fixtures/valid/<ziel>--<name>.json` muss akzeptiert werden,
`contracts/fixtures/invalid/<ziel>--<fehlerklasse>--<name>.json` muss mit **genau dieser Fehlerklasse** (und
keiner weiteren) abgewiesen werden. Ziele: `to-runner`, `from-runner`, `project-config`, `agent-profile`,
`work-order`, `decision`, `integration-request`, `review-report`, `skill-snapshot`. Go (`TestContractFixtures`) und Python
(`test_contract_fixtures`) lesen dieselben Dateien; keine Seite hat eigene Fixtures für dieselbe Regel. Ein
neues Verhalten wird durch eine neue Fixture beider Seiten belegt. Ungültige Fixtures enthalten genau einen
Verstoß. Fixtures sind Testdaten und enthalten keine Produktdaten; Beispielaufgaben stehen nur dort.

Die Fixtures decken Version, ID, Pfad, Modell, Effort, Größe, Aktion, Pflichtfeld und unbekanntes Feld ab
(ein Test stellt das sicher). Übergroße Nachrichten (> 1 MiB) werden wegen der Dateigröße nicht als Fixture
abgelegt, sondern in beiden Sprachen im Test erzeugt (`TestMessageSizeLimit`, `test_size_limit_is_exact_…`).

## Kompatibilitätsregeln

- **Version.** Die Schema-Dateien tragen die Hauptversion im Namen (`…-v1`). Der Umschlag trägt
  `protocol_version`, `Decision` und `ProjectConfig` tragen `schema_version`. Beide Seiten weisen jede andere
  Version ab (`version`).
- **Änderungen an v1 nur abwärtskompatibel.** Abwärtskompatibel heißt: jedes bisher gültige Dokument bleibt
  gültig. Erlaubt sind neue *optionale* Felder, zusätzliche Aufzählungswerte und gelockerte Grenzen – jeweils
  mit Fixtures und gleichzeitiger Anpassung von Go und Python. Nicht erlaubt in v1 sind: Felder entfernen oder
  umbenennen, optionale Felder verpflichtend machen, Grenzen verschärfen, die Bedeutung eines Felds ändern,
  Nachrichtentypen oder Aktionen entfernen. Das ist ein **v2**: neue Dateien `…-v2.schema.json` neben den
  v1-Dateien, neue `protocol_version`, ein Übergang, in dem Go beide Versionen liest.
- **Strikt.** Wegen `additionalProperties: false` lehnt jede Seite Dokumente mit Feldern ab, die sie nicht
  kennt. Go-Dienst und Runner stammen deshalb aus demselben Repository-Stand; dass der Dienst nur einen Runner mit
  derselben Protokollversion startet, ist eine Anforderung an die Runtime (M04). Gespeicherte Dokumente (Konfiguration, Entscheidungen,
  Berichte) tragen die Version und bleiben mit späteren v1-Ständen lesbar.
- **Neue Aufzählungswerte** (Rollen, Aktionen, Nachrichtentypen, …) werden in Schema, Go-Konstanten und
  Fixtures im selben Änderungsstand ergänzt; Tests vergleichen die Go-Listen mit den Schema-Enums.
- **JSON-Namen** sind `snake_case` und in Go (Struct-Tags) und Python (Schema) identisch; Go-Typen, die ein
  Schema abbilden, werden in den Fixture-Tests per Round-Trip gegen das Schema geprüft.

## Entscheidungen bei Lücken der Spezifikation

- **Rollenmenge, Voraussetzungsarten, Zielarten, Fehlerarten, Aktivitätsarten, Interrupt-Gründe** sind in
  der Spezifikation nicht aufgezählt und hier festgelegt. Sie stehen als Aufzählungen im Schema.
- **`plan_extend`** trägt in v1 nur Ziel, Voraussetzungen und Begründung; die konkreten Planergänzungen
  (neue Aufträge, Abhängigkeiten) legt M02 fest, weil sie vom Supervisor-Skill abhängen.
- **`Execution`** (Versuch-ID, Session, Worktree, Prozessidentität, Start/Ende) ist ein Persistenzobjekt des
  Dienstes (M03). Hier definiert sind Zustand, Nachrichtenfolge (`ExecutionTracker`) und Prozessidentität.
- **`interrupt` und `stop`** führen beide in „wird beendet“; sie unterscheiden sich im Runner (Runde beenden
  gegenüber Sitzung beenden).
- **Abo-Wartezustände** sind ein `error` mit `kind: subscription_limit` (nicht terminal) und lösen keine
  Crash-Sperre aus.
- **Konzeptversion** ist eine Bezeichnung (zum Beispiel `1.2.0`), keine geprüfte SemVer.

## Prüfbefehle

```bash
go test ./internal/domain/...
cd runners/claude_sdk && uv run --frozen pytest tests/test_contracts.py -q
make check
```
