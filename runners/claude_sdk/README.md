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
entfernt der Runner und meldet ihre Namen. Einstellungsquellen sind immer aus (`setting_sources=[]`), claude.ai-Connectors
sind abgeschaltet; beim Init schreibt der Runner Anzahlen der geladenen Quellen auf stderr. Bei einer CLI-Version, die nicht
exakt der nachgewiesenen entspricht, sind schreibende Profile gesperrt. `--max-turns N` (1 bis 200) begrenzt die Runden der
Sitzung. Befunde und Belege: `docs/architecture/runtime-capabilities.md`.

Skills (CC-35): `skill_ids` brauchen einen `skill_snapshot` im Start (Kennung und Pfad des von `ccmas skills snapshot` geschriebenen
Verzeichnisses). `claude_sdk_runner.skill_snapshot` rechnet vor dem Start alle Hashes nach und gleicht Kennung, Rolle und Skill-IDs ab
(Fehler `skill_snapshot_missing`, `skill_snapshot_invalid`, `skill_snapshot_mismatch`); die Sitzung lädt das Plugin `ccmas` mit genau
diesen Skills (`ccmas:<id>`) und bricht ab, wenn der Init-Frame etwas anderes zeigt (`skills_not_loaded`). Ohne Snapshot ist kein Skill
aufrufbar. Live-Tests: `CCMAS_LIVE_TESTS=1 uv run --frozen pytest -q -m live -k skill`.

```bash
uv run --frozen pytest tests/test_runtime_smoke.py -q                              # ohne Konto, ohne Netzwerk
CCMAS_LIVE_TESTS=1 uv run --frozen pytest tests/test_runtime_smoke.py -m live -q   # 4 echte Läufe (Unterprozess, Allowlist-Umgebung), verbraucht Abo-Kontingent
```
