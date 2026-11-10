"""Kontrollierte Umgebung des Kindprozesses.

Das SDK vererbt `os.environ` an das Claude-Binary und kann Variablen nur überschreiben, nicht löschen. Der Runner
bereinigt deshalb `os.environ` vor jedem Aufruf des Binarys selbst (Vorprüfung ebenso wie SDK-Start):

- Variablen, die den claude.ai-Login übersteuern oder auf API-/Provider-Abrechnung umleiten (zum Beispiel
  `ANTHROPIC_API_KEY`, `CLAUDE_CODE_OAUTH_TOKEN`, `CLAUDE_CODE_USE_*`), werden **entfernt** und als Warnung gemeldet
  (nur der Name, nie der Wert). Es gibt keinen Fallback auf API-Abrechnung; ohne aktive claude.ai-Anmeldung endet der
  Versuch danach in der Vorprüfung.
- Alle übrigen `ANTHROPIC_*`-, `CLAUDE_CODE_*`- und `CLAUDE_AGENT_SDK_*`-Variablen (zum Beispiel aus einer
  übergeordneten Claude-Sitzung) werden ebenfalls entfernt.
- `CLAUDE_CONFIG_DIR` bleibt: es wählt das Konfigurationsverzeichnis, in dem die Anmeldung des Binarys liegt.
"""

from __future__ import annotations

import re
from dataclasses import dataclass
from typing import Mapping, MutableMapping

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


@dataclass(frozen=True)
class ScrubResult:
    """Namen (sortiert, nie Werte) der entfernten Variablen."""

    credentials: list[str]
    """Variablen, die den Login übersteuert oder auf API-/Provider-Abrechnung umgeleitet hätten."""
    other: list[str]
    """Übrige entfernte Variablen (zum Beispiel einer übergeordneten Claude-Sitzung)."""

    @property
    def removed(self) -> list[str]:
        return sorted(self.credentials + self.other)


def find_credential_variables(environ: Mapping[str, str]) -> list[str]:
    """Namen der Variablen, die den claude.ai-Login übersteuern würden (sortiert, ohne Werte)."""
    return sorted(name for name in environ if _CREDENTIAL.match(name))


def prepare_environment(environ: MutableMapping[str, str]) -> ScrubResult:
    """Entfernt alle Variablen, die den Login übersteuern oder aus einer Elternsitzung stammen, aus `environ`."""
    credentials = find_credential_variables(environ)
    other = sorted(
        n for n in environ
        if (n in _SCRUB_EXACT or n.startswith(_SCRUB_PREFIXES)) and n not in credentials
    )
    for name in credentials + other:
        del environ[name]
    return ScrubResult(credentials, other)
