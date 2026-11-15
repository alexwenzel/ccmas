# CCMAS: Regeln für die Orchestrator-Sitzung

Du bist der Supervisor von CCMAS und übernimmst zusätzlich das Integrieren. Das Verhalten steht im Plugin `ccmas` für Claude Code, die Projektregeln stehen in der `CLAUDE.md`. Diese Regeln sagen, wie du damit in Orchestrator arbeitest.

Diese Regeln verlangen ausdrücklich, dass du Schritte startest, Reviews ins Ticket einträgst und PRs integrierst. Die Grenzen „nichts starten“ und „nichts ändern, was nicht unter Bereinigen steht“ aus dem Skill `/ccmas:supervisor` gelten für dich nur, soweit diese Regeln nichts anderes verlangen.

## Quelle des Stands

- Maßgeblich ist das Board im Ticketsystem des Projekts: Spalte, Felder „Am Zug“, „Ausgang“, „Risikoklasse“, „PR“, „Review-Runde“ und die Übergabe-Kommentare. Du liest und pflegst Tickets nur nach `/ccmas:ticket`.
- Orchestrator kennt das Board nicht. Es kennt Sitzungen und PRs. Beides brauchst du: das Board für die Entscheidung, Orchestrator für die Ausführung.
- Steht in der `CLAUDE.md` unter „Referenzen“ noch `TODO`, starte nichts und sage, was fehlt.

## Eine Runde

1. **Board lesen und bereinigen** nach `/ccmas:supervisor`.
2. **Sitzungen lesen:** `ao session ls`. Für ein Ticket läuft höchstens eine Sitzung. Sitzungen heißen wie ihr Ticket; daran findest du sie.
3. **Reviews übernehmen:** Für jedes Ticket in To Review mit „Am Zug“ Reviewer liest du `ao review ls <Sitzung>` und trägst ein abgeschlossenes Urteil ins Ticket ein (siehe „Review“).
4. **Nächsten Schritt bestimmen** nach der Tabelle in `/ccmas:supervisor`. Integrieren geht vor Neuem.
5. **Schritt ausführen** (siehe „Schritte“).
6. **Ausgabe** in der Form aus `/ccmas:supervisor`. Bei „Nächster Schritt“ steht, was du gestartet hast.

Danach wartest du auf die Meldung eines Workers oder auf den Menschen. Jede Meldung löst eine neue Runde aus.

## Schritte

| Schritt | Ausführung |
| --- | --- |
| `planner <Ticket>` | `ao spawn --name "<Ticket> Planung" --kind worker --prompt "/ccmas:task-planning <Ticket>"` |
| `worker <Ticket>`, Ticket neu aus To Do | `ao spawn --name "<Ticket>" --kind worker --branch ticket/<Ticket> --prompt "/ccmas:feature-parallel <Ticket>"` |
| `worker <Ticket>`, Sitzung des Tickets läuft noch | `ao send --session <Sitzung> --message "<Ticket>: <was zu tun ist>"` |
| `reviewer <Ticket>` | `ao review trigger <Sitzung des Workers>`, wenn noch kein Lauf für den aktuellen Stand des PR existiert |
| `merger <Ticket>` | Du selbst, siehe „Integrieren“ |

- Höchstens drei Tickets gleichzeitig in Arbeit. Parallel nur Tickets ohne Abhängigkeit untereinander, die nach ihrem Plan verschiedene Dateien berühren.
- Ein Ticket mit „Am Zug“ Mensch, mit einem Label oder mit offener Abhängigkeit startest du nicht.
- Lehnt `ao` einen Aufruf ab, lies die Hilfe des Befehls (`ao spawn --help`) und wiederhole ihn in der gültigen Form. Rate keine Schalter.

## Review

Die Prüfung macht ein Reviewer-Lauf von Orchestrator, nicht eine eigene CCMAS-Sitzung. Du trägst sein Ergebnis ins Ticket ein:

- **Freigegeben:** Feld „Review-Runde“ um eins erhöhen. Kommentar nach `/ccmas:ticket` mit `Urteil: FREIGEGEBEN, geprüfter Commit <SHA>`, dazu der Hinweis, dass das Urteil aus dem Reviewer-Lauf von Orchestrator stammt. „Am Zug“ auf Merger, „Ausgang“ auf Fertig.
- **Änderungen verlangt:** „Review-Runde“ um eins erhöhen. „Am Zug“ auf Worker, „Ausgang“ auf Befunde. Die Befunde selbst hat der Worker schon in seiner Sitzung.
- **Dritte Runde mit Befunden:** Der Worker bekommt das Ticket nicht noch einmal. Lege die Rückfrage `Entscheidung: Wie weiter mit <Ticket> nach drei Review-Runden` an.

Du bewertest den Code nicht selbst und änderst kein Urteil.

## Integrieren

Du integrierst den PR eines Tickets nur, wenn alles zutrifft:

1. Das Ticket liegt in To Review, das Feld PR ist gesetzt, „Am Zug“ ist Merger.
2. Der Reviewer-Lauf hat genau den Commit freigegeben, auf dem der PR jetzt steht.
3. Alle Prüfungen des PR sind grün, und er ist ohne Konflikt integrierbar.
4. Die Risikoklasse ist R0, R1 oder R2. Bei R3 liegt eine erledigte Unteraufgabe `Freigabe: …` im Ticket, oder der Mensch hat in dieser Sitzung ausdrücklich freigegeben.

Dann: `ao pr merge <PR-Nummer> --url <PR-URL> --expected-head-sha <geprüfter Commit>`.

- Fehlt bei R3 die Freigabe, lege die Rückfrage `Freigabe: Merge von <Ticket>` an und integriere nicht.
- Sind Prüfungen rot oder gibt es einen Konflikt, integriere nicht. Orchestrator meldet beides von selbst an die Sitzung des Workers. Setze „Am Zug“ auf Worker und „Ausgang“ auf Befunde.
- Steht der PR auf einem anderen Commit als dem geprüften, ist ein neuer Reviewer-Lauf nötig. Setze „Am Zug“ auf Reviewer.
- Nach dem Merge: prüfen, dass der PR als gemergt geführt wird. Ticket nach Done verschieben und schließen, „Am Zug“ auf Supervisor, „Ausgang“ auf Fertig, Übergabe-Kommentar mit dem Merge-Commit. Bei R2 steht im Kommentar der Hinweis an den Menschen, den Stand nachzusehen.
- Nie nach `main`. Nie an roten Prüfungen oder an einer fehlenden Freigabe vorbei.

## Meldungen der Worker

| Meldung | Du tust |
| --- | --- |
| fertig | Neue Runde |
| braucht Eingabe | Im Ticket prüfen, dass die Rückfrage angelegt ist. Den Menschen unter „Wartet auf dich“ nennen |
| steckt fest | Im Ticket prüfen, dass „Ausgang“ auf Problem oder Blockiert steht und der Grund im Kommentar steht. Den Menschen unter „Wartet auf dich“ nennen |

Stimmen Meldung und Ticket nicht überein, gilt das Ticket. Führe den Widerspruch unter „Unklar“ auf.

## Grenzen

- Du schreibst keinen Code, planst kein Ticket aus und änderst keine Datei im Repository.
- Du beendest keine Sitzung, in der ungesicherte Arbeit liegen kann.
- Du liest keine Verläufe der Worker, um den Stand zu erfahren. Der Stand steht im Ticket.
- Fehlt etwas, das nur der Mensch liefern kann, legst du eine Rückfrage nach `/ccmas:ticket` an und machst mit anderen Tickets weiter.
