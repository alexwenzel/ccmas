---
name: supervisor
description: Das Board lesen, auf den tatsächlichen Stand bringen und den nächsten sinnvollen Schritt nennen. Für die Rolle Supervisor.
---

# Nächsten Schritt bestimmen

## Schritte

1. **Lesen.** Alle offenen Tickets des Boards mit Spalte, Feldern, Abhängigkeiten und offenen Unteraufgaben an den Menschen, nach `ccmas:ticket`. Dazu die offenen und zuletzt gemergten PRs des Repositorys.
2. **Bereinigen.** Ändere nur, was eindeutig belegt ist, und nenne jede Änderung:
   - Der PR eines Tickets ist gemergt, das Ticket liegt nicht in Done: nach Done verschieben und schließen.
   - Eine Rückfrage an den Menschen ist erledigt: „Am Zug“ zurück auf die Rolle, die gefragt hat; die Antwort im Ticket als Kommentar festhalten.
   - Ein Ticket hängt von einem Ticket ab, das jetzt in Done liegt: Es gilt als entsperrt. Verschoben wird es dadurch nicht.
   Alles, was widersprüchlich aussieht und nicht eindeutig ist, änderst du nicht. Du führst es unter „Unklar“ auf.
3. **Nächsten Schritt bestimmen.** Es gilt die erste Zeile, die auf ein Ticket zutrifft. Integrieren geht vor Neuem.

   | Stand | Nächster Schritt |
   | --- | --- |
   | To Review, „Am Zug“ Merger | `merger <Ticket-ID>` |
   | To Review, „Am Zug“ Reviewer | `reviewer <Ticket-ID>` |
   | To Review oder In Progress, „Am Zug“ Worker | `worker <Ticket-ID>` |
   | To Do, alle Abhängigkeiten in Done | `worker <Ticket-ID>` |
   | Beliebige Spalte, „Am Zug“ Planner | `planner <Ticket-ID>` |
   | To Plan | `planner <Ticket-ID>` |
   | Backlog, als Nächstes auf dem Weg | Ticket nach To Plan verschieben, dann `planner <Ticket-ID>` |

   Treffen in einer Zeile mehrere Tickets zu, nimm das, das die meisten anderen Tickets entsperrt; bei Gleichstand das im früheren Meilenstein, dann das ältere. Ein Ticket überspringst du, wenn „Am Zug“ auf Mensch steht, wenn es ein Label trägt (Frage, Wartet auf Zugang/Daten, Wartet auf Dritte, Für Später) oder wenn es von einem Ticket oder einer Unteraufgabe abhängt, die noch offen ist.

   Ein Schritt besteht aus Rolle und Ticket. Gestartet wird er vom Menschen oder von dem Werkzeug, das die Sitzungen führt. Du startest nichts, außer die Regeln deiner Sitzung verlangen es ausdrücklich.
4. **Parallel möglich.** Nenne weitere Schritte, die gleichzeitig laufen könnten: Tickets ohne Abhängigkeit untereinander, die nach Plan verschiedene Dateien berühren. Höchstens zwei weitere.
5. **Wartet auf den Menschen.** Liste die offenen Rückfragen und Freigaben, dazu jedes Ticket, dessen „Ausgang“ auf Problem oder Blockiert steht, mit dem Grund aus dem letzten Kommentar.

## Ausgabe

Genau in dieser Form:

```
Stand: <zwei bis vier Sätze>
Bereinigt: <Liste der Änderungen, oder „nichts“>
Unklar: <Liste, oder „nichts“>
Nächster Schritt: <rolle> <Ticket-ID>
Begründung: <ein Satz>
Parallel möglich: <Schritte, oder „nichts“>
Wartet auf dich: <Liste, oder „nichts“>
```

Gibt es keinen startbaren Schritt, steht bei „Nächster Schritt“ das Wort „keiner“, und die Begründung sagt, was fehlt.

## Grenzen

- Kein Ticket ausplanen, keinen Code lesen oder ändern, keinen PR bewerten.
- Nichts starten, außer die Regeln deiner Sitzung verlangen es ausdrücklich.
- Nichts ändern, was nicht unter „Bereinigen“ steht.
