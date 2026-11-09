"""Importtest des Runner-Pakets in einer gelockten Umgebung.

Es findet bewusst keine Agentenausführung, kein Netzwerk- und kein Kontozugriff statt:
geprüft wird nur, dass Paket und offizielles SDK importierbar sind.
"""

import importlib
import importlib.metadata
import tomllib
from pathlib import Path

PYPROJECT = Path(__file__).resolve().parents[1] / "pyproject.toml"


def test_runner_import():
    runner = importlib.import_module("claude_sdk_runner")
    assert isinstance(runner.__version__, str) and runner.__version__

    # Das offizielle Claude Agent SDK (PyPI: claude-agent-sdk) muss importierbar sein
    # und die vom Runner benötigten Einstiegspunkte bereitstellen.
    sdk = importlib.import_module("claude_agent_sdk")
    for name in ("query", "ClaudeSDKClient", "ClaudeAgentOptions"):
        assert hasattr(sdk, name), f"claude_agent_sdk.{name} fehlt"


def test_installed_sdk_matches_exact_pin_in_pyproject():
    # Die gelockte Umgebung muss genau die in pyproject.toml exakt (==) gepinnte SDK-Version liefern.
    with PYPROJECT.open("rb") as handle:
        dependencies = tomllib.load(handle)["project"]["dependencies"]
    pins = [d for d in dependencies if d.startswith("claude-agent-sdk")]
    assert len(pins) == 1 and "==" in pins[0], f"SDK nicht exakt gepinnt: {pins}"
    expected = pins[0].split("==", 1)[1].strip()
    assert importlib.metadata.version("claude-agent-sdk") == expected
