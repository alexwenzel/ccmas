---
name: ccmas-work-assessment
description: Arbeit bewerten und das passende Profil wählen – Aufwandsschätzung (Größe, Komplexität, Unsicherheit) getrennt vom Modell-Effort festhalten, Modell und Effort nach Qualität wählen (stärker bei unsicherer Architektur oder kritischem Review, schlanker bei gut beschriebener Arbeit), Pflichtprüfungen nie streichen. Verwenden bei jeder Beauftragung durch den Supervisor.
---

# Arbeit bewerten und Profil wählen

Zwei Dinge werden getrennt bestimmt, getrennt gespeichert und getrennt angezeigt:

- die **Aufwandsschätzung** (`estimate`) beschreibt die Arbeit: erwartete Größe, Komplexität und Unsicherheit;
- der **Modell-Effort** (`selection.effort`) beschreibt die Denkintensität der Modellausführung.

Beide sind Angaben des Supervisors. Eine Schätzung ist keine garantierte Laufzeit; tatsächliche Dauer und Ergebnisse (letztes
Ergebnis, Befunde, Reparaturrunden) fließen in spätere Bewertungen ein. Rolle `supervisor`, Kategorie `planning`; der Ablauf
der Planung steht in `ccmas-supervisor`.

## Aufwandsschätzung

| Feld | Werte | Bedeutung |
| --- | --- | --- |
| `size` | `xs` `s` `m` `l` `xl` | erwarteter Umfang der Arbeit: `xs` Handgriff (eine kleine Änderung), `s` ein klar umrissener Teil einer Datei oder eines Dokuments, `m` mehrere zusammenhängende Dateien, `l` mehrere Komponenten, `xl` quer durch das System |
| `complexity` | `low` `medium` `high` | wie schwierig die Arbeit in der Sache ist: viele Wechselwirkungen, Randfälle, Nebenläufigkeit, Sicherheitsgrenzen |
| `uncertainty` | `low` `medium` `high` | wie unsicher der Weg ist: unklare Anforderungen, unbekannter Bestand, fehlende Belege, frühere Fehlversuche oder Befunde |

Grundlage sind Akzeptanzkriterien, Konzeptstand, Abhängigkeiten, bisherige Ergebnisse und Befunde des Auftrags im Zustand.
Keine Zeitangaben. `complexity` und `uncertainty` verwenden dieselben drei Wörter wie die Effort-Stufen `low`, `medium` und
`high`; sie sind trotzdem etwas anderes. Übernimm nie den Effort als Schätzung oder die Schätzung als Effort.

Hohe Unsicherheit ist ein Grund für Konzeptarbeit oder eine Frage vor der Umsetzung, nicht nur für ein stärkeres Profil.

## Profil wählen

Qualität ist die leitende Größe. Wähle Modell und Effort aus dem Angebot der Rolle im Zustand (`roles[]`: erlaubte Modelle
und Effort-Stufen, Standardwerte) und die Skills aus den für die Rolle erlaubten.

- **Stärkeres Profil** (stärkeres Modell, höherer Effort) für: unsichere Architekturfragen, Konzepte mit hoher Unsicherheit,
  kritische Reviews (Sicherheit, Datenverlust, Plattformgrenzen, Integration), Arbeit nach wiederholten Befunden.
- **Schlankeres Profil** für gut beschriebene, klar umrissene Arbeit mit niedriger Komplexität und Unsicherheit und für
  mechanische Teile (Dokumentation nach vorhandenem Stand).
- **Im Zweifel** der Standard der Rolle. Ein Profil unter dem Standard braucht einen Grund in der Begründung.

Pflichtprüfungen entfallen durch ein schlankeres Profil nie: Konzeptfreigabe, unabhängiges Review vor dem Push, CI und belegter
Abschluss bleiben, wie sie sind. Ein Review ist immer eine frische Ausführung der Rolle `reviewer`, unabhängig vom gewählten
Profil.

## Vorrang und Sichtbarkeit

- Ein individueller Agent der Rolle (`roles[].agent_id`) und ausdrückliche Auftragswerte haben Vorrang vor deiner Wahl, je Feld
  einzeln. Du darfst Modell und Effort trotzdem angeben; maßgeblich ist das wirksame Profil, das die Plattform auflöst.
- Unterstützt das Modell eine Effort-Stufe nicht, ist die Kombination nur zulässig, wenn die Konfiguration die Reduktion
  ausdrücklich genehmigt hat; sie wird dann sichtbar festgehalten. Wähle nach Möglichkeit eine Stufe, die das Modell
  unterstützt.
- Wähle nur Modelle und Stufen, die im Angebot der Rolle stehen. Die Plattform lehnt alles andere ab (`profile_not_allowed`).

## In die Entscheidung übernehmen

Für jede Beauftragung:

1. `estimate`: Größe, Komplexität, Unsicherheit.
2. `selection`: Rolle, Skills, Modell, Effort.
3. `rationale`: ein Satz, der die Profilwahl aus Schätzung und Risiko ableitet (zum Beispiel „hohe Unsicherheit, kritische
   Plattformgrenze: stärkeres Profil“ oder „gut beschrieben, niedrige Komplexität: Standardprofil“).
4. `expected_outcome`: woran das Ergebnis überprüfbar erkannt wird.
