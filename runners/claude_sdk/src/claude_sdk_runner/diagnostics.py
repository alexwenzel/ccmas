"""Benannte Fehlerarten des Runners (Diagnosen) und Textwerkzeuge für Meldungen.

Die Codes sind Teil des Protokolls (`error.code`, Muster ``^[a-z][a-z0-9_]{0,63}$``). Die Art (`error.kind`) ist
eine der Vertragsarten ``runtime``, ``subscription_limit``, ``protocol``, ``internal``.
"""

from __future__ import annotations

import re

NOT_LOGGED_IN = "not_logged_in"
NOT_SUBSCRIPTION_LOGIN = "not_subscription_login"
API_CREDENTIALS_REMOVED = "api_credentials_removed"
CLI_VERSION_UNTESTED = "cli_version_untested"
API_BILLING = "api_billing_detected"
SDK_CLI_INCOMPATIBLE = "sdk_cli_incompatible"
CLI_UNAVAILABLE = "cli_unavailable"
BILLING_ERROR = "billing_error"
SUBSCRIPTION_LIMIT = "subscription_limit"
UNKNOWN_POLICY = "unknown_policy_profile"
ISOLATION_NOT_PROVEN = "isolation_not_proven"
INVALID_CWD = "invalid_cwd"
MAX_RUNTIME = "max_runtime_exceeded"
SESSION_FAILED = "session_failed"
INTERNAL = "internal_error"
SKILL_SNAPSHOT_MISSING = "skill_snapshot_missing"
SKILL_SNAPSHOT_INVALID = "skill_snapshot_invalid"
SKILL_SNAPSHOT_MISMATCH = "skill_snapshot_mismatch"
SKILLS_NOT_LOADED = "skills_not_loaded"
OUTPUT_SCHEMA_INVALID = "output_schema_invalid"
STRUCTURED_OUTPUT_FAILED = "structured_output_failed"
STRUCTURED_OUTPUT_MISSING = "structured_output_missing"
STRUCTURED_OUTPUT_INVALID = "structured_output_invalid"
STRUCTURED_OUTPUT_TOO_LARGE = "structured_output_too_large"


class RuntimeDiagnostic(Exception):
    """Konkrete, benannte Ursache, die den Versuch mit einem terminalen `error` beendet."""

    def __init__(self, code: str, message: str, kind: str = "runtime") -> None:
        super().__init__(message)
        self.code = code
        self.message = message
        self.kind = kind


_SECRET_PATTERNS = [
    re.compile(r"sk-ant-[A-Za-z0-9_\-]+"),
    re.compile(r"(?i)\bbearer\s+[A-Za-z0-9._\-]+"),
    re.compile(r"(?i)\b([A-Z0-9_]*(?:API[_-]?KEY|TOKEN|SECRET|PASSWORD|PASSWD)[A-Z0-9_]*)\s*[=:]\s*\S+"),
]


def redact(text: str) -> str:
    """Entfernt offensichtliche Geheimnisse aus Text, der in Meldungen oder Logs gelangt."""
    out = text
    out = _SECRET_PATTERNS[0].sub("<geschwärzt>", out)
    out = _SECRET_PATTERNS[1].sub("Bearer <geschwärzt>", out)
    out = _SECRET_PATTERNS[2].sub(lambda m: f"{m.group(1)}=<geschwärzt>", out)
    return out


def fit(text: str, limit: int) -> str:
    """Einzeilig, geschwärzt und auf `limit` Zeichen gekürzt (nie leer)."""
    one = " ".join(redact(text).split())
    if not one:
        return "-"
    return one if len(one) <= limit else one[: limit - 1] + "…"
