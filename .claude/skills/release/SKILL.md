---
name: release
description: Veröffentlicht eine neue Version von CCMAS mit Release-Branch, Version, CHANGELOG, PR nach main, Tag und GitHub-Release. Nur für dieses Repository.
argument-hint: "[patch|minor|X.Y.Z]"
disable-model-invocation: true
---

# Release

Führt durch den Ablauf in [RELEASE.md](../../../RELEASE.md). Dort stehen die Regeln, hier nur die Schritte. Jeder Schritt nach außen (Push, PR, Merge, Tag, Release) wird vorher angekündigt und erst nach Bestätigung ausgeführt.

Auftrag: `$ARGUMENTS`. Leer heißt: Art der Version selbst vorschlagen.

## Voraussetzungen

Prüfe alles, bevor du etwas änderst. Trifft etwas nicht zu, nenne es und höre auf.

1. `git status --porcelain` ist leer.
2. `gh auth status` ist angemeldet, `git fetch origin --tags` gelingt.
3. `origin/develop` ist der Stand, aus dem veröffentlicht wird. Der Branch des Releases geht von `origin/develop` aus.
4. `CHANGELOG.md` hat unter „Unveröffentlicht“ mindestens einen Eintrag.
5. Es gibt keinen offenen PR von `release/*` oder `hotfix/*` nach `main` (`gh pr list --base main`).
6. Bei einem Hotfix: der Auftrag nennt eine PATCH-Version, und der Branch geht von `origin/main` aus.

## Schritte

1. **Version bestimmen.** Letzter Tag: `git tag --list 'ccmas--v*' --sort=-v:refname | head -1`; gibt es keinen, ist `version` in `plugin.json` die Ausgangsbasis. Lies „Unveröffentlicht“ und schlage die Version nach [Versionsnummer](../../../RELEASE.md#versionsnummer) vor. Eine Version, die nicht größer als der letzte Tag ist, lehnst du ab.
2. **Branch.** `git switch -c release/X.Y.Z origin/develop` (Hotfix: `hotfix/X.Y.Z` von `origin/main`).
3. **Version setzen.** `version` in `claude-plugin/.claude-plugin/plugin.json`.
4. **CHANGELOG.** Mache `## [Unveröffentlicht]` zu `## [X.Y.Z] – JJJJ-MM-TT` (heutiges Datum), lege darüber einen neuen leeren Abschnitt „Unveröffentlicht“ an und ziehe die Links am Ende nach: `[Unveröffentlicht]` vergleicht `ccmas--vX.Y.Z...develop`, `[X.Y.Z]` vergleicht den vorigen Tag mit dem neuen (ohne vorigen Tag: `commits/ccmas--vX.Y.Z`).
5. **Prüfen.** Führe aus und zeige das Ergebnis:
   - `claude plugin validate claude-plugin`
   - `claude plugin validate .`
   - `python3 -I scripts/links-pruefen.py`
   - `python3 -I scripts/release-pruefen.py`
6. **Commit und Push.** Commit-Nachricht: `Release X.Y.Z`. Frage vor dem Push.
7. **PR nach `main`.** `gh pr create --base main` mit dem CHANGELOG-Abschnitt als Beschreibung. Warte, bis die CI grün ist (`gh pr checks --watch`). Rot heißt: nicht mergen, Ursache nennen.
8. **Merge.** Frage nach, dann `gh pr merge --merge`, kein Squash und kein Rebase, damit die Historie von `develop` auf `main` erhalten bleibt.
9. **Tag und Release.** `git fetch origin main`, dann `git tag -a ccmas--vX.Y.Z <Merge-Commit auf origin/main> -m "CCMAS X.Y.Z"` und `git push origin ccmas--vX.Y.Z`. Danach `gh release create ccmas--vX.Y.Z --title "CCMAS X.Y.Z" --notes-file <Abschnitt X.Y.Z aus dem CHANGELOG>`. Frage vor dem Push des Tags.
10. **Zurück nach `develop`.** Branch `chore/main-nach-develop-X.Y.Z` von `origin/develop`, `git merge --no-ff origin/main`, PR nach `develop`, Merge nach Rückfrage. Bei Konflikten in `CHANGELOG.md` gilt: Abschnitte von `main` behalten, neue Einträge unter „Unveröffentlicht“ aus `develop` behalten.

## Wenn es nicht weitergeht

- **CI rot:** Nichts mergen. Zeige den fehlgeschlagenen Schritt (`gh run view --log-failed`), behebe es auf dem Release-Branch, pushe nach Rückfrage.
- **Merge-Konflikt beim PR nach `main`:** Stoppe und melde es. Löse ihn nicht durch `--force` oder durch Rebase des Release-Branches.
- **Tag oder Release existiert schon:** Stoppe. Nichts überschreiben.
- **Release ist durchgegangen, aber falsch:** Siehe [Wenn etwas schiefgeht](../../../RELEASE.md#wenn-etwas-schiefgeht). Es folgt ein Hotfix mit der nächsten PATCH-Version.

## Grenzen

- Kein `--force`, kein Push auf `main` ohne PR, kein Löschen oder Verschieben eines gesetzten Tags.
- Ändere im Release nichts außer `plugin.json` und `CHANGELOG.md`. Inhaltliche Änderungen gehören in einen Feature-PR nach `develop`.
- Ändere die GitHub-Einstellungen des Repositorys (Standardbranch, Schutzregeln) nicht von dir aus.

## Abschluss

Nenne die Version, die Links zum PR und zum GitHub-Release, den Stand der CI und ob der Rückfluss nach `develop` erledigt ist. Was nicht ausgeführt oder nicht geprüft wurde, steht ausdrücklich dabei.
