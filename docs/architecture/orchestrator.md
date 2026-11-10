# Orchestrator: Reservierung und sichere Zustandsübergänge (CC-42)

Stand: CC-42 (W42.1 und W42.2). Das Paket `internal/orchestrator` verbindet Planung und Zustand. Es **entscheidet nichts
inhaltlich**: Was zulässig ist, sagt der Validator ([`supervisor.md`](supervisor.md)); was gespeichert wird und wie,
regelt die Persistenzschicht ([`persistence.md`](persistence.md)). Der Orchestrator sorgt dafür, dass beides in **einer
Transaktion** zusammenkommt: Ein angenommener Batch reserviert Arbeit höchstens einmal, ein Ereignis eines alten Versuchs ändert
nichts, und kein Ergebnis eines Runners schließt einen Auftrag ab.

Nicht Teil dieses Pakets (gehört zu späteren Tickets): die CLI `task start/cancel/resume` (CC-51 bindet sie an die Operationen
unten), das Senden von Stopp- und Antwortsignalen an den Runner (M04), die Wiederherstellung nach Absturz und die
Crash-Serie (M09), Nachrichten und Outbox (CC-45).

## Bausteine

| Baustein | Aufgabe |
| --- | --- |
| `BuildSnapshot`, `StoreSnapshotSource` | `supervisor.ProjectSnapshot` aus SQLite (W42.1) |
| `Tasks` (`NewTasks`) | Reservierung, Ereignisse der Runner, Abbruch, Wiederaufnahme (W42.2) |
| `orderStartTarget`, `orderFinishTarget` | Tabelle der Auftragsübergänge je Rolle (`transitions.go`) |
| `TaskError`, `TaskClass` | stabile Fehlerklassen |

`NewTasks(store, policy, offers, goal, actor, TasksPolicy)`:

- `policy` und `offers` sind dieselben wie bei der Planung (Beispielkonfiguration und Skill-Katalog); `policy.Check()` muss
  bestehen.
- `goal` (`TasksGoalFunc`, oder `TasksFixedGoal("ms-1")`) liefert je Projekt das beauftragte Ziel, gegen das der Snapshot gebaut
  und der Batch erneut geprüft wird.
- `actor` ist der Akteur im Auditverlauf für Reservierungen und Ereignisse der Runner. `CancelTask` und `ResumeTask` nennen ihren
  Akteur selbst (der Mensch, der es veranlasst).
- `TasksPolicy`: `Now` (Uhr für Beginn und Ablauf der Lease, Standard `time.Now`) und `LeaseGrace` (Zuschlag, Standard 5 Minuten,
  `DefaultLeaseGrace`). Der Store hat seine eigene Uhr (Zeit der Transaktion); in Produktion sind beide `time.Now`.

`Tasks` ist nebenläufig nutzbar, auch über mehrere `Store`-Instanzen auf derselben Datei: Die Schreibtransaktion ist IMMEDIATE,
die Datenbank serialisiert.

## Reservierung

### `ReserveBatch(ctx, projectID, batchID) ([]domain.ExecutionLease, error)`

Reserviert **alle** Delegationen eines gespeicherten Batches in **einer** IMMEDIATE-Transaktion (`Store.Update`). Begründung:
Jede Reservierung ist Planungszustand und erhöht die Revision. Einzelreservierungen nacheinander ließen die übrigen
Entscheidungen desselben Batches veralten. Entweder gelingen alle, oder nichts ändert sich (keine Zeile, kein Ereignis, keine
Revision).

Ablauf in der Transaktion, in dieser Reihenfolge:

1. **Plan lesen.** Das Urteil muss `accepted` sein, sonst Klasse `rejected` mit den gespeicherten Ablehnungen.
2. **Doppelte Entscheidung.** Hat eine Delegation des Batches schon reserviert (`DecisionLease`; die Datenbank hält dasselbe als
   `UNIQUE (project_id, batch_id, decision_id)`), ist das ein `conflict`, ohne zweite Wirkung. Die Prüfung steht **vor** der
   Revisionsprüfung: Die erste Reservierung hat die Revision erhöht, und die erneute Zustellung desselben Batches soll als
   Konflikt erscheinen, nicht als „veraltet“.
3. **Revision.** `plan.state_revision` muss der aktuellen `projects.state_revision` gleichen, sonst Klasse `revision`
   (neu planen).
4. **Validator erneut, in der Transaktion.** `BuildSnapshot(tx, …)` und `Validator.Accept(batch, snapshot, policy)` laufen auf dem
   Zustand derselben Transaktion. Lehnt er ab, ändert sich nichts, und die Ablehnungen kommen strukturiert zurück
   (`TaskError.Rejections`, Klasse `rejected`). Nur ein angenommener Batch reserviert (Übergabe aus CC-14); Locks und
   Reservierungen bleiben außerhalb des Validators.
5. **Profile.** Die wirksamen Profile der erneuten Prüfung müssen den gespeicherten (`decisions.delegation_profile_json`)
   entsprechen (kanonischer Vergleich), sonst `conflict`: Policy oder Konfiguration haben sich seit der Planung geändert, neu
   planen.
6. **Je Delegation reservieren** (nächster Abschnitt), in der Reihenfolge des Batches.

Aktionen **ohne Delegation** (`wait`, `ask_question`, `plan_extend`, `request_integration`, `request_completion`) führt
`ReserveBatch` **nicht** aus. Sie sind im Batch gültig geprüft, bleiben aber für CC-51 (Dienst). Ein Batch ganz ohne Delegation
ist `invalid`.

### Eine Delegation reservieren

Der Validator prüft vieles schon; die Reservierung wiederholt es **bewusst als Schutz in der Transaktion** (gegen einen
gespeicherten Plan, der am Validator vorbei entstanden ist):

| Prüfung | Klasse |
| --- | --- |
| Revision der Auftragszeile (`RequireWorkOrderRevision`; nur bei `ReserveTask`) | `revision` |
| Auftrag gehört zum beauftragten Ziel (Umfang) | `scope` |
| Auftragszustand erlaubt den Start der Rolle (Tabelle unten) | `state` |
| alle Abhängigkeiten `completed` | `dependency` |
| keine aktive Lease und kein nicht beendeter Versuch am Auftrag | `conflict` |
| Kapazität: aktive Ausführungen ohne Supervisor plus neue ≤ `max_workers` (Reviewer zählen mit) | `capacity` |
| Agent verfügbar und passend zur Rolle | `unavailable` / `state` |

**Abhängigkeiten.** Nur `completed` gibt frei. `blocked` und `cancelled` eines Auftrags sowie `controlled_failure` oder `crashed`
eines Versuchs (der Auftrag bleibt dann `in_progress`) geben **nichts** frei.

**Anlegen** (alles auditiert, die Revision steigt in der Transaktion genau einmal):

1. Ausführung (`CreateExecution`: Rolle, Agent, Auftrag).
2. Profilversion (`RecordProfile`, falls nicht vorhanden; eine gleiche Version mit anderem Inhalt ist `conflict`).
3. Versuch 1 im Zustand `initializing` mit Profil-ID und -Version.
4. Lease (`CreateLease`) mit Herkunft (`LeaseOrigin`: Batch und Entscheidung), **sofort an den Versuch gebunden**.
5. Auftragsübergang laut Tabelle (unverändert, wenn schon im Zielzustand).
6. Agent `available → reserved`.

Ausführungs- und Lease-ID werden deterministisch aus Projekt, Batch und Entscheidung abgeleitet (`exec-…`, `lease-…`, SHA-256,
24 Hex-Zeichen): Dieselbe Entscheidung kann nie zwei Ausführungen oder Leases anlegen, auch wenn jede andere Prüfung versagte (die
Primärschlüssel kollidieren).

**Gültigkeit der Lease:** `ProjectConfig.Resources.AttemptRuntimeMinutes` plus `LeaseGrace` (Standard 5 Minuten: Zeit für Start,
Stopp und das letzte Ereignis, bevor M09 die Lease als abgelaufen behandelt). Beginn ist die Zeit der Uhr von `Tasks`.

### Agentenwahl

- Nennt `profile_id` einen Agenten (`<rolle>:<agent>`, aus dem Angebot der Rolle im Snapshot), wird **dieser** genommen. Ist er
  nicht verfügbar, scheitert die Reservierung (`unavailable`): Es gibt kein Ausweichen auf einen anderen.
- Sonst der **erste verfügbare Agent der Rolle in aufsteigender ID-Reihenfolge**. Ohne verfügbaren Agenten scheitert der ganze
  Batch ohne Änderung (`unavailable`).
- Ein Agent einer anderen Rolle ist `state`, ein unbekannter `not_found`.
- Wer die **Grundausstattung der Agenten** anlegt (Agenten in der Datenbank), gehört zu CC-51 (offen, siehe unten).

### `ReserveTask(ctx, ReserveTaskRequest) (domain.ExecutionLease, error)`

```go
type ReserveTaskRequest struct {
    ProjectID, WorkOrderID, AgentID string // AgentID leer: Agentenwahl wie oben
    ExpectedRevision int64                 // Zeilenrevision des Auftrags (ab 1)
    BatchID, DecisionID string
}
```

Für einen Batch mit **genau dieser einen** Delegation; dieselben Schritte wie `ReserveBatch`, zusätzlich die Revisionsprüfung des
Auftrags mit `RequireWorkOrderRevision` in derselben Transaktion. Hat der Batch mehrere Delegationen, verweist der Fehler
(`invalid`) auf `ReserveBatch`.

**Abweichung vom Ticketvertrag.** Der Vertrag nennt `ReserveTask(ctx, taskID, agentID, expectedRevision)`. Die Übergabe aus CC-14
verlangt, dass nur ein angenommener Batch reserviert, mit Revisionsprüfung in derselben Transaktion; deshalb gehören Batch und
Entscheidung zur Anfrage. `taskID` ist die `work_order_id`. Ohne Batch gäbe es keine Herkunft für die Lease und keine
Idempotenz.

## Auftragsübergänge je Rolle

Der Auftragszustand ist eine eigene Ebene. Er folgt aus der Reservierung und aus dem Ende eines Versuchs, **nie aus dem Prozess**.
Die Tabelle steht in `transitions.go` (`orderStartTarget`, `orderFinishTarget`; Tests: `TestOrderTransitionsByRole`,
`TestRunnerNeverCompletesOrder`).

**Reservierung:**

| Rollen | Übergang |
| --- | --- |
| `implementer`, `concept`, `documentation` | `waiting`, `in_progress`, `validation`, `review`, `waiting_integration` → `in_progress` (unverändert, wenn schon `in_progress`) |
| `documentation` zusätzlich | `completed` → `completed` (unverändert; der Validator lässt Dokumentation nach dem Abschluss zu) |
| `reviewer`, `concept_reviewer` | `validation` → `review` (unverändert, wenn schon `review`) |
| `supervisor` | ohne Auftrag; reserviert nicht über `ReserveBatch` |

**Terminaler Versuch:**

| Rollen | Ergebnis | Auftrag |
| --- | --- | --- |
| `implementer`, `concept`, `documentation` | `succeeded` | `in_progress` → `validation` |
| `implementer`, `concept`, `documentation` | anderes Ergebnis (kontrolliertes Scheitern, Absturz, Abbruch) | bleibt |
| `reviewer`, `concept_reviewer` | beliebig | bleibt |

- Ein **Absturz ändert den Auftrag nicht**. Die Crash-Serie und ihre Folgen (blockieren nach der Grenze) gehören zu M09.
- **Nie** wird ein Auftrag durch ein Runner-Ergebnis `completed`. Abgeschlossen wird er erst nach der Integration (`waiting_integration`
  → `completed`), außerhalb dieser Schicht.
- Das Urteil eines Reviews setzt der Supervisor auf Grundlage des Berichts, nicht das Ende des Prozesses.

**Begründung gegen die Startzustände des Validators.** Der Validator lässt Arbeit aus `waiting`, `in_progress`, `validation`,
`review` und `waiting_integration` beauftragen (`startableStates`; Dokumentation zusätzlich aus `completed`). „Implementierung
beendet“ heißt dort `validation`, `review`, `waiting_integration` oder `completed` ohne aktive Ausführung. Ein erfolgreicher Versuch
führt deshalb nach `validation`, dem Zustand, aus dem der Validator den Review zulässt (`reviewer` verlangt `validation` oder
`review`). Erneute Arbeit (Reparatur nach Befunden, Konzept- oder Dokumentationsarbeit) setzt den Auftrag auf `in_progress`, weil
„Implementierung beendet“ dann erst wieder mit dem nächsten erfolgreichen Versuch gilt.

**Enger als der Validator:** Der Start eines Prüfers ist auf `validation`/`review` begrenzt. Der Validator lässt für
`concept_reviewer` jeden startbaren Zustand zu; aus einem anderen Zustand wäre die Zustandswirkung nicht definiert, und der Start
scheitert lieber sichtbar (`state`), als den Auftrag still zu ändern.

## Ereignisse der Runner

Persistent mit denselben reinen Funktionen wie der `ExecutionTracker` ([`contracts.md`](contracts.md)): `domain.StepOnEvent` und
`domain.StepOnProcessExit`. Es gibt keine zweite Kopie der Regel.

### `ApplyEvent(ctx, domain.RunEvent) error`

In einer Transaktion: Ausführung und jüngsten Versuch lesen, Position bilden, Schritt berechnen, **Fencing** prüfen, anwenden.

| Ereignis | Wirkung |
| --- | --- |
| `session_started` | Session setzen (`SetSession`), Versuch `initializing → running`, Agent `reserved → active` |
| `question` | Frage anlegen (`CreateQuestion`), Versuch `running → awaiting_answer` |
| `result`, `error` mit `terminal=true` | Versuch beenden (`FinishAttempt`: genau ein terminales Ergebnis), Lease `active → released`, Agent `→ available`, Auftrag nach der Tabelle |
| `activity`, `tool_event`, `error` mit `terminal=false` | **nichts Gespeichertes** |

Die Zusammenfassung des Versuchs (aus `result` oder `error`) wird bereinigt (ungültiges UTF-8, Steuerzeichen) und auf 2 KiB gekürzt,
damit ein Ereignis nie an der Formprüfung des Datensatzes scheitert und den Versuch offen ließe.

### `ApplyProcessExit(ctx, projectID, executionID, attempt, exitCode) error`

Über `StepOnProcessExit`. **Ohne terminales Ergebnis ist das ein Absturz, auch bei Exit-Code 0**: Der Versuch endet als `crashed`,
die Lease wird freigegeben, der Agent ist wieder verfügbar, der Auftrag bleibt unverändert. Nach einem terminalen Ergebnis ändert
das Prozessende nichts und ist kein Fehler (der übliche Fall nach einem `result`). Dieselben Fencing-Regeln.

### Fencing

Das Ereignis muss zum jüngsten Versuch der Ausführung **und** zur jüngsten Lease-Generation des Auftrags gehören, und diese Lease
muss `active` sein (`AttemptLease`, `Leases`). Sonst ändert es **nichts**: keine Zeile, kein Audit-Ereignis, keine Revision. Der
Aufrufer protokolliert den Fehler; die Datenbank schreibt dafür keinen Eintrag.

| Lage | Ergebnis |
| --- | --- |
| Ereignis eines älteren Versuchs | `stale` (Schrittfunktion) |
| Ereignis eines nicht begonnenen Versuchs | `sequence` |
| Ereignis nach dem terminalen Ergebnis (auch ein doppeltes `result`) | `sequence` |
| Versuch nicht an die jüngste Generation gebunden | `stale` |
| Lease `released`, `expired` oder `revoked` | `stale` |
| Versuch einer Ausführung **ohne Auftrag** (Supervisor) | keine Lease, keine Absicherung |

**Ausnahme:** Ist die Lease durch `CancelTask` **widerrufen** und der Versuch in `stopping`, schließt genau sein **terminales**
Ergebnis (`result`, terminaler `error`, Prozessende) den **Versuch** ab. Auftrag und neue Versuche bleiben unberührt (der Auftrag
bleibt `cancelled`, auch bei `result succeeded`), die Lease bleibt `revoked`, der Agent wird `available`. Jedes andere Ereignis
einer widerrufenen Lease ist `stale`. Die Ausnahme gilt nur, solange die widerrufene Lease die **jüngste** Generation des Auftrags
ist. Gibt es eine jüngere, ist auch das terminale Ergebnis des alten Versuchs `stale` und ändert nichts. Der Fall entsteht im
normalen Ablauf nicht (ein abgebrochener Auftrag ist nicht startbar, der Validator lehnt ihn ab); die Prüfung ist eine bewusste
zusätzliche Absicherung, belegt durch `TestRevokedLeaseNotNewestIsStale`, das die jüngere Lease direkt über die Persistenz anlegt.

Eine **abgelaufene** Lease (`expired`) lässt auch ein spätes Ergebnis nicht mehr zu; wie der Zustand wiederhergestellt wird, regelt
M09.

## Abbrechen und Wiederaufnehmen

### `CancelTask(ctx, projectID, workOrderID, actor, reason) error`

In einer Transaktion, mit Akteur und Grund im Audit (`work_order.cancelled`, Detail `reason`; der Zustandswechsel trägt den Akteur):

- Auftrag `→ cancelled`; die aktive Lease `→ revoked`.
- Ein laufender oder wartender Versuch (`running`, `awaiting_answer`, `initializing` mit Prozess) `→ stopping`. Das Stoppsignal an
  den Runner sendet die Runtime (M04). Der Agent bleibt belegt, bis das terminale Ergebnis eintrifft (Ausnahme oben).
- Ein Versuch, der **noch nie einen Prozess** hatte (`initializing` ohne Prozessidentität), hat nichts, das zu stoppen oder zu
  melden wäre. Er wird direkt **kontrolliert** beendet, ohne Absturz zu melden, über die vorhandenen Zustände:
  `initializing → stopping` (`StepOnStopSent`), dann `stopping → cancelled` mit dem Ergebnis `cancelled` (derselbe Schritt wie ein
  `result` mit Ausgang `cancelled`, `StepOnEvent`). Die Zusammenfassung nennt den Ursprung („durch CancelTask beendet, bevor ein Prozess
  gestartet war“). Ein neuer Quellwert des terminalen Ergebnisses war nicht nötig (er hätte `ExecutionAttempt.Check` und die
  Schemabeschränkungen berührt). Der Agent wird sofort `available`.
- Eine Lease ohne gebundenen Versuch (reserviert, nie gestartet) gibt den Agenten sofort frei.
- Ein **zweiter Aufruf** für einen schon abgebrochenen Auftrag ist **idempotent**: keine Zeile, kein Ereignis, keine Revision.
- Ein abgeschlossener Auftrag lässt sich nicht abbrechen (`state`). Der Grund ist Pflicht (höchstens 512 Bytes, ohne Steuerzeichen).

Ein abgebrochener Auftrag gibt abhängige Aufträge **nicht** frei.

### `ResumeTask(ctx, projectID, workOrderID, actor, reason) error`

Auftrag `blocked → waiting`, auditiert mit Akteur und Grund (`work_order.resumed`). **Kein automatischer Neustart:** Die nächste
Reservierung braucht einen neuen Plan und den Validator. Jeder andere Zustand ist `state`. Das Zurücksetzen der Crash-Serie gehört
zu M09.

## Snapshot-Bauer

`BuildSnapshot(r SnapshotReader, projectID, goalID, offers)` füllt `supervisor.ProjectSnapshot` aus einer Leseschnittstelle, die
`*persistence.ReadTx` und `*persistence.Tx` erfüllen: Wer lesend plant, baut in `Store.View`, wer reserviert, in der
Schreibtransaktion selbst (dann sieht der Snapshot die eigenen, noch nicht festgeschriebenen Änderungen und dieselbe Revision).
`StoreSnapshotSource` erfüllt `supervisor.SnapshotSource` über `Store.View` für ein festes Projekt und Ziel; ein anderes Projekt ist
ein Fehler.

- **Aufträge:** alle Aufträge des Ziels und die Aufträge außerhalb des Ziels, von denen sie (auch über mehrere Stufen) abhängen.
  Aufträge anderer Ziele fehlen. Ein Verweis auf einen nicht vorhandenen Auftrag ist ein Fehler.
- **Kapazität:** `max_workers` aus der Projektkonfiguration; `active` sind alle aktiven Ausführungen des Projekts (auch anderer
  Ziele). `active_execution_id` eines Auftrags ist bei mehreren aktiven die kleinste ID: „aktiv“ bleibt gesetzt, der Validator
  startet nichts Zweites.
- **Offene Fragen:** die noch nicht zugestellten (offen oder beantwortet), soweit ihr Auftrag im Snapshot steht.
- **Rollen und Skills** kommen nicht aus SQLite, sondern als `SnapshotOffers` (`supervisor.BuildRoleOffers`, `BuildSkillOffers`).
- Das Ergebnis ist kanonisch (`Canonical`) und besteht `Check`.

**Revision 0.** Revision 0 ist ein gewöhnlicher Wert, kein Sonderfall. Ein Projekt, das in derselben Transaktion angelegt und weiter
geändert wird, bleibt auf Revision 0 (die anlegende Transaktion zählt als eine Änderung mit dem Ergebnis 0). `ReserveBatch` arbeitet
auf Revision 0 normal (Test `TestReserveOnRevisionZero`); die Reservierung setzt sie auf 1.

**Konzeptweitergabe (Präzisierung gegenüber der Beschreibung in `supervisor.md`).** Ein Umsetzungs- oder Fehlerauftrag, der von einem
Konzeptauftrag abhängt, führt in der Datenbank keinen eigenen Konzeptstand. Der Bau überträgt den **freigegebenen** Stand abgeleitet
und bei jedem Bau neu (die Datenbank bleibt unverändert), jeweils fail-safe:

- Ein eigener Konzeptstand des Auftrags hat Vorrang und wird nie überschrieben.
- Quelle nur Aufträge der Art `concept`, Ziel nur Aufträge der Art `implementation` oder `bug` (Konzeptarbeit führt ihren eigenen
  Stand, Organisationsaufträge haben keinen Konzeptbezug).
- Freigegeben heißt: eine Freigabe liegt vor und gilt für genau die Version des Konzeptstands. Wer wen freigeben durfte
  (Einstufung, Freigeber), prüft der Validator weiter selbst.
- Hängt der Auftrag von genau einem Konzeptauftrag ab, wird dessen freigegebener Stand übertragen. Bei mehreren nur, wenn **alle**
  freigegeben sind und **denselben** Stand tragen; sonst bleibt der Auftrag ohne Konzeptstand mit Konzeptbezug, und der Validator
  lehnt die Umsetzung ab. Raten, welcher Stand gilt, wäre eine stille Freigabe.

## Fehlerklassen

Jeder Fehler von `Tasks` ist ein `*TaskError` (`Class`, `Op`, `Message`, `Rejections`, `Err`); `TaskClassOf(err)` liefert die Klasse,
`errors.Is(err, &TaskError{Class: TaskStale})` prüft sie. Ein Abbruch des Kontexts bleibt unverändert. Die Klassen sind
Schnittstelle und ändern sich nicht ohne Versionswechsel.

| Klasse | Bedeutung | Herkunft |
| --- | --- | --- |
| `stale` | Ereignis eines älteren Versuchs oder einer beendeten, widerrufenen, abgelaufenen Lease; keine Wirkung | `domain` (`CatStale`) und Fencing |
| `sequence` | Ereignis außerhalb der Folge, doppeltes terminales Ergebnis, unerlaubter Ausführungsübergang | `domain` (`CatSequence`) |
| `revision` | Plan oder Auftragszeile veraltet; neu planen | Revisionsprüfung |
| `conflict` | doppelte oder gleichzeitige Reservierung, Auftrag schon reserviert, Profil geändert | Prüfung, `persistence` (`conflict`) |
| `state` | Zustand von Auftrag oder Agent erlaubt die Operation nicht | Prüfung, `persistence` (`state`) |
| `dependency` | eine Abhängigkeit ist nicht `completed` | Schutz der Reservierung |
| `scope` | Auftrag außerhalb des beauftragten Ziels | Schutz der Reservierung |
| `capacity` | alle `max_workers`-Plätze belegt | Schutz der Reservierung |
| `unavailable` | kein verfügbarer (oder der genannte nicht verfügbare) Agent | Agentenwahl |
| `rejected` | Plan abgelehnt oder erneute Prüfung lehnt ab; `Rejections` gefüllt | Plan, Validator |
| `not_found` | Plan, Auftrag, Ausführung, Agent unbekannt | `persistence` |
| `invalid` | ungültige ID, Grund oder Anfrage | Eingabeprüfung, `persistence` |
| `id`, `message_type`, `value`, … | Vertragsverstoß der Schrittfunktion, Klasse unverändert | `domain` |
| `busy` | Datenbank gesperrt; Wiederholen zulässig | `persistence` |
| `internal` | unerwarteter Fehler | `persistence` |

## Grenzen

- **Keine Laufzeit.** Das Stoppsignal an den Runner, das Senden von Antworten (`StepOnAnswerSent` ist vorhanden, wird aber erst von
  der Runtime aufgerufen), Prozessbeobachtung (`RecordProcess`) und Zeitüberschreitungen sind M04.
- **Keine Wiederherstellung.** Eine abgelaufene Lease wird hier nicht erneuert oder bereinigt; ein verwaister `stopping`-Versuch
  bleibt, bis M09 ihn behandelt. Die Crash-Serie (zehn Abstürze in Folge → blockieren) ist M09.
- **Offene Fragen bleiben bei `CancelTask` unberührt.** Eine offene Frage eines abgebrochenen Auftrags wird nicht geschlossen.
  Abgebrochene Aufträge starten nie wieder (der Validator lehnt sie ab), die Frage blockiert nichts.
- **Keine Dienstebene.** Idempotenz von Kommandos, Steuer-Socket und die CLI sind CC-51.

## Offene Punkte

**CC-51 (Dienst, CLI):**

- `task start/cancel/resume` an `ReserveBatch`/`ReserveTask`, `CancelTask`, `ResumeTask` binden.
- Aktionen ohne Delegation ausführen (`wait`, `ask_question`, `plan_extend`, `request_integration`, `request_completion`).
- **Agenten-Grundausstattung:** Wer legt die Agenten in der Datenbank an (`CreateAgent`)? Ohne Agenten scheitert jede Reservierung mit
  `unavailable`. Zu klären ist auch, wie Agenten aus `agents.yaml` und Agenten in der Datenbank zusammenhängen.
- Den Fehler eines Ereignisses protokollieren (`stale`/`sequence` schreiben nichts in die Datenbank).
- **Umfang des Snapshots.** Der Snapshot-Bauer nimmt nur Aufträge des Ziels (`goal_id`) plus deren Abhängigkeiten, keine Aufträge
  untergeordneter Ziele (`parent_goal_id`). Für die Planung eines Meilensteins oder Features ist das bewusst zu klären.

**Nach dem Merge mit CC-45 (Nachrichten, Outbox):**

- Abbruch- und Wiederaufnahmegründe (`reason`) sowie Runner-Zusammenfassungen (`taskSummary`) durch die Maskierung
  `domain.RedactSecrets` aus CC-45 leiten.
- Die Audit-Arten von Nachrichten und Outbox in `AuditKinds()` aufnehmen.

**M04 (Laufzeit):** Stoppsignal nach `CancelTask` (Versuch in `stopping`); Aufruf von `ApplyEvent` und `ApplyProcessExit`; Sendung der
Antwort (`StepOnAnswerSent`); `RecordProcess`; Zeitüberschreitung eines Versuchs über die Lease-Gültigkeit.

**M09 (Wiederherstellung):** Crash-Serie und ihre Grenze; Zurücksetzen der Crash-Serie bei `ResumeTask`; Behandlung abgelaufener Leases
und verwaister `stopping`-Versuche; Rückführung von Aufträgen nach Absturz.

- **Zehn-Absturz-Grenze je Auftrag und Rolle.** Jede Reservierung legt eine neue Ausführung mit Versuch 1 an. Die Grenze muss deshalb
  je **Auftrag und Rolle** zählen (über Ausführungen hinweg), nicht je Ausführung. Sonst ließe sie sich über neue Ausführungs-IDs
  umgehen; das verbietet die Spezifikation.

## Prüfbefehle

```bash
go test -count=1 ./internal/orchestrator/... ./internal/persistence/... ./internal/domain/...
go test -race -count=20 -run 'TestConcurrentReservation|TestStaleLeaseEvent|TestDuplicateCompletion' ./internal/orchestrator/...
go test -race -count=2 ./internal/orchestrator/... ./internal/persistence/...
make check
```

Die Tests laufen gegen echte SQLite-Dateien in `t.TempDir()`, mit der Policy aus der Beispielkonfiguration und dem echten
Skill-Katalog.
