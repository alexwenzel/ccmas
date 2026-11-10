"""Policy-Profile: aus `RunSpec.policy_profile` werden SDK-Optionen für die technische Dateisystemgrenze.

Ergebnis der Vorversuche (docs/architecture/runtime-capabilities.md, Abschnitt 5): Eine Grenze entsteht erst aus zwei
Ebenen. Die OS-Sandbox der CLI (macOS Seatbelt) gilt nur für Shell-Prozesse, Permission-Regeln nur für die
eingebauten Dateiwerkzeuge. `dontAsk` verweigert alles, was nicht ausdrücklich erlaubt ist.

Profile:

- ``readonly``: nur das Read-Werkzeug, auf das Arbeitsverzeichnis begrenzt.
- ``scoped_write``: Read/Write/Edit auf das Arbeitsverzeichnis begrenzt, Bash nur in der Sandbox (Schreiben nur
  im Arbeitsverzeichnis, Lesen verweigert für Elternverzeichnis und Home, ausgenommen das Arbeitsverzeichnis).
- ``autonomous``: nicht verfügbar (kein vollständiger Isolationsnachweis), wird abgelehnt.
"""

from __future__ import annotations

import os
from dataclasses import dataclass, field
from typing import Any

from .diagnostics import INVALID_CWD, ISOLATION_NOT_PROVEN, UNKNOWN_POLICY, RuntimeDiagnostic

PROFILES = ("readonly", "scoped_write")

_AUTONOMOUS_GAPS = [
    "Commit und Push aus der Sandbox sind nicht nachgewiesen (das .git eines Worktrees liegt außerhalb des Arbeitsverzeichnisses).",
    "Testläufe der Werkzeugkette (go, uv, Caches unter dem Home-Verzeichnis) sind in der Sandbox nicht nachgewiesen.",
    "Netzwerkgrenze der Sandbox und Prozesszählung sind nicht nachgewiesen.",
    "Hooks, MCP-Server und ausgenommene Befehle laufen außerhalb der Sandbox und sind nicht abgedeckt.",
    "Nachgewiesen ist nur die Dateisystemgrenze unter macOS mit Claude Code 2.1.311.",
]


def autonomous_execution_status() -> dict[str, Any]:
    """Auskunft, ob autonome Ausführung freigegeben werden darf (derzeit: nein) und warum nicht."""
    return {"available": False, "reasons": list(_AUTONOMOUS_GAPS)}


@dataclass(frozen=True)
class Policy:
    profile: str
    permission_mode: str
    tools: list[str]
    allowed_tools: list[str]
    disallowed_tools: list[str] = field(default_factory=list)
    sandbox: dict[str, Any] | None = None
    settings: dict[str, Any] | None = None
    cwd: str = ""


def _rule(tool: str, path: str) -> str:
    return f"{tool}(//{path.lstrip('/')}/**)"


def build_policy(profile: str, cwd: str, *, home: str | None = None) -> Policy:
    """Erzeugt die Policy für `profile` und das Arbeitsverzeichnis `cwd` (muss existieren)."""
    if profile == "autonomous":
        raise RuntimeDiagnostic(
            ISOLATION_NOT_PROVEN,
            "Autonome Ausführung ist nicht verfügbar: " + " ".join(_AUTONOMOUS_GAPS),
        )
    if profile not in PROFILES:
        raise RuntimeDiagnostic(UNKNOWN_POLICY, f"Unbekanntes Policy-Profil: {profile}")
    if not os.path.isdir(cwd):
        raise RuntimeDiagnostic(INVALID_CWD, "Das Arbeitsverzeichnis existiert nicht oder ist kein Verzeichnis.")
    real = os.path.realpath(cwd)
    home_real = os.path.realpath(home if home is not None else os.path.expanduser("~"))
    parent = os.path.dirname(real)
    if real in ("/", home_real) or parent == "/":
        raise RuntimeDiagnostic(
            INVALID_CWD,
            "Das Arbeitsverzeichnis darf nicht die Wurzel, das Home-Verzeichnis oder ein Verzeichnis direkt unter der "
            "Wurzel sein; sonst ließe sich die Lesegrenze nicht ziehen.",
        )
    settings = {"permissions": {"blockReadsOutsideWorkingDirectories": True}}
    if profile == "readonly":
        return Policy(
            profile=profile, permission_mode="dontAsk", tools=["Read"],
            allowed_tools=[_rule("Read", real)], settings=settings, cwd=real,
        )
    deny_read = [parent] if home_real == parent else [parent, home_real]
    sandbox = {
        "enabled": True,
        "failIfUnavailable": True,
        "allowUnsandboxedCommands": False,
        "autoAllowBashIfSandboxed": True,
        "filesystem": {"allowWrite": [real], "denyRead": deny_read, "allowRead": [real]},
    }
    return Policy(
        profile=profile, permission_mode="dontAsk", tools=["Read", "Write", "Edit", "Bash"],
        allowed_tools=[_rule("Write", real), _rule("Edit", real), _rule("Read", real), "Bash"],
        sandbox=sandbox, settings=settings, cwd=real,
    )
