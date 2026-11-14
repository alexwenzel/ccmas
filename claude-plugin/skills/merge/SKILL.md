---
name: merge
description: Den freigegebenen PR eines Tickets fertig machen und in den Integrationsbranch integrieren. Konflikte lösen, Prüfbefehle und CI prüfen, Ticket nach Done verschieben. Für die Rolle Merger.
argument-hint: <Ticket-ID>
---

# PR integrieren

Eingabe: die Ticket-ID aus dem Auftrag ($ARGUMENTS).

## Voraussetzungen

Prüfe alle, bevor du etwas änderst. Trifft eine nicht zu, ändere nichts und melde den Ausgang „Problem“ mit dem Grund.

1. Das Ticket liegt in To Review, das Feld PR ist gesetzt, „Am Zug“ ist Merger.
2. Der letzte Kommentar des Reviewers trägt das Urteil FREIGEGEBEN.
3. Du stehst im Worktree des Tickets auf dessen Branch, nicht im Hauptverzeichnis.

## Schritte

1. **Lesen.** Ticket nach `ccmas:ticket`: Risikoklasse, Urteil mit geprüftem Commit, PR.
2. **Freigabe bei R3.** Bei Risikoklasse R3 integrierst du nur, wenn der Mensch freigegeben hat: entweder durch eine erledigte Unteraufgabe `Freigabe: …` im Ticket oder ausdrücklich in dieser Sitzung. Frage ihn in der Sitzung. Gibt er frei, halte das im Ticket fest. Ist er nicht da oder lehnt er ab, lege die Rückfrage `Freigabe: Merge von <Ticket-ID>` nach `ccmas:ticket` an und höre auf.
3. **Stand prüfen.** Vergleiche den Commit, auf dem der PR steht, mit dem geprüften Commit aus dem Urteil. Weichen sie voneinander ab, weil nach dem Review noch geändert wurde: „Am Zug“ auf Reviewer, „Ausgang“ auf Problem, Kommentar, Ende.
4. **Fertig machen.** Hole den aktuellen Integrationsbranch und führe ihn in den Branch des Tickets zusammen (kein Rebase, kein `--force`). Löse Konflikte. Führe die Prüfbefehle aus der `CLAUDE.md` aus. Pushe den Branch und warte, bis die CI des PR durch ist. Steht in der `CLAUDE.md`, dass das Projekt keine CI hat, entfällt das Warten; dann zählen allein die Prüfbefehle.
5. **Was du selbst beheben darfst.** Konflikte, Formatierung und kleine Korrekturen an Tests. Ändert sich dabei das Verhalten des Codes, oder ist dafür mehr nötig: nicht weitermachen. „Am Zug“ auf Worker, „Ausgang“ auf Befunde, im Kommentar genau beschreiben, was zu tun ist. Ende.
6. **Integrieren.** Sind die Prüfbefehle und, wo vorhanden, die CI grün, integriere den PR in den Integrationsbranch (Squash). Prüfe danach, dass der PR als gemergt geführt wird.
7. **Abschließen.** Ticket nach Done verschieben und schließen. „Am Zug“ auf Supervisor, „Ausgang“ auf Fertig. Übergabe-Kommentar mit dem Merge-Commit. Bei Risikoklasse R2 steht im Kommentar zusätzlich der Hinweis an den Menschen, den Stand nachzusehen.

## Wenn es nicht weitergeht

- **CI rot aus einem Grund außerhalb des Tickets:** „Ausgang“ auf Blockiert, „Am Zug“ auf Mensch, Ursache im Kommentar. Nicht integrieren.
- **Konflikt nicht ohne fachliche Entscheidung lösbar:** Rückfrage nach `ccmas:ticket`.

## Grenzen

- Nur der PR dieses Tickets.
- Nie nach `main`, nie mit `--force`, nie an einer roten CI vorbei. Eine fehlende CI ist nur in Ordnung, wenn die `CLAUDE.md` sagt, dass das Projekt keine hat.
- Der Worktree wird von dir nicht entfernt.

## Abschluss

Nenne am Ende: Ticket, Ausgang, Merge-Commit oder den Grund für den Halt, und was du selbst behoben hast.
