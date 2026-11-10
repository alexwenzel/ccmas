# CCMAS – Supervisor: Snapshot und Validator

Stand: Ticket CC-14, Teil W14.1 (Verträge, `ProjectSnapshot`, Validator, Plattformprüfungen). **Führend ist die
Spezifikation** „Multi Agent Setup“, Abschnitte „Intelligente Arbeitsverteilung als Herzstück“ (Aufgabe des Supervisors,
Skill und tatsächliche Intelligenz, Aufwand/Modell/Effort, Planungszyklus), „Supervisor-Entscheidungen“, „Projekt, Profile und
Arbeitsauftrag“, „Zustandsmodell“, „Tickets und Mehrfachauswahl“, „PR und Auto-Merge“, „Milestones starten“, „Fragen und
Entscheidungen“, „Beziehungen und Abschluss“, „Unabhängiges Review vor dem Push“ und „Parameter und Profile“. Diese Datei hält
fest, was das Repository umsetzt, und ist keine Kopie der Spezifikation.

**Nicht Teil dieses Stands** (W14.2/W14.3): der Planer, der den Snapshot befüllt und das Modell aufruft, die Supervisor-Skills
(`ccmas-supervisor`, `ccmas-work-assessment`, Anpassung von `neue-aufgabe-nehmen` und `morgen-briefing`), die Erweiterung des
Runners um strukturierte Planungsausgabe und Live-Läufe. Es gibt noch keinen Aufrufer des Validators im Dienst: Der
Orchestrator (M03) existiert nicht. Alles unten beschreibt Bausteine, die getestet sind, nicht einen laufenden Planungszyklus.

## Grundsatz: Go entscheidet Zulässigkeit, nicht Planung

Das Gehirn von CCMAS ist ein planender Claude-Supervisor. Go enthält die technischen Regeln für Zulässigkeit und Ausführung
und **ersetzt die fachliche Planung nicht durch eine fest programmierte Auswahl**. Der Validator

- wählt keine Aufgabe aus, priorisiert nichts und ergänzt nichts;
- lehnt ab, was die Regeln nicht erlauben, und begründet jede Ablehnung so, dass sie dem Modell zur Neuplanung zurückgegeben
  werden kann;
- lässt zu, was zulässig ist: unabhängige Aufträge laufen parallel, zulässige unabhängige Arbeit darf vorgezogen werden, ein
  abhängiger Auftrag wartet (`wait`) bis zur erfüllten Voraussetzung.

Der Validator verändert weder Batch noch Snapshot noch Policy, startet nichts und schreibt nichts.

## Bausteine (`internal/supervisor`)

| Datei | Inhalt |
| --- | --- |
| `snapshot.go` | `ProjectSnapshot` und seine Teile, strikte Dekodierung (`DecodeSnapshot`), deterministische Serialisierung, Invarianten (`Problems`, `Check`) |
| `offers.go` | `BuildRoleOffers` und `BuildSkillOffers`: die erlaubten Rollenprofile und Skills für den Snapshot, aus Konfiguration und Katalog |
| `policy.go` | `Policy` (`domain.Catalog`, `*skills.Rules`, `*profiles.Resolver`, `profiles.Capabilities`) und `NewPolicy` |
| `validator.go` | `Validator` mit `Validate`, `Accept`, `AcceptJSON`; `ValidatedBatch`, `Delegation` |
| `rejection.go` | `Code`, `Rejection`, `RejectionError`, `AsRejection` |

## ProjectSnapshot

Der Snapshot ist der Zustand eines Projekts zu einer Zustandsrevision: genau das, was der Supervisor zum Planen sieht und der
Validator zum Prüfen braucht. Er ist ein Go-Typ mit JSON-Namen in `snake_case`, **kein** sprachübergreifender Vertrag unter
`contracts/`. Er wird strikt dekodiert (genau ein Objekt, unbekannte Felder und Folgedaten sind Fehler) und
**deterministisch** serialisiert: `json.Marshal` sortiert Aufträge, Abhängigkeiten, Ausführungen, Fragen, Rollen und Skills
auf einer Kopie und gibt nie `null` aus, sodass gleiche Inhalte byte-gleich sind.

| Feld | Inhalt |
| --- | --- |
| `schema_version`, `project_id`, `state_revision` | Version (1), Projekt, Zustandsrevision |
| `goal` | Art (`ticket`, `feature`, `milestone`), ID, Titel, Akzeptanzkriterien, Umfang (`scope`: beauftragte Tickets/Features), `out_of_scope` (bekannt, aber nicht beauftragt), Abschlussnachweise (`evidence`) |
| `integration_mode` | beauftragter Modus des Ziels (`pr` oder `auto`) |
| `capacity` | `max_workers` und die aktiven Ausführungen (`execution_id`, `role`, `work_order_id`, `state`); der Supervisor selbst wird nicht mitgezählt |
| `orders` | je Auftrag: `id`, `kind` (`concept`, `implementation`, `bug`, `organisation`), `state` (Zustandsmodell), `mode`, `depends_on`, `concept` (Version, Einstufung `class`, zutreffende `criteria`, Freigabe `approval` mit Version und Freigeber), `head_commit` und `branch`, `last_review` (Bericht mit Urteil, Commit, Konzeptversion und Ausführung des Reviewers, dazu Rolle der Reviewer-Ausführung und Ausführung der Implementierung), `last_result` (Rolle und Ergebnis der Rollenregeln), `pr` (Nummer, Stand, CI), Reparaturrunden je Schleife, `active_execution_id` |
| `open_questions` | noch nicht zugestellte Fragen (`question_id`, Auftrag, Zustand) |
| `roles` | erlaubte Rollenprofile (`RoleOffer`): Rolle, optional der individuelle Agent, Standardmodell und -effort, Policy, wählbare Modelle und Stufen, erlaubte und Standard-Skills |
| `skills` | freigegebene Skills mit Kategorie, Beschreibung, Rollen |

`Problems()` nennt alle Verstöße gegen die Invarianten (gültige IDs, eindeutige Aufträge, bekannte Zustände und Arten,
bekannte Abhängigkeiten, `max_workers` 1–16, aktive Ausführungen mit laufendem Zustand, aktive Ausführung eines Auftrags in der Liste der
aktiven Ausführungen, vollständige Commit-Hashes, PR- und CI-Stand, konsistente Berichte). Ein ungültiger Snapshot ist keine
Grundlage: der Validator lehnt jeden Batch mit `invalid_snapshot` ab. Eine fehlende Reviewer-Rolle gilt als „unbekannt“ und ist
gültig; sie führt später zu `integration_not_approved`.

**Vom Orchestrator (M03) zu liefern.** Der Snapshot ist eine Schnittstelle: Befüllt wird er aus dem führenden Zustand. Was erst
M03 kennt, ist hier als Feld vorbereitet, aber noch nicht produktiv befüllt: Ausführungsdatensätze (aktive Ausführungen, Rolle der
Reviewer-Ausführung, Ausführung der Implementierung), Kopf-Commit und Branch, PR- und CI-Stand, Abschlussnachweise
(`goal.evidence`), das zuletzt gemeldete Rollenergebnis (`last_result`), Reparaturrunden und die offenen Fragen. Der Validator
verlässt sich nur auf den Snapshot, nie auf Angaben des Modells. Die Echtheit der Kennungen und Nachweise im Snapshot
verantwortet der Orchestrator.

## Policy und Profile

`NewPolicy(resolver, caps, katalog, regeln)` baut die Policy aus denselben Quellen wie alle anderen Pfade. Der `domain.Catalog`
entsteht über `profiles.BuildCatalog` aus `models.yaml` (Modelle, Stufen, **genehmigte Effort-Fallbacks**, Policies) und dem
Skill-Katalog (freigegebene Skills **mit den Rollen, die sie erhalten dürfen**); es gibt keine zweite Modell- oder Skillliste.
Die Regeln müssen mit `skills.LoadChecked` geladen sein. Eine unvollständige Policy lehnt alles ab (`policy_incomplete`, fail-safe).

Für jede beauftragende Aktion löst der Validator das **wirksame Profil** auf:
`ResolveProfile(rolle, agent, Overrides{Supervisor: Auswahl}, caps)`. Rangfolge je Feld: ausdrücklicher Auftragswert, individueller
Agentenwert, Wahl des Supervisors, Rollenstandard ([`profiles.md`](profiles.md)). Der Agent ist der, den das Rollenangebot im
Snapshot (`roles[].agent_id`) nennt; sein Wert hat Vorrang vor der Wahl des Supervisors. Das Ergebnis liegt im
`ValidatedBatch` je `Delegation` und trägt `value_sources` sowie eine genehmigte Reduktion (`requested_effort`, `fallback`)
sichtbar. `ccmas profile resolve --supervisor-model/--supervisor-effort/--supervisor-skill` zeigt dasselbe Ergebnis auf der CLI.

`Decision.Check` prüft die Auswahl mit demselben Katalog: Effort vom Modell unterstützt **oder** ausdrücklich genehmigt
reduziert (`CheckEffective` liefert die wirksame Auswahl samt Reduktion), Skills freigegeben **und für die Rolle der Auswahl
erlaubt**.

## Validator

```go
v := supervisor.Validator{}
err := v.Validate(batch, snapshot, policy)          // Ticketvertrag: nil oder *RejectionError
vb, err := v.Accept(batch, snapshot, policy)        // zusätzlich der validierte Batch mit Profil je Delegation
vb, err := v.AcceptJSON(raw, snapshot, policy)      // liest den Batch zuerst streng gegen plan-v1/decision-v1
```

**Atomar.** Wird auch nur eine Entscheidung abgelehnt, ist das Ergebnis leer; der Fehler (`*RejectionError`) listet **alle**
Ablehnungen mit Code, Entscheidungs-ID (leer: der Batch als Ganzes) und Begründung. Reihenfolge: Ablehnungen des Batches zuerst,
dann die Entscheidungen in Batch-Reihenfolge, je Entscheidung nach Code; gleiche Eingabe ergibt dieselbe Liste. `Error()` formuliert
die Liste als Rückmeldung an das Modell (auf dem aktuellen Zustand neu planen, jeden Punkt beheben); die Struktur ist als JSON
verwendbar (`batch_id`, `rejections[]` mit `code`, `decision_id`, `reason`). `AsRejection` und `errors.As` liefern sie.

**Reihenfolge der Prüfung.** (1) Policy vollständig? (2) Snapshot gültig? Ohne beides wird alles abgelehnt. (3) Schema: der Batch
wird zu JSON serialisiert (fehlende Listen als `[]`) und gegen `plan-v1` und `decision-v1` geprüft; bei einem Schemafehler endet
die Prüfung, denn auf einem formal ungültigen Batch lassen sich die Regeln nicht sinnvoll anwenden. (4) Alle inhaltlichen Regeln
laufen unabhängig voneinander und werden gesammelt.

### Ablehnungsgründe

| Code | Wann | Auswerter / Quelle |
| --- | --- | --- |
| `schema` | Batch oder Entscheidung verletzt das Schema (zum Beispiel `implementation` ohne `selection`, `plan_extension` bei anderer Aktion, doppelte Skills) | `domain.Validate` (`decision-batch`) |
| `policy_incomplete` | Regeln, Resolver oder Katalog fehlen | `Policy.Check` |
| `invalid_snapshot` | Snapshot verletzt seine Invarianten | `ProjectSnapshot.Problems` |
| `foreign_project` | `project_id` von Batch oder Entscheidung ≠ Snapshot | Vergleich |
| `stale_revision` | `state_revision` von Batch oder Entscheidung ≠ Snapshot (älter oder neuer) | Vergleich |
| `unknown_reference` | Auftrag, Ziel oder Referenz ist im Snapshot nicht bekannt; ein in diesem Batch neu angelegter Auftrag ist erst im nächsten Zyklus ansprechbar | Snapshot |
| `invalid_target` | Zielart passt nicht zur Aktion (zum Beispiel `implementation` auf einem Ticket, `request_completion` nicht auf dem Ziel), Auftragsart passt nicht (Konzeptarbeit nur für `concept`, Umsetzung/Review/Integration nur für `implementation`/`bug`), Prüfziel nennt einen anderen Auftrag | Snapshot |
| `duplicate_decision_id` | eine Entscheidungs-ID kommt im Batch mehrfach vor | Vergleich |
| `duplicate_reference` | eine neue Ref ist schon vergeben (Auftrag, Ziel, Umfang oder zweimal im Batch) | Snapshot |
| `dependency_cycle` | Abhängigkeitsgraph aus vorhandenen Abhängigkeiten plus `plan_extension` hat einen Zyklus (auch Selbstabhängigkeit); gemeldet bei den Entscheidungen, die Beteiligte anlegen oder beauftragen | Graph (starke Zusammenhangskomponenten) |
| `precondition_unmet` | Auftrag blockiert, abgebrochen oder abgeschlossen (und andere nicht startbare Zustände); offene Abhängigkeit; Konzept fehlt, nicht freigegeben oder Freigabe für andere Version; Konzeptprüfer gibt Strategisches frei (dann entscheidet der Nutzer, nur `ask_question`); offene Frage; Review ohne abgeschlossene Implementierung, mit anderem Commit oder anderer Konzeptversion; nicht erfüllte deklarierte Voraussetzung | `ConceptApprover`, `ConceptApprovalValid`, Snapshot |
| `capacity_exceeded` | neue Ausführungen (`concept_work`, `implementation`, `review`, `documentation_work`) plus aktive (ohne Supervisor) > `max_workers`; Reviewer zählen mit | Zählung |
| `duplicate_start` | zwei Entscheidungen für denselben Auftrag, oder ein Auftrag mit aktiver Ausführung | Snapshot |
| `scope_extension` | Ziel oder Abhängigkeit außerhalb des beauftragten Umfangs (`out_of_scope`), neue Ref, die ein nicht beauftragtes Ticket übernimmt | Snapshot |
| `mode_extension` | Modus eines neuen Auftrags (`plan_extension[].mode`) oder eines Auftrags bei `request_integration` weitet den beauftragten Modus auf | `IntegrationModeAllowed` |
| `integration_not_approved` | `request_integration` ohne gültiges, unabhängiges Review | `PushAllowed` (Rolle und Ausführungs-IDs aus dem Snapshot) |
| `premature_completion` | `request_completion` ohne belegten Abschluss; offener PR im PR-Modus ist Wartezustand `waiting_integration`; offene Aufträge | `FeatureComplete` / `MilestoneComplete` |
| `repair_budget_exhausted` | Reparaturbudget der Code- oder Konzeptschleife erschöpft; zulässig bleibt `ask_question` | `RepairStep` / `ConceptRepairStep` |
| `profile_not_allowed` | Rolle passt nicht zur Aktion, Modell unbekannt, Effort nicht unterstützt und nicht genehmigt reduzierbar, Modell oder Effort für die Rolle nicht erlaubt, Skill unbekannt, nicht freigegeben oder für die Rolle nicht erlaubt, Agent nicht angelegt | `Decision.Check`, `Resolver.ResolveProfile` |
| `step_not_covered` | Folgeschritt (Aktion und Rolle) steht nicht in den Rollenregeln, oder die dort geforderten Voraussetzungen fehlen unter `preconditions` | `NextSteps` |

### Auslegungen, die der Auftrag offenließ

Diese Festlegungen sind Entscheidungen dieses Stands und stehen hier, damit sie prüfbar sind:

- **Voraussetzungen.** Bei Aktionen, die jetzt starten (Beauftragung, `request_integration`, `request_completion`), müssen die
  genannten `preconditions` im Snapshot gelten (`work_order_completed`: bei einem Fremdauftrag abgeschlossen, beim eigenen Auftrag
  die Implementierung beendet ohne aktive Ausführung; `concept_version_approved`: `ref` ist die Konzeptversion; `review_approved`;
  `ci_passed`; `capacity_available`). Bei `wait`, `ask_question` und `plan_extend` sind sie die Bedingung des Wartens
  beziehungsweise der Ergänzung und werden nur auf gültige Bezüge geprüft. So darf ein abhängiger Auftrag mit `wait` auf seine
  Voraussetzung warten, während seine Implementierung vorher abgelehnt wird.
- **Folgeschritt.** Gedeckt ist ein Schritt, der unter den Ergebnissen der Rolle `supervisor` steht oder dem zuletzt gemeldeten
  Ergebnis des Auftrags (`last_result`) folgt; die in `requires` genannten Voraussetzungen müssen in der Entscheidung stehen.
  Die Prüfung einer Konzeptversion durch den Konzeptprüfer (`review` mit Rolle `concept_reviewer`) ist deshalb nur nach einem
  Konzeptergebnis zulässig.
- **Konzeptfreigabe für jede Implementierung.** Ein Auftrag ohne Konzeptstand wird nicht implementiert. Ausnahmen für kleine
  Fehleraufträge sind eine Produktentscheidung und nicht von Go zu treffen.
- **Kapazität.** Jede beauftragende Entscheidung des Batches zählt, auch wenn sie aus anderem Grund abgelehnt wird; die
  Entscheidungen, die nicht mehr hineinpassen, werden einzeln abgelehnt.
- **Doppelstart.** Zwei Entscheidungen mit Auftragsziel für denselben Auftrag sind unabhängig von der Aktion ein Doppelstart; die
  zweite und jede weitere wird abgelehnt.
- **Plan-Ergänzung.** Neue Aufträge tragen `kind` (`partial`, `correction`, `repair`): neue Milestones oder Features sind so nicht
  ausdrückbar. Abhängigkeiten dürfen vorhandene Aufträge, Elemente des Umfangs und andere neue Refs nennen.
- **Moduserweiterung.** Die Entscheidung trägt keinen Modus. Aufgeweitet werden kann er nur durch einen neuen Auftrag
  (`plan_extension[].mode`) oder durch einen Auftrag, dessen Modus im Snapshot weiter ist als der des Ziels.
- **Reparaturrunde.** Eine Runde zählt nur, wenn das letzte Review `BEFUNDE` hat: von der Rolle `concept_reviewer` für die
  Konzeptschleife, sonst für die Code-Schleife; die nächste Runde ist die Zahl der bisherigen plus eins.

## Was Go entscheidet und was nicht

**Go entscheidet** Zulässigkeit: Bezug (Projekt, Revision, IDs, Umfang), Voraussetzungen, Abhängigkeitszyklen, Kapazität, Doppelstart,
Modus, Review vor dem Push, Abschlussnachweise, Reparaturbudget, Profile (Modell, Effort, Fallback, Rollenerlaubnis, Skills) und
Deckung durch die Rollenregeln.

**Go entscheidet nicht** Planung: welches Ziel als Nächstes, welche Aufgaben in welcher Reihenfolge, wie zerlegt wird, welches
Profil fachlich passt, wie hoch der Aufwand ist (`estimate` und `expected_outcome` sind Angaben des Modells ohne Prüfung gegen eine
Zahl) und ob ein Review fachlich genügt. Das Modell plant im Rahmen der Zulässigkeit; ist der Supervisor nicht verfügbar, wartet die
Disposition sichtbar und kontrolliert (Spezifikation) — dafür ist der Orchestrator zuständig, nicht der Validator.

## Schnittstelle zum Orchestrator und Grenzen

- **Aufrufer.** Der Validator ist der produktive Aufrufer von `PushAllowed`, `ConceptApprover`, `ConceptApprovalValid`, `RepairStep`,
  `ConceptRepairStep`, `IntegrationModeAllowed`, `FeatureComplete`/`MilestoneComplete` und `NextSteps` für Planungsergebnisse. Der
  Orchestrator (M03) soll **vor jeder Ausführung** `Validate`/`Accept` aufrufen; dieser Aufruf existiert bis M03 nicht. Die
  Auswerter bleiben ebenso an den Stellen aufzurufen, an denen Push, Integration und Abschluss tatsächlich geschehen.
- **Locks und Belege.** Reservierungen, Worktree-Locks und Qualitätsbelege über das Dargestellte hinaus (Spezifikation „Supervisor-Entscheidungen“)
  gehören zum Orchestrator.
- **Zeitpunkt.** Der Validator prüft gegen einen Snapshot. Zwischen Prüfung und Ausführung kann sich der Zustand ändern; die
  Revision im Batch und im Snapshot macht das sichtbar (`stale_revision`), die atomare Übernahme ist Sache des Orchestrators.

## Prüfbefehle

```bash
go test ./internal/supervisor/... ./internal/domain/... ./internal/skills/... ./internal/profiles/... -count=1 -race
make check
```
