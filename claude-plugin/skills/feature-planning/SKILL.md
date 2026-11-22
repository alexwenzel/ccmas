---
name: feature-planning
description: Einen Milestone (Epic, Feature) im Gespräch mit dem Menschen konzipieren und in Tasks zerlegen, oder ein einzelnes Ticket aus einer Beschreibung ausplanen. Legt Tech-Setup und gewünschtes Verhalten fest, recherchiert im Netz, bringt das Konzept als Doku-PR in den Integrationsbranch und legt die Tasks mit Szenarien und Akzeptanzkriterien im Board an. Für die Rolle Planner.
argument-hint: <Milestone-ID oder Beschreibung>
---

# Milestone konzipieren und in Tasks zerlegen

Eingabe aus dem Auftrag ($ARGUMENTS): die ID eines Milestones oder eine Beschreibung.

Du planst genau einen Milestone. Du arbeitest im Gespräch mit dem Menschen. Ohne seine Freigabe legst du nichts im Board an und mergst nichts.

Du legst fest, **was** gewünscht ist und **womit**: Tech-Setup und Verhalten. Du beschreibst nicht, **wie** es im Code umgesetzt wird: keine Dateien, keine Arbeitsschritte. Das entscheidet der Worker am einzelnen Task.

## Eingabe bestimmen

- **Milestone-ID:** Arbeite mit diesem Milestone.
- **Beschreibung eines Features:** Sie wird nach der Freigabe als Milestone im Backlog angelegt (Schritt 10). Der Text des Menschen kommt unverändert in dessen Beschreibung.
- **Beschreibung eines einzelnen Tickets** (ein Fehler, eine kleine Änderung): Es entsteht kein Konzept und kein Doku-PR. Arbeite nach „Einzelnes Ticket“.

## Voraussetzungen

1. Du stehst in einem eigenen Worktree, nicht im Hauptverzeichnis: `git rev-parse --git-dir` und `git rev-parse --git-common-dir` liefern verschiedene Pfade.
2. Der aktuelle Branch ist ein eigener Branch, weder `main` noch der Integrationsbranch.
3. Der Milestone hat noch keine Tasks. Hat er welche, arbeite nach „Nachziehen“ und lege nichts doppelt an.

Trifft die erste oder zweite nicht zu, ändere nichts und nenne den Grund. Für ein einzelnes Ticket gelten sie nicht, weil du dabei keine Datei änderst.

## Schritte

1. **Verstehen.** Lies den Milestone nach `ccmas:ticket`, dazu `CLAUDE.md`, `docs/roadmap.md`, die übrige Dokumentation unter `docs/` und die Stellen im Code, auf denen der Milestone aufbaut. Was die Roadmap als Grundentscheidung festlegt, gilt und wird nicht neu entschieden.
2. **Klären.** Frage den Menschen nach allem, was das Verhalten bestimmt und nicht in Milestone, Roadmap oder Code steht. Fragen gebündelt, mit Empfehlung.
3. **Recherchieren.** Prüfe im Netz, was es für die Aufgabe gibt und was aktuell ist: Bibliotheken und ihre aktuelle Version, bekannte Grenzen, übliche Lösungen. Lies die Dokumentation der Bibliotheken, die du vorschlägst. Notiere die Quellen. Das Konzept muss realistisch sein: Es passt zum vorhandenen Code und zum Stand der Technik, und nichts darin ist geraten.
4. **Konzept entwerfen.** Gliederung:

   ```text
   # <Name des Milestones>
   ## Ergebnisziel
   ## Gewünschtes Verhalten
   <Was das System danach kann, aus Sicht dessen, der es benutzt. Schnittstellen, Daten, Fehlerfälle.>
   ## Tech-Setup
   | Baustein | Wahl | Version | Begründung | Quelle |
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
   - Ein Task ist die Arbeit eines Workers und ergibt einen PR. Braucht er mehr als etwa einen Tag, teile ihn.
   - Jeder Task ist für sich prüfbar und lässt den Integrationsbranch lauffähig.
   - Von klein nach groß: erst die einzelnen Logiken, dann die Tasks, die sie zusammensetzen.
   - Die Reihenfolge steht in den Abhängigkeiten. Was unabhängig ist, bleibt unabhängig, damit es parallel laufen kann.
   - **Der letzte Task ist der Abnahmetest.** Er hängt von allen anderen Tasks ab und prüft das Ergebnisziel des Milestones. Seine Szenarien sind die Prüfungen des Ergebnisziels; Befehle und Ausgaben hält der Worker im PR fest.
6. **Tasks ausformulieren.** Jeder Task bekommt diese Beschreibung:

   ```text
   ## Ziel
   <ein bis drei Sätze>

   ## Gewünschtes Verhalten
   <Schnittstelle, Eingaben, Ausgaben, Fehlerfälle>

   ## Technische Vorgaben
   <was aus dem Konzept für diesen Task gilt, mit Verweis auf den Abschnitt in docs/milestones/<datei>.md>

   ## Szenarien
   - Gegeben <Ausgangslage>, wenn <Eingabe oder Ereignis>, dann <erwartetes Ergebnis>

   ## Akzeptanzkriterien
   - [ ] <prüfbare Aussage>
   ```

   - **Szenarien** sind die bekannten Fälle: der Normalfall, die Grenzfälle, die Fehlerfälle. Der Worker macht aus jedem zuerst einen Test und schreibt danach den Code.
   - **Akzeptanzkriterien** sind einzeln prüfbar: ein Befehl, ein Test oder eine beobachtbare Ausgabe. „Funktioniert gut“ ist kein Kriterium. Du schreibst sie, nicht die Rolle, die später umsetzt.
   - Der Task ist damit ausgeplant. Der Worker prüft vor dem ersten Code nur noch, ob er in diesem Code so umsetzbar ist.
7. **Freigabe.** Zeige dem Menschen das Konzept und die Taskliste. Arbeite seine Änderungen ein, bis er beides freigibt.
8. **Konzept schreiben und Doku-PR.** Schreibe das Konzept nach `docs/milestones/<kurzname>.md`. Committen, Branch pushen, PR auf den Integrationsbranch erstellen, Titel `Konzept: <Name des Milestones>`. Der PR enthält nur Dateien unter `docs/`. Hat das Projekt laut `CLAUDE.md` CI am PR, warte, bis sie grün ist.
9. **Mergen.** Nenne dem Menschen den PR. Nach seinem Ja integrierst du ihn in den Integrationsbranch (`gh pr merge <PR> --squash`) und prüfst, dass er als gemergt geführt wird. So liegt das Konzept im Integrationsbranch, bevor ein Worker beginnt.
10. **Board.** Erst jetzt, nach `ccmas:ticket`:
    - Kam die Beschreibung eines Features statt eines Milestones: zuerst den Milestone im Backlog anlegen.
    - Am Milestone die Beschreibung ergänzen: das Konzept in Kurzform (Ergebnisziel, gewünschtes Verhalten, Tech-Setup in je wenigen Sätzen) und der Pfad zur Konzeptseite. Der ursprüngliche Text bleibt darüber stehen.
    - Je Task ein Ticket in To Do mit der Beschreibung aus Schritt 6, Typ und Meilenstein. Abhängigkeiten zwischen den Tasks setzen.
    - Ein Kommentar am Milestone: ausgeplant, Zahl der Tasks, PR des Konzepts.

## Einzelnes Ticket

1. Lies `CLAUDE.md`, `docs/` und die betroffenen Stellen im Code. Kläre mit dem Menschen, was fehlt.
2. Formuliere den Task wie in Schritt 6. Bei einem Fehler beschreibt das erste Szenario, wie er sich zeigt.
3. Nach der Freigabe des Menschen legst du den Task in To Do an, mit Typ und, wenn er zu einem Milestone gehört, Meilenstein.

Liegt das Ticket schon im Backlog, ergänzt du seine Beschreibung und verschiebst es nach To Do, statt ein zweites anzulegen.

## Nachziehen

Der Milestone hat schon Tasks, und eine Antwort des Menschen auf eine Rückfrage ändert das Konzept:

1. Lies die Rückfrage und die Antwort am Task.
2. Ändere das Konzept und bringe es als Doku-PR in den Integrationsbranch wie in den Schritten 7 bis 9.
3. Passe die Tasks an, an denen noch niemand arbeitet (Backlog, To Do). Tasks in In Progress, To Review oder Done änderst du nicht; was dort nachzuholen ist, wird ein neuer Task.

## Wenn es nicht weitergeht

- **Der Mensch ist nicht da oder gibt nicht frei:** Lege keine Tasks an und merge nichts. Committe und pushe den Entwurf, lege am Milestone die Rückfrage `Freigabe: Konzept <Name des Milestones>` nach `ccmas:ticket` an und höre auf.
- **Eine Grundentscheidung der Roadmap passt nicht:** Entscheide sie nicht um. Frage den Menschen; die Roadmap wird dann in diesem PR mitgeändert.
- **Die Recherche ergibt, dass etwas so nicht geht:** Sage es dem Menschen mit Quelle und zwei Alternativen. Schreibe kein Konzept, das du nicht für umsetzbar hältst.
- **Die CI am Doku-PR ist rot:** Nicht mergen. Behebe, was an deinen Dateien liegt; sonst nenne die Ursache.

## Grenzen

- Genau ein Milestone je Sitzung. Spätere Milestones planst du nicht mit aus.
- Kein Code. Nur Dateien unter `docs/`.
- Keine Umsetzung beschreiben: keine Dateilisten, keine Arbeitsschritte.
- Nichts im Hauptverzeichnis ändern, kein Push auf `main` oder den Integrationsbranch, kein `--force`.
- Du mergst nur deinen eigenen Doku-PR, und nur nach dem Ja des Menschen in dieser Sitzung.

## Abschluss

Nenne am Ende: den Milestone, das Tech-Setup in zwei bis drei Sätzen, die Tasks in Reihenfolge mit ihren IDs und Abhängigkeiten, den PR mit Merge-Commit und was offen ist. Den Worktree räumt der Supervisor in seiner nächsten Runde auf.
