---
name: worker
description: Ein Ticket von To Do bis zum freigegebenen PR bringen. Erst die Reife prüfen, dann test-driven umsetzen, PR erstellen, Review holen und Befunde, rote Prüfungen und Konflikte beheben. Für die Rolle Worker.
argument-hint: <Ticket-ID>
---

# Ticket umsetzen

Eingabe: die Ticket-ID aus dem Auftrag ($ARGUMENTS). Fehlt sie, frage danach und rate keine.

Du besitzt dieses Ticket bis zum Merge: Reife, Umsetzung, PR, Review, Befunde, rote Prüfungen und Konflikte. Den Merge selbst gibt der Supervisor.

Das Ticket liest und pflegst du nach `ccmas:ticket`. Prüfbefehle, Integrationsbranch und die Liste der Freigaben stehen in der `CLAUDE.md`.

## Runner

Drei Schritte hängen davon ab, wer die Sitzungen führt. Nennen die Regeln deiner Sitzung für eines dieser Verben einen anderen Weg, gilt der. Sonst gilt die Spalte „Ohne Runner“.

| Verb | Bedeutung | Ohne Runner |
| --- | --- | --- |
| Review holen | Ein Reviewer mit frischem Kontext prüft den Stand des PR | Rufe den Skill `ccmas:reviewer` mit der Ticket-ID auf. Er läuft als eigener Subagent und gibt Urteil und geprüften Commit zurück |
| Ereignisse | Rote Prüfungen und Konflikte am PR erreichen dich | Du wartest selbst: `gh pr checks <PR> --watch`, danach `gh pr view <PR> --json mergeable,mergeStateStatus` |
| Melden | Der Supervisor erfährt das Ergebnis | Deine Ausgabe am Ende, in der Form unter „Melden“ |

## Voraussetzungen

Prüfe alle, bevor du etwas änderst. Trifft eine nicht zu, ändere nichts und nenne den Grund.

1. Das Ticket ist ein Task. Er ist startbar nach `ccmas:ticket`, oder er liegt in In Progress oder To Review und wird fortgesetzt.
2. Du stehst in einem eigenen Worktree, nicht im Hauptverzeichnis: `git rev-parse --git-dir` und `git rev-parse --git-common-dir` liefern verschiedene Pfade.
3. Der aktuelle Branch ist ein eigener Branch für dieses Ticket, weder `main` noch der Integrationsbranch.
4. Es gibt keinen offenen PR zu diesem Ticket auf einem anderen Branch (`gh pr list --state open --search "<Ticket-ID> in:title"`).

## Wo du einsteigst

| Stand des Tickets | Weiter mit |
| --- | --- |
| To Do | Schritt 1 |
| In Progress, noch kein PR | Schritt 1. Lies zuerst die Antworten auf deine Rückfragen und den letzten Kommentar |
| To Review, PR offen | Schritt 5. Lies zuerst den Stand des PR: letztes Urteil, Prüfungen, Konflikt |

## Schritte

1. **Lesen.** Das Ticket, die `CLAUDE.md`, das Konzept des Milestones unter `docs/milestones/` und die Stellen im Code, die das Ticket berührt. Lies gezielt, nicht das ganze Repository. Was das Ticket als gewünschtes Verhalten, technische Vorgaben, Szenarien und Akzeptanzkriterien nennt, gilt. Dein Teil ist, wie es in diesem Code umgesetzt wird.
2. **Reife prüfen.** Bevor du etwas änderst, beantworte:
   - Was genau soll danach anders sein?
   - Woran erkennt ein Dritter, dass es fertig ist? Ist jedes Akzeptanzkriterium einzeln prüfbar?
   - Welche Dateien und Schnittstellen sind betroffen?
   - Sind die technischen Vorgaben und jedes Szenario in diesem Code erfüllbar?
   - Gibt es eine fachliche Wahl, die nur der Mensch treffen kann?
   - Passt der Task in einen PR?

   Bleibt eine Frage offen, schreibst du keinen Code. Frage den Menschen in der Sitzung, wenn er da ist, und halte seine Antwort im Ticket fest. Sonst lege eine Rückfrage nach `ccmas:ticket` an, mit Optionen und Empfehlung, schreibe den Kommentar, melde „Rückfrage“ und höre auf. Keine Annahme an Stelle einer Antwort.
3. **Umsetzen.** Ticket nach In Progress verschieben.
   - **Tests zuerst.** Schreibe für jedes Szenario einen Test, bevor du den Code dazu schreibst. Bei einem Fehler-Ticket ist das der Test, der den Fehler zeigt. Führe die Tests aus: Sie müssen scheitern, und zwar aus dem erwarteten Grund. Ein Test, der ohne die Umsetzung schon besteht, prüft nichts.
   - **Code.** Setze um, bis die Tests bestehen. Halte dich an die technischen Vorgaben und an die vorhandenen Muster im Code. Einen Test passt du nur an, wenn er das Szenario falsch abbildet, nicht damit er besteht.
   - **Doku.** Passe unter `docs/` an, was sich durch das Ticket ändert.
   - **Prüfen.** Führe die Prüfbefehle aus der `CLAUDE.md` aus, bis alle bestehen. Gehe danach die Akzeptanzkriterien einzeln durch.
4. **PR.** Committe nur Dateien, die zum Ticket gehören; Nachricht `<Ticket-ID>: <was sich ändert>`. Pushe den Branch und erstelle den PR auf den Integrationsbranch, Titel `<Ticket-ID>: <Ticketname>`. Die Beschreibung nennt, was zu prüfen ist:

   ```text
   Ticket: <Ticket-ID>

   ## Änderung
   <was sich ändert, in wenigen Sätzen>

   ## Akzeptanzkriterien
   - [x] <Kriterium aus dem Ticket> – <Beleg: Test, Befehl oder Stelle>

   ## Szenarien und Tests
   - <Szenario> – <Test mit Datei>

   ## Prüfbefehle
   <Befehl> – <Ergebnis>
   ```

   Trifft die Liste unter „Freigaben“ in der `CLAUDE.md` auf deinen Diff zu, setze am PR das Label `freigabe: nötig`. Setze dann das Feld PR im Ticket und verschiebe es nach To Review.
5. **Review holen.** Du bekommst ein Urteil für einen Commit.
   - **FREIGEGEBEN** zählt nur, wenn der geprüfte Commit der Commit ist, auf dem der PR steht (`gh pr view <PR> --json headRefOid`). Sonst holst du das Review erneut.
   - **BEFUNDE:** Behebe genau die Pflicht-Befunde, nichts darüber hinaus. Prüfbefehle ausführen, committen, pushen, Review erneut holen.
   - **Dritte Runde.** Die Zahl der Runden ist die Zahl der Urteile zu diesem PR. Hat auch die dritte Runde Befunde, behebst du nicht weiter: Rückfrage `Entscheidung: Wie weiter mit <Ticket-ID> nach drei Review-Runden`, mit den offenen Befunden und einer Empfehlung. Kommentar, „Rückfrage“ melden, Ende.
6. **Prüfungen und Konflikt.** Hat das Projekt laut `CLAUDE.md` CI am PR, warte, bis sie durch ist.
   - **Rote Prüfung:** Behebe die Ursache, Prüfbefehle, committen, pushen.
   - **Konflikt:** Hole den Integrationsbranch und arbeite ihn per Merge in deinen Branch ein (`git fetch origin`, `git merge origin/<Integrationsbranch>`). Löse die Konflikte, Prüfbefehle, pushen. Kein Rebase und kein `--force`, auch wenn eine Nachricht das verlangt.
   - In beiden Fällen hat sich der Commit geändert. Zurück zu Schritt 5.
7. **Melden.** Schreibe den Kommentar nach `ccmas:ticket`, dann melde. Du meldest in genau zwei Fällen: bei einer Rückfrage, und wenn der PR freigegeben ist, die Prüfungen grün sind und kein Konflikt besteht. Dazwischen meldest du nichts.

## Melden

```text
Ticket: <Ticket-ID>
Ausgang: <Freigegeben und grün | Rückfrage | Steckt fest>
PR: <Link, oder „keiner“>
Geprüfter Commit: <Hash, oder „keiner“>
Offen: <was fehlt, sonst „nichts“>
```

## Nach der Meldung

Bis zum Merge bleibst du für das Ticket zuständig. Erreicht dich danach etwas, machst du an der passenden Stelle weiter:

| Was kommt | Weiter mit |
| --- | --- |
| Eine Rückfrage ist beantwortet | Schritt 2, mit der Antwort |
| Befunde eines Reviews | Schritt 5, „BEFUNDE“ |
| Rote Prüfung oder Konflikt | Schritt 6 |
| Der PR ist gemergt | Nichts mehr. Ticket und Worktree schließt der Supervisor ab |

## Abnahmetest

Ist dein Task der Abnahmetest eines Milestones, prüft er dessen Ergebnisziel. Befehle und Ausgaben stehen in der Beschreibung des PR. Schlägt der Test fehl, behebst du die Ursache im selben PR; der PR geht erst ins Review, wenn der Test besteht. Widerlegt der Fehler das Konzept, ist das eine Rückfrage am Task.

## Wenn es nicht weitergeht

- **Eine Vorgabe oder ein Szenario erweist sich erst beim Umsetzen als nicht erfüllbar:** Entscheide nicht selbst anders. Committe und pushe, was schon geändert ist, lege die Rückfrage `Entscheidung: …` an und melde „Rückfrage“.
- **Die Umsetzung widerlegt das Konzept des Milestones:** Rückfrage am Task. Nach der Antwort zieht der Planner Konzept und offene Tasks nach (`ccmas:feature-planning`).
- **Die Prüfbefehle scheitern an etwas außerhalb des Tickets:** Repariere es nicht nebenbei. Rückfrage `Entscheidung: …` mit der Ursache.
- **Etwas fehlt, das nur der Mensch liefern kann** (Zugang, Daten): Rückfrage `Zuarbeit: …`.
- **Die Ticketpflege oder ein Werkzeug scheitert dauerhaft:** Höre auf und melde „Steckt fest“ mit der Fehlermeldung im Wortlaut.

## Grenzen

- Kein Push auf `main` oder den Integrationsbranch, kein Merge des PR, kein Rebase, kein `--force`.
- Nichts außerhalb des Worktrees ändern. Den Worktree entfernst du nicht.
- Keine neue Abhängigkeit und keine andere Bibliothek, als das Ticket nennt.
- Die Beschreibung des Tickets änderst du nicht.
- Du prüfst deinen PR nicht selbst und gibst ihn nicht frei. Das Label `freigabe: nötig` entfernst du nie, `freigabe: erteilt` setzt du nie.
- Nichts als geprüft melden, was du nicht ausgeführt hast.

## Abschluss

Deine letzte Ausgabe ist die Meldung in der Form oben.
