---
name: init
description: Ein Projekt für CCMAS einrichten. Legt die Projektregeln (CLAUDE.md), docs/ und die Einstellungen für Worktrees an, aktiviert das Plugin für das Projekt und trägt die Referenzen auf Board, Spalten und Felder ein. Überschreibt nichts.
disable-model-invocation: true
argument-hint: [Name oder Link des Boards]
---

# Projekt für CCMAS einrichten

Eingabe aus dem Auftrag ($ARGUMENTS): der Name oder der Link des Boards. Fehlt die Angabe, frage danach.

Du arbeitest im Hauptverzeichnis des Projekts. Du überschreibst keine vorhandene Datei und committest nichts.

## Schritte

1. **Prüfen.** Ist das Verzeichnis ein Git-Repository? Gibt es den Integrationsbranch `develop`? Steht das Hauptverzeichnis auf ihm? Fehlt etwas davon, nenne es und frage, ob du trotzdem weitermachen sollst. Lege weder Repository noch Branch an.
2. **Projektregeln.** Gibt es keine `CLAUDE.md`, kopiere `${CLAUDE_PLUGIN_ROOT}/skills/init/CLAUDE.vorlage.md` unverändert nach `CLAUDE.md`. Gibt es schon eine, lass sie stehen und nenne die Abschnitte der Vorlage, die ihr fehlen.
3. **Dokumentation.** Gibt es kein `docs/`, lege `docs/README.md` aus `${CLAUDE_PLUGIN_ROOT}/skills/init/docs-README.vorlage.md` an.
4. **Worktrees.** Diese beiden Einstellungen wirken, wenn Claude Code den Worktree selbst anlegt (`claude --worktree`). Ein anderes Werkzeug verwaltet seine Worktrees selbst.
   - `.gitignore`: Die Zeile `.claude/worktrees/` ergänzen, wenn sie fehlt.
   - `.claude/settings.json`: Den Schlüssel `worktree.baseRef` auf `"head"` setzen. Gibt es die Datei schon, ergänze nur diesen Schlüssel und lass alles andere unverändert. Damit zweigt `claude --worktree` vom Stand des Hauptverzeichnisses ab, also vom Integrationsbranch, statt vom Standardbranch des Repositorys.
5. **Plugin für das Projekt aktivieren.** Steht `ccmas@alexwenzel` noch nicht unter `enabledPlugins` in `.claude/settings.json`, führe `claude plugin install ccmas@alexwenzel --scope project` aus. Der Befehl trägt das Plugin dort ein; alles andere in der Datei bleibt. So laden auch Sitzungen in Worktrees und Sitzungen, die ein anderes Werkzeug startet, das Plugin. Scheitert der Befehl, nenne ihn unter „Offen“.
6. **Referenzen eintragen.** Fülle die Tabellen unter „Referenzen“ in der `CLAUDE.md`:
   - Projekt, Spalten und Felder mit ihren Kennungen liest du über die Verbindung zum Ticketsystem aus dem genannten Board. Bei Auswahlfeldern trägst du jeden Wert mit seiner Kennung ein.
   - Repository aus `git remote get-url origin`.
   - Den Menschen für Rückfragen und Freigaben erfragst du. Nimm nicht ungefragt das Konto, unter dem die Verbindung schreibt.
   - Prüfbefehle: Schlage sie aus dem Projekt vor (Makefile, `package.json` und Ähnliches) und trage sie erst nach Bestätigung ein.
   - „CI am PR“: ja, wenn es unter `.github/workflows/` eine Prüfung für PRs gibt, sonst nein.
   - Fehlt im Board eine Spalte, ein Feld oder ein Wert, lege nichts an. Lass `TODO` stehen und führe es unter „Fehlt im Board“ auf.
7. **Nichts raten.** Eine Kennung, die du nicht gelesen hast, bleibt `TODO`.

## Ausgabe

```
Angelegt: <Dateien>
Übersprungen: <Dateien, die es schon gab>
Fehlt im Board: <Spalten, Felder, Werte, oder „nichts“>
Offen: <verbliebene TODO, oder „nichts“>
Nächster Schritt: Dateien prüfen und committen (CLAUDE.md, docs/, .gitignore, .claude/settings.json)
```
