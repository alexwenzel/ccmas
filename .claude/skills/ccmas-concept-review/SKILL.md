---
name: ccmas-concept-review
description: Eine bestimmte Konzeptversion unabhängig prüfen – gegen Ziel, Abnahmekriterien, Regeln und frühere Entscheidungen –, als routine oder strategisch einstufen und entweder für genau diese Version freigeben, mit konkreten Befunden zurückgeben oder dem Nutzer mit Optionen und Empfehlung vorlegen. Verwenden, wenn ein Konzept-Agent ein Konzept als „ready“ meldet, bevor davon abhängige Implementierung startet, immer in einer frischen Ausführung, die das Konzept nicht geschrieben hat.
---

# Konzeptprüfung

Der Konzeptprüfer entscheidet, ob eine Konzeptversion tragfähig genug ist, dass daraus Arbeit abgeleitet werden darf.
Er schreibt das Konzept nicht um und gibt nur frei, was routinemäßig ist und vollständig geklärt. Alles
Strategische entscheidet der Nutzer.

Rolle `concept_reviewer`, Kategorie `review`. Der maschinenlesbare Vertrag steht in `contract.yaml`, Einstufung und
Freigabewege in `rules/roles.yaml` (Abschnitt `concept_approval`, Softwareordner). Dieser Skill beschreibt den Ablauf;
er ändert weder Regeln noch Worker-Limit, Integrationsmodus, Policy-Profil oder Berechtigungen.

## Ausführungskontext

- **In CCMAS** erhält die frische Ausführung den Prüfauftrag mit Konzeptversion und gibt ihr Ergebnis als
  Prüfbericht an den Supervisor. Alle Schreibaktionen (Asana, Vault, Ticketkommentar) führt die Plattform über ihre
  Adapter aus; dieser Skill schreibt nichts.
- **In einer Entwicklungssitzung ohne CCMAS-Dienst** erledigt die Hauptsitzung nach der Regeldatei die
  Schreibvorgänge und legt strategische Fälle dem Projektinhaber vor (Regel 7.5, `AskUserQuestion`).

## Eingaben

- Prüfauftrag mit Ziel und Abnahmekriterien,
- Konzeptversion (Seite und Version), die geprüft wird,
- maßgebliche Regeln, festgelegte Produktentscheidungen und frühere Entscheidungen,
- Ausführungskennung des Konzept-Agenten (für den Nachweis der Unabhängigkeit).

Ist die Konzeptversion nicht eindeutig bestimmbar, gibt es keine Freigabe: BEFUNDE mit Schwere `blocker`.

## Ablauf

1. **Version festlegen.** Genau die angeforderte Version lesen, nicht eine neuere oder ältere. Die verlinkten
   Seiten, das Ticket und die Entscheidungsliste dazu lesen.
2. **Gegen das Ziel prüfen.** Sind Ziel, Umfang und Abnahmekriterien vollständig und widerspruchsfrei umgesetzt?
   Ist Ist-Zustand von Plan und offener Entscheidung getrennt? Sind Aussagen über den Bestand belegt?
3. **Gegen die Regeln prüfen.** Festgelegte Produktentscheidungen, Berechtigungs- und Isolationsgrenzen,
   Entscheidungen aus früheren Konzepten. Abgelehnte Optionen kehren nicht ungeprüft zurück.
4. **Umsetzbarkeit prüfen.** Könnte ein Implementierer jedes abgeleitete Ticket ohne Rückfrage beginnen? Sind
   Prüfwege und Grenzfälle benannt?
5. **Einstufen.** Siehe unten. Zweifel gelten als strategisch.
6. **Ergebnis bilden.**

## Einstufung

Das Konzept ist **strategisch**, wenn mindestens eines der Kriterien aus `concept_approval.strategic_criteria`
zutrifft: geänderter Funktionsumfang, neue kostenpflichtige Dienste (auch API-Abrechnung statt Abonnement),
Abweichung von festgelegten Produktentscheidungen, grundlegender Architekturwechsel, bewusst in Kauf genommene
Einschränkung, Änderung an Berechtigungs- und Isolationsgrenzen, wesentliche fachliche Unklarheit. Es ist
**routine**, wenn keines zutrifft und alles ausreichend geklärt ist. Lässt es sich nicht sicher einstufen, geht es
an den Nutzer (fail-safe).

## Ergebnis

| Einstufung | Ergebnis | Folge |
| --- | --- | --- |
| routine | **FREIGEGEBEN**: alle Kriterien belegt erfüllt, keine offenen Befunde | Freigabe gilt nur für genau diese Konzeptversion |
| routine | **BEFUNDE**: Lücken, Widersprüche oder Regelverstöße, je mit Ort und erwarteter Korrektur | Zurück an den Konzept-Agenten |
| strategisch oder ungeklärt | **keine Freigabe**, Ergebnis `needs_user_decision` | Empfehlung an den Nutzer: Frage mit Optionen, Folgen und einer Empfehlung |

Der Bericht nutzt das Format `ReviewReport`: `verdict`, `findings` (mit `severity` blocker/major/minor und
`location`) und `concept_version`. Der Bericht bezieht sich auf die **Konzeptversion**. Das Schema verlangt dort
zusätzlich einen `commit`; er ist nur der **Bezugsstand des Code-Repositorys** (Stand `develop` zum Prüfzeitpunkt,
Hilfslösung) und **keine Freigabe von Code**: Ein Konzeptprüfbericht trägt nie einen Push, die Plattform akzeptiert für
Code nur Berichte der Rolle `reviewer`. Eine eigene Berichtsart oder ein optionaler `commit` ist ein offener Punkt
(Vertragsänderung, Folgeticket). Bei strategischen oder ungeklärten Konzepten steht im Bericht die
Einstufung mit den zutreffenden Kriterien und die Frage an den Nutzer; ein Urteil FREIGEGEBEN gibt es dann nicht.

```text
Auftrag: <Kennung> · Konzeptversion: <version>
Einstufung: routine | strategisch | nicht einstufbar (mit zutreffenden Kriterien)
Urteil: FREIGEGEBEN | BEFUNDE | keine Freigabe (Nutzerentscheidung nötig)
Kriterien: <Kriterium> → erfüllt/nicht erfüllt, Beleg (je Zeile)
Befunde (nur bei BEFUNDE): <Seite/Abschnitt> – <Schwere> – Problem – erwartete Korrektur
Empfehlung an den Nutzer (nur bei strategisch): Frage – Optionen mit Folgen – Empfehlung
```

## Grenzen

- Nur lesen und prüfen. Das Konzept, Tickets und Seiten nicht ändern, nichts freigeben, was strategisch ist, und
  nicht an Stelle des Nutzers entscheiden.
- Eine Freigabe ersetzt weder das Code-Review noch eine Entscheidung des Nutzers. Ändert sich die Konzeptversion
  wesentlich, gilt die Freigabe nicht mehr; betroffene Aufträge und Prüfbelege werden neu bewertet.
- Keine weiteren Agenten starten, keine Asana- oder Vault-Schreibvorgänge.

## Folgeaufträge

Nach dem Bericht beurteilt der Supervisor den nächsten Schritt; der Vertrag nennt die zulässigen:

- **FREIGEGEBEN** → Plan ergänzen (`plan_extend`, Supervisor) oder Implementierung der abhängigen Arbeit
  (`implementation`), jeweils mit der Voraussetzung `concept_version_approved`.
- **BEFUNDE** → Konzeptarbeit (`concept_work`) mit den Befunden, danach neue Prüfung durch eine frische Ausführung.
  Diese Schleife hat dasselbe Reparaturbudget wie die Code-Schleife (`repair_loop.max_rounds`, Standard 3 Runden);
  danach folgt ein sichtbarer Halt mit einer Frage an den Nutzer (`ask_question`) statt einer weiteren Runde.
- **Nutzerentscheidung nötig** → Rückfrage an den Nutzer (`ask_question`); ohne Antwort gibt es keine Freigabe.
