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


# --- Fixture für die Quellen-Probe (Projektdateien, die der Runner nicht laden darf) ------------------------------------

SOURCE_MARKER = "ZEBRA17"
HOOK_MARKER_FILE = "ccmas-hook-ran.marker"
PROBE_SKILL = "ccmas-probe"


def write_source_probe_fixture(fx: Path) -> None:
    """Legt Projektdateien an, die nur geladen werden, wenn `project` als Einstellungsquelle gilt: eine CLAUDE.md mit
    eindeutiger Markeranweisung, einen harmlosen Hook (legt nur eine Markerdatei im Fixture an) und einen Skill."""
    (fx / "CLAUDE.md").write_text(f"Beende jede Antwort mit dem Wort {SOURCE_MARKER}.\n")
    claude = fx / ".claude"
    (claude / "skills" / PROBE_SKILL).mkdir(parents=True)
    (claude / "skills" / PROBE_SKILL / "SKILL.md").write_text(
        f"---\nname: {PROBE_SKILL}\ndescription: Probe-Skill des Selbsttests; nur laden, wenn ausdrücklich verlangt.\n---\n"
        f"Antworte mit {SOURCE_MARKER}.\n")
    command = f'touch "$CLAUDE_PROJECT_DIR/{HOOK_MARKER_FILE}"'
    hook = [{"hooks": [{"type": "command", "command": command}]}]
    (claude / "settings.json").write_text(json.dumps({"hooks": {"SessionStart": hook, "UserPromptSubmit": hook}}, indent=2) + "\n")


# --- Stub-Binary (Test-Double für das claude-Binary: Vorprüfungen, Sitzungsstart nur protokolliert) ---


def make_stub_cli(tmp: Path, *, version: str = "2.1.311 (Claude Code)", auth: dict[str, Any] | None = None) -> tuple[str, Path]:
    """Legt ein kleines Skript an, das `-v` und `auth status` beantwortet und jeden Aufruf protokolliert.

    Zwei Protokolle: `stub.log` (Argumente; bei einem Sitzungsstart zusätzlich `SESSION-REQUESTED`) und, lesbar über
    `stub_calls`, `stub-env.log` mit den **Namen** der Umgebungsvariablen jedes Aufrufs (nie Werte).
    """
    if auth is None:
        auth = {"loggedIn": True, "authMethod": "claude.ai", "apiProvider": "firstParty", "subscriptionType": "max"}
    log = tmp / "stub.log"
    env_log = tmp / "stub-env.log"
    auth_file = tmp / "auth.json"
    auth_file.write_text(json.dumps(auth))
    script = tmp / "claude-stub"
    script.write_text(
        "#!/bin/sh\n"
        f'echo "$@" >> "{log}"\n'
        "kind=session\n"
        'case "$1" in -v|--version) kind=version;; auth) kind=auth;; esac\n'
        f'{{ printf "%s:" "$kind"; env | sed -n \'s/^\\([A-Za-z_][A-Za-z0-9_]*\\)=.*/ \\1/p\' | sort | tr -d "\\n"; echo; }} >> "{env_log}"\n'
        'if [ "$1" = "-v" ] || [ "$1" = "--version" ]; then echo "' + version + '"; exit 0; fi\n'
        'if [ "$1" = "auth" ] && [ "$2" = "status" ]; then\n'
        f'  cat "{auth_file}"\n'
        f'  if grep -q \'"loggedIn": *true\' "{auth_file}"; then exit 0; else exit 1; fi\n'
        "fi\n"
        f'echo SESSION-REQUESTED >> "{log}"\n'
        "echo SESSION-REQUESTED >&2; exit 9\n"
    )
    script.chmod(script.stat().st_mode | stat.S_IXUSR)
    return str(script), log


def stub_calls(binary: str) -> list[tuple[str, set[str]]]:
    """Aufrufe des Stub-Binarys in Reihenfolge: (Art `version`/`auth`/`session`, Namen der Umgebungsvariablen)."""
    env_log = Path(binary).parent / "stub-env.log"
    if not env_log.exists():
        return []
    calls = []
    for line in env_log.read_text().splitlines():
        kind, _, names = line.partition(":")
        calls.append((kind, set(names.split())))
    return calls


# --- Fake-Client (Test-Double für ClaudeSDKClient; liefert echte SDK-Nachrichtentypen) ------------


HANG_LIMIT_SECONDS = 30.0
"""Obergrenze für jeden absichtlichen Hänger der Test-Doubles. Greift die Laufzeitbegrenzung des Runners nicht (Regression),
endet der Hänger nach dieser Zeit mit einem Fehlschlag des Tests, statt die Testsuite hängen zu lassen."""

EXPIRED_HANGS: list[str] = []
"""Hänger, die nicht vom Runner abgebrochen wurden und von selbst abliefen (die Fixture in conftest.py wertet das aus,
auch wenn der Runner die Ausnahme verschluckt)."""


async def bounded_hang(what: str = "hang") -> None:
    """Blockiert, bis der Runner die Aufgabe abbricht (CancelledError). Läuft sie ungestört ab, ist der Test rot."""
    await asyncio.sleep(HANG_LIMIT_SECONDS)
    EXPIRED_HANGS.append(what)
    raise AssertionError(f"Hänger '{what}' wurde nicht abgebrochen (Regression der Laufzeitbegrenzung)")


class Wait:
    """Skript-Element: blockiert, bis interrupt() aufgerufen wurde."""


class Hang:
    """Skript-Element: blockiert (für Laufzeitbegrenzung), höchstens `HANG_LIMIT_SECONDS`."""


def init_message(*, api_key_source: str = "none", version: str = "2.1.311", capabilities: list[str] | None = None,
                 model: str = "claude-haiku-5-5", mode: str = "dontAsk", session_id: str = "11111111-2222-3333-4444-555555555555",
                 extra: dict[str, Any] | None = None) -> SystemMessage:
    data: dict[str, Any] = {
        "type": "system",
        "subtype": "init",
        "session_id": session_id,
        "model": model,
        "permissionMode": mode,
        "apiKeySource": api_key_source,
        "claude_code_version": version,
        "capabilities": ["interrupt_receipt_v1"] if capabilities is None else capabilities,
        "tools": [],
    }
    data.update(extra or {})
    return SystemMessage(subtype="init", data=data)


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
    """Ersetzt ClaudeSDKClient im Test. Spielt ein Skript aus echten SDK-Nachrichten ab.

    Hänger lassen sich je Phase erzwingen (`hang_enter`, `hang_query`, `hang_exit`); `*_cancelled` hält fest, ob die
    blockierte Phase vom Runner abgebrochen wurde (also kein Task zurückbleibt).
    """

    created: list["FakeClient"] = []

    def __init__(self, options: Any, script: list[Any]) -> None:
        self.options = options
        self.script = script
        self.prompt: str | None = None
        self.interrupted = asyncio.Event()
        self.interrupt_calls = 0
        self.calls: list[str] = []
        self.query_delay = 0.0
        self.query_error: Exception | None = None
        self.hang_enter = False
        self.hang_query = False
        self.hang_exit = False
        self.enter_cancelled = False
        self.query_cancelled = False
        self.exit_cancelled = False
        self.exit_calls = 0
        FakeClient.created.append(self)

    @staticmethod
    async def _hang(flag: str, owner: "FakeClient") -> None:
        try:
            await bounded_hang(flag)
        except asyncio.CancelledError:
            setattr(owner, flag, True)
            raise

    async def __aenter__(self) -> "FakeClient":
        if self.hang_enter:
            await self._hang("enter_cancelled", self)
        return self

    async def __aexit__(self, *exc: Any) -> bool:
        self.exit_calls += 1
        if self.hang_exit:
            await self._hang("exit_cancelled", self)
        return False

    async def query(self, prompt: str) -> None:
        if self.hang_query:
            await self._hang("query_cancelled", self)
        await asyncio.sleep(self.query_delay)  # Zeit für früh eingetroffene Kommandos
        if self.query_error is not None:
            raise self.query_error
        self.prompt = prompt
        self.calls.append("query")

    async def interrupt(self) -> None:
        self.interrupt_calls += 1
        self.calls.append("interrupt")
        self.interrupted.set()

    async def receive_response(self):
        for item in self.script:
            if isinstance(item, Wait):
                await self.interrupted.wait()
            elif isinstance(item, Hang):
                await bounded_hang("receive_response")
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
