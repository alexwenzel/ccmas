# CCMAS – Supervisor: Snapshot, Validator und Planer

Stand: Ticket CC-14 (inklusive Befundrunde 2 des Reviews), Teil W14.1 (Verträge, `ProjectSnapshot`, Validator, Plattformprüfungen), Teil W14.2 (Supervisor-Skills, Planer, Systemprompt und strukturierte Ausgabe im Runner) und Teil W14.3 (Nachweis des Ergebnisziels von M02 mit echten Supervisor-Läufen, Abschnitt „Nachweis M02“) und Teil W14.4 (Szenario S7: zustandsabhängige Wahl von Modell und Effort). **Führend ist die
Spezifikation** „Multi Agent Setup“, Abschnitte „Intelligente Arbeitsverteilung als Herzstück“ (Aufgabe des Supervisors,
Skill und tatsächliche Intelligenz, Aufwand/Modell/Effort, Planungszyklus), „Supervisor-Entscheidungen“, „Projekt, Profile und
Arbeitsauftrag“, „Zustandsmodell“, „Tickets und Mehrfachauswahl“, „PR und Auto-Merge“, „Milestones starten“, „Fragen und
Entscheidungen“, „Beziehungen und Abschluss“, „Unabhängiges Review vor dem Push“ und „Parameter und Profile“. Diese Datei hält
fest, was das Repository umsetzt, und ist keine Kopie der Spezifikation.

**Grenzen dieses Stands:** Der Planer ruft das Modell auf, wird aber noch von keinem Dienst aufgerufen: Der Orchestrator (M03) existiert nicht, ebenso wenig
die Befüllung des `ProjectSnapshot` aus dem führenden Zustand. Alles unten beschreibt Bausteine, die getestet sind, nicht einen
laufenden Planungszyklus. Die Planung ist mit dem echten Supervisor an acht synthetischen Zuständen belegt (Abschnitt „Nachweis M02“), nicht an echten Projektzuständen und
ohne dass die geplante Arbeit ausgeführt wurde.

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
| `scenario/` | synthetische Snapshots S1–S7 (S2 in Routine und strategisch getrennt) und benannte semantische Prädikate als Auswertungswerkzeug, kein Teil der Planung (seit W14.2, S6 und Ergebnis je Prädikat seit W14.3, S7 seit W14.4); in den Tests des Pakets: Live-Szenarien (`live_test.go`), Belegformat und Offline-Prüfung der Belege (`evidence_test.go`), Test der Belegkette ohne Konto (`roundtrip_test.go`) |
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
| `orders` | je Auftrag: `id`, `kind` (`concept`, `implementation`, `bug`, `organisation`), `state` (Zustandsmodell), `mode`, `depends_on`, `concept` (Version, Einstufung `class`, zutreffende `criteria`, Freigabe `approval` mit Version und Freigeber), `review_concept_version` (abgeleitet, siehe unten), `head_commit` und `branch`, `last_review` (Bericht mit Urteil, Commit, Konzeptversion und Ausführung des Reviewers, dazu Rolle der Reviewer-Ausführung und Ausführung der Implementierung), `last_result` (Rolle und Ergebnis der Rollenregeln), `pr` (Nummer, Stand, CI, optional `head_commit`), Reparaturrunden je Schleife, `active_execution_id` |
| `open_questions` | noch nicht zugestellte Fragen (`question_id`, Auftrag, Zustand) |
| `roles` | erlaubte Rollenprofile (`RoleOffer`): Rolle, optional der individuelle Agent, Standardmodell und -effort, Policy, wählbare Modelle und Stufen, erlaubte und Standard-Skills |
| `skills` | freigegebene Skills mit Kategorie, Beschreibung, Rollen |

`Problems()` nennt alle Verstöße gegen die Invarianten (gültige IDs, eindeutige Aufträge, bekannte Zustände und Arten,
bekannte Abhängigkeiten, höchstens ein Profilangebot (und damit höchstens ein Agent) je Rolle, `max_workers` 1–16, aktive Ausführungen mit laufendem Zustand, aktive Ausführung eines Auftrags in der Liste der
aktiven Ausführungen, vollständige Commit-Hashes, PR- und CI-Stand, konsistente Berichte). Ein ungültiger Snapshot ist keine
Grundlage: der Validator lehnt jeden Batch mit `invalid_snapshot` ab. Eine fehlende Reviewer-Rolle gilt als „unbekannt“ und ist
gültig; sie führt später zu `integration_not_approved`.

**Konzeptversion für Reviews (`review_concept_version`).** `review_target.concept_version` ist im Schema Pflicht und nicht leer. Der Wert
ist abgeleitet und steht je Auftrag im Zustand, das Modell erfindet nichts: die Version des Konzeptstands des Auftrags; für Umsetzungs- und
Fehleraufträge **ohne Konzeptbezug** (kein Konzeptstand, keine Abhängigkeit auf einen Konzeptauftrag) die feste Kennung `kein-konzept`
(`NoConceptVersion`, ohne Schemaänderung); leer, wo es keinen gültigen Wert gibt (Konzeptbezug nur über eine Abhängigkeit, kein Konzeptstand
am Auftrag: dann sind Review und Integration nicht zulässig). `Canonical` setzt das Feld immer neu, ein vom Befüller gesetzter Wert zählt nicht;
der Validator rechnet selbst. Die Kennung ist reserviert: ein Konzeptstand mit dieser Version macht den Snapshot ungültig. Derselbe Wert gilt für
den Bericht des Reviewers (`concept_version`, Skill `ccmas-code-review`) und die Integrationsanforderung, damit die Bindung in `PushAllowed`
(`CheckReview`) greift; eine Übernahme der Berichtsversion gibt es nicht.

**Vom Orchestrator (M03) zu liefern.** Der Snapshot ist eine Schnittstelle: Befüllt wird er aus dem führenden Zustand. Was erst
M03 kennt, ist hier als Feld vorbereitet, aber noch nicht produktiv befüllt: Ausführungsdatensätze (aktive Ausführungen, Rolle der
Reviewer-Ausführung, Ausführung der Implementierung), Kopf-Commit und Branch, PR- und CI-Stand, Abschlussnachweise
(`goal.evidence`), das zuletzt gemeldete Rollenergebnis (`last_result`), Reparaturrunden und die offenen Fragen. Der PR-Stand trägt optional
den Kopf-Commit des PR (`pr.head_commit`); ohne ihn lehnt der Validator bei einem offenen PR jede erneute Integration ab (fail-safe, siehe
„Keine Dublette bei der Integration“). **Konzeptfreigabe abhängiger Aufträge:** Der Snapshot-Bauer überträgt den freigegebenen Konzeptstand (Version,
Einstufung, Freigabe) auf Aufträge, die von einem Konzeptauftrag abhängen; ohne diesen Konzeptstand am Auftrag bleiben sie fail-safe blockiert
(Implementierung, Review und Integration abgelehnt, `review_concept_version` leer). Der Validator
verlässt sich nur auf den Snapshot, nie auf Angaben des Modells. Die Echtheit der Kennungen und Nachweise im Snapshot
verantwortet der Orchestrator. Welches Feld aus welcher Tabelle und Lesemethode kommt, steht seit CC-27 in [`persistence.md`](persistence.md), Abschnitt „Abbildung des `ProjectSnapshot`“.

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
| `precondition_unmet` | Auftrag blockiert, abgebrochen oder abgeschlossen (und andere nicht startbare Zustände); offene Abhängigkeit; bei Konzeptbezug des Auftrags (Konzeptstand am Auftrag oder Abhängigkeit auf einen Konzeptauftrag) Konzeptstand fehlt, Konzept nicht freigegeben oder Freigabe für andere Version (beides auch bei `request_integration`); Konzeptprüfer gibt Strategisches frei (dann entscheidet der Nutzer, nur `ask_question`); offene Frage; Review ohne abgeschlossene Implementierung, mit anderem Commit oder anderer Konzeptversion (ohne Konzeptbezug muss das Prüfziel genau `kein-konzept` tragen, mit Konzeptbezug darf es die Kennung nicht); nicht erfüllte deklarierte Voraussetzung | `ConceptApprover`, `ConceptApprovalValid`, Snapshot |
| `capacity_exceeded` | grundsätzlich startbare neue Ausführungen (`concept_work`, `implementation`, `review`, `documentation_work`; aus anderem Grund abgelehnte zählen nicht) plus aktive (ohne Supervisor) > `max_workers`; Reviewer zählen mit | Zählung |
| `duplicate_start` | zwei startende Entscheidungen (Beauftragung oder `request_integration`) für denselben Auftrag, ein Auftrag mit aktiver Ausführung, oder eine erneute `request_integration` ohne neue Wirkung (PR gemergt; offener PR trägt bereits den freigegebenen Commit; Kopf-Commit des offenen PR nicht belegt); `wait` und `ask_question` starten nichts | Snapshot |
| `scope_extension` | Ziel oder Abhängigkeit außerhalb des beauftragten Umfangs (`out_of_scope`), neue Ref, die ein nicht beauftragtes Ticket übernimmt | Snapshot |
| `mode_extension` | Modus eines neuen Auftrags (`plan_extension[].mode`) oder eines Auftrags bei `request_integration` weitet den beauftragten Modus auf | `IntegrationModeAllowed` |
| `integration_not_approved` | `request_integration` ohne gültiges, unabhängiges Review | `PushAllowed` (Rolle und Ausführungs-IDs aus dem Snapshot) |
| `premature_completion` | `request_completion` ohne belegten Abschluss; offener PR im PR-Modus ist Wartezustand `waiting_integration`; offene Aufträge | `FeatureComplete` / `MilestoneComplete` |
| `repair_budget_exhausted` | Reparaturbudget der Code- oder Konzeptschleife erschöpft; zulässig bleibt `ask_question` | `RepairStep` / `ConceptRepairStep` |
| `profile_not_allowed` | Rolle passt nicht zur Aktion, Modell unbekannt, Effort nicht unterstützt und nicht genehmigt reduzierbar, Modell oder Effort für die Rolle nicht erlaubt, Skill unbekannt, nicht freigegeben oder für die Rolle nicht erlaubt, Agent nicht angelegt | `Decision.Check`, `Resolver.ResolveProfile` |
| `step_not_covered` | Folgeschritt (Aktion und Rolle) steht nicht in den Rollenregeln, oder die dort geforderten Voraussetzungen fehlen unter `preconditions` | `NextSteps` |
| `delegation_incomplete` | Beauftragung (`concept_work`, `implementation`, `review`, `documentation_work`) ohne erwartetes Ergebnis (`expected_outcome`) oder ohne Aufwandsschätzung (`estimate`); im Schema optional (v1-kompatibel), im Betrieb Pflicht | Vorhandensein, kein Urteil über den Inhalt |

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
  und `ConceptApprovalValid` wie bisher (strategisch oder nicht einstufbar: nur die Frage an den Nutzer), und zwar bei `implementation`, `review` **und** `request_integration`: ein Konzeptstand ohne gültige Freigabe lässt auch keine Integration zu (`precondition_unmet`). Ein Konzeptbezug über
  eine Abhängigkeit ohne Konzeptstand am Auftrag wird abgelehnt (fail-safe); ohne Konzeptbezug gibt es nichts freizugeben, eine
  deklarierte Voraussetzung `concept_version_approved` ohne Version ist dort leer und kein Verstoß, mit Version falsch. Die
  Rollenregeln führen dafür zwei Schritte (`implementation` mit und ohne `concept_version_approved`); bei Konzeptbezug ergänzt
  die Deckung die Voraussetzung auch für den Schritt ohne sie. Ohne Konzeptbezug gibt es keine Konzeptversion, an die ein Review
  zu binden wäre; das Schema verlangt aber einen Wert. Prüfziel, Bericht und Integrationsanforderung tragen dann die feste Kennung
  `kein-konzept` (Abschnitt ProjectSnapshot); der Validator verlangt genau sie, bei Konzeptbezug lehnt er sie ab. Bei einem
  Konzeptbezug nur über eine Abhängigkeit (kein Konzeptstand am Auftrag) wird auch `request_integration` abgelehnt, wie `implementation`
  und `review`; es gibt keine Übernahme der Berichtsversion.
- **Keine Dublette bei der Integration.** Spezifikation „Externe Aktionen ohne Dubletten“ und „Unabhängiges Review vor dem Push“ (Schritte 6
  und 7: Branch und PR werden bereitgestellt beziehungsweise aktualisiert; jede weitere Änderung braucht vor dem nächsten Push wieder
  Review): Eine erneute `request_integration` wird mit `duplicate_start` abgelehnt, wenn der PR gemergt ist, wenn der offene PR bereits
  den Commit des freigegebenen Reviews als Kopf trägt (keine neue Wirkung) oder wenn der Kopf-Commit des offenen PR im Snapshot
  (`pr.head_commit`, optionales Feld, Schnittstelle zu M03) nicht belegt ist (fail-safe). Zulässig bleibt die Aktualisierung eines
  offenen PR: nach Reparatur und neuem `FREIGEGEBEN` für einen anderen Commit. Ein geschlossener PR gilt nicht als Dublette. Gewählt
  ist `duplicate_start` statt eines eigenen Codes: Es ist derselbe Fehler (ein zweiter Start derselben Wirkung); die Rückmeldung
  nennt den Grund.
- **Pflichtangaben je Delegation.** Jede Beauftragung nennt Rolle, Skills, Modell, Effort, erwartetes Ergebnis, Aufwand und Begründung
  (Spezifikation „Intelligente Arbeitsverteilung“). Auswahl und Begründung verlangt das Schema; `expected_outcome` und `estimate` sind dort
  v1-kompatibel optional, der Validator verlangt sie bei `concept_work`, `implementation`, `review` und `documentation_work`
  (`delegation_incomplete`). Er prüft das Vorhandensein, nicht die Qualität.
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
Profil fachlich passt, wie hoch der Aufwand ist (`estimate` und `expected_outcome` sind Angaben des Modells; der Validator verlangt ihr
Vorhandensein bei Beauftragungen, prüft sie aber nicht gegen eine Zahl) und ob ein Review fachlich genügt. Das Modell plant im Rahmen der Zulässigkeit; ist der Supervisor nicht verfügbar, wartet die
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
Zustand, Modus, Abhängigkeiten; Konzeptstand, Review samt Befundtexten, Ergebnis, PR und Commit entfallen), (3) `skill_list_dropped`, (4) `question_texts_shortened` (200 Zeichen, nur für erledigte Fragen:
zugestellt, ersetzt, abgebrochen). **Offene und blockierte Aufträge bleiben vollständig, auch ihre Befundtexte, ebenso die Texte offener und beantworteter, noch nicht zugestellter Fragen; kein Auftrag wird
entfernt** (Abhängigkeiten verweisen auf sie). Kürzungen treffen nur Abgeschlossenes oder Abgebrochenes; reicht es danach nicht, ist das `ContextError` und der Wartezustand `context_too_large`
(vor der Befundrunde 2 kürzte eine Regel `finding_texts_shortened` auch die Befunde offener Aufträge; sie entfällt). Der Prompt weist auf eine Verkürzung hin; das
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
`session_started` geladenen Skills; die **rohe strukturierte Ausgabe** und der **gestempelte Batch** (`stamped_batch`: was der Validator geprüft hat); Urteil (`accepted`, `rejected`, `no_structured_output`, `supervisor_failed`, `state_unavailable`), ob der Stand veraltet war, die aktuelle Revision, Ablehnungscodes und
-text, dazu dieselben Ablehnungen strukturiert (`rejections`: Code, Entscheidung, Begründung), der Hash des Zustands, gegen den geprüft wurde (`current_snapshot_sha256`, weicht bei veraltetem Stand vom Planungs-Snapshot ab), Code des terminalen Fehlers. Es ist die Grundlage der Belege in W14.3 („Nachweis M02“).

**Keine szenariospezifische Logik.** Der Planer enthält keine fest programmierte Auswahl von Aufgaben und keinen Ersatzplan; `TestPlannerAddsNothingOfItsOwn` hält fest, dass ein Plan mit einer Entscheidung eine Entscheidung bleibt.

## Skills (W14.2)

`ccmas-supervisor` (führend) beschreibt den Entscheidungsablauf: Zielverständnis, Konzeptreife, Risiko und Aufwand, Abhängigkeiten, nächste Arbeit (Fertiges vor Neuem, Reparatur bei Befunden, Integration nur nach Freigabe,
Parallelität im Kapazitätsrahmen, belegter Abschluss), Ergebnisbewertung, die Felder jeder Entscheidung, wann eine Frage an den Nutzer statt Arbeit folgt, und die Reaktion auf jeden Ablehnungscode. `ccmas-work-assessment`
trennt die Aufwandsschätzung (Größe, Komplexität, Unsicherheit) vom Modell-Effort und beschreibt die Profilwahl nach Qualität (Pflichtprüfungen entfallen nie). Beide sind Hilfsskills der Kategorie `planning` ohne
`contract.yaml` (Workflow-Verträge gibt es nur für die Kategorien `concept`, `review`, `delivery`, `completion`, `questions`), Rolle `supervisor`, Standardausstattung der Rolle, Herkunft `package: none`
([`skill-migration.md`](skill-migration.md)). `ccmas-neue-aufgabe-nehmen` und `ccmas-morgen-briefing` nutzt der Planer nicht; sie bleiben `pending_adaptation`, ihre Zuordnung zu einem Ticket ist offen.

## Szenario-Prädikate (W14.2, erweitert in W14.3 und W14.4)

`internal/supervisor/scenario` enthält die synthetischen Snapshots S1 bis S7 (erfundene Aufträge `sc1-a`, …; nie der Stand des echten Boards) und **benannte** semantische Prädikate, die einen Batch ohne Rücksicht auf den Wortlaut bewerten
(`Predicates(id)`, `EvaluateDetailed` = Ergebnis je Prädikat; `Evaluate` = alle Verstöße; gemeinsames Prädikat `delegations_complete` plus die des Szenarios). `TestScenarioPlanContract` prüft handgeschriebene gute und schlechte Batches, jeweils auch gegen den Validator. W14.3 wendet
dieselben Prädikate auf die echten Ausgaben an. S2 ist seit W14.3 in zwei Zustände getrennt (`S2-routine`, `S2-strategisch`), weil Routine- und strategisches Konzept verschiedene Wege haben (Konzeptfreigabe durch den `concept_reviewer` oder Entscheidung des Nutzers).

| Szenario | Zustand | Prädikate |
| --- | --- | --- |
| S1 | A und B unabhängig startbar, C hängt an A | `independent_orders_in_parallel`: Implementierung für A und B; `dependent_order_waits`: keine für C |
| S2 routine (`S2-routine`) | Routinekonzept, Version 0.1 ohne Freigabe; die Umsetzung hängt am Konzeptauftrag | `no_implementation`, `no_integration_or_completion`; `concept_path`: Konzeptarbeit am Konzeptauftrag, eine Frage an den Nutzer oder ein Konzeptreview durch den `concept_reviewer` (der Validator deckt die Konzeptprüfung erst nach dem gemeldeten Konzeptergebnis `concept_ready`; der Zustand meldet keines, im Zustand ist sie also nicht zulässig) |
| S2 strategisch (`S2-strategisch`) | strategisches Konzept (`scope_change`), Version 0.1 ohne Freigabe; die Umsetzung hängt am Konzeptauftrag | `no_implementation`, `no_integration_or_completion`; `user_decides`: eine Frage an den Nutzer oder klärende Konzeptarbeit; `no_approval_by_concept_reviewer`: nie ein Review (Freigabe) über den `concept_reviewer` |
| S3 | `max_workers` 2, eine Ausführung läuft, eine Implementierung fertig ohne Review, ein weiterer startbarer Auftrag | `review_of_finished_work` (Rolle `reviewer`, an den Kopf-Commit gebunden), `review_before_new_implementation`, `capacity_respected` (Beauftragungen ≤ freie Plätze) |
| S4 | Review mit BEFUNDE, Reparaturrunde 1 von 3 | `repair_for_same_order`: Implementierung (Rolle `implementer`) für denselben Auftrag; `no_integration_or_completion` |
| S5 | PR-Modus, Review FREIGEGEBEN, PR offen mit dem freigegebenen Commit als Kopf, CI bestanden | `wait`, `no_completion`, `no_repeated_integration` (der Validator lehnt sie zusätzlich als `duplicate_start` ab), `no_new_work_on_order`, `no_mode_extension` (kein neuer Auftrag im Modus `auto`) |
| S6 | Fehlerauftrag ohne Konzeptbezug, Review FREIGEGEBEN (Berichtsversion `kein-konzept`), noch kein PR; die Zustandsrevision ändert sich während der Planung (12 → 13) | `integration_requested`: genau eine Integration von `sc6-a`; `no_new_work_on_order`; `no_completion`. Der erste Plan (Revision 12) wird mit `stale_revision` abgelehnt, die Neuplanung läuft auf dem frischen Snapshot (13) |
| S7 | PR-Modus, `max_workers` 3, nichts aktiv; zwei unabhängige, startbare Implementierungsaufträge: `sc7-a` (Umbau der Zugriffskontrolle der lokalen Schnittstelle mit Sicherheits- und Rückwärtskompatibilitätsrisiko, ausführliche Akzeptanzkriterien, vom Nutzer freigegebenes strategisches Konzept 2.0 mit `architecture_change` und `permission_change`, im Titel als Architekturentscheidung mit hoher Unsicherheit ausgewiesen) und `sc7-b` (Fehlerauftrag ohne Konzeptbezug: ein Tippfehler in einer Fehlermeldung, ein Satz, klare Kriterien) | `both_orders_implemented`: beide werden von der Rolle `implementer` implementiert, jeder mit eigener Aufwandsschätzung (getrennt vom Effort); `critical_order_stricter_profile`: für `sc7-a` ein strengeres Profil als der Rollenstandard (stärkeres Modell oder höherer Effort); `trivial_order_not_stronger`: für `sc7-b` weder stärkeres Modell noch höherer Effort; `rationales_state_profile_reason`: beide Begründungen sind angegeben (nur Vorhandensein, nicht Wortlaut) |

Für jede Beauftragung (alle Szenarien) verlangt `CheckDelegations` (`delegations_complete`): Auswahl mit Rolle, mindestens einem Skill, Modell und Effort aus dem Angebot der Rolle im Snapshot, ein erwartetes Ergebnis, eine vollständige
Aufwandsschätzung (`estimate`: Größe, Komplexität, Unsicherheit; getrennt vom Effort) und eine Begründung.
**Stärke von Modell und Effort in S7:** Der Effort ist höher, wenn er im Angebot der Rolle (`allowed_efforts`, nach Stufe geordnet) hinter dem Standard steht. Das Angebot trägt keine Stärkeordnung der Modelle (`allowed_models` ist nach Modell-ID sortiert); das Prädikat stuft ein Modell deshalb nach der Familie im Namen ein (`haiku` < `sonnet` < `opus`). Ein Modell ohne bekannte Familie ist ein Verstoß („nicht einstufbar“), kein stillschweigendes Bestehen. „Strenger“ heißt: stärkeres Modell oder höherer Effort; „nicht stärker“ heißt: weder noch. Die Prädikate urteilen über den Inhalt; ob der Validator einen Batch annimmt, ist davon unabhängig
(zum Beispiel ist ein einzelnes `implementation` in S3 zulässig, aber inhaltlich schlecht).

## Belege (W14.2)

Ein einzelner Rauchlauf mit dem echten Runner (Durchleitung, nicht Planungsqualität): [`runtime-capabilities.md`](runtime-capabilities.md), Abschnitt 11, Beleg `evidence/live-supervisor-planner.jsonl`.
Test: `CCMAS_LIVE_TESTS=1 go test ./internal/supervisor -run TestLivePlanner -count=1 -v`. Er läuft mit haiku/low über ausdrückliche Auftragswerte (`Overrides.Order`), nicht über das Supervisorprofil.

## Nachweis M02 (W14.3)

Ergebnisziel von M02: Ein echter Claude-Supervisor beurteilt unterschiedliche Zustände und wählt begründet nächste Arbeit, Rollen, Skills, Modell und Effort; die Pläne unterscheiden sich belegbar je Zustand.
Aufgezeichnete Beispielentscheidungen ersetzen diesen Nachweis nicht (Spezifikation, „Kritische Negativ- und Grenzfälle“); die Belege hier sind rohe Ausgaben echter Sitzungen.

**Aufbau.** `TestLiveSupervisorScenarios` (`internal/supervisor/scenario/live_test.go`) plant jeden der acht Zustände S1, S2-routine, S2-strategisch, S3, S4, S5, S6 und S7 mit dem echten `Planner`: über den Go-Runner-Adapter und den echten Python-Runner,
mit dem Supervisorprofil der Beispielkonfiguration (`profile init` in ein temporäres Konfigurationsverzeichnis, gelesen aus `models.yaml`/`agents.yaml` dort; **keine Overrides**: angefordert und wirksam `claude-opus-5-5`, Herkunft `role`, Effort `high`),
dem Skill-Katalog dieses Worktrees (`skills.LoadChecked`; Snapshot der Rolle `supervisor` mit `ccmas-supervisor` Version 2 und `ccmas-work-assessment` Version 1, beide laut `session_started` in der Sitzung geladen), dem Systemprompt `prompts/supervisor.md` und dem lokalen Claude-Binary (Claude Code 2.1.311, `claude-agent-sdk` 0.2.180, Max-Anmeldung).
Die Zustände sind die synthetischen aus `internal/supervisor/scenario`, nie das echte Board und nie Asana. Standardwerte des Planers: höchstens 8 Runden, 10 Minuten je Versuch, bis zu zwei Neuplanungen. Das Budget waren 16 echte Sitzungen für S1 bis S6 (einschließlich Neuplanungen und Fehlversuchen); mit S7 gilt für die Sitzungen der Belege und einen Live-Testlauf zusammen höchstens 10 (`SessionLimit` = 10, je Live-Testlauf; der Rauchlauf aus W14.2 wird dort nicht mitgezählt, das Ticketbudget von 12 bis 16 Sitzungen ist mit insgesamt 10 echten Sitzungen in CC-14 eingehalten);
der Test zählt mit, startet kein Szenario, das das Budget überschreiten könnte, und zählt auch die Sitzungen bereits vorhandener Belege. Ein vorhandener Beleg wird nie überschrieben; eine Wiederholung gäbe es nur nach einem Infrastrukturfehler (Abo-Limit, Zeitüberschreitung, Prozessfehler: Wartegrund `supervisor_unavailable` oder `subscription_limit`), höchstens einmal je Szenario und als zweiter Lauf im selben Beleg mit Grund (`repeatAllowed`); ein inhaltlich schwacher Plan ist kein Grund.
S6: die `SnapshotSource` des Tests liefert zur Validierung des ersten Versuchs den Zustand mit der Revision 13 (gleichzeitige Änderung simuliert, Inhalt sonst gleich) und danach denselben neuen Stand.

**Lauf am 2026-11-10** (Aufruf `CCMAS_LIVE_TESTS=1 go test ./internal/supervisor/scenario -run TestLiveSupervisorScenarios -count=1 -timeout 90m -v`, S1 zuerst allein, dann die übrigen sechs): **8 echte Sitzungen von 16**, keine Wiederholung nach Infrastrukturfehler, kein Szenario ausgesetzt.

**Lauf S7 am 2026-11-10** (W14.4; Aufruf `CCMAS_LIVE_TESTS=1 CCMAS_LIVE_SCENARIOS=S7 go test ./internal/supervisor/scenario -run TestLiveSupervisorScenarios -count=1 -timeout 60m -v`; Prompt, Skills, Rollenregeln, Beispielkonfigurationen und Validator unverändert, die Policy-Hashes der Belege S1 bis S6 gelten weiter und die Belege wurden nicht neu erzeugt): **1 echte Sitzung, angenommen im ersten Versuch (14,7 s, Kontext 12.037 Bytes), keine Wiederholung. Insgesamt in CC-14: 9 echte Sitzungen von höchstens 10.**

| Szenario | Sitzungen (Versuche) | Angenommene Entscheidungen (Aktion Auftrag; Rolle, Modell/Effort; Aufwand Größe/Komplexität/Unsicherheit) | Prädikate | Auffälligkeiten |
| --- | --- | --- | --- | --- |
| S1 | `d9272048-f22e-452e-a3d5-7ce01a143c35` (1) | `implementation` sc1-a und sc1-b (implementer, sonnet/high; m/medium/medium), `wait` sc1-c bis sc1-a abgeschlossen | alle 3 erfüllt | Begründung nennt das freigegebene Routinekonzept, die freien Plätze und die Abhängigkeit; Profil = Rollenstandard |
| S2 routine | `1b015461-601c-4ca8-a781-9bb86ca3e2f2` (1) | `concept_work` sc2-con-routine (concept, sonnet/high; s/low/medium), `wait` sc2-impl-routine | alle 4 erfüllt | wählt Konzeptarbeit statt Konzeptreview, weil kein Konzeptergebnis `concept_ready` vorliegt (das Review wäre `step_not_covered`) |
| S2 strategisch | `e492c332-9ee1-4da7-99f3-e11c3332a92c` (1) | `ask_question` sc2-con-strategic (Frage an den Nutzer mit Kontext und Empfehlung), `wait` sc2-impl-strategic | alle 5 erfüllt | erkennt `scope_change` als Grund für die Entscheidung des Nutzers; keine Beauftragung |
| S3 | `f2fecfc1-828a-467e-9fd8-67ae4fcd7d0d` (1) | `review` sc3-fin (reviewer, opus/high; m/medium/medium), `wait` sc3-new, `wait` sc3-run | alle 4 erfüllt | Review vor neuer Implementierung, ein freier Platz genutzt; sc3-new wartet auf einen Platz |
| S4 | `ed36f38b-574f-4171-8315-b8e735e96b5f` (1) | `implementation` sc4-a (implementer, sonnet/high; s/low/low) als Reparatur | alle 3 erfüllt | nennt Befund f-1 und Reparaturrunde 1 von 3; keine Integration |
| S5 | `c4452fb5-a9a9-4282-a9ff-b719de776c0d` (1) | `wait` sc5-a | alle 6 erfüllt | begründet mit dem offenen PR, der den freigegebenen Commit trägt, bestandener CI und fehlendem Merge |
| S6 | `ba9fc29e-7457-4130-a7fd-a113df7e813f` (Versuch 1: abgelehnt, `stale_revision`), `c026baba-feb7-4426-81d7-c3224669e491` (Versuch 2: angenommen) (2) | `request_integration` sc6-a (beide Versuche inhaltlich gleich) | alle 4 erfüllt | Versuch 1 beruht auf Revision 12, geprüft gegen 13: `stale_revision`, nichts ausgeführt; Versuch 2 plant auf dem frischen Snapshot (Revision 13, anderer Hash) und wird angenommen |
| S7 | `21575c61-092f-4523-991f-bfc320ba9a45` (1) | `implementation` sc7-a (implementer, **opus/xhigh**; l/high/high), `implementation` sc7-b (implementer, **sonnet/low**; xs/low/low) | alle 5 erfüllt | erste Abweichung vom Rollenstandard (sonnet/high): strenger für den Umbau der Zugriffskontrolle, schlanker für den Tippfehler; beide Begründungen nennen den Profilgrund (Berechtigungs- und Kompatibilitätsgrenzen, hohe Unsicherheit des Konzepts; sehr kleine mechanische Änderung ohne Logik); beide laufen parallel auf 2 von 3 freien Plätzen |

Jede Zeile gilt für den Beleg `evidence/supervisor/<Szenario>.json` (`S1.json`, `S2-routine.json`, `S2-strategisch.json`, `S3.json`, `S4.json`, `S5.json`, `S6.json`, `S7.json`). Alle Versuche in S1 bis S6 liefen zwischen 7 und 13 Sekunden; der Kontext lag bei 10,4 bis 11,2 KiB (10.678 bis 11.432 Bytes), keine Reduktionsregel griff.

**Was ein Beleg enthält (je Versuch):** Datum, Szenario-ID, den vollständigen kanonischen Snapshot und dessen sha256, angewandte Kontextreduktionen, Hashes von Prompt, Systemprompt und Ausgabeschema, Skill-Snapshot-ID sowie Skill-IDs, -Versionen und `tree_sha256`,
das Supervisorprofil (angefordert, wirksam, Herkunft, `profile_id`/`version`), das wirksame Modell laut Sitzung, SDK- und CLI-Version, Session-ID, Dauer, die rohe strukturierte Ausgabe, den gestempelten Batch und das Validierungsurteil mit Ablehnungscodes und -texten (strukturiert und als Text).
Je Szenario kommen Endergebnis (angenommen/ausgesetzt), Anzahl der Versuche, Ergebnis jedes Prädikats auf dem angenommenen Batch und die Hashes der Policy-Eingaben hinzu (`models.example.yaml`, `agents.example.yaml`, die per `profile init` angelegten `models.yaml`/`agents.yaml`, `rules/roles.yaml`, `.claude/skills/manifest.yaml`, `prompts/supervisor.md`, Ausgabeschema),
damit spätere Abweichungen erklärbar sind. Die Belege sind redigiert: keine Pfade, keine Konto-, E-Mail- oder Organisationsdaten; der Schreiber und der Offline-Test weisen Pfade und E-Mail-Adressen ab.

**Offline-Prüfung (läuft in jedem `make check`, ohne Konto):** `TestSupervisorScenarioEvidence` lädt alle Belege und prüft die Snapshot-Hashes nach, den ersten Snapshot gegen `scenario.Snapshot`, die Policy-Eingaben gegen den heutigen Stand (Abweichung: „Beleg veraltet: Policy-Eingabe …“), Profil, Skills und Systemprompt,
wendet den heutigen Validator mit derselben Policy auf den gestempelten Batch jedes Versuchs an und vergleicht Urteil, Codes und Entscheidungen der Ablehnungen, wendet die Prädikate erneut auf die angenommenen Batches an,
und stellt fest, dass die angenommenen Pläne (Menge aus Aktion, Zielauftrag und Rolle) paarweise verschieden sind und sich auch in der Art der Arbeit (Aktion/Rolle) unterscheiden. S6: erster Versuch `stale_revision` auf der erhöhten Revision, Neuplanung auf dem frischen Snapshot, Endergebnis angenommen.
`TestEvidenceChainWritesEvidenceTheOfflineCheckAccepts` führt dieselbe Kette (Schreiben, Format, Prüfung) mit einer Fake-Runtime ohne Konto aus und zeigt, dass veraltete Policy-Eingaben, ein verfälschtes Urteil, ein veränderter Snapshot, ein veränderter Plan und Pfade im Beleg auffallen.

**Bewertung: was belegt ist.**

- Ein echter Claude (wirksames Modell `claude-opus-5-5` laut `session_started` in allen 9 Sitzungen) hat über die vollständige Kette (Go-Planer, Go-Adapter, Python-Runner, SDK, Claude Code) acht unterschiedliche Zustände beurteilt und jeweils einen vom Validator angenommenen Plan geliefert; sieben Szenarien beim ersten Versuch, S6 nach der Neuplanung.
- Die Pläne unterscheiden sich belegbar je Zustand: parallele Implementierung mit Warten des abhängigen Auftrags (S1), zwei parallele Implementierungen mit unterschiedlichem Profil (S7), Konzeptarbeit (S2-routine), Frage an den Nutzer (S2-strategisch), Review vor neuer Arbeit bei einem freien Platz (S3), Reparatur (S4), Warten auf den Merge (S5), Integration (S6). Die Prädikate (Inhalt) und der Validator (Zulässigkeit) bestätigen jeden angenommenen Plan.
- **Zustandsabhängige Wahl von Modell und Effort (S7):** In S1 bis S6 entsprach die Wahl überall dem Rollenstandard (implementer sonnet/high, reviewer opus/high, concept sonnet/high), die Zustände verlangten nichts anderes. In S7 wählte der echte Supervisor bei zwei unabhängigen, startbaren Implementierungsaufträgen für den Umbau der Zugriffskontrolle (Sicherheits- und Kompatibilitätsrisiko, Architekturentscheidung mit hoher Unsicherheit) ein stärkeres Profil (opus/xhigh, Schätzung l/high/high) und für den Tippfehler ein schlankeres (sonnet/low, Schätzung xs/low/low). Alle fünf Prädikate sind erfüllt; die Begründungen leiten die Wahl aus Risiko und Schwierigkeit ab, und Aufwandsschätzung und Effort stehen getrennt. Das ist ein Lauf an einem Zustand, dessen Lage fachlich eindeutig beschrieben ist (Skill und Prompt wurden dafür nicht zugeschnitten): Er zeigt, dass der Supervisor Profile zustandsabhängig und begründet wählen kann, nicht, wie zuverlässig oder wie fein.
- Rolle, Skills, Modell, Effort, erwartetes Ergebnis, Aufwandsschätzung und Begründung stehen bei jeder Beauftragung und liegen im Angebot der Rolle. Belegt ist damit die Auswahl **aus dem Angebot**, nicht eine zustandsabhängige Wahl der Skills: in allen Läufen S1 bis S7 sind die gewählten Skills gleich der Standardausstattung der Rolle (implementer `ccmas-feature-parallel`, reviewer `ccmas-code-review`, concept `ccmas-concept-ticket` und `ccmas-obsidian-seite`); die Aufwandsschätzung unterscheidet sich zwischen den Zuständen (zum Beispiel S4 s/low/low, S1 m/medium/medium, S7 l/high/high gegenüber xs/low/low).
- Die Erkennung eines veralteten Zustands greift mit dem echten Modell: der Plan von Revision 12 wird gegen 13 abgelehnt, nichts wird ausgeführt, die Neuplanung läuft auf dem frischen Snapshot.
- Das Supervisorprofil stammt aus der Beispielkonfiguration (Herkunft `role`), das wirksame Modell stimmt mit dem angeforderten überein, die Skills des Snapshots waren in der Sitzung geladen.

**Bewertung: was nicht belegt ist.**

- **Wirksamer Effort:** `high` ist angefordert und aus der Konfiguration abgeleitet; das SDK meldet den wirksamen Effort nicht (die Belege tragen ihn als „nicht gemessen“).
- **Reichweite der Profilwahl:** S7 ist ein Lauf an zwei klaren Gegenpolen. Nicht gezeigt ist, wie der Supervisor Zwischenfälle (mittleres Risiko) wählt und ob er auch für kritische Reviews oder Konzeptarbeit ein stärkeres Profil wählt (in S3 und S2 wählte er den Rollenstandard); dass derselbe Zustand immer zur selben Wahl führt, ist nicht gezeigt (siehe „Streuung“). Der Vergleich der Stärke von Modellen beruht auf der Familie im Namen, nicht auf einer Ordnung im Angebot.
- **Streuung:** je Szenario ein Lauf (S6: zwei Versuche mit demselben Ergebnis). Das Modell ist nicht deterministisch; wie oft derselbe Zustand zum selben Plan führt, ist nicht gemessen.
- **Echte Projektzustände** sind Sache von M03 (Befüllung aus Asana und dem führenden Zustand); die Zustände hier sind klein (10 bis 11 KiB), synthetisch und frei von Rauschen. Das Verhalten bei großen Zuständen und den Kontextreduktionen ist mit dem echten Modell nicht belegt.
- **Echte Ausführung** der geplanten Arbeit: nichts wurde gestartet, kein Worker, kein Review, kein Push; der Plan ist ein Vorschlag, den der Validator als zulässig bestätigt.
- **Inhaltliche Ablehnung mit dem echten Modell:** die einzige Ablehnung in den Läufen ist die künstlich erzeugte `stale_revision`; wie das Modell auf eine inhaltliche Ablehnung (zum Beispiel `capacity_exceeded`) reagiert, zeigen nur die Tests mit Fake-Runtime, nicht diese Läufe.
- **Konzeptreview der Routine (S2-routine):** der Zustand meldet kein Konzeptergebnis; das Review durch den `concept_reviewer` wäre vom Validator nicht gedeckt. Ob das Modell bei einem gemeldeten `concept_ready` das Review wählt, ist nicht belegt.
- **Zustandsabhängige Skill-Wahl:** nicht belegt. Die Angebote für `implementer` und `reviewer` enthalten nur einen Skill (der zugleich der Standard ist), die Wahl war dort nicht frei; nur `concept` bietet mit `ccmas-asana-ticket` einen Skill über die Standardausstattung hinaus, und der Supervisor wählte in S2-routine genau den Standard. Ob er Skills je nach Zustand abwählt, ergänzt oder austauscht, zeigen die Läufe nicht; dafür bräuchte es Zustände, in denen ein Zusatzskill fachlich gebraucht wird oder der Standardskill nicht passt, und Rollen mit mehr als einem wählbaren Skill.
- **Nutzung der Skills durch das Modell:** belegt ist, dass die Skills geladen und im Prompt waren, nicht, welche Teile das Modell verwendet hat.

## Prüfbefehle

```bash
go test ./internal/supervisor/... ./internal/domain/... ./internal/skills/... ./internal/profiles/... ./internal/workers/... ./prompts/... -count=1 -race
make check
# Live-Szenarien (verbrauchen Abo-Kontingent, vorhandene Belege werden nie überschrieben; das Budget ist erschöpft bis auf 1 Sitzung):
CCMAS_LIVE_TESTS=1 go test ./internal/supervisor/scenario -run TestLiveSupervisorScenarios -count=1 -timeout 60m -v
```
