---
name: ccmas-obsidian-seite
description: Seiten im Obsidian-Vault eines Projekts sicher lesen, anlegen und ändern – Zugriff je Umgebung (claude.ai mit verbundenem Rechner, Claude Code lokal, Cloud), Ersetzen mit Prüfung, YAML-Properties, Wikilinks mit Abschnitts-Ankern, Index-Seite, Link-Prüfung. Immer verwenden, wenn Konzept-, Plan- oder Regelseiten im Vault geschrieben oder umgebaut werden, auch bei kleinen Ergänzungen wie „trag das im Konzept nach“ oder „verlinke das im Projektplan“.
---

# Obsidian-Seite

Wie man im Obsidian-Projektordner schreibt, ohne Inhalte zu verlieren, mit Properties und Verlinkung nach den Regeln
des Projekts.

## Projektkontext zuerst

Aus der Regeldatei des Projekts (meist `CLAUDE.md` im Projektordner) lesen: Vault und Projektordner, Ablage-
struktur, Property-Schema, Verlinkungsregeln, Index-Seite. Weicht die Regeldatei von diesem Skill ab, gilt sie.

## Zugriff je Umgebung

Read/Write/Edit/Bash in claude.ai laufen in einem Cloud-Container, nicht auf dem Rechner. Wer dort auf den
Vault-Pfad schreibt, erzeugt eine fremde Datei, die niemand sieht.

| Umgebung                              | Zugriff                                                                                                                                                                  |
| ------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| claude.ai/App mit verbundenem Rechner | `device_bash`, Ordner unter `$HOME/mnt/<Ordnername>`. Ist kein Ordner verbunden, einmal `device_request_folder_access` mit dem absoluten Pfad und kurzem Grund aufrufen. |
| Claude Code auf dem Rechner           | Pfad direkt mit Read/Edit/Bash, in Anführungszeichen (Leerzeichen, `~`).                                                                                                 |
| Ohne Rechner                          | Kein Vault-Zugriff. Nichts simulieren; die geplante Änderung als offenen Punkt melden.                                                                                   |

Eigenheiten von `device_bash`: Jeder Aufruf ist eine neue Shell, also `cd` in jedem Befehl. Timeout höchstens 180 s.
Löschen ist gesperrt (`mv` geht). Bilder oder PDFs ansehen: `device_stage_files`, danach Read.

## Lesen

- Erst die Gliederung (`grep -n "^#" Seite.md`), dann gezielt Abschnitte (`sed -n 'a,bp'`). Große Seiten nicht ganz
  laden, wenn ein Abschnitt reicht.
- Einstieg über die Index-Seite des Projekts, dann die betroffenen Seiten.

## Ändern: ersetzen mit Prüfung

Eine Datei nie aus einer Tool-Ausgabe neu abtippen; Ausgaben werden gekürzt, und dann fehlt still ein Teil der Seite.
Immer lesen, im Speicher ersetzen, zurückschreiben, und dabei prüfen, dass jede Fundstelle genau einmal vorkommt:

```bash
cd "$HOME/mnt/<Ordner>" && cp "Seite.md" /tmp/Seite.bak && python3 - <<'EOF'
p = "Seite.md"
s = open(p, encoding="utf-8").read()
reps = [
    ("alter Text", "neuer Text"),
    ("version: 0.3.3\nupdated: 2026-11-01", "version: 0.3.4\nupdated: 2026-11-04"),
]
for a, b in reps:
    assert s.count(a) == 1, (s.count(a), a[:60])
    s = s.replace(a, b)
open(p, "w", encoding="utf-8").write(s)
print("ok")
EOF
```

- Schlägt ein `assert` fehl, ist nichts geschrieben. Die Fundstelle präzisieren statt das `assert` zu lockern.
- Neue Seite: vollständigen Inhalt per Heredoc schreiben (`cat > "Ordner/Neue Seite.md" <<'EOF' … EOF`).
- In Claude Code lokal tut es das Edit-Tool mit eindeutigem `old_string`.

## Properties

Bei neuer oder inhaltlich geänderter Seite das Property-Schema des Projekts anwenden. Üblich:

- `version` als Dokumentrevision: PATCH für eine kleine sachliche Ergänzung, MINOR für eine substanzielle Erweiterung,
  MAJOR für eine Neufassung. Ohne Inhaltsänderung keine Erhöhung; mehrere Änderungen in einer Sitzung ergeben eine.
- `updated` = heutiges Datum. `stand` nur, wenn der Inhalt dadurch nachweislich aktuell ist.
- `status`: Fachlich-strategische Inhalte bleiben `draft` oder `review`, bis der Projektinhaber freigibt.
- Vorhandene Properties wie `tags` oder IDs unverändert lassen.

## Verlinkung

Ziel ist, dass man eine Seite lesen kann, indem man Verweisen folgt, statt zu suchen.

- Jeder Verweis auf eine Seite, einen Abschnitt, ein Ticket, einen PR oder einen Commit wird verlinkt, je Abschnitt
  bei der ersten Nennung.
- Seite `[[Seite]]`, Abschnitt `[[Seite#Überschrift|Kurztext]]`, eigene Seite `[[#Überschrift|Kurztext]]`,
  Absatz per Block-ID `[[Seite#^e13|E13]]`.
- Überschrift exakt übernehmen, inklusive Nummer. Verlinkte Überschriften enthalten keine Formatierung (Backticks,
  Fettdruck); Obsidian findet sie sonst nicht zuverlässig.
- In Tabellen den Alias-Trenner als `\|` schreiben, sonst bricht die Tabelle.
- Tickets, PRs und Commits als Markdown-Link mit voller URL.
- Neue zentrale Seiten in die Index-Seite eintragen und zurück auf ihre übergeordnete Seite verlinken.
- Wird eine Überschrift umbenannt oder eine Seite verschoben, eingehende Links suchen (`grep -rn "Seite#Alt" .`) und
  nachziehen.
- Texte, die außerhalb von Obsidian eingefügt werden (Systemanweisungen, Asana), enthalten keine Wikilinks.

## Link-Prüfung

Nach dem Schreiben `scripts/linkcheck.py` über die geänderten Seiten laufen lassen. Es meldet Wikilinks, deren Seite
oder Überschrift fehlt.

- Claude Code lokal: `cd "<Projektordner>" && python3 <Skill-Pfad>/scripts/linkcheck.py "Seite.md"`.
- `device_bash`: Das Skript liegt nicht auf dem Rechner. Inhalt lesen und als Heredoc übergeben:
  `cd "$HOME/mnt/<Ordner>" && python3 - "Seite.md" <<'EOF'` + Skriptinhalt + `EOF`.

Treffer bewerten: Seiten außerhalb des Projektordners erscheinen als fehlend. Ein „Hinweis, Überschrift mit
Formatierung“ heißt, das Ziel enthält Backticks oder Fettdruck. Der Link mag funktionieren, bricht aber, sobald jemand
die Formatierung ändert; dann Überschrift und alle eingehenden Links gemeinsam bereinigen, nicht nur den Link.

## Inhalt

- Ist-Zustand, geplante Änderung und offene Entscheidung trennen; Belegtes von Vermutung.
- Jede Aussage steht in genau einer Seite, andere verlinken darauf. Bestehende Seite erweitern statt Parallelseite.
- Keine Ticketlisten oder Changelogs in Obsidian; auf Asana bzw. Git verlinken.
- Mermaid vor dem Einfügen mit `mmdc` rendern, wenn verfügbar; sonst im Bericht sagen, dass die Darstellung
  ungeprüft ist.

## Abschluss

- Geänderte Seiten mit Abschnitt und neuer Version im Bericht nennen.
- Das betroffene Ticket kommentieren (Skill `ccmas-asana-ticket`).
- Wurde die Regeldatei selbst geändert: Skill `ccmas-regeln-pflegen`.
