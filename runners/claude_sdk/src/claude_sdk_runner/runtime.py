"""Laufzeit: Vorprüfung des lokalen Claude-Binarys und eine echte SDK-Sitzung.

Der produktive Pfad verwendet ausschließlich das offizielle `claude_agent_sdk` (`ClaudeSDKClient`) mit dem
ausdrücklich gewählten, unveränderten lokalen Binary (`cli_path`). Es gibt keinen API-Schlüssel-Pfad, keine
Token-Vermittlung und keinen Fallback. Die Anmeldung erledigt das Binary mit seinem eigenen Login; der Runner liest,
kopiert und speichert keine Zugangsdaten.

`client_factory` ist ein Einhängepunkt für Tests; die Voreinstellung ist der echte `ClaudeSDKClient`.
"""

from __future__ import annotations

import asyncio
import json
import os
import re
import subprocess
from dataclasses import dataclass
from datetime import datetime, timezone
from pathlib import Path
from typing import Any, Callable, MutableMapping, Protocol

import claude_agent_sdk
from claude_agent_sdk import (
    AssistantMessage,
    ClaudeAgentOptions,
    ClaudeSDKClient,
    RateLimitEvent,
    ResultMessage,
    SystemMessage,
    TextBlock,
    ThinkingBlock,
    ToolResultBlock,
    ToolUseBlock,
    UserMessage,
)

from . import diagnostics as dx
from .diagnostics import RuntimeDiagnostic, fit, redact
from .environment import prepare_environment
from .policy import Policy, build_policy

EXPECTED_SDK_VERSION = "0.2.180"
"""Gepinnte SDK-Version (pyproject.toml, uv.lock); Abweichung ist eine Inkompatibilität."""

VERIFIED_CLI_VERSION = "2.1.311"
"""Mit dieser CLI-Version wurden die echten Läufe belegt (docs/architecture/evidence/)."""

MINIMUM_CLI_VERSION = (2, 1, 311)
"""Untergrenze: ältere CLI-Versionen sind nicht nachgewiesen. Neuere sind erlaubt, werden aber als ungetestet gemeldet."""

REQUIRED_CAPABILITIES = ("interrupt_receipt_v1",)
"""Fähigkeiten, die der Init-Frame der CLI melden muss (Interrupt mit Quittung)."""

_VERSION_RE = re.compile(r"^(\d+)\.(\d+)\.(\d+)")
_CMD_TIMEOUT = 15


class EventSink(Protocol):
    terminal_sent: bool

    def send(self, type_: str, payload: dict[str, Any]) -> None: ...


@dataclass(frozen=True)
class Preflight:
    claude_binary: str
    claude_version: str
    auth_source: str
    scrubbed: list[str]
    untested_version: bool


def _run_cli(binary: str, args: list[str], env: dict[str, str]) -> subprocess.CompletedProcess[str]:
    try:
        return subprocess.run([binary, *args], capture_output=True, text=True, timeout=_CMD_TIMEOUT, env=env, stdin=subprocess.DEVNULL)
    except subprocess.TimeoutExpired:
        raise RuntimeDiagnostic(dx.CLI_UNAVAILABLE, f"Zeitüberschreitung bei `claude {' '.join(args)}`.") from None
    except OSError as e:
        raise RuntimeDiagnostic(dx.CLI_UNAVAILABLE, f"Claude-Binary nicht startbar: {e.strerror or type(e).__name__}.") from None


def parse_version(text: str) -> tuple[int, int, int] | None:
    m = _VERSION_RE.match(text.strip())
    return (int(m.group(1)), int(m.group(2)), int(m.group(3))) if m else None


def check_binary(path: str) -> None:
    """Das Binary muss ausdrücklich gewählt sein: absolut, vorhanden, ausführbar und nicht die gebündelte CLI."""
    if not os.path.isabs(path) or not os.path.isfile(path) or not os.access(path, os.X_OK):
        raise RuntimeDiagnostic(dx.CLI_UNAVAILABLE, "claude_binary ist kein vorhandenes, ausführbares Binary (absoluter Pfad erwartet).")
    bundled = Path(claude_agent_sdk.__file__).resolve().parent
    if Path(os.path.realpath(path)).is_relative_to(bundled):
        raise RuntimeDiagnostic(dx.CLI_UNAVAILABLE, "Die im SDK-Paket gebündelte CLI ist nicht zulässig; das lokale Binary muss gewählt werden.")


def preflight(spec: dict[str, Any], environ: MutableMapping[str, str]) -> tuple[Preflight, Policy]:
    """Alle Prüfungen vor dem Start. Löst `RuntimeDiagnostic` aus; es wird keine Sitzung und kein Modellaufruf gestartet."""
    scrubbed = prepare_environment(environ)
    pol = build_policy(spec["policy_profile"], spec["cwd"])
    binary = spec["claude_binary"]
    check_binary(binary)
    if claude_agent_sdk.__version__ != EXPECTED_SDK_VERSION:
        raise RuntimeDiagnostic(
            dx.SDK_CLI_INCOMPATIBLE,
            f"SDK-Version {claude_agent_sdk.__version__} weicht von der gepinnten {EXPECTED_SDK_VERSION} ab.",
        )
    env = dict(environ)
    v = _run_cli(binary, ["-v"], env)
    version = parse_version(v.stdout)
    if v.returncode != 0 or version is None:
        raise RuntimeDiagnostic(dx.SDK_CLI_INCOMPATIBLE, "Die CLI-Version ist nicht lesbar (`claude -v`).")
    if version < MINIMUM_CLI_VERSION:
        raise RuntimeDiagnostic(
            dx.SDK_CLI_INCOMPATIBLE,
            f"CLI {'.'.join(map(str, version))} ist älter als die nachgewiesene Mindestversion "
            f"{'.'.join(map(str, MINIMUM_CLI_VERSION))} (SDK {EXPECTED_SDK_VERSION}).",
        )
    version_text = ".".join(map(str, version))
    a = _run_cli(binary, ["auth", "status"], env)
    try:
        status = json.loads(a.stdout)
        logged_in, method, provider = status["loggedIn"], status["authMethod"], status["apiProvider"]
    except (ValueError, KeyError, TypeError):
        raise RuntimeDiagnostic(dx.SDK_CLI_INCOMPATIBLE, "Die Ausgabe von `claude auth status` ist nicht lesbar.") from None
    # Aus der Ausgabe werden nur diese drei Felder verwendet; Konto-, E-Mail- und Organisationsdaten werden verworfen.
    if logged_in is not True:
        raise RuntimeDiagnostic(dx.NOT_LOGGED_IN, "Kein Login im lokalen Claude-Binary. Bitte dort `/login` mit dem claude.ai-Konto ausführen; es gibt keinen API-Fallback.")
    if method != "claude.ai" or provider != "firstParty":
        raise RuntimeDiagnostic(dx.NOT_SUBSCRIPTION_LOGIN, "Der aktive Weg ist nicht die claude.ai-Anmeldung. Es gibt keinen Wechsel zu API-Abrechnung.")
    pre = Preflight(binary, version_text, "claude_ai", scrubbed, version_text != VERIFIED_CLI_VERSION)
    return pre, pol


def _iso(epoch: float | int | None) -> str:
    if not epoch:
        return "unbekannt"
    return datetime.fromtimestamp(epoch, tz=timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def _safe_id(value: str) -> str:
    cleaned = re.sub(r"[^A-Za-z0-9._:-]", "_", value)[:128] or "t0"
    return cleaned if cleaned[0].isalnum() else "t" + cleaned


class Session:
    """Eine SDK-Sitzung für genau einen Versuch. Meldet Ereignisse an `sink` und endet mit genau einem terminalen Ergebnis."""

    def __init__(
        self,
        spec: dict[str, Any],
        pre: Preflight,
        pol: Policy,
        sink: EventSink,
        log: Callable[[str], None],
        *,
        client_factory: Callable[[ClaudeAgentOptions], Any] | None = None,
        max_turns: int | None = None,
    ) -> None:
        self.spec, self.pre, self.pol, self.sink, self.log = spec, pre, pol, sink, log
        self._factory = client_factory or ClaudeSDKClient
        self._max_turns = max_turns
        self._client: Any = None
        self._ready = asyncio.Event()
        self._interrupt_requested = False
        self._interrupt_sent = False
        self._finished = False
        self._limit_reported = False
        self._assistant_error: str | None = None
        self._tools: dict[str, str] = {}
        self._pending: list[asyncio.Task[None]] = []

    # --- Steuerung ---------------------------------------------------------------------------------------

    def request_interrupt(self, reason: str) -> None:
        """Beendet die laufende Runde kontrolliert (auch für `stop`). Trifft das Kommando vor dem Senden der
        Anfrage ein, wird es vorgemerkt und angewendet, sobald die Sitzung bereit ist."""
        self._interrupt_requested = True
        self.log(f"interrupt angefordert ({reason})")
        self._pending.append(asyncio.ensure_future(self._send_interrupt()))

    async def _send_interrupt(self) -> None:
        await self._ready.wait()
        if self._finished or self._interrupt_sent:
            return
        self._interrupt_sent = True
        try:
            await asyncio.wait_for(self._client.interrupt(), 10)
        except Exception as e:  # noqa: BLE001 - Fehler hier darf den Lauf nicht verdecken
            self.log(f"interrupt fehlgeschlagen: {type(e).__name__}")

    # --- Lauf --------------------------------------------------------------------------------------------

    def _options(self) -> ClaudeAgentOptions:
        return ClaudeAgentOptions(
            cli_path=self.pre.claude_binary,
            cwd=self.pol.cwd,
            model=self.spec["model"],
            effort=self.spec["effort"],
            permission_mode=self.pol.permission_mode,
            tools=list(self.pol.tools),
            allowed_tools=list(self.pol.allowed_tools),
            disallowed_tools=list(self.pol.disallowed_tools),
            sandbox=self.pol.sandbox,  # type: ignore[arg-type]
            settings=json.dumps(self.pol.settings) if self.pol.settings else None,
            setting_sources=[],
            max_turns=self._max_turns,
            resume=self.spec.get("resume_session_id"),
            stderr=lambda line: self.log("cli: " + line.strip()[:300]),
        )

    async def run(self) -> None:
        try:
            if self.pre.untested_version:
                self._warn("cli_version_untested", f"CLI {self.pre.claude_version} ist nicht die nachgewiesene Version {VERIFIED_CLI_VERSION}.")
            if self.spec.get("skill_ids"):
                self._warn("skills_not_applied", "skill_ids werden in diesem Stand nicht angewendet (Skill-Zuordnung folgt in M02/M04).")
            async with self._factory(self._options()) as client:
                self._client = client
                await client.query(self.spec["prompt"])
                # Ein Interrupt/Stop, der früher eintraf, wartet hier und wird jetzt angewendet (nicht wirkungslos).
                self._ready.set()
                try:
                    async with asyncio.timeout(self.spec["max_runtime_seconds"]) as tm:
                        async for msg in client.receive_response():
                            try:
                                self._handle(msg)
                            except _Abort:
                                await self._send_interrupt_now(client)
                                raise
                except TimeoutError:
                    if not tm.expired():
                        raise
                    await self._send_interrupt_now(client)
                    raise RuntimeDiagnostic(dx.MAX_RUNTIME, f"Laufzeitgrenze von {self.spec['max_runtime_seconds']} s überschritten; Lauf unterbrochen.") from None
            if not self.sink.terminal_sent:
                raise RuntimeDiagnostic(dx.SESSION_FAILED, "Die Sitzung endete ohne Ergebnis.")
        except RuntimeDiagnostic as d:
            self._fail(d.code, d.message, d.kind)
        except Exception as e:  # noqa: BLE001 - jeder Fehler des SDK wird zu genau einem terminalen error
            self.log(f"sitzung fehlgeschlagen: {type(e).__name__}: {redact(str(e))[:300]}")
            self._fail(dx.SESSION_FAILED, f"SDK-Sitzung fehlgeschlagen ({type(e).__name__}): {fit(str(e), 400)}", "runtime")
        finally:
            self._finished = True
            for t in self._pending:
                t.cancel()

    async def _send_interrupt_now(self, client: Any) -> None:
        if not self._interrupt_sent:
            self._interrupt_sent = True
            try:
                await asyncio.wait_for(client.interrupt(), 10)
            except Exception as e:  # noqa: BLE001
                self.log(f"interrupt fehlgeschlagen: {type(e).__name__}")

    # --- Ausgabe -------------------------------------------------------------------------------------------

    def _warn(self, code: str, message: str, kind: str = "runtime") -> None:
        self.sink.send("error", {"code": code, "message": fit(message, 4000), "kind": kind, "terminal": False})

    def _fail(self, code: str, message: str, kind: str = "runtime") -> None:
        if self.sink.terminal_sent:
            self.log(f"weiterer Fehler nach terminalem Ergebnis verworfen: {code}")
            return
        self.sink.send("error", {"code": code, "message": fit(message, 4000), "kind": kind, "terminal": True})

    def _result(self, outcome: str, summary: str) -> None:
        self.sink.send("result", {"outcome": outcome, "summary": fit(summary, 8000)})

    # --- Nachrichten des SDK -----------------------------------------------------------------------------------

    def _handle(self, msg: Any) -> None:
        if isinstance(msg, SystemMessage):
            if msg.subtype == "init":
                self._on_init(msg.data)
        elif isinstance(msg, RateLimitEvent):
            info = msg.rate_limit_info
            if info.status == "rejected" and not self._limit_reported:
                self._report_limit(info.rate_limit_type, info.resets_at)
        elif isinstance(msg, AssistantMessage):
            self._on_assistant(msg)
        elif isinstance(msg, UserMessage):
            self._on_user(msg)
        elif isinstance(msg, ResultMessage):
            self._on_result(msg)

    def _on_init(self, data: dict[str, Any]) -> None:
        source = data.get("apiKeySource")
        if source != "none":
            # Nicht über das Abo: sofort abbrechen, kein Werkzeug darf laufen.
            raise _Abort(dx.API_BILLING, "Die Sitzung würde nicht über die claude.ai-Anmeldung abgerechnet (Quelle: "
                         f"{fit(str(source), 60)}). Lauf abgebrochen; es gibt keinen API-Fallback.")
        caps = data.get("capabilities") or []
        missing = [c for c in REQUIRED_CAPABILITIES if c not in caps]
        if missing:
            raise _Abort(dx.SDK_CLI_INCOMPATIBLE, "Der Init-Frame der CLI meldet nicht die benötigte Fähigkeit: " + ", ".join(missing))
        version = str(data.get("claude_code_version", ""))
        if version != self.pre.claude_version:
            raise _Abort(dx.SDK_CLI_INCOMPATIBLE, f"Die gestartete CLI ({fit(version, 30)}) weicht von der geprüften ({self.pre.claude_version}) ab.")
        payload: dict[str, Any] = {
            "session_id": str(data["session_id"]),
            "model": str(data["model"]),
            # Die CLI meldet das wirksame Effort nicht (Init-Frame ohne Effort-Feld): hier steht der übergebene Wert.
            "effort": self.spec["effort"],
            "claude_version": version,
            "claude_binary": self.pre.claude_binary,
            "auth_source": self.pre.auth_source,
        }
        if data.get("permissionMode"):
            payload["permission_mode"] = str(data["permissionMode"])
        self.sink.send("session_started", payload)

    def _on_assistant(self, msg: AssistantMessage) -> None:
        if msg.error:
            self._assistant_error = msg.error
        for block in msg.content:
            if isinstance(block, TextBlock) and block.text.strip():
                self.sink.send("activity", {"kind": "assistant_text", "summary": fit(block.text, 500)})
            elif isinstance(block, ThinkingBlock):
                self.sink.send("activity", {"kind": "thinking", "summary": "Denkt nach"})
            elif isinstance(block, ToolUseBlock):
                self._tools[block.id] = block.name
                payload = {"tool_use_id": _safe_id(block.id), "tool": fit(block.name, 100), "phase": "started"}
                summary = self._summarize(block.name, block.input)
                if summary:
                    payload["summary"] = summary
                self.sink.send("tool_event", payload)

    def _on_user(self, msg: UserMessage) -> None:
        if not isinstance(msg.content, list):
            return
        for block in msg.content:
            if isinstance(block, ToolResultBlock):
                payload = {
                    "tool_use_id": _safe_id(block.tool_use_id),
                    "tool": fit(self._tools.get(block.tool_use_id, "unbekannt"), 100),
                    "phase": "failed" if block.is_error else "finished",
                }
                if block.is_error:
                    payload["summary"] = fit(str(block.content), 200)
                self.sink.send("tool_event", payload)

    def _summarize(self, name: str, tool_input: dict[str, Any]) -> str | None:
        if name == "Bash":
            return fit(str(tool_input.get("command", "")), 200)
        path = tool_input.get("file_path") or tool_input.get("path")
        if path:
            home = os.path.expanduser("~")
            return fit(str(path).replace(home, "~"), 200)
        return None

    def _report_limit(self, window: str | None, resets_at: Any) -> None:
        self._limit_reported = True
        self._warn(
            dx.SUBSCRIPTION_LIMIT,
            f"Abo-Limit erreicht (Fenster {window or 'unbekannt'}), Rücksetzung {_iso(resets_at)}. Es wird nichts über API abgerechnet.",
            kind="subscription_limit",
        )

    def _on_result(self, msg: ResultMessage) -> None:
        err = self._assistant_error
        if self._limit_reported or err == "rate_limit" or msg.api_error_status == 429:
            if not self._limit_reported:
                self._report_limit(None, None)
            self._result("controlled_failure", "Abo-Limit erreicht; der Versuch endet kontrolliert. Wiederholung nach der Rücksetzung.")
        elif err == "authentication_failed":
            self._fail(dx.NOT_LOGGED_IN, "Die Anmeldung wurde abgelehnt oder ist abgelaufen. Im lokalen Binary `/login` ausführen; es gibt keinen API-Fallback.")
        elif err == "billing_error":
            self._fail(dx.BILLING_ERROR, "Die CLI meldet einen Abrechnungsfehler. Es gibt keinen Wechsel zu API-Abrechnung.")
        elif self._interrupt_requested and (msg.terminal_reason or "").startswith("aborted"):
            self._result("cancelled", "Lauf auf Anforderung kontrolliert beendet.")
        elif msg.subtype == "success" and not msg.is_error:
            self._result("succeeded", msg.result or "Lauf beendet.")
        elif msg.subtype == "error_max_turns":
            self._result("controlled_failure", "Die maximale Rundenzahl wurde erreicht.")
        else:
            self._fail(dx.SESSION_FAILED, f"Die Sitzung endete mit einem Fehler (subtype {fit(msg.subtype, 60)}, Grund {fit(str(msg.terminal_reason), 60)}).")


class _Abort(RuntimeDiagnostic):
    """Diagnose während des Laufs: unterbricht die Sitzung vor dem Verlassen."""
