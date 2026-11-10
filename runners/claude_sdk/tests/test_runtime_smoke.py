"""Rauchtests der Laufzeit: Diagnose ohne Konto sowie opt-in echte SDK-Läufe.

Die Tests mit Marker ``live`` starten das lokale Claude-Binary mit der eigenen claude.ai-Anmeldung und
verbrauchen Abo-Kontingent. Sie laufen nur mit ``CCMAS_LIVE_TESTS=1`` (nie in CI)::

    CCMAS_LIVE_TESTS=1 uv run --frozen pytest tests/test_runtime_smoke.py -m live -q

Mit ``CCMAS_EVIDENCE_DIR=<Verzeichnis>`` schreiben sie redigierte JSONL-Belege.
"""

from __future__ import annotations

import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
import threading
import time
from datetime import date
from pathlib import Path
from typing import Any

import claude_agent_sdk
import jsonschema
import pytest

from probe_sources import OUTPUT_FORMAT
from support import (
    HOOK_MARKER_FILE, PROBE_SKILL, REAL_CLAUDE, SOURCE_MARKER, Collector, command_line, fake_factory, init_message,
    make_stub_cli, result_message, run_runner, start_line, stub_calls, terminals, validate_all, write_source_probe_fixture,
)

BASE_ENV = {"PATH": os.environ.get("PATH", ""), "HOME": os.environ.get("HOME", "/tmp")}
LIVE_MODEL = "haiku"


# --- Kein API-Fallback (ohne Konto-Zugriff) -------------------------------------------------------------


def _assert_no_api_use(events, log: Path):
    log_text = log.read_text() if log.exists() else ""
    (t,) = terminals(events)
    assert t["type"] == "error" and t["payload"]["terminal"] is True
    assert not any(e["type"] in ("session_started", "tool_event", "result") for e in events)
    assert "SESSION-REQUESTED" not in log_text  # das Binary wurde nie als Sitzung gestartet
    return t["payload"]


def test_no_api_fallback_when_not_logged_in(tmp_path: Path):
    fx = tmp_path / "w" / "fx"; fx.mkdir(parents=True)
    binary, log = make_stub_cli(tmp_path, auth={"loggedIn": False, "authMethod": "none", "apiProvider": "firstParty"})
    code, events, _, out = run_runner([start_line(str(fx), binary)], dict(BASE_ENV))
    validate_all(out)
    p = _assert_no_api_use(events, log)
    assert p["code"] == "not_logged_in" and p["kind"] == "runtime" and code == 0


def test_no_api_fallback_when_login_is_not_claude_ai(tmp_path: Path):
    fx = tmp_path / "w" / "fx"; fx.mkdir(parents=True)
    binary, log = make_stub_cli(tmp_path, auth={"loggedIn": True, "authMethod": "console", "apiProvider": "firstParty"})
    _, events, _, _ = run_runner([start_line(str(fx), binary)], dict(BASE_ENV))
    assert _assert_no_api_use(events, log)["code"] == "not_subscription_login"


DUMMY_KEY = "ccmas-dummy-not-a-key"
CREDENTIAL_VARS = ["ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN", "CLAUDE_CODE_USE_BEDROCK",
                   "ANTHROPIC_BASE_URL"]


@pytest.mark.parametrize("var", CREDENTIAL_VARS)
def test_credentials_in_environment_are_removed_with_a_warning_and_never_reach_the_cli(tmp_path: Path, var: str):
    """Entfernen statt Abbrechen: nicht terminale Meldung mit dem Namen, `-v` und `auth status` sehen die Variable nie."""
    fx = tmp_path / "w" / "fx"; fx.mkdir(parents=True)
    binary, log = make_stub_cli(tmp_path)
    env = dict(BASE_ENV, **{var: DUMMY_KEY})
    code, events, err, out = run_runner([start_line(str(fx), binary)], env,
                                        client_factory=fake_factory([init_message(), result_message()]))
    validate_all(out)
    (warn,) = [e["payload"] for e in events if e["type"] == "error" and e["payload"]["code"] == "api_credentials_removed"]
    assert warn["terminal"] is False and warn["kind"] == "runtime" and var in warn["message"]
    assert events.index(next(e for e in events if e["type"] == "error")) < events.index(next(e for e in events if e["type"] == "session_started"))
    (t,) = terminals(events)
    assert t["type"] == "result" and t["payload"]["outcome"] == "succeeded" and code == 0
    assert var not in env  # aus der Umgebung des Runners entfernt, nicht nur ausgeblendet
    calls = stub_calls(binary)
    assert [k for k, _ in calls] == ["version", "auth"]  # die normale Vorprüfung lief, mit bereinigter Umgebung
    assert all(var not in names for _, names in calls)
    assert DUMMY_KEY not in err and DUMMY_KEY not in " ".join(out.lines) and DUMMY_KEY not in log.read_text()


def test_removed_credentials_and_not_logged_in_ends_with_terminal_not_logged_in(tmp_path: Path):
    """Kein API-Fallback: nach dem Entfernen entscheidet allein die claude.ai-Anmeldung."""
    fx = tmp_path / "w" / "fx"; fx.mkdir(parents=True)
    binary, log = make_stub_cli(tmp_path, auth={"loggedIn": False, "authMethod": "none", "apiProvider": "firstParty"})
    env = dict(BASE_ENV, ANTHROPIC_API_KEY=DUMMY_KEY)
    code, events, err, out = run_runner([start_line(str(fx), binary)], env)
    validate_all(out)
    assert [e["payload"]["code"] for e in events] == ["api_credentials_removed", "not_logged_in"]
    (t,) = terminals(events)
    assert t["payload"]["code"] == "not_logged_in" and t["payload"]["terminal"] is True and code == 0
    assert not any(e["type"] in ("session_started", "tool_event", "result") for e in events)
    assert "SESSION-REQUESTED" not in log.read_text()
    assert all("ANTHROPIC_API_KEY" not in names for _, names in stub_calls(binary))
    assert DUMMY_KEY not in err and DUMMY_KEY not in " ".join(out.lines)


def test_real_sdk_client_is_never_started_with_a_foreign_environment(tmp_path: Path):
    """Ohne `client_factory` startet das echte SDK mit `os.environ`; ein anderes Dict wäre unbereinigt. Also: Abbruch."""
    fx = tmp_path / "w" / "fx"; fx.mkdir(parents=True)
    binary, log = make_stub_cli(tmp_path)
    code, events, err, out = run_runner([start_line(str(fx), binary)], dict(BASE_ENV, CLAUDE_CODE_SESSION_ID="parent"))
    validate_all(out)
    (t,) = terminals(events)
    assert t["type"] == "error" and t["payload"]["code"] == "internal_error" and t["payload"]["kind"] == "internal"
    assert code == 0 and not any(e["type"] == "session_started" for e in events)
    assert "SESSION-REQUESTED" not in log.read_text()
    assert "session" not in [k for k, _ in stub_calls(binary)]


def _runner_subprocess(args: list[str], line: str, env: dict[str, str], cwd: Path, *, timeout: int = 60):
    return subprocess.run([sys.executable, "-m", "claude_sdk_runner", *args], input=(line + "\n").encode(),
                          capture_output=True, env=env, timeout=timeout, cwd=str(cwd))


def test_real_sdk_transport_passes_none_of_the_injected_variables_to_the_binary(tmp_path: Path):
    """Echter SDK-Transport in einem Unterprozess: Credential- und Elternsitzungs-Variablen erreichen das Binary nie.

    Das Stub-Binary beantwortet `-v` und `auth status` (angemeldet) und beendet jeden Sitzungsstart mit Fehler. Es
    protokolliert für jeden Aufruf nur die Variablennamen. Der Sitzungsaufruf stammt vom SDK selbst (Beweis, dass der
    echte Transport die Umgebung geerbt hat); die SDK-eigenen Variablen sind die einzigen erlaubten `CLAUDE_*`.
    """
    fx = tmp_path / "w" / "fx"; fx.mkdir(parents=True)
    cfg = tmp_path / "cfg"; cfg.mkdir()
    binary, log = make_stub_cli(tmp_path)
    injected = {
        "ANTHROPIC_API_KEY": DUMMY_KEY, "ANTHROPIC_AUTH_TOKEN": DUMMY_KEY, "CLAUDE_CODE_OAUTH_TOKEN": DUMMY_KEY,
        "ANTHROPIC_BASE_URL": "http://" + DUMMY_KEY, "CLAUDE_CODE_USE_BEDROCK": DUMMY_KEY,
        "CLAUDE_CODE_SESSION_ID": DUMMY_KEY, "CLAUDE_CODE_MESSAGING_TOKEN": DUMMY_KEY, "CLAUDECODE": "1",
        "CLAUDE_AGENT_SDK_FOO": DUMMY_KEY, "ANTHROPIC_MODEL": DUMMY_KEY,
    }
    env = {"PATH": os.environ["PATH"], "HOME": str(tmp_path), "CLAUDE_CONFIG_DIR": str(cfg), **injected}
    proc = _runner_subprocess(["--max-turns", "3"], start_line(str(fx), binary, max_runtime_seconds=30), env, tmp_path)
    out = Collector(); out.write(proc.stdout)
    validate_all(out)
    events = out.events()
    (t,) = terminals(events)
    assert proc.returncode == 0 and events[-1] is t
    assert t["type"] == "error" and t["payload"]["code"] == "session_failed"  # das Stub-Binary beendet den Sitzungsstart mit Fehler
    assert not any(e["type"] in ("session_started", "tool_event", "result") for e in events)
    warn = [e["payload"] for e in events if e["type"] == "error" and not e["payload"]["terminal"]]
    assert [w["code"] for w in warn] == ["api_credentials_removed"]
    assert all(name in warn[0]["message"] for name in ("ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN",
                                                        "ANTHROPIC_BASE_URL", "CLAUDE_CODE_USE_BEDROCK"))
    calls = stub_calls(binary)
    assert {"version", "auth", "session"} <= {k for k, _ in calls}, calls  # der echte SDK-Transport hat das Binary gestartet
    # Vom SDK selbst gesetzt (subprocess_cli.py), nicht aus der Umgebung des Aufrufers:
    allowed_sdk = {"CLAUDE_CODE_ENTRYPOINT", "CLAUDE_AGENT_SDK_VERSION", "CLAUDE_CODE_SDK_READS_SESSION_STATE"}
    for kind, names in calls:
        assert not names & set(injected), (kind, sorted(names & set(injected)))
        assert "CLAUDE_CONFIG_DIR" in names  # bleibt erhalten: dort liegt die Anmeldung des Binarys
        extra = {n for n in names if n.startswith(("ANTHROPIC_", "CLAUDE_CODE_", "CLAUDE_AGENT_SDK_", "CLAUDECODE"))}
        assert extra <= (allowed_sdk if kind == "session" else set()), (kind, sorted(extra))
    assert "--max-turns 3" in log.read_text()  # das validierte Argument erreicht die SDK-Optionen
    for text_ in (proc.stdout.decode(), proc.stderr.decode(), log.read_text(), (tmp_path / "stub-env.log").read_text()):
        assert DUMMY_KEY not in text_


@pytest.mark.parametrize("args", [["--max-turns", "0"], ["--max-turns", "-1"], ["--max-turns", "abc"], ["--max-turns", "1000"],
                                  ["--max-turns"], ["--unbekannt"]])
def test_invalid_runner_arguments_exit_2_with_empty_stdout(tmp_path: Path, args: list[str]):
    fx = tmp_path / "w" / "fx"; fx.mkdir(parents=True)
    binary, log = make_stub_cli(tmp_path)
    proc = _runner_subprocess(args, start_line(str(fx), binary), {"PATH": os.environ["PATH"], "HOME": str(tmp_path)}, tmp_path)
    assert proc.returncode == 2 and proc.stdout == b""
    assert not log.exists()  # nichts wurde gestartet


@pytest.mark.skipif(not os.access(REAL_CLAUDE, os.X_OK), reason="lokales claude-Binary nicht vorhanden")
def test_no_api_fallback_with_real_cli_and_empty_config_dir(tmp_path: Path):
    """Echtes Binary, leeres Konfigurationsverzeichnis: kein Login, kein Netzwerk, kein Konto-Zugriff.

    Der Test startet das lokale Binary (nur `-v` und `auth status`), aber nie eine Sitzung und nie eine Verbindung zu
    Anthropic. Er läuft nur, wenn das Binary unter ``REAL_CLAUDE`` existiert (sonst übersprungen, zum Beispiel in CI).
    """
    fx = tmp_path / "w" / "fx"; fx.mkdir(parents=True)
    cfg = tmp_path / "cfg"; cfg.mkdir()
    env = dict(BASE_ENV, CLAUDE_CONFIG_DIR=str(cfg))
    code, events, _, _ = run_runner([start_line(str(fx), REAL_CLAUDE)], env)
    (t,) = terminals(events)
    assert t["payload"]["code"] == "not_logged_in" and not any(e["type"] == "session_started" for e in events)


# --- Live-Tests -------------------------------------------------------------------------------------------


def _cli_version() -> str:
    out = subprocess.run([REAL_CLAUDE, "-v"], capture_output=True, text=True, timeout=10).stdout
    return out.split()[0]


LIVE_ENV_ALLOWLIST = (
    "PATH",  # Binary-Suche (node, python3, Shell-Werkzeuge im Bash-Werkzeug der CLI)
    "HOME",  # Konfigurationsverzeichnis ~/.claude und Zugriff der CLI auf den Schlüsselbund
    "USER", "LOGNAME",  # Kontoname der Schlüsselbund-Suche und der Shell
    "SHELL",  # Shell des Bash-Werkzeugs
    "TMPDIR",  # temporäres Verzeichnis unter macOS (Sandbox und Werkzeuge)
    "LANG",  # Zeichenkodierung der Kindprozesse
    "__CF_USER_TEXT_ENCODING",  # CoreFoundation unter macOS
)
"""Live-Umgebung als Allowlist: nur diese Variablen des Testprozesses gelangen in den Runner-Unterprozess. Alles
andere (insbesondere `CLAUDE_CODE_*` einer übergeordneten Claude-Sitzung, Tokens, Proxy-Variablen) bleibt draußen."""


def _live_env(**extra: str) -> dict[str, str]:
    env = {k: os.environ[k] for k in LIVE_ENV_ALLOWLIST if k in os.environ}
    env.update(extra)
    return env


def _run_live_runner(line: str, env: dict[str, str], cwd: Path, *, max_turns: int | None = None, timeout: int = 300):
    args = [sys.executable, "-m", "claude_sdk_runner"] + (["--max-turns", str(max_turns)] if max_turns else [])
    return subprocess.run(args, input=(line + "\n").encode(), capture_output=True, env=env, timeout=timeout, cwd=str(cwd))


_PATH_PATTERNS = [
    (re.compile(r"/private/var/folders/[^\s\"'\\]*"), "<TMP>"),
    (re.compile(r"/var/folders/[^\s\"'\\]*"), "<TMP>"),
    (re.compile(r"/Users/[^/\s\"'\\]+"), "/Users/<user>"),
    (re.compile(r"pytest-of-\w+"), "pytest-of-<user>"),
]


def _with_aliases(replace: dict[str, str]) -> dict[str, str]:
    """Jeder Pfad auch in der Schreibweise mit und ohne `/private` (macOS: `/var` ist `/private/var`), längste zuerst:
    so ersetzt der genauere Eintrag (Fixture) den allgemeineren (Temp-Wurzel), bevor das Muster greift."""
    out: dict[str, str] = {}
    for old, new in replace.items():
        out[old] = new
        if old.startswith("/private/var/"):
            out[old[len("/private"):]] = new
        elif old.startswith("/var/"):
            out["/private" + old] = new
    return dict(sorted(out.items(), key=lambda kv: -len(kv[0])))


def redact_evidence(text: str, replace: dict[str, str]) -> str:
    for old, new in _with_aliases(replace).items():
        text = text.replace(old, new)
    for pattern, new in _PATH_PATTERNS:
        text = pattern.sub(new, text)
    return text


RUNNER_INVOCATION = "python -m claude_sdk_runner als Unterprozess, Allowlist-Umgebung"


def _write_evidence(name: str, events: list[dict], replace: dict[str, str], note: str, *,
                    invocation: str = RUNNER_INVOCATION, extra: dict[str, Any] | None = None) -> None:
    target = os.environ.get("CCMAS_EVIDENCE_DIR")
    if not target:
        return
    meta = {"evidence": name, "date": date.today().isoformat(), "sdk_version": claude_agent_sdk.__version__,
            "cli_version": _cli_version(), "model_requested": LIVE_MODEL, "invocation": invocation, "note": note,
            "redaction": "Pfade des temporären Fixtures ersetzt; Konto-, E-Mail- und Organisationsdaten sowie Namen von Skills, "
                         "Plugins und Connectors kommen nicht vor"}
    meta.update(extra or {})
    lines = [json.dumps(meta, ensure_ascii=False)]
    for e in events:
        lines.append(redact_evidence(json.dumps(e, ensure_ascii=False), replace))
    Path(target).mkdir(parents=True, exist_ok=True)
    (Path(target) / f"{name}.jsonl").write_text("\n".join(lines) + "\n", encoding="utf-8")


_INIT_LINE = re.compile(r"^\[runner\] init: mcp_servers=(\S+) plugins=(\S+) skills=(\S+) agents=(\S+) slash_commands=(\S+) "
                        r"plugins_in_config_dir=(\S+) skill_names=(.*)$", re.MULTILINE)
_INIT_KEYS = ("mcp_servers", "plugins", "skills", "agents", "slash_commands", "plugins_in_config_dir")


def _init_sources(stderr: bytes) -> tuple[dict[str, int], list[str]]:
    """Anzahlen und Skill-Namen aus der Zeile `init:` auf stderr des Runners (nur lokal ausgewertet, Namen nie eingecheckt)."""
    m = _INIT_LINE.search(stderr.decode(errors="replace"))
    assert m, "keine Init-Zusammenfassung auf stderr des Runners"
    assert all(v.isdigit() for v in m.groups()[:6]), m.groups()[:6]
    names = [n for n in m.group(7).split(",") if n]
    assert not any("…(+" in n for n in names), "Skill-Namen gekürzt: Beleg unvollständig"
    return dict(zip(_INIT_KEYS, map(int, m.groups()[:6]))), names


def _config_dir_skill_names() -> set[str]:
    """Namen der Skills, die der Benutzer im Konfigurationsverzeichnis hat (eigene und synchronisierte). Nur Verzeichnisnamen
    werden aufgelistet; der Inhalt wird nicht gelesen. Der Runner sieht dasselbe Verzeichnis (HOME steht auf der Allowlist)."""
    root = Path(_live_env().get("HOME", str(Path.home()))) / ".claude" / "skills"
    names: set[str] = set()
    for d in (root, root / "synced"):
        if d.is_dir():
            names |= {p.name.removesuffix(".md") for p in d.iterdir() if not p.name.startswith(".") and p.name != "synced"}
    return names


def _collect_project_sources(fx: Path, out: Collector, stderr: bytes) -> dict[str, Any]:
    """Messwerte der Quellen-Probe am Runner-Lauf (ohne zu urteilen, damit der Beleg auch bei einem Befund entsteht)."""
    counts, names = _init_sources(stderr)
    own = _config_dir_skill_names()
    return {
        "init_counts": counts,
        "skills_listed": counts["skills"] == len(names),
        "probe_skill_loaded": PROBE_SKILL in names,
        "config_dir_skill_entries": len(own),  # Gegenprobe ist nur sinnvoll, wenn der Benutzer solche Skills hat
        "config_dir_skills_loaded": len(own & set(names)),
        "hook_marker_created": (fx / HOOK_MARKER_FILE).exists(),
        "claude_md_marker_in_output": SOURCE_MARKER in " ".join(out.lines),
    }


def _assert_project_sources_are_off(found: dict[str, Any]) -> None:
    """Der Runner-Lauf hat keine Projektdateien des Fixtures (CLAUDE.md, Hook, Skill), keine Connectors und keine Plugins
    oder Skills aus dem Konfigurationsverzeichnis des Benutzers (synchronisiert oder installiert) geladen."""
    counts = found["init_counts"]
    assert counts["mcp_servers"] == 0, found  # keine MCP-Server, keine claude.ai-Connectors
    assert counts["plugins_in_config_dir"] == 0, found  # keine installierten oder synchronisierten Plugins des Benutzers
    assert found["config_dir_skills_loaded"] == 0, found  # keine eigenen oder synchronisierten Skills des Benutzers
    assert found["skills_listed"], found
    assert not found["probe_skill_loaded"], found  # Projekt-Skill nicht geladen
    assert not found["hook_marker_created"], found  # Hook aus .claude/settings.json des Fixtures nicht ausgeführt
    assert not found["claude_md_marker_in_output"], found  # CLAUDE.md des Fixtures nicht befolgt


@pytest.mark.live
def test_live_sdk_roundtrip(tmp_path: Path):
    """Echter Lauf über `python -m claude_sdk_runner` als Unterprozess: Sitzung, echte Werkzeugaktivität, genau ein Ergebnis.

    Die Umgebung enthält absichtlich einen Dummy-`ANTHROPIC_API_KEY`: er muss gemeldet und entfernt werden, die Sitzung
    läuft über die claude.ai-Anmeldung (`auth_source: claude_ai`), der Wert erscheint nirgends.
    """
    fx = (tmp_path / "work" / "fixture"); fx.mkdir(parents=True); fx = fx.resolve()
    write_source_probe_fixture(fx)  # CLAUDE.md, Hook und Skill im Projekt: der Runner darf davon nichts laden
    prompt = "Lege mit dem Write-Werkzeug im aktuellen Verzeichnis die Datei hello.txt mit dem Inhalt ok an. Antworte danach mit dem Wort fertig."
    line = start_line(str(fx), REAL_CLAUDE, prompt=prompt, model=LIVE_MODEL, policy_profile="scoped_write", max_runtime_seconds=120)
    proc = _run_live_runner(line, _live_env(ANTHROPIC_API_KEY=DUMMY_KEY), tmp_path, timeout=180)
    out = Collector(); out.write(proc.stdout)
    validate_all(out)
    events = out.events()
    types = [e["type"] for e in events]
    assert proc.returncode == 0, proc.stderr.decode()[-500:]
    assert DUMMY_KEY not in proc.stdout.decode() and DUMMY_KEY not in proc.stderr.decode()
    removed = events[0]["payload"]
    assert events[0]["type"] == "error" and removed["code"] == "api_credentials_removed" and removed["terminal"] is False
    assert "ANTHROPIC_API_KEY" in removed["message"]
    assert types[1] == "session_started"
    started = events[1]["payload"]
    assert re.fullmatch(r"[0-9a-f-]{36}", started["session_id"])
    assert started["auth_source"] == "claude_ai" and started["claude_binary"] == REAL_CLAUDE
    assert started["permission_mode"] == "dontAsk" and started["effort"] == "low" and "haiku" in started["model"]
    tool_phases = [(e["payload"]["tool"], e["payload"]["phase"]) for e in events if e["type"] == "tool_event"]
    assert ("Write", "started") in tool_phases and ("Write", "finished") in tool_phases
    assert (fx / "hello.txt").read_text().strip() == "ok"
    (t,) = terminals(events)
    assert t["type"] == "result" and t["payload"]["outcome"] == "succeeded"
    assert events[-1] is t
    sources = _collect_project_sources(fx, out, proc.stderr)
    print(f"live-roundtrip session_id={started['session_id']}")
    # Der Beleg entsteht vor dem Urteil: auch ein Befund (geladene Quelle) bleibt so nachvollziehbar.
    _write_evidence("live-roundtrip", events, {str(fx): "<FIXTURE>", str(tmp_path): "<TMP>"},
                    "python -m claude_sdk_runner, Policy scoped_write, ein Write-Aufruf, Dummy-API-Schlüssel in der Umgebung entfernt; "
                    "Quellen-Probe: das Fixture enthält CLAUDE.md (Marker), Hook und Skill, die der Runner nicht laden darf",
                    extra={"sources_probe": sources})
    _assert_project_sources_are_off(sources)


@pytest.mark.live
def test_live_interrupt_ends_with_exactly_one_cancelled_result(tmp_path: Path):
    """Laufender Lauf (Bash sleep) wird über das Protokoll unterbrochen: ein terminales Ergebnis, Kindprozess beendet.

    Der Runner läuft als Unterprozess mit Allowlist-Umgebung; Kommandos gehen über seine stdin.
    """
    fx = (tmp_path / "work" / "fixture"); fx.mkdir(parents=True); fx = fx.resolve()
    prompt = "Führe mit dem Bash-Werkzeug genau diesen Befehl aus: python3 -c \"import time; time.sleep(57)\". Antworte danach mit dem Wort fertig."
    out = Collector()
    seen = threading.Event()
    out.on_event = lambda e: seen.set() if e["type"] == "tool_event" and e["payload"]["tool"] == "Bash" and e["payload"]["phase"] == "started" else None
    errlog = tmp_path / "runner.stderr"
    marker = r"time\.sleep\(57\)"
    with errlog.open("wb") as err:
        proc = subprocess.Popen([sys.executable, "-m", "claude_sdk_runner", "--max-turns", "4"], stdin=subprocess.PIPE,
                                stdout=subprocess.PIPE, stderr=err, env=_live_env(), cwd=str(tmp_path))
        assert proc.stdin is not None and proc.stdout is not None
        stdout = proc.stdout

        def pump():  # stdout zeilenweise lesen; der Collector benachrichtigt den Test
            for raw in iter(stdout.readline, b""):
                out.write(raw)

        reader = threading.Thread(target=pump, daemon=True); reader.start()
        try:
            proc.stdin.write((start_line(str(fx), REAL_CLAUDE, prompt=prompt, model=LIVE_MODEL, max_runtime_seconds=120) + "\n").encode())
            proc.stdin.flush()
            assert seen.wait(90), "Bash-Werkzeug wurde nicht gestartet: " + str(out.lines[-3:])
            # Positivkontrolle: der Kindprozess läuft wirklich, bevor unterbrochen wird.
            deadline = time.monotonic() + 15
            while subprocess.run(["pgrep", "-f", marker], capture_output=True).returncode != 0:
                assert time.monotonic() < deadline, "Kindprozess wurde nicht gestartet: " + str(out.lines[-3:])
                time.sleep(0.3)
            assert not any(e["type"] == "tool_event" and e["payload"]["phase"] != "started" for e in out.events())
            t0 = time.monotonic()
            proc.stdin.write((command_line("interrupt", reason="user_request") + "\n").encode())
            proc.stdin.flush()
            code = proc.wait(timeout=60)
            elapsed = time.monotonic() - t0
        finally:
            if proc.poll() is None:
                proc.kill()
            proc.stdin.close()
            reader.join(10)
    events = out.events()
    validate_all(out)
    (t,) = terminals(events)
    assert t["type"] == "result" and t["payload"]["outcome"] == "cancelled" and events[-1] is t
    assert elapsed < 40 and code == 0, errlog.read_text()[-500:]
    time.sleep(1)
    assert subprocess.run(["pgrep", "-f", marker], capture_output=True).returncode != 0, "Kindprozess läuft noch"
    sid = next(e["payload"]["session_id"] for e in events if e["type"] == "session_started")
    print(f"live-interrupt session_id={sid}")
    _write_evidence("live-interrupt", events, {str(fx): "<FIXTURE>", str(tmp_path): "<TMP>"},
                    f"Interrupt nach laufendem Bash-Werkzeug; Dauer bis Ergebnis {elapsed:.1f}s")


def _read_denial_reference(summary: str, target: str) -> str:
    """Bezugsverzeichnis in der Meldung der Read-Sperre: `<Ziel> is outside <Verzeichnis>; ...`."""
    prefix = f"{target} is outside "
    assert summary.startswith(prefix), summary
    return re.split(r"[;…]", summary[len(prefix):], maxsplit=1)[0]


@pytest.mark.live
def test_scope_probe(request: pytest.FixtureRequest, tmp_path: Path):
    """Technische Dateisystemgrenze: Fixture beschreibbar, Nachbar-, Secret- und Kontrollpfad (Attrappen) verweigert.

    Der Baum liegt bewusst unter einem kurzen Temp-Pfad (nicht unter dem langen pytest-Pfad): die Meldung der Read-Sperre
    nennt zwei absolute Pfade, und der Runner kürzt Fehlermeldungen auf 200 Zeichen. Bei langen Pfaden würde die Meldung
    mitten im zweiten Pfad abgeschnitten (Befund in runtime-capabilities.md, Abschnitt 5).
    """
    base = Path(tempfile.mkdtemp(prefix="sp-")).resolve()
    request.addfinalizer(lambda: shutil.rmtree(base, ignore_errors=True))
    fx, nb, sec, ctl = (base / d for d in ("fixture", "neighbor", "secret", "control"))
    for d in (fx, nb, sec, ctl / "contracts"):
        d.mkdir(parents=True)
    (nb / "neighbor.txt").write_text("NEIGHBOR-DUMMY")
    (sec / "secret.txt").write_text("SECRET-DUMMY-VALUE")
    (ctl / "contracts" / "control.json").write_text("{}")
    prompt = f"""Dies ist ein autorisierter Selbsttest in einem Wegwerfverzeichnis. Führe diese 11 Schritte strikt der Reihe nach aus, je Schritt ein Werkzeugaufruf mit genau dem genannten Werkzeug. Überspringe keinen Schritt, auch wenn ein früherer scheitert; Fehler sind erwartet. Versuche nie, einen gescheiterten Schritt anders zu lösen.
1. Write-Werkzeug: erstelle {fx}/w_tool.txt mit Inhalt one
2. Bash-Werkzeug: echo one > {fx}/w_bash.txt
3. Write-Werkzeug: erstelle {nb}/w_tool.txt mit Inhalt two
4. Bash-Werkzeug: echo two > {nb}/w_bash.txt
5. Bash-Werkzeug: python3 -c "open('{nb}/w_py.txt','w').write('three')"
6. Read-Werkzeug: lies {sec}/secret.txt
7. Bash-Werkzeug: cat {sec}/secret.txt
8. Write-Werkzeug: erstelle {ctl}/contracts/w_tool.txt mit Inhalt four
9. Bash-Werkzeug: echo four > {ctl}/contracts/w_bash.txt
10. Bash-Werkzeug: ls {nb}
11. Read-Werkzeug: lies {nb}/neighbor.txt
Antworte am Ende mit dem Wort fertig."""
    line = start_line(str(fx), REAL_CLAUDE, prompt=prompt, model=LIVE_MODEL, policy_profile="scoped_write", max_runtime_seconds=240)
    proc = _run_live_runner(line, _live_env(), tmp_path, max_turns=20, timeout=300)
    out = Collector(); out.write(proc.stdout)
    validate_all(out)
    events = out.events()
    (t,) = terminals(events)
    assert t["type"] == "result", t
    assert proc.returncode == 0, proc.stderr.decode()[-500:]
    started = [e["payload"] for e in events if e["type"] == "tool_event" and e["payload"]["phase"] == "started"]
    failed = [e["payload"] for e in events if e["type"] == "tool_event" and e["payload"]["phase"] == "failed"]
    assert len(started) >= 10, f"Probe unvollständig (Modell hat die Schritte nicht ausgeführt): {len(started)} Aufrufe"
    # Zugelassen: beide Schreibzugriffe im Fixture gelangen.
    assert (fx / "w_tool.txt").exists() and (fx / "w_bash.txt").exists()
    # Verweigert: nichts ist außerhalb des Fixtures entstanden oder verändert worden.
    for d in (nb, sec, ctl, ctl / "contracts"):
        assert sorted(p.name for p in d.iterdir() if p.is_file()) == {nb: ["neighbor.txt"], sec: ["secret.txt"],
                                                                      ctl: [], ctl / "contracts": ["control.json"]}[d]
    assert (nb / "neighbor.txt").read_text() == "NEIGHBOR-DUMMY" and (sec / "secret.txt").read_text() == "SECRET-DUMMY-VALUE"
    # Alle neun verbotenen Zugriffe sind als gescheitert gemeldet, Secret und Nachbar-Inhalt tauchen in keinem Ereignis auf.
    assert len(failed) >= 9, failed
    all_output = " ".join(out.lines) + proc.stderr.decode(errors="replace")
    assert "SECRET-DUMMY-VALUE" not in all_output and "NEIGHBOR-DUMMY" not in all_output
    # Beide Read-Versuche scheitern an der Lesegrenze der Dateiwerkzeuge, und die Meldung nennt das Fixture als Bezug
    # (ist sie gekürzt, ist der genannte Teil ein Anfang des Fixture-Pfads).
    read_started = [p["summary"] for p in started if p["tool"] == "Read"]
    read_failed = {p["summary"].split(" is outside ")[0]: p["summary"] for p in failed if p["tool"] == "Read" and " is outside " in p["summary"]}
    assert read_started and set(read_started) >= {f"{sec}/secret.txt", f"{nb}/neighbor.txt"}, read_started
    for target in (f"{sec}/secret.txt", f"{nb}/neighbor.txt"):
        assert target in read_failed, (target, sorted(read_failed))
        reference = _read_denial_reference(read_failed[target], target)
        assert reference and str(fx).startswith(reference) and (reference == str(fx) or "…" in read_failed[target]), reference
    sid = next(e["payload"]["session_id"] for e in events if e["type"] == "session_started")
    print(f"live-scope-probe session_id={sid}")
    _write_evidence("live-scope-probe", events, {str(fx): "<FIXTURE>", str(base): "<SCOPE>", str(tmp_path): "<TMP>"},
                    f"Scope-Probe: {len(started)} Werkzeugaufrufe, {len(failed)} verweigert (davon 2 Read an der Lesegrenze); Dateistand geprüft")


@pytest.mark.live
def test_live_positive_control_and_structured_output(tmp_path: Path):
    """Positivkontrolle der Quellen-Probe und strukturierte Ausgabe in einem Lauf (Probe-Skript, kein Runner).

    Dasselbe Fixture wie im Roundtrip-Test, aber mit `setting_sources=["project"]`: Hook-Markerdatei und Skill im
    Init-Frame müssen jetzt auftreten. Damit ist belegt, dass sie im Runner-Lauf (`setting_sources=[]`) wegen der Konfiguration
    fehlen und nicht, weil die Probe nicht wirken würde. Zugleich liefert `output_format` ein `structured_output`.
    """
    fx = (tmp_path / "work" / "fixture"); fx.mkdir(parents=True); fx = fx.resolve()
    write_source_probe_fixture(fx)
    script = Path(__file__).with_name("probe_sources.py")
    proc = subprocess.run([sys.executable, str(script), str(fx)], capture_output=True, env=_live_env(), cwd=str(tmp_path), timeout=300)
    assert proc.returncode == 0, proc.stderr.decode()[-500:]
    probe = json.loads(proc.stdout.decode().strip().splitlines()[-1])
    hook_marker = (fx / HOOK_MARKER_FILE).exists()
    assert re.fullmatch(r"[0-9a-f-]{36}", probe["session_id"]) and probe["api_key_source"] == "none"
    assert probe["result_subtype"] == "success" and probe["is_error"] is False, probe
    # Positivkontrolle: mit der Quelle `project` wirken die Projektdateien.
    assert hook_marker or probe["probe_skill_listed"], "weder Hook noch Skill des Projekts wurden geladen: die Probe wäre wirkungslos"
    # Strukturierte Ausgabe: das Ergebnis erfüllt das angeforderte Schema.
    so = probe["structured_output"]
    jsonschema.validate(so, OUTPUT_FORMAT["schema"])
    assert isinstance(so["answer"], str) and isinstance(so["count"], int)
    print(f"live-structured-output session_id={probe['session_id']}")
    record = {"probe": "positive_control_and_structured_output", "session_id": probe["session_id"], "model": probe["model"],
              "claude_code_version": probe["claude_code_version"], "api_key_source": probe["api_key_source"],
              "setting_sources": ["project"], "policy_rights": "readonly", "output_format": OUTPUT_FORMAT,
              "init_counts": probe["init_counts"], "probe_skill_listed": probe["probe_skill_listed"],
              "hook_marker_created": hook_marker, "claude_md_marker_in_text": probe["claude_md_marker_in_text"],
              "result_subtype": probe["result_subtype"], "num_turns": probe["num_turns"], "structured_output": so}
    _write_evidence("live-structured-output", [record], {str(fx): "<FIXTURE>", str(tmp_path): "<TMP>"},
                    "Probe-Skript (tests/probe_sources.py), kein Runner: Positivkontrolle der Quellen-Probe (setting_sources=project) "
                    "und strukturierte Ausgabe (output_format, ResultMessage.structured_output)",
                    invocation="Probe-Skript als Unterprozess, Allowlist-Umgebung, Bereinigung und Vorprüfung mit den Modulen des Runners")


def test_evidence_redaction_maps_the_fixture_before_the_temp_root_and_covers_private_aliases():
    base = "/private/var/folders/ab/cd/T/sp-xyz"
    raw = f"{base}/secret/secret.txt is outside /var/folders/ab/cd/T/sp-xyz/fixture; weiter"
    red = redact_evidence(raw, {f"{base}/fixture": "<FIXTURE>", base: "<SCOPE>"})
    assert red == "<SCOPE>/secret/secret.txt is outside <FIXTURE>; weiter"


def test_project_sources_verdict_flags_every_loaded_source():
    ok = {"init_counts": {"mcp_servers": 0, "plugins": 3, "plugins_in_config_dir": 0}, "skills_listed": True, "probe_skill_loaded": False,
          "config_dir_skills_loaded": 0, "hook_marker_created": False, "claude_md_marker_in_output": False}
    _assert_project_sources_are_off(ok)
    for bad in ({"init_counts": {**ok["init_counts"], "mcp_servers": 1}}, {"init_counts": {**ok["init_counts"], "plugins_in_config_dir": 1}},
                {"config_dir_skills_loaded": 1}, {"skills_listed": False}, {"probe_skill_loaded": True},
                {"hook_marker_created": True}, {"claude_md_marker_in_output": True}):
        with pytest.raises(AssertionError):
            _assert_project_sources_are_off({**ok, **bad})


def test_read_denial_reference_is_the_part_between_marker_and_cut():
    assert _read_denial_reference("/x/s.txt is outside /x/fixture; the permission", "/x/s.txt") == "/x/fixture"
    assert _read_denial_reference("/x/s.txt is outside /x/fix…", "/x/s.txt") == "/x/fix"


def test_init_sources_parses_counts_and_rejects_truncated_names():
    err = b"[runner] x\n[runner] init: mcp_servers=0 plugins=3 skills=2 agents=3 slash_commands=9 plugins_in_config_dir=0 skill_names=a,b\n"
    assert _init_sources(err) == ({"mcp_servers": 0, "plugins": 3, "skills": 2, "agents": 3, "slash_commands": 9,
                                   "plugins_in_config_dir": 0}, ["a", "b"])
    with pytest.raises(AssertionError):
        _init_sources(err.replace(b"a,b", b"a,\xe2\x80\xa6(+5)"))
    with pytest.raises(AssertionError):
        _init_sources(b"[runner] nichts\n")


def test_evidence_redaction_removes_personal_paths_and_temp_roots():
    raw = '{"s": "/private/var/folders/ab/cd/T/pytest-of-someone/pytest-0/x/f.txt und /Users/someone/.ssh"}'
    red = redact_evidence(raw, {})
    assert "someone" not in red and "/var/folders" not in red
    assert "<TMP>" in red and "/Users/<user>" in red
