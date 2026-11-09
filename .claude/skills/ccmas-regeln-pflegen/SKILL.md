---
name: ccmas-regeln-pflegen
description: Die Arbeitsregeln eines Agent-Projekts (CLAUDE.md im Obsidian-Vault) ändern und alle Kopien und Verweise synchron halten – Projektdokument im Claude-Projekt, Systemanweisung und Projektbeschreibung, Repo-CLAUDE.md, Skills, Änderungsnotiz in Asana. Immer verwenden, wenn eine Arbeitsweise, Konvention, Zuständigkeit, Ressource (GID, Pfad, Zugang) oder Workflow-Entscheidung festgeschrieben werden soll, auch bei Formulierungen wie „halte das fest“, „ab jetzt so“ oder „nimm das in die Regeln auf“.
---

# Regeln pflegen

Ein Agent-Projekt hat eine einzige vollständige Regelquelle: die Regeldatei (meist `CLAUDE.md` im
Obsidian-Projektordner). Alle anderen Stellen sind Kopien oder Kurzfassungen. Wird nur die Quelle geändert, laufen
die Kopien auseinander, und eine Sitzung ohne Vault-Zugriff arbeitet nach alten Regeln. Darum werden alle Stellen in
einem Arbeitsgang nachgezogen.

## Fundstellen

Welche es im Projekt gibt, steht in der Regeldatei (meist in einem Abschnitt „Pflege dieser Regeln“). Üblich:

| Stelle                                                       | Inhalt                                                | Wer schreibt                                    |
| ------------------------------------------------------------ | ----------------------------------------------------- | ----------------------------------------------- |
| Regeldatei im Vault                                          | Regeln, führend                                       | Agent                                           |
| Projektdokument im Claude-Projekt (z. B. `claude/CLAUDE.md`) | identische Kopie                                      | Agent (`Projects`, `project_write`)             |
| Systemanweisung und Beschreibung im Claude-Projekt           | Kurzfassung aus der Regeldatei                        | Projektinhaber fügt ein; Agent liefert den Text |
| `CLAUDE.md` im Repo                                          | nur Repo-Spezifisches plus Verweis auf die Regeldatei | Agent per PR                                    |
| Skills (Repo `.claude/skills/`, claude.ai)                   | Abläufe, die auf Regeln verweisen                     | Agent per PR bzw. Vorschlag                     |
| Asana                                                        | Änderungsnotiz im zuständigen Ticket                  | Agent                                           |

## Ablauf

1. **Einordnen.** Ist es eine Regel zur Arbeitsweise oder eine fachliche Entscheidung? Fachliches gehört in die
   Konzeptseiten, nicht in die Regeldatei. Änderungen an Rolle, Autonomie oder Grenzen des Agenten erst nach
   ausdrücklicher Freigabe des Projektinhabers: vorher Optionen mit Empfehlung vorlegen.
2. **Quelle ändern** (Skill `ccmas-obsidian-seite`, Ersetzen mit Prüfung):
   - Bei Entscheidungen das Datum festhalten („Entscheidung des Projektinhabers vom JJJJ-MM-TT“), damit spätere
     Sitzungen wissen, was gilt und seit wann.
   - Neue Ressourcen (GIDs, Pfade, Accounts, Zugänge) nur verifiziert und mit Datum in die Ressourcen-Tabelle.
     Keine Secrets; höchstens den Fundort.
   - Versionsregel: PATCH für Präzisierung, MINOR für neue oder geänderte Regel, MAJOR für Neufassung.
     `updated` und `stand` auf heute.
3. **Kurzfassung prüfen.** Betrifft die Änderung etwas, das eine Sitzung ohne Vault mindestens wissen muss? Dann den
   Text für Systemanweisung bzw. Beschreibung in der Regeldatei anpassen. Kurz halten, ohne Wikilinks; er wird in ein
   Textfeld kopiert.
4. **Kopie im Claude-Projekt** byte-gleich machen:
   - claude.ai mit verbundenem Rechner: `device_stage_files` auf die Vault-Datei, die bereitgestellte Kopie nach
     `/home/claude/` kopieren, `Projects` mit `method: project_write`, `local_path` auf diese Datei, `path` der
     bestehenden Kopie. Danach die `version`-Zeile vergleichen.
   - Den Dateiinhalt nicht über `content` aus dem Gedächtnis schreiben; `local_path` überträgt die Datei genau.
   - Ohne Zugriff auf das Claude-Projekt (z. B. Claude Code lokal): im Bericht vermerken, dass die Kopie ersetzt
     werden muss.
5. **Repo und Skills prüfen.**
   - Die Repo-`CLAUDE.md` nur ändern, wenn Repo-Spezifisches betroffen ist (Branches, Merge-Regel, Befehle). Regeln
     dort nicht duplizieren.
   - In den Skills nach dem betroffenen Begriff suchen (`grep -rn "<Begriff>" .claude/skills`). Beschreibt ein Skill
     einen geänderten Ablauf, im Repo per Branch und PR anpassen und für claude.ai dieselbe Fassung als
     Verbesserung vorschlagen.
6. **Asana.** Kommentar im zuständigen Ticket (sonst im Ticket für die Projekteinrichtung): neue Version, was sich
   geändert hat, betroffene Abschnitte, ob die Systemanweisung neu eingefügt werden muss.
7. **Übergabe im Chat:**
   - geänderte Abschnitte und neue Version,
   - wenn sich die Kurzfassung geändert hat: den vollständigen Text in einem Codeblock, mit dem Hinweis, den alten
     Text komplett zu ersetzen,
   - Handarbeit, die nur der Projektinhaber erledigen kann.

## Prüfliste

- [ ] Regeldatei geschrieben, Version und Datum angehoben.
- [ ] Kopie im Claude-Projekt mit derselben Version (oder Übergabe vermerkt).
- [ ] Kurzfassung deckt sich mit der Regel; Text geliefert, falls geändert.
- [ ] Repo-`CLAUDE.md` und Skills geprüft.
- [ ] Asana-Kommentar gesetzt.

## Abweichung bemerken

Die eingefügte Systemanweisung steht im Kontext jeder claude.ai-Sitzung des Projekts. Weicht sie von der Kurzfassung
in der Regeldatei ab, den Projektinhaber darauf hinweisen und den aktuellen Text liefern, auch wenn sonst nichts zu
ändern war.
