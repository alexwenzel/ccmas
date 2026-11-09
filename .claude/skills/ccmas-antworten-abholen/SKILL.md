---
name: ccmas-antworten-abholen
description: Antworten des Projektinhabers aus Asana einsammeln und verarbeiten – abgehakte oder kommentierte Aufgaben „Entscheidung:“, „Freigabe:“, „Zuarbeit:“ und neue Kommentare auf offenen Tickets auslegen, im Ticket übernehmen, Labels bereinigen, Entscheidungen nach Obsidian tragen, blockierte Arbeit fortsetzen – und das Board aufräumen (Erledigtes nach Done, abgenommene Meilensteine schließen, entsperrte Tickets einreihen, Feldwerte vereinheitlichen). Verwenden für den geplanten Abhol-Lauf, beim Sitzungsstart und immer, wenn der Nutzer sagt „ich hab geantwortet“, „schau mal in Asana“, „räum das Board auf“ oder „was ist offen von mir“.
---

# Antworten abholen

Der Projektinhaber beantwortet Fragen asynchron in Asana: Er hakt eine Aufgabe ab oder kommentiert. Dieser Skill
findet neue Antworten, legt sie aus, überträgt sie, setzt die Arbeit fort, die nur daran hing, und räumt danach das
Board auf, damit Ansichten und Filter den echten Stand zeigen. Er läuft unbeaufsichtigt (geplanter Task) oder
interaktiv (Sitzungsstart, auf Zuruf).

## Projektkontext zuerst

Aus der Regeldatei des Projekts lesen: Projekt-GID, User-GIDs von Projektinhaber und Agent-Account, Label-Feld und
Optionen, Präfixe der Aufgaben an den Projektinhaber, Grenzen des Agenten. Für Schreibaktionen in Asana gilt Skill
`ccmas-asana-ticket`, für Obsidian Skill `ccmas-obsidian-seite`.

## 1. Kandidaten finden

1. **Aufgaben an den Projektinhaber:** `search_tasks` im Projekt mit `assignee_any` = Projektinhaber,
   `modified_at_after` = letzter Lauf (unbekannt: 7 Tage zurück), ohne Filter auf `completed`,
   `opt_fields` mit `completed,completed_at,parent.name,custom_fields.display_value`. Relevant sind:
   - Aufgaben mit Präfix (`Entscheidung:`, `Freigabe:`, `Zuarbeit:`) oder Label `Frage`/`Wartet auf Zugang/Daten`,
     erledigt oder neu kommentiert;
   - **alle** seit dem letzten Lauf erledigten Aufgaben des Projektinhabers, auch eigenständige Tickets ohne Präfix
     (Konto eingerichtet, Entscheidung getroffen). Sie können Arbeit des Agenten freigeben; ihre abhängigen Tickets
     (`dependents`) prüfen.
     Der Suchindex kann einige Minuten hinterherhinken; den Stand eines Treffers vor dem Verarbeiten mit `get_task`
     bestätigen.
2. **Kommentare auf offenen Tickets:** `search_tasks` mit `completed: false`, `modified_at_after` wie oben. Für jeden
   Treffer `get_task` (mit Kommentaren) und nur Kommentare des Projektinhabers betrachten, die nach dem letzten
   Kommentar des Agenten kamen.
3. **Schon verarbeitet?** Steht im übergeordneten bzw. abhängigen Ticket nach der Antwort bereits ein Kommentar des
   Agenten mit „Antwort übernommen“, ist sie erledigt. Dieser Marker verhindert Doppelverarbeitung zwischen Läufen
   und Sitzungen.

Gibt es keine neuen Antworten, die Schritte 2 bis 4 überspringen und direkt mit Schritt 5 (Aufräumen) weitermachen.

## 2. Auslegen

- **Abgehakt ohne Kommentar** heißt: Empfehlung bzw. Freigabe angenommen.
- **Ein Kommentar ist maßgeblich**, auch wenn zusätzlich abgehakt wurde: „B“ heißt Option B, ein Änderungswunsch
  heißt nicht freigegeben.
- **Unklar oder widersprüchlich:** nichts annehmen. Neue Unteraufgabe `Entscheidung: …` mit präziserer Frage anlegen
  (Skill `ccmas-asana-ticket`). Bei folgenreichen Fragen ist eine Rückfrage billiger als eine falsche Umsetzung.
- **Zuarbeit geliefert:** prüfen, ob das Gelieferte am genannten Ablageort tatsächlich vorhanden ist, bevor darauf
  aufgebaut wird. Werte von Secrets nie in Asana oder Obsidian übernehmen.

## 3. Übernehmen

Je Antwort, in dieser Reihenfolge:

1. **Kommentar** im übergeordneten bzw. abhängigen Ticket: `Antwort übernommen: <was entschieden wurde>` und der
   nächste Schritt. Dieser Kommentar ist zugleich der Marker aus Schritt 1.3.
2. **Labels bereinigen:** nur an Aufgaben, die offen bleiben, und nur, wenn der Grund entfällt. Erledigte Aufgaben
   behalten ihr Label.
3. **Obsidian nachtragen**, wo die Entscheidung dauerhaft gilt (Konzeptseite, Entscheidungsliste, Projektplan).
   Ohne Vault-Zugriff im Kommentar „Obsidian-Nachtrag offen“ vermerken.
4. **Freigaben mit Folgehandlung** (z. B. Meilenstein abgenommen): Skill `ccmas-meilenstein-abschluss`, Teil „Nach der
   Abnahme“.

## 4. Fortsetzen

Nur Tickets in `In Progress`, deren nächster Schritt allein an dieser Antwort hing, und nur im Rahmen der Grenzen aus
der Regeldatei. Unbeaufsichtigt gilt zusätzlich: kein Merge, kein Push auf Integrations- oder Produktionsbranch,
nichts an Produktion, kein neues Vorhaben. Alles andere wird nur dokumentiert und bleibt für die nächste interaktive
Sitzung.

## 5. Aufräumen

Läuft bei jedem Durchgang, auch ohne neue Antworten. Ziel: Board, Filter und Dashboards zeigen den tatsächlichen
Stand, ohne dass der Projektinhaber Karten von Hand nachschiebt. Nur offene oder seit dem letzten Lauf geänderte
Aufgaben prüfen, nicht das ganze Archiv. Schreibaktionen nach Skill `ccmas-asana-ticket` (Sammelaufrufe, `failed` prüfen).

**Selbst erledigen** (eindeutig, folgenlos, umkehrbar):

1. **Erledigt, aber nicht in `Done`:** in die Section `Done` verschieben. Das gilt auch für Aufgaben, die der
   Projektinhaber abgehakt hat; das Abhaken ist seine Antwort, das Verschieben nur Ordnung. Labels bleiben stehen.
2. **Abgenommene Meilensteine:** Ist die `Freigabe:`-Aufgabe eines Meilensteins abgehakt (ohne Änderungswunsch) und
   sind alle Tickets des Meilensteins erledigt, den Meilenstein nach Skill `ccmas-meilenstein-abschluss`, Teil „Nach der
   Abnahme“, schließen: Asana `Done`, GitHub-Milestone schließen, Projektplan nachziehen.
3. **Entsperrte Tickets:** Ein offenes Ticket in `Backlog`, dessen Abhängigkeiten jetzt alle erledigt sind und dessen
   Meilenstein laut Terminliste aktuell ist (Startdatum erreicht), nach `To Do`.
4. **Feldwerte vereinheitlichen:** `Meilenstein` in Kurzform (`M09` statt „M09 Seitentyp …“); Aufgaben mit Präfix
   `Entscheidung:`/`Freigabe:`/`Zuarbeit:` ohne `Typ` erhalten `Organisation`; offene Tickets ohne Termin, deren
   Meilenstein einen hat, erhalten dessen Zeitfenster (nicht bei `Für Später`).
5. **GitHub-Milestones**, deren Asana-Meilenstein erledigt ist, schließen.

**Nur melden** (Urteil nötig oder schwer umkehrbar):

- Tickets in `Done`, die nicht erledigt sind; erledigte Tickets mit offenen Unteraufgaben.
- Offene Aufgaben des Agenten, deren Meilenstein bereits erledigt ist.
- Überfällige Meilensteine (Verschieben entscheidet der Projektinhaber, Skill `ccmas-meilenstein-abschluss`, Teil C).
- Tickets ohne Meilenstein oder ohne Assignee.

Nie anfassen: Tickets mit `Für Später`, offene Aufgaben an den Projektinhaber (nicht abhaken, nicht umformulieren),
Inhalte von Beschreibungen und Kommentaren.

## 6. Bericht

Am Ende knapp, auch im unbeaufsichtigten Lauf:

- übernommen: Ticket-ID, Antwort, Folge,
- fortgesetzt: Ticket-ID, was getan wurde,
- aufgeräumt: gebündelt („6 Freigaben nach Done, M10–M15 geschlossen, 3 Tickets nach To Do“),
- offen: neue Rückfragen, Obsidian-Nachträge, gemeldete Unstimmigkeiten, Dinge für eine interaktive Sitzung.

Interaktiv zusätzlich die noch unbeantworteten Aufgaben an den Projektinhaber auflisten, blockierende zuerst.
