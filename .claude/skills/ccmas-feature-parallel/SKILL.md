---
name: ccmas-feature-parallel
description: Ein oder mehrere Code-Features parallel entwickeln – je Feature eigener Worktree mit frischem Branch von develop, Tests gegen isolierte Test-DB, Push und PR auf develop; Merge per Schalter (Default nur PR).
---

# ccmas-feature-parallel

Entwickelt ein oder mehrere Features gleichzeitig. Jedes Feature läuft isoliert:

- eigener Git-Worktree, frischer Branch `feature/<slug>` von `origin/develop`
- eigene, frisch aufgesetzte Testdatenbank, nie die Datenbank aus der Haupt-`.env`
- erst wenn lokal alles grün ist: Push und PR auf `develop`
- Merge nur, wenn der Merge-Schalter auf `auto` steht. Default ist `pr`: PR anlegen, nicht mergen, Nutzer benachrichtigen.

Eingabe: $ARGUMENTS

## Aufruf

```
/ccmas-feature-parallel [--merge=pr|auto]
<slug>: <Beschreibung> [(auto)|(pr)]
<slug>: <Beschreibung> [(auto)|(pr)]
```

- Ein Feature je Zeile oder durch `;` getrennt. Fehlt der Slug, aus der Beschreibung ableiten (kebab-case, höchstens 40 Zeichen).
- `--merge=…` gilt für alle Features. `(auto)` oder `(pr)` hinter einem Feature überschreibt das für dieses Feature.
- Natürliche Sprache gilt auch: „merge selbst“, „automatisch mergen“ → `auto`; „nur PR“, „ich merge selbst“ → `pr`.
- Ohne Angabe: `pr`.

Beispiel:

```
/ccmas-feature-parallel
login-magic-link: Login per Magic Link
csv-export: CSV-Export der Rechnungsliste (auto)
```

## Merge-Modi

| Modus          | Verhalten                                                                                                   |
| -------------- | ----------------------------------------------------------------------------------------------------------- |
| `pr` (Default) | Push, PR auf `develop` anlegen, **nicht mergen**. Am Ende den Nutzer benachrichtigen, dass er mergen kann. |
| `auto`         | Push, PR anlegen, CI abwarten, dann selbst mit `gh pr merge` in `develop` mergen.                           |

`auto` mergt nur, wenn alles zutrifft: lokale Prüfungen grün, CI-Checks grün (oder keine vorhanden), PR konfliktfrei
und von GitHub als mergebar gemeldet. Sonst bleibt die PR offen, der Grund kommt als PR-Kommentar und in den Bericht,
und das Feature wird wie `pr` behandelt.

Harte Regeln für beide Modi:

- Nie direkt auf `develop` (oder `main`) pushen. PRs immer mit `--base develop`.
- Force-Push nur als `git push --force-with-lease` auf den eigenen Feature-Branch nach einem Rebase.
- Nie gegen eine Datenbank testen oder migrieren, die nicht die Feature-Test-DB ist (siehe Guard).

## Phase 0 – Vorbereitung (Hauptsitzung)

1. **Eingabe auslegen:** Features, Slugs und Merge-Modus je Feature bestimmen. Nur bei echter Unklarheit nachfragen.
2. **Voraussetzungen prüfen**, jeweils als einzelner, einfacher Befehl:
   - `git rev-parse --show-toplevel`
   - `git remote get-url origin`
   - `gh auth status`
   - `git fetch origin develop`
   - `git rev-parse --verify origin/develop`
   - Bei Docker in der Konfiguration: `docker info --format '{{.ServerVersion}}'`

   Schlägt etwas fehl: abbrechen und dem Nutzer sagen, was fehlt.
3. **Projektkonfiguration laden:** `.claude/ccmas-feature-parallel.md` im Repo-Root (Vorlage unten). Fehlt sie: aus dem
   Repo ableiten (`package.json`-Scripts, Lockfile, `docker-compose*.yml`, `prisma/schema.prisma`, `drizzle.config.*`,
   `knexfile.*`, `.env.example`), dem Nutzer als Vorschlag zeigen und nach Bestätigung speichern. Das ist die einzige
   Stelle, an der vor dem Start gefragt wird.
4. **Umgebungsdateien:** Braucht das Projekt eine `.env` und gibt es keine `.worktreeinclude` im Repo-Root, vorschlagen,
   eine mit `.env` (und ggf. `.env.local`) anzulegen. Claude Code kopiert diese gitignorierten Dateien dann in jeden
   neuen Worktree.
5. **Überschneidungen:** Werden zwei Features absehbar dieselben Module oder Migrationen ändern, kurz darauf hinweisen
   und anbieten, sie nacheinander statt parallel zu bauen.

## Phase 1 – Fan-out

Für jedes Feature einen Subagent starten, **alle in derselben Antwort**, jeweils mit dem Agent-Tool,
`isolation: "worktree"`, im Hintergrund. Der Prompt jedes Subagents ist der Block aus Phase 2 mit eingesetzten Werten
und der **vollständigen** Projektkonfiguration im Wortlaut, denn der Worktree enthält keine untracked Dateien aus dem
Haupt-Checkout.

Der Subagent-Worktree startet vom Default-Branch des Remotes. Schritt 1 in Phase 2 legt den Feature-Branch trotzdem
explizit auf `origin/develop`, damit die Basis immer stimmt.

## Phase 2 – Auftrag an den Feature-Agenten

Diesen Block in jeden Subagent-Prompt kopieren und `{SLUG}`, `{BESCHREIBUNG}`, `{MERGE}` und `{KONFIG}` ersetzen.

````markdown
Du entwickelst genau ein Feature isoliert in deinem eigenen Git-Worktree.

Feature: {SLUG}
Beschreibung: {BESCHREIBUNG}
Merge-Modus: {MERGE}   (pr = nur PR anlegen, nicht mergen; auto = nach grüner CI selbst mergen)

Projektkonfiguration:
{KONFIG}

Regeln:
- Stelle keine Rückfragen. Wenn du blockiert bist: aufhören, nichts pushen, im Bericht erklären warum.
- Arbeite nur in deinem Worktree. Führe git-Befehle als einfache, einzelne Befehle aus.
- Nie auf develop oder main pushen. Nie die Datenbank aus der Haupt-.env benutzen.
- Jeder Befehl, der Datenbank oder Tests berührt, beginnt mit `. ./.fp-guard.sh &&`.

Schritte:

1. Branch anlegen
   git fetch origin develop
   git switch -c feature/{SLUG} origin/develop
   Existiert feature/{SLUG} schon auf origin: abbrechen und melden.

2. Umgebung
   - Abhängigkeiten installieren (`install` aus der Konfiguration).
   - DB-Name und Ports bestimmen:
       DB_NAME=test_<slug, Nicht-[a-z0-9] durch _ ersetzt>
       OFF = (cksum des Slugs) % 400
       DB_PORT = db_port_basis + OFF, APP_PORT = app_port_basis + OFF
     Ist ein Port belegt (`lsof -iTCP:<port> -sTCP:LISTEN`), um 1 erhöhen, bis er frei ist.
   - `.fp.env` im Worktree-Root schreiben:
       FP_DB_NAME=<DB_NAME>
       FP_DB_PORT=<DB_PORT>
       <db_url_var>=<db_url mit eingesetzten {DB_NAME} und {DB_PORT}>
       <app_port_var>=<APP_PORT>
       NODE_ENV=test
   - `.fp-guard.sh` im Worktree-Root schreiben (`<db_url_var>` konkret einsetzen):
       set -a; . ./.fp.env; set +a
       case "$<db_url_var>" in *localhost*|*127.0.0.1*|file:*) ;; *) echo "ABBRUCH: DB nicht lokal" >&2; return 1 2>/dev/null || exit 1;; esac
       case "$<db_url_var>" in *"$FP_DB_NAME"*) ;; *) echo "ABBRUCH: nicht die Feature-Test-DB" >&2; return 1 2>/dev/null || exit 1;; esac
   - `.fp.env`, `.fp-guard.sh` und `.fp-pr.md` in die Datei eintragen, die `git rev-parse --git-path info/exclude`
     liefert, damit sie nie committet werden.
   - In gitignorierten Kopien von `.env`, `.env.local`, `.env.test` im Worktree (vorher mit `git check-ignore` prüfen)
     die Zeile `<db_url_var>=…` durch die Test-URL ersetzen. Getrackte Dateien dafür nicht ändern.

3. Datenbank frisch aufsetzen
   . ./.fp-guard.sh && <db_down> ; true
   . ./.fp-guard.sh && <db_up>
   . ./.fp-guard.sh && <db_migrate>
   . ./.fp-guard.sh && <db_seed>     (falls konfiguriert)

4. Implementieren
   - Repo-Konventionen lesen (CLAUDE.md, README, bestehender Code im betroffenen Bereich).
   - Feature umsetzen und Tests für das neue Verhalten schreiben, inklusive Integrationstests gegen die Datenbank,
     wo das Feature Daten liest oder schreibt.
   - In kleinen, sinnvollen Commits committen, Commit-Stil des Repos übernehmen (sonst `feat({SLUG}): …`).

5. Komplett testen gegen frische DB
   - Schritt 3 vollständig wiederholen (down, up, migrate, seed).
   - Alle `checks` aus der Konfiguration in Reihenfolge ausführen, jeweils mit `. ./.fp-guard.sh &&` davor.
   - Fehler beheben und Schritt 5 wiederholen. Nach 5 Durchläufen ohne Fortschritt: abbrechen, nicht pushen, berichten.

6. Auf develop aktualisieren
   git fetch origin develop
   git rebase origin/develop
   Konflikte lösen. Nicht sinnvoll lösbar: `git rebase --abort`, nicht pushen, berichten.
   Hat der Rebase neue Commits aus develop gebracht: Schritt 5 erneut.

7. Push und PR
   git push -u origin feature/{SLUG}
   PR-Text nach `.fp-pr.md` schreiben:
     ## Zusammenfassung
     ## Änderungen
     ## Tests   (ausgeführte Checks mit Ergebnis; Hinweis: gegen frische Test-DB <DB_NAME>)
     ## Migrationen   (ja/nein, Dateien)
     ## Merge-Modus   ({MERGE})
   gh pr create --base develop --head feature/{SLUG} --title "<prägnanter Titel>" --body-file .fp-pr.md

8. Merge
   - Modus pr: nicht mergen. Weiter mit Schritt 9.
   - Modus auto:
     a) gh pr checks feature/{SLUG} --watch --fail-fast
        Meldet gh "no checks reported": weiter ohne CI. Schlägt ein Check fehl: nicht mergen (Fallback).
     b) gh pr view feature/{SLUG} --json mergeable,mergeStateStatus
        - MERGEABLE und CLEAN oder HAS_HOOKS: mergen.
        - BEHIND: Schritt 6 und 5, dann `git push --force-with-lease`, zurück zu a). Höchstens 2 Mal.
        - sonst (BLOCKED, DIRTY, UNKNOWN …): nicht mergen (Fallback).
     c) gh pr merge feature/{SLUG} --<merge_methode>
        Nicht `--delete-branch` benutzen: gh will dann lokal auf develop wechseln, was im Worktree scheitert.
        Danach: git push origin --delete feature/{SLUG}   (Fehler ignorieren, falls GitHub ihn schon gelöscht hat)
     Fallback: gh pr comment feature/{SLUG} --body "Auto-Merge nicht ausgeführt: <Grund>"

9. Aufräumen
   . ./.fp-guard.sh && <db_down>

10. Bericht (deine letzte Nachricht, genau diese Felder):
    Feature, Branch, PR-URL, Checks (je Check grün/rot), Migrationen (ja/nein),
    Merge-Status (gemergt | offen – bereit zum Mergen | offen – Grund), offene Punkte.
````

## Phase 3 – Abschluss (Hauptsitzung)

Wenn alle Subagents fertig sind:

1. **Übersicht** als Tabelle: Feature | Branch | PR | Checks | Merge-Status.
2. **Offene PRs** (Modus `pr` oder Fallback) ausdrücklich als „bereit zum Mergen“ mit Link nennen. Reihenfolge empfehlen:
   PRs mit Migrationen und Basisänderungen zuerst. Nach jedem Merge können die übrigen PRs hinter `develop` liegen:
   dann `gh pr update-branch <nr> --rebase` oder das Feature neu testen lassen.
3. **Benachrichtigen**, damit der Nutzer es auch ohne Blick ins Terminal mitbekommt:
   - macOS: `osascript -e 'display notification "<n> PR(s) bereit zum Mergen" with title "ccmas-feature-parallel"' || true`
   - Ist ein Benachrichtigungs-Tool verfügbar (z. B. Push-Benachrichtigung), zusätzlich darüber.
4. **Nachbesserung** an einer PR: `claude --worktree "#<PR-Nummer>"` öffnet einen Worktree auf dem Stand der PR.

Gescheiterte Features mit Grund nennen. Nichts als fertig melden, was nicht gepusht ist.

## Projektkonfiguration `.claude/ccmas-feature-parallel.md`

Vorlage (Postgres per eigenem Docker-Container je Feature). `{DB_NAME}` und `{DB_PORT}` setzt der Feature-Agent ein.

```
install: npm ci
db_url_var: DATABASE_URL
db_url: postgresql://test:test@127.0.0.1:{DB_PORT}/{DB_NAME}
db_port_basis: 55400
app_port_var: PORT
app_port_basis: 3400
db_up: docker run -d --rm --name fp-{DB_NAME} -e POSTGRES_USER=test -e POSTGRES_PASSWORD=test -e POSTGRES_DB={DB_NAME} -p 127.0.0.1:{DB_PORT}:5432 postgres:16 && for i in $(seq 30); do docker exec fp-{DB_NAME} pg_isready -U test -q && break; sleep 1; done
db_down: docker rm -f fp-{DB_NAME}
db_migrate: npx prisma migrate deploy
db_seed: npx prisma db seed
checks:
  - npm run lint
  - npm run typecheck
  - npm test
  - npm run build
merge_methode: squash
```

Varianten für `db_*`:

| Datenbank                         | db_url                                                | db_up / db_down                                                                                       |
| --------------------------------- | ----------------------------------------------------- | ----------------------------------------------------------------------------------------------------- |
| Postgres, gemeinsamer lokaler Server | `postgresql://postgres@127.0.0.1:5432/{DB_NAME}`   | `psql "postgresql://postgres@127.0.0.1:5432/postgres" -c 'CREATE DATABASE {DB_NAME}'` / `… -c 'DROP DATABASE IF EXISTS {DB_NAME} WITH (FORCE)'` |
| MySQL/MariaDB im Container        | `mysql://root:test@127.0.0.1:{DB_PORT}/{DB_NAME}`     | `docker run -d --rm --name fp-{DB_NAME} -e MYSQL_ROOT_PASSWORD=test -e MYSQL_DATABASE={DB_NAME} -p 127.0.0.1:{DB_PORT}:3306 mysql:8` / `docker rm -f fp-{DB_NAME}` |
| MongoDB, lokaler Server           | `mongodb://127.0.0.1:27017/{DB_NAME}`                 | – (entsteht beim ersten Schreiben) / `mongosh "mongodb://127.0.0.1:27017/{DB_NAME}" --quiet --eval 'db.dropDatabase()'` |
| SQLite                            | `file:./{DB_NAME}.db`                                 | – / `rm -f ./{DB_NAME}.db`                                                                            |

Die Datei kann ins Repo committet werden. Der Orchestrator gibt sie jedem Feature-Agenten trotzdem im Wortlaut mit.

## Voraussetzungen im Projekt

- `gh` angemeldet, Remote `origin` mit Branch `develop`.
- Docker läuft, wenn die Konfiguration Container nutzt.
- `.claude/worktrees/` in `.gitignore`.
- Hintergrund-Agents bleiben an Berechtigungsabfragen hängen. Die benötigten Befehle vorab in
  `.claude/settings.json` erlauben, z. B. `Bash(git *)`, `Bash(gh *)`, `Bash(docker *)`, `Bash(npm *)`, `Bash(npx *)`,
  `Bash(lsof *)`.
- Für `auto`: Damit „CI grün“ etwas bedeutet, sollte auf GitHub eine Pipeline für PRs auf `develop` laufen.