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
import jsonschema
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

from . import contracts
from . import diagnostics as dx
from .diagnostics import RuntimeDiagnostic, fit, redact
from .environment import ScrubResult, prepare_environment
from .policy import WRITING_PROFILES, Policy, build_policy
from .skill_snapshot import PLUGIN_NAME, VerifiedSnapshot, verify_for_spec

EXPECTED_SDK_VERSION = "0.2.180"
"""Gepinnte SDK-Version (pyproject.toml, uv.lock); Abweichung ist eine Inkompatibilität."""

VERIFIED_CLI_VERSION = "2.1.311"
"""Mit dieser CLI-Version wurden die echten Läufe belegt (docs/architecture/evidence/)."""

MINIMUM_CLI_VERSION = (2, 1, 311)
"""Untergrenze: ältere CLI-Versionen sind nicht nachgewiesen (`sdk_cli_incompatible`). Neuere Versionen sind ungetestet:
schreibende Profile (`policy.WRITING_PROFILES`) werden gesperrt, `readonly` läuft mit Warnung `cli_version_untested`."""

REQUIRED_CAPABILITIES = ("interrupt_receipt_v1",)
"""Fähigkeiten, die der Init-Frame der CLI melden muss (Interrupt mit Quittung)."""

_VERSION_RE = re.compile(r"^(\d+)\.(\d+)\.(\d+)")
_CMD_TIMEOUT = 15
INTERRUPT_TIMEOUT = 10
"""Sekunden, die ein Interrupt-Aufruf an die CLI höchstens dauern darf."""
CLOSE_TIMEOUT = 10
"""Sekunden für das Schließen der Sitzung (Disconnect beendet den CLI-Prozess); eigene Grenze, nicht Teil von `max_runtime_seconds`."""


class EventSink(Protocol):
    terminal_sent: bool

    def send(self, type_: str, payload: dict[str, Any]) -> None: ...


@dataclass(frozen=True)
class Preflight:
    claude_binary: str
    claude_version: str
    """Rohes Versions-Token von `claude -v` (gekürzt), nicht die normalisierte Zahlenfolge."""
    auth_source: str
    scrubbed: list[str]
    untested_version: bool
    skill_snapshot: VerifiedSnapshot | None = None
    """Geprüfter Skill-Snapshot des Auftrags (`None` ohne `skill_snapshot`, dann sind keine Skills aufrufbar)."""


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


def preflight(spec: dict[str, Any], environ: MutableMapping[str, str], scrub: ScrubResult | None = None) -> tuple[Preflight, Policy]:
    """Alle Prüfungen vor dem Start. Löst `RuntimeDiagnostic` aus; es wird keine Sitzung und kein Modellaufruf gestartet.

    `scrub` ist das Ergebnis einer bereits ausgeführten Bereinigung von `environ` (der Aufrufer meldet entfernte
    Credential-Variablen schon vor der Vorprüfung); ohne Angabe bereinigt `preflight` selbst.
    """
    if scrub is None:
        scrub = prepare_environment(environ)
    pol = build_policy(spec["policy_profile"], spec["cwd"])
    check_output_schema(spec.get("output_schema"))
    # Skills nur aus einem geprüften Snapshot: Hashes, Verzeichnisbestand, Kennung, Rolle und Skill-IDs (ohne Modellaufruf).
    snapshot = verify_for_spec(spec)
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
    # Fail-closed streng: maßgeblich ist das rohe Token, nicht die geparste Zahlenfolge (`2.1.311-rc.1`, `2.1.311.7`).
    version_text = fit(v.stdout.split()[0], 40)
    if version_text != VERIFIED_CLI_VERSION and pol.profile in WRITING_PROFILES:
        # Fail-closed: die Dateisystemgrenze ist nur für die nachgewiesene Version belegt.
        raise RuntimeDiagnostic(
            dx.CLI_VERSION_UNTESTED,
            f"CLI {version_text} ist nicht die nachgewiesene Version {VERIFIED_CLI_VERSION}; das schreibende Profil "
            f"{pol.profile} ist nur mit der nachgewiesenen Version freigegeben. Das Profil readonly läuft mit Warnung.",
        )
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
    pre = Preflight(binary, version_text, "claude_ai", scrub.removed, version_text != VERIFIED_CLI_VERSION, snapshot)
    return pre, pol


STRUCTURED_OUTPUT_TOOL = "StructuredOutput"
"""Name des Werkzeugs, das die CLI bei gesetztem `output_format` ergänzt (SDK-Dokumentation, „Structured outputs“): der
Mechanismus der strukturierten Ausgabe, kein fachlicher Schritt. Der Runner meldet es nicht als `tool_event`."""


def check_output_schema(schema: Any) -> None:
    """Das Ausgabeschema muss ein gültiges Draft-07-Schema sein (nach Draft-07 validiert das SDK). Sonst endet der Versuch vor
    jedem Prozess und jedem Modellaufruf mit `output_schema_invalid`. `None`: keine strukturierte Ausgabe."""
    if schema is None:
        return
    try:
        jsonschema.Draft7Validator.check_schema(schema)
    except jsonschema.SchemaError as e:
        raise RuntimeDiagnostic(dx.OUTPUT_SCHEMA_INVALID, f"Das Ausgabeschema ist kein gültiges Draft-07-Schema: {fit(e.message, 300)}") from None


def _config_dir() -> str:
    return os.environ.get("CLAUDE_CONFIG_DIR") or os.path.join(os.path.expanduser("~"), ".claude")


def init_summary(data: dict[str, Any], config_dir: str | None = None) -> str:
    """Anzahlen der Quellen im Init-Frame der CLI, für die Log-Zeile `init:` (stderr, kein Protokollfeld).

    `plugins_in_config_dir` zählt die Plugins, deren Pfad unter dem Konfigurationsverzeichnis des Benutzers liegt
    (installiert oder synchronisiert), `?` wenn ein Eintrag keinen Pfad nennt. Namen nennt die Zeile nur für Skills,
    gekürzt (je Name 40 Zeichen, zusammen höchstens 400, danach `…(+N)`). Namen von MCP-Servern, Connectors, Plugins
    und Agents erscheinen nie."""
    def count(key: str) -> str:
        value = data.get(key)
        return str(len(value)) if isinstance(value, (list, dict)) else "?"

    def plugins_in_config_dir() -> str:
        plugins = data.get("plugins")
        if not isinstance(plugins, list):
            return "?"
        root = os.path.realpath(config_dir or _config_dir())
        paths = [p.get("path") if isinstance(p, dict) else None for p in plugins]
        if not all(isinstance(p, str) and p for p in paths):
            return "?"
        return str(sum(1 for p in paths if os.path.realpath(p) == root or os.path.realpath(p).startswith(root + os.sep)))

    skills = data.get("skills")
    names: list[str] = [fit(str(s), 40) for s in skills] if isinstance(skills, list) else []
    shown: list[str] = []
    used = 0
    for name in names:
        if used + len(name) + 1 > 400:
            break
        shown.append(name)
        used += len(name) + 1
    joined = ",".join(shown) + (f",…(+{len(names) - len(shown)})" if len(shown) < len(names) else "")
    counts = " ".join(f"{k}={count(k)}" for k in ("mcp_servers", "plugins", "skills", "agents", "slash_commands"))
    return f"{counts} plugins_in_config_dir={plugins_in_config_dir()} skill_names={joined}"


def _iso(epoch: float | int | None) -> str:
    if not epoch:
        return "unbekannt"
    return datetime.fromtimestamp(epoch, tz=timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def _safe_id(value: str) -> str:
    cleaned = re.sub(r"[^A-Za-z0-9._:-]", "_", value)[:128] or "t0"
    return cleaned if cleaned[0].isalnum() else "t" + cleaned


def check_skills_loaded(data: dict[str, Any], snapshot: VerifiedSnapshot | None) -> list[str]:
    """Prüft im Init-Frame, dass genau die Skills des Snapshots aus dem Plugin mit dessen Pfad geladen sind.

    Erwartet wird: das Plugin `ccmas` mit genau dem aufgelösten Pfad des Snapshots ist geladen (kein weiteres Plugin
    dieses Namens), jeder `ccmas:<id>` des Snapshots steht in `skills`, und es gibt keinen weiteren `ccmas:`-Eintrag. Ohne
    Snapshot (oder ohne Skills darin) darf weder ein Plugin `ccmas` noch ein `ccmas:`-Skill geladen sein. Mitgelieferte Skills
    der CLI ohne Präfix gehören nicht dazu (das Skill-Werkzeug weist sie über die Allowlist ab, siehe
    runtime-capabilities.md). Fehlt `plugins` oder `skills` im Frame, gilt die Prüfung als nicht bestanden (fail-closed).

    Liefert die geladenen Skill-IDs ohne Präfix (aufsteigend); bei einer Abweichung `_Abort(skills_not_loaded)`."""
    expected = set(snapshot.skill_ids) if snapshot else set()
    plugins, skills = data.get("plugins"), data.get("skills")
    if not isinstance(plugins, list) or not isinstance(skills, list):
        raise _Abort(dx.SKILLS_NOT_LOADED, "Der Init-Frame der CLI enthält keine Angaben zu Plugins und Skills; die Skills "
                     "der Sitzung lassen sich nicht prüfen.")
    problems: list[str] = []
    named = [p for p in plugins if isinstance(p, dict) and p.get("name") == PLUGIN_NAME]
    if snapshot is not None and expected:
        at_path = [p for p in named if isinstance(p.get("path"), str) and os.path.realpath(p["path"]) == snapshot.path]
        if not at_path:
            errors = [str(e.get("type")) for e in data.get("plugin_errors") or [] if isinstance(e, dict)][:3]
            hint = f" (plugin_errors: {', '.join(errors)})" if errors else ""
            problems.append(f"Das Plugin {PLUGIN_NAME} mit dem Pfad des Snapshots ist nicht geladen{hint}")
        if len(named) > len(at_path):
            problems.append(f"Ein weiteres Plugin namens {PLUGIN_NAME} mit anderem Pfad ist geladen")
    elif named:
        problems.append(f"Ein Plugin namens {PLUGIN_NAME} ist geladen, obwohl der Auftrag keine Skills hat")
    prefix = PLUGIN_NAME + ":"
    loaded = {s[len(prefix):] for s in skills if isinstance(s, str) and s.startswith(prefix)}
    if expected - loaded:
        problems.append("Nicht geladen: " + ", ".join(sorted(expected - loaded)))
    if loaded - expected:
        problems.append("Nicht freigegeben, aber geladen: " + ", ".join(sorted(loaded - expected)))
    if problems:
        raise _Abort(dx.SKILLS_NOT_LOADED, "Die Skills der Sitzung entsprechen nicht dem Snapshot: " + "; ".join(problems) + ". Lauf abgebrochen.")
    return sorted(loaded)


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
        self._structured_calls: dict[str, bool] = {}  # tool_use_id des Werkzeugs StructuredOutput -> abgelehnt?
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
            await asyncio.wait_for(self._client.interrupt(), INTERRUPT_TIMEOUT)
        except Exception as e:  # noqa: BLE001 - Fehler hier darf den Lauf nicht verdecken
            self.log(f"interrupt fehlgeschlagen: {type(e).__name__}")

    # --- Lauf --------------------------------------------------------------------------------------------

    def _options(self) -> ClaudeAgentOptions:
        snapshot = self.pre.skill_snapshot
        tools = list(self.pol.tools)
        plugins: list[Any] = []
        skills: list[str] = []  # ausdrücklich leer: ohne Snapshot ist kein Skill aufrufbar (nicht `None` = CLI-Standard)
        if snapshot is not None and snapshot.skill_ids:
            # Das Plugin im Snapshot und eine Allowlist genau dieser Skills; das Skill-Werkzeug muss in `tools` stehen. Die
            # Rechte des Profils (allowed_tools, Sandbox, Settings) bleiben unverändert, insbesondere gibt es keinen
            # Lesezugriff auf das Snapshot-Verzeichnis; das SDK ergänzt nur die Regeln `Skill(<name>)` für die Allowlist.
            tools.append("Skill")
            plugins = [{"type": "local", "path": snapshot.path}]
            skills = snapshot.qualified_skills
        return ClaudeAgentOptions(
            cli_path=self.pre.claude_binary,
            cwd=self.pol.cwd,
            model=self.spec["model"],
            effort=self.spec["effort"],
            permission_mode=self.pol.permission_mode,
            tools=tools,
            plugins=plugins,
            skills=skills,
            allowed_tools=list(self.pol.allowed_tools),
            disallowed_tools=list(self.pol.disallowed_tools),
            sandbox=self.pol.sandbox,  # type: ignore[arg-type]
            settings=json.dumps(self.pol.settings) if self.pol.settings else None,
            setting_sources=[],
            max_turns=self._max_turns,
            resume=self.spec.get("resume_session_id"),
            # Ohne Angabe `None`: das SDK startet die CLI mit `--system-prompt ""` (kein Standardprompt von Claude Code).
            system_prompt=self.spec.get("system_prompt"),
            output_format=self._output_format(),
            stderr=lambda line: self.log("cli: " + line.strip()[:300]),
        )

    def _output_format(self) -> dict[str, Any] | None:
        schema = self.spec.get("output_schema")
        return None if schema is None else {"type": "json_schema", "schema": schema}

    async def run(self) -> None:
        try:
            if self.pre.untested_version:
                self._warn(dx.CLI_VERSION_UNTESTED, f"CLI {self.pre.claude_version} ist nicht die nachgewiesene Version {VERIFIED_CLI_VERSION}.")
            await self._run_session()
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

    async def _run_session(self) -> None:
        """Verbinden, Anfrage senden und Antworten lesen; alles innerhalb von `max_runtime_seconds`. Das Schließen
        (beendet den CLI-Prozess) hat eine eigene Grenze und läuft in jedem Fall, sobald die Sitzung verbunden war.

        Wird `__aenter__` selbst abgebrochen, räumt der SDK-Client den gestarteten Prozess selbst auf."""
        limit = self.spec["max_runtime_seconds"]
        client = self._factory(self._options())
        entered = False
        try:
            try:
                async with asyncio.timeout(limit) as tm:
                    await client.__aenter__()
                    entered = True
                    self._client = client
                    await client.query(self.spec["prompt"])
                    # Ein Interrupt/Stop, der früher eintraf, wartet hier und wird jetzt angewendet (nicht wirkungslos).
                    self._ready.set()
                    async for msg in client.receive_response():
                        try:
                            self._handle(msg)
                        except _Abort:
                            await self._send_interrupt_now(client)
                            raise
            except TimeoutError:
                if not tm.expired():
                    raise
                if self._ready.is_set():  # nur eine bereite Sitzung kennt einen Interrupt
                    await self._send_interrupt_now(client)
                raise RuntimeDiagnostic(dx.MAX_RUNTIME, f"Laufzeitgrenze von {limit} s überschritten; Lauf unterbrochen.") from None
        finally:
            if entered:
                await self._close(client)

    async def _close(self, client: Any) -> None:
        """Disconnect mit eigener Grenze. Fehler hier ändern das Ergebnis nicht (es gibt höchstens ein terminales)."""
        try:
            await asyncio.wait_for(client.__aexit__(None, None, None), CLOSE_TIMEOUT)
        except TimeoutError:
            self.log(f"schließen der sitzung nach {CLOSE_TIMEOUT} s abgebrochen")
        except Exception as e:  # noqa: BLE001
            self.log(f"schließen der sitzung fehlgeschlagen: {type(e).__name__}")

    async def _send_interrupt_now(self, client: Any) -> None:
        if not self._interrupt_sent:
            self._interrupt_sent = True
            try:
                await asyncio.wait_for(client.interrupt(), INTERRUPT_TIMEOUT)
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
        # Welche Quellen die CLI geladen hat (Beleg, dass Projekt-/Benutzerquellen und Connectors aus sind).
        self.log("init: " + init_summary(data))
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
        # Genau die Skills des Snapshots, sonst Abbruch, bevor ein Werkzeug laufen kann.
        loaded_skills = check_skills_loaded(data, self.pre.skill_snapshot)
        payload: dict[str, Any] = {
            "session_id": str(data["session_id"]),
            "model": str(data["model"]),
            # Die CLI meldet das wirksame Effort nicht (Init-Frame ohne Effort-Feld): hier steht der übergebene Wert.
            "effort": self.spec["effort"],
            "claude_version": version,
            "sdk_version": claude_agent_sdk.__version__,
            "claude_binary": self.pre.claude_binary,
            "auth_source": self.pre.auth_source,
        }
        if data.get("permissionMode"):
            payload["permission_mode"] = str(data["permissionMode"])
        if self.pre.skill_snapshot is not None:
            payload["skill_snapshot_id"] = self.pre.skill_snapshot.snapshot_id
            payload["skills"] = loaded_skills
        self.sink.send("session_started", payload)

    def _on_assistant(self, msg: AssistantMessage) -> None:
        if msg.error:
            self._assistant_error = msg.error
        for block in msg.content:
            if isinstance(block, TextBlock) and block.text.strip():
                self.sink.send("activity", {"kind": "assistant_text", "summary": fit(block.text, 500)})
            elif isinstance(block, ThinkingBlock):
                self.sink.send("activity", {"kind": "thinking", "summary": "Denkt nach"})
            elif isinstance(block, ToolUseBlock) and block.name == STRUCTURED_OUTPUT_TOOL:
                self._structured_calls[block.id] = False  # gezählt und im Log ausgewiesen, nicht als Schritt gemeldet
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
            if isinstance(block, ToolResultBlock) and block.tool_use_id in self._structured_calls:
                self._structured_calls[block.tool_use_id] = bool(block.is_error)  # Inhalt der Ablehnung bleibt im Log
            elif isinstance(block, ToolResultBlock):
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
        if name == "Skill":  # nur der Skill-Name (`ccmas:<id>`), nie ein Inhalt
            return fit(str(tool_input.get("skill", "")), 200)
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

    def _log_structured_calls(self) -> None:
        if self._structured_calls:
            rejected = sum(1 for r in self._structured_calls.values() if r)
            self.log(f"structured_output: aufrufe={len(self._structured_calls)} abgelehnt={rejected}")

    def _structured_result(self, msg: ResultMessage) -> None:
        """Erfolg bei gesetztem Ausgabeschema: nur mit einer gültigen strukturierten Ausgabe (Objekt, Schema, Größe)."""
        self._log_structured_calls()
        out = msg.structured_output
        if out is None:
            self._fail(dx.STRUCTURED_OUTPUT_MISSING, "Die Sitzung endete erfolgreich, aber ohne strukturierte Ausgabe, obwohl ein "
                       "Ausgabeschema gesetzt war.")
            return
        if not isinstance(out, dict):
            self._fail(dx.STRUCTURED_OUTPUT_INVALID, f"Die strukturierte Ausgabe ist kein Objekt (Typ {type(out).__name__}).")
            return
        try:
            jsonschema.Draft7Validator(self.spec["output_schema"]).validate(out)
        except jsonschema.ValidationError as e:  # nur Ort und Schlüsselwort, nie Werte der Ausgabe
            where = "/" + "/".join(str(p) for p in e.absolute_path)
            self._fail(dx.STRUCTURED_OUTPUT_INVALID, f"Die strukturierte Ausgabe verletzt das Ausgabeschema bei {where} (Schlüsselwort {e.validator}).")
            return
        limit = contracts.byte_limit("result", "structured_output")
        size = contracts.canonical_size(out)
        if size > limit:
            self._fail(dx.STRUCTURED_OUTPUT_TOO_LARGE, f"Die strukturierte Ausgabe hat {size} Bytes, erlaubt sind höchstens {limit}.")
            return
        try:
            self.sink.send("result", {"outcome": "succeeded", "summary": fit(msg.result or "Lauf beendet.", 8000), "structured_output": out})
        except contracts.ContractError as e:  # zum Beispiel mehr als 64 Schlüssel auf oberster Ebene
            code = dx.STRUCTURED_OUTPUT_TOO_LARGE if "size" in e.categories else dx.STRUCTURED_OUTPUT_INVALID
            self._fail(code, f"Die strukturierte Ausgabe verletzt den Vertrag des Ergebnisses (Klasse {','.join(e.categories)}).")

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
            if self.spec.get("output_schema") is not None:
                self._structured_result(msg)
            else:
                self._result("succeeded", msg.result or "Lauf beendet.")
        elif msg.subtype == "error_max_structured_output_retries":
            self._log_structured_calls()
            detail = fit("; ".join(msg.errors or []), 400) if msg.errors else "ohne Angabe"
            self._fail(dx.STRUCTURED_OUTPUT_FAILED, "Das SDK konnte nach mehreren Versuchen keine gültige strukturierte Ausgabe "
                       f"erzeugen (Untertyp error_max_structured_output_retries; {detail}).")
        elif msg.subtype == "error_max_turns":
            self._result("controlled_failure", "Die maximale Rundenzahl wurde erreicht.")
        else:
            self._fail(dx.SESSION_FAILED, f"Die Sitzung endete mit einem Fehler (subtype {fit(msg.subtype, 60)}, Grund {fit(str(msg.terminal_reason), 60)}).")


class _Abort(RuntimeDiagnostic):
    """Diagnose während des Laufs: unterbricht die Sitzung vor dem Verlassen."""
