"""Runner-Einstieg: liest JSON-Lines-Kommandos von stdin und schreibt Protokoll-Ereignisse nach stdout.

- stdout enthält ausschließlich gültige Runner-Ereignisse (jede Zeile wird vor dem Schreiben gegen den Vertrag
  `from-runner` geprüft); stderr enthält redigierte, begrenzte Logs. Prompts werden nie protokolliert.
- Genau ein terminales Ergebnis je Versuch (`result` oder `error` mit `terminal=true`). Endet der Prozess ohne
  eines, ist das ein Absturz (Exit-Code 1); die Entscheidung trifft der Go-Dienst.
- Exit-Codes: 0 = terminales Ergebnis gesendet (auch bei terminalem `error`), 1 = Ende ohne Ergebnis,
  2 = ungültiger Start (nichts auf stdout, weil Projekt-/Ausführungsbezug fehlt).
- EOF auf stdin heißt: keine weiteren Kommandos. Der Lauf läuft weiter (Grenze: `max_runtime_seconds`).
"""

from __future__ import annotations

import asyncio
import json
import os
import threading
from datetime import datetime, timezone
from typing import Any, BinaryIO, Callable, MutableMapping, TextIO

from . import contracts
from .contracts import ContractError
from .diagnostics import API_CREDENTIALS_REMOVED, INTERNAL, RuntimeDiagnostic, fit, redact
from .environment import prepare_environment
from .runtime import Session, preflight


class _Oversize:
    """Marker für eine Zeile über dem Größenlimit."""


class Log:
    """Redigierte, begrenzte Logzeilen auf stderr."""

    def __init__(self, stream: TextIO) -> None:
        self._stream = stream

    def __call__(self, message: str) -> None:
        try:
            self._stream.write("[runner] " + redact(message).replace("\n", " ")[:1000] + "\n")
            self._stream.flush()
        except Exception:  # noqa: BLE001 - Logging darf nie den Lauf beenden
            pass


class ProtocolSink:
    """Schreibt Ereignisse mit Umschlag, prüft sie gegen den Vertrag und erzwingt ein terminales Ergebnis."""

    def __init__(self, out: BinaryIO, ctx: dict[str, Any], clock: Callable[[], datetime] | None = None) -> None:
        self._out = out
        self._ctx = ctx
        self._clock = clock or (lambda: datetime.now(timezone.utc))
        self.terminal_sent = False

    def send(self, type_: str, payload: dict[str, Any]) -> None:
        terminal = type_ == "result" or (type_ == "error" and payload.get("terminal") is True)
        if self.terminal_sent:  # gilt für ein zweites terminales Ergebnis ebenso wie für jedes spätere Ereignis
            raise RuntimeError("Ereignis nach dem terminalen Ergebnis verhindert")
        envelope = {
            "protocol_version": "1",
            "project_id": self._ctx["project_id"],
            "execution_id": self._ctx["execution_id"],
            "attempt": self._ctx["attempt"],
            "correlation_id": self._ctx["correlation_id"],
            "type": type_,
            "timestamp": self._clock().strftime("%Y-%m-%dT%H:%M:%SZ"),
            "payload": payload,
        }
        line = json.dumps(envelope, ensure_ascii=False, separators=(",", ":"))
        contracts.validate("from-runner", line)  # ein Runner-Fehler erreicht stdout nie als ungültige Zeile
        self._out.write(line.encode("utf-8") + b"\n")
        self._out.flush()
        if terminal:
            self.terminal_sent = True


def _start_reader(stream: BinaryIO, loop: asyncio.AbstractEventLoop, queue: "asyncio.Queue[Any]") -> None:
    limit = contracts.max_message_bytes()

    def put(item: Any) -> bool:
        try:
            loop.call_soon_threadsafe(queue.put_nowait, item)
            return True
        except RuntimeError:  # Ereignisschleife bereits beendet
            return False

    def work() -> None:
        while True:
            try:
                line = stream.readline(limit + 2)
            except (ValueError, OSError):
                break
            if not line:
                break
            if len(line.rstrip(b"\r\n")) > limit:
                while not line.endswith(b"\n"):  # Rest der überlangen Zeile verwerfen
                    line = stream.readline(65536)
                    if not line:
                        break
                item: Any = _Oversize()
            else:
                item = line.rstrip(b"\r\n")
                if not item:
                    continue
            if not put(item):
                return
        put(None)

    threading.Thread(target=work, name="runner-stdin", daemon=True).start()


async def _next_item(queue: "asyncio.Queue[Any]") -> Any:
    return await queue.get()


def _validate_command(item: Any) -> dict[str, Any]:
    if isinstance(item, _Oversize):
        raise ContractError(["size"], "Zeile über dem Größenlimit")
    return contracts.validate("to-runner", item)


async def _command_loop(queue: "asyncio.Queue[Any]", session: Session, sink: ProtocolSink, log: Log) -> None:
    while True:
        item = await _next_item(queue)
        if item is None:
            log("stdin geschlossen: keine weiteren Kommandos")
            return
        try:
            msg = _validate_command(item)
        except ContractError as e:
            log(f"ungültiges Kommando verworfen: {e.category}")
            _warn(sink, "invalid_command", f"Kommando abgewiesen (Klasse {e.category}).", log)
            continue
        kind = msg["type"]
        if kind in ("interrupt", "stop"):
            session.request_interrupt(f"{kind}:{msg['payload']['reason']}")
        elif kind == "answer":
            _warn(sink, "answer_unsupported", "Antworten auf Fragen werden in diesem Stand nicht unterstützt (M04).", log)
        else:
            _warn(sink, "unexpected_start", "Ein zweiter Start innerhalb eines Versuchs wird ignoriert.", log)


def _warn(sink: ProtocolSink, code: str, message: str, log: Log, kind: str = "protocol") -> None:
    if sink.terminal_sent:
        return
    try:
        sink.send("error", {"code": code, "message": fit(message, 4000), "kind": kind, "terminal": False})
    except Exception as e:  # noqa: BLE001
        log(f"warnung nicht gesendet: {type(e).__name__}")


async def _amain(stdin: BinaryIO, stdout: BinaryIO, log: Log, environ: MutableMapping[str, str],
                 client_factory: Callable[[Any], Any] | None, max_turns: int | None) -> int:
    loop = asyncio.get_running_loop()
    queue: asyncio.Queue[Any] = asyncio.Queue()
    _start_reader(stdin, loop, queue)
    first = await queue.get()
    if first is None:
        log("kein start-Kommando empfangen (stdin leer)")
        return 2
    try:
        start = _validate_command(first)
        if start["type"] != "start":
            raise ContractError(["message_type"], "erstes Kommando ist kein start")
    except ContractError as e:
        log(f"start ungültig: {e.category}")
        return 2
    spec = start["payload"]
    sink = ProtocolSink(stdout, {k: start[k] for k in ("project_id", "execution_id", "attempt", "correlation_id")})
    log(f"start empfangen: execution={start['execution_id']} attempt={start['attempt']} profil={spec['policy_profile']} modell={spec['model']}")
    # Die Umgebung wird vor jedem Aufruf des Binarys bereinigt; entfernte Credential-Variablen werden gemeldet (nur Namen).
    scrub = prepare_environment(environ)
    if scrub.credentials:
        log(f"umgebung: {len(scrub.credentials)} credential-variablen entfernt")
        _warn(sink, API_CREDENTIALS_REMOVED,
              "Aus der Umgebung entfernt, weil sie die claude.ai-Anmeldung übersteuern oder auf API-Abrechnung umleiten "
              "würden: " + ", ".join(scrub.credentials) + ". Es gibt keinen API-Fallback; die Anmeldung des lokalen Binarys "
              "muss aktiv sein.", log, kind="runtime")
    try:
        pre, pol = await asyncio.to_thread(preflight, spec, environ, scrub)
    except RuntimeDiagnostic as d:
        log(f"vorprüfung: {d.code}")
        sink.send("error", {"code": d.code, "message": fit(d.message, 4000), "kind": d.kind, "terminal": True})
        return 0
    except Exception as e:  # noqa: BLE001
        log(f"vorprüfung fehlgeschlagen: {type(e).__name__}")
        sink.send("error", {"code": INTERNAL, "message": f"Vorprüfung fehlgeschlagen ({type(e).__name__}).", "kind": "internal", "terminal": True})
        return 0
    log(f"vorprüfung ok: cli={pre.claude_version} anmeldung={pre.auth_source} entfernt={len(pre.scrubbed)} Variablen")
    if client_factory is None and environ is not os.environ:
        # Das echte SDK vererbt `os.environ` an die CLI. Nur diese Umgebung wurde bereinigt, also darf kein
        # echter Client mit einem anderen Mapping gestartet werden (sonst käme eine unbereinigte Umgebung an die CLI).
        log("echter sdk-start mit fremdem environ-mapping verweigert")
        sink.send("error", {"code": INTERNAL, "kind": "internal", "terminal": True,
                            "message": "Ein echter SDK-Start ist nur mit der bereinigten Prozessumgebung zulässig (os.environ); "
                                       "ein anderes Umgebungs-Mapping wäre unbereinigt."})
        return 0
    session = Session(spec, pre, pol, sink, log, client_factory=client_factory, max_turns=max_turns)
    commands = asyncio.ensure_future(_command_loop(queue, session, sink, log))
    try:
        await session.run()
    finally:
        commands.cancel()
    if not sink.terminal_sent:  # Programmfehler: nie stillschweigend enden
        log("sitzung endete ohne terminales ergebnis")
        return 1
    return 0


def main(stdin: BinaryIO, stdout: BinaryIO, stderr: TextIO, environ: MutableMapping[str, str] | None = None, *,
         client_factory: Callable[[Any], Any] | None = None, max_turns: int | None = None) -> int:
    """Führt einen Versuch aus. `client_factory` und `max_turns` sind Einhängepunkte für Tests und Kostenbegrenzung.

    `environ` ist nur mit `client_factory` zulässig: ein echter SDK-Client erbt immer `os.environ` (siehe `_amain`)."""
    env = os.environ if environ is None else environ
    log = Log(stderr)
    try:
        return asyncio.run(_amain(stdin, stdout, log, env, client_factory, max_turns))
    except Exception as e:  # noqa: BLE001
        log(f"unerwarteter fehler: {type(e).__name__}")
        return 1
