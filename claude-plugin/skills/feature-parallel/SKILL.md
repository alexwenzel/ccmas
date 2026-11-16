---
name: feature-parallel
description: Ein umsetzungsreifes Ticket im Worktree test-driven umsetzen. Erst die Tests aus den Szenarien, dann Code, Doku, Commit, Push und PR auf den Integrationsbranch, danach Übergabe an den Reviewer. Auch für das Beheben von Review-Befunden. Für die Rolle Worker.
argument-hint: <Ticket-ID>
---

# Ticket umsetzen

Eingabe: die Ticket-ID aus dem Auftrag ($ARGUMENTS).

## Voraussetzungen

Prüfe alle vier, bevor du etwas änderst. Trifft eine nicht zu, ändere nichts und melde den Ausgang „Problem“ mit dem Grund.

1. Das Ticket liegt in To Do, oder es liegt in In Progress oder To Review und „Am Zug“ ist Worker (Fortsetzung oder Befunde).
2. Bei einem neuen Ticket: Die Beschreibung enthält die Abschnitte „Plan“, „Szenarien“ und „Akzeptanzkriterien“, das Feld Risikoklasse ist gesetzt. Bei Befunden genügen die Akzeptanzkriterien; das gilt auch für das Ticket eines Doku-PR, dessen Befunde du im Worktree dieses PR behebst.
3. Du stehst in einem eigenen Worktree, nicht im Hauptverzeichnis: `git rev-parse --git-dir` und `git rev-parse --git-common-dir` liefern verschiedene Pfade.
4. Der aktuelle Branch ist ein eigener Branch für dieses Ticket, weder `main` noch der Integrationsbranch.

## Schritte bei einem neuen Ticket

1. **Starten.** Ticket nach In Progress verschieben, „Am Zug“ auf Worker.
2. **Tests zuerst.** Schreibe für jedes Szenario des Tickets einen Test, bevor du den Code dazu schreibst. Bei einem Fehler-Ticket ist das der Test, der den Fehler zeigt. Führe die Tests aus: Sie müssen scheitern, und zwar aus dem erwarteten Grund. Ein Test, der ohne die Umsetzung schon besteht, prüft nichts.
3. **Umsetzen.** Folge dem Plan Schritt für Schritt, bis die Tests bestehen. Halte dich an die technischen Vorgaben des Tickets und an die vorhandenen Muster im Code. Die Tests passt du nur an, wenn sie das Szenario falsch abbilden, nicht damit sie bestehen.
4. **Doku.** Passe unter `docs/` an, was der Plan nennt.
5. **Prüfen.** Führe die Prüfbefehle aus der `CLAUDE.md` aus. Behebe, was scheitert, und führe sie erneut aus, bis alle bestehen. Gehe danach die Akzeptanzkriterien einzeln durch.
6. **Committen.** Nur Dateien, die zum Ticket gehören. Nachricht: `<Ticket-ID>: <was sich ändert>`.
7. **Pushen und PR.** Branch pushen. PR auf den Integrationsbranch erstellen, Titel `<Ticket-ID>: <Ticketname>`. Der Text des PR enthält: Link oder ID des Tickets, was sich ändert, die Akzeptanzkriterien als Liste mit Haken, welcher Test welches Szenario abprüft, die ausgeführten Prüfbefehle mit Ergebnis.
8. **Übergeben.** Feld PR mit dem Link setzen, „Am Zug“ auf Reviewer, „Ausgang“ auf Fertig. Ticket nach To Review verschieben. Übergabe-Kommentar mit Branch, Commit und PR schreiben.

## Schritte nach Befunden

Befunde kommen vom Reviewer oder, wenn beim Integrieren etwas auffällt, vom Merger.

1. Lies den letzten Übergabe-Kommentar mit dem Ausgang „Befunde“. Behebe genau die genannten Befunde, nichts darüber hinaus.
2. Prüfen wie oben, committen, pushen. Der PR aktualisiert sich dadurch.
3. „Am Zug“ auf Reviewer, „Ausgang“ auf Fertig. Übergabe-Kommentar: welcher Befund wie behoben wurde, neuer Commit. Die Spalte bleibt To Review.

## Wenn es nicht weitergeht

- **Technische Vorgabe oder Szenario nicht erfüllbar:** Nicht selbst anders entscheiden. Wie beim nicht umsetzbaren Plan zurück an den Planner.
- **Plan nicht umsetzbar oder widersprüchlich:** Nicht umplanen. „Ausgang“ auf Problem, „Am Zug“ auf Planner, im Kommentar die Stelle und den Grund nennen. Bereits Geändertes committen und pushen, damit nichts verloren geht.
- **Etwas fehlt, das nur der Mensch liefern kann:** Rückfrage nach `ccmas:ticket`.
- **Prüfbefehle scheitern an etwas außerhalb des Tickets:** „Ausgang“ auf Blockiert, „Am Zug“ auf Mensch, Ursache im Kommentar.

## Grenzen

- Kein Push auf `main` oder den Integrationsbranch, kein Merge, kein `--force`.
- Nichts außerhalb des Worktrees ändern.
- Keine neue Abhängigkeit und keine andere Bibliothek, als Ticket und Plan nennen.
- Der Worktree bleibt stehen. Reviewer und Merger arbeiten darin weiter; aufgeräumt wird er nach dem Merge vom Supervisor.

## Abschluss

Nenne am Ende: Ticket, Ausgang, Branch, Commit, PR, die ausgeführten Prüfbefehle mit Ergebnis, geänderte Dateien unter `docs/`.
