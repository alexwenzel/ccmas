# CCMAS – Rollenregeln und Workflow-Verträge

Stand: Ticket CC-33, Teil 1 (Rollenregeln, Workflow-Verträge, Prüfung in Go, Prüfskills). **Führend ist die
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

`skills.Load(root)` prüft Manifest, Verträge und Regeln zusammen; `ccmas skills validate` zeigt alle Befunde auf einmal
(Exit-Code 1 bei Befunden, `--json` mit `workflow_contracts`). Eine fehlende oder ungültige Regeldatei ist ein
Ladefehler: ohne Regeln entscheidet die Plattform nichts (fail-safe). Beide Dateien werden **streng** gelesen
(`go.yaml.in/yaml/v3`, `KnownFields`): unbekannte und doppelte Schlüssel, leere Dateien, mehrere YAML-Dokumente und
ein Wert wie `2.5` für eine ganze Zahl sind Fehler. Meldungen nennen den Schlüsselpfad (`results.findings.followups[0]`).

Workflow-Skills sind die Skills der Kategorien `concept`, `review`, `delivery`, `completion`, `questions`
(`workflow_categories` in der Regeldatei): `ccmas-concept-ticket`, `ccmas-concept-review`, `ccmas-code-review`,
`ccmas-feature-parallel`, `ccmas-meilenstein-abschluss`, `ccmas-antworten-abholen`. Hilfsskills (`tracking`,
`knowledge`, `rules`, `planning`, `setup`) haben keinen Vertrag; eine `contract.yaml` dort ist ein Befund.

## Fehlercodes

| Code | Bedeutung |
| --- | --- |
| `missing_rules` / `invalid_rules` | `rules/roles.yaml` fehlt beziehungsweise verletzt Format, Querbezüge oder feste Regeln (Pfad `rules/roles.yaml`) |
| `missing_contract` / `invalid_contract` | Workflow-Skill ohne `contract.yaml` beziehungsweise Vertrag verletzt Format, Rollenregeln oder Querbezüge (Skill und Pfad `contract.yaml`) |
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
| `repair_loop` | `max_rounds: 3`, bei Befunden `implementation`/`implementer`, danach `ask_question` | Budget 1 bis 10 (ganze Zahl); feste Aktionen |
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
  `implementer`, `review` → `reviewer` oder `concept_reviewer`. Für die übrigen gilt dieselbe Zuordnung als Regel der
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
| `supervisor` | `plan_updated` (→ `implementation`), `user_decision_needed` (→ `ask_question`), `goal_reached` (→ `request_completion`), `idle` (→ `wait`) |
| `project_setup` | `project_connected` (→ `plan_extend`), `setup_blocked` (→ `ask_question`) |
| `concept` | `concept_ready` (→ `review`/`concept_reviewer`, sonst `ask_question`), `concept_blocked` (→ `ask_question`) |
| `concept_reviewer` | `concept_approved` (FREIGEGEBEN, → `plan_extend`), `concept_findings` (BEFUNDE, → `concept_work`), `needs_user_decision` (→ `ask_question`) |
| `implementer` | `implemented` (→ `review`/`reviewer`), `implementation_blocked` (→ `ask_question`) |
| `reviewer` | `approved` (FREIGEGEBEN, → `request_integration`), `findings` (BEFUNDE, → `implementation`) |
| `documentation` | `documentation_done` (→ `request_completion`), `documentation_blocked` (→ `ask_question`) |
| `completion` | `completed` (→ `plan_extend`), `waiting_integration` (→ `wait`), `incomplete` (→ `plan_extend`) |
| `question_handler` | `answer_assigned` (→ `implementation`, sonst `concept_work`), `answer_unclear` (→ `ask_question`), `no_answer` (→ `wait`) |

Die Rolle `documentation` ist über Folgeschritte noch nicht erreichbar: dafür fehlt bis zum Supervisor-Skill (CC-14)
eine passende Decision-Aktion; die Regeln bleiben dort offen.

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
`ccmas-meilenstein-abschluss` (`completion`), `ccmas-antworten-abholen` (`question_handler`). Die Verträge der drei
Skills, deren Texte erst in W33.2 angepasst werden, beschreiben bereits das **Zielbild** (zentrale Delegation, Push und
PR erst nach dem Review durch die Plattform, Abschluss nur mit Merge und Nachweisen, Fragenweg über die Zentrale); die
Skills selbst bleiben bis dahin `pending_adaptation`.

## Auswerter der Plattform (`internal/skills/workflow_eval.go`)

Kleine, zustandslose Funktionen auf `*skills.Rules` (`Catalog.Rules()` oder `LoadRules`). Sie entscheiden
Zulässigkeit, keine Fachplanung, und sind fail-safe: Unbekanntes, Fehlendes und Widersprüchliches führt zur
strengeren Seite.

| Funktion | Entscheidet |
| --- | --- |
| `PushAllowed(req, report, implementerExecutionID)` | Push/Integration nur mit Review-Bericht, der `IntegrationRequest.CheckReview` besteht (gleicher Bericht, Auftrag, **Commit**, **Konzeptversion**, Urteil `FREIGEGEBEN`) und von einer **anderen** Ausführung stammt als die Implementierung. Fehlende Kennung der Implementierung oder des Reviews zählt als nicht unabhängig. Codes `review_missing`, `review_invalid`, `review_not_independent` |
| `ConceptApprover(assessment)` | Routine ohne zutreffendes Kriterium → `concept_reviewer` (Aktion `review`); strategisch, nicht eingestuft, unbekannt eingestuft oder Routine mit zutreffendem Kriterium → Nutzer (`ask_question`) |
| `ConceptApprovalValid(approval, assessment, currentVersion)` | Freigabe gilt nur für dieselbe, nicht leere Konzeptversion; der Konzeptprüfer gibt nur Routinekonzepte frei, der Nutzer jedes, ebenfalls nur für die genannte Version |
| `RepairStep(round)` / `MaxRepairRounds()` | Runde 1 bis `max_rounds` (Standard 3): Implementierung mit den Befunden und danach frisches Review; darüber sichtbarer Halt mit `ask_question` |
| `NextSteps(role, result)` / `NextStep` | zulässige Folgeschritte beziehungsweise Standardschritt; unbekannte Rolle oder unbekanntes Ergebnis ist `invalid_request` |
| `FeatureComplete(evidence)` / `MilestoneComplete(evidence)` | Abschluss nur mit Kriterien erfüllt, bestätigtem Merge, Review-, Test- und CI-Nachweis sowie nachgezogenem Aufgaben- und Wissensstand (Meilenstein zusätzlich Teilziele und Abnahme). Ergebnis `Complete`, `State` und `Missing`. Offener PR → `waiting_integration`, nie `completed`. Im PR-Modus zählt nur ein menschlicher Merge; ein unbekannter Modus gilt als PR |
| `IntegrationModeAllowed(ordered, requested)` | Auto nur, wenn der Auftrag Auto ausdrücklich enthält; Aufweitung ist `platform_limit` |
| `QuestionRoute()` / `SilenceIsApproval()` | Fragenweg; Schweigen ist nie Freigabe |

Wichtig für den Orchestrator (M03): Diese Auswerter sind **Bausteine**. Sie werden an der Stelle aufgerufen, an der
Push, Integration, Abschluss oder Freigabe tatsächlich ausgeführt werden; im Skill-Katalog allein löst sich nichts davon
aus.

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
  keine Textsuche nach Formulierungen. Eingehalten wird er durch die Ausführung, ihre Berechtigungen (Policy-Profil,
  Worktree-Isolation) und das Review.
- Dass `PushAllowed`, `FeatureComplete` und die übrigen Auswerter **aufgerufen** werden: das ist Sache des
  Orchestrators (M03) und der Adapter (M05/M06). Bis dahin sind sie getestete Bausteine.
- Die fachliche Einstufung eines Konzepts (routine oder strategisch) und die Qualität eines Reviews: sie entstehen in
  den Ausführungen von Konzept-Agent und Prüfern.
- Die Echtheit der Ausführungskennungen, Hashes und Nachweise, die der Orchestrator übergibt.
- Ob der Supervisor aus den zulässigen Folgeschritten den fachlich richtigen wählt (CC-14).

## Ausführungskontext der Skills

- **In CCMAS** führt die Plattform alle Schreibaktionen (Asana, Vault, Git, PR) über die kontrollierten Adapter aus.
  Der Skill liefert Inhalt und Absicht und startet keine Agenten.
- **In einer Entwicklungssitzung ohne CCMAS-Dienst** erledigt die Hauptsitzung die Schreibvorgänge nach der
  Regeldatei und setzt die Stationen (Review vor Push, Freigabe strategischer Fälle) selbst um.

## Offene Punkte

- **`ReviewReport` ohne Commit bei Konzeptprüfungen:** Das Schema verlangt `commit` (Git-Commit). Für eine
  Konzeptprüfung gilt vorläufig der HEAD-Commit des geprüften Repositorys zum Prüfzeitpunkt. Ein eigener Berichtstyp
  für Konzeptprüfungen wäre eine Vertragsänderung (`contracts/`, `internal/domain`, CC-46).
- **Supervisor und Dokumentationsrolle:** Die Verträge von `ccmas-supervisor` und `ccmas-work-assessment` entstehen mit
  CC-14. Die Rolle `documentation` bekommt dann ihren Eintrittspunkt.
- **W33.2:** Anpassung der Texte von `ccmas-feature-parallel`, `ccmas-antworten-abholen` und
  `ccmas-meilenstein-abschluss` an die Verträge; danach `status: approved`.
- **Anbindung an Profile und Decision:** `AgentProfile.skill_ids` und `Decision.selection.skill_ids` sowie die
  Voraussetzungsprüfung vor einer Ausführung gehören zum Orchestrator (CC-46, M03).
