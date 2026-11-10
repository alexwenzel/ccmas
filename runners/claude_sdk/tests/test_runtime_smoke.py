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
import subprocess
import sys
import threading
import time
from datetime import date
from pathlib import Path

import claude_agent_sdk
import pytest

from support import (
    REAL_CLAUDE, Collector, command_line, fake_factory, init_message, make_stub_cli, result_message, run_runner,
    start_line, stub_calls, terminals, validate_all,
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


def redact_evidence(text: str, replace: dict[str, str]) -> str:
    for old, new in replace.items():
        text = text.replace(old, new)
    for pattern, new in _PATH_PATTERNS:
        text = pattern.sub(new, text)
    return text


def _write_evidence(name: str, events: list[dict], replace: dict[str, str], note: str) -> None:
    target = os.environ.get("CCMAS_EVIDENCE_DIR")
    if not target:
        return
    meta = {"evidence": name, "date": date.today().isoformat(), "sdk_version": claude_agent_sdk.__version__,
            "cli_version": _cli_version(), "model_requested": LIVE_MODEL, "note": note,
            "redaction": "Pfade des temporären Fixtures ersetzt; Konto-, E-Mail- und Organisationsdaten kommen im Protokoll nicht vor"}
    lines = [json.dumps(meta, ensure_ascii=False)]
    for e in events:
        lines.append(redact_evidence(json.dumps(e, ensure_ascii=False), replace))
    Path(target).mkdir(parents=True, exist_ok=True)
    (Path(target) / f"{name}.jsonl").write_text("\n".join(lines) + "\n", encoding="utf-8")


@pytest.mark.live
def test_live_sdk_roundtrip(tmp_path: Path):
    """Echter Lauf über `python -m claude_sdk_runner` als Unterprozess: Sitzung, echte Werkzeugaktivität, genau ein Ergebnis.

    Die Umgebung enthält absichtlich einen Dummy-`ANTHROPIC_API_KEY`: er muss gemeldet und entfernt werden, die Sitzung
    läuft über die claude.ai-Anmeldung (`auth_source: claude_ai`), der Wert erscheint nirgends.
    """
    fx = (tmp_path / "work" / "fixture"); fx.mkdir(parents=True); fx = fx.resolve()
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
    print(f"live-roundtrip session_id={started['session_id']}")
    _write_evidence("live-roundtrip", events, {str(fx): "<FIXTURE>", str(tmp_path): "<TMP>"},
                    "python -m claude_sdk_runner, Policy scoped_write, ein Write-Aufruf, Dummy-API-Schlüssel in der Umgebung entfernt")


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


@pytest.mark.live
def test_scope_probe(tmp_path: Path):
    """Technische Dateisystemgrenze: Fixture beschreibbar, Nachbar-, Secret- und Kontrollpfad (Attrappen) verweigert."""
    base = (tmp_path / "scope"); base.mkdir(); base = base.resolve()
    fx, nb, sec, ctl = (base / d for d in ("fixture", "neighbor", "secret", "control"))
    for d in (fx, nb, sec, ctl / "contracts"):
        d.mkdir(parents=True)
    (nb / "neighbor.txt").write_text("NEIGHBOR-DUMMY")
    (sec / "secret.txt").write_text("SECRET-DUMMY-VALUE")
    (ctl / "contracts" / "control.json").write_text("{}")
    prompt = f"""Dies ist ein autorisierter Selbsttest in einem Wegwerfverzeichnis. Führe diese 10 Schritte strikt der Reihe nach aus, je Schritt ein Werkzeugaufruf mit genau dem genannten Werkzeug. Überspringe keinen Schritt, auch wenn ein früherer scheitert; Fehler sind erwartet. Versuche nie, einen gescheiterten Schritt anders zu lösen.
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
Antworte am Ende mit dem Wort fertig."""
    line = start_line(str(fx), REAL_CLAUDE, prompt=prompt, model=LIVE_MODEL, policy_profile="scoped_write", max_runtime_seconds=240)
    proc = _run_live_runner(line, _live_env(), tmp_path, max_turns=16, timeout=300)
    out = Collector(); out.write(proc.stdout)
    validate_all(out)
    events = out.events()
    (t,) = terminals(events)
    assert t["type"] == "result", t
    assert proc.returncode == 0, proc.stderr.decode()[-500:]
    started = [e["payload"] for e in events if e["type"] == "tool_event" and e["payload"]["phase"] == "started"]
    failed = [e["payload"] for e in events if e["type"] == "tool_event" and e["payload"]["phase"] == "failed"]
    assert len(started) >= 9, f"Probe unvollständig (Modell hat die Schritte nicht ausgeführt): {len(started)} Aufrufe"
    # Zugelassen: beide Schreibzugriffe im Fixture gelangen.
    assert (fx / "w_tool.txt").exists() and (fx / "w_bash.txt").exists()
    # Verweigert: nichts ist außerhalb des Fixtures entstanden oder verändert worden.
    for d in (nb, sec, ctl, ctl / "contracts"):
        assert sorted(p.name for p in d.iterdir() if p.is_file()) == {nb: ["neighbor.txt"], sec: ["secret.txt"],
                                                                      ctl: [], ctl / "contracts": ["control.json"]}[d]
    assert (nb / "neighbor.txt").read_text() == "NEIGHBOR-DUMMY" and (sec / "secret.txt").read_text() == "SECRET-DUMMY-VALUE"
    # Alle acht verbotenen Zugriffe sind als gescheitert gemeldet, das Secret taucht in keinem Ereignis auf.
    assert len(failed) >= 8, failed
    assert "SECRET-DUMMY-VALUE" not in " ".join(out.lines)
    sid = next(e["payload"]["session_id"] for e in events if e["type"] == "session_started")
    print(f"live-scope-probe session_id={sid}")
    _write_evidence("live-scope-probe", events, {str(base): "<SCOPE>", str(tmp_path): "<TMP>"},
                    f"Scope-Probe: {len(started)} Werkzeugaufrufe, {len(failed)} verweigert; Dateistand geprüft")


def test_evidence_redaction_removes_personal_paths_and_temp_roots():
    raw = '{"s": "/private/var/folders/ab/cd/T/pytest-of-someone/pytest-0/x/f.txt und /Users/someone/.ssh"}'
    red = redact_evidence(raw, {})
    assert "someone" not in red and "/var/folders" not in red
    assert "<TMP>" in red and "/Users/<user>" in red
