---
name: ccmas-asana-ticket
description: Asana-Tickets in einem Projekt mit Agent-Workflow anlegen, ändern, verschieben, kommentieren und abschließen, inklusive Aufgaben an den Projektinhaber (Entscheidung, Freigabe, Zuarbeit) mit Label, Abhängigkeit und Terminen. Immer verwenden, wenn im Asana-Projekt eines Vorhabens geschrieben wird – Ticket anlegen, Status ändern, Rückfrage stellen, Folge-Tickets erzeugen, Kommentar setzen –, auch wenn der Nutzer nur „leg das in Asana an“ oder „frag mich das im Ticket“ sagt.
---

# Asana-Ticket

Wie man im Asana-Projekt eines Vorhabens sauber schreibt: Tickets mit allen Feldern, Rückfragen an den Projektinhaber,
Statuswechsel, Kommentare. Der Skill beschreibt Abläufe und Tool-Fallstricke. Die konkreten Werte und die Regeln des
Projekts stehen in dessen Regeldatei.

## Projektkontext zuerst

Jedes Projekt hat eine Regeldatei (meist `CLAUDE.md` im Obsidian-Projektordner, Fundort nennt die Projekt-
Systemanweisung oder die `CLAUDE.md` im Repo). Lies daraus vor dem ersten Schreibzugriff:

- Ressourcen: Projekt-GID, Section-GIDs, Feld- und Options-GIDs (ID, Typ, Meilenstein, Label), User-GIDs von
  Projektinhaber und Agent-Account.
- Regeln zu Tickets, Labels, Aufgaben an den Projektinhaber, Done-Kriterien, zurückgestellter Arbeit, Meilensteinen
  und Terminen.

Werte nie raten oder aus einem anderen Projekt übernehmen; eine falsche GID schreibt still ins falsche Feld. Fehlt ein
Wert, fragen. Weicht die Regeldatei von diesem Skill ab, gilt die Regeldatei.

## Arbeitsmodell (Standard)

Diese Begriffe verwendet der Skill. Ein Projekt kann sie anders benennen; dann gelten seine Namen.

- **Sections:** Backlog → To Do → In Progress → Review → Done.
- **Felder:** `ID` (automatisch, z. B. `SC-42`; nicht im Namen), `Typ` (Concept, Implementation, Bug, Organisation),
  `Meilenstein` (Textfeld, z. B. `M09`), `Label` (Mehrfachauswahl).
- **Assignee** ist, wer die Arbeit ausführt: Agent-Account oder Projektinhaber.
- **Labels** stehen auf der Aufgabe, bei der gehandelt werden muss oder die ruht, nicht auf dem blockierten Ticket.
  `Frage` und `Wartet auf Zugang/Daten` gehören auf Aufgaben an den Projektinhaber; `Wartet auf Dritte` und
  `Für Später` auf das betroffene Ticket. Blockaden bildet eine Asana-Abhängigkeit ab.
- **Aufgaben an den Projektinhaber:** Unteraufgabe mit Präfix `Entscheidung:`, `Freigabe:` oder `Zuarbeit:`,
  ins Projekt aufgenommen. Abhaken heißt: Empfehlung angenommen.

## Vorbereitung

- Asana-Tools sind oft verzögert geladen. In einem einzigen `ToolSearch` laden, z. B.
  `select:mcp__Asana__search_tasks,mcp__Asana__get_task,mcp__Asana__create_tasks,mcp__Asana__update_tasks,mcp__Asana__add_comment`.
- Prüfen, als wer der Connector schreibt (erster Kommentar oder `get_me`). Schreibt er als Projektinhaber statt als
  Agent-Account, bekommt dieser keine Benachrichtigungen. Das im Bericht melden.

## Ticket finden

- **Per ID:** `search_tasks` mit `projects_any` = Projekt-GID und `custom_fields` als JSON-String
  `{"<GID Feld ID>.value":"SC-42"}`. Die ID steht nicht im Namen, eine Textsuche nach „SC-42“ findet nichts.
- **Vor jedem Anlegen** nach Dubletten suchen: `search_tasks` mit `text` und `completed: false`.
- **Details:** `get_task` mit `opt_fields`, z. B.
  `name,custom_fields.name,custom_fields.display_value,memberships.section.name,dependencies.name,dependents.name,start_on,due_on,assignee.name,parent.name`.

## Ticket anlegen

1. **Termine** nach der Termin-Regel des Projekts bestimmen (z. B. Zeitfenster des Meilensteins aus der Terminliste
   im Projektplan). Tickets ohne Meilenstein oder mit `Für Später` bekommen keinen Termin.
2. **`create_tasks`** (bis 50 je Aufruf) mit `default_project`. Je Task: `name` (ohne ID), `section_id`, `assignee`,
   `start_on`, `due_on`, `html_notes` und `custom_fields` als **JSON-String**, z. B.
   `{"<Typ-GID>":"<Options-GID>","<Meilenstein-GID>":"M09"}`. Ein Label nur setzen, wo die Label-Regel es verlangt.
   Den Meilenstein immer in der Kurzform schreiben (`M09`, nicht den Titel): Filter, Ansichten und andere Skills
   suchen nach genau diesem Wert, und ein abweichender Wert lässt das Ticket aus Auswertungen fallen.
3. **Beschreibung** mit Ziel, Umfang, prüfbaren Akzeptanzkriterien, Verweis auf die maßgebliche Obsidian-Seite und
   Abhängigkeiten:

   ```text
   <body><strong>Ziel:</strong> …

   <strong>Umfang</strong>
   <ul><li>…</li></ul>

   <strong>Akzeptanzkriterien</strong>
   <ul><li>…</li></ul>

   <strong>Obsidian:</strong> Seite, Abschnitt. <strong>Abhängigkeiten:</strong> …</body>
   ```

   - Erlaubt sind nur: `body strong em u s code ol ul li a blockquote pre h1 h2 hr img table tr td`.
     Kein `p`, kein `br`, kein `th`. Zeilenumbrüche sind echte `\n`.
   - Das Dokument muss wohlgeformtes XML sein: `&amp;`, `&lt;`, `&gt;`. Asana lehnt fehlerhaftes HTML mit 400 ab.
   - Ticketverweis: `<a data-asana-gid="<gid>"/>`. Obsidian-Seiten als Klartext (Seite und Abschnitt), da Wikilinks
     in Asana nicht funktionieren.

4. **ID auslesen.** Die Antwort enthält nur die GID. Die `SC-<n>` danach per `get_task` lesen und für Branch,
   Commit, PR und Bericht verwenden. IDs nie vorhersagen: Bei gleichzeitig angelegten Tickets ist die Reihenfolge
   nicht garantiert.
5. **Abhängigkeiten** in einem zweiten Aufruf: `update_tasks` mit `add_dependencies` (blockiert durch) oder
   `add_dependents`. Innerhalb von `create_tasks` sind die neuen GIDs noch nicht bekannt.
6. **Meilenstein verknüpfen**, falls das Projekt Meilenstein-Tasks führt. An einem Nachbarticket prüfen, in welcher
   Richtung die Abhängigkeit gesetzt ist, und genauso verfahren.

## Aufgabe an den Projektinhaber

Blockiert eine offene Frage den nächsten Schritt, gibt es eine Unteraufgabe. Ist es eigenständige Arbeit des
Projektinhabers (Konto einrichten, Zugang beschaffen, Etappe abnehmen), gibt es ein eigenes Ticket vom Typ
`Organisation`.

1. **Anlegen:** `create_tasks` mit `parent` = Ticket-GID (ohne `default_project`), `assignee` = Projektinhaber,
   Name mit Präfix, Termine wie das übergeordnete Ticket. Beschreibung:

   ```text
   <body><strong>Kontext:</strong> 1–3 Sätze, Verweis auf PR oder Obsidian-Seite
   <strong>Optionen:</strong>
   <ul><li>A – … (Folge: …)</li><li>B – … (Folge: …)</li></ul>
   <strong>Empfehlung:</strong> B, weil …
   <strong>Antwort:</strong> Abhaken = Empfehlung angenommen. Sonst kurz kommentieren (z. B. „A“), dann abhaken.</body>
   ```

   Bei `Freigabe:` steht dort, was wo zu prüfen ist (PR, Seite, URL, Screenshots). Bei `Zuarbeit:` steht, was
   gebraucht wird und wo es abgelegt werden soll. Secrets kommen nie in Asana; nur den Ablageort nennen.

2. **Ins Projekt aufnehmen.** Unteraufgaben gehören sonst zu keinem Projekt: Sie fehlen im Board und haben keine
   Projektfelder. `update_tasks` mit `add_projects: [{project_id, section_id}]`, Section `To Do`, bei `Freigabe:`
   `Review`.
3. **Felder setzen**, in einem eigenen Aufruf nach der Projektaufnahme (erst dann existieren sie an der Aufgabe):
   `Typ` = Organisation, `Meilenstein` wie das Ticket, `Label` = `Frage` (Entscheidung, Freigabe) bzw.
   `Wartet auf Zugang/Daten` (Zuarbeit).
4. **Blockade setzen:** am übergeordneten Ticket `add_dependencies: [<Unteraufgabe>]`. Das übergeordnete Ticket
   bekommt kein Label.
5. **Eine Frage je Unteraufgabe**, damit jede einzeln beantwortbar ist. Fragen, die mehrere Tickets betreffen, ins
   führende Ticket. Die Zuweisung benachrichtigt bereits; keine zusätzliche @-Erwähnung.
6. Unabhängige Arbeit weiterführen. Eine ausbleibende Antwort ist keine Zustimmung.

## Ändern, verschieben, abschließen

- **Section wechseln:** `update_tasks` mit `add_projects: [{project_id, section_id: <Ziel>}]`. Das verschiebt
  innerhalb des Projekts; einen eigenen Section-Parameter gibt es nicht.
- **Label ändern:** Die Mehrfachauswahl wird ganz überschrieben. Aktuellen Wert lesen, neues Array schreiben;
  `[]` entfernt alle. An erledigten Aufgaben Labels stehen lassen, sie fallen aus „unerledigt“-Filtern ohnehin
  heraus.
- **Termine entfernen** (z. B. bei `Für Später`): `due_on: null` und `start_on: null` im selben Aufruf; `start_on`
  geht nur zusammen mit `due_on`.
- **Abschließen:** Abschlusskommentar setzen, dann Section `Done` und `completed: true`, sofern die Done-Kriterien
  des Projekts erfüllt sind. Aufgaben an den Projektinhaber hakt nur er selbst ab; nach dem Abhaken darf der Agent sie nach `Done`
  verschieben.

## Kommentieren

- `add_comment` mit `html_text`. Hier sind nur `body strong em u s code ol ul li a blockquote pre` erlaubt
  (anders als in Beschreibungen kein `h1`, `hr`, `img`, `table`).
- Fortschritt als Phasenabschluss: Ergebnis, Entscheidungen, Tests (Befehl → Ergebnis), Code (Branch/PR),
  Doku (Seite), Offen, Nächster Schritt.
- Native Erwähnung nur in einer laufenden Diskussion: `<a data-asana-gid="<User-GID>"/>`. Ein bloßes `@Name` ist
  keine Erwähnung.

## Prüfen und berichten

- Bei Sammelaufrufen die Felder `succeeded` und `failed` der Antwort lesen und Teilfehler einzeln nachsetzen. Nichts
  als erledigt melden, was die API nicht bestätigt hat.
- Im Chat-Bericht neue oder geänderte Tickets mit ID und Link (`https://app.asana.com/0/<Projekt-GID>/<Task-GID>`)
  nennen, Aufgaben an den Projektinhaber gesondert.

## Bekannte Grenzen des Connectors

- Asana-Tags kann er weder anlegen noch setzen. Deshalb sind Labels ein Feld.
- Approval-Tasks gibt es nur in Business/Enterprise; Ersatz sind die Präfix-Unteraufgaben.
- Ansichten, Filter und Dashboards kann er nicht anlegen. Das übernimmt der Projektinhaber.
