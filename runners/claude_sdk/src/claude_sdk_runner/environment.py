"""Kontrollierte Umgebung des Kindprozesses.

Das SDK vererbt `os.environ` an das Claude-Binary und kann Variablen nur überschreiben, nicht löschen. Der Runner
bereinigt deshalb `os.environ` vor dem Start selbst:

- Variablen, die den Login übersteuern oder auf API-/Provider-Abrechnung umleiten, sind ein **Fehler** (nur der
  Name wird gemeldet, nie der Wert). Es gibt keinen stillen Fallback.
- Alle übrigen `ANTHROPIC_*`-, `CLAUDE_CODE_*`- und `CLAUDE_AGENT_SDK_*`-Variablen (zum Beispiel aus einer
  übergeordneten Claude-Sitzung) werden entfernt.
"""

from __future__ import annotations

import re
from typing import Mapping, MutableMapping

from .diagnostics import API_CREDENTIALS, RuntimeDiagnostic

_CREDENTIAL = re.compile(
    r"^(?:"
    r"ANTHROPIC_(?:API_KEY|AUTH_TOKEN|BASE_URL|PROFILE|ORGANIZATION_ID|FEDERATION_\w+|IDENTITY_TOKEN\w*|\w*_API_KEY|\w*_AUTH_TOKEN|\w*_BASE_URL)"
    r"|CLAUDE_CODE_OAUTH_\w+"
    r"|CLAUDE_CODE_USE_\w+"
    r"|CLAUDE_CODE_API_KEY_\w+"
    r"|AWS_BEARER_TOKEN_BEDROCK"
    r")$"
)
_SCRUB_PREFIXES = ("ANTHROPIC_", "CLAUDE_CODE_", "CLAUDE_AGENT_SDK_", "CLAUDE_REMOTE_CONTROL_")
_SCRUB_EXACT = frozenset({"CLAUDECODE"})


def find_credential_variables(environ: Mapping[str, str]) -> list[str]:
    """Namen der Variablen, die den claude.ai-Login übersteuern würden (sortiert, ohne Werte)."""
    return sorted(name for name in environ if _CREDENTIAL.match(name))


def prepare_environment(environ: MutableMapping[str, str]) -> list[str]:
    """Prüft und bereinigt `environ`. Gibt die entfernten Namen zurück oder löst eine Diagnose aus."""
    found = find_credential_variables(environ)
    if found:
        raise RuntimeDiagnostic(
            API_CREDENTIALS,
            "Variablen in der Umgebung würden die claude.ai-Anmeldung übersteuern oder auf API-Abrechnung "
            "umleiten: " + ", ".join(found) + ". Es gibt keinen API-Fallback; Variablen entfernen.",
        )
    removed = sorted(n for n in environ if n in _SCRUB_EXACT or n.startswith(_SCRUB_PREFIXES))
    for name in removed:
        del environ[name]
    return removed
