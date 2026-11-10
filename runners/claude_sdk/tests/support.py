"""Hilfen der Runtime-Tests: Nachrichten bauen, Runner in-process aufrufen, Test-Doubles.

Test-Doubles (Stub-Binary, Fake-Client) gibt es nur hier. Der produktive Pfad enthält keine.
"""

from __future__ import annotations

import asyncio
import io
import json
import os
import stat
import threading
from pathlib import Path
from typing import Any, Callable

from claude_agent_sdk import (
    AssistantMessage,
    RateLimitEvent,
    RateLimitInfo,
    ResultMessage,
    SystemMessage,
    TextBlock,
    ToolResultBlock,
    ToolUseBlock,
    UserMessage,
)

from claude_sdk_runner import contracts
from claude_sdk_runner.entrypoint import main

TS = "2026-11-10T12:00:00Z"
REAL_CLAUDE = "/opt/homebrew/bin/claude"


def envelope(type_: str, payload: dict[str, Any], *, execution_id: str = "exec-1", attempt: int = 1) -> str:
    return json.dumps(
        {
            "protocol_version": "1",
            "project_id": "proj-1",
            "execution_id": execution_id,
            "attempt": attempt,
            "correlation_id": "corr-1",
            "type": type_,
            "timestamp": TS,
            "payload": payload,
        }
    )


def start_line(cwd: str, claude_binary: str, **over: Any) -> str:
    payload: dict[str, Any] = {
        "work_order_id": "wo-1",
        "role": "implementer",
        "runtime": "claude",
        "model": "haiku",
        "effort": "low",
        "skill_ids": [],
        "policy_profile": "scoped_write",
        "cwd": cwd,
        "claude_binary": claude_binary,
        "prompt": "Antworte mit dem Wort ok.",
        "max_runtime_seconds": 120,
    }
    payload.update(over)
    return envelope("start", payload)


def command_line(type_: str, **payload: Any) -> str:
    if type_ in ("interrupt", "stop"):
        payload.setdefault("reason", "user_request")
    return envelope(type_, payload)


class Collector(io.RawIOBase):
    """Binäres Ausgabeziel, das vollständige Zeilen sammelt und Beobachter benachrichtigt."""

    def __init__(self) -> None:
        self._buf = b""
        self.lines: list[str] = []
        self._lock = threading.Lock()
        self.on_event: Callable[[dict[str, Any]], None] | None = None

    def writable(self) -> bool:
        return True

    def write(self, data: bytes) -> int:  # type: ignore[override]
        with self._lock:
            self._buf += bytes(data)
            while b"\n" in self._buf:
                line, self._buf = self._buf.split(b"\n", 1)
                self.lines.append(line.decode("utf-8"))
                if self.on_event:
                    self.on_event(json.loads(line))
        return len(data)

    def events(self) -> list[dict[str, Any]]:
        return [json.loads(line) for line in self.lines]


def run_runner(
    lines: list[str],
    environ: dict[str, str],
    *,
    client_factory: Callable[[Any], Any] | None = None,
    max_turns: int | None = None,
    stdin: Any = None,
    out: Collector | None = None,
) -> tuple[int, list[dict[str, Any]], str, Collector]:
    """Ruft main() in-process auf. Gibt Exit-Code, Ereignisse, stderr-Text und das Ausgabeziel zurück."""
    out = out or Collector()
    err = io.StringIO()
    if stdin is None:
        stdin = io.BytesIO(("\n".join(lines) + "\n").encode("utf-8") if lines else b"")
    code = main(stdin, out, err, environ, client_factory=client_factory, max_turns=max_turns)
    return code, out.events(), err.getvalue(), out


def validate_all(out: Collector) -> None:
    """Jede Zeile auf stdout ist ein gültiges Runner-Ereignis (Vertrag from-runner)."""
    for line in out.lines:
        contracts.validate("from-runner", line)


def terminals(events: list[dict[str, Any]]) -> list[dict[str, Any]]:
    return [
        e
        for e in events
        if e["type"] == "result" or (e["type"] == "error" and e["payload"]["terminal"])
    ]


# --- Stub-Binary (Test-Double für das claude-Binary; nur Vorprüfungen, nie eine Sitzung) ------------


def make_stub_cli(tmp: Path, *, version: str = "2.1.311 (Claude Code)", auth: dict[str, Any] | None = None) -> tuple[str, Path]:
    """Legt ein kleines Skript an, das `-v` und `auth status` beantwortet und jeden Aufruf protokolliert."""
    if auth is None:
        auth = {"loggedIn": True, "authMethod": "claude.ai", "apiProvider": "firstParty", "subscriptionType": "max"}
    log = tmp / "stub.log"
    auth_file = tmp / "auth.json"
    auth_file.write_text(json.dumps(auth))
    script = tmp / "claude-stub"
    script.write_text(
        "#!/bin/sh\n"
        f'echo "$@" >> "{log}"\n'
        'if [ "$1" = "-v" ] || [ "$1" = "--version" ]; then echo "' + version + '"; exit 0; fi\n'
        'if [ "$1" = "auth" ] && [ "$2" = "status" ]; then\n'
        f'  cat "{auth_file}"\n'
        f'  if grep -q \'"loggedIn": *true\' "{auth_file}"; then exit 0; else exit 1; fi\n'
        "fi\n"
        "echo SESSION-REQUESTED >&2; exit 9\n"
    )
    script.chmod(script.stat().st_mode | stat.S_IXUSR)
    return str(script), log


# --- Fake-Client (Test-Double für ClaudeSDKClient; liefert echte SDK-Nachrichtentypen) ------------


class Wait:
    """Skript-Element: blockiert, bis interrupt() aufgerufen wurde."""


class Hang:
    """Skript-Element: blockiert dauerhaft (für Laufzeitbegrenzung)."""


def init_message(*, api_key_source: str = "none", version: str = "2.1.311", capabilities: list[str] | None = None,
                 model: str = "claude-haiku-5-5", mode: str = "dontAsk", session_id: str = "11111111-2222-3333-4444-555555555555") -> SystemMessage:
    return SystemMessage(
        subtype="init",
        data={
            "type": "system",
            "subtype": "init",
            "session_id": session_id,
            "model": model,
            "permissionMode": mode,
            "apiKeySource": api_key_source,
            "claude_code_version": version,
            "capabilities": ["interrupt_receipt_v1"] if capabilities is None else capabilities,
            "tools": [],
        },
    )


def result_message(*, subtype: str = "success", is_error: bool = False, terminal_reason: str | None = "completed",
                   result: str | None = "fertig", api_error_status: int | None = None) -> ResultMessage:
    return ResultMessage(
        subtype=subtype, duration_ms=1, duration_api_ms=1, is_error=is_error, num_turns=1,
        session_id="11111111-2222-3333-4444-555555555555", result=result, terminal_reason=terminal_reason,
        api_error_status=api_error_status,
    )


def rate_limit_rejected() -> RateLimitEvent:
    info = RateLimitInfo(status="rejected", resets_at=1794283200, rate_limit_type="five_hour",
                         utilization=None, overage_status=None, overage_resets_at=None,
                         overage_disabled_reason=None, raw={"status": "rejected"})
    return RateLimitEvent(rate_limit_info=info, uuid="u1", session_id="s1")


def tool_use(tool_id: str, name: str, **inp: Any) -> AssistantMessage:
    return AssistantMessage(content=[ToolUseBlock(id=tool_id, name=name, input=inp)], model="claude-haiku-5-5")


def tool_result(tool_id: str, *, is_error: bool = False, content: str = "x") -> UserMessage:
    return UserMessage(content=[ToolResultBlock(tool_use_id=tool_id, content=content, is_error=is_error)])


def text(t: str) -> AssistantMessage:
    return AssistantMessage(content=[TextBlock(text=t)], model="claude-haiku-5-5")


class FakeClient:
    """Ersetzt ClaudeSDKClient im Test. Spielt ein Skript aus echten SDK-Nachrichten ab."""

    created: list["FakeClient"] = []

    def __init__(self, options: Any, script: list[Any]) -> None:
        self.options = options
        self.script = script
        self.prompt: str | None = None
        self.interrupted = asyncio.Event()
        self.interrupt_calls = 0
        FakeClient.created.append(self)

    async def __aenter__(self) -> "FakeClient":
        return self

    async def __aexit__(self, *exc: Any) -> bool:
        return False

    async def query(self, prompt: str) -> None:
        self.prompt = prompt

    async def interrupt(self) -> None:
        self.interrupt_calls += 1
        self.interrupted.set()

    async def receive_response(self):
        for item in self.script:
            if isinstance(item, Wait):
                await self.interrupted.wait()
            elif isinstance(item, Hang):
                await asyncio.sleep(3600)
            else:
                yield item


def fake_factory(script: list[Any]) -> Callable[[Any], FakeClient]:
    FakeClient.created.clear()
    return lambda options: FakeClient(options, script)


def write_pipe_stdin() -> tuple[Any, Callable[[str], None], Callable[[], None]]:
    """Echte Pipe als stdin: der Test schreibt Kommandos später und schließt sie am Ende."""
    r, w = os.pipe()
    reader = os.fdopen(r, "rb")
    writer = os.fdopen(w, "wb", buffering=0)

    def send(line: str) -> None:
        writer.write(line.encode("utf-8") + b"\n")

    def close() -> None:
        writer.close()

    return reader, send, close
