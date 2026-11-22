---
name: project-planning
description: Eine Projektbeschreibung im Gespräch mit dem Menschen in eine Roadmap aus Milestones zerlegen, von klein nach groß geordnet. Schreibt die Roadmap nach docs/, bringt sie als Doku-PR in den Integrationsbranch und legt die Milestones im Board an. Legt keine Tasks an. Für die Rolle Planner.
argument-hint: <Projektbeschreibung oder Pfad zu einer Datei>
---

# Projekt in Milestones zerlegen

Eingabe aus dem Auftrag ($ARGUMENTS): die Beschreibung des Projekts als Text oder der Pfad zu einer Datei. Fehlt sie, frage danach.

Du arbeitest im Gespräch mit dem Menschen. Ohne seine Freigabe legst du nichts im Board an und mergst nichts.

## Voraussetzungen

1. Du stehst in einem eigenen Worktree, nicht im Hauptverzeichnis: `git rev-parse --git-dir` und `git rev-parse --git-common-dir` liefern verschiedene Pfade.
2. Der aktuelle Branch ist ein eigener Branch, weder `main` noch der Integrationsbranch.

Trifft eine nicht zu, ändere nichts und nenne den Grund.

## Schritte

1. **Lesen.** Die Projektbeschreibung, die `CLAUDE.md`, `docs/` und, wenn es schon Code gibt, dessen Aufbau. Lies im Board nach `ccmas:ticket`, welche Milestones es schon gibt.
2. **Klären.** Frage den Menschen, bis feststeht: Was ist das Ziel? Was gehört ausdrücklich nicht dazu? Welcher Rahmen gilt (Plattform, Betrieb, vorhandene Systeme, Vorgaben)? Stelle Fragen gebündelt und mit einer Empfehlung.
3. **Grundentscheidungen festhalten.** Was für das ganze Projekt gilt: Tech-Stack, grobe Architektur, Betrieb. Wo du etwas nicht sicher weißt, recherchiere im Netz, was es gibt und was aktuell ist, und nenne die Quelle. Jede Entscheidung bekommt eine Begründung. Entscheidet der Mensch anders als deine Empfehlung, gilt seine Entscheidung.
4. **In Milestones zerlegen.** Jeder Milestone bekommt:
   - ein **Ergebnisziel**, das sich prüfen lässt („Ein Nutzer kann sich anmelden“, nicht „Anmeldung bauen“),
   - **Umfang** und **Nicht-Umfang** in wenigen Sätzen,
   - eine **Einordnung**: Art (Grundlage, Unit, Zusammenführung), Größe (klein, mittel, groß), Unsicherheit (bekannt, zu klären),
   - seine **Abhängigkeiten** zu anderen Milestones.
5. **Von klein nach groß ordnen.**
   - Zuerst die Grundlagen: Projektgerüst, Prüfbefehle, was alle Units brauchen.
   - Dann die einzelnen Units, jede für sich lauffähig und geprüft.
   - Danach die Zusammenführungen: Units werden Stück für Stück zu einer funktionierenden Software zusammengesetzt.
   - Kein Milestone setzt etwas zusammen, das nicht vorher als Unit steht.
   - Innerhalb dieser Ordnung kommt Unsicheres früh. Was nebeneinander laufen kann, kennzeichnest du als parallelen Weg.
6. **Freigabe der Roadmap.** Zeige dem Menschen die Roadmap: Grundentscheidungen, Milestones in Reihenfolge, parallele Wege. Arbeite seine Änderungen ein, bis er freigibt.
7. **Roadmap schreiben.** Schreibe `docs/roadmap.md` nach dieser Gliederung:

   ```text
   # Roadmap
   ## Ziel
   ## Nicht-Ziele
   ## Grundentscheidungen
   | Entscheidung | Begründung | Quelle |
   ## Milestones
   | Nr. | Milestone | Ergebnisziel | Art | Größe | Unsicherheit | Hängt ab von |
   ## Parallele Wege
   ```

   Gibt es die Datei schon, überarbeite sie, statt eine zweite anzulegen.
8. **Doku-PR.** Committen, Branch pushen, PR auf den Integrationsbranch erstellen, Titel `Roadmap: <Projektname>`. Der PR enthält nur Dateien unter `docs/`. Hat das Projekt laut `CLAUDE.md` CI am PR, warte, bis sie grün ist.
9. **Mergen.** Nenne dem Menschen den PR. Nach seinem Ja integrierst du ihn in den Integrationsbranch (`gh pr merge <PR> --squash`) und prüfst, dass er als gemergt geführt wird.
10. **Board.** Erst jetzt, nach `ccmas:ticket`: je Milestone ein Milestone im Backlog. Name wie in der Roadmap. Beschreibung: Ergebnisziel, Umfang, Nicht-Umfang, Einordnung. Abhängigkeiten zwischen den Milestones setzen.

## Wenn es nicht weitergeht

- **Der Mensch ist nicht da oder gibt nicht frei:** Lege nichts im Board an und merge nichts. Committe und pushe den Entwurf, damit nichts verloren geht, und nenne im Abschluss, was offen ist.
- **Eine Grundentscheidung braucht mehr Klärung, als die Sitzung leisten kann:** Halte sie in der Roadmap unter „Grundentscheidungen“ als offen fest und lege den Milestone, der sie klärt, an den Anfang.
- **Die CI am Doku-PR ist rot:** Nicht mergen. Behebe, was an deinen Dateien liegt; sonst nenne die Ursache.

## Grenzen

- Keine Tasks anlegen und keinen Milestone ausplanen. Das ist die Aufgabe von `ccmas:feature-planning`.
- Kein Code. Nur Dateien unter `docs/`.
- Nichts im Hauptverzeichnis ändern, kein Push auf `main` oder den Integrationsbranch, kein `--force`.
- Du mergst nur deinen eigenen Doku-PR, und nur nach dem Ja des Menschen in dieser Sitzung.

## Abschluss

Nenne am Ende: die Milestones in Reihenfolge mit ihren IDs, die Grundentscheidungen, den PR mit Merge-Commit und was offen ist. Den Worktree räumt der Supervisor in seiner nächsten Runde auf.
