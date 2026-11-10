"""Tests der Runner-Laufzeit ohne Konto und ohne Netzwerk.

Das Binary ist in diesen Tests ein kleines Stub-Skript (nur `-v` und `auth status`) oder ein Fake-Client mit
echten SDK-Nachrichtentypen. Echte Läufe stehen in test_runtime_smoke.py (Marker ``live``).
"""

from __future__ import annotations

import json
import os
import threading
import time
import tomllib
from pathlib import Path

import pytest
import claude_agent_sdk

from claude_sdk_runner import contracts, environment, policy, runtime
from claude_sdk_runner.entrypoint import ProtocolSink
from claude_sdk_runner.diagnostics import RuntimeDiagnostic

from support import (
    FakeClient, Hang, Wait, Collector, command_line, fake_factory, init_message, make_stub_cli,
    rate_limit_rejected, result_message, run_runner, start_line, terminals, text, tool_result, tool_use,
    validate_all, write_pipe_stdin,
)

BASE_ENV = {"PATH": os.environ.get("PATH", ""), "HOME": os.environ.get("HOME", "/tmp")}


@pytest.fixture
def fx(tmp_path: Path) -> Path:
    d = tmp_path / "work" / "fixture"
    d.mkdir(parents=True)
    return d.resolve()


# --- Umgebung ---------------------------------------------------------------------------------------


def test_credential_variables_are_reported_by_name_only():
    env = {"ANTHROPIC_API_KEY": "sk-ant-geheim", "CLAUDE_CODE_OAUTH_TOKEN": "t", "CLAUDE_CODE_USE_BEDROCK": "1",
           "ANTHROPIC_BASE_URL": "http://x", "ANTHROPIC_AUTH_TOKEN": "a", "PATH": "/bin"}
    assert environment.find_credential_variables(env) == [
        "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL", "CLAUDE_CODE_OAUTH_TOKEN",
        "CLAUDE_CODE_USE_BEDROCK",
    ]


def test_prepare_environment_raises_without_leaking_values():
    env = {"ANTHROPIC_API_KEY": "sk-ant-geheim-wert"}
    with pytest.raises(RuntimeDiagnostic) as e:
        environment.prepare_environment(env)
    assert e.value.code == "api_credentials_in_environment"
    assert "ANTHROPIC_API_KEY" in e.value.message
    assert "geheim" not in e.value.message


def test_prepare_environment_scrubs_parent_session_and_overrides_but_keeps_login_inputs():
    env = {
        "ANTHROPIC_MODEL": "x", "CLAUDE_CODE_SESSION_ID": "s", "CLAUDE_CODE_MESSAGING_TOKEN": "t", "CLAUDECODE": "1",
        "CLAUDE_AGENT_SDK_VERSION": "9", "PATH": "/bin", "HOME": "/h", "CLAUDE_CONFIG_DIR": "/c", "TMPDIR": "/t",
    }
    removed = environment.prepare_environment(env)
    assert sorted(env) == ["CLAUDE_CONFIG_DIR", "HOME", "PATH", "TMPDIR"]
    assert "ANTHROPIC_MODEL" in removed and "CLAUDE_CODE_MESSAGING_TOKEN" in removed
    assert not any(v in " ".join(removed) for v in ("=t", "=s"))


# --- Policy -------------------------------------------------------------------------------------------


def test_scoped_write_policy_is_fail_closed(fx: Path):
    p = policy.build_policy("scoped_write", str(fx), home="/Users/someone")
    assert p.permission_mode == "dontAsk"
    sb = p.sandbox
    assert sb["enabled"] is True and sb["failIfUnavailable"] is True
    assert sb["allowUnsandboxedCommands"] is False
    assert sb["filesystem"]["allowWrite"] == [str(fx)]
    assert str(fx.parent) in sb["filesystem"]["denyRead"] and "/Users/someone" in sb["filesystem"]["denyRead"]
    assert sb["filesystem"]["allowRead"] == [str(fx)]
    # Schreib- und Lesewerkzeuge sind auf das Arbeitsverzeichnis begrenzt, nie pauschal erlaubt.
    for tool in ("Write", "Edit", "Read"):
        assert f"{tool}(//{str(fx).lstrip('/')}/**)" in p.allowed_tools
        assert tool not in p.allowed_tools
    assert p.settings["permissions"]["blockReadsOutsideWorkingDirectories"] is True
    assert "bypassPermissions" not in (p.permission_mode, )


def test_readonly_policy_has_no_write_or_shell(fx: Path):
    p = policy.build_policy("readonly", str(fx), home="/Users/someone")
    assert p.permission_mode == "dontAsk"
    joined = " ".join(p.allowed_tools + list(p.tools or []))
    assert "Write" not in joined and "Edit" not in joined and "Bash" not in joined


def test_autonomous_and_unknown_profiles_are_rejected(fx: Path):
    with pytest.raises(RuntimeDiagnostic) as e:
        policy.build_policy("autonomous", str(fx))
    assert e.value.code == "isolation_not_proven"
    with pytest.raises(RuntimeDiagnostic) as e:
        policy.build_policy("whatever", str(fx))
    assert e.value.code == "unknown_policy_profile"
    status = policy.autonomous_execution_status()
    assert status["available"] is False and status["reasons"]


@pytest.mark.parametrize("bad", ["/", "HOME"])
def test_policy_rejects_cwd_that_would_expose_home_or_root(bad: str):
    cwd = os.path.realpath(os.path.expanduser("~")) if bad == "HOME" else bad
    with pytest.raises(RuntimeDiagnostic) as e:
        policy.build_policy("scoped_write", cwd)
    assert e.value.code == "invalid_cwd"


# --- Vorprüfung: CLI und Konto --------------------------------------------------------------------------


def test_sdk_pin_is_consistent():
    assert runtime.EXPECTED_SDK_VERSION == claude_agent_sdk.__version__
    pyproject = tomllib.loads((Path(__file__).resolve().parents[1] / "pyproject.toml").read_text())
    assert f"claude-agent-sdk=={runtime.EXPECTED_SDK_VERSION}" in pyproject["project"]["dependencies"]


@pytest.mark.parametrize("version", ["1.9.0 (Claude Code)", "2.1.310 (Claude Code)", "kaputt", ""])
def test_old_or_unreadable_cli_version_is_incompatible(tmp_path: Path, fx: Path, version: str):
    binary, log = make_stub_cli(tmp_path, version=version)
    code, events, _, out = run_runner([start_line(str(fx), binary)], dict(BASE_ENV))
    validate_all(out)
    (t,) = terminals(events)
    assert t["type"] == "error" and t["payload"]["code"] == "sdk_cli_incompatible"
    assert t["payload"]["kind"] == "runtime" and code == 0
    assert "SESSION" not in log.read_text()


def test_bundled_sdk_cli_is_rejected(fx: Path):
    bundled = Path(claude_agent_sdk.__file__).parent / "_bundled" / "claude"
    code, events, _, _ = run_runner([start_line(str(fx), str(bundled))], dict(BASE_ENV))
    (t,) = terminals(events)
    assert t["payload"]["code"] == "cli_unavailable"


def test_missing_binary_is_cli_unavailable(fx: Path):
    _, events, _, _ = run_runner([start_line(str(fx), "/nonexistent/claude")], dict(BASE_ENV))
    (t,) = terminals(events)
    assert t["payload"]["code"] == "cli_unavailable"


def test_unknown_policy_profile_is_terminal_error(tmp_path: Path, fx: Path):
    binary, _ = make_stub_cli(tmp_path)
    _, events, _, _ = run_runner([start_line(str(fx), binary, policy_profile="autonomous")], dict(BASE_ENV))
    (t,) = terminals(events)
    assert t["payload"]["code"] == "isolation_not_proven"


# --- Protokoll -------------------------------------------------------------------------------------------


def test_invalid_start_produces_no_stdout_and_exit_code_2(fx: Path):
    code, events, err, out = run_runner(['{"protocol_version":"1"}'], dict(BASE_ENV))
    assert code == 2 and out.lines == [] and "start" in err.lower()


def test_non_start_first_message_is_rejected(fx: Path):
    code, events, _, out = run_runner([command_line("stop")], dict(BASE_ENV))
    assert code == 2 and out.lines == []


def test_eof_without_input_is_exit_2():
    code, _, _, out = run_runner([], dict(BASE_ENV))
    assert code == 2 and out.lines == []


def test_roundtrip_with_fake_client_maps_events_and_effective_config(tmp_path: Path, fx: Path):
    binary, _ = make_stub_cli(tmp_path)
    script = [
        init_message(),
        tool_use("toolu_01", "Write", file_path=str(fx / "a.txt"), content="x"),
        tool_result("toolu_01"),
        tool_use("toolu_02", "Bash", command="export API_KEY=abc123xyz && echo hi"),
        tool_result("toolu_02", is_error=True, content="Exit code 1"),
        text("fertig"),
        result_message(),
    ]
    code, events, err, out = run_runner([start_line(str(fx), binary)], dict(BASE_ENV), client_factory=fake_factory(script))
    validate_all(out)
    assert code == 0
    types = [e["type"] for e in events]
    assert types[0] == "session_started" and types[-1] == "result"
    assert len(terminals(events)) == 1
    started = events[0]["payload"]
    assert started == {
        "session_id": "11111111-2222-3333-4444-555555555555", "model": "claude-haiku-5-5", "effort": "low",
        "claude_version": "2.1.311", "permission_mode": "dontAsk", "claude_binary": binary, "auth_source": "claude_ai",
    }
    tools = [(e["payload"]["tool_use_id"], e["payload"]["phase"]) for e in events if e["type"] == "tool_event"]
    assert tools == [("toolu_01", "started"), ("toolu_01", "finished"), ("toolu_02", "started"), ("toolu_02", "failed")]
    assert "abc123xyz" not in out.lines.__str__() and "abc123xyz" not in err
    assert events[-1]["payload"]["outcome"] == "succeeded"
    # Der Fake-Client erhielt die ausdrücklich gewählte CLI und keine Settings-Quellen.
    opts = FakeClient.created[0].options
    assert opts.cli_path == binary and opts.setting_sources == [] and opts.cwd == str(fx)
    assert FakeClient.created[0].prompt == "Antworte mit dem Wort ok."


def test_stdout_carries_only_protocol_and_logs_go_to_stderr(tmp_path: Path, fx: Path):
    binary, _ = make_stub_cli(tmp_path)
    _, _, err, out = run_runner([start_line(str(fx), binary)], dict(BASE_ENV),
                                client_factory=fake_factory([init_message(), result_message()]))
    validate_all(out)
    assert err  # Logs vorhanden
    assert "Antworte mit dem Wort ok" not in err  # der Prompt wird nie protokolliert


def _interactive(binary: str, fx: Path, extra_lines: list[str]) -> tuple[list[dict], Collector]:
    """Startet einen Lauf, der auf einen Interrupt wartet, sendet die Zeilen und beendet ihn danach per Interrupt."""
    script = [init_message(), Wait(), result_message(subtype="error_during_execution", is_error=True,
                                                     terminal_reason="aborted_streaming", result=None)]
    stdin, send, close = write_pipe_stdin()
    out = Collector()
    started = threading.Event()
    out.on_event = lambda e: started.set() if e["type"] == "session_started" else None
    send(start_line(str(fx), binary))
    th, _ = _run_in_thread("", dict(BASE_ENV), fake_factory(script), stdin, out)
    assert started.wait(10)
    for line in extra_lines:
        send(line)
    send(command_line("interrupt"))
    th.join(15)
    close()
    assert not th.is_alive()
    return out.events(), out


def test_answer_and_second_start_are_non_terminal_warnings(tmp_path: Path, fx: Path):
    binary, _ = make_stub_cli(tmp_path)
    events, out = _interactive(binary, fx, [
        command_line("answer", answer_id="a1", question_id="q1", text="ja", created_at="2026-11-10T12:00:00Z"),
        start_line(str(fx), binary), "kein json"])
    validate_all(out)
    warns = [e["payload"] for e in events if e["type"] == "error"]
    assert {w["code"] for w in warns} == {"answer_unsupported", "unexpected_start", "invalid_command"}
    assert all(w["terminal"] is False and w["kind"] == "protocol" for w in warns)
    (t,) = terminals(events)
    assert t["payload"]["outcome"] == "cancelled"


def test_oversized_command_line_is_rejected_and_reading_continues(tmp_path: Path, fx: Path):
    binary, _ = make_stub_cli(tmp_path)
    events, _ = _interactive(binary, fx, ["x" * (contracts_limit() + 10)])
    assert any(e["type"] == "error" and e["payload"]["code"] == "invalid_command" for e in events)
    (t,) = terminals(events)
    assert t["payload"]["outcome"] == "cancelled"  # der folgende Interrupt wurde noch gelesen


def contracts_limit() -> int:
    from claude_sdk_runner import contracts
    return contracts.max_message_bytes()


# --- Diagnosen im Lauf --------------------------------------------------------------------------------------


def test_subscription_limit_is_non_terminal_error_then_controlled_failure(tmp_path: Path, fx: Path):
    binary, _ = make_stub_cli(tmp_path)
    script = [init_message(), rate_limit_rejected(), result_message(subtype="success", is_error=True, api_error_status=429,
                                                                    terminal_reason="completed", result="limit")]
    code, events, _, out = run_runner([start_line(str(fx), binary)], dict(BASE_ENV), client_factory=fake_factory(script))
    validate_all(out)
    kinds = [(e["type"], e["payload"].get("kind"), e["payload"].get("terminal")) for e in events if e["type"] in ("error", "result")]
    assert kinds[0] == ("error", "subscription_limit", False)
    assert kinds[-1][0] == "result" and events[-1]["payload"]["outcome"] == "controlled_failure"
    assert len(terminals(events)) == 1 and code == 0


def test_api_key_source_in_init_aborts_immediately(tmp_path: Path, fx: Path):
    binary, _ = make_stub_cli(tmp_path)
    script = [init_message(api_key_source="ANTHROPIC_API_KEY"), tool_use("toolu_01", "Write", file_path="x"), result_message()]
    _, events, _, _ = run_runner([start_line(str(fx), binary)], dict(BASE_ENV), client_factory=fake_factory(script))
    (t,) = terminals(events)
    assert t["payload"]["code"] == "api_billing_detected" and t["payload"]["kind"] == "runtime"
    assert not any(e["type"] in ("session_started", "tool_event") for e in events)
    assert FakeClient.created[0].interrupt_calls == 1


def test_missing_interrupt_capability_is_incompatibility(tmp_path: Path, fx: Path):
    binary, _ = make_stub_cli(tmp_path)
    _, events, _, _ = run_runner([start_line(str(fx), binary)], dict(BASE_ENV),
                                 client_factory=fake_factory([init_message(capabilities=[]), result_message()]))
    (t,) = terminals(events)
    assert t["payload"]["code"] == "sdk_cli_incompatible"


def test_authentication_failure_in_session_is_not_logged_in(tmp_path: Path, fx: Path):
    from claude_agent_sdk import AssistantMessage
    binary, _ = make_stub_cli(tmp_path)
    bad = AssistantMessage(content=[], model="m", error="authentication_failed")
    script = [init_message(), bad, result_message(is_error=True, result="login")]
    _, events, _, _ = run_runner([start_line(str(fx), binary)], dict(BASE_ENV), client_factory=fake_factory(script))
    (t,) = terminals(events)
    assert t["type"] == "error" and t["payload"]["code"] == "not_logged_in"


def test_sdk_exception_becomes_one_terminal_error(tmp_path: Path, fx: Path):
    binary, _ = make_stub_cli(tmp_path)

    class Boom(FakeClient):
        async def receive_response(self):
            raise claude_agent_sdk.ProcessError("exit 1 mit geheimem Token sk-ant-abcdef123456")
            yield  # pragma: no cover

    _, events, err, out = run_runner([start_line(str(fx), binary)], dict(BASE_ENV),
                                     client_factory=lambda o: Boom(o, []))
    (t,) = terminals(events)
    assert t["payload"]["code"] == "session_failed"
    assert "abcdef123456" not in err and "abcdef123456" not in " ".join(out.lines)


# --- Interrupt, Stop, Laufzeit -------------------------------------------------------------------------------


def _run_in_thread(lines_first: str, env: dict, factory, stdin, out: Collector, max_turns=None):
    result: dict = {}

    def target():
        from claude_sdk_runner.entrypoint import main
        import io
        result["code"] = main(stdin, out, io.StringIO(), env, client_factory=factory, max_turns=max_turns)

    th = threading.Thread(target=target)
    th.start()
    return th, result


@pytest.mark.parametrize("kind", ["interrupt", "stop"])
def test_interrupt_and_stop_end_with_exactly_one_cancelled_result(tmp_path: Path, fx: Path, kind: str):
    binary, _ = make_stub_cli(tmp_path)
    script = [init_message(), tool_use("toolu_01", "Bash", command="sleep 57"), Wait(),
              result_message(subtype="error_during_execution", is_error=True, terminal_reason="aborted_tools", result=None)]
    stdin, send, close = write_pipe_stdin()
    out = Collector()
    started = threading.Event()
    out.on_event = lambda e: started.set() if e["type"] == "tool_event" else None
    send(start_line(str(fx), binary))
    th, res = _run_in_thread("", dict(BASE_ENV), fake_factory(script), stdin, out)
    assert started.wait(10)
    send(command_line(kind, reason="user_request"))
    th.join(15)
    close()
    assert not th.is_alive()
    events = out.events()
    validate_all(out)
    (t,) = terminals(events)
    assert t["type"] == "result" and t["payload"]["outcome"] == "cancelled"
    assert events[-1] is t and res["code"] == 0
    assert FakeClient.created[0].interrupt_calls == 1


def test_interrupt_after_normal_completion_does_not_fake_cancellation(tmp_path: Path, fx: Path):
    binary, _ = make_stub_cli(tmp_path)
    _, events, _, _ = run_runner([start_line(str(fx), binary), command_line("interrupt")], dict(BASE_ENV),
                                 client_factory=fake_factory([init_message(), result_message()]))
    (t,) = terminals(events)
    assert t["payload"]["outcome"] == "succeeded"


def test_max_runtime_exceeded_is_terminal_error(tmp_path: Path, fx: Path):
    binary, _ = make_stub_cli(tmp_path)
    t0 = time.monotonic()
    _, events, _, _ = run_runner([start_line(str(fx), binary, max_runtime_seconds=1)], dict(BASE_ENV),
                                 client_factory=fake_factory([init_message(), Hang()]))
    (t,) = terminals(events)
    assert t["type"] == "error" and t["payload"]["code"] == "max_runtime_exceeded"
    assert time.monotonic() - t0 < 10
    assert FakeClient.created[0].interrupt_calls == 1


def test_skill_ids_produce_a_warning_not_silent_ignore(tmp_path: Path, fx: Path):
    binary, _ = make_stub_cli(tmp_path)
    _, events, _, _ = run_runner([start_line(str(fx), binary, skill_ids=["ccmas-x"])], dict(BASE_ENV),
                                 client_factory=fake_factory([init_message(), result_message()]))
    assert any(e["type"] == "error" and e["payload"]["code"] == "skills_not_applied" and not e["payload"]["terminal"]
               for e in events)


# --- Genau ein terminales Ergebnis -------------------------------------------------------------------------------------


def _sink() -> tuple[ProtocolSink, Collector]:
    out = Collector()
    ctx = {"project_id": "proj-1", "execution_id": "exec-1", "attempt": 1, "correlation_id": "corr-1"}
    return ProtocolSink(out, ctx), out


RESULT = ("result", {"outcome": "succeeded", "summary": "fertig"})
TERMINAL_ERROR = ("error", {"code": "session_failed", "message": "x", "kind": "runtime", "terminal": True})
WARNING = ("error", {"code": "w", "message": "x", "kind": "runtime", "terminal": False})
ACTIVITY = ("activity", {"kind": "idle", "summary": "x"})


@pytest.mark.parametrize("first,second", [(RESULT, RESULT), (RESULT, TERMINAL_ERROR), (TERMINAL_ERROR, RESULT),
                                          (TERMINAL_ERROR, TERMINAL_ERROR)])
def test_sink_refuses_a_second_terminal_result(first, second):
    sink, out = _sink()
    sink.send(*first)
    assert sink.terminal_sent
    with pytest.raises(RuntimeError):
        sink.send(*second)
    assert len(out.lines) == 1  # das zweite Ergebnis hat stdout nie erreicht


@pytest.mark.parametrize("late", [WARNING, ACTIVITY])
def test_sink_refuses_any_event_after_the_terminal_result(late):
    sink, out = _sink()
    sink.send(*RESULT)
    with pytest.raises(RuntimeError):
        sink.send(*late)
    assert len(out.lines) == 1


def test_sink_allows_non_terminal_events_before_the_terminal_result():
    sink, out = _sink()
    sink.send(*WARNING)
    sink.send(*ACTIVITY)
    assert not sink.terminal_sent
    sink.send(*RESULT)
    validate_all(out)
    assert [e["type"] for e in out.events()] == ["error", "activity", "result"]


@pytest.mark.parametrize("bad", [
    ("activity", {"kind": "bogus", "summary": "x"}),
    ("result", {"outcome": "unbekannt", "summary": "x"}),
    ("start", {"x": 1}),  # falsche Richtung
    ("activity", {"kind": "idle", "summary": "x" * 600}),
])
def test_sink_rejects_invalid_outgoing_lines_without_writing(bad):
    sink, out = _sink()
    with pytest.raises(contracts.ContractError):
        sink.send(*bad)
    assert out.lines == [] and not sink.terminal_sent


def test_exception_after_the_result_does_not_produce_a_second_terminal(tmp_path: Path, fx: Path):
    binary, _ = make_stub_cli(tmp_path)

    class FailsOnExit(FakeClient):
        async def __aexit__(self, *exc):
            raise RuntimeError("Fehler beim Schließen der Sitzung")

    script = [init_message(), result_message()]
    code, events, err, out = run_runner([start_line(str(fx), binary)], dict(BASE_ENV),
                                        client_factory=lambda o: FailsOnExit(o, script))
    validate_all(out)
    (t,) = terminals(events)
    assert t["type"] == "result" and t["payload"]["outcome"] == "succeeded"
    assert code == 0  # der Fehler nach dem Ergebnis macht den Versuch nicht zum Absturz
    assert "verworfen" in err


def test_billing_error_in_session_is_terminal_error_without_fallback(tmp_path: Path, fx: Path):
    from claude_agent_sdk import AssistantMessage
    binary, _ = make_stub_cli(tmp_path)
    script = [init_message(), AssistantMessage(content=[], model="m", error="billing_error"),
              result_message(is_error=True, result="billing")]
    _, events, _, _ = run_runner([start_line(str(fx), binary)], dict(BASE_ENV), client_factory=fake_factory(script))
    (t,) = terminals(events)
    assert t["type"] == "error" and t["payload"]["code"] == "billing_error" and t["payload"]["kind"] == "runtime"
    assert "API" in t["payload"]["message"]


def test_cli_version_in_init_must_match_the_checked_one(tmp_path: Path, fx: Path):
    binary, _ = make_stub_cli(tmp_path, version="2.1.311 (Claude Code)")
    script = [init_message(version="2.1.312"), tool_use("toolu_01", "Write", file_path="x"), result_message()]
    _, events, _, _ = run_runner([start_line(str(fx), binary)], dict(BASE_ENV), client_factory=fake_factory(script))
    (t,) = terminals(events)
    assert t["payload"]["code"] == "sdk_cli_incompatible"
    assert not any(e["type"] in ("session_started", "tool_event") for e in events)
    assert FakeClient.created[0].interrupt_calls == 1


def test_newer_but_consistent_cli_runs_with_untested_warning(tmp_path: Path, fx: Path):
    binary, _ = make_stub_cli(tmp_path, version="2.1.312 (Claude Code)")
    _, events, _, _ = run_runner([start_line(str(fx), binary)], dict(BASE_ENV),
                                 client_factory=fake_factory([init_message(version="2.1.312"), result_message()]))
    warns = [e["payload"]["code"] for e in events if e["type"] == "error"]
    assert warns == ["cli_version_untested"]
    assert terminals(events)[0]["payload"]["outcome"] == "succeeded"


# --- Interrupt vor der bereiten Sitzung ----------------------------------------------------------------------------------


@pytest.mark.parametrize("kind", ["interrupt", "stop"])
def test_early_interrupt_is_applied_once_the_session_is_ready(tmp_path: Path, fx: Path, kind: str):
    """Das Kommando trifft zusammen mit dem Start ein, also vor client.query. Es darf nicht wirkungslos bleiben."""
    binary, _ = make_stub_cli(tmp_path)
    script = [init_message(), Wait(), result_message(subtype="error_during_execution", is_error=True,
                                                     terminal_reason="aborted_streaming", result=None)]
    def slow_query(options):
        client = FakeClient(options, script)
        client.query_delay = 0.5  # das Kommando wird gelesen, während die Anfrage noch gesendet wird
        return client

    _, events, _, _ = run_runner([start_line(str(fx), binary), command_line(kind)], dict(BASE_ENV),
                                 client_factory=slow_query)
    (t,) = terminals(events)
    assert t["type"] == "result" and t["payload"]["outcome"] == "cancelled"
    client = FakeClient.created[0]
    assert client.calls == ["query", "interrupt"]  # nie vor der Anfrage
