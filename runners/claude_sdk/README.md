# claude_sdk_runner

Schlanker Python-Runner von CCMAS. Er verbindet den Go-Dienst mit dem offiziellen
Claude Agent SDK (`claude-agent-sdk`). Der Runner ist keine zweite Aufgabenverwaltung:
Er erhält einen Auftrag und meldet Ereignisse. Rolle und Grenzen stehen in
`docs/architecture/ccmas-design.md`.

```bash
uv sync --frozen        # gelockte Umgebung herstellen
uv run --frozen pytest  # Tests (keine Agentenausführung, kein Netzwerk)
```

## Verträge

`claude_sdk_runner.contracts` prüft Nachrichten und Dokumente gegen dieselben JSON-Schemas wie der Go-Dienst
(`contracts/` im Repository-Wurzelverzeichnis; für andere Ablageorte `CCMAS_CONTRACTS_DIR`). Das Paket enthält
keine eigene Typdefinition. Regeln und Fehlerklassen: `docs/architecture/contracts.md`.

```bash
uv run --frozen pytest tests/test_contracts.py -q
```

## Runner und Live-Tests

`python -m claude_sdk_runner` liest JSON-Lines-Kommandos (`start`, `answer`, `interrupt`, `stop`) von stdin und
schreibt Ereignisse nach stdout (Protokoll, redigierte Logs auf stderr). Er verwendet das offizielle SDK mit dem
ausdrücklich gewählten lokalen Claude-Binary (`RunSpec.claude_binary`) und dessen eigener claude.ai-Anmeldung; es gibt
keinen API-Fallback. Variablen, die den Login übersteuern würden (`ANTHROPIC_API_KEY`, `CLAUDE_CODE_OAUTH_TOKEN`, ...),
entfernt der Runner und meldet ihre Namen. `--max-turns N` (1 bis 200) begrenzt die Runden der Sitzung. Befunde und
Belege: `docs/architecture/runtime-capabilities.md`.

```bash
uv run --frozen pytest tests/test_runtime_smoke.py -q                              # ohne Konto, ohne Netzwerk
CCMAS_LIVE_TESTS=1 uv run --frozen pytest tests/test_runtime_smoke.py -m live -q   # echte Läufe (Unterprozess, Allowlist-Umgebung), verbraucht Abo-Kontingent
```
