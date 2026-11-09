# CLAUDE.md – ccmas (Repository)

Die verbindlichen Arbeitsregeln stehen **nicht hier**, sondern in der Regeldatei im Obsidian-Vault:

- Vault `w3entwickler`, Ordner `Eigene Projekte/ccmas`, Datei `CLAUDE.md`
- lokal: `~/Library/Mobile Documents/iCloud~md~obsidian/Documents/w3entwickler/Eigene Projekte/ccmas/CLAUDE.md`
- ohne Vault-Zugriff: Projektdokument `claude/CLAUDE.md` im Claude-Projekt „CCMAS“

Dort stehen auch Asana-Projekt, GIDs, Felder und Workflow. Spezifikation: Seite `Multi Agent Setup` im selben Ordner. Diese Datei enthält nur Repo-Spezifisches; Regeln hier nicht duplizieren.

## Remote und Branches

- Remote: `https://github.com/alexwenzel/ccmas`
- `develop` = Integration; alle Branches gehen von `develop` ab, PRs zurück nach `develop`
- `main` = Standardbranch, nur Releases aus `develop` nach Freigabe
- Branches `feature/CC-<n>-<kurz>`, `fix/CC-<n>-<kurz>`, `docs/CC-<n>-<kurz>`; Commits mit `CC-<n>`
- Der Agent legt PRs an, der Projektinhaber merged (Squash)

## Befehle

```bash
make test     # go test ./...
make race     # go test -race ./...
make build    # bin/ccmas
go vet ./...
make py-test  # Python-Runner-Tests (uv run --frozen pytest)
make check    # test, race, vet, build, py-test
```

## Projektlokale Skills

`.claude/skills/ccmas-<name>/` ist der führende Bestand der CCMAS-Skills. Eine identische Kopie liegt im Vault unter `Eigene Projekte/ccmas/Skills/`; jede Änderung an beiden Orten nachziehen (Regeldatei, Abschnitt 8.3). In diesem Repo gelten die `ccmas-`-Skills, nicht die gleichnamigen Konto-Skills ohne Präfix.

## Subagenten

`.claude/agents/` legt die Worker mit festen Parametern fest; der Orchestrator (Hauptsitzung) delegiert dorthin:

| Agent | Modell / Effort | Rolle |
|---|---|---|
| `ccmas-worker` | sonnet / high | Ein Ticket im vorbereiteten Worktree umsetzen, testen, committen; kein Push, kein Merge, kein Asana/Vault |
| `ccmas-reviewer` | sonnet / high | Unabhängiges, nur lesendes Review des committeten Stands vor dem Push |

Worktrees für parallele Features (`ccmas-feature-parallel`) liegen unter `.claude/worktrees/` und werden nicht eingecheckt.
