# CCMAS – SQLite-Zustand (`internal/persistence`)

Stand: Ticket CC-27 (Meilenstein M03), ergänzt um CC-45 (Nachrichten und Outbox). **Führend ist die Spezifikation** „Multi Agent Setup“ (Version 1.7.0), Abschnitte
„Führender interner Zustand“, „Projektbezogene Arbeitsorte“ und „Prüfbefehl status“. Diese Datei spiegelt den Code
(`internal/persistence/doc.go` ist die Quelle) und ist keine Kopie der Spezifikation. Bei Abweichungen gilt der Code, und
die Datei wird nachgezogen.

## Treiber

`modernc.org/sqlite` **v1.61.0** mit **SQLite 3.54.1** (Stand 2026-11-10). Gründe:

- reines Go, **kein cgo**: `make build-all` baut ohne C-Toolchain für macOS (arm64, amd64), Linux (amd64) und Windows (amd64);
- eine Abhängigkeit, keine Systembibliothek, die zum Binary passen müsste;
- Auswahlregel: die **neueste stabile Version, die mindestens zwei Wochen alt ist**. Eine Aktualisierung ist ein eigenes
  Ticket mit erneutem Lauf der Testsuite.

`sqlite.StrictPragmas(true)` ist prozessweit eingeschaltet (mehrere Anweisungen in einem `_pragma` sind nie nötig). Das
Paket ist der einzige Ort, der den Treiber verwendet; die CLI erreicht ihn nur über `persistence`.

## Ort, Dateirechte

Die Datenbank liegt fest unter `<Ausführungsbereich>/state/ccmas.db`. Der Ausführungsbereich ergibt sich aus
`--execution-dir`, sonst `CCMAS_EXECUTION_DIR`, sonst `<CCMAS-Softwareordner>/.ccmas` (`internal/cli/execdir.go`:
`resolveExecutionDir`, `stateDBPath`, `runDirPath`; CC-51 verwendet dieselben Funktionen, sein Socket liegt unter
`<Ausführungsbereich>/run/`). `.ccmas/` ist ignoriert (`.gitignore`). Der Pfad ist absolut und kanonisch (`Open` und
`Inspect` lehnen anderes mit `invalid` ab, ebenso `?` und `#`).

- Das Verzeichnis legt `Open` mit 0700 an, die Datei vor dem ersten Öffnen mit 0600; `-wal` und `-shm` erben die Rechte der
  Datenbank. Eine vorhandene Datei bleibt durch `Open` unverändert (kein Schreibzugriff, auch nicht auf den `journal_mode`);
  ihre Prüfung ist Sache von `Inspect` und `ccmas status`.
- SQLite gehört **nicht in einen synchronisierten Ordner** (iCloud „Mobile Documents“, `com~apple~CloudDocs`):
  `-wal`, `-shm` und Sperren lassen sich nicht synchronisieren, Konfliktkopien beschädigen die Datenbank. `status` weist darauf hin.

## Pragmas und Schreibstrategie

| Zweck | Einstellung |
| --- | --- |
| Pools | **Schreibpool mit genau einer Verbindung**, getrennter **Lesepool** (4 Verbindungen, `query_only(1)`) auf derselben Datei |
| Transaktionen | Schreibtransaktionen beginnen mit `BEGIN IMMEDIATE` (`_txlock=immediate`): eine gemeinsame Sperre, kein Upgrade-Deadlock |
| Wartezeit | `busy_timeout` 5 s (Option `WithBusyTimeout`); danach der stabile Code `busy`, kein Panic |
| Journal | `journal_mode=WAL` (Leser blockieren Schreiber nicht und umgekehrt), eine Eigenschaft der Datei, **nicht** des DSN: `Open` stellt eine von ihm **neu angelegte** Datei sofort auf WAL; eine **vorhandene** Datei stellt erst `Migrate` um, nach bestandener Plan- und Historienprüfung und vor dem ersten Schreiben. Eine vorhandene, nicht migrierte Datei ohne WAL verweigern `Update`/`View` mit `schema_outdated` (bei einer von dieser Software migrierten Datei unerreichbar) |
| Dauerhaftigkeit | `synchronous=FULL` (DSN) |
| Integrität | `foreign_keys=1` auf jeder Verbindung (DSN) |

Warum `journal_mode` nicht im DSN steht: Es würde schon beim ersten Verbindungsaufbau in die Datei schreiben. Eine vorhandene
Datenbank im Rollback-Modus würde so verändert, auch wenn `Migrate` danach `schema_too_new` oder `foreign_database` meldet.
So bleiben eine zu neue oder fremde Datei und jede Datei, deren Historie nicht stimmt, **byte-identisch** und ohne `-wal`/`-shm`
(Test `TestOpenAndFailedMigrateLeaveExistingFileUntouched`).

`Open` migriert nicht. `Update` und `View` verlangen eine migrierte Datenbank (sonst `schema_outdated`). Mutierende Methoden
gibt es nur auf `*Tx`.

## Migrationen

Die Migrationen liegen eingebettet unter `internal/persistence/migrations/NNN_name.sql`. `Migrate` ist ein expliziter Aufruf;
jede Migration läuft in einer eigenen IMMEDIATE-Transaktion samt ihrem Eintrag in `schema_migrations(version, name, sha256,
applied_at)`, `PRAGMA user_version` steht auf der höchsten angewandten Version. Es gibt kein Downgrade.

| Regel | Fehlercode |
| --- | --- |
| Die Datenbank trägt eine höhere Version als die Software kennt: kein Schreibzugriff | `schema_too_new` |
| Die Prüfsumme einer angewandten Migration weicht von der eingebetteten ab | `migration_checksum_mismatch` |
| Die angewandten Migrationen sind kein Präfix der bekannten (Version, Name), oder `user_version` passt nicht zur Historie | `migration_history_mismatch` |
| Fremde Datenbank: Objekte in `sqlite_master`, aber keine Tabelle `schema_migrations` (ohne jeden Schreibzugriff; die Prüfung auf „zu neu“ geht vor) | `foreign_database` |
| `Update`/`View` auf einer nicht vollständig migrierten Datenbank | `schema_outdated` |

Bekannte Versionen steigen streng (Lücken sind erlaubt). Ein Fehler der Prüfung hinterlässt keine Schreibwirkung. Reihenfolge in
`Migrate`: Zustand lesen, Plan und Historie prüfen, dann (vorhandene Datei) auf WAL umstellen, dann je Migration eine
IMMEDIATE-Transaktion.

## Tabellen

Schema 001 (`001_initial.sql`). Zeitstempel sind RFC 3339 in UTC mit neun Nachkommastellen; Aufzählungen sind CHECK-Listen,
die genau den Go-Listen entsprechen (`TestEnumChecksMatchDomain`); Dokumente (`…_json`) sind vor dem Schreiben gegen ihren
Schemavertrag geprüft; Spalten für Zugangsdaten gibt es nicht (`TestNoSecretColumns`).

| Tabelle | Zweck |
| --- | --- |
| `schema_migrations` | Migrationshistorie |
| `projects` | Projektzuordnung (`config_json`), `state_revision` |
| `profile_versions` | aufgelöstes `AgentProfile` je Version, unveränderlich (Ausführungen verweisen darauf) |
| `skill_snapshots`, `skill_snapshot_skills` | Skill-Snapshot (`domain.SkillSnapshot`) und Abfrageindex je Skill, unveränderlich |
| `agents` | Rolle und Verfügbarkeit, Zeilenrevision |
| `goals` | Ziel (Ticket, Feature, Milestone) mit Kriterien, Umfang, Modus, Abschlussnachweisen |
| `work_orders`, `work_order_dependencies` | Arbeitsauftrag mit fachlichem Zustand, Kopf-Commit, Branch, Ergebnis, Reparaturrunden, Zeilenrevision; Abhängigkeiten |
| `concept_states`, `pull_requests` | Konzeptstand mit Freigabe, PR-Stand eines Auftrags |
| `tasks` | Asana-Abbild (GID, Titel, Priorität, Section, Zeit der letzten Synchronisation); kein fachlicher Zustand |
| `executions`, `execution_attempts` | logische Ausführung; Versuch mit Ausführungszustand, Session-ID, Prozessidentität, beobachtetem Prozesszustand, Worktree, terminalem Ergebnis, prompt-freiem Spec-Datensatz |
| `plans`, `decisions` | Planungsergebnis (`DecisionBatch`) mit Urteil des Validators und der Revision, auf der es beruht; einzelne Entscheidungen |
| `questions`, `answers` | Fragen mit Zustand und Zustellbestätigung; Antworten (unveränderlich). `questions.answer_id` ist ein Fremdschlüssel `(project_id, answer_id, question_id)` auf `answers`: die Antwort muss zur selben Frage und zum selben Projekt gehören (bei `answer_id IS NULL` ohne Wirkung; Reihenfolge Frage, Antwort, Aktualisierung der Frage) |
| `review_reports` | Review-Berichte (Urteil, Commit, Konzeptversion) |
| `events` | Auditverlauf, nur anhängbar (Trigger sperren Ändern und Löschen) |
| `leases` | Reservierungen (Tabelle und Einschränkungen vorhanden, **Logik folgt in CC-42**); Zustandstyp `domain.LeaseState` |
| `messages`, `message_deliveries`, `outbox` | Nachrichten, Zustellung, externe Aktionen (Logik seit CC-45, siehe [`messaging.md`](messaging.md)); Zustandstypen `domain.DeliveryState` und `domain.OutboxState` |

Die Zustandslisten für Reservierung, Zustellung und Outbox (`domain.LeaseState` in `lease.go`, `domain.DeliveryState` in
`message.go`, `domain.OutboxState` in `outbox.go`) liegen in `internal/domain`, mit `Valid()`. **Übergangstabellen** mit
`CanTransition` gibt es seit CC-45 für die Zustellung und die Outbox (Trigger in Migration 003); die für die Reservierung
kommt mit CC-42. In `persistence/enums.go` stehen nur noch
interne Hilfslisten, die Typen aus `supervisor` und `skills` spiegeln.

## Zustandsrevision und Zeilenrevision

- **`projects.state_revision`** (ab 0) steigt beim Festschreiben um **genau 1**, wenn die Transaktion mindestens eine Änderung
  des **Planungszustands** dieses Projekts enthält; mehrere Änderungen in einer Transaktion zählen einmal, eine Transaktion ohne
  Änderung erhöht nichts. Das ist die Revision, gegen die `Decision`, `DecisionBatch` und `ProjectSnapshot` geprüft werden
  (veraltete Revision: `stale_revision`). Ein Trigger erlaubt nur Schritte von +1.
- **`work_orders.revision`** (ab 1) ist die Zeilenrevision für Compare-and-Set (`RequireWorkOrderRevision`); ebenso
  `agents.revision`.

Einteilung der Änderungen:

| Planungszustand (erhöht die Revision) | Journal und Beobachtung (erhöhen sie nicht) |
| --- | --- |
| Projektkonfiguration, Agenten, Ziele, Aufträge samt Konzeptstand, PR, Abhängigkeiten und Ergebnissen, Ausführungen und ihr Ausführungszustand, Fragen und Antworten, Review-Berichte (also alles, was der `ProjectSnapshot` zeigt) | Pläne und Entscheidungen (ein Plan gilt für genau die Revision, auf der er beruht), Profilversionen und Skill-Snapshots (unveränderliche Verweisziele), das Asana-Abbild `tasks`, Session-ID, Prozessbeobachtung, Nachrichten, Zustellungen und Outbox (siehe [`messaging.md`](messaging.md)) |

Die Journal-Einträge tragen im Ereignis die unveränderte aktuelle Revision; das Ereignis einer Änderung des Planungszustands
trägt die Revision **nach** der Änderung. Der Grund: Würde jede Aufzeichnung die Revision erhöhen, ließe sie den Plan veralten,
den sie festhält.

## Audit-Kopplung und Rollback

Jede fachliche Änderung schreibt ihr Audit-Ereignis (`events`, `domain.AuditEvent`) **in derselben Transaktion**. Die Details
bestehen aus typisierten Feldern (höchstens 4 KiB; keine Prompts, Umgebungsvariablen oder Zugangsdaten). Ein Rollback nimmt
Änderung, Ereignis und Revision zurück. Eine Änderung ohne Wirkung (gleicher Wert) schreibt nichts. Scheitert ein Aufruf,
ist die Transaktion verbraucht und wird nicht festgeschrieben, auch wenn der Callback den Fehler ignoriert.

## Getrennte Zustandsebenen

Auftragszustand (am Auftrag), Ausführungszustand (am Versuch), beobachteter Prozesszustand mit Beobachtungszeit und
Prozessidentität (PID und Startzeit, beide oder keins; am Versuch) und Agentenverfügbarkeit (am Agenten) sind getrennte
Spalten mit getrennten Typen (`internal/domain`) und getrennten Methoden. Nichts wird abgeleitet oder gemeinsam geändert:
Ein Prozessende mit Exit-Code 0 beendet weder Versuch noch Auftrag. Übergänge prüfen `CanTransition` der Ebene (sonst `state`)
und den erwarteten Ist-Zustand (sonst `conflict`); jede Ebene schreibt ihr eigenes Ereignis.

## Anfangszustand eines Auftrags

`CreateWorkOrder` nimmt **nur den Anfangszustand `waiting`** an. Jeder andere Zustand liefert den Code `state`, und es wird nichts
geschrieben (kein Auftrag, kein Ereignis, keine Revision). Fortschritt gibt es nur über `TransitionWorkOrder` (Übergangstabelle,
erwarteter Ist-Zustand, Audit-Ereignis); Testhelfer und Seeds (`persistencetest`) führen Aufträge deshalb über Übergänge in ihren
Zustand. Ein späterer Import bereits abgeschlossener Asana-Tickets (M06) braucht einen **eigenen, auditierten Weg** und darf
diese Regel nicht aufweichen (Test `TestCreateWorkOrderOnlyInWaiting`).

## Festlegung: Arbeitsauftrag statt Task

Die reservierbare, ausführbare Einheit ist der **Arbeitsauftrag** (`work_orders`; ID = `domain.WorkOrder.WorkOrderID` =
`supervisor.Order.ID`). Die Spezifikation nennt „tasks / dependencies“ mit fachlichem Zustand; ein Asana-Ticket ist aber nicht
dasselbe wie das, was ein Agent ausführt (Teil-, Korrektur- und Reparaturaufträge, Reservierung, Versuche, Reviews, Fragen).
Fachlicher Zustand und Abhängigkeiten liegen deshalb am Auftrag; **`tasks` ist das Asana-Abbild** (interne Task-ID,
eindeutige GID je Projekt, optional der eine Auftrag). Damit gibt es keine Doppelführung des Zustands.

## Abbildung des `ProjectSnapshot`

Der Snapshot-Bauer (CC-42) füllt den Snapshot aus der Datenbank. Die Leseschicht liefert alles in **einer** Lesetransaktion
(`Store.View`), also zu einer einzigen Revision.

| Feld des Snapshots | Quelle | Lesemethode |
| --- | --- | --- |
| `project_id`, `state_revision` | `projects` | `Project`, `Revision` |
| `goal` (Art, ID, Titel, Kriterien, Umfang, `out_of_scope`) | `goals` | `Goal`, `Goals` |
| `goal.evidence` | `goals.evidence_json` | `Goal` (`GoalRecord.Goal.Evidence`); geschrieben mit `SetGoalEvidence` |
| `integration_mode` | `goals.integration_mode` | `Goal` |
| `capacity.max_workers` | `projects.config_json` (`ProjectConfig.MaxWorkers`) | `Project` |
| `capacity.active` | `execution_attempts` in Nicht-Endzuständen, mit `executions` | `ActiveExecutions` |
| `orders[]` (`id`, `kind`, `title`, `state`, `mode`, `depends_on`) | `work_orders`, `work_order_dependencies` | `WorkOrders`, `WorkOrdersOfGoal`, `WorkOrder` |
| `orders[].concept` | `concept_states` | `WorkOrder` (`Concept`); geschrieben mit `SetConceptState` |
| `orders[].head_commit`, `branch` | `work_orders` | `WorkOrder`; `SetWorkOrderHead` |
| `orders[].last_result` | `work_orders.last_result_*` | `WorkOrder`; `SetLastResult` |
| `orders[].pr` | `pull_requests` | `WorkOrder`; `SetPullRequest` |
| `orders[].code_repair_rounds`, `concept_repair_rounds` | `work_orders` | `WorkOrder`; `SetRepairRounds` |
| `orders[].last_review` | `review_reports` mit der Rolle aus `executions.role` | `LastReview`, `Reviews` |
| `orders[].active_execution_id` | `execution_attempts` (Nicht-Endzustand) | `ActiveExecutions` |
| `orders[].review_concept_version` | **abgeleitet** (`Canonical`), nicht gespeichert | – |
| `open_questions[]` | `questions` (Zustand `open`) | `OpenQuestions` |
| `roles[]`, `skills[]` | **nicht aus SQLite**: aus `agents.yaml`, `models.yaml` und dem Skill-Katalog | `supervisor.BuildRoleOffers`, `BuildSkillOffers` |

Die Konzeptfreigabe abhängiger Aufträge (Übertragung des freigegebenen Konzeptstands) und die Befüllung des Snapshots gehören
dem Snapshot-Bauer (CC-42); dieses Paket liefert die gespeicherten Werte unverändert.

## Fehlercodes des Pakets

`*persistence.Error` mit stabilem `Code` (`CodeOf`):

| Code | Bedeutung |
| --- | --- |
| `busy` | Datenbank blieb über den `busy_timeout` hinaus gesperrt; Wiederholen ist zulässig |
| `schema_outdated` | nicht (vollständig) migriert |
| `schema_too_new` | Schemaversion höher als bekannt; kein Zugriff, kein Downgrade |
| `migration_checksum_mismatch` | Prüfsumme einer angewandten Migration weicht ab |
| `migration_history_mismatch` | Historie ist kein Präfix der bekannten Migrationen, oder `user_version` passt nicht dazu |
| `foreign_database` | Tabellen ohne Migrationshistorie (`schema_migrations`): die Datei gehört nicht zu CCMAS; kein Schreibzugriff |
| `not_found` | Entität gibt es nicht |
| `conflict` | gibt es schon mit anderem Inhalt, oder erwarteter Ist-Zustand stimmt nicht |
| `state` | Übergang ist nach der Übergangstabelle nicht erlaubt (Ursache `*domain.ContractError`) |
| `invalid` | Eingabe verletzt ihren Vertrag; nichts wurde geschrieben (Ursache `*domain.ContractError`) |
| `constraint` | die Datenbank hat die Änderung abgewiesen (Fremdschlüssel, CHECK, Trigger) |
| `closed` | Store oder Transaktion geschlossen |
| `internal` | unerwarteter Fehler der Datenbank oder des Dateisystems |

## Nur lesende Prüfung und `ccmas status`

`persistence.Inspect(ctx, path string) (Report, error)` prüft eine bestehende Datenbank **ohne Schreibwirkung** und ist die
Grundlage von `ccmas status`. Ein Fehler ist nur ein ungültiger Pfad (`invalid`) oder ein abgebrochener Kontext; alles andere,
auch eine fehlende, kaputte oder fremde Datei, steht im Bericht.

**Keine Schreibwirkung:** Weder Datei noch Verzeichnis werden angelegt (fehlende Datenbank: `exists=false`, Hinweis, kein
Befund), kein `journal_mode` wird gesetzt, keine Rechte geändert, nichts migriert, nach der Rückkehr bleibt keine neue Datei
liegen, die Hauptdatei bleibt byte-identisch. Alle Abfragen laufen in **einer** Lesetransaktion: Der Bericht zeigt den
festgeschriebenen Stand, auch bei offenem Schreiber; WAL-Leser blockieren Schreiber nicht.

**Bericht** (`Report`, stabile JSON-Namen): `path`, `exists`, `size_bytes`, `read_mode`, `permissions` (Verzeichnis, Datei,
`-wal`, `-shm`: `exists`, `mode`, `insecure`, `size_bytes`), `journal_mode`, `schema` (`user_version`, `highest_applied`,
`highest_known`, `applied`, `pending`), `integrity` (`quick_check`, `foreign_key_violations`), `data` (je Projekt
`state_revision`, Aufträge je Zustand, `active_executions`, `open_questions`, Outbox je Zustand; `events`, `last_event_at`),
`notes` (Hinweise) und `findings` (Befunde).

**Befunde** (`findings[].code`): `schema_too_new`, `migration_checksum_mismatch`, `migration_history_mismatch`, `foreign_database`
(Tabellen ohne `schema_migrations`), `quick_check_failed`, `foreign_key_violations`, `insecure_permissions` (Gruppen- oder
Weltrechte an Verzeichnis, Datei, `-wal` oder `-shm`; unter Windows entfällt die Prüfung), `unreadable` (keine SQLite-Datei,
kaputt, keine reguläre Datei, nicht lesbar), `busy`. **Hinweise, keine Befunde:** ausstehende Migrationen und ein Pfad in einem
iCloud-Ordner.

### Verhalten von `mode=ro` bei einer WAL-Datenbank

Gemessen mit `modernc.org/sqlite` v1.61.0 (SQLite 3.54.1) auf macOS (Tests `TestInspect…`); Linux ist hier nicht gemessen.

| Lage | `mode=ro` | `immutable=1` |
| --- | --- | --- |
| geschlossen, kein `-wal`/`-shm`, Verzeichnis beschreibbar | liest, **legt aber `-wal` (leer) und `-shm` (32 KiB) an und lässt sie liegen** | liest, legt nichts an |
| wie oben, Verzeichnis nicht beschreibbar | Fehler „attempt to write a readonly database“ | liest |
| Schreiber offen (`-wal` und `-shm` vorhanden) | liest den festgeschriebenen Stand samt noch nicht zurückgeschriebener WAL-Daten, legt nichts an, auch bei nicht beschreibbarem Verzeichnis oder Datei 0400 | ignoriert das WAL (Daten nur im WAL fehlen) |
| `-wal` ohne `-shm` (Reste) | **legt `-shm` an und lässt es liegen**; bei nicht beschreibbarem Verzeichnis Fehler „unable to open database file“ | ignoriert das WAL |

`immutable=1` meldet außerdem `journal_mode=delete`, weil es das WAL nicht öffnet; der Modus wird deshalb aus dem Dateikopf
gelesen. Aus der Messung folgt die Wahl des Lesewegs (`read_mode`):

| Lage | Leseweg |
| --- | --- |
| weder `-wal` noch `-journal` | `immutable`: ohne Sperren und ohne neue Dateien; sicher, weil kein Schreiber aktiv ist |
| `-wal` und `-shm` vorhanden (Schreiber offen) | `readonly`: `mode=ro` auf dem Original; legt nichts an |
| `-wal` ohne `-shm` | `snapshot_copy`: Datei und `-wal` werden in ein privates 0700-Temp-Verzeichnis kopiert (höchstens 256 MiB) und dort mit `mode=ro` gelesen; das Verzeichnis wird gelöscht |
| `-journal` vorhanden (Rollback-Modus) | `readonly` |

Alle Wege öffnen mit `query_only(1)` und kurzem `busy_timeout` (2 s).

**Grenzen (ehrlich):**

1. `immutable` liest ohne Sperre. Wird währenddessen ein Schreiber aktiv, erkennt `Inspect` das am Erscheinen von
   `-wal`/`-journal` oder an einer Änderung von Größe oder Zeit der Hauptdatei, verwirft den Versuch und wiederholt ihn
   (höchstens dreimal, sonst Befund `busy`). Ein Schreiber, der innerhalb des Lesefensters beginnt und endet, ohne die Hauptdatei
   zu verändern, ist nicht auszuschließen; er ändert auch nichts am Gelesenen.
2. Auf dem `readonly`-Weg kann sich der Inhalt von `-shm` ändern (Wal-Index, Lesemarken); Hauptdatei und `-wal` bleiben
   byte-identisch. Endet der letzte Schreiber genau zwischen der Prüfung der Nachbardateien und dem Öffnen, kann `mode=ro` leere
   `-wal`/`-shm` anlegen.
3. Ein offener Lesezugriff hält eine Lesemarke; er verhindert ein Zurücksetzen des WAL nur für die Dauer der Prüfung.
4. Ein heißes Rollback-Journal kann ein nur lesender Zugriff nicht zurückspielen; das Ergebnis ist ein Befund `unreadable`.
   Dieser Weg ist nur mit einem leeren Journal getestet.

### `ccmas status`

`ccmas status [--execution-dir <pfad>] [--root <pfad>] [--json]` ersetzt den früheren Platzhalter. Es installiert nichts,
startet nichts, schreibt nichts und führt keine Agentenausführung aus. Der Abschnitt „Zustand (SQLite)“ zeigt den Bericht von
`Inspect` samt Stufe (`not_present`, `present`, `configured`, `functional_read`):

- `present`: Datei vorhanden, aber nicht vollständig migriert oder mit Befund im Schema;
- `configured`: Schema aktuell, Historie und Prüfsummen stimmig;
- `functional_read`: zusätzlich Integrität und Grunddaten lesend belegt. Das ist **nur der Lesepfad**; der Schreibpfad des
  Dienstes wird nicht geprüft. „Angemeldet“ gibt es für eine lokale Datei nicht.

Ausdrücklich **nicht geprüft** und so ausgewiesen (`not_checked` im JSON): Dienstzustand (folgt mit CC-51) sowie Werkzeuge,
Anmeldung und Projektzuordnung (folgen mit M06). Exit-Codes: 0 (Datenbank fehlt oder ohne Befund; ausstehende Migrationen sind
ein Hinweis), 1 (mindestens ein Befund), 2 (Bedienfehler, auch ein nicht bestimmbarer Ausführungsbereich). Weitere Exit-Codes
(4, 5, 6) hat der Projektinhaber für CC-51 festgelegt; sie gehören nicht zu CC-27 und sind nicht Aussage der Spezifikation.

## Migrationsnummern und Schemagrenzen für die Folgetickets

Die Migrationsnummern der parallelen Folgetickets sind vergeben, damit sie nicht kollidieren: **CC-42 → `002`**, **CC-45 → `003`**,
**CC-51 → `004`** (falls nötig). **`003` ist belegt** (`003_message_delivery_checks.sql`: Nachrichten, Zustellung und Outbox; nur Trigger
und eine Spalte `outbox.reconcile_attempts` per `ADD COLUMN`, kein Tabellenumbau). Eine bereits angewandte Migration wird nie geändert (Prüfsumme, `migration_checksum_mismatch`);
Änderungen kommen als neue Migration. Nur `001` durfte noch geändert werden, solange es keine produktive Datenbank gibt.

**Tabellen-Umbauten** (neue Tabelle anlegen, Zeilen kopieren, alte löschen, umbenennen) brauchen wegen `foreign_keys=ON`
`PRAGMA defer_foreign_keys = ON` am Anfang der Migration: Das Löschen einer referenzierten Tabelle gilt sonst als Verstoß, und
`PRAGMA foreign_keys` selbst lässt sich innerhalb einer Transaktion nicht umschalten. Die Prüfung wird damit bis zum Festschreiben
aufgeschoben; verletzte Bezüge brechen das Festschreiben ab.

**Was das Schema nicht erzwingt und CC-42/CC-45 deshalb in Go prüfen müssen:**

- **Lease und Versuch:** `leases.(project_id, execution_id, attempt)` verweist auf einen Versuch **desselben Projekts**, nicht
  auf einen Versuch desselben **Auftrags**. Dass die Ausführung zum Auftrag der Lease gehört, prüft die Reservierungslogik.
- **Outbox:** `outbox.operation_key` ist **global** eindeutig (`UNIQUE`), nicht je Projekt. Der Schlüssel muss das Projekt
  enthalten, sonst kollidieren gleiche Operationen verschiedener Projekte als `conflict`. Seit CC-45 bildet und prüft
  `domain.OperationKey` den Schlüssel (`<project_id>/<kind>/<teil>…`) und ein Trigger (Migration 003) erzwingt das Präfix
  `<project_id>/<kind>/` auch gegen direktes SQL; die Aktionen bilden ihren Schlüssel mit `domain.NewOutboxAction`.

## Plattformnachweis

Die Tests laufen auf **darwin/arm64** grün (Go 1.27.3, SQLite 3.54.1, `modernc.org/sqlite` v1.61.0). Linux und Windows sind nur
**cross-gebaut** (`make build-all`), nicht ausgeführt; das Verhalten von `mode=ro`/`immutable` und der Rechteprüfungen dort ist
nicht gemessen.

## Was erst später folgt

| Ticket | Inhalt |
| --- | --- |
| **CC-42** | Reservierung und Leases (Logik auf `leases`, Übergangstabelle für `LeaseState`), Snapshot-Bauer (`ProjectSnapshot` aus der Datenbank), Orchestrator-Aufruf des Validators |
| **CC-45** | umgesetzt: Nachrichten, Zustellung und Outbox mit Übergangstabellen (`CanTransition`) für `DeliveryState` und `OutboxState`, Abgleich nach ungewissem Ergebnis, begrenztem Backoff und Klärungsbedarf; siehe [`messaging.md`](messaging.md). Offen bleiben die echten Asana- und GitHub-Adapter (M05/M06) und die CLI-Anbindung der Klärung (CC-51) |
| **CC-51** | Dienst (Schreiber der Datenbank, Steuer-Socket unter `<Ausführungsbereich>/run/`), Idempotenz von Kommandos, die weiteren Exit-Codes von `status` (4, 5, 6; vom Projektinhaber festgelegt) |

## Prüfbefehle

```bash
go test ./internal/persistence/... ./internal/cli/... ./internal/domain/...
go test -race -count=3 ./internal/persistence/... ./internal/cli/...
make check          # test, race, vet, build, py-test
make build-all      # Cross-Builds ohne cgo (darwin arm64/amd64, linux amd64, windows amd64)
go list -m modernc.org/sqlite
bin/ccmas status --execution-dir <ordner> [--json]
```
