# Projektboard und Übergaben

## Projektboard

Das Konzept ist nicht an ein bestimmtes Ticketsystem gebunden. Spalten und Felder lassen sich in Asana, Jira oder einem anderen Board gleich abbilden. Das Board legt der Mensch an; `/ccmas:init` liest die Kennungen aus und trägt sie in die Projektregeln ein.

### Spalten

| Spalte | Bedeutung | Wer schiebt hinein |
| --- | --- | --- |
| Backlog | Erfasst, noch nicht an der Reihe. Hier liegen auch die Milestones, bis sie abgenommen sind | Mensch; Project Planner (Milestones); Feature Planner (Tasks); Planner (Folgetickets) |
| To Plan | Liegt als Nächstes auf dem Weg, ist aber noch nicht ausgeplant | Supervisor; Planner, wenn er ein Ticket aus einer Beschreibung anlegt |
| To Do | Ausgeplant und umsetzungsreif | Planner |
| In Progress | Wird gerade umgesetzt | Worker |
| To Review | Umgesetzt, PR liegt vor, wartet auf Review, Merge oder Freigabe | Worker; Project Planner und Feature Planner mit dem Ticket ihres Doku-PR |
| Done | Fertig und geschlossen | Merger; Supervisor beim Bereinigen, wenn der PR gemergt ist, und für abgenommene Milestones |

Ein Ticket bleibt in To Review, bis es integriert ist. Review, Befunde und Freigabe ändern nur die Felder, nicht die Spalte.

### Felder

Die Felder tragen die Kommunikation zwischen den Rollen. Was als Freitext im Kommentar steht, kann nur ein Modell lesen. Was in einem Feld steht, kann auch ein Programm lesen und filtern.

| Feld         | Art             | Zweck                                                                                                                    | Wer setzt es                                                                                                      |
| ------------ | --------------- | ------------------------------------------------------------------------------------------------------------------------ | ----------------------------------------------------------------------------------------------------------------- |
| ID           | automatisch     | Eindeutige Kennung des Tickets. Commits und PR verweisen darauf. Steht nicht im Namen des Tickets                        | Ticketsystem                                                                                                      |
| Typ | Auswahl | Art der Arbeit: Concept, Implementation, Bug, Organisation. Ein Milestone hat keinen Typ; ihn kennzeichnet die Ticketart | Wer das Ticket anlegt: Mensch, Feature Planner oder Planner |
| Meilenstein | Text | Zu welchem Milestone der Task gehört: der Name des Milestones. Grundlage für die Reihenfolge und den Abschluss | Wer das Ticket anlegt: Mensch, Feature Planner oder Planner |
| Label        | Mehrfachauswahl | Warum etwas auf jemanden außerhalb der Agents wartet: Frage, Wartet auf Zugang/Daten, Wartet auf Dritte, Für Später      | Die Rolle an der Rückfrage (Frage, Wartet auf Zugang/Daten); der Mensch am Ticket (Wartet auf Dritte, Für Später) |
| Risikoklasse | Auswahl | [R0 bis R3](rollen.md#risikoklassen). Entscheidet, ob der Merger ohne den Menschen integriert | Feature Planner (vorläufig); Planner; Reviewer darf hochstufen |
| Am Zug       | Auswahl         | Welche Rolle als Nächstes handelt: Supervisor, Planner, Worker, Reviewer, Merger, Mensch                                 | Die übergebende Rolle                                                                                             |
| Ausgang      | Auswahl         | Wie der letzte Schritt geendet hat: Fertig, Rückfrage, Befunde, Problem, Blockiert                                       | Die übergebende Rolle                                                                                             |
| PR | Text | Der Pull Request des Tickets. Reviewer und Merger finden ihn ohne Suche | Worker; Project Planner und Feature Planner am Ticket ihres Doku-PR |
| Review-Runde | Zahl            | Zahl der Review-Durchgänge; leer zählt als null. Nach der dritten Runde mit Befunden geht eine Rückfrage an den Menschen | Reviewer                                                                                                          |

Die Felder beantworten verschiedene Fragen und ersetzen einander nicht:

- **Typ:** Was für eine Arbeit ist es? Der Typ ändert sich nie, alle anderen Angaben ändern sich im Lauf. Im aktuellen Plugin durchlaufen alle Typen dieselbe Kette; der Typ steuert noch keinen eigenen Ablauf ([Offene Punkte](offene-punkte.md)).
- **Spalte:** Wo steht das Ticket im Ablauf?
- **Am Zug:** Wer handelt als Nächstes?
- **Ausgang:** Wie endete der letzte Schritt?
- **Label:** Wartet etwas auf jemanden außerhalb der Agents? Ein Ticket mit Label startet der Supervisor nicht.

### Was sonst am Ticket hängt

| Bestandteil | Zweck |
| --- | --- |
| Beschreibung | Am Task die Abschnitte Ziel, Gewünschtes Verhalten, Technische Vorgaben, Szenarien und Akzeptanzkriterien des Feature Planners, darunter der Plan des Planners. Am Milestone Ergebnisziel, Umfang und das Konzept in Kurzform |
| Kommentare | Die Übergaben der Rollen in fester Form ([Übergaben](#übergaben)) |
| Abhängigkeiten | Welches Ticket erst nach welchem beginnen kann. Der Supervisor startet nur Tickets, deren Abhängigkeiten in Done liegen |
| Unteraufgaben | Rückfragen an den Menschen: `Entscheidung:`, `Freigabe:`, `Zuarbeit:`. Das Ticket hängt von ihnen ab. Einen anderen Zweck haben Unteraufgaben nicht |

## Übergaben

Eine Übergabe besteht aus drei Teilen, die zusammen den Stand eines Tickets ergeben:

| Teil | Trägt | Lesbar für |
| --- | --- | --- |
| Spalte | Wo das Ticket im Ablauf steht | Mensch, Modell, Programm |
| Felder „Am Zug“ und „Ausgang“, dazu Risikoklasse, PR, Review-Runde | Wer als Nächstes handelt und wie der letzte Schritt endete | Mensch, Modell, Programm |
| Übergabe-Kommentar | Was getan wurde und was offen ist | Mensch, Modell |

Project Planner, Feature Planner, Planner, Worker, Reviewer und Merger beenden ihre Arbeit an einem Ticket mit genau einem Kommentar in dieser Form:

```text
Übergabe: <Rolle>
Ausgang: <Fertig | Rückfrage | Befunde | Problem | Blockiert>
Ergebnis: <ein bis drei Sätze; Branch, Commit, PR oder Urteil>
Risikoklasse: <R0 | R1 | R2 | R3>
Nächste Rolle: <Supervisor | Planner | Worker | Reviewer | Merger | Mensch>
Offen: <was fehlt, sonst „nichts“>
```

- Die erste Zeile nennt die Rolle. Sie ist die einzige Stelle, an der zu erkennen ist, wer geschrieben hat ([Wer ins Ticket schreibt](#wer-ins-ticket-schreibt)).
- Der Reviewer schreibt im Feld Ergebnis sein Urteil mit dem geprüften Commit, die Zahl der erfüllten Kriterien und die Befunde als Liste. Dasselbe Urteil steht als Kommentar am PR.
- Ausführliches gehört in die Beschreibung des Tickets, in den PR oder nach `docs/`, nicht in den Kommentar.
- Der Supervisor übergibt anders: Sein Ergebnis ist die Ausgabe seiner Sitzung ([Supervisor-Loop](ablauf.md#supervisor-loop)). Ins Ticket schreibt er nur, was er beim Bereinigen ändert.

### Übergaben im Ablauf

So stehen Spalte und Felder nach jedem Schritt:

| Von | Anlass | Spalte danach | Am Zug | Ausgang |
| --- | --- | --- | --- | --- |
| Project Planner | Roadmap freigegeben, Doku-PR erstellt | Milestones im Backlog; Ticket des Doku-PR in To Review | Reviewer (am Ticket des Doku-PR) | Fertig |
| Feature Planner | Konzept freigegeben, Doku-PR erstellt | Tasks im Backlog; Ticket des Doku-PR in To Review | Reviewer (am Ticket des Doku-PR) | Fertig |
| Feature Planner | Mensch nicht da oder Freigabe fehlt | unverändert | Mensch (am Milestone) | Rückfrage |
| Supervisor | Ticket ist das nächste | To Plan | unverändert | unverändert |
| Planner | Ticket ist reif | To Do | Worker | Fertig |
| Planner | Entscheidung fehlt | unverändert | Mensch | Rückfrage |
| Worker | PR erstellt | To Review | Reviewer | Fertig |
| Worker | Befunde behoben | To Review | Reviewer | Fertig |
| Worker | Plan nicht umsetzbar | unverändert | Planner | Problem |
| Worker | Prüfbefehle scheitern an etwas außerhalb des Tickets | unverändert | Mensch | Blockiert |
| Reviewer | Urteil FREIGEGEBEN | To Review | Merger | Fertig |
| Reviewer | Urteil BEFUNDE | To Review | Worker | Befunde |
| Reviewer | BEFUNDE in der dritten Runde | To Review | Mensch | Rückfrage |
| Merger | Integriert | Done, geschlossen | Supervisor | Fertig |
| Merger | Verhalten müsste sich ändern | To Review | Worker | Befunde |
| Merger | PR nach dem Review geändert | To Review | Reviewer | Problem |
| Merger | R3 ohne Freigabe | To Review | Mensch | Rückfrage |
| Merger | CI rot außerhalb des Tickets | To Review | Mensch | Blockiert |
| Supervisor | Rückfrage ist beantwortet | unverändert | Die Rolle, die gefragt hat | unverändert |
| Supervisor | Alle Tasks eines Milestones in Done | unverändert | Mensch (am Milestone) | Rückfrage |
| Supervisor | Milestone abgenommen | Done, geschlossen | unverändert | unverändert |

Ob eine Übergabe stimmt, prüft die nächste Rolle an ihren Voraussetzungen: Spalte, Felder, PR. Stimmt etwas nicht, ändert sie nichts am Ticket und meldet in ihrer Sitzung den Ausgang „Problem“ mit dem vorgefundenen Stand. Scheitert ein Schreiben ins Ticketsystem auch beim zweiten Versuch, hört die Rolle auf und meldet den Fehler im Wortlaut, statt weiterzuarbeiten, als wäre es gelungen.

### Rückfragen an den Menschen

Was der Mensch in der Sitzung beantworten kann, wird dort gefragt und im Ticket festgehalten. Was offen bleibt, wird eine Rückfrage:

1. Eine Unteraufgabe im Ticket, dem Menschen zugewiesen. Der Name beginnt mit `Entscheidung:`, `Freigabe:` oder `Zuarbeit:`.
2. Die Beschreibung nennt den Kontext in ein bis drei Sätzen, die Optionen mit ihren Folgen und eine Empfehlung.
3. Die Unteraufgabe trägt das Label „Frage“, bei `Zuarbeit:` das Label „Wartet auf Zugang/Daten“.
4. Das Ticket wird von der Unteraufgabe abhängig gemacht. „Am Zug“ steht auf Mensch, „Ausgang“ auf Rückfrage. Die Spalte bleibt.

Eine Frage je Unteraufgabe. Keine Antwort ist keine Zustimmung. Hat der Mensch die Unteraufgabe erledigt, gibt der Supervisor das Ticket in der nächsten Runde an die Rolle zurück, die gefragt hat.

### Wer ins Ticket schreibt

Alle sieben Rollen schreiben ins Ticketsystem. Sie nutzen dieselbe Verbindung und schreiben unter **einem** Konto: dem, mit dem die Verbindung angemeldet ist. Ein eigenes Konto je Rolle gibt es nicht. Welche Rolle geschrieben hat, steht in der ersten Zeile des Kommentars (`Übergabe: <Rolle>`), nicht im Namen des Schreibenden.

| Rolle | Legt an | Verschiebt | Setzt Felder | Schreibt |
| --- | --- | --- | --- | --- |
| Project Planner | Milestones (Backlog), das Ticket des Doku-PR der Roadmap (To Review) | nichts | PR, Am Zug und Ausgang am Ticket des Doku-PR | Ergebnisziel, Umfang und Einordnung in die Beschreibung der Milestones, Übergabe als Kommentar |
| Feature Planner | Tasks (Backlog), das Ticket des Doku-PR des Konzepts (To Review), einen Milestone aus einer Beschreibung, Rückfragen | nichts | Typ, Meilenstein und vorläufige Risikoklasse an den Tasks; PR, Am Zug und Ausgang am Ticket des Doku-PR | Die Beschreibung der Tasks, das Konzept in Kurzform am Milestone, Übergabe als Kommentar |
| Supervisor | Die Abnahme-Rückfrage am Milestone | nach To Plan; beim Bereinigen nach Done, wenn der PR gemergt ist; abgenommene Milestones nach Done | Am Zug (beim Bereinigen) | Antworten des Menschen als Kommentar |
| Planner | Ticket aus einer Beschreibung (To Plan), Folgetickets (Backlog), Rückfragen | nach To Do | Typ und Meilenstein an neuen Tickets, Risikoklasse, Am Zug, Ausgang | Den Plan in die Beschreibung, bei Tickets ohne Vorarbeit auch Szenarien und Akzeptanzkriterien; Übergabe als Kommentar |
| Worker | Rückfragen | nach In Progress, nach To Review | PR, Am Zug, Ausgang | Übergabe als Kommentar |
| Reviewer | Rückfragen | nichts | Review-Runde, Risikoklasse (nur höher), Am Zug, Ausgang | Urteil mit geprüftem Commit, im Ticket und am PR |
| Merger | Rückfragen, bei R3 die Freigabe | nach Done, schließt das Ticket | Am Zug, Ausgang | Übergabe mit Merge-Commit, bei R2 den Hinweis zur Nachkontrolle |
| Mensch | Tickets und Meilensteine | wie er will | Label, alles andere bei Bedarf | Antworten auf Rückfragen |

- Milestones legt der Project Planner an, Tasks der Feature Planner, einzelne Tickets aus einer Beschreibung und Folgetickets der Planner. Nur Merger und Supervisor schließen ein Ticket. Keine Rolle löscht eines.
- Die Regeln dazu stehen im Skill `ccmas:ticket`, der einzigen Stelle, die das Ticketsystem kennt.
- Unter welchem Konto geschrieben wird, hängt an der Anmeldung der Verbindung, nicht an der Rolle.
