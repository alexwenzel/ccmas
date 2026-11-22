# Board

Das Konzept ist nicht an ein bestimmtes Ticketsystem gebunden. Spalten und Felder lassen sich in Asana, Jira oder einem anderen Board gleich abbilden. Das Board legt der Mensch an; `/ccmas:init` liest die Kennungen aus und trägt sie in die Projektregeln ein. Im Plugin kennt nur der Skill [`ticket`](../../claude-plugin/skills/ticket/SKILL.md) das Ticketsystem.

## Spalten

| Spalte | Bedeutung | Wer schiebt hinein |
| --- | --- | --- |
| Backlog | Erfasst, noch nicht ausgeplant. Hier liegen auch die Milestones bis zur Abnahme | Mensch, Planner |
| To Do | Ausgeplant: Ziel, Verhalten, Vorgaben, Szenarien und Akzeptanzkriterien stehen im Ticket | Planner |
| In Progress | Ein Worker arbeitet daran | Worker |
| To Review | Der PR steht: Review, Befunde, Merge | Worker |
| Done | Gemergt und geschlossen | Supervisor |

Ein Ticket bleibt in To Review, bis es gemergt ist. Review, Befunde und Freigabe ändern die Spalte nicht; ihr Stand steht am PR.

**Startbar** ist ein Task in To Do, dessen Abhängigkeiten in Done liegen, der keine offene Rückfrage und kein Label trägt und nicht dem Menschen zugewiesen ist.

## Felder

| Feld | Zweck | Wer setzt es |
| --- | --- | --- |
| ID | Kennung des Tickets. Commits und PR verweisen darauf | Ticketsystem |
| Typ | Art der Arbeit, für die Übersicht im Board: Concept, Implementation, Bug, Organisation. Kein Ablauf hängt davon ab | Wer das Ticket anlegt |
| Meilenstein | Zu welchem Milestone der Task gehört | Wer das Ticket anlegt |
| Label | Frage, Wartet auf Zugang/Daten, Wartet auf Dritte, Für Später | Die Rolle an der Rückfrage; der Mensch am Ticket |
| PR | Link zum Pull Request | Worker |

## Am Ticket

- **Beschreibung eines Tasks:** Ziel, Gewünschtes Verhalten, Technische Vorgaben, Szenarien, Akzeptanzkriterien. Der Planner schreibt sie, der Worker ändert sie nicht.
- **Beschreibung eines Milestones:** Ergebnisziel, Umfang, Nicht-Umfang, das Konzept in Kurzform und der Pfad zur Konzeptseite.
- **Abhängigkeiten:** zwischen Tasks und zwischen Milestones. Sie sind die Reihenfolge und sagen, was parallel laufen kann.
- **Kommentar:** einer je Übergabe (Rückfrage, freigegebener PR, Merge), am Anfang die Rolle.

  ```text
  <Rolle>: <Ergebnis in ein bis drei Sätzen; PR, Commit>
  Offen: <was fehlt, sonst „nichts“>
  ```

- **Zuweisung:** Ein Ticket, das dem Menschen zugewiesen ist, startet kein Agent. Der Supervisor nennt es unter „Wartet auf dich“.
- **Milestones** durchlaufen die Spalten nicht. Sie liegen im Backlog, bis der Supervisor sie nach der Abnahme nach Done verschiebt. Sie nutzen die Ticketart, die das Ticketsystem dafür hat: in Asana ein Meilenstein, in Jira ein Epic.

## Rückfragen an den Menschen

Was der Mensch in der Sitzung beantworten kann, wird dort gefragt und im Ticket festgehalten. Sonst:

1. Unteraufgabe im Ticket, dem Menschen zugewiesen: `Entscheidung: …`, `Freigabe: …` oder `Zuarbeit: …`.
2. Beschreibung: Kontext in ein bis drei Sätzen, Optionen mit Folgen, eine Empfehlung.
3. Label „Frage“, bei `Zuarbeit:` „Wartet auf Zugang/Daten“. Das Ticket hängt von der Unteraufgabe ab.

Abhaken ohne Kommentar heißt: Empfehlung angenommen. Ein Kommentar geht der Empfehlung vor. Keine Antwort ist keine Zustimmung. Die nächste Sitzung am Ticket liest die Antwort aus der Unteraufgabe. Unteraufgaben haben keinen anderen Zweck.

## Am PR

Der PR trägt den Stand der Umsetzung. Nichts davon wird ins Ticket übertragen.

| Bestandteil | Inhalt | Wer schreibt |
| --- | --- | --- |
| Titel | `<Ticket-ID>: <Ticketname>` | Worker |
| Beschreibung | Ticket, Änderung, Akzeptanzkriterien, Szenarien mit ihren Tests, Prüfbefehle mit Ergebnis | Worker |
| Urteil | FREIGEGEBEN oder BEFUNDE, mit dem geprüften Commit und den Befunden | Reviewer |
| Prüfungen | Die CI des Projekts, wo es eine gibt | GitHub |
| Label `freigabe: nötig` | Der Diff berührt die Liste der [Freigaben](konzept.md#freigaben) | Worker; der Reviewer, wenn es fehlt |
| Label `freigabe: erteilt` | Die Freigabe ist erteilt | Supervisor, nach der Antwort des Menschen oder nach eigener Entscheidung, wo die `CLAUDE.md` sie ihm überträgt |

## Wer was darf

| Rolle | Legt an | Verschiebt nach | Setzt | Schließt |
| --- | --- | --- | --- | --- |
| Planner | Milestones im Backlog, Tasks in To Do, Rückfragen | To Do, wenn er ein erfasstes Ticket ausgeplant hat | Typ, Meilenstein, Beschreibung, Abhängigkeiten | nichts |
| Worker | Rückfragen | In Progress, To Review | PR | nichts |
| Reviewer | nichts | nichts | Am PR: Urteil, `freigabe: nötig` | nichts |
| Supervisor | Rückfragen zu Merge und Abnahme | Done | Am PR: `freigabe: erteilt` | Tasks nach dem Merge, Milestones nach der Abnahme |

Alle Rollen schreiben unter dem Konto ins Ticketsystem, mit dem die Verbindung angemeldet ist. Welche Rolle geschrieben hat, steht am Anfang des Kommentars. Keine Rolle löscht ein Ticket.
