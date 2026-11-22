---
name: init
description: Ein Projekt für CCMAS einrichten. Legt die Projektregeln (CLAUDE.md), docs/ und die Einstellungen für Worktrees und Verbote an, prüft, dass das Plugin global aktiv ist, und trägt die Referenzen auf Board, Spalten und Felder ein. Überschreibt nichts.
disable-model-invocation: true
argument-hint: "[Name oder Link des Boards]"
---

# Projekt für CCMAS einrichten

Eingabe aus dem Auftrag ($ARGUMENTS): der Name oder der Link des Boards. Fehlt die Angabe, frage danach.

Du arbeitest im Hauptverzeichnis des Projekts, im Gespräch mit dem Menschen. Du überschreibst keine vorhandene Datei und committest nichts.

## Schritte

1. **Prüfen.** Ist das Verzeichnis ein Git-Repository? Gibt es den Integrationsbranch `develop`? Steht das Hauptverzeichnis auf ihm? Fehlt etwas davon, nenne es und frage, ob du trotzdem weitermachen sollst. Lege weder Repository noch Branch an.
2. **Projektregeln.** Gibt es keine `CLAUDE.md`, kopiere `${CLAUDE_PLUGIN_ROOT}/skills/init/CLAUDE.vorlage.md` unverändert nach `CLAUDE.md`. Gibt es schon eine, lass sie stehen und nenne die Abschnitte der Vorlage, die ihr fehlen.
3. **Dokumentation.** Gibt es kein `docs/`, lege `docs/README.md` aus `${CLAUDE_PLUGIN_ROOT}/skills/init/docs-README.vorlage.md` an.
4. **Worktrees.** Diese Einstellungen wirken, wenn Claude Code den Worktree selbst anlegt (`claude --worktree`). Ein Runner verwaltet seine Worktrees selbst.
   - `.gitignore`: Die Zeile `.claude/worktrees/` ergänzen, wenn sie fehlt.
   - `.claude/settings.json`: Den Schlüssel `worktree.baseRef` auf `"head"` setzen. Gibt es die Datei schon, ergänze nur diesen Schlüssel und lass alles andere unverändert. Damit zweigt `claude --worktree` vom ausgecheckten Stand des Hauptverzeichnisses ab, also vom Integrationsbranch, statt vom Standardbranch des Repositorys.
   - `.worktreeinclude`: Frage den Menschen, welche von Git ignorierten Dateien jeder Worktree braucht, etwa `.env`. Nennt er welche und fehlt die Datei, lege sie mit einer Zeile je Datei an. Claude Code kopiert diese Dateien in jeden neuen Worktree.
5. **Verbote.** Ergänze in `.claude/settings.json` unter `permissions.deny` die Regeln, die noch fehlen. Vorhandene Einträge bleiben. `<Integrationsbranch>` ersetzt du durch den Namen aus Schritt 1:

   ```json
   [
     "Bash(git push --force*)",
     "Bash(git push * --force*)",
     "Bash(git push -f *)",
     "Bash(git push * -f)",
     "Bash(git push * -f *)",
     "Bash(git push * main)",
     "Bash(git push * main *)",
     "Bash(git push *:main)",
     "Bash(git push * <Integrationsbranch>)",
     "Bash(git push * <Integrationsbranch> *)",
     "Bash(git push *:<Integrationsbranch>)"
   ]
   ```

   Die Verbote gelten in jeder Sitzung des Projekts, auch wenn sie ohne Rückfragen läuft. Sie stützen die Grenzen der Rollen und ersetzen keinen Branch-Schutz.
6. **Plugin global.** Das Plugin wird nur global aktiviert, nicht im Projekt. Prüfe mit `claude plugin list`, ob `ccmas@alexwenzel` mit Scope `user` aktiv ist. Ist es das nicht, nenne `claude plugin install ccmas@alexwenzel --scope user` unter „Offen“. Steht `ccmas@alexwenzel` unter `enabledPlugins` in `.claude/settings.json`, lass die Datei unverändert und nenne unter „Offen“ `claude plugin uninstall ccmas@alexwenzel --scope project`.
7. **Labels am Repository.** Die Freigabe eines PR steht an zwei Labels: `freigabe: nötig` und `freigabe: erteilt`. Prüfe mit `gh label list`, ob es sie gibt. Fehlt eines, lege es nach dem Ja des Menschen an (`gh label create "<Name>"`).
8. **Referenzen eintragen.** Fülle die Tabellen unter „Referenzen“ in der `CLAUDE.md`:
   - Projekt, Spalten und Felder mit ihren Kennungen liest du über die Verbindung zum Ticketsystem aus dem genannten Board. Bei Auswahlfeldern trägst du jeden Wert mit seiner Kennung ein.
   - Repository aus `git remote get-url origin`.
   - Den Menschen für Rückfragen und Freigaben erfragst du. Nimm nicht ungefragt das Konto, unter dem die Verbindung schreibt.
   - Prüfbefehle: Schlage sie aus dem Projekt vor (Makefile, `package.json` und Ähnliches) und trage sie erst nach Bestätigung ein.
   - „CI am PR“: ja, wenn es unter `.github/workflows/` eine Prüfung für PRs gibt, sonst nein.
   - Fehlt im Board eine Spalte, ein Feld oder ein Wert, lege nichts an. Lass `TODO` stehen und führe es unter „Fehlt im Board“ auf.
9. **Freigaben.** Zeige dem Menschen den Abschnitt „Freigaben“ der `CLAUDE.md`: die Liste, wann ein PR eine Freigabe braucht, und wer entscheidet. Die Vorgabe ist überall der Mensch. Ändere nur, was er ausdrücklich anders will.
10. **Nichts raten.** Eine Kennung, die du nicht gelesen hast, bleibt `TODO`.

## Ausgabe

```text
Angelegt: <Dateien, Labels>
Übersprungen: <was es schon gab>
Fehlt im Board: <Spalten, Felder, Werte, oder „nichts“>
Offen: <verbliebene TODO, oder „nichts“>
Nächster Schritt: Dateien prüfen und committen (CLAUDE.md, docs/, .gitignore, .claude/settings.json, .worktreeinclude)
```
