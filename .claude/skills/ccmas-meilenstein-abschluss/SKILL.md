---
name: ccmas-meilenstein-abschluss
description: Meilensteine eines Projekts mit Asana (führend) und GitHub-Milestones abschließen oder verschieben – Definition of Done abgleichen, Freigabe beim Projektinhaber anfordern, nach der Abnahme in beiden Systemen schließen, bei Verzug den Meilenstein samt aller Folgemeilensteine und Tickets um denselben Versatz verschieben und den Projektplan nachziehen. Verwenden, wenn ein Meilenstein fertig wirkt, abgenommen wurde, ein Termin gerissen ist, oder der Nutzer fragt „ist M09 durch?“, „schließ den Meilenstein“, „schieb den Plan“.
---

# Meilenstein abschließen oder verschieben

Ein Meilenstein existiert doppelt: als Meilenstein-Task in Asana (führend, mit Definition of Done und Abnahme) und
als Milestone in GitHub (zeigt den Code-Fortschritt). Beide heißen gleich und tragen denselben Termin. Dieser Skill
hält sie beim Abschluss und beim Verschieben synchron.

## Projektkontext zuerst

Aus der Regeldatei und dem Projektplan lesen: Meilenstein-Regeln (Benennung, Termine, Takt, Verschieben,
Abnahme), Definition of Done je Meilenstein, Terminliste, Reihenfolge und Abhängigkeiten, Prüfbefehle des Repos
(z. B. lint, typecheck, test, e2e, build), Repo-Name. Asana-Schreibaktionen nach Skill `ccmas-asana-ticket`,
Obsidian nach Skill `ccmas-obsidian-seite`.

GitHub-Milestones gehen nur über die REST-API (`gh api`); die GitHub-MCP-Tools kennen keine Milestones, und GraphQL
ist in Claude-Code-Sitzungen gesperrt.

## A. Abnahmereif machen

1. **Stand erheben**
   - Asana: alle Tickets des Meilensteins und die Abhängigkeiten und Unteraufgaben des Meilenstein-Tasks. Mit
     `starts_with` statt `value` suchen (`{"<Meilenstein-GID>.starts_with":"M09"}`), denn abweichend befüllte Werte
     („M09 Seitentyp …“ statt „M09“) fallen sonst durch. Solche Werte auf die Kurzform korrigieren.
     Offene Tickets auflisten; Tickets mit `Für Später` zählen nicht.
   - Unstimmigkeiten mit aufnehmen: erledigte Tickets außerhalb von `Done`, Tickets in `Done`, die nicht erledigt
     sind, Unteraufgaben ohne `Typ`.
   - GitHub: offene PRs und Issues im Milestone:
     `gh api "repos/<owner>/<repo>/issues?milestone=<nummer>&state=open" --jq '.[] | [.number,.title] | @tsv'`.
     Die Nummer liefert `gh api "repos/<owner>/<repo>/milestones?state=all" --jq '.[] | [.number,.title,.state,.due_on] | @tsv'`.
   - Sind noch Tickets offen, ist der Meilenstein nicht abnahmereif. Dann melden, was fehlt, und ggf. Teil C.
2. **Definition of Done abgleichen**, Punkt für Punkt, jeweils mit Beleg: Testlauf mit Befehl und Ergebnis, PR,
   Screenshot, Obsidian-Abschnitt. Prüfungen selbst ausführen, wenn die Umgebung es erlaubt. Was nicht ausführbar
   war, ausdrücklich als „nicht geprüft“ führen, nicht als erfüllt.
3. **Dokumentation** auf den tatsächlich umgesetzten Stand bringen (Abschnitt „umgesetzt“ in der Konzeptseite,
   Abweichungen vom Konzept als Tabelle), wenn das Projekt das vorsieht.
4. **Kommentar am Meilenstein-Task**: DoD-Abgleich als Liste (erfüllt / nicht erfüllt / nicht geprüft, je mit
   Beleg), PRs, Abweichungen, offene Punkte.
5. **Freigabe anfordern**: Unteraufgabe `Freigabe: Mxx <Titel>` an den Projektinhaber (Skill `ccmas-asana-ticket`), mit
   Prüfweg (Befehle, URLs, Screenshots als Anhang oder Ablageort) und der Abweichungsliste. Meilenstein-Task nach
   `Review`.
6. **Nicht selbst schließen.** Der Projektinhaber nimmt ab.

## B. Nach der Abnahme

Auslöser: Die `Freigabe:`-Unteraufgabe ist abgehakt, ohne Änderungswunsch im Kommentar (Auslegung wie Skill
`ccmas-antworten-abholen`).

1. Asana: Kommentar „Abnahme übernommen“ am Meilenstein-Task, Section `Done`, `completed: true`.
2. GitHub: Milestone schließen:
   `gh api -X PATCH repos/<owner>/<repo>/milestones/<nummer> -f state=closed`.
3. Projektplan: Status bzw. Terminzeile des Meilensteins als erledigt kennzeichnen, sofern der Plan das führt.
4. Bericht: welcher Meilenstein als nächster an der Reihe ist und ob er startbereit ist (offene Abhängigkeiten,
   offene Aufgaben an den Projektinhaber).

Gibt es Meilensteine, die in Asana erledigt, in GitHub aber noch offen sind, ist das eine Abweichung: auflisten und
nach Rückfrage gemeinsam schließen.

## C. Verschieben

Auslöser: Ein Meilenstein ist zum Termin nicht abgeschlossen, oder der Projektinhaber verschiebt bewusst.

1. **Versatz bestimmen** in Kalendertagen (neuer Termin minus alter Termin). Bei bewusster Verschiebung gibt der
   Projektinhaber ihn vor; bei Verzug den kleinsten realistischen Versatz vorschlagen.
2. **Betroffene Menge:** der Meilenstein selbst und alle nach der Projektreihenfolge folgenden, nicht erledigten
   Meilensteine. Erledigte Meilensteine und erledigte Tickets behalten ihre Termine.
3. **Neue Termine rechnen, nicht von Hand zählen** (Monatswechsel, Schaltjahre):
   ```bash
   python3 -c "import datetime as d;o=2;[print(x, d.date.fromisoformat(x)+d.timedelta(o)) for x in ['2026-11-06','2026-11-08']]"
   ```
4. **Anwenden**, je Meilenstein:
   - Asana: `due_on` des Meilenstein-Tasks; bei allen offenen Tickets dieses Meilensteins `start_on` und `due_on`
     (in einem Aufruf je Ticket, `start_on` braucht `due_on`). Sammelaufrufe bis 50 Tasks, `failed` prüfen.
   - Aufgaben an den Projektinhaber erhalten das Fenster ihres Tickets.
   - GitHub: `gh api -X PATCH repos/<owner>/<repo>/milestones/<nummer> -f due_on=<JJJJ-MM-TT>T00:00:00Z`; dasselbe
     Format wie die übrigen Milestones verwenden, damit die Darstellung nicht um einen Tag springt.
5. **Projektplan** (Terminliste) nachziehen und **Kommentar** am verschobenen Meilenstein-Task: Grund, Versatz,
   betroffene Meilensteine.
6. **Gegenprüfen**: Termine in Asana, GitHub und Projektplan stimmen für jeden betroffenen Meilenstein überein.

## Bericht

- Abschluss: DoD-Ergebnis, Freigabe-Aufgabe mit ID, offene Punkte.
- Nach Abnahme: geschlossen in Asana und GitHub, nächster Meilenstein.
- Verschieben: Versatz, Tabelle alt → neu je Meilenstein, Zahl der geänderten Tickets.
