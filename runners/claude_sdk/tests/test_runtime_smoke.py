"""Rauchtests der Laufzeit: Diagnose ohne Konto sowie opt-in echte SDK-Läufe.

Die Tests mit Marker ``live`` starten das lokale Claude-Binary mit der eigenen claude.ai-Anmeldung und
verbrauchen Abo-Kontingent. Sie laufen nur mit ``CCMAS_LIVE_TESTS=1`` (nie in CI)::

    CCMAS_LIVE_TESTS=1 uv run --frozen pytest tests/test_runtime_smoke.py -m live -q

Mit ``CCMAS_EVIDENCE_DIR=<Verzeichnis>`` schreiben sie redigierte JSONL-Belege.
"""

from __future__ import annotations

import io
import json
import os
import re
import shutil
import subprocess
import sys
import threading
import time
from datetime import date
from pathlib import Path

import claude_agent_sdk
import pytest

from support import (
    REAL_CLAUDE, Collector, command_line, make_stub_cli, run_runner, start_line, terminals, validate_all,
    write_pipe_stdin,
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


@pytest.mark.parametrize("var", ["ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN",
                                 "CLAUDE_CODE_USE_BEDROCK", "ANTHROPIC_BASE_URL"])
def test_no_api_fallback_when_credentials_in_environment(tmp_path: Path, var: str):
    fx = tmp_path / "w" / "fx"; fx.mkdir(parents=True)
    binary, log = make_stub_cli(tmp_path)
    env = dict(BASE_ENV, **{var: "sk-ant-niemals-ausgeben"})
    _, events, err, out = run_runner([start_line(str(fx), binary)], env)
    p = _assert_no_api_use(events, log)
    assert p["code"] == "api_credentials_in_environment" and var in p["message"]
    assert "niemals" not in err and "niemals" not in " ".join(out.lines)
    assert not log.exists()  # nicht einmal `-v` oder `auth status` wurde aufgerufen


@pytest.mark.skipif(not os.access(REAL_CLAUDE, os.X_OK), reason="lokales claude-Binary nicht vorhanden")
def test_no_api_fallback_with_real_cli_and_empty_config_dir(tmp_path: Path):
    """Echtes Binary, leeres Konfigurationsverzeichnis: kein Login, kein Netzwerk, kein Konto-Zugriff."""
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


def _live_env() -> dict[str, str]:
    env = {k: v for k, v in os.environ.items() if not k.startswith(("ANTHROPIC_", "CLAUDE_CODE_", "CLAUDE_AGENT_SDK"))
           and k != "CLAUDECODE"}
    return env


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
    """Echter Lauf über `python -m claude_sdk_runner` als Unterprozess: Sitzung, echte Werkzeugaktivität, genau ein Ergebnis."""
    fx = (tmp_path / "work" / "fixture"); fx.mkdir(parents=True); fx = fx.resolve()
    prompt = "Lege mit dem Write-Werkzeug im aktuellen Verzeichnis die Datei hello.txt mit dem Inhalt ok an. Antworte danach mit dem Wort fertig."
    line = start_line(str(fx), REAL_CLAUDE, prompt=prompt, model=LIVE_MODEL, policy_profile="scoped_write", max_runtime_seconds=120)
    proc = subprocess.run([sys.executable, "-m", "claude_sdk_runner"], input=(line + "\n").encode(), capture_output=True,
                          env=_live_env(), timeout=180, cwd=str(tmp_path))
    out = Collector(); out.write(proc.stdout)
    validate_all(out)
    events = out.events()
    types = [e["type"] for e in events]
    assert proc.returncode == 0, proc.stderr.decode()[-500:]
    assert types[0] == "session_started"
    started = events[0]["payload"]
    assert re.fullmatch(r"[0-9a-f-]{36}", started["session_id"])
    assert started["auth_source"] == "claude_ai" and started["claude_binary"] == REAL_CLAUDE
    assert started["permission_mode"] == "dontAsk" and started["effort"] == "low" and "haiku" in started["model"]
    tool_phases = [(e["payload"]["tool"], e["payload"]["phase"]) for e in events if e["type"] == "tool_event"]
    assert ("Write", "started") in tool_phases and ("Write", "finished") in tool_phases
    assert (fx / "hello.txt").read_text().strip() == "ok"
    (t,) = terminals(events)
    assert t["type"] == "result" and t["payload"]["outcome"] == "succeeded"
    assert events[-1] is t
    _write_evidence("live-roundtrip", events, {str(fx): "<FIXTURE>", str(tmp_path): "<TMP>"},
                    "python -m claude_sdk_runner, Policy scoped_write, ein Write-Aufruf")


@pytest.mark.live
def test_live_interrupt_ends_with_exactly_one_cancelled_result(tmp_path: Path):
    """Laufender Lauf (Bash sleep) wird über das Protokoll unterbrochen: ein terminales Ergebnis, Kindprozess beendet."""
    fx = (tmp_path / "work" / "fixture"); fx.mkdir(parents=True); fx = fx.resolve()
    prompt = "Führe mit dem Bash-Werkzeug genau diesen Befehl aus: python3 -c \"import time; time.sleep(57)\". Antworte danach mit dem Wort fertig."
    stdin, send, close = write_pipe_stdin()
    out = Collector()
    seen = threading.Event()
    out.on_event = lambda e: seen.set() if e["type"] == "tool_event" and e["payload"]["tool"] == "Bash" and e["payload"]["phase"] == "started" else None
    result: dict = {}

    def target():
        from claude_sdk_runner.entrypoint import main
        result["code"] = main(stdin, out, io.StringIO(), _live_env(), max_turns=4)

    send(start_line(str(fx), REAL_CLAUDE, prompt=prompt, model=LIVE_MODEL, max_runtime_seconds=120))
    th = threading.Thread(target=target); th.start()
    assert seen.wait(90), "Bash-Werkzeug wurde nicht gestartet: " + str(out.lines[-3:])
    # Positivkontrolle: der Kindprozess läuft wirklich, bevor unterbrochen wird.
    marker = r"time\.sleep\(57\)"
    deadline = time.monotonic() + 15
    while subprocess.run(["pgrep", "-f", marker], capture_output=True).returncode != 0:
        assert time.monotonic() < deadline, "Kindprozess wurde nicht gestartet: " + str(out.lines[-3:])
        time.sleep(0.3)
    assert not any(e["type"] == "tool_event" and e["payload"]["phase"] != "started" for e in out.events())
    t0 = time.monotonic()
    send(command_line("interrupt", reason="user_request"))
    th.join(60); close()
    assert not th.is_alive()
    elapsed = time.monotonic() - t0
    events = out.events()
    validate_all(out)
    (t,) = terminals(events)
    assert t["type"] == "result" and t["payload"]["outcome"] == "cancelled" and events[-1] is t
    assert elapsed < 40 and result["code"] == 0
    time.sleep(1)
    assert subprocess.run(["pgrep", "-f", marker], capture_output=True).returncode != 0, "Kindprozess läuft noch"
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
    code, events, err, out = run_runner(
        [start_line(str(fx), REAL_CLAUDE, prompt=prompt, model=LIVE_MODEL, policy_profile="scoped_write", max_runtime_seconds=240)],
        _live_env(), max_turns=16)
    validate_all(out)
    (t,) = terminals(events)
    assert t["type"] == "result", t
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
    _write_evidence("live-scope-probe", events, {str(base): "<SCOPE>", str(tmp_path): "<TMP>"},
                    f"Scope-Probe: {len(started)} Werkzeugaufrufe, {len(failed)} verweigert; Dateistand geprüft")


def test_evidence_redaction_removes_personal_paths_and_temp_roots():
    raw = '{"s": "/private/var/folders/ab/cd/T/pytest-of-someone/pytest-0/x/f.txt und /Users/someone/.ssh"}'
    red = redact_evidence(raw, {})
    assert "someone" not in red and "/var/folders" not in red
    assert "<TMP>" in red and "/Users/<user>" in red
