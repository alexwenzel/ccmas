---
name: feature-planning
description: Einen Milestone (Epic, Feature) im Gespräch mit dem Menschen konzipieren und in Tasks zerlegen. Legt Tech-Setup und gewünschtes Verhalten fest, recherchiert im Netz, schreibt das Konzept nach docs/ und legt die Tasks mit Szenarien und Akzeptanzkriterien im Board an. Für die Rolle Feature Planner.
argument-hint: <Milestone-ID oder Beschreibung eines Features>
---

# Milestone konzipieren und in Tasks zerlegen

Eingabe aus dem Auftrag ($ARGUMENTS): die ID eines Milestones oder die Beschreibung eines Features.

Du planst genau einen Milestone. Du arbeitest im Gespräch mit dem Menschen. Ohne seine Freigabe legst du nichts im Board an.

Du legst fest, **was** gewünscht ist und **womit**: Tech-Setup und Verhalten. Du beschreibst nicht, **wie** es im Code umgesetzt wird: keine Dateien, keine Arbeitsschritte. Das macht der Planner am einzelnen Task.

## Eingabe bestimmen

- **Milestone-ID:** Arbeite mit diesem Milestone.
- **Alles andere ist eine Beschreibung:** Sie wird nach der Freigabe als Milestone im Backlog angelegt (Schritt 9). Der Text des Menschen kommt unverändert in dessen Beschreibung.

## Voraussetzungen

1. Du stehst in einem eigenen Worktree, nicht im Hauptverzeichnis: `git rev-parse --git-dir` und `git rev-parse --git-common-dir` liefern verschiedene Pfade.
2. Der aktuelle Branch ist ein eigener Branch, weder `main` noch der Integrationsbranch.
3. Der Milestone hat noch keine Tasks. Hat er welche, frage den Menschen, ob du ergänzen sollst; lege nichts doppelt an.

Trifft eine nicht zu, ändere nichts und melde den Grund.

## Schritte

1. **Verstehen.** Lies den Milestone nach `ccmas:ticket`, dazu `CLAUDE.md`, `docs/roadmap.md`, die übrige Dokumentation unter `docs/` und die Stellen im Code, auf denen der Milestone aufbaut. Was die Roadmap als Grundentscheidung festlegt, gilt und wird nicht neu entschieden.
2. **Klären.** Frage den Menschen nach allem, was das Verhalten bestimmt und nicht in Milestone, Roadmap oder Code steht. Fragen gebündelt, mit Empfehlung.
3. **Recherchieren.** Prüfe im Netz, was es für die Aufgabe gibt und was aktuell ist: Bibliotheken und ihre aktuelle Version, bekannte Grenzen, übliche Lösungen. Lies die Dokumentation der Bibliotheken, die du vorschlägst. Notiere die Quellen. Das Konzept muss realistisch sein: Es passt zum vorhandenen Code und zum Stand der Technik, und nichts darin ist geraten.
4. **Konzept entwerfen.** Gliederung:

   ```
   # <Name des Milestones>
   ## Ergebnisziel
   ## Gewünschtes Verhalten
   <Was das System danach kann, aus Sicht dessen, der es benutzt. Schnittstellen, Daten, Fehlerfälle.>
   ## Tech-Setup
   | Baustein | Wahl | Version | Begründung | Quelle |
   <Stack, Bibliotheken, Konfiguration, Dienste.>
   ## High-Level-Konzept
   <Die Bausteine und wie sie zusammenspielen. Keine Dateien, keine Arbeitsschritte.>
   ## Nicht-Ziele
   ## Entscheidungen
   | Entscheidung | Begründung | Quelle |
   ## Tasks
   | Nr. | Task | Hängt ab von |
   ```

5. **In Tasks zerlegen.** Klein, logisch, aufeinander aufbauend:
   - Ein Task ist eine isolierte Logik: ein abgegrenztes Stück mit klarer Schnittstelle, das sich ohne den Rest prüfen lässt.
   - Ein Task ist die Arbeit eines Worker-Laufs und ergibt einen PR. Braucht er mehr als etwa einen Tag, teile ihn.
   - Jeder Task ist für sich prüfbar und lässt den Integrationsbranch lauffähig.
   - Von klein nach groß: erst die einzelnen Logiken, dann die Tasks, die sie zusammensetzen.
   - Die Reihenfolge steht in den Abhängigkeiten. Was unabhängig ist, bleibt unabhängig, damit es parallel laufen kann.
6. **Tasks ausformulieren.** Jeder Task bekommt diese Beschreibung:

   ```
   ## Ziel
   <ein bis drei Sätze>

   ## Gewünschtes Verhalten
   <Schnittstelle, Eingaben, Ausgaben, Fehlerfälle>

   ## Technische Vorgaben
   <was aus dem Konzept für diesen Task gilt, mit Verweis auf den Abschnitt in docs/milestones/<datei>.md>

   ## Szenarien
   - Gegeben <Ausgangslage>, wenn <Eingabe oder Ereignis>, dann <erwartetes Ergebnis>
   - …

   ## Akzeptanzkriterien
   - [ ] <prüfbare Aussage>
   - [ ] …
   ```

   - **Szenarien** sind die bekannten Fälle: der Normalfall, die Grenzfälle, die Fehlerfälle. Der Worker macht aus jedem zuerst einen Test und schreibt danach den Code.
   - **Akzeptanzkriterien** sind einzeln prüfbar: ein Befehl, ein Test oder eine beobachtbare Ausgabe. „Funktioniert gut“ ist kein Kriterium. Du schreibst sie, nicht die Rolle, die später umsetzt.
   - Dazu Typ, Meilenstein und eine vorläufige Risikoklasse nach der Tabelle in `ccmas:task-planning`.
7. **Freigabe.** Zeige dem Menschen das Konzept und die Taskliste. Arbeite seine Änderungen ein, bis er beides freigibt.
8. **Konzept schreiben und Doku-PR.** Schreibe das Konzept nach `docs/milestones/<kurzname>.md`. Committen, Branch pushen, PR auf den Integrationsbranch erstellen, Titel `Konzept: <Name des Milestones>`. Der PR enthält nur Dateien unter `docs/`.
9. **Board.** Nach `ccmas:ticket`:
   - Kam eine Beschreibung statt eines Milestones: zuerst den Milestone im Backlog anlegen.
   - Am Milestone die Beschreibung ergänzen: das Konzept in Kurzform (Ergebnisziel, gewünschtes Verhalten, Tech-Setup in je wenigen Sätzen) und der Pfad zur Konzeptseite. Der ursprüngliche Text bleibt darüber stehen.
   - Ein Ticket `Konzept <Name des Milestones> dokumentieren` für den Doku-PR: Typ Concept, Meilenstein gesetzt, Risikoklasse R0, Feld PR gesetzt, Spalte To Review, „Am Zug“ Reviewer, „Ausgang“ Fertig. Akzeptanzkriterien in der Beschreibung:
     - Die Konzeptseite enthält alle Abschnitte der Gliederung.
     - Jede Wahl im Tech-Setup hat Version, Begründung und Quelle.
     - Jeder Task im Board steht in der Konzeptseite und umgekehrt.
     - Das Konzept widerspricht keiner Grundentscheidung der Roadmap.
   - Je Task ein Ticket im Backlog mit der Beschreibung aus Schritt 6, Typ, Meilenstein und Risikoklasse. Abhängigkeiten zwischen den Tasks setzen.
   - Jeder Task ohne andere Abhängigkeit hängt vom Ticket `Konzept … dokumentieren` ab. So beginnt kein Worker, bevor das Konzept im Integrationsbranch liegt.
   - Übergabe-Kommentar am Ticket `Konzept … dokumentieren`, mit Branch, Commit, PR und dem Namen des Worktrees. Übergabe-Kommentar am Milestone: ausgeplant, Zahl der Tasks, ID des Konzept-Tickets.

## Wenn es nicht weitergeht

- **Der Mensch ist nicht da oder gibt nicht frei:** Lege keine Tasks an. Committe und pushe den Entwurf, lege am Milestone die Rückfrage `Freigabe: Konzept <Name des Milestones>` nach `ccmas:ticket` an und halte an.
- **Eine Grundentscheidung der Roadmap passt nicht:** Entscheide sie nicht um. Frage den Menschen; die Roadmap wird dann in diesem PR mitgeändert.
- **Die Recherche ergibt, dass etwas so nicht geht:** Sage es dem Menschen mit Quelle und zwei Alternativen. Schreibe kein Konzept, das du nicht für umsetzbar hältst.

## Grenzen

- Genau ein Milestone je Sitzung. Spätere Milestones planst du nicht mit aus.
- Kein Code. Nur Dateien unter `docs/`.
- Keine Umsetzung beschreiben: keine Dateilisten, keine Arbeitsschritte.
- Nichts im Hauptverzeichnis ändern, kein Push auf `main` oder den Integrationsbranch, kein Merge.

## Abschluss

Nenne am Ende: den Milestone, das Tech-Setup in zwei bis drei Sätzen, die Tasks in Reihenfolge mit ihren IDs und Abhängigkeiten, den PR, die ID des Konzept-Tickets, den Namen des Worktrees und was offen ist.
