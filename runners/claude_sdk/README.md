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
