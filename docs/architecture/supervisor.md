# CCMAS – Supervisor: Snapshot, Validator und Planer

Stand: Ticket CC-14, Teil W14.1 (Verträge, `ProjectSnapshot`, Validator, Plattformprüfungen) und Teil W14.2 (Supervisor-Skills, Planer, Systemprompt und strukturierte Ausgabe im Runner). **Führend ist die
Spezifikation** „Multi Agent Setup“, Abschnitte „Intelligente Arbeitsverteilung als Herzstück“ (Aufgabe des Supervisors,
Skill und tatsächliche Intelligenz, Aufwand/Modell/Effort, Planungszyklus), „Supervisor-Entscheidungen“, „Projekt, Profile und
Arbeitsauftrag“, „Zustandsmodell“, „Tickets und Mehrfachauswahl“, „PR und Auto-Merge“, „Milestones starten“, „Fragen und
Entscheidungen“, „Beziehungen und Abschluss“, „Unabhängiges Review vor dem Push“ und „Parameter und Profile“. Diese Datei hält
fest, was das Repository umsetzt, und ist keine Kopie der Spezifikation.

**Nicht Teil dieses Stands** (W14.3): die Live-Szenarien S1 bis S6 mit echten Ausgaben des Supervisors und ihre Belege. Der
Planer ruft das Modell auf, wird aber noch von keinem Dienst aufgerufen: Der Orchestrator (M03) existiert nicht, ebenso wenig
die Befüllung des `ProjectSnapshot` aus dem führenden Zustand. Alles unten beschreibt Bausteine, die getestet sind, nicht einen
laufenden Planungszyklus. Der Planer ist nur in einem kleinen Rauchlauf mit dem echten Runner belegt (Durchleitung, nicht
Planungsqualität; Abschnitt „Planer“, Belege).

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
| `planner.go`, `context.go`, `modelschema.go`, `attempt.go`, `snapshotdir.go` | `Planner` (Plan, PlanWithRecord), Planungszustand mit Kontextbudget, Modell-Ausgabeschema, Versuchsprotokoll und Wartezustand (seit W14.2) |
| `scenario/` | synthetische Snapshots S1–S5 und semantische Prädikate als Auswertungswerkzeug, kein Teil der Planung (seit W14.2) |
| `../../prompts/` | Systemprompt der Rolle supervisor (`supervisor.md`, Go-Paket `prompts`, eingebettet) |

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
bekannte Abhängigkeiten, höchstens ein Profilangebot (und damit höchstens ein Agent) je Rolle, `max_workers` 1–16, aktive Ausführungen mit laufendem Zustand, aktive Ausführung eines Auftrags in der Liste der
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
| `precondition_unmet` | Auftrag blockiert, abgebrochen oder abgeschlossen (und andere nicht startbare Zustände); offene Abhängigkeit; bei Konzeptbezug des Auftrags (Konzeptstand am Auftrag oder Abhängigkeit auf einen Konzeptauftrag) Konzeptstand fehlt, Konzept nicht freigegeben oder Freigabe für andere Version; Konzeptprüfer gibt Strategisches frei (dann entscheidet der Nutzer, nur `ask_question`); offene Frage; Review ohne abgeschlossene Implementierung, mit anderem Commit oder anderer Konzeptversion; nicht erfüllte deklarierte Voraussetzung | `ConceptApprover`, `ConceptApprovalValid`, Snapshot |
| `capacity_exceeded` | grundsätzlich startbare neue Ausführungen (`concept_work`, `implementation`, `review`, `documentation_work`; aus anderem Grund abgelehnte zählen nicht) plus aktive (ohne Supervisor) > `max_workers`; Reviewer zählen mit | Zählung |
| `duplicate_start` | zwei startende Entscheidungen (Beauftragung oder `request_integration`) für denselben Auftrag, oder ein Auftrag mit aktiver Ausführung; `wait` und `ask_question` starten nichts | Snapshot |
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
- **Konzeptfreigabe nur bei Konzeptbezug.** Die Spezifikation („Concept-Tickets in Asana“) verlangt die Freigabe für abhängige
  Implementation-Tickets; `Bug` und `Organisation` bleiben nutzbar. Der Validator verlangt sie deshalb nur, wo der Snapshot einen
  Konzeptbezug führt: ein Konzeptstand am Auftrag oder eine Abhängigkeit auf einen Konzeptauftrag. Dann gelten `ConceptApprover`
  und `ConceptApprovalValid` wie bisher (strategisch oder nicht einstufbar: nur die Frage an den Nutzer). Ein Konzeptbezug über
  eine Abhängigkeit ohne Konzeptstand am Auftrag wird abgelehnt (fail-safe); ohne Konzeptbezug gibt es nichts freizugeben, eine
  deklarierte Voraussetzung `concept_version_approved` ohne Version ist dort leer und kein Verstoß, mit Version falsch. Die
  Rollenregeln führen dafür zwei Schritte (`implementation` mit und ohne `concept_version_approved`); bei Konzeptbezug ergänzt
  die Deckung die Voraussetzung auch für den Schritt ohne sie. Ohne Konzeptstand gibt es keine Konzeptversion, an die ein Review
  zu binden wäre: die Integration bindet dann nur an Auftrag, Commit, Urteil und unabhängige Ausführung.
- **Kapazität.** Es zählen nur grundsätzlich startbare Delegationen des Batches; eine aus anderem Grund abgelehnte (offene
  Abhängigkeit, Profil, Doppelstart, …) belegt keinen Platz und löst kein `capacity_exceeded` aus. Die Entscheidungen, die nicht
  mehr hineinpassen, werden einzeln abgelehnt.
- **Doppelstart.** Nur startende Entscheidungen zählen: die vier Beauftragungen und `request_integration`. Zwei davon für denselben
  Auftrag sind ein Doppelstart; die zweite und jede weitere wird abgelehnt. `wait` und `ask_question` zum selben Auftrag stehen
  nebeneinander zulässig.
- **Plan-Ergänzung.** Neue Aufträge tragen `kind` (`partial`, `correction`, `repair`): neue Milestones oder Features sind so nicht
  ausdrückbar. Abhängigkeiten dürfen vorhandene Aufträge, Elemente des Umfangs und andere neue Refs nennen.
- **Moduserweiterung.** Die Entscheidung trägt keinen Modus. Aufgeweitet werden kann er nur durch einen neuen Auftrag
  (`plan_extension[].mode`) oder durch einen Auftrag, dessen Modus im Snapshot weiter ist als der des Ziels. Ohne
  `plan_extension[].mode` erbt der neue Auftrag den beauftragten Modus des Ziels; der wirksame Modus je neuer Ref steht im
  validierten Batch (`ValidatedDecision.NewOrderModes`).
- **Profilangebot je Rolle.** Der Snapshot führt je Rolle höchstens ein Angebot (`roles[]`), also höchstens einen Agenten
  (`ProjectSnapshot.Problems`). Der Validator wählt nicht unter mehreren Agenten aus; dessen Werte haben Vorrang vor der Wahl des Supervisors.
- **Reparaturrunde.** Eine Runde zählt nur, wenn das letzte Review `BEFUNDE` hat: von der Rolle `concept_reviewer` für die
  Konzeptschleife, sonst für die Code-Schleife; die nächste Runde ist die Zahl der bisherigen plus eins.

## Was Go entscheidet und was nicht

**Go entscheidet** Zulässigkeit: Bezug (Projekt, Revision, IDs, Umfang), Voraussetzungen, Abhängigkeitszyklen, Kapazität, Doppelstart,
Modus, Review vor dem Push, Abschlussnachweise, Reparaturbudget, Profile (Modell, Effort, Fallback, Rollenerlaubnis, Skills) und
Deckung durch die Rollenregeln.

**Go entscheidet nicht** Planung: welches Ziel als Nächstes, welche Aufgaben in welcher Reihenfolge, wie zerlegt wird, welches
Profil fachlich passt, wie hoch der Aufwand ist (`estimate` und `expected_outcome` sind Angaben des Modells ohne Prüfung gegen eine
Zahl) und ob ein Review fachlich genügt. Das Modell plant im Rahmen der Zulässigkeit; ist der Supervisor nicht verfügbar, wartet die
Disposition sichtbar und kontrolliert (Spezifikation): Der Planer meldet das als `*SuspendedError` („Planung ausgesetzt“, Abschnitt „Planer“); die
Disposition darauf ist Sache des Orchestrators, nicht des Validators.

## Schnittstelle zum Orchestrator und Grenzen

- **Aufrufer.** Der Validator ist der produktive Aufrufer von `PushAllowed`, `ConceptApprover`, `ConceptApprovalValid`, `RepairStep`,
  `ConceptRepairStep`, `IntegrationModeAllowed`, `FeatureComplete`/`MilestoneComplete` und `NextSteps` für Planungsergebnisse. Der
  Orchestrator (M03) soll **vor jeder Ausführung** `Validate`/`Accept` aufrufen; dieser Aufruf existiert bis M03 nicht. Die
  Auswerter bleiben ebenso an den Stellen aufzurufen, an denen Push, Integration und Abschluss tatsächlich geschehen.
- **Locks und Belege.** Reservierungen, Worktree-Locks und Qualitätsbelege über das Dargestellte hinaus (Spezifikation „Supervisor-Entscheidungen“)
  gehören zum Orchestrator.
- **Zeitpunkt.** Der Validator prüft gegen einen Snapshot. Zwischen Prüfung und Ausführung kann sich der Zustand ändern; die
  Revision im Batch und im Snapshot macht das sichtbar (`stale_revision`), die atomare Übernahme ist Sache des Orchestrators.

## Planer (W14.2)

`Planner.Plan(ctx, snapshot) (domain.DecisionBatch, error)` ist der Ticketvertrag; `PlanWithRecord` liefert zusätzlich den validierten Batch
(Profil je Delegation) und das Protokoll aller Versuche. Der Planer **wählt nichts aus**: er stellt den Auftrag zusammen, lässt den Claude-Supervisor
planen und lässt den Validator das Ergebnis prüfen. Ohne Supervisor oder ohne zulässigen Plan gibt er **keinen Ersatzplan**, sondern setzt sichtbar aus.

**Abhängigkeiten als Schnittstellen** (`PlannerConfig`):

| Schnittstelle | Aufgabe | Produktiv / Test |
| --- | --- | --- |
| `Runtime` | `Execute(ctx, runner.Request) (runner.Execution, error)` | `*runner.Runner` / Fake |
| `SnapshotSource` | `CurrentSnapshot(ctx, projectID)`: aktueller Zustand zur Validierung und nach veralteter Revision | Orchestrator (M03) / Fake |
| `ProfileResolver` | `ResolveProfile` der Rolle `supervisor`; Standard `Policy.Resolver` (`*profiles.Resolver`) | `profiles.Resolver` |
| Skill-Katalog | `*skills.Catalog`, **muss** mit `skills.LoadChecked` geladen sein | `skills.LoadChecked` |

**Ablauf je Versuch.**

1. **Profil.** Das Profil der Rolle `supervisor` löst der Resolver auf (aus `agents.yaml`; Beispiel opus/high). Ein individueller Agent der Rolle
   (aus dem Rollenangebot im Snapshot) hat Vorrang vor dem Rollenstandard, ausdrückliche Auftragswerte (`PlannerConfig.Overrides.Order`, nur für Prüfläufe) vor beiden.
   Das Protokoll hält angefordert/wirksam und die Herkunft je Feld fest. Die Policy muss `readonly` sein; sonst läuft nichts.
2. **Skill-Snapshot.** `Catalog.Resolve(supervisor, profile.SkillIDs)` und `Snapshot.Write` in `PlannerConfig.SnapshotDir` (inhaltsadressiert, wiederverwendet); der
   Verweis geht als `RunSpec.skill_snapshot` an den Runner (`runner.Request.SkillSnapshot`), der ihn vor dem Start nachrechnet. Der Inhalt der **führenden Skills**
   (Standardausstattung der Rolle, `default_for`, hier `ccmas-supervisor` und `ccmas-work-assessment`; ohne `ccmas-supervisor` plant der Planer nicht) stammt aus
   demselben Snapshot, nicht aus einer zweiten Lesung des Katalogs, und steht, aufsteigend nach ID und ohne Frontmatter, im Prompt.
3. **Planungszustand.** `BuildPlanningContext` serialisiert den Snapshot kanonisch (kompakt, sortiert) und hält ihn auf höchstens 64 KiB (siehe unten).
4. **Prompt.** Kopf (Projekt, Revision, Ziel, Modus, freie Plätze), Entscheidungsabläufe, Zustand und bei einer Neuplanung die Rückmeldung; gleiche Eingaben ergeben
   dieselben Bytes. Der Systemprompt ist `prompts/supervisor.md` (siehe unten).
5. **Sitzung.** Policy `readonly`, neutrales leeres temporäres Verzeichnis als `cwd` (danach entfernt), höchstens 8 Runden (`DefaultMaxTurns`), Laufzeitgrenze
   10 Minuten (`DefaultMaxRuntime`), `output_schema` = `ModelOutputSchema()`.
6. **Auswertung.** Strukturierte Ausgabe → `DecisionBatch`. **`schema_version`, `batch_id`, `project_id` und `state_revision` setzt Go** (Batch und Entscheidungen) aus
   dem Planungs-Snapshot; was das Modell dort angab, wird überschrieben. Dann der Zustand der `SnapshotSource` und `Validator.Accept(batch, aktueller Zustand, Policy)`.
   Weil der Batch die Revision des Planungs-Snapshots trägt, zeigt jede Zustandsänderung seitdem `stale_revision`.

**Neuplanung und Wartezustand.** Eine Ablehnung des Validators, ein veralteter Stand (`stale_revision`) und eine Sitzung ohne gültige strukturierte Ausgabe lösen
höchstens **zwei Neuplanungen** aus (`DefaultMaxReplans`; insgesamt höchstens drei Versuche). Die Neuplanung läuft auf dem **frischen** Zustand der `SnapshotSource`;
ein veralteter Stand zählt als Neuplanung. Der nächste Prompt enthält die vollständige Rückmeldung (`RejectionError.Error()` mit Code, Entscheidung und Grund) und den
abgelehnten Vorschlag (gekürzt auf 16 KiB). Danach, und sofort bei fehlendem Supervisor, endet die Planung mit `*SuspendedError` (`errors.Is(err, ErrPlanningSuspended)`,
„Planung ausgesetzt“) mit **allen Versuchen**:

| Grund (`SuspendReason`) | Wann |
| --- | --- |
| `replan_budget_exhausted` | drei Versuche ohne zulässigen Plan (Ablehnung, veralteter Stand oder fehlende Ausgabe) |
| `supervisor_unavailable` | die Runtime startet nicht, der Runner meldet einen terminalen Fehler (zum Beispiel `not_logged_in`, `max_runtime_exceeded`, `skills_not_loaded`), Absturz, Sitzung ohne Ergebnis, Profil oder Skills nicht auflösbar, Policy nicht `readonly`, führender Skill fehlt; keine Wiederholung |
| `subscription_limit` | der Runner meldet das Abo-Limit (`kind: subscription_limit`); keine Wiederholung, kein Fallback |
| `state_unavailable` | `SnapshotSource` liefert nichts, einen ungültigen Zustand oder ein anderes Projekt; auch ein ungültiger Planungs-Snapshot |
| `context_too_large` | der Zustand passt auch nach den Reduktionsregeln nicht in das Budget (`errors.Is(err, ErrContextTooLarge)`); kein Modellaufruf |

Ein Abbruch durch den Aufrufer (Kontext) ist kein Wartezustand; `Plan` gibt dann den Kontextfehler zurück.

**Kontextbudget.** Der Zustand im Prompt ist auf `DefaultContextLimit` (64 KiB) begrenzt. Passt er nicht, greifen die Reduktionsregeln in dieser Reihenfolge, jede nur,
solange das Budget überschritten ist: (1) `skill_descriptions_dropped`, (2) `closed_orders_compacted` (abgeschlossene und abgebrochene Aufträge bleiben als Kurzform: ID, Art, Titel,
Zustand, Modus, Abhängigkeiten), (3) `finding_texts_shortened` (160 Zeichen), (4) `skill_list_dropped`, (5) `question_texts_shortened` (200 Zeichen). **Offene und blockierte Aufträge bleiben
vollständig, kein Auftrag wird entfernt** (Abhängigkeiten verweisen auf sie); reicht es nicht, ist das `ContextError` und der Wartezustand `context_too_large`. Der Prompt weist auf eine Verkürzung hin; das
Protokoll nennt die angewandten Regeln. Der Validator prüft immer gegen den vollständigen Zustand.

**Systemprompt.** `prompts/supervisor.md` (Rolle, Grenzen, Ausgabepflicht; kurz, höchstens 4000 Zeichen, die Fachlogik steht in den Skills) wird als `RunSpec.system_prompt` übergeben.
Damit ist der offene Punkt „Systemprompt“ aus CC-17 für den Supervisor entschieden: **eigener Prompt statt `--system-prompt ""`**. Für andere Rollen bleibt es bei der bisherigen Voreinstellung,
bis sie einen Prompt bekommen. Der Hash des Prompts steht in jedem Versuchsprotokoll; er ist die Version des Stands.

**Ausgabeschema.** `ModelOutputSchema()` ist ein flaches Draft-07-Schema (kein `$ref`, keine Bedingungen, kein `pattern`, keine Schlüsselwörter von 2020-12; rund 3,6 KiB) für das Planungsergebnis ohne die
Felder, die Go setzt. Die Aufzählungen entstehen aus denselben Go-Aufzählungen wie die Prüfung in `internal/domain` (`DecisionActions`, `Roles`, `Efforts`, `PreconditionKinds`,
`EstimateSizes`, `EstimateLevels`, `PlanKinds`, …); `TestModelSchemaEnumsMatchTheContracts` hält sie mit `decision-v1` und `plan-v1` gleich, `TestModelSchemaCoversTheContractFields` die Felder und
Pflichtfelder. **Maßgeblich bleibt die Prüfung in Go**: das Schema hilft dem Modell, die Form zu treffen; Bedingungen (`selection` bei Beauftragungen), ID-Muster und Längen prüft `plan-v1`/`decision-v1`
beim Validator, nicht das Modellschema.

**Versuchsprotokoll** (`Attempt`, JSON-fähig, nie der Text des Prompts): Nummer und Ausführungs-ID, Dauer; Hash des Planungs-Snapshots (kanonisch, vollständig) und Revision; Bytes des Kontexts und angewandte Reduktionen;
Hash und Länge von Prompt, Systemprompt und Ausgabeschema; Hash der Rückmeldung; Skill-Snapshot-ID und je Skill Version, `tree_sha256` und ob der Inhalt im Prompt steht; das wirksame Supervisor-Profil
(`AgentProfile` mit angefordert/wirksam/Fallback/Herkunft, Agent, wirksames Modell laut `session_started`); Session-ID, CLI- und SDK-Version (`session_started.claude_version`/`sdk_version`) und die laut
`session_started` geladenen Skills; die **rohe strukturierte Ausgabe**; Urteil (`accepted`, `rejected`, `no_structured_output`, `supervisor_failed`, `state_unavailable`), ob der Stand veraltet war, die aktuelle Revision, Ablehnungscodes und
-text, Code des terminalen Fehlers. Es ist die Grundlage der Belege in W14.3.

**Keine szenariospezifische Logik.** Der Planer enthält keine fest programmierte Auswahl von Aufgaben und keinen Ersatzplan; `TestPlannerAddsNothingOfItsOwn` hält fest, dass ein Plan mit einer Entscheidung eine Entscheidung bleibt.

## Skills (W14.2)

`ccmas-supervisor` (führend) beschreibt den Entscheidungsablauf: Zielverständnis, Konzeptreife, Risiko und Aufwand, Abhängigkeiten, nächste Arbeit (Fertiges vor Neuem, Reparatur bei Befunden, Integration nur nach Freigabe,
Parallelität im Kapazitätsrahmen, belegter Abschluss), Ergebnisbewertung, die Felder jeder Entscheidung, wann eine Frage an den Nutzer statt Arbeit folgt, und die Reaktion auf jeden Ablehnungscode. `ccmas-work-assessment`
trennt die Aufwandsschätzung (Größe, Komplexität, Unsicherheit) vom Modell-Effort und beschreibt die Profilwahl nach Qualität (Pflichtprüfungen entfallen nie). Beide sind Hilfsskills der Kategorie `planning` ohne
`contract.yaml` (Workflow-Verträge gibt es nur für die Kategorien `concept`, `review`, `delivery`, `completion`, `questions`), Rolle `supervisor`, Standardausstattung der Rolle, Herkunft `package: none`
([`skill-migration.md`](skill-migration.md)). `ccmas-neue-aufgabe-nehmen` und `ccmas-morgen-briefing` nutzt der Planer nicht; sie bleiben `pending_adaptation`, ihre Zuordnung zu einem Ticket ist offen.

## Szenario-Prädikate (W14.2)

`internal/supervisor/scenario` enthält die synthetischen Snapshots S1 bis S5 (erfundene Aufträge `sc1-a`, …; nie der Stand des echten Boards) und semantische Prädikate, die einen Batch ohne Rücksicht auf den Wortlaut bewerten
(`Evaluate` = gemeinsame Prädikate `CheckDelegations` plus `Check` des Szenarios). `TestScenarioPlanContract` prüft handgeschriebene gute und schlechte Batches, jeweils auch gegen den Validator. W14.3 wendet
dieselben Prädikate auf die echten Ausgaben an.

| Szenario | Zustand | Prädikat |
| --- | --- | --- |
| S1 | A und B unabhängig startbar, C hängt an A | Implementierung für A und B; keine für C |
| S2 | Konzept unreif, einmal Routine, einmal strategisch (Umsetzungen hängen an den Konzeptaufträgen) | keine Implementierung; Konzeptarbeit oder Frage; weder Integration noch Abschluss |
| S3 | `max_workers` 2, eine Ausführung läuft, eine Implementierung fertig ohne Review, ein weiterer startbarer Auftrag | Review (Rolle `reviewer`, an den Kopf-Commit gebunden) vor jeder neuen Implementierung; Beauftragungen ≤ freie Plätze |
| S4 | Review mit BEFUNDE, Reparaturrunde 1 von 3 | Implementierung (Rolle `implementer`) für denselben Auftrag; weder Integration noch Abschluss |
| S5 | PR-Modus, Review FREIGEGEBEN, PR offen, CI bestanden | `wait`; kein Zielabschluss, keine erneute Integration, keine Beauftragung für den Auftrag, kein neuer Auftrag im Modus `auto` |

Für jede Beauftragung (alle Szenarien) verlangt `CheckDelegations`: Auswahl mit Rolle, mindestens einem Skill, Modell und Effort aus dem Angebot der Rolle im Snapshot, ein erwartetes Ergebnis, eine vollständige
Aufwandsschätzung (`estimate`: Größe, Komplexität, Unsicherheit; getrennt vom Effort) und eine Begründung. Die Prädikate urteilen über den Inhalt; ob der Validator einen Batch annimmt, ist davon unabhängig
(zum Beispiel ist ein einzelnes `implementation` in S3 zulässig, aber inhaltlich schlecht).

## Belege (W14.2)

Ein einzelner Rauchlauf mit dem echten Runner (Durchleitung, nicht Planungsqualität): [`runtime-capabilities.md`](runtime-capabilities.md), Abschnitt 11, Beleg `evidence/live-supervisor-planner.jsonl`.
Test: `CCMAS_LIVE_TESTS=1 go test ./internal/supervisor -run TestLivePlanner -count=1 -v`. Er läuft mit haiku/low über ausdrückliche Auftragswerte (`Overrides.Order`), nicht über das Supervisorprofil.

## Prüfbefehle

```bash
go test ./internal/supervisor/... ./internal/domain/... ./internal/skills/... ./internal/profiles/... ./internal/workers/... ./prompts/... -count=1 -race
make check
```
