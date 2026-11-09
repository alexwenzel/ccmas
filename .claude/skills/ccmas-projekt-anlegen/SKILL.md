---
name: ccmas-projekt-anlegen
description: Neues Agent-Projekt komplett einrichten oder ein bestehendes Asana-Projekt anbinden – Obsidian-Ordner mit @index, CLAUDE.md, Beschreibung und Projektplan-Gerüst, Asana-Board mit Sections und Feldern nach dem Muster von Swimcheck, Einrichtungstickets, Texte fürs Claude-Projekt.
---

# Projekt anlegen

Richtet ein Projekt für die Arbeit mit dem Agenten vollständig ein, nach dem Muster der bestehenden Agent-Projekte:
Obsidian-Ordner mit Index-Seite, Regeldatei `CLAUDE.md`, Kurzbeschreibung und Projektplan-Gerüst; Asana-Projekt mit
Board, Sections und Feldern; Einrichtungstickets; Texte für das Claude-Projekt. Danach arbeiten die übrigen
Projekt-Skills (`ccmas-asana-ticket`, `ccmas-obsidian-seite`, `ccmas-neue-aufgabe-nehmen`, `ccmas-concept-ticket`, `ccmas-feature-parallel` …) ohne
weitere Einrichtung.

Eingabe: $ARGUMENTS

Auslöser: „leg ein neues Projekt an“, „richte Projekt X ein“, „verknüpfe das Asana-Projekt X mit Obsidian“, „setz das
Projekt wie Swimcheck auf“.

## Referenz und Workspace-Werte

**Referenzprojekt** für Aufbau und Regeltexte:

| Was               | Wo                                                                                     |
| ----------------- | -------------------------------------------------------------------------------------- |
| Regeldatei        | Vault `w3entwickler`, `Eigene Projekte/Swimcheck/Swimcheck - Rebuild/CLAUDE.md`        |
| Index-Seite       | `Eigene Projekte/Swimcheck/Swimcheck - Rebuild/@index.md`                              |
| Asana-Projekt     | „Swimcheck - Rebuild“, GID `1219508488635976`                                          |
| Einfachere Fassung | `Eigene Projekte/Freelancing/CLAUDE.md` (w3entwickler.de: Meilensteine nur als Textfeld, keine geplanten Tasks) |
| Liste der Skills  | `Fachwissen/Claude/Skills - Eine Liste mit Beschreibung.md`                            |

**Workspace-Werte** (Stand 2026-11-09, vor Gebrauch prüfen, Phase 2):

| Wert                                          | GID                                                                                                                                        |
| --------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------ |
| Workspace                                     | `1218492359718773`                                                                                                                         |
| Projektinhaber Alexander                      | `1218492359718760`                                                                                                                         |
| Agent-Account Claude (`claude@w3entwickler.de`) | `1219534484468263`                                                                                                                       |
| Feld `Typ` (Einfachauswahl, workspaceweit)    | `1219535027925901`: Concept `1219535027925902`, Implementation `1219535027925903`, Bug `1219535027925904`, Organisation `1219594901324083` |
| Feld `Label` (Mehrfachauswahl, workspaceweit) | `1219535003836333`: Frage `1219535003836334`, Wartet auf Zugang/Daten `1219535003836335`, Wartet auf Dritte `1219535003836336`, Für Später `1219576912022819` |
| Feld `Meilenstein` (Text, workspaceweit)      | `1219594902238902`                                                                                                                         |
| Feld `ID`                                     | je Projekt ein eigenes Feld mit eigenem Präfix; belegte Präfixe per Suche (Phase 2)                                                        |

## Phase 1 – Eingaben klären

Aus `$ARGUMENTS` und Kontext ableiten, den Rest gebündelt fragen (`AskUserQuestion`, bis 4 Fragen je Aufruf,
Empfehlung als erste Option; weitere als nummerierte Liste). Nichts erfinden: Was offen bleibt, wird in den Seiten als
offener Punkt geführt.

| Eingabe                 | Default / Beispiel                                                                                             |
| ----------------------- | -------------------------------------------------------------------------------------------------------------- |
| Projektname             | Anzeigename, z. B. „ccmas“                                                                                     |
| Obsidian-Ordner         | `Eigene Projekte/<Projektname>` im Vault `w3entwickler`                                                        |
| Kurzbeschreibung        | 2–5 Sätze: was entsteht, für wen, wozu                                                                         |
| Projektumfang           | Teile oder Produkte, Abgrenzung                                                                                |
| Phase                   | Prototyp/MVP                                                                                                   |
| Asana                   | neues Projekt anlegen, oder bestehendes verknüpfen (Link oder GID)                                             |
| ID-Präfix               | 2–3 Großbuchstaben aus dem Namen, im Workspace noch frei                                                       |
| Code                    | GitHub `owner/repo`, lokaler Pfad, Standard- und Integrationsbranch (`develop`) – oder „noch kein Repo“        |
| Bestands-Repo           | keins; sonst Pfad, nur lesend                                                                                  |
| Meilensteine            | **einfach:** nur Textfeld `Meilenstein` in Asana (Default) · **voll:** Meilenstein-Tasks, Termine, GitHub-Milestones wie im Referenzprojekt |
| Merge nach Integration  | Agent legt PRs an, Projektinhaber mergt (Default) · Agent mergt selbst bei grüner CI                            |
| Geplante Tasks          | keine; Antworten abholen, Briefing, Aufgabenvorbereitung auf Zuruf (Default)                                   |
| Claude-Projekt          | ja, Texte liefern (Default) · nein                                                                             |
| Sichtbarkeit Asana      | privat, Mitglieder Alexander und Claude (Default)                                                              |

Vor dem ersten Schreibzugriff den Plan in höchstens acht Zeilen vorlegen (Ordner, Asana neu oder verknüpft, Präfix,
Mitglieder, Repo, Meilenstein-Modell, Merge-Regel) und die Bestätigung abwarten. Ein neues Projekt und neue Mitglieder
brauchen die Zustimmung des Projektinhabers. Läuft der Skill unbeaufsichtigt: nur den Plan berichten, nichts anlegen.

## Phase 2 – Prüfen

1. **Vault-Zugriff** nach Skill `ccmas-obsidian-seite`: claude.ai mit verbundenem Rechner über `device_bash`; ist der
   Elternordner des Zielordners nicht verbunden, einmal `device_request_folder_access` darauf. Claude Code: direkt.
   Ohne Rechner abbrechen, der Vault ist Pflicht.
2. **Zielordner:** Existiert er schon mit `CLAUDE.md` oder `@index.md`, nichts überschreiben. Dann fehlende Dateien
   ergänzen und Abweichungen vom Muster melden.
3. **Referenz lesen:** Gliederung der Referenz-`CLAUDE.md` (`grep -n "^#"`), dann die Abschnitte, die übernommen
   werden (Phase 4); `@index.md` als Formvorlage. Die Liste der Skills aus der Skills-Seite.
4. **Asana-Tools** in einem `ToolSearch` laden: `get_me`, `get_project`, `create_project`, `update_project`,
   `search_objects`, `create_tasks`, `update_tasks`, `get_task`, `add_comment`. `get_me`: Schreibt der Connector als
   Agent-Account? Wenn nicht, melden (der Projektinhaber bekommt dann keine Benachrichtigungen).
5. **Workspace-Werte** bestätigen: `search_objects` mit `resource_type: custom_field`, `query` „Typ“ bzw. „Label“,
   `opt_fields: name,resource_subtype,enum_options.name,enum_options.gid`. Abweichende Werte verwenden und melden.
6. **ID-Präfix frei?** `search_objects`, `resource_type: custom_field`, `query: ID`, `opt_fields: name,id_prefix`.
   Belegt: anderen Präfix vorschlagen.
7. **Dubletten:** `search_objects`, `resource_type: project`, Projektname. Gibt es das Projekt schon, verknüpfen statt
   neu anlegen (Rückfrage, falls der Nutzer „neu“ gesagt hat).
8. **Repo**, falls genannt: Existiert es (lokaler Pfad, `gh repo view` bzw. GitHub-Tools)? Standardbranch,
   Integrationsbranch, vorhandene `CLAUDE.md`. Ein Repo nur auf ausdrücklichen Auftrag anlegen.

## Phase 3 – Asana

**Neues Projekt**

1. `create_project`: `name`, `owner` = Projektinhaber, `members` = Projektinhaber und Agent-Account,
   `privacy_setting` (Default `private`), `default_view: board`, `sections` in dieser Reihenfolge: Backlog, To Do,
   In Progress, Review, Done; `html_notes` = Projektbeschreibung (Vorlage unten).
2. `get_project` mit `include_sections: true`: Section-GIDs lesen. Hat Asana zusätzlich eine leere Standard-Section
   angelegt, sie mit `update_project` → `remove_sections` entfernen.
3. `update_project` → `add_custom_fields`: `Typ`, `Label`, `Meilenstein`, jeweils `is_important: true`.
4. **ID-Feld:** Der Connector kann keine Felder anlegen. Den Projektinhaber bitten, im Projekt über „Anpassen“ ein
   Feld vom Typ „ID“ mit dem Präfix `<XX>` hinzuzufügen. Interaktiv darauf warten, dann mit `get_project`
   (`opt_fields: custom_field_settings.custom_field.gid,custom_field_settings.custom_field.name,custom_field_settings.custom_field.id_prefix`)
   prüfen und die GID notieren. Geht das nicht sofort: `Zuarbeit:`-Unteraufgabe in Phase 5 und im Bericht melden,
   dass die Tickets ihre ID erst mit dem Feld bekommen.

**Bestehendes Projekt verknüpfen**

1. `get_project` mit Sections, Mitgliedern und `custom_field_settings` (Felder wie oben).
2. Sections: fehlende mit `add_sections` ergänzen; abweichend benannte nach Rückfrage mit `rename_sections`
   umbenennen. Nichts löschen, keine Tasks verschieben. Eine falsche Reihenfolge kann der Connector nicht ändern:
   melden.
3. Fehlende Felder mit `add_custom_fields` ergänzen; ID-Feld und Präfix prüfen, fehlt es: wie oben.
4. Fehlt der Agent-Account als Mitglied: `update_project` → `add_members`. Das Tool verlangt eine Bestätigung;
   `confirm_sensitive_changes` erst setzen, wenn der Nutzer zugestimmt hat.
5. `default_view` auf `board`, wenn der Nutzer nichts anderes will. Eine vorhandene Projektbeschreibung nicht
   überschreiben: leer → Vorlage; sonst die Vorlage im Bericht als Vorschlag.

**Projektbeschreibung** (`html_notes`; erlaubt sind nur `body strong em u s code ol ul li a blockquote pre`,
wohlgeformtes XML, Zeilenumbrüche als echte `\n`):

```text
<body><strong>Projekt:</strong> <Kurzbeschreibung>

<strong>Workflow</strong>
<ul><li>Backlog: erfasst, nicht eingeplant; mit Label „Für Später“ bewusst zurückgestellt.</li><li>To Do: konkretisiert und startbereit.</li><li>In Progress: in Bearbeitung.</li><li>Review: Ergebnis liegt vor, Prüfung oder Abnahme durch den Projektinhaber offen.</li><li>Done: Akzeptanzkriterien erfüllt, Doku aktualisiert, Abschlusskommentar; bei Code zusätzlich Tests grün und gepusht.</li></ul>
<strong>Felder</strong>
<ul><li>ID: <XX>-&lt;Nummer&gt;, nicht im Ticketnamen. Branches, Commits und PRs verweisen darauf.</li><li>Typ: Concept, Implementation, Bug, Organisation.</li><li>Meilenstein: Kurzname, zweistellig (M00, M01, …).</li><li>Label: Frage, Wartet auf Zugang/Daten, Wartet auf Dritte, Für Später.</li><li>Assignee: wer die Arbeit ausführt, Claude oder Alexander.</li></ul>
<strong>Aufgaben an den Projektinhaber:</strong> Unteraufgaben mit Präfix „Entscheidung:“, „Freigabe:“ oder „Zuarbeit:“, mit Empfehlung. Abhaken heißt Empfehlung angenommen; ein Kommentar ist maßgeblich.

<strong>Regeln:</strong> Verbindlich ist CLAUDE.md im Obsidian-Ordner „<Ordner>“ (Vault „w3entwickler“). Obsidian: Konzepte und Entscheidungen. Asana: Aufgaben und Verlauf. GitHub: Code.</body>
```

## Phase 4 – Obsidian

Ordner anlegen, neue Seiten vollständig per Heredoc schreiben (Skill `ccmas-obsidian-seite`). Properties nach dem Schema
der Referenz (`title`, `type`, `status`, `stand`, `version: 0.1.0`, `updated`), echtes heutiges Datum.

**Eindeutige Namen:** Im Vault gibt es `CLAUDE.md`, `@index.md`, `Projektplan.md` mehrfach. Neue Seiten heißen
`<Projektname> - Beschreibung und Konzept.md` und `<Projektname> - Projektplan.md`; `CLAUDE` und `@index` werden mit
vollem Pfad verlinkt (`[[<Ordner>/CLAUDE|CLAUDE]]`).

**1. `@index.md`**

````markdown
---
tags: MOCs
title: "<Projektname> – Projektübersicht"
type: overview
status: draft
stand: <heute>
version: 0.1.0
updated: <heute>
---
```folder-index-content
```

# <Projektname> – Projektübersicht

## Ziel

<Kurzbeschreibung>

## Zentrale Dokumente

| Seite | Inhalt |
|---|---|
| [[<Projektname> - Beschreibung und Konzept]] | Ziele, Umfang, Grundsätze, Entscheidungen |
| [[<Projektname> - Projektplan]] | Phasen und Meilensteine (im Aufbau) |
| [[<Ordner>/CLAUDE\|CLAUDE]] | Arbeitsregeln für den KI-Agenten (einzige Regelquelle) |

## Systeme

| System | Ort | Inhalt |
|---|---|---|
| Obsidian | dieser Ordner, `<Ordner>` | Konzepte, Anforderungen, Architektur, Entscheidungen |
| Asana | [<Projektname>](<Projekt-URL>) | Aufgaben, Status, Arbeitsverlauf, Rückfragen |
| Code | GitHub `<owner/repo>`, lokal `<Pfad>`, Branches `<Integration>` → `<Standard>` | Code, Commits, PRs |
| Bestand | `<Pfad>`, nur lesend | Altsystem (nur wenn vorhanden) |

Ressourcen-IDs und Regeln: [[<Ordner>/CLAUDE#3. Ressourcen (verifiziert)|CLAUDE, Abschnitt 3]].
````

Ohne Repo steht in der Zeile „Code“: „noch kein Repo, siehe [[<Ordner>/CLAUDE#3. Ressourcen (verifiziert)|CLAUDE 3]]“.

**2. `CLAUDE.md`** – aus der Referenz abgeleitet, **gleiche Abschnittsnummern**, denn die Skills verweisen auf sie
(7.4, 7.8, 8.1, 13, 14). Kopf: Properties (`title: "CLAUDE.md – Arbeitsregeln für den KI-Agenten"`,
`type: guideline`, `status: review`), dann der Satz zur einzigen Regelquelle und „Angelegt am <heute> nach dem Muster
der Regeln von Swimcheck Rebuild (Skill `ccmas-projekt-anlegen`)“.

| Abschnitt                                | Herkunft                                                                                                                |
| ---------------------------------------- | ----------------------------------------------------------------------------------------------------------------------- |
| 1 Rolle und Arbeitsmodus                 | Rolle passend zum Projekt, Phase aus den Eingaben; „Gesteuerte Autonomie“ wörtlich                                      |
| 1.1 Projektumfang                        | neu aus den Eingaben, Link auf die Beschreibung                                                                         |
| 2 Systeme und Zuständigkeiten            | Tabelle mit dem neuen Asana-Projekt und Repo; Text wörtlich                                                             |
| 3 Ressourcen (verifiziert)               | neu, nur geprüfte Werte: Vault und Ordner, Workspace, Projekt-GID, Sections, Projektinhaber, Agent-Account, Felder mit Optionen und ID-Präfix, Repo, GitHub-Zugriff, Asana-Plan (Approval-Tasks nicht verfügbar, Ersatz Präfix-Unteraufgaben), Skills (aus der Skills-Seite), geplante Tasks. Jede Zeile mit Datum; Offenes als „offen, siehe <Ticket>“ |
| 4 Autonomie und Grenzen                  | wörtlich; Branches und Merge-Regel nach den Eingaben                                                                    |
| 5 Session-Start                          | wörtlich, Link auf die neue Index-Seite                                                                                 |
| 6 Obsidian                               | 6.1, 6.3–6.5 wörtlich (Beispiel-Links mit neuem Präfix und Repo); 6.2 Ablage neu: Ebene 1 Regeln, Index, Beschreibung, Projektplan; Unterordner nach Bedarf, z. B. `Bestand/` |
| 7.1–7.6 Asana                            | wörtlich mit neuem Präfix und den Personen aus Abschnitt 3                                                              |
| 7.7 Meilensteine                         | einfach: Fassung wie Freelancing 7.7; voll: Fassung der Referenz                                                        |
| 7.8 Antworten des Projektinhabers        | ohne geplanten Task: Fassung wie Freelancing 7.8; mit Task: Fassung der Referenz                                        |
| 7.9 Neue Aufgabe nehmen                  | auf Zuruf; Startbefehl-Vorlage nur, wenn der lokale Repo-Pfad bekannt ist (Repo-Pfad, `--add-dir` auf den Vault-Ordner, Session-Name `<XX>-<n> - <Kurztitel>`) |
| 8 Code und Git                           | 8.1 nach den Eingaben (Branches `feature/<XX>-<n>-<kurz>` usw. von der Integration, Squash-Merge, Merge-Regel; Repo-`CLAUDE.md` nur Repo-Spezifisches plus Verweis); 8.2 nur mit Bestands-Repo |
| 9–12                                     | wörtlich                                                                                                                |
| 13 Pflege dieser Regeln                  | wörtlich; Ankerticket = Einrichtungsticket aus Phase 5                                                                  |
| 14 Texte für das Claude-Projekt          | 14.1 Projektbeschreibung (1–2 Sätze), 14.2 Systemanweisung nach dem Muster der Referenz mit neuen Namen und gewähltem Modell, ohne Wikilinks |

**Bereinigen:** Nichts aus dem Referenzprojekt übernehmen, was nur dort gilt: keine Ticket-, PR- oder Seitenlinks,
keine GIDs, Pfade, Personenzusagen, Zugänge, Technikentscheidungen, keine Datumsangaben früherer Entscheidungen.
Übernommene Regeln stehen ohne „Entscheidung vom …“; mit Datum nur, was der Projektinhaber in dieser Einrichtung
entschieden hat. Prüfung nach dem Schreiben, Ergebnis muss leer sein:
`grep -n -i "swimcheck\|SC-[0-9]\|1219508488635976\|oc-swimcheck\|merkur\|payload" "<Ordner>/CLAUDE.md"`
(bei Ableitung aus der Freelancing-Fassung zusätzlich `w3entwickler.de\|W3-[0-9]`).

**3. `<Projektname> - Beschreibung und Konzept.md`** (`type: concept`, `status: draft`): 1 Ziele, 2 Umfang und
Abgrenzung, 3 Grundsätze, 4 Offene Punkte, 5 Entscheidungen. Nur Inhalte aus den Eingaben; Lücken als offene Punkte.

**4. `<Projektname> - Projektplan.md`** (`type: concept`, `status: draft`): 1 Scope und Vorgehen, 2 Meilensteine
(„im Aufbau, siehe <Projektplan-Ticket>“), 3 Termine (nur bei Modell „voll“), 4 Annahmen und Risiken, 5 Stand.

**Übergeordnete Index-Seite:** Zeigt der Elternordner seine Unterordner nicht automatisch (`folder-index-content`),
dort den Link auf die neue Index-Seite ergänzen.

Danach die Link-Prüfung aus `ccmas-obsidian-seite` über die vier Seiten.

## Phase 5 – Einrichtungstickets

Nach dem ID-Feld, nach Skill `ccmas-asana-ticket` (Beschreibung mit Ziel, Umfang, Akzeptanzkriterien, Obsidian-Verweis):

| Ticket                                   | Typ          | Assignee        | Section     |
| ---------------------------------------- | ------------ | --------------- | ----------- |
| Projekt einrichten                       | Organisation | Agent           | In Progress |
| Projektplan Phase 1 erstellen            | Concept      | Agent           | To Do       |
| Beschreibung und Konzept ausarbeiten     | Concept      | Agent           | Backlog     |

- IDs per `get_task` auslesen und in `CLAUDE.md` (Abschnitt 13 Ankerticket, Abschnitt 3 Offenes), Projektplan und
  Beschreibung eintragen.
- Unteraufgaben an den Projektinhaber am Ticket „Projekt einrichten“ (Ablauf `ccmas-asana-ticket`: anlegen, ins Projekt
  aufnehmen, Felder setzen, Abhängigkeit am Ticket):
  - `Freigabe: Arbeitsregeln und Projektbeschreibung` (Review, Label `Frage`): zu prüfen sind `CLAUDE.md`
    Abschnitte 1, 4, 7.7, 8 und die Beschreibung. Abhaken heißt freigegeben.
  - `Zuarbeit: Claude-Projekt „<Projektname>“ anlegen` (To Do, Label `Wartet auf Zugang/Daten`), nur wenn gewünscht:
    Beschreibung und Systemanweisung aus `CLAUDE.md` 14 einfügen, `CLAUDE.md` als Projektdokument `claude/CLAUDE.md`
    hinzufügen.
  - `Zuarbeit: ID-Feld mit Präfix <XX> anlegen`, nur wenn es in Phase 3 nicht erledigt wurde.
  - `Zuarbeit: Repository nennen oder anlegen`, nur wenn Code geplant ist und kein Repo genannt wurde.
- Phasenabschluss-Kommentar am Ticket „Projekt einrichten“: angelegte Seiten mit Version, Asana-Aufbau, offene
  Punkte, nächster Schritt. Das Ticket bleibt in In Progress, bis die Unteraufgaben erledigt sind; dann Done.

## Phase 6 – Repo (nur mit Repo und Zugriff)

- Branch `docs/<XX>-<n>-claude-md` von der Integration. Repo-`CLAUDE.md` anlegen oder ergänzen: nur
  Repo-Spezifisches (Remote, Branches, Befehle) und der Verweis auf die Regeldatei im Vault mit Pfad. Damit finden auch
  `ccmas-feature-parallel` und andere Skills im Repo die Asana-Werte. Regeln dort nicht duplizieren.
- `.claude/worktrees/` in `.gitignore`, falls `ccmas-feature-parallel` genutzt werden soll.
- PR auf die Integration, Link im Ticket „Projekt einrichten“. Gemergt wird nach der Merge-Regel aus Abschnitt 8.1.
- Modell „voll“: GitHub-Milestones entstehen erst mit dem Projektplan, nicht hier.
- Ohne Zugriff: als offenen Punkt im Ticket und im Bericht.

## Phase 7 – Prüfen und berichten

1. `get_project`: Sections in der richtigen Reihenfolge, vier Felder (ID mit Präfix), Mitglieder, Board-Ansicht.
2. Vault: vier Seiten vorhanden, Properties gesetzt, Link-Prüfung ohne Fehler, Bereinigungs-`grep` leer.
3. Tickets: IDs vorhanden, Unteraufgaben im Projekt, Abhängigkeiten gesetzt (`succeeded`/`failed` geprüft).
4. Bericht im Chat:
   - Obsidian: Ordner und Seiten mit Version.
   - Asana: Projekt-Link, Sections, Felder, Mitglieder.
   - Tickets mit ID und Link; Aufgaben an den Projektinhaber gesondert.
   - Texte für das Claude-Projekt (14.1, 14.2) als Codeblöcke zum Einfügen, wenn gewünscht.
   - Offen: was der Projektinhaber tun muss, was nicht geprüft werden konnte.
   - Nächster Schritt: „Projektplan Phase 1 erstellen“ (Skill `ccmas-concept-ticket`) oder `ccmas-neue-aufgabe-nehmen`.

## Grenzen

- Nichts überschreiben: vorhandene Seiten, Projektbeschreibung, Sections mit Tasks bleiben; Abweichungen melden.
- Kein neues Projekt, keine Mitglieder, keine öffentliche Sichtbarkeit, kein Repo und keine geplanten Tasks ohne
  Zustimmung des Projektinhabers.
- Keine Secrets in Obsidian, Asana oder Repo; Zugänge nur mit Ablageort.
- Nichts als eingerichtet melden, was nicht geprüft ist.