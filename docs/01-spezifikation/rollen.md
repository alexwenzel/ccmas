# Rollen und Risikoklassen

## Rollen

Acht Rollen arbeiten am Board: sieben Agents und der Mensch. Die Tabelle gilt unabhängig davon, mit welchem Werkzeug die Rollen umgesetzt sind.

| Rolle | Aufgabe | Ergebnis | Übergabe an |
| --- | --- | --- | --- |
| Mensch | Projekt und Features bestimmen, mit den Planners planen, Rückfragen beantworten, hohes Risiko freigeben, Milestones abnehmen | Entscheidungen, Antworten, Freigaben | Supervisor |
| Project Planner | Eine Projektbeschreibung in Milestones zerlegen, von klein nach groß geordnet | Roadmap und Milestones im Board | Feature Planner |
| Feature Planner | Einen Milestone konzipieren: Tech-Setup, gewünschtes Verhalten; in Tasks zerlegen | Konzept und Tasks mit Szenarien und Akzeptanzkriterien | Supervisor |
| Supervisor | Board auf den tatsächlichen Stand bringen, Worktrees aufräumen, Milestones abschließen, nächste Arbeit bestimmen, Parallelisierung prüfen, nennen, was auf den Menschen wartet | Genau ein nächster Schritt aus Rolle und Ticket, mit Begründung | Die genannte Rolle |
| Planner | Einen Task umsetzungsreif machen: Plan für diesen Code, Risikoklasse; zu Großes zerlegen | Umsetzungsreifes Ticket in To Do | Worker |
| Worker | Ein Ticket test-driven umsetzen: Tests aus den Szenarien, Code, Doku, PR. Befunde beheben | PR, Ticket in To Review | Reviewer |
| Reviewer | Den PR unabhängig gegen die Akzeptanzkriterien prüfen, nur lesend | Urteil für einen Commit: freigegeben oder Befunde | Merger, oder zurück an den Worker |
| Merger | Den freigegebenen PR eines Tickets fertig machen und integrieren; nicht Behebbares melden | Gemergter Stand, Ticket in Done | Supervisor (nächste Runde) |

- Der Supervisor schreibt keinen Code, plant weder Ticket noch Milestone aus und bewertet keinen PR. Er entscheidet nur, was als Nächstes drankommt und warum.
- Project Planner und Feature Planner schreiben nur Dokumentation und legen nichts im Board an, bevor der Mensch freigegeben hat. Der Feature Planner beschreibt keine Umsetzung, der Planner entscheidet nichts zu Tech-Setup oder Verhalten.
- Der Planner ändert keinen Code. Der Reviewer ändert gar nichts. Der Merger ändert das Verhalten des Codes nicht.
- Eine Rückfrage an den Menschen darf jede Rolle stellen ([Rückfragen an den Menschen](board.md#rückfragen-an-den-menschen)).

Wie die Rollen im Plugin umgesetzt sind und wie man sie startet, steht in der [README des Plugins](../../claude-plugin/README.md#rollen).

## Risikoklassen

Es gilt immer die höchste Klasse, deren Beschreibung zutrifft.

| Klasse | Trifft zu, wenn | Merge |
| --- | --- | --- |
| R0 trivial | Nur Doku, Kommentare, Tippfehler oder reine Tests | Merger integriert nach bestandenem Review und grünen Prüfungen |
| R1 niedrig | Abgegrenzte Änderung in einem Modul, mit Tests, ohne geänderte Schnittstelle | Merger integriert nach bestandenem Review und grünen Prüfungen |
| R2 mittel | Mehrere Module, geänderte Schnittstelle, neue Abhängigkeit oder größeres Refactoring | Merger integriert wie bei R1; im Übergabe-Kommentar steht zusätzlich der Hinweis an den Menschen, den Stand nachzusehen |
| R3 hoch | Datenmodell oder Migration, Berechtigungen, Zugangsdaten, CI oder Build, Merge-Logik, die Regeln der Agents selbst (`CLAUDE.md`, Dateien unter `.claude/`) | Merger integriert erst nach Freigabe durch den Menschen |

- **Grüne Prüfungen** heißt: Die Prüfbefehle aus den Projektregeln bestehen, und die CI am PR ist grün. Hat das Projekt laut Projektregeln keine CI, zählen allein die Prüfbefehle.
- **Review braucht jede Klasse.** Ohne das Urteil „freigegeben“ für den aktuellen Stand des PR integriert der Merger nichts. R0 und R1 werden damit im aktuellen Plugin gleich behandelt ([Offene Punkte](offene-punkte.md)).
- **Freigabe bei R3:** eine erledigte Unteraufgabe `Freigabe: …` im Ticket oder die ausdrückliche Freigabe des Menschen in der Sitzung des Mergers. Sie wird im Ticket festgehalten.
- **Vier Stufen statt drei,** weil „mittel“ sonst zwei Fälle mischt: Merge ohne den Menschen mit Nachkontrolle und Merge nur mit dem Menschen.

Die Einstufung läuft in drei Schritten:

1. **Vorab:** Der Feature Planner setzt am Task eine vorläufige Klasse. Der Planner prüft sie am Plan und stuft hoch, wenn eine höhere zutrifft. Hat das Ticket noch keine, setzt er sie.
2. **Durch den Reviewer am Diff:** Er prüft, ob die Klasse zum tatsächlichen Diff passt. Er darf hochstufen, nie herunterstufen.
3. **Im Ausbau durch feste Regeln am Diff:** Betroffene Pfade, Diff-Größe, neue Abhängigkeiten, Migrationen. Das prüft dann ein Skript im Skill `merge`, nicht das Urteil eines Modells. Im aktuellen Plugin gibt es diesen Schritt nicht.
