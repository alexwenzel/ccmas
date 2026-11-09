---
name: ccmas-neue-aufgabe-nehmen
description: Ist-Stand erfassen (Asana, Vault, Git), nächste Aufgabe bestimmen, offene Punkte mit dem Projektinhaber klären und mit den Projekt-Skills in Obsidian und Asana festhalten, bis die Umsetzung ohne Rückfrage beginnen kann. Für den geplanten Lauf und „was machen wir als Nächstes?“.
---

# Neue Aufgabe nehmen

Ein Vorbereitungslauf: Er schaut, wo das Projekt steht, bestimmt die nächste Aufgabe und sorgt dafür, dass sie
umsetzungsreif ist: Fragen geklärt, Konzept und Spezifikation in Obsidian, Ticket mit prüfbaren Akzeptanzkriterien.
Er setzt keinen Code um. Ergebnis ist ein Ticket, mit dem eine Umsetzungssitzung sofort beginnen kann, oder eine
klare Liste dessen, was der Projektinhaber dafür noch entscheiden oder liefern muss.

## Projektkontext zuerst

Aus der Regeldatei lesen: Projekt- und User-GIDs, Sections, Felder, Grenzen des Agenten (was vorher abzustimmen ist),
Done-Kriterien, Ablage und Properties in Obsidian, Termine der Meilensteine und die Vorlage des Startbefehls für
Umsetzungssitzungen. Asana-Schreibaktionen nach Skill
`ccmas-asana-ticket`, Obsidian nach Skill `ccmas-obsidian-seite`.

**Modus:** Interaktiv wird direkt gefragt. Unbeaufsichtigt (geplanter Lauf) wird nicht im Chat gefragt, sondern über
Aufgaben an den Projektinhaber. Je Lauf **eine** Aufgabe ausarbeiten.

## 1. Ist-Stand erfassen

Kompakt, nur was die Wahl beeinflusst:

- **Neue Antworten zuerst:** Gibt es abgehakte oder neu kommentierte Aufgaben an den Projektinhaber ohne Marker
  „Antwort übernommen“, zuerst Skill `ccmas-antworten-abholen` (Schritte 1–4). Antworten können Aufgaben entsperren oder
  eine angefangene Klärung abschließen.
- **Asana:** offene Tickets in `In Progress`, `Review`, `To Do`; offene Aufgaben an den Projektinhaber; der aktuelle
  Meilenstein (frühester offener laut Terminliste) mit Definition of Done, Tickets und Abhängigkeiten.
- **Vault:** Index-Seite, Projektplan (Meilensteine, Termine, Abhängigkeiten), die Seiten, auf die Kandidaten
  verweisen, offene Punkte und Entscheidungslisten.
- **Git:** offene PRs mit Status der Checks, aktive Branches, was auf dem Integrationsbranch zuletzt umgesetzt wurde
  (`gh pr list`, `gh api`, GitHub-Tools). Hält fest, was schon gebaut ist, damit nichts doppelt geplant wird.
- **Gerade in Arbeit:** Tickets in `In Progress` mit Aktivität in den letzten 2 Stunden (Kommentar, Commit, Push)
  bearbeitet gerade jemand; sie werden nicht angefasst.

## 2. Nächste Aufgabe bestimmen

In dieser Reihenfolge; die erste Aufgabe, an der dieser Lauf etwas bewegen kann, gewinnt (höchstens drei Kandidaten
prüfen):

1. Angefangene Klärung: Ticket, dessen Rückfragen jetzt beantwortet sind und dessen Konzept nachgezogen werden muss.
2. Ticket in `To Do` des aktuellen Meilensteins ohne Marker „Bereit zur Umsetzung“, Abhängigkeiten zuerst.
3. Entsperrtes Ticket in `Backlog` des aktuellen Meilensteins.
4. `Concept`-Ticket des aktuellen oder nächsten Meilensteins.
5. Meilenstein ohne (genug) Tickets: seine Vorbereitung, also Tickets aus der Definition of Done ableiten
   (Skill `ccmas-concept-ticket`, „bereite Mnn vor“).

**Nie wählen:** Label `Für Später`; Assignee Projektinhaber; Tickets in `Review`; Tickets, die gerade bearbeitet
werden; Tickets mit offenen Fragen an den Projektinhaber, solange keine neue Antwort vorliegt; Tickets mit Marker
„Bereit zur Umsetzung“ ohne Änderung seitdem.

## 3. Reife prüfen

Eine Umsetzungssitzung muss ohne Rückfrage beginnen können. Prüfen und Lücken notieren:

- Ziel und abgegrenzter Umfang in der Beschreibung; prüfbare Akzeptanzkriterien.
- Maßgebliche Obsidian-Seite verlinkt, vorhanden und für diesen Teil vollständig; keine offenen Punkte dort, die das
  Ticket betreffen; strategische Inhalte freigegeben, wo die Regeldatei das verlangt.
- Technisches Konzept vorhanden, wo die Umsetzung Entscheidungen braucht (Datenmodell, Schnittstellen, Abläufe,
  Grenzfälle, Tests).
- Abhängigkeiten erledigt; Zugänge und Daten vorhanden.
- Kein Punkt, der laut Regeldatei vorher abzustimmen ist (Funktionsumfang, kostenpflichtige Dienste, Architektur,
  irreversible Migrationen, SEO-relevante URLs).

Ist alles erfüllt: weiter mit Schritt 5.

## 4. Klären

Offene Punkte **gebündelt** vorlegen, jede Frage mit Optionen, Folgen und Empfehlung. Nur fragen, was den Lösungsweg
tatsächlich ändert; technische Details mit klarem Standard selbst entscheiden und in der Seite begründen.

- **Interaktiv:** `AskUserQuestion` (bis 4 Fragen je Aufruf, Empfehlung als erste Option mit „(Recommended)“). Bei
  mehr Fragen eine nummerierte Liste im Chat, damit er in einer Nachricht antworten kann. Antworten sofort festhalten
  (Schritt 5).
- **Unbeaufsichtigt:** je Frage eine `Entscheidung:`- oder `Zuarbeit:`-Unteraufgabe mit Label und Abhängigkeit (Skill
  `ccmas-asana-ticket`). Vorher prüfen, ob dieselbe Frage schon offen ist. Unabhängige Teile trotzdem ausarbeiten.
- Vor dem Fragen gegen Bestand und echte Daten prüfen, was sich belegen lässt. Abgelehnte Optionen nicht erneut
  vorschlagen.

## 5. Ausarbeiten und festhalten

Mit den vorhandenen Skills, nichts nur im Chat:

- **Konzept fehlt oder ist dünn:** Skill `ccmas-concept-ticket` (bestehendes Concept-Ticket oder neu anlegen; das
  Umsetzungsticket hängt davon ab). Ergebnis ist eine Obsidian-Seite, die ein Code-Agent ohne Rückfrage umsetzen kann.
- **Spezifikation ergänzen:** Skill `ccmas-obsidian-seite` (bestehende Seite erweitern, Properties, Links, Index-Seite).
  Ist-Zustand, Plan und offene Entscheidung getrennt halten.
- **Ticket schärfen:** Skill `ccmas-asana-ticket`: Beschreibung mit Ziel, Umfang, Akzeptanzkriterien, Link auf den Abschnitt
  der Seite, Abhängigkeiten; Folge-Tickets klein und prüfbar anlegen; absehbare, nicht beauftragte Arbeit als
  `Für Später`.
- **Entscheidungen** dort eintragen, wo sie dauerhaft gelten (Konzeptseite, Entscheidungsliste, Projektplan).
- **Abschluss im Ticket:**
  - reif: Kommentar `Bereit zur Umsetzung: <Seite/Abschnitt>, <was geklärt wurde>`; Ticket aus `Backlog` nach `To Do`.
  - nicht reif: Kommentar mit Stand, offenen Unteraufgaben und nächstem Schritt.
  - Concept-Ticket fertig: Abschluss nach Skill `ccmas-concept-ticket` (Freigabe bzw. Done).

## 6. Bericht

Knapp, höchstens etwa zwölf Zeilen, mit Links. Im geplanten Lauf ist er die letzte Nachricht und erreicht den
Projektinhaber als Benachrichtigung.

- **Stand:** ein bis zwei Sätze (Meilenstein, was zuletzt fertig wurde, was läuft).
- **Nächste Aufgabe:** Ticket und warum.
- **Erledigt in diesem Lauf:** geklärte Punkte, geschriebene oder geänderte Seiten mit Version, angelegte Tickets.
- **Du bist dran:** neue Aufgaben an den Projektinhaber, blockierende zuerst.
- **Einstieg:** womit die Umsetzung beginnen kann, z. B. „SC-123 ist bereit, Umsetzung nach Seite X, Abschnitt Y“.
  Ist ein Ticket reif, den Startbefehl nach der Vorlage der Regeldatei vollständig ausgefüllt als Codeblock anhängen
  (Session-Name, Auftrag mit Ticket-ID und Name, Grundlage mit Seite und Abschnitt), damit der Projektinhaber ihn nur
  noch ins Terminal kopiert. Fehlt eine Vorlage, entfällt der Befehl.

## Grenzen

- Kein Code, keine Branches, Commits oder PRs; Umsetzung ist nicht Teil dieses Laufs.
- Eine Aufgabe je Lauf; nur Aufgaben aus dem Plan, kein neues, fachlich unabhängiges Vorhaben.
- Keine Annahmen bei folgenreichen Fragen; ausbleibende Antwort ist keine Zustimmung.
- Nicht anfassen: `Für Später`, offene Aufgaben an den Projektinhaber, Tickets, die gerade bearbeitet werden.