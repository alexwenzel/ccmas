---
name: ccmas-morgen-briefing
description: Knappes Tages-Briefing für ein Projekt mit Asana, GitHub und Obsidian – was sich seit gestern geändert hat, was heute ansteht, was der Projektinhaber jetzt tun muss und womit es weitergeht, als Einstiegspunkt in den Arbeitstag. Verwenden für den täglichen geplanten Lauf (nach dem Antworten-Abholen) und immer, wenn der Nutzer fragt „wo stehen wir?“, „was ist seit gestern passiert?“, „was steht heute an?“, „womit mache ich weiter?“ oder ein Briefing, Update oder einen Statusüberblick zum Projekt will. Nicht für den allgemeinen persönlichen Morgen-Überblick über Kalender und Mails.
---

# Morgen-Briefing

Ein Einstiegspunkt in den Arbeitstag: in zwei Minuten lesbar, mit klarer Antwort auf „was ist passiert, was muss
ich tun, wie geht es weiter“. Es ersetzt keinen Statusbericht. Was nicht zu einer Entscheidung oder Handlung führt,
bleibt weg.

## Projektkontext zuerst

Aus der Regeldatei des Projekts (meist `CLAUDE.md` im Obsidian-Projektordner) lesen: Asana-Projekt-GID, Sections,
Felder, User-GIDs von Projektinhaber und Agent-Account, Repo, Vault-Ordner, Meilenstein- und Terminregeln,
Zeitzone. Den Projektplan mit der Terminliste der Meilensteine öffnen. Nur lesen; das Briefing schreibt nichts in
Asana, GitHub oder Obsidian.

## Zeitraum

- **Seit:** Zeitpunkt des letzten Briefings, wenn bekannt (geplanter Lauf: letzter Lauf des Tasks). Sonst 24 Stunden.
  Montags das Wochenende einschließen (seit Freitag Morgen).
- **Heute:** Kalenderdatum in der Zeitzone des Projekts, nicht UTC.

## Daten sammeln

Alle Abfragen gezielt mit Zeitfiltern; nicht das ganze Projekt laden.

**Asana** (Skill `ccmas-asana-ticket` für Tool-Details; `opt_fields` mit
`name,completed,completed_at,assignee.name,due_on,memberships.section.name,custom_fields.display_value,parent.name`)

1. Seit gestern erledigt: `search_tasks` mit `completed_at_after`.
2. Seit gestern neu: `search_tasks` mit `created_at_after`.
3. Seit gestern bewegt oder kommentiert: `search_tasks` mit `modified_at_after` und `completed: false`; bei den
   wenigen relevanten Treffern `get_task` für die letzten Kommentare. Kommentare des Projektinhabers und
   „Antwort übernommen“-Kommentare des Agenten zählen, reine Feld-Updates nicht.
4. Wartet auf den Projektinhaber: offene Aufgaben mit `assignee_any` = Projektinhaber. Blockierend sind Aufgaben mit
   Label `Frage` oder `Wartet auf Zugang/Daten` oder mit abhängigen Tickets des Agenten; den Rest als „kann warten“.
   Tickets mit `Für Später` nicht aufführen.
5. Heute fällig oder überfällig: `due_on_before` = morgen und `completed: false`.
6. In Arbeit: Section `In Progress` und `Review`.

Der Filter `sections_any` greift in Kombination mit anderen Filtern nicht zuverlässig. Section deshalb über
`memberships.section.name` im Ergebnis nachfiltern.

**GitHub** (`gh api`, REST; GraphQL ist in Claude-Code-Sitzungen gesperrt)

- Seit gestern gemergt:
  `gh api "search/issues?q=repo:<owner>/<repo>+is:pr+is:merged+merged:>=<JJJJ-MM-TT>" --jq '.items[] | [.number,.title] | @tsv'`
- Offene PRs: `gh api "repos/<owner>/<repo>/pulls?state=open" --jq '.[] | [.number,.title,.head.ref,.draft] | @tsv'`
- Bei offenen PRs, die auf Freigabe oder Tests warten, kurz den Grund (Status-Checks, Beschreibung).

**Obsidian** (Skill `ccmas-obsidian-seite` für den Zugriff)

- Seit gestern geänderte Seiten: `find . -name "*.md" -newermt "<JJJJ-MM-TT HH:MM>"` im Projektordner, dazu die
  Property `version`. Nur Seiten mit inhaltlicher Änderung nennen.
- Ohne Vault-Zugriff diesen Teil weglassen und im Briefing einen Satz dazu.

**Meilensteine:** aktueller und nächster Meilenstein aus der Terminliste; Termin heute oder überschritten,
Meilensteine in `Review` (warten auf Abnahme).

## Bewerten

Bevor geschrieben wird, die Daten zu einer Lage verdichten:

- **Was zählt seit gestern?** Abgeschlossene Tickets und Meilensteine, gemergte PRs, getroffene Entscheidungen, neue
  Rückfragen, neue Blocker. Gleichartiges zusammenfassen („5 M09-Tickets erledigt, PRs #45–#49“).
- **Wer blockiert wen?** Eine Aufgabe des Projektinhabers, von der Agentenarbeit abhängt, steht über allem anderen.
  Reihenfolge danach: Blocker, älteste Wartezeit, nächster Meilenstein.
- **Ist der Plan noch zu halten?** Überfällige Meilensteine oder Tickets nennen und einen Satz zum Vorgehen
  (Verschieben nach Skill `ccmas-meilenstein-abschluss` oder Nachziehen). Ein Meilenstein, der in den nächsten Tagen fällig
  ist, aber noch keine Tickets hat, ist ein Planrisiko: Er braucht zuerst eine Detailplanung (Skill
  `ccmas-concept-ticket`).
- **Bündeln:** Gleichartige Aufgaben an den Projektinhaber in eine Zeile („7 Freigaben M09–M15: SC-202, …“).
  Entscheidungen vor Freigaben, weil eine Entscheidung die Umsetzung noch ändern kann, die freigegeben werden soll.
- **Unstimmigkeiten**, die beim Sammeln auffallen (erledigt, aber nicht in Done; Feldwerte abweichend), höchstens
  in einer Zeile unter „Hinweise“.

## Format

Knapp, Deutsch, auf dem Handy lesbar. Feste Reihenfolge, leere Abschnitte weglassen. Jede Ticket-, PR- und
Meilenstein-Nennung als Link, damit der Projektinhaber direkt springen kann
(`https://app.asana.com/0/<Projekt-GID>/<Task-GID>`, PR-URL).

```markdown
# Briefing <Projekt> – <Wochentag, TT.MM.>

**Lage:** Ein bis zwei Sätze: wo das Projekt steht und was heute das Wichtigste ist.

## Seit gestern

- höchstens 5 Punkte, gebündelt, mit Links

## Du bist dran

1. Blockierendes zuerst: was zu tun ist, Link, und was es freigibt („→ entsperrt M10“)
2. …
   (Kann warten: n weitere, Links)

## Heute und als Nächstes

- fällige oder überfällige Meilensteine und Tickets
- was der Agent als Nächstes umsetzt bzw. umsetzen kann, sobald du geantwortet hast

## Einstieg

Ein konkreter Vorschlag, womit die Sitzung beginnt, als Satz zum Kopieren:
„<z. B. Gib M09 frei (SC-202), dann starte M16 mit „Lass uns M16 vorbereiten“.>“

Hinweise: höchstens eine Zeile, nur wenn nötig.
```

Regeln für den Inhalt:

- **Höchstens etwa 25 Zeilen.** Wird es mehr, bündeln statt kürzen; Details stehen hinter den Links.
- „Du bist dran“ enthält nur, was der Projektinhaber selbst tun muss. Arbeit des Agenten gehört nach „Als Nächstes“.
- Keine Wiederholung derselben Sache in zwei Abschnitten.
- Nur Belegtes: Was nicht abgefragt werden konnte (Vault offline, Tool-Fehler), wird als Lücke genannt, nicht
  geschätzt.
- Ohne Änderungen seit gestern: „Seit gestern nichts Neues“ und direkt „Du bist dran“ und „Einstieg“.

## Ausgabe

- **Interaktiv:** das Briefing als Chat-Antwort.
- **Geplanter Lauf:** das Briefing als Abschluss der Sitzung ausgeben; das ist der Text, den die Benachrichtigung
  und die Sitzung zeigen. Lief vorher `ccmas-antworten-abholen`, fließt dessen Ergebnis ein, statt einen zweiten Bericht
  danebenzustellen: übernommene Antworten und Aufgeräumtes (gebündelt, eine Zeile) in „Seit gestern“, fortgesetzte
  Tickets in „Als Nächstes“, gemeldete Unstimmigkeiten in „Hinweise“. Die Daten erst nach dem Aufräumen sammeln, damit
  das Briefing den bereinigten Stand zeigt.
- Schreiben nach Asana oder Obsidian nur, wenn die Regeldatei des Projekts das für das Briefing vorsieht.
