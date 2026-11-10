"""Test-eigenes Probe-Skript (kein Produktionscode): Positivkontrolle der Quellen-Probe und strukturierte Ausgabe.

Aufruf (nur aus `test_runtime_smoke.py`, Unterprozess mit Allowlist-Umgebung): ``python probe_sources.py <Fixture>``.

Es startet das SDK direkt mit demselben lokalen Binary, bereinigt und prüft vorab mit den Modulen des Runners und
verwendet die Rechte des Profils `readonly`, aber mit ``setting_sources=["project"]``. Damit werden die Projektdateien
des Fixtures (CLAUDE.md, Hook, Skill, siehe `support.write_source_probe_fixture`) tatsächlich geladen. Zeigen sich
Hook-Markerdatei und Skill hier, belegt das, dass ihr Ausbleiben im Runner-Lauf (`setting_sources=[]`) eine Wirkung der
Konfiguration ist und nicht der Probe. Zusätzlich fordert der Lauf eine strukturierte Ausgabe (`output_format`) an.

Ausgabe: eine JSON-Zeile auf stdout. Namen von Skills, Plugins oder Connectors werden nicht ausgegeben, nur Anzahlen
und Ja/Nein-Angaben.
"""

from __future__ import annotations

import asyncio
import json
import os
import sys
from pathlib import Path
from typing import Any

from claude_agent_sdk import ClaudeAgentOptions, ClaudeSDKClient, ResultMessage, SystemMessage

from claude_sdk_runner.environment import prepare_environment
from claude_sdk_runner.runtime import preflight
from support import PROBE_SKILL, REAL_CLAUDE, SOURCE_MARKER

SCHEMA: dict[str, Any] = {
    "type": "object",
    "properties": {"answer": {"type": "string"}, "count": {"type": "integer"}},
    "required": ["answer", "count"],
    "additionalProperties": False,
}
OUTPUT_FORMAT: dict[str, Any] = {"type": "json_schema", "schema": SCHEMA}
PROMPT = "Gib als Antwort answer mit dem Text ok und count mit der Zahl 3 zurück. Verwende keine Werkzeuge."


def _count(data: dict[str, Any], key: str) -> int | None:
    value = data.get(key)
    return len(value) if isinstance(value, (list, dict)) else None


async def probe(options: ClaudeAgentOptions) -> dict[str, Any]:
    init: dict[str, Any] = {}
    result: ResultMessage | None = None
    async with ClaudeSDKClient(options) as client:
        await client.query(PROMPT)
        async for msg in client.receive_response():
            if isinstance(msg, SystemMessage) and msg.subtype == "init":
                init = msg.data
            elif isinstance(msg, ResultMessage):
                result = msg
    skills = [str(s) for s in init.get("skills") or []]
    commands = [str(s) for s in init.get("slash_commands") or []]
    return {
        "session_id": init.get("session_id"),
        "model": init.get("model"),
        "claude_code_version": init.get("claude_code_version"),
        "api_key_source": init.get("apiKeySource"),
        "init_counts": {k: _count(init, k) for k in ("mcp_servers", "plugins", "skills", "agents", "slash_commands")},
        "probe_skill_listed": PROBE_SKILL in skills or PROBE_SKILL in commands,
        "result_subtype": result.subtype if result else None,
        "is_error": result.is_error if result else None,
        "num_turns": result.num_turns if result else None,
        "structured_output": result.structured_output if result else None,
        "claude_md_marker_in_text": SOURCE_MARKER in ((result.result or "") if result else ""),
    }


def main(argv: list[str]) -> int:
    fx = Path(argv[0]).resolve()
    scrub = prepare_environment(os.environ)  # wie der Runner: Login-Overrides und Elternsitzung entfernen
    pre, pol = preflight({"policy_profile": "readonly", "cwd": str(fx), "claude_binary": REAL_CLAUDE}, os.environ, scrub)
    options = ClaudeAgentOptions(
        cli_path=pre.claude_binary, cwd=pol.cwd, model="haiku", effort="low", permission_mode=pol.permission_mode,
        tools=list(pol.tools), allowed_tools=list(pol.allowed_tools), settings=json.dumps(pol.settings),
        setting_sources=["project"], max_turns=6, output_format=OUTPUT_FORMAT,
    )
    out = asyncio.run(probe(options))
    out["removed_variables"] = len(scrub.removed)
    sys.stdout.write(json.dumps(out) + "\n")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
