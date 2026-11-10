---
name: ccmas-concept-ticket
description: Ein Concept-Ticket (Analyse, Konzept, Datenmodell, Architektur- oder Technikentscheidung) von der Klärung bis zur Freigabe bearbeiten – Ticket und Vault-Kontext lesen, offene Punkte gebündelt mit Empfehlung klären, gegen echte Daten prüfen, Obsidian-Seite schreiben, verwandte Seiten konsistent halten, Selbstreview, Folge-Tickets anlegen, zur Abnahme vorlegen. Verwenden, wenn der Nutzer ein Konzept-, Recherche- oder Planungsticket angehen will („lass uns SC-8 machen“, „konzipier das Datenmodell“, „bereite M16 vor“), auch wenn das Wort „Concept“ nicht fällt.
---

# Concept-Ticket bearbeiten

Concept-Tickets erzeugen kein Code-Ergebnis, sondern eine Obsidian-Seite, auf die spätere Implementierungstickets
bauen. Der Wert liegt darin, dass Entscheidungen geklärt, belegt und so präzise aufgeschrieben sind, dass ein
Code-Agent ohne Rückfragen umsetzen kann. Fehler hier vervielfachen sich in jedem Folgeticket.

## Projektkontext zuerst

Aus der Regeldatei lesen: Ablage und Property-Schema für Obsidian, Done-Kriterien für Concept-Tickets (oft: Freigabe
durch den Projektinhaber bei strategischen Entscheidungen), Aufgaben-an-Projektinhaber-Regeln, Grenzen (was vorher
abzustimmen ist). Asana nach Skill `ccmas-asana-ticket`, Obsidian nach Skill `ccmas-obsidian-seite`.

## 1. Verstehen

- Ticket mit Beschreibung, Kommentaren und Abhängigkeiten lesen; Ticket nach `In Progress`.
- Verlinkte und thematisch nahe Obsidian-Seiten lesen: Gesamtkonzept, betroffene Fachseiten, Entscheidungsliste,
  Projektplan. Nur die relevanten Abschnitte, aber vollständig genug, um Widersprüche zu sehen.
- Bestand und Daten heranziehen, wo vorhanden (Analysen des Altsystems, Exporte, Screenshots). Aussagen über den
  Bestand mit Fundstelle belegen.
- Festhalten, was schon entschieden ist. Abgelehnte Optionen nicht erneut vorschlagen.

## 2. Klären

Offene Punkte sammeln und **gebündelt** vorlegen, nicht einzeln über viele Nachrichten verteilt. Jede Frage mit
Optionen, Folgen und einer Empfehlung; so kann der Projektinhaber schnell zustimmen oder abweichen.

- **Interaktiv:** `AskUserQuestion` (bis 4 Fragen je Aufruf, Empfehlung als erste Option mit „(Recommended)“).
  Bei mehr Fragen eine nummerierte Liste im Chat, damit er in einer Nachricht antworten kann.
- **Asynchron** (Projektinhaber nicht da): je Frage eine `Entscheidung:`-Unteraufgabe (Skill `ccmas-asana-ticket`), an
  unabhängigen Teilen weiterarbeiten.
- Nur fragen, was den Lösungsweg tatsächlich ändert. Technische Details mit klarem Standard selbst entscheiden und
  in der Seite begründen.

## 3. Prüfen

Antworten und eigene Annahmen gegen die echten Daten prüfen, bevor geschrieben wird (z. B. Werte in einem Export,
Häufigkeiten, Sonderfälle). Ergibt sich ein Widerspruch, ihn vorlegen statt still zu entscheiden.

## 4. Schreiben

- Ergebnis auf eine Obsidian-Seite: bestehende Seite erweitern oder neue Seite am richtigen Ort, in die Index-Seite
  eingetragen.
- Aufbau, der für Umsetzer taugt: Ziel und Abgrenzung, Entscheidungen mit Begründung und Konsequenzen,
  Spezifikation (Struktur, Felder, Regeln, Abläufe), Grenzfälle, Tests und Abnahme, offene Punkte.
- Ist-Zustand, Plan und offene Entscheidung klar trennen. Status `review`, bis der Projektinhaber freigibt.
- **Verwandte Seiten** im selben Arbeitsgang konsistent machen: nach den zentralen Begriffen suchen (`grep -rn`) und
  Aussagen, die jetzt nicht mehr stimmen, anpassen oder auf die neue Seite verweisen. Jede Aussage steht nur an
  einer Stelle.

## 5. Selbstreview

Die geschriebene Seite mit frischem Blick erneut lesen, gezielt auf:

- Vollständigkeit nach Umbauten: Wurde beim Überarbeiten etwas aus Diagrammen, Tabellen oder Listen entfernt, das
  noch gilt? Elemente in Diagrammen gegen die Spezifikation abzählen.
- Widersprüche zu anderen Seiten und zu getroffenen Entscheidungen.
- Umsetzbarkeit: Könnte ein Code-Agent jedes Folgeticket ohne Rückfrage beginnen?
- Links: Link-Prüfung aus Skill `ccmas-obsidian-seite`.

Gefundenes sofort beheben und im Bericht nennen.

## 6. Folge-Tickets

Aus der Seite die Umsetzungsarbeit ableiten: kleine, prüfbare Tickets mit Typ, Meilenstein, Assignee, Akzeptanz-
kriterien und Verweis auf den Abschnitt der Seite; Abhängigkeiten untereinander setzen. Absehbare, aber nicht
beauftragte Arbeit als zurückgestelltes Ticket erfassen statt nebenbei umzusetzen.

## 7. Abschließen

- Kommentar im Ticket nach Phasenabschluss-Muster: Ergebnis, Entscheidungen, Seite und Abschnitte, Folge-Tickets,
  offene Punkte, nächster Schritt.
- **Einstufen:** Ist das Konzept **routine** oder **strategisch**? Strategisch ist es, wenn eines der Kriterien aus
  `rules/roles.yaml` (`concept_approval.strategic_criteria`, nach Regel 4 und 7.5) zutrifft oder wesentliche fachliche
  Unklarheit bleibt: geänderter Funktionsumfang, neue kostenpflichtige Dienste, Abweichung von festgelegten
  Produktentscheidungen, grundlegender Architekturwechsel, bewusst in Kauf genommene Einschränkung, Änderung an
  Berechtigungs- und Isolationsgrenzen. Lässt es sich nicht sicher einstufen, gilt es als strategisch. Einstufung und
  zutreffende Kriterien stehen im Kommentar und im Ergebnis.
- **Routinekonzept:** unabhängige Konzeptprüfung anfordern (Skill `ccmas-concept-review`, frische Ausführung). Die
  Freigabe gilt nur für die geprüfte Konzeptversion; abhängige Implementation-Tickets starten erst danach.
- **Strategisch oder nicht ausreichend geklärt:** `Freigabe:`-Unteraufgabe an den Projektinhaber, Ticket nach `Review`.
  Ohne Antwort gibt es keine Freigabe.
- Bericht im Chat: Ergebnis, geänderte Seiten mit Version, Tickets mit ID, offene Fragen, empfohlener nächster
  Schritt. Danach kein neues Vorhaben beginnen.

## Ausführungskontext und Vertrag

Rolle `concept`, Kategorie `concept`; der maschinenlesbare Vertrag (Eingaben, Umfang, Ergebnisse, Folgeaufträge)
steht in `contract.yaml`, die Rollenregeln in `rules/roles.yaml`. Der Skill ändert weder die Regeln noch Worker-Limit,
Integrationsmodus, Policy-Profil oder Berechtigungen und startet keine weiteren Agenten.

- **In CCMAS** liefert der Skill Inhalt und Absicht; Asana-, Vault- und Git-Schreibvorgänge führt die Plattform über
  ihre Adapter aus.
- **In einer Entwicklungssitzung ohne CCMAS-Dienst** erledigt die Hauptsitzung diese Schreibvorgänge nach der
  Regeldatei und legt strategische Fälle dem Projektinhaber vor.
