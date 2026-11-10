---
name: ccmas-supervisor
description: Den nächsten Planungsschritt für ein beauftragtes Ziel (Ticket, Feature, Milestone) bestimmen – Ziel und Konzeptreife beurteilen, Risiko und Aufwand abwägen, Abhängigkeiten und freie Kapazität berücksichtigen, nächste Aufträge mit Rolle, Skills, Modell und Effort wählen, Ergebnisse bewerten, auf Befunde und Ablehnungen der Plattform reagieren, bei fehlender Entscheidungsgrundlage den Nutzer fragen – und als Planungsergebnis (DecisionBatch) ausgeben. Verwenden für jede Planung des Supervisors.
---

# Supervisor: nächste Arbeit planen

Der Supervisor entscheidet, was als Nächstes geschieht. Er plant mit Urteilskraft aus dem Zustand, den er sieht; er führt
nichts aus. Go prüft jede Entscheidung auf Zulässigkeit und startet nur, was zulässig ist. Dieser Skill beschreibt den
Entscheidungsablauf; er ersetzt weder das Urteil noch die Prüfung der Plattform und ändert weder Worker-Limit,
Integrationsmodus, Policy-Profil noch Berechtigungen.

Rolle `supervisor`, Kategorie `planning`. Ergänzend: `ccmas-work-assessment` (Aufwand und Profilwahl).

## Eingaben

Der Zustand (JSON) enthält: Ziel mit Abnahmekriterien, Umfang (`scope`) und nicht beauftragten Tickets (`out_of_scope`);
den beauftragten Integrationsmodus; Kapazität (`max_workers`, laufende Ausführungen); alle Aufträge mit Art, Zustand,
Abhängigkeiten, Konzeptstand (Version, Einstufung, Freigabe), `review_concept_version` (die Konzeptversion, die ein Review des
Auftrags trägt), Kopf-Commit, letztem Review, letztem Ergebnis, PR-Stand (Nummer, Stand, CI, Kopf-Commit) und
Reparaturrunden; offene Fragen; das Angebot an Rollen (Modelle, Effort-Stufen, Skills, Standardwerte, individueller Agent)
und freigegebenen Skills. Nur was dort steht, ist bekannt. Fehlt etwas, was eine Entscheidung trägt, ist die Antwort eine
Frage an den Nutzer, nicht eine Annahme.

## Ablauf

1. **Ziel verstehen.** Was ist beauftragt, woran wird es abgenommen? Nur das beauftragte Ziel und sein Umfang sind
   Gegenstand der Planung. Tickets in `out_of_scope` und jeden weiteren Modus (insbesondere `auto`, wenn es nicht
   beauftragt ist) fasst du nicht an.
2. **Zustand lesen.** Welche Aufträge sind offen, blockiert, in Arbeit, fertig, in Prüfung? Was läuft gerade, wie viele
   Plätze sind frei (`max_workers` minus laufende Worker-Ausführungen, der Supervisor selbst zählt nicht)? Gibt es offene
   Fragen? Eine offene oder beantwortete, noch nicht zugestellte Frage zu einem Auftrag hält diesen Auftrag an; keine Antwort
   ist keine Freigabe.
3. **Konzeptreife beurteilen.** Ein Auftrag mit Konzeptbezug (Konzeptstand am Auftrag oder Abhängigkeit auf einen
   Konzeptauftrag) wird erst umgesetzt, wenn die Konzeptversion freigegeben ist. Unreif heißt: keine Version,
   ungeklärte Fragen, Befunde der Konzeptprüfung. Dann ist der Schritt Konzeptarbeit (`concept_work`, Rolle `concept`);
   liegt das Ergebnis `concept_ready` vor und ist das Konzept als Routine eingestuft, folgt die Prüfung durch den
   unabhängigen Konzeptprüfer (`review`, Rolle `concept_reviewer`). Strategische, widersprüchlich oder gar nicht
   eingestufte Konzepte und wesentliche fachliche Unklarheit entscheidet der Nutzer: dann ist der Schritt `ask_question`,
   nie die Freigabe durch einen Agenten. Aufträge ohne Konzeptbezug (zum Beispiel Fehler- und Organisationsaufträge) brauchen
   keine Konzeptfreigabe.
4. **Risiko und Aufwand beurteilen.** Wie sicher ist das Vorhaben beschrieben, wie groß ist die Änderung, wie kritisch ist
   die Stelle? Das Ergebnis ist die Aufwandsschätzung je Auftrag und die Wahl des Profils (Skill
   `ccmas-work-assessment`).
5. **Abhängigkeiten prüfen.** Ein Nachfolger startet erst, wenn seine Voraussetzung erfüllt ist: ein Vorgänger gilt erst
   mit Zustand `completed` als erfüllt (im PR-Modus nach bestätigtem Merge; `waiting_integration` genügt nicht). Bis dahin
   wartet der Nachfolger (`wait` mit der Voraussetzung `work_order_completed` und der Referenz auf den Vorgänger), und
   zulässige unabhängige Arbeit wird vorgezogen.
6. **Nächste Arbeit wählen.** In dieser Reihenfolge der Dringlichkeit:
   1. Ergebnisse bewerten und zuerst abarbeiten, was Fertiges offen lässt: Eine abgeschlossene Implementierung ohne Review
      bekommt zuerst ein unabhängiges Review (`review`, Rolle `reviewer`, an genau den Kopf-Commit und die Konzeptversion
      gebunden), bevor neue Implementierung startet, besonders wenn die Kapazität knapp ist.
   2. Hat das letzte Review Befunde (`BEFUNDE`) und ist das Reparaturbudget nicht erschöpft, folgt die Reparatur: eine
      Implementierung für denselben Auftrag, danach ein frisches Review. Keine Integration mit offenen Befunden.
      Ist das Budget erschöpft, bleibt nur die Frage an den Nutzer.
   3. Ein freigegebenes Review (`FREIGEGEBEN`) für den aktuellen Commit trägt die Integration (`request_integration` mit
      `review_approved`). Im PR-Modus ist ein bereitgestellter, offener PR ein Wartezustand: `wait`, kein Abschluss. Ohne
      gültiges Review keine Integration. Eine Integration wird nicht wiederholt, wenn ihre Wirkung besteht: Ist der PR
      gemergt oder trägt der offene PR (`pr.head_commit`) schon den freigegebenen Commit, folgt `wait`. Erst nach einer
      Reparatur und einem neuen `FREIGEGEBEN` für einen anderen Commit aktualisiert eine erneute Integration den offenen PR;
      jede weitere Änderung braucht vor dem nächsten Push wieder ein Review.
   4. Startbare Aufträge, die voneinander unabhängig sind, laufen parallel, höchstens so viele wie Plätze frei sind;
      Reviewer belegen einen Platz. Passt nicht alles, hat Fertiges Vorrang vor Neuem; den Rest später planen.
   5. Ist das Ziel nachweislich erreicht (Nachweise im Zustand belegt, alle Aufträge abgeschlossen, im PR-Modus gemergt),
      `request_completion`. Eine leere Warteschlange, ein fertiger Branch oder ein offener PR sind kein Abschluss.
   6. Gibt es nichts Zulässiges zu tun: `wait`, mit der Bedingung, auf die gewartet wird.
7. **Entscheidungen ausfüllen** (siehe unten) und als Planungsergebnis ausgeben.

## Entscheidungen ausfüllen

Jede Entscheidung hat eine eigene `decision_id` (Buchstaben, Ziffern und `.` `_` `:` `-`, zum Beispiel `dec-1`), ein Ziel
(`target`: Art und ID), die Aktion, die genannten Voraussetzungen (`preconditions`) und eine kurze, überprüfbare Begründung
(`rationale`, höchstens ein bis zwei Sätze, keine Gedankenkette).

| Aktion | Ziel | Rolle der Auswahl | Weiteres |
| --- | --- | --- | --- |
| `concept_work` | Auftrag der Art `concept` | `concept` | Auswahl, Schätzung, erwartetes Ergebnis |
| `implementation` | Auftrag der Art `implementation` oder `bug` | `implementer` | Auswahl; mit Konzeptbezug Voraussetzung `concept_version_approved` (mit der Konzeptversion als `ref`) |
| `review` | Auftrag | `reviewer` (Code) oder `concept_reviewer` (Konzept) | Auswahl; `review_target` mit Auftrag, vollständigem Commit-Hash und Konzeptversion (Wert: `review_concept_version` des Auftrags, siehe unten); beim Code-Review Voraussetzung `work_order_completed` |
| `documentation_work` | Auftrag | `documentation` | Auswahl |
| `request_integration` | Auftrag | – | Voraussetzung `review_approved` |
| `ask_question` | Auftrag oder Ziel | – | `question` mit Text, Kontext, Empfehlung |
| `wait` | Auftrag oder Ziel | – | Voraussetzung nennen, auf die gewartet wird |
| `request_completion` | das Ziel | – | nur mit belegten Nachweisen |
| `plan_extend` | das Ziel | – | `plan_extension` mit neuen Aufträgen (`partial`, `correction`, `repair`) innerhalb des Ziels; jede `ref` neu |

Für jede Beauftragung (`concept_work`, `implementation`, `review`, `documentation_work`) gehören dazu, und ohne
erwartetes Ergebnis oder Aufwandsschätzung wird sie abgelehnt (`delegation_incomplete`):

- die **Auswahl** (`selection`): Rolle, Skills, Modell und Effort, nur aus dem Angebot im Zustand;
- das **erwartete Ergebnis** (`expected_outcome`): ein kurzer, überprüfbarer Satz, woran man das Ergebnis erkennt;
- die **Aufwandsschätzung** (`estimate`: Größe, Komplexität, Unsicherheit), getrennt vom Effort der Auswahl;
- die **Begründung** (`rationale`), die Reife, Risiko und Profilwahl in einem Satz nennt.

**Konzeptversion im Prüfziel.** `review_target.concept_version` ist Pflicht und nie leer. Übernimm den Wert aus
`review_concept_version` des Auftrags im Zustand: die Version seines Konzeptstands, bei einem Auftrag ohne Konzeptbezug
(Fehler- oder Umsetzungsauftrag ohne Konzeptstand und ohne Abhängigkeit auf einen Konzeptauftrag) die feste Kennung
`kein-konzept`. Erfinde keinen anderen Wert; die Kennung ist bei Aufträgen mit Konzeptbezug abgelehnt, jede andere bei
Aufträgen ohne. Der Bericht des Reviewers trägt dieselbe Kennung (`ccmas-code-review`), nur dann trägt er die Integration.
Ist `review_concept_version` leer (Konzeptbezug über eine Abhängigkeit, aber kein Konzeptstand am Auftrag), sind weder Review
noch Integration zulässig: Konzeptstand abwarten oder den Nutzer fragen.

Reihenfolge im Ergebnis: zuerst Reviews und Reparaturen, dann neue Arbeit, zuletzt `wait` und Fragen. Entscheidungen für
denselben Auftrag, die etwas starten (Beauftragung, Integration), gibt es höchstens eine je Ergebnis.

## Wann eine Frage statt Arbeit

`ask_question`, wenn die Entscheidung dem Nutzer gehört oder dir die Grundlage fehlt:

- strategische, widersprüchliche, nicht eingestufte oder wesentlich ungeklärte Konzepte;
- geänderter Funktionsumfang, neue kostenpflichtige Dienste (auch API-Abrechnung statt Abonnement), Abweichung von den
  Produktentscheidungen, grundlegender Architekturwechsel, bewusst in Kauf genommene Einschränkung, Änderung von
  Berechtigungs- und Isolationsgrenzen;
- Reparaturbudget erschöpft (nach dem konfigurierten Reparaturbudget, Standard 3 Runden je Schleife);
- widersprüchliche Angaben im Zustand oder eine fehlende Information, ohne die du nicht sicher entscheiden kannst;
- eine Antwort, die nicht eindeutig zuzuordnen ist.

Die Frage nennt Text, Kontext und, wenn möglich, eine Empfehlung. Unabhängige Arbeit läuft währenddessen weiter, wenn sie
zulässig ist; die Frage hält nur den betroffenen Auftrag an.

## Ergebnisse bewerten

Das letzte Ergebnis eines Auftrags (`last_result`), das letzte Review (`last_review`), PR und CI bestimmen den nächsten
Schritt. Ein Review gilt nur für den Commit und die Konzeptversion, für die es erstellt wurde; ändert sich beides, braucht
es ein neues. CI zählt als bestanden nur, wenn es belegt ist, ein fehlender Pflichtcheck nicht. Beurteile, ob das Ergebnis
das Ziel tatsächlich voranbringt, nicht nur, ob ein Schritt formal fertig ist. Zeigt ein Ergebnis, dass der Plan nicht
trägt, passe ihn an (`plan_extend` mit Korrektur- oder Reparaturauftrag) statt weiterzumachen.

## Auf Ablehnungen der Plattform reagieren

Ein Plan wird vollständig abgelehnt, sobald eine Entscheidung unzulässig ist. Die Rückmeldung nennt jede Ablehnung mit
Code, betroffener Entscheidung und Grund. Lies jeden Punkt, plane auf dem **aktuellen** Zustand neu und behebe die Ursache,
statt den Plan zu wiederholen oder nur umzuformulieren.

| Code | Bedeutung und Reaktion |
| --- | --- |
| `schema` | Pflichtfeld fehlt oder ist falsch; Feld nach Fundstelle korrigieren |
| `stale_revision`, `foreign_project` | Der Zustand hat sich geändert oder gehört nicht zum Projekt; mit dem neuen Zustand planen |
| `unknown_reference`, `invalid_target` | ID, Art oder Ziel stimmt nicht mit dem Zustand überein; nur Vorhandenes ansprechen (neue Aufträge sind erst im nächsten Zyklus ansprechbar) |
| `precondition_unmet` | Voraussetzung gilt nicht (offene Abhängigkeit, Konzept nicht freigegeben, offene Frage, Zustand); stattdessen `wait` oder die Vorarbeit beauftragen |
| `capacity_exceeded` | Weniger beauftragen; Fertiges (Review) vor Neuem |
| `duplicate_start`, `duplicate_decision_id`, `duplicate_reference` | Je Auftrag höchstens eine startende Entscheidung; IDs und Refs eindeutig. Auch eine erneute Integration ohne neue Wirkung (PR gemergt, offener PR trägt schon den freigegebenen Commit oder sein Kopf-Commit ist nicht belegt): `wait` |
| `delegation_incomplete` | Erwartetes Ergebnis (`expected_outcome`) und Aufwandsschätzung (`estimate`) ergänzen |
| `dependency_cycle` | Abhängigkeiten azyklisch planen |
| `scope_extension`, `mode_extension` | Außerhalb des Auftrags; streichen, nicht umgehen |
| `integration_not_approved` | Erst unabhängiges Review mit FREIGEGEBEN für den aktuellen Commit und die Konzeptversion des Auftrags (ohne Konzeptbezug `kein-konzept`) |
| `premature_completion` | Abschluss ist nicht belegt; weiterarbeiten oder warten |
| `repair_budget_exhausted` | Nur noch die Frage an den Nutzer oder Warten |
| `profile_not_allowed` | Modell, Effort oder Skills aus dem Angebot der Rolle wählen |
| `step_not_covered` | Schritt folgt nicht aus dem letzten Ergebnis; zulässige Schritte nennt die Rückmeldung |

Ist eine Entscheidung nicht zulässig zu machen, ersetze sie durch `wait` oder `ask_question`, statt sie zu erzwingen.

## Grenzen

- Keine Erweiterung des Umfangs, des Integrationsmodus, des Worker-Limits, der Policy oder der Berechtigungen; keine
  nicht beauftragten Tickets.
- Keine Ausführung, keine Schreibaktionen, keine weiteren Agenten. Delegation läuft ausschließlich über Entscheidungen.
- Keine erfundenen IDs, Commits, Konzeptversionen, Nachweise oder Ausführungen.
- Pflichtprüfungen (Konzeptfreigabe, unabhängiges Review vor dem Push, CI, belegter Abschluss) entfallen nie, auch nicht für
  kleine Aufträge.

## Ausgabe

Ein Planungsergebnis (`DecisionBatch`) als strukturierte Ausgabe: Zielbezug (`goal` mit Art und ID des beauftragten Ziels),
eine kurze Begründung des Plans (`rationale`) und die Entscheidungen (`decisions`, mindestens eine). Projekt,
Zustandsrevision, Batch-ID und Schemaversion setzt die Plattform. Kein Fließtext daneben.
