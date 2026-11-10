# CCMAS – Rollenregeln und Workflow-Verträge

Stand: Ticket CC-33, Teil 1 (Rollenregeln, Workflow-Verträge, Prüfung in Go, Prüfskills), nachgebessert nach dem
unabhängigen Review (Entkopplung von Katalog und Plattformregeln, Konzeptprüfschleife). **Führend ist die
Spezifikation** „Multi Agent Setup“, Abschnitte „Skills, Regeln und Rollen“ (Skill-Schleife), „Concept-Tickets in
Asana“, „Beziehungen und Abschluss“, „PR und Auto-Merge“, „Fragen und Entscheidungen“, „Unabhängiges Review vor dem
Push“ und „Merge Queue und Abschluss“, dazu die Regeldatei im Vault (Abschnitte 4, 7.5, 7.7, 7.8, 8.1, 8.3). Diese
Datei hält fest, wie das Repository die Regeln maschinenlesbar führt, was Go prüft und was nicht. Sie ist keine Kopie
der Spezifikation und widerspricht der Regeldatei nie.

## Zweck und Grundsatz

Skills beschreiben Abläufe und Werkzeugwissen. Die **Regeln** stehen in der Regeldatei im Vault; `rules/roles.yaml`
ist ihr maschinenlesbarer Auszug für die Plattform. Je Workflow-Skill hält eine `contract.yaml` fest, was er
braucht, darf und liefert und was danach folgen darf.

Ein Vertrag und die Regeldatei **wählen aus und beschränken**; sie erweitern nichts. Weder ein Skill noch ein
`contract.yaml` noch `roles.yaml` kann Worker-Limit, Integrations-/Merge-Modus, Policy-Profil, Berechtigungen oder den
Zielbranch `develop` setzen oder aufweiten. Diese Werte kommen aus Projektkonfiguration, Profil und Auftrag. Ein
Versuch ist ein Ladefehler mit dem Code `platform_limit`, kein stilles Ignorieren.

## Orte und Laden

| Datei | Ort (relativ zum CCMAS-Softwareordner) | Hash/Snapshot |
| --- | --- | --- |
| Rollenregeln | `rules/roles.yaml` | nein (Plattformdatei, versioniert im Repository) |
| Workflow-Vertrag | `.claude/skills/<id>/contract.yaml` | ja: Datei des Skills im Manifest, damit Teil von `tree_sha256` und Snapshot |

**Zwei Stufen, bewusst getrennt.** Katalog und Plattformregeln sind nicht aneinander gekoppelt; wer nur einen Katalog
braucht (Anzeige, Profilprüfung), lädt mit `Load` und braucht weder Regeldatei noch Verträge. Tests, die einen
geprüften Bestand brauchen, legen ihn mit `internal/skills/skilltest` an (gültiger Softwareordner samt `rules/roles.yaml` und
Verträgen).

| Stufe | Funktion | Prüft | Verlangt |
| --- | --- | --- | --- |
| 1 | `skills.Load(root)` | Manifest, Dateien, Hashes, Frontmatter und **jede vorhandene** `contract.yaml` streng in sich: Schema, unbekannte und doppelte Schlüssel, `platform_limit` in jeder Tiefe, Aktionen, Rollen, Voraussetzungen und Urteile gegen `internal/domain`, Skill-ID und Rolle gegen den eigenen Manifest-Eintrag, `request_integration` nur mit `review_approved` | weder `rules/roles.yaml` noch das Vorhandensein von Verträgen |
| 2 | `skills.LoadRules(root)` und `skills.ValidateWorkflow(catalog, rules)`; `skills.LoadChecked(root)` bündelt Load, LoadRules und ValidateWorkflow | Regeldatei; Vertragspflicht je Workflow-Kategorie (`missing_contract`), kein Vertrag bei Hilfsskills; jeder Vertrag gegen die Rollenregeln (Folgeaufträge als Auswahl der Folgeschritte samt `requires`, `guarded_actions`, Umfang gegen `may`, Ergebnisformat, genau die Ergebnisse der Rolle, Kategorie trägt die Rolle); Bezüge zum Katalog (`uses_skills`, `external_writes`); fehlende Regeln sind `missing_rules` | geladene Regeln |

Die Stufe 2 läuft an drei Stellen, damit keine Lücke im echten Bestand unbemerkt bleibt: in `ccmas skills validate`
(Exit-Code 1 bei Befunden, `--json` mit `workflow_contracts`, alle Befunde auf einmal), in den Tests gegen den echten
Bestand (`TestWorkflowSkillContracts`, `TestValidateWorkflowIsSeparateStage`, `TestRepoManifestMatchesFiles` und die
übrigen Bestandstests über `LoadChecked`, damit die CI jede Lücke fängt) und bei jedem Aufrufer der Auswerter: Die
Auswerter sind Methoden auf `*skills.Rules`; ein nicht geladenes (`nil`) `*Rules` ergibt keine Entscheidung, sondern
`missing_rules` beziehungsweise die strengste Antwort (siehe unten). Welche Pfade geprüft laden (seit CC-14): `ccmas skills validate`, `ccmas skills snapshot` und `ccmas profile verify`
(auch `--live`) laden mit `LoadChecked`; bei einem Befund wird kein Snapshot geschrieben und kein Live-Lauf gestartet. Der
Runner-Adapter schreibt Snapshots für Ausführungen nur aus einem Katalog, der mit `LoadChecked` geladen ist
(`Catalog.Checked()`; sonst weist `BuildSpec` ab). `ccmas skills list/show` und die Profilbefehle (`profile resolve/set`,
`agent`) laden nur den Katalog (Stufe 1): Wer einen Bestand ansehen will, muss das auch können, wenn die Plattformregeln
gerade fehlen oder verletzt sind; diese Befehle starten keine Ausführung und schreiben keinen Snapshot. Eine fehlende oder
ungültige Regeldatei ist daher kein Fehler von `Load`, sondern ein Befund von Stufe 2: ohne Regeln entscheidet die Plattform
nichts (fail-safe). Beide Dateien werden **streng** gelesen
(`go.yaml.in/yaml/v3`, `KnownFields`): unbekannte und doppelte Schlüssel, leere Dateien, mehrere YAML-Dokumente und
ein Wert wie `2.5` für eine ganze Zahl sind Fehler. Meldungen nennen den Schlüsselpfad (`results.findings.followups[0]`).

`Catalog.Contract(id)` liefert den in Stufe 1 geprüften Vertrag; gegen die Rollenregeln ist er erst nach
`ValidateWorkflow` geprüft. Workflow-Skills sind die Skills der Kategorien `concept`, `review`, `delivery`, `completion`, `questions`
(`workflow_categories` in der Regeldatei): `ccmas-concept-ticket`, `ccmas-concept-review`, `ccmas-code-review`,
`ccmas-feature-parallel`, `ccmas-meilenstein-abschluss`, `ccmas-antworten-abholen`. Hilfsskills (`tracking`,
`knowledge`, `rules`, `planning`, `setup`) haben keinen Vertrag; eine `contract.yaml` dort ist ein Befund.

## Fehlercodes

| Code | Bedeutung |
| --- | --- |
| `missing_rules` / `invalid_rules` | `rules/roles.yaml` fehlt beziehungsweise verletzt Format, Querbezüge oder feste Regeln (Pfad `rules/roles.yaml`); gemeldet von `LoadRules`, `LoadChecked`, `ValidateWorkflow` (Regeln nicht geladen) und den Auswertern ohne Regeln, nie von `Load` |
| `missing_contract` / `invalid_contract` | Workflow-Skill ohne `contract.yaml` (nur `ValidateWorkflow`) beziehungsweise Vertrag verletzt Format, Rollenregeln oder Querbezüge (Skill und Pfad `contract.yaml`; Format-Verstöße schon in `Load`) |
| `platform_limit` | Regeldatei oder Vertrag enthält einen Schlüssel, der eine Plattformgrenze setzt (`max_workers`, `integration_mode`, `merge`, `mode`, `policy_profile`, `permissions`, `permission_mode`, `allowed_tools`, `tools`, `base_branch`, `target_branch`, …), einen anderen Zielbranch als `develop`, oder die Regeldatei schützt die Plattformwerte nicht mehr vollständig. Auch zur Laufzeit: Aufweitung des Integrationsmodus |
| `review_missing` / `review_invalid` / `review_not_independent` | Auswerter `PushAllowed` (siehe unten) |
| `concept_approval_invalid` | Auswerter `ConceptApprovalValid` |

Die Codes erscheinen zusammen mit den übrigen in `errors.Is(err, &skills.Error{Code: …})`, `skills.CodeOf` und in
den Befunden der CLI.

## Aufbau `rules/roles.yaml` (`schema_version: 1`)

| Abschnitt | Inhalt | Von Go erzwungen |
| --- | --- | --- |
| `platform` | `target_branch: develop`, `protected: [max_workers, integration_mode, policy_profile, permissions, target_branch]` | `target_branch` gleich `domain.BaseBranch`; `protected` genau diese Menge (`platform_limit`) |
| `capabilities`, `always_forbidden` | Vokabular für `scope`; `start_agents` und `change_platform_limits` sind für alle Rollen verboten | genau das bekannte Vokabular; die beiden Pflichtverbote |
| `workflow_categories` | Kategorie → Rollen, die sie tragen | genau die fünf Kategorien; Rollen bekannt und mit Regeln |
| `formats` | Ergebnisformate mit Pflichtfeldern | `review_report` hat genau die Felder von `domain.ReviewReport`; `decision` nur Felder von `domain.Decision` |
| `review_before_push` | Urteil `FREIGEGEBEN`, gebunden an `commit` und `concept_version`, unabhängige Ausführung, neues Review nach jeder Änderung, geschützte Aktion `request_integration` mit Voraussetzung `review_approved` | feste Werte; jede Lockerung ist ein Fehler |
| `concept_approval` | Routine → `concept_reviewer`/`review`; strategisch und nicht einstufbar → Nutzer/`ask_question`; Bindung an `concept_version`; Kriterien für „strategisch“ | feste Wege; mindestens ein Kriterium, Kennungen eindeutig |
| `repair_loop` | `max_rounds: 3`; Code-Schleife: bei Befunden `implementation`/`implementer`; Konzeptschleife: bei Befunden der Konzeptprüfung `concept_work`/`concept` (`on_concept_findings`); danach `ask_question` | Budget 1 bis 10 (ganze Zahl); feste Aktionen |
| `integration` | Standardmodus `pr`, Auto nur ausdrücklich je Auftrag, Wartezustand `waiting_integration` | feste Werte |
| `completion` | Nachweise für Feature und zusätzlich Meilenstein; offener PR zählt nie als Abschluss | feste Nachweismengen; `open_pr_counts_as_completion: false` |
| `question_route` | Worker → Supervisor → CCMAS-Zentrale → Supervisor → Worker; `silence_is_approval: false` | feste Folge; Schweigen ist nie Freigabe |
| `roles` | je Rolle aus `internal/domain`: `may` (höchstens erlaubte Fähigkeiten), `format`, `results` mit `verdict` (nur bei `review_report`) und `next` | alle neun Rollen; Aktionen und Rollen gültig; keine Sackgassen |

Kriterien für „strategisch“ (`concept_approval.strategic_criteria`), nach Regel 4 („Vorher abstimmen“) und
Spezifikation („Fragen und Entscheidungen“): `scope_change`, `paid_service`, `product_decision_deviation`,
`architecture_change`, `accepted_limitation`, `permission_change`, `unclear_requirements`.

**Folgeschritte (`roles.<rolle>.results.<ergebnis>.next`)** sind Einträge `{action, role, requires}`:

- `action` ist eine der acht Decision-Aktionen aus `internal/domain`.
- `role` passt zur Aktion wie in `domain.Decision.Check`: `concept_work` → `concept`, `implementation` →
  `implementer`, `review` → `reviewer` oder `concept_reviewer`, `documentation_work` → `documentation` (seit CC-14). Für die übrigen gilt dieselbe Zuordnung als Regel der
  Plattform: `plan_extend` → `supervisor`, `request_completion` → `completion`; `ask_question` (Nutzer über die Zentrale),
  `request_integration` (Plattform, Merge Queue) und `wait` tragen keine Rolle. `TestRulesMatchPlatformDefinitions`
  vergleicht die Tabelle mit dem echten `Decision.Check`.
- `requires` nennt die mindestens erforderlichen Voraussetzungsarten (`work_order_completed`,
  `concept_version_approved`, `review_approved`, `ci_passed`, `capacity_available`; Test gegen
  `decision-v1.schema.json`).
- Jede Aktion in `review_before_push.guarded_actions` verlangt `review_approved`; ein Folgeschritt ohne diese
  Voraussetzung ist ein Fehler in der Regeldatei.
- Der erste Eintrag ist der Standardschritt. **Keine Sackgassen:** von jedem Ergebnis muss über die Folgeschritte
  Warten, eine Frage an den Nutzer oder die Integration erreichbar sein; eine Schleife ohne Ausgang wird abgelehnt.

Ergebnisse je Rolle (Stand W33.1):

| Rolle | Ergebnisse (→ Standardschritt) |
| --- | --- |
| `supervisor` | `plan_updated` (→ `implementation` mit `concept_version_approved`, danach `implementation` ohne Konzeptbezug; ferner `concept_work`, `review`, `documentation_work`, `request_integration`, `request_completion`, `plan_extend`, `wait`), `user_decision_needed` (→ `ask_question`), `goal_reached` (→ `request_completion`), `idle` (→ `wait`) |
| `project_setup` | `project_connected` (→ `plan_extend`), `setup_blocked` (→ `ask_question`) |
| `concept` | `concept_ready` (→ `review`/`concept_reviewer`, sonst `ask_question`), `concept_blocked` (→ `ask_question`) |
| `concept_reviewer` | `concept_approved` (FREIGEGEBEN, → `plan_extend`), `concept_findings` (BEFUNDE, → `concept_work`), `needs_user_decision` (→ `ask_question`) |
| `implementer` | `implemented` (→ `review`/`reviewer`), `implementation_blocked` (→ `ask_question`) |
| `reviewer` | `approved` (FREIGEGEBEN, → `request_integration`), `findings` (BEFUNDE, → `implementation`) |
| `documentation` | `documentation_done` (→ `request_completion`), `documentation_blocked` (→ `ask_question`) |
| `completion` | `completed` (→ `plan_extend`), `waiting_integration` (→ `wait`), `incomplete` (→ `plan_extend`) |
| `question_handler` | `answer_assigned` (→ `implementation`, sonst `concept_work`), `answer_unclear` (→ `ask_question`), `no_answer` (→ `wait`) |

**Erreichbarkeit der Rolle `documentation` (seit CC-14).** Die Aktion `documentation_work` (Rolle `documentation`) ist ein Folgeschritt
des Supervisors in `plan_updated`; `Decision.Check` nimmt die Rolle bei dieser Aktion an und lehnt jede andere ab. Damit kann der
Supervisor Dokumentationsarbeit beauftragen. Der Folgeschritt `plan_extend` (Rolle `supervisor`) steht ebenfalls in `plan_updated`,
weil die Planung des Supervisors selbst eine Plan-Ergänzung ist. Die Prüfung einer Konzeptversion durch den Konzeptprüfer
(`review` mit Rolle `concept_reviewer`) bleibt Folgeschritt der Rolle `concept` (`concept_ready`).

## Aufbau `contract.yaml` (`schema_version: 1`)

```yaml
schema_version: 1
skill: ccmas-code-review          # = Verzeichnisname
role: reviewer                    # in roles des Manifests und von der Kategorie getragen
uses_skills: []                   # Hilfsskills; müssen existieren und die Rolle erlauben
inputs:                           # Eingaben
  - {name: commit, required: true, description: "…"}
preconditions: [work_order_completed]   # Voraussetzungsarten (auch leer)
scope:                            # erlaubter Umfang
  within_work_order: true         # muss true sein: der Skill erweitert den Nutzerauftrag nicht
  allowed: [read_repo, read_vault, run_checks]
  forbidden: [write_worktree, commit, push, …]
result:
  format: review_report           # Format der Rolle aus roles.yaml
checks:                           # Prüfungen
  - {id: stand_unique, description: "…"}
blockers:                         # Blockaden: Bedingung und das Ergebnis, das dann gemeldet wird
  - {id: stand_not_unique, description: "…", result: findings}
completion_criteria: ["…"]        # Abschlusskriterien
results:                          # Folgeaufträge je Ergebnis der Rolle
  approved:
    verdict: FREIGEGEBEN          # wie in den Rollenregeln, nur bei Prüfrollen
    followups:
      - {action: request_integration, preconditions: [review_approved]}
  findings:
    verdict: BEFUNDE
    followups:
      - {action: implementation, role: implementer}
```

Alle zwölf Schlüssel auf der obersten Ebene sind Pflicht. Go prüft gegen die Rollenregeln und das Manifest:

- `skill` gleich Skill-ID; `role` im Manifest-Eintrag und in `workflow_categories` für die Kategorie.
- `scope.allowed` ⊆ `roles.<rolle>.may`; alles, was die Rolle nicht darf, steht ausdrücklich in `scope.forbidden`;
  `allowed` und `forbidden` überschneiden sich nicht; `start_agents` und `change_platform_limits` sind nie erlaubt.
  Erlaubte Schreibfähigkeiten müssen im Inventar `external_writes` des Manifests stehen (`write_asana` → `asana`,
  `write_vault` → `vault`, `commit`/`push` → `git`, `open_pr`/`merge` → `github`).
- `result.format` gleich dem Format der Rolle.
- `results` enthält **genau** die Ergebnisse der Rolle, jedes mit mindestens einem Folgeauftrag und mit dem Urteil der
  Rollenregeln. Jeder Folgeauftrag ist eine Auswahl aus den `next`-Einträgen der Rolle (gleiche Aktion und Rolle),
  nennt mindestens deren `requires` und bei geschützten Aktionen `review_approved`. Ein Vertrag kann Folgeschritte
  einschränken, nie ergänzen.
- `blockers[].result` verweist auf ein Ergebnis des Vertrags; Kennungen sind eindeutig; Beschreibungen nicht leer.

Skills mit Vertrag im Bestand und ihre Rolle: `ccmas-concept-ticket` (`concept`), `ccmas-concept-review`
(`concept_reviewer`), `ccmas-code-review` (`reviewer`), `ccmas-feature-parallel` (`implementer`),
`ccmas-meilenstein-abschluss` (`completion`, zugleich Feature-Abschluss), `ccmas-antworten-abholen` (`question_handler`).
Alle sechs sind `approved` und Standard ihrer Rolle. Seit W33.2 stimmen Text und Vertrag der drei zuletzt angepassten Skills
überein: zentrale Delegation statt Fan-out, Push und PR erst nach dem Review, Abschluss nur mit Merge und Nachweisen,
Fragenweg über die Zentrale, `external_writes` wie der Umfang (`ccmas-feature-parallel`: nur `git` für den lokalen Commit).
Die Verträge von Abschluss und Fragenbearbeitung nennen `ccmas-obsidian-seite` in `uses_skills` (Wissen für die
Schreibabsicht; Ausführung über die Adapter), der Skill ist dafür für beide Rollen erlaubt, aber nicht Standard.

## Auswerter der Plattform (`internal/skills/workflow_eval.go`)

Kleine, zustandslose Funktionen auf `*skills.Rules` (`Catalog.Rules()` oder `LoadRules`). Sie entscheiden
Zulässigkeit, keine Fachplanung, und sind fail-safe: Unbekanntes, Fehlendes und Widersprüchliches führt zur
strengeren Seite.

| Funktion | Entscheidet |
| --- | --- |
| `PushAllowed(req, report, implementerExecutionID, reviewerRole)` | Push/Integration nur mit Review-Bericht, der `IntegrationRequest.CheckReview` besteht (gleicher Bericht, Auftrag, **Commit**, **Konzeptversion**, Urteil `FREIGEGEBEN`), von einer **anderen** Ausführung als die Implementierung, und aus einer Ausführung der **Rolle `reviewer`**. Fehlende Kennung der Implementierung oder des Reviews zählt als nicht unabhängig; jede andere oder fehlende Rolle (auch `concept_reviewer`) ist `review_invalid`. Codes `review_missing`, `review_invalid`, `review_not_independent` |
| `ConceptApprover(assessment)` | Routine ohne zutreffendes Kriterium → `concept_reviewer` (Aktion `review`); strategisch, nicht eingestuft, unbekannt eingestuft oder Routine mit zutreffendem Kriterium → Nutzer (`ask_question`) |
| `ConceptApprovalValid(approval, assessment, currentVersion)` | Freigabe gilt nur für dieselbe, nicht leere Konzeptversion; der Konzeptprüfer gibt nur Routinekonzepte frei, der Nutzer jedes, ebenfalls nur für die genannte Version |
| `RepairStep(round)` / `ConceptRepairStep(round)` / `MaxRepairRounds()` | Runde 1 bis `max_rounds` (Standard 3): Code-Schleife = Implementierung mit den Befunden und danach frisches Review; Konzeptschleife = Konzeptarbeit mit den Befunden (`concept_work`) und danach frische Konzeptprüfung; darüber sichtbarer Halt mit `ask_question`. Beide Schleifen haben dasselbe Budget und werden je Schleife getrennt gezählt |
| `NextSteps(role, result)` / `NextStep` | zulässige Folgeschritte beziehungsweise Standardschritt; unbekannte Rolle oder unbekanntes Ergebnis ist `invalid_request` |
| `FeatureComplete(evidence)` / `MilestoneComplete(evidence)` | Abschluss nur mit Kriterien erfüllt, bestätigtem Merge, Review-, Test- und CI-Nachweis sowie nachgezogenem Aufgaben- und Wissensstand (Meilenstein zusätzlich Teilziele und Abnahme). Ergebnis `Complete`, `State` und `Missing`. Offener PR → `waiting_integration`, nie `completed`. Im PR-Modus zählt nur ein menschlicher Merge; ein unbekannter Modus gilt als PR |
| `IntegrationModeAllowed(ordered, requested)` | Auto nur, wenn der Auftrag Auto ausdrücklich enthält; Aufweitung ist `platform_limit` |
| `QuestionRoute()` / `SilenceIsApproval()` | Fragenweg; Schweigen ist nie Freigabe |

**Ohne geladene Regeln** (`nil`-`*Rules`) entscheidet kein Auswerter etwas: `PushAllowed`, `ConceptApprovalValid`,
`RepairStep`, `ConceptRepairStep`, `NextSteps`/`NextStep` und `IntegrationModeAllowed` liefern `missing_rules`,
`ConceptApprover` verweist an den Nutzer, `FeatureComplete`/`MilestoneComplete` melden nie Abschluss (`Missing: ["rules"]`),
`MaxRepairRounds` ist 0, `QuestionRoute` leer und `SilenceIsApproval` falsch. Test: `TestEvaluatorsWithoutRulesAreFailSafe`.

**Rolle des Prüfers.** Das `ReviewReport`-Schema hat kein Rollenfeld; ein Bericht kann seine Rolle nicht selbst belegen.
`reviewerRole` kommt deshalb aus dem Ausführungsdatensatz der Plattform (Rolle, in der `report.ReviewerExecutionID`
gestartet wurde), nicht aus dem Bericht. Damit kann ein Konzeptprüfbericht (Rolle `concept_reviewer`) keinen Push tragen,
auch wenn sein `commit` zufällig dem zu pushenden Stand entspricht. Die Grenze: Ist der Ausführungsdatensatz falsch, hilft
das nicht; Echtheit der Kennungen und Rollen verantwortet der Orchestrator.

Wichtig für den Orchestrator (M03): Diese Auswerter sind **Bausteine**. Sie werden an der Stelle aufgerufen, an der
Push, Integration, Abschluss oder Freigabe tatsächlich ausgeführt werden; im Skill-Katalog allein löst sich nichts davon
aus.

**Produktiver Aufrufer seit CC-14.** Der Validator in `internal/supervisor` ([`supervisor.md`](supervisor.md)) ruft
`PushAllowed` (mit Rolle der Reviewer-Ausführung und Ausführungs-IDs aus dem `ProjectSnapshot`), `ConceptApprover`,
`ConceptApprovalValid`, `RepairStep`, `ConceptRepairStep`, `IntegrationModeAllowed`, `FeatureComplete`/`MilestoneComplete` und
`NextSteps` für jedes Planungsergebnis des Supervisors auf. Damit entscheiden diese Auswerter über Entscheidungen, nicht nur in
Tests. Was weiter offen bleibt: den Validator ruft erst der Orchestrator (M03) vor jeder Ausführung auf, und er liefert die
Ausführungsdatensätze, aus denen der Snapshot Rolle und IDs des Reviews bezieht.

## Bedeutung von `scope` (Schreibabsicht, keine Berechtigung)

`scope.allowed` mit `write_asana`, `write_vault`, `write_worktree`, `commit` meint die **Schreibabsicht über die
kontrollierten Adapter** der Plattform: Der Skill liefert Inhalt und Absicht, die Plattform führt aus. Es ist keine
direkte Berechtigung und keine Aussage über Werkzeuge der Sitzung; Rechte kommen allein aus Policy-Profil und
Berechtigungsmodell. `external_writes` im Manifest bleibt dazu das Inventar der betroffenen Systeme.

## Was Go erzwingt und was nicht

**Erzwungen** (Tests in `internal/skills/workflow_test.go`):

- Form und Strenge beider Dateien, Pflichtteile, geschlossene Vokabulare (Rollen, Aktionen, Voraussetzungsarten,
  Fähigkeiten, Ergebnisformate).
- Keine Plattformgrenze in Regeldatei oder Vertrag; feste Werte der Regeln (Review vor Push, Konzeptwege, Abschluss,
  Fragenweg, Integrationsmodus).
- Querbezüge: Rolle zur Aktion wie in `Decision.Check`, Folgeaufträge für jedes Ergebnis, Auswahl nur aus den
  Rollenregeln, Hilfsskills im Katalog, Schreibfähigkeiten im Inventar, keine Sackgassen.
- Kein Vertrag sieht `request_integration` ohne `review_approved` vor (Vertrags- und Regelebene).
- Die Auswerter oben verhalten sich wie beschrieben (Review-, Konzept-, Reparatur-, Abschluss-, Modusfälle).

**Nicht erzwungen** (Verantwortung bleibt bei Plattform und Prozess):

- Der **Inhalt** der `SKILL.md`: dass ein Skill seinen Vertrag tatsächlich einhält, sagt kein Test. Es gibt bewusst
  keine Textsuche nach Formulierungen; geprüft wird nur, dass die Kennungen des Vertrags (Ergebnisse, Blockaden,
  Prüfungen) der drei in W33.2 angepassten Skills im Text als `kennung` vorkommen und jede SKILL.md einen Abschnitt
  „Ausführungskontext“ hat. Eingehalten wird er durch die Ausführung, ihre Berechtigungen (Policy-Profil,
  Worktree-Isolation) und das Review.
- Dass `PushAllowed`, `FeatureComplete` und die übrigen Auswerter an den Stellen **aufgerufen** werden, an denen Push,
  Integration und Abschluss tatsächlich geschehen: das ist Sache des Orchestrators (M03) und der Adapter (M05/M06). Für
  Planungsergebnisse des Supervisors ruft sie seit CC-14 der Validator auf (`supervisor.md`); der Validator selbst wird erst vom
  Orchestrator aufgerufen.
- Die fachliche Einstufung eines Konzepts (routine oder strategisch) und die Qualität eines Reviews: sie entstehen in
  den Ausführungen von Konzept-Agent und Prüfern.
- Die Echtheit der Ausführungskennungen, Hashes und Nachweise, die der Orchestrator übergibt.
- Ob der Supervisor aus den zulässigen Folgeschritten den fachlich richtigen wählt: das entscheidet das Modell, nicht Go
  (`supervisor.md`).

## Ausführungskontext der Skills

- **In CCMAS** führt die Plattform alle Schreibaktionen (Asana, Vault, Git, PR) über die kontrollierten Adapter aus.
  Der Skill liefert Inhalt und Absicht und startet keine Agenten.
- **In einer Entwicklungssitzung ohne CCMAS-Dienst** erledigt die Hauptsitzung die Schreibvorgänge nach der
  Regeldatei und setzt die Stationen (Review vor Push, Freigabe strategischer Fälle) selbst um.

## Offene Punkte

- **`ReviewReport` ohne Commit bei Konzeptprüfungen:** Das Schema verlangt `commit` (Git-Commit). Der Bericht einer
  Konzeptprüfung bezieht sich auf die **Konzeptversion** (`concept_version`); `commit` ist dort nur der Bezugsstand des
  Code-Repositorys (Stand `develop` zum Prüfzeitpunkt, Hilfslösung) und **keine Freigabe von Code**. `PushAllowed`
  verlangt dafür die Rolle `reviewer` der Ausführung (siehe oben). Offen: eine eigene Berichtsart für Konzeptprüfungen oder ein
  optionaler `commit` im Schema; beides ist eine Vertragsänderung (`contracts/`, `internal/domain`, Folgeticket).
- **Supervisor-Skills:** `ccmas-supervisor` und `ccmas-work-assessment` (CC-14, W14.2) sind Hilfsskills der Kategorie `planning` und haben bewusst keinen Vertrag
  (Verträge gibt es nur für die Workflow-Kategorien; ihr Ergebnisformat ist das Planungsergebnis `decision-batch`, geprüft vom Validator). Die
  Rolle `documentation` ist seit W14.1 über die Aktion `documentation_work` erreichbar; ein Workflow-Skill für sie gibt es nicht,
  sie arbeitet mit `ccmas-obsidian-seite` und bei beauftragter Regelpflege `ccmas-regeln-pflegen`.
- **Anbindung an Profile und Decision:** `AgentProfile.skill_ids` und `Decision.selection.skill_ids` sowie die
  Voraussetzungsprüfung vor einer Ausführung gehören zum Orchestrator (CC-46, M03).
