---
name: task-planning
description: Ein Ticket umsetzungsreif machen oder aus einer Beschreibung anlegen. Plan, Akzeptanzkriterien und Risikoklasse ins Ticket schreiben und es nach To Do verschieben, oder bei Unklarheit nachfragen. Für die Rolle Planner.
argument-hint: <Ticket-ID oder Beschreibung>
---

# Ticket ausplanen

Eingabe aus dem Auftrag ($ARGUMENTS): eine Ticket-ID oder die Beschreibung eines Vorhabens.

## Eingabe bestimmen

- **Ticket-ID** (Buchstaben, Bindestrich, Zahl, etwa `CC-12`): Arbeite mit diesem Ticket.
- **Alles andere ist eine Beschreibung:** Lege daraus nach `ccmas:ticket` ein Ticket in To Plan an. Die Beschreibung des Menschen kommt unverändert in die Beschreibung des Tickets. Nenne die vergebene ID und arbeite mit diesem Ticket weiter.

## Voraussetzung

Das Ticket liegt im Backlog oder in To Plan, oder „Am Zug“ steht auf Planner, weil ein Worker den Plan als nicht umsetzbar gemeldet hat. Sonst ändere nichts und melde den Ausgang „Problem“ mit der vorgefundenen Spalte.

Kommt das Ticket von einem Worker zurück, lies dessen letzten Kommentar und überarbeite den vorhandenen Plan, statt einen zweiten daneben zu schreiben. Was im Branch des Tickets schon umgesetzt ist, bleibt bestehen; der neue Plan nennt, was davon gilt.

## Schritte

1. **Lesen.** Ticket nach `ccmas:ticket`, dazu `CLAUDE.md`, `docs/` und die Stellen im Code, die das Ticket berührt. Lies gezielt, nicht das ganze Repository.
2. **Reife prüfen.** Das Ticket ist reif, wenn alle vier Fragen beantwortbar sind:
   - Was genau soll danach anders sein?
   - Woran erkennt ein Dritter, dass es fertig ist?
   - Welche Dateien und Schnittstellen sind betroffen?
   - Gibt es eine fachliche Wahl, die der Mensch treffen muss?
3. **Bei einer offenen fachlichen Wahl oder fehlenden Angaben:** Frage den Menschen in der Sitzung. Antwortet er, halte die Antwort im Ticket fest und mache weiter. Bleibt die Frage offen, lege eine Rückfrage nach `ccmas:ticket` an. Kein Plan auf Grundlage einer Annahme.
4. **Zu groß?** Braucht das Ticket mehr als etwa einen Tag Arbeit oder berührt es mehrere unabhängige Dinge, plane nur den ersten in sich geschlossenen Teil. Lege die übrigen Teile als eigene Tickets im Backlog an, mit Typ, Meilenstein und Abhängigkeit, und nenne sie im Übergabe-Kommentar.
5. **Plan schreiben.** Ergänze die Beschreibung des Tickets um zwei Abschnitte. Der ursprüngliche Text bleibt darüber unverändert stehen.

   ```
   ## Plan
   1. <Schritt mit betroffenen Dateien>
   2. …
   Tests: <welche Tests neu oder geändert werden>
   Doku: <welche Datei unter docs/ angepasst wird, oder „keine“>

   ## Akzeptanzkriterien
   - [ ] <prüfbare Aussage>
   - [ ] …
   ```

   Jedes Akzeptanzkriterium ist einzeln prüfbar: ein Befehl, ein Test oder eine beobachtbare Ausgabe. „Funktioniert gut“ ist kein Kriterium.
6. **Risikoklasse festlegen.** Es gilt die höchste zutreffende Zeile.

   | Klasse | Trifft zu, wenn |
   | --- | --- |
   | R0 trivial | Nur Doku, Kommentare, Tippfehler oder reine Tests |
   | R1 niedrig | Abgegrenzte Änderung in einem Modul, mit Tests, ohne geänderte Schnittstelle |
   | R2 mittel | Mehrere Module, geänderte Schnittstelle, neue Abhängigkeit oder größeres Refactoring |
   | R3 hoch | Datenmodell oder Migration, Berechtigungen, Zugangsdaten, CI oder Build, Merge-Logik, Dateien unter `.claude/` oder die `CLAUDE.md` |

7. **Übergeben.** Feld Risikoklasse setzen, „Am Zug“ auf Worker, „Ausgang“ auf Fertig. Ticket nach To Do verschieben. Übergabe-Kommentar schreiben.

## Grenzen

- Kein Code, kein Branch, keine Datei im Repository ändern.
- Kein Plan, der über das Ticket hinausgeht.

## Abschluss

Nenne am Ende: Ticket, Ausgang, Risikoklasse, den Plan in zwei bis drei Sätzen, die Akzeptanzkriterien als Liste, angelegte Rückfragen und Folgetickets.
