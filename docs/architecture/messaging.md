# CCMAS – Nachrichten und Outbox (`internal/domain`, `internal/persistence`, `internal/orchestrator`)

Stand: Ticket CC-45 (Meilenstein M03). **Führend ist die Spezifikation** „Multi Agent Setup“ (Version 1.7.0), Abschnitte
„Nachrichten“, „Externe Aktionen ohne Dubletten“ und „Führender interner Zustand“. Diese Datei spiegelt den Code (die
Kommentare in `internal/domain/message.go`, `outbox.go`, `outbox_action.go`, `outbox_outcome.go` und
`internal/persistence/doc.go` sind die Quelle) und ist keine Kopie der Spezifikation. Bei Abweichungen gilt der Code, und die
Datei wird nachgezogen. Das Schema liefern `001_initial.sql` und `003_message_delivery_checks.sql`.

Nachrichten und Outbox sind **Journal**, kein Planungszustand: Sie stehen nicht im `ProjectSnapshot` und erhöhen
`projects.state_revision` nicht (siehe [`persistence.md`](persistence.md)). Jede Änderung schreibt ihr Audit-Ereignis in derselben
Transaktion; die Ereignisse tragen nie Nutzdaten.

## Nachrichten (W45.1)

### Arten und Adressen

`domain.MessageKind` (Liste = Prüfung der Datenbank auf `messages.type`): `task_start`, `task_progress`, `task_result`,
`review_request`, `dependency_request`, `supervisor_decision`, `agent_message`, `question`, `answer`, `delegation_request`. Das
ist nicht der `MessageType` des Runner-Protokolls.

Absender und Empfänger sind **Adressen**, kein Freitext: `orchestrator`, `supervisor`, `cli`, `agent:<id>` und `execution:<id>`
(ID im Format von `CheckID`, Adresse höchstens 128 Zeichen). Eine Nachricht hat 1 bis 16 Empfänger ohne Dubletten und je
Empfänger eine eigene Zustellung. Eine Delegation (`delegation_request`) geht von einem Agenten oder einer Ausführung allein an
den Orchestrator und startet nichts: Reservierung und Start gehören dem Orchestrator (CC-42).

### Pflichtbezüge je Art

Die Spezifikation nennt als Mindestinhalt einer Nachricht „Auftrag/Task“ und „Ausführung“ (Ausführung mit Versuch). Das Ticket
verlangt, diese Korrelation zu erzwingen. `Message.Check` prüft deshalb je Art (`MessageKind.RequiredLinks`), bevor die
Persistenz etwas schreibt; ein Verstoß ist `invalid` und hinterlässt keine Zeile und kein Ereignis:

| Art | Auftrag | Ausführung und Versuch | Begründung |
| --- | --- | --- | --- |
| `task_start`, `task_progress`, `task_result` | Pflicht | Pflicht | Sie beschreiben den Lauf eines Versuchs. |
| `delegation_request` | Pflicht | Pflicht | Eine Delegation fordert nur eine laufende Ausführung an; ohne sie wüsste der Orchestrator nicht, für wen er reserviert. Absender `agent:`/`execution:`, Empfänger allein `orchestrator`. |
| `review_request`, `dependency_request`, `question`, `answer` | Pflicht | wo vorhanden | Sie gehören zu einem Auftrag, müssen aber nicht aus einer Ausführung stammen (zum Beispiel eine Frage des Orchestrators an die CLI, eine Antwort der CLI). |
| `supervisor_decision` | optional | optional | Eine Entscheidung kann ein Ziel oder einen Batch betreffen und nicht einen einzelnen Auftrag. |
| `agent_message` | optional | optional, aber Pflicht für Absender `agent:`/`execution:` | s. u. |

Zwei Regeln gelten über alle Arten:

- **Sendende Ausführung:** Ist der Absender `agent:<id>` oder `execution:<id>`, sind Auftrag, Ausführung und Versuch
  Pflicht, gleich welche Art. Eine Nachricht eines Agenten ohne die sendende Ausführung ließe sich keinem Versuch zuordnen.
- **Eigene Ausführung:** Ist der Absender `execution:<id>`, muss `ExecutionID` genau diese ID sein (sonst `invalid` an
  `/execution_id`). Ein Absender kann nicht für eine fremde Ausführung sprechen.

Die Ausnahmen („optional“) gelten nur dort, wo fachlich keine Ausführung existiert: bei Nachrichten von Orchestrator, Supervisor
oder CLI, die ein Ziel, einen Batch oder das Gespräch mit dem Menschen betreffen. Es gibt keine Ausnahme für Nachrichten, die
ein Agent oder eine Ausführung sendet.

Die vorhandene Konsistenz bleibt: Eine Ausführung gibt es nur mit Versuch und Auftrag, und die Persistenz prüft, dass Projekt,
Auftrag und Versuch existieren und dass die Ausführung zum genannten Auftrag gehört (Fremdschlüssel binden nur das Projekt).

Nutzdaten sind ein JSON-Objekt (höchstens 64 KiB, ohne doppelte Schlüssel, Ganzzahlen statt Kommazahlen); `Message.CanonicalPayload()`
liefert die kanonische Form (sortierte Schlüssel, kompakt). Gleiche Nutzdaten ergeben dieselben Bytes; darauf beruht die
Duplikaterkennung beim erneuten Anlegen.

### Zustellung

`domain.DeliveryState`, Tabelle `message_deliveries` (Trigger in Migration 003):

| Übergang | Bedeutung |
| --- | --- |
| `pending` → `delivered` | erste Zustellung, zählt als Versuch |
| `delivered` → `delivered` | Wiederholung einer zugestellten, nicht bestätigten Nachricht (zum Beispiel nach einem Neustart), weiterer Versuch |
| `delivered` → `acknowledged` | Quittung des Empfängers; Endzustand |

Eine Bestätigung ohne vorherige Zustellung (`pending` → `acknowledged`) ist verboten (`state`): Sie würde eine Nachricht
schließen, die der Empfänger nie erhalten hat. Aus „bestätigt“ gibt es keinen Weg zurück.

### Duplikaterkennung

- **Anlegen:** Gleiche `message_id` mit gleichem Inhalt (gleiche Felder, gleiche Empfängermenge, gleiche kanonische Nutzdaten)
  ist ein No-op ohne Ereignis; mit anderem Inhalt, auch in einem anderen Projekt, `conflict` (`message_id` ist global eindeutig).
- **Zustellen:** `Messages.Deliver` liefert eine bereits bestätigte Nachricht nicht erneut aus (`Duplicate`, ohne Schreibwirkung).
  Eine zugestellte, nicht bestätigte Nachricht kommt als `Redelivery` mit höherem Zähler (`DeliverPending` nach einem Neustart).
- **Bestätigen:** Eine zweite Bestätigung ist ein No-op ohne zweites Ereignis.

Ereignisarten (Ebene `message`): `message.created`, `message.delivered`, `message.redelivered`, `message.acknowledged`
(`domain.MessageAuditKinds`).

## Outbox für externe Aktionen (W45.2)

Eine SQLite-Transaktion umfasst keine gleichzeitig atomare Asana- oder GitHub-Aktion. Der Dienst speichert deshalb die
**Absicht vor der Ausführung** (in derselben Transaktion wie die auslösende fachliche Änderung) und **gleicht unsichere
Ergebnisse ab**, statt blind erneut anzulegen. Ein Timeout beweist weder Erfolg noch Misserfolg.

### Zustände und Übergänge

`domain.OutboxState` mit `CanTransition` und `IsTerminal`; die Datenbank erzwingt dieselbe Tabelle durch Trigger.

| Zustand | erlaubte Folgezustände |
| --- | --- |
| `pending` | `claimed`, `cancelled` |
| `claimed` | `succeeded`, `retry_wait`, `reconciling`, `needs_attention` |
| `retry_wait` | `claimed`, `cancelled`, `needs_attention` |
| `reconciling` | `reconciling` (weiterer Abgleich), `succeeded`, `pending`, `needs_attention` |
| `needs_attention` | `pending`, `succeeded`, `cancelled` (nur als ausdrückliche, auditierte Klärung) |
| `succeeded`, `cancelled` | Endzustände |

Mit Absicht fehlt `claimed` → `pending` (und `claimed` → `claimed`): Ein ungewisser Ausgang führt nie direkt zurück in die
Warteschlange, sondern über `reconciling`.

### Schlüssel und Marker

- **Schlüssel** `operation_key`: `<project_id>/<kind>/<teil>[/<teil>…]`, zum Beispiel `ccmas/asana.comment/wo-1/result`.
  `domain.OperationKey` bildet, `CheckOperationKey` und `ParseOperationKey` prüfen ihn. Er enthält das Projekt, weil die Spalte
  **global** eindeutig ist. Die 1 bis 6 stabilen Teile haben das Format einer ID (kein `/`) und nennen, was die Absicht
  eindeutig macht (Auftrag, Zweck); sie enthalten weder Zeit noch Zufall, damit eine Wiederholung derselben Absicht denselben
  Schlüssel ergibt. Höchstens 256 Zeichen. Die Datenbank prüft das Präfix `<project_id>/<kind>/` durch Trigger.
- **Aktions-ID:** `op-` plus 24 Hexzeichen (SHA-256 des Schlüssels); sie folgt aus dem Schlüssel (`domain.OutboxActionID`).
- **Marker:** `ccmas-op:` plus dieselben 24 Hexzeichen (`domain.OperationMarker`, `OutboxAction.Marker()`). Der Adapter schreibt
  ihn in das externe Objekt (Beschreibung, Kommentar, PR-Text) und sucht ihn beim Abgleich. Er ist deterministisch und verrät den
  Schlüssel nicht im Klartext.

### Ablauf

```text
Enqueue (gleiche Transaktion wie die fachliche Änderung)      pending
Claim  (IMMEDIATE-Transaktion, Versuch +1, Claim bis jetzt+TTL) claimed
Execute (Adapter, mit Zeitgrenze)
  Erfolg mit Referenz ........................................ succeeded
  sicher wiederholbar ........................................ retry_wait (Backoff)  oder needs_attention (Höchstzahl)
  dauerhaft .................................................. needs_attention
  ungewiss (Timeout, Verbindungsabbruch, Panic, Erfolg ohne
            verwertbare Referenz) ............................ reconciling
Prozess stirbt, Claim läuft ab ............................... reconciling (nie pending)
```

`Outbox.RunOnce(ctx, projectID)` (Paket `orchestrator`) tut in dieser Reihenfolge: abgelaufene Claims nach `reconciling`, fällige
Abgleiche, dann fällige Aktionen **einzeln** beanspruchen, ausführen und das Ergebnis speichern. Der Claim einer Aktion läuft nie,
während eine andere Aktion desselben Durchlaufs ausgeführt wird. Das Ergebnis wird mit einem vom Abbruch gelösten Kontext
gespeichert, damit ein gelungener Aufruf nicht verloren geht. `Outbox.Run` wiederholt Durchläufe und wartet bis zur nächsten
Fälligkeit (höchstens `PollInterval`); es endet mit dem Kontext.

### Abgleich

Der Adapter sucht das Objekt (`ExternalAdapter.Lookup`) über bekannte externe IDs und den Marker:

| Ergebnis der Suche | Folge |
| --- | --- |
| gefunden (`LookupFound`, mit Referenz) | `succeeded` mit der Referenz, **kein zweites `Execute`** |
| sicher nicht vorhanden (`LookupAbsent`) | `pending`, ein erneuter Versuch ist zulässig; ist die Höchstzahl der Versuche erreicht, `needs_attention` |
| nicht schlüssig (`LookupUnknown`, Fehler, Timeout) | bleibt `reconciling`, Abgleichzähler +1, nächster Abgleich mit Backoff; nach `MaxReconcileAttempts` nicht schlüssigen Abgleichen `needs_attention` |
| dauerhaft unmöglich (`LookupBlocked`, zum Beispiel keine Berechtigung) | sofort `needs_attention` |

Nur ein **maßgebliches** Ergebnis darf „nicht vorhanden“ melden (ein direkter Lesezugriff, keine verzögerte Suche, die ein
frisch angelegtes Objekt noch nicht kennt). Im Zweifel `LookupUnknown`. Ein Adapter ohne Registrierung für das System der Aktionsart
führt sichtbar nach `needs_attention` (`no_adapter`).

### Fehlerklassen, Backoff, Grenzen

`domain.OutboxOutcome`: `success`, `retryable`, `permanent`, `uncertain`. **Wiederholbar ist nur, was belegt, dass das externe
System nichts getan hat.** Alles andere, was kein Erfolg ist, ist ungewiss. `orchestrator.ClassifyTransportError` ordnet
Netzfehler so ein (Timeout, Abbruch, EOF, Verbindungsabbruch, Unbekanntes: ungewiss; verweigerte Verbindung beim Aufbau und
DNS „nicht gefunden“: wiederholbar).

`orchestrator.OutboxPolicy` (Nullwerte = Standard, `DefaultOutboxPolicy`):

| Feld | Standard | Bedeutung |
| --- | --- | --- |
| `ClaimTTL` | 2 min | Gültigkeit eines Claims; muss länger sein als `ExecuteTimeout` und `LookupTimeout` |
| `ExecuteTimeout`, `LookupTimeout` | 30 s | Zeitgrenze eines Adapteraufrufs |
| `PersistTimeout` | 10 s | Zeitgrenze beim Speichern eines Ergebnisses |
| `MaxAttempts` | 6 | Aufrufe je Aktion; danach `needs_attention` (`retries_exhausted`) |
| `MaxReconcileAttempts` | 5 | tolerierte nicht schlüssige Abgleiche; der nächste führt nach `needs_attention` (`reconcile_exhausted`) |
| `BackoffInitial`, `BackoffMax` | 10 s, 10 min | Abstand `initial · 2^(n-1)`, gedeckelt (`BackoffDelay`): 10, 20, 40, 80, 160 s … |
| `BatchSize` | 20 | Aufrufe und Abgleiche je Durchlauf |
| `PollInterval` | 30 s | längste Wartezeit von `Run` |
| `Now`, `Wait` | `time.Now`, Timer | austauschbar; Tests schlafen nie wirklich |

Der Backoff ist begrenzt (Deckel, Höchstzahl) und über den Kontext unterbrechbar; er hat kein Jitter, weil ein einziger Dienst
schreibt.

### Klärungsbedarf

`needs_attention` ist sichtbarer Klärungsbedarf: Die Aktion steht in `Outbox.NeedsAttention` und in der Zählung je Zustand von
`persistence.Inspect` (`ccmas status`). Hinaus führt nur `Outbox.Resolve(ctx, projectID, actionID, actor, resolution)` mit
Weg, Akteur und Grund (Ereignis `outbox.resolved`):

| Weg | Folge |
| --- | --- |
| `retry` | `pending`, Versuchs- und Abgleichzähler zurückgesetzt. Der Mensch verantwortet, dass das externe Objekt nicht existiert |
| `confirm` | `succeeded` mit der angegebenen externen Referenz, ohne Ausführung |
| `cancel` | `cancelled` |

Die CLI-Anbindung folgt mit CC-51.

### Teilbatches

Jede Aktion eines Batches ist eine **eigene Zeile** mit eigenem Schlüssel und wird **einzeln** ausgeführt und bestätigt.
`Outbox.EnqueueBatch` legt die Absichten in einer Transaktion an (ganz oder gar nicht); danach ist ein Teilfehler eine Sache der
einzelnen Zeile und macht erfolgreiche Teile nicht rückgängig. Erledigte Aktionen werden nie erneut ausgeführt.

### Audit und Geheimnisse

Ereignisarten (Ebene `outbox`, `domain.OutboxAuditKinds`): `outbox.enqueued`, `outbox.claimed`, `outbox.succeeded`,
`outbox.retry_scheduled`, `outbox.uncertain`, `outbox.claim_expired`, `outbox.reconcile_retry`, `outbox.requeued`,
`outbox.needs_attention`, `outbox.resolved`, `outbox.cancelled`. Details: Aktionsart, Zähler, Fehlercode, Zeitpunkt, bei
Erfolg und Klärung die externe Referenz und der Grund; nie Nutzdaten oder Fehlermeldungen.

`outbox.last_error` enthält nur die **Fehlerkennung** `code: kurze meldung` (`domain.OutboxError`): Code in `a-z0-9_.-`, höchstens
64 Bytes; Meldung ohne Steuerzeichen und ungültiges UTF-8, Leerraum zusammengefasst, höchstens 256 Bytes (bei Kürzung mit „…“).
Die Begrenzung geschieht vor dem Speichern, in der Persistenzschicht. **Adapter dürfen keine Zugangsdaten, Header,
Antwortkörper oder Nutzdaten in Meldungen geben**; das kann keine Prüfung beweisen, und Logs und Fehlertexte werden nicht für
Secrets entschärft, die ein Adapter hineinschreibt.

### Grenzen

- Die echten Asana- und GitHub-Adapter folgen mit M05/M06; hier gilt nur die Schnittstelle `ExternalAdapter`. Ihr Vertrag (Marker
  schreiben, vor dem Anlegen prüfen, maßgebliches „nicht vorhanden“) steht im Kommentar der Schnittstelle.
- Der Abgleich ist so gut wie die Suche des Adapters. Ein Adapter, der „nicht vorhanden“ meldet, obwohl das Objekt existiert
  (verzögerter Suchindex), verursacht ein Duplikat. Deshalb schreibt der Adapter den Marker und prüft ihn in `Execute` ein zweites Mal.
- Läuft ein Aufruf länger als `ClaimTTL`, nimmt ein anderer Prozess die Aktion in den Abgleich. Ein später eintreffender Erfolg
  wird aus `reconciling` übernommen; ein später eintreffender Fehlschlag nicht (die Aktion bleibt im Abgleich).
- Eine eskalierte Aktion wartet auf einen Menschen; es gibt keinen automatischen Weg aus `needs_attention`.
- Die Tabelle `outbox` kennt keine Aktionsarten-Liste, nur die Form `<system>.<aktion>` (`CheckOutboxKind`).

## Prüfbefehle

```bash
go test -count=1 ./internal/domain/... ./internal/persistence/... ./internal/orchestrator/...
go test -race -count=3 ./internal/persistence/... ./internal/orchestrator/...
go test -count=1 -run 'TestCrashAfterRemoteSuccess|TestBatchPartialFailure|TestTimeoutIsNotFailure|TestReconcileNotFoundRetries|TestUnresolvableNeedsAttention|TestBackoffBounded|TestNeedsAttentionResolution' ./internal/orchestrator/
go test -count=1 -run 'TestEnqueueKeyConflict|TestEnqueueInSameTransaction|TestConcurrentClaim|TestOutbox' ./internal/persistence/
```
