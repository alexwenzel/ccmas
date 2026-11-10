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

import support
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


def test_prepare_environment_removes_credentials_and_reports_names_only():
    env = {"ANTHROPIC_API_KEY": "sk-ant-geheim-wert", "CLAUDE_CODE_USE_BEDROCK": "1", "AWS_BEARER_TOKEN_BEDROCK": "geheim2",
           "CLAUDE_CODE_SESSION_ID": "s", "PATH": "/bin"}
    result = environment.prepare_environment(env)
    assert env == {"PATH": "/bin"}  # nichts davon erreicht je ein Kindprozess
    assert result.credentials == ["ANTHROPIC_API_KEY", "AWS_BEARER_TOKEN_BEDROCK", "CLAUDE_CODE_USE_BEDROCK"]
    assert result.other == ["CLAUDE_CODE_SESSION_ID"]
    assert "geheim" not in repr(result)


def test_prepare_environment_scrubs_parent_session_and_overrides_but_keeps_login_inputs():
    env = {
        "ANTHROPIC_MODEL": "x", "CLAUDE_CODE_SESSION_ID": "s", "CLAUDE_CODE_MESSAGING_TOKEN": "t", "CLAUDECODE": "1",
        "CLAUDE_AGENT_SDK_VERSION": "9", "PATH": "/bin", "HOME": "/h", "CLAUDE_CONFIG_DIR": "/c", "TMPDIR": "/t",
    }
    result = environment.prepare_environment(env)
    assert sorted(env) == ["CLAUDE_CONFIG_DIR", "HOME", "PATH", "TMPDIR"]
    assert "ANTHROPIC_MODEL" in result.removed and "CLAUDE_CODE_MESSAGING_TOKEN" in result.removed
    assert result.credentials == []  # kein Login-Override, nur Erbe einer Elternsitzung
    assert not any(v in " ".join(result.removed) for v in ("=t", "=s"))


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


def test_writing_profiles_are_exactly_everything_except_readonly():
    """Fail-closed: ein neues Profil gilt als schreibend, bis es ausdrücklich als schreibgeschützt festgeschrieben ist."""
    assert set(policy.PROFILES) - set(policy.WRITING_PROFILES) == {"readonly"}
    assert set(policy.WRITING_PROFILES) <= set(policy.PROFILES)


@pytest.mark.parametrize("profile", ["readonly", "scoped_write"])
def test_policy_turns_off_claude_ai_connectors_in_both_profiles(fx: Path, profile: str):
    p = policy.build_policy(profile, str(fx))
    assert p.settings["disableClaudeAiConnectors"] is True
    assert p.settings["permissions"]["blockReadsOutsideWorkingDirectories"] is True


@pytest.mark.parametrize("profile", ["readonly", "scoped_write"])
def test_policy_never_touches_the_synced_skill_and_plugin_settings(fx: Path, profile: str):
    """`syncClaudeAiSkills`/`syncClaudeAiPlugins` = false verschiebt laut Settings-Doku bereits synchronisierte Einträge
    in `.trash`; der Runner darf die Konfiguration des Benutzers nicht verändern. Die Quellen bleiben über
    `setting_sources=[]` aus (Beleg: Init-Frame im Live-Test)."""
    p = policy.build_policy(profile, str(fx))
    assert not [k for k in p.settings if k.lower().startswith("sync")]


@pytest.mark.parametrize("profile", ["readonly", "scoped_write"])
def test_connector_setting_and_empty_sources_reach_the_sdk_options(tmp_path: Path, fx: Path, profile: str):
    binary, _ = make_stub_cli(tmp_path)
    run_runner([start_line(str(fx), binary, policy_profile=profile)], dict(BASE_ENV),
               client_factory=fake_factory([init_message(), result_message()]))
    opts = FakeClient.created[0].options
    assert json.loads(opts.settings)["disableClaudeAiConnectors"] is True
    assert opts.setting_sources == []


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
    assert "schließen" in err  # Fehler beim Schließen wird protokolliert, nicht gemeldet


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


def test_newer_but_consistent_cli_runs_readonly_with_untested_warning(tmp_path: Path, fx: Path):
    binary, _ = make_stub_cli(tmp_path, version="2.1.312 (Claude Code)")
    _, events, _, _ = run_runner([start_line(str(fx), binary, policy_profile="readonly")], dict(BASE_ENV),
                                 client_factory=fake_factory([init_message(version="2.1.312"), result_message()]))
    warns = [e["payload"] for e in events if e["type"] == "error"]
    assert [w["code"] for w in warns] == ["cli_version_untested"]
    assert warns[0]["terminal"] is False and "2.1.312" in warns[0]["message"] and "2.1.311" in warns[0]["message"]
    assert terminals(events)[0]["payload"]["outcome"] == "succeeded"


def test_untested_cli_version_blocks_the_writing_profile_before_any_session(tmp_path: Path, fx: Path):
    """Fail-closed: bei ungetesteter CLI-Version kein Schreibprofil, ohne Sitzung und ohne Modellaufruf."""
    binary, log = make_stub_cli(tmp_path, version="2.1.312 (Claude Code)")
    FakeClient.created.clear()
    code, events, _, out = run_runner([start_line(str(fx), binary, policy_profile="scoped_write")], dict(BASE_ENV),
                                      client_factory=fake_factory([init_message(version="2.1.312"), tool_use("toolu_01", "Write", file_path="x"),
                                                                   result_message()]))
    validate_all(out)
    (t,) = terminals(events)
    assert t["type"] == "error" and t["payload"]["terminal"] is True and code == 0
    assert t["payload"]["code"] == "cli_version_untested" and t["payload"]["kind"] == "runtime"
    assert "2.1.312" in t["payload"]["message"] and "2.1.311" in t["payload"]["message"]
    assert [e["type"] for e in events] == ["error"]  # keine Warnung davor, keine Sitzung, kein Werkzeug
    assert FakeClient.created == [] and "SESSION" not in log.read_text()


def test_verified_cli_version_allows_the_writing_profile(tmp_path: Path, fx: Path):
    binary, _ = make_stub_cli(tmp_path)  # 2.1.311
    _, events, _, _ = run_runner([start_line(str(fx), binary, policy_profile="scoped_write")], dict(BASE_ENV),
                                 client_factory=fake_factory([init_message(), result_message()]))
    assert [e["payload"]["code"] for e in events if e["type"] == "error"] == []
    assert terminals(events)[0]["payload"]["outcome"] == "succeeded"


@pytest.mark.parametrize("token", ["2.1.311-rc.1", "2.1.311.7", "2.1.311+build5", "2.1.3110"])
def test_only_the_exact_verified_version_token_counts_as_tested(tmp_path: Path, fx: Path, token: str):
    """Fail-closed streng: `2.1.311-rc.1` oder `2.1.311.7` sind nicht die nachgewiesene Version."""
    binary, log = make_stub_cli(tmp_path, version=f"{token} (Claude Code)")
    FakeClient.created.clear()
    code, events, _, out = run_runner([start_line(str(fx), binary, policy_profile="scoped_write")], dict(BASE_ENV),
                                      client_factory=fake_factory([init_message(version=token), result_message()]))
    validate_all(out)
    (t,) = terminals(events)
    assert t["type"] == "error" and t["payload"]["code"] == "cli_version_untested" and code == 0
    assert token in t["payload"]["message"] and "2.1.311" in t["payload"]["message"]
    assert FakeClient.created == [] and "SESSION" not in log.read_text()


@pytest.mark.parametrize("token", ["2.1.311-rc.1", "2.1.311.7"])
def test_readonly_runs_with_a_warning_for_a_lookalike_version_token(tmp_path: Path, fx: Path, token: str):
    binary, _ = make_stub_cli(tmp_path, version=f"{token} (Claude Code)")
    _, events, _, _ = run_runner([start_line(str(fx), binary, policy_profile="readonly")], dict(BASE_ENV),
                                 client_factory=fake_factory([init_message(version=token), result_message()]))
    assert [e["payload"]["code"] for e in events if e["type"] == "error"] == ["cli_version_untested"]
    assert next(e for e in events if e["type"] == "session_started")["payload"]["claude_version"] == token
    assert terminals(events)[0]["payload"]["outcome"] == "succeeded"


# --- Init-Frame-Zusammenfassung (Quellen-Probe) -----------------------------------------------------------------------


def test_init_summary_logs_counts_and_skill_names_only(tmp_path: Path, fx: Path):
    binary, _ = make_stub_cli(tmp_path)
    extra = {"mcp_servers": [{"name": "geheimer-connector", "status": "connected"}],
             "plugins": [{"name": "mein-plugin", "path": "/irgendwo"}],
             "skills": ["debug", "ccmas-probe"], "agents": ["Explore", "Plan", "general-purpose"],
             "slash_commands": ["a", "b", "c", "d"]}
    code, events, err, out = run_runner([start_line(str(fx), binary)], dict(BASE_ENV),
                                        client_factory=fake_factory([init_message(extra=extra), result_message()]))
    validate_all(out)
    (line,) = [ln for ln in err.splitlines() if "init:" in ln]
    assert "mcp_servers=1 plugins=1 skills=2 agents=3 slash_commands=4 plugins_in_config_dir=0" in line
    assert "skill_names=debug,ccmas-probe" in line
    for hidden in ("geheimer-connector", "mein-plugin", "Explore", "general-purpose"):
        assert hidden not in err  # Namen außer denen der Skills gelangen nie ins Log
    assert "geheimer-connector" not in " ".join(out.lines)  # und keine neuen Protokollfelder
    assert code == 0 and terminals(events)[0]["payload"]["outcome"] == "succeeded"


def test_init_summary_marks_missing_lists_and_bounds_the_skill_names():
    assert runtime.init_summary({}) == "mcp_servers=? plugins=? skills=? agents=? slash_commands=? plugins_in_config_dir=? skill_names="
    many = {"skills": [f"skill-{i:03d}" for i in range(100)]}
    line = runtime.init_summary(many)
    assert "skills=100" in line and "skill-000" in line and "skill-099" not in line
    assert "…(+" in line and len(line) < 600  # gekürzt und als gekürzt erkennbar


def test_init_summary_counts_plugins_below_the_config_dir_and_flags_unclassifiable_entries():
    """Plugins unter dem Konfigurationsverzeichnis (installiert oder synchronisiert) sind genau die, die aus der
    Benutzerkonfiguration stammen; deren Anzahl ist die eigentliche Messgröße der Quellen-Probe."""
    cfg = "/home/u/.claude"
    plugins = [{"name": "a", "path": f"{cfg}/plugins/synced/a"}, {"name": "b", "path": f"{cfg}/plugins/cache/x/b"},
               {"name": "c", "path": "/opt/cli/bundled/c"}, {"name": "d", "path": f"{cfg}-other/d"}]
    line = runtime.init_summary({"plugins": plugins}, config_dir=cfg)
    assert "plugins=4" in line and "plugins_in_config_dir=2" in line
    assert "plugins_in_config_dir=?" in runtime.init_summary({"plugins": [{"name": "ohne-pfad"}]}, config_dir=cfg)


def test_init_summary_uses_claude_config_dir_from_the_environment(monkeypatch: pytest.MonkeyPatch):
    monkeypatch.setenv("CLAUDE_CONFIG_DIR", "/cfg")
    assert "plugins_in_config_dir=1" in runtime.init_summary({"plugins": [{"name": "a", "path": "/cfg/plugins/a"}]})


# --- Hilfen der Tests ----------------------------------------------------------------------------------------------------


def test_hang_helper_is_bounded_and_fails_instead_of_hanging(monkeypatch: pytest.MonkeyPatch):
    """Regression der Laufzeitbegrenzung darf die Suite nicht hängen lassen: der Hänger läuft nach der Obergrenze ab."""
    import asyncio
    monkeypatch.setattr(support, "HANG_LIMIT_SECONDS", 0.05)
    t0 = time.monotonic()
    with pytest.raises(AssertionError, match="nicht abgebrochen"):
        asyncio.run(support.bounded_hang("probe"))
    assert time.monotonic() - t0 < 5
    assert support.EXPIRED_HANGS == ["probe"]
    support.EXPIRED_HANGS.clear()  # erwartet; sonst schlägt die Fixture in conftest.py an


def test_unaborted_hang_in_a_runtime_phase_turns_the_test_red_not_into_a_hang(tmp_path: Path, fx: Path, monkeypatch: pytest.MonkeyPatch):
    """Simulierte Regression: das Schließen wird nicht begrenzt. Der Hänger endet nach der Obergrenze, der Runner schluckt
    die Ausnahme beim Schließen, aber die Fixture meldet den abgelaufenen Hänger."""
    monkeypatch.setattr(support, "HANG_LIMIT_SECONDS", 0.2)
    monkeypatch.setattr(runtime, "CLOSE_TIMEOUT", 3600)
    binary, _ = make_stub_cli(tmp_path)
    _, events, _, _ = run_runner([start_line(str(fx), binary)], dict(BASE_ENV), client_factory=_hanging(hang_exit=True))
    assert terminals(events)[0]["payload"]["outcome"] == "succeeded"
    assert support.EXPIRED_HANGS == ["exit_cancelled"]  # so würde conftest.py den Test rot färben
    support.EXPIRED_HANGS.clear()


# --- Interrupt vor der bereiten Sitzung ----------------------------------------------------------------------------------


@pytest.mark.parametrize("kind", ["interrupt", "stop"])
def test_early_interrupt_is_applied_once_the_session_is_ready(tmp_path: Path, fx: Path, kind: str):
    """Das Kommando trifft zusammen mit dem Start ein, also vor client.query. Es darf nicht wirkungslos bleiben."""
    binary, _ = make_stub_cli(tmp_path)
    script = [init_message(), Wait(), result_message(subtype="error_during_execution", is_error=True,
                                                     terminal_reason="aborted_streaming", result=None)]
    FakeClient.created.clear()

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


def _query_raises(options):
    client = FakeClient(options, [init_message(), Wait()])
    client.query_delay = 0.3
    client.query_error = RuntimeError("Verbindung zur Anfrage verloren")
    return client


@pytest.mark.parametrize("kind", ["interrupt", "stop"])
def test_early_interrupt_with_failing_query_ends_with_exactly_one_session_failed(tmp_path: Path, fx: Path, kind: str):
    """Interrupt trifft vor dem Ende von query ein und query scheitert: ein terminaler Fehler, kein Hängen."""
    binary, _ = make_stub_cli(tmp_path)
    FakeClient.created.clear()
    t0 = time.monotonic()
    code, events, _, out = run_runner([start_line(str(fx), binary), command_line(kind)], dict(BASE_ENV),
                                      client_factory=_query_raises)
    validate_all(out)
    (t,) = terminals(events)
    assert t["type"] == "error" and t["payload"]["code"] == "session_failed" and t["payload"]["kind"] == "runtime"
    assert events[-1] is t and code == 0 and time.monotonic() - t0 < 5
    assert FakeClient.created[0].interrupt_calls == 0  # die Sitzung war nie bereit: kein Interrupt auf einem toten Client


# --- Laufzeitgrenze umfasst Verbindungsaufbau und Anfrage ------------------------------------------------------------


def _hanging(**flags):
    def factory(options):
        client = FakeClient(options, [init_message(), result_message()])
        for name, value in flags.items():
            setattr(client, name, value)
        return client
    return factory


@pytest.mark.parametrize("phase,cancelled", [("hang_enter", "enter_cancelled"), ("hang_query", "query_cancelled")])
def test_max_runtime_also_bounds_connect_and_query(tmp_path: Path, fx: Path, phase: str, cancelled: str):
    binary, _ = make_stub_cli(tmp_path)
    FakeClient.created.clear()
    t0 = time.monotonic()
    code, events, _, out = run_runner([start_line(str(fx), binary, max_runtime_seconds=1)], dict(BASE_ENV),
                                      client_factory=_hanging(**{phase: True}))
    validate_all(out)
    (t,) = terminals(events)
    assert t["type"] == "error" and t["payload"]["code"] == "max_runtime_exceeded" and code == 0
    assert time.monotonic() - t0 < 8
    client = FakeClient.created[0]
    assert getattr(client, cancelled) is True  # die blockierte Phase wurde abgebrochen, kein Task bleibt hängen
    assert not any(e["type"] in ("session_started", "result") for e in events)


def test_hanging_query_is_closed_and_not_interrupted_without_a_ready_session(tmp_path: Path, fx: Path):
    binary, _ = make_stub_cli(tmp_path)
    FakeClient.created.clear()
    run_runner([start_line(str(fx), binary, max_runtime_seconds=1)], dict(BASE_ENV), client_factory=_hanging(hang_query=True))
    client = FakeClient.created[0]
    assert client.exit_calls == 1  # der Client wird nach dem Timeout geschlossen (beendet den CLI-Prozess)
    assert client.interrupt_calls == 0 and client.query_cancelled is True


def test_hanging_disconnect_is_bounded_and_keeps_the_single_result(tmp_path: Path, fx: Path, monkeypatch: pytest.MonkeyPatch):
    monkeypatch.setattr(runtime, "CLOSE_TIMEOUT", 0.5)
    binary, _ = make_stub_cli(tmp_path)
    FakeClient.created.clear()
    t0 = time.monotonic()
    code, events, err, out = run_runner([start_line(str(fx), binary)], dict(BASE_ENV), client_factory=_hanging(hang_exit=True))
    validate_all(out)
    (t,) = terminals(events)
    assert t["type"] == "result" and t["payload"]["outcome"] == "succeeded" and code == 0
    assert time.monotonic() - t0 < 5
    assert FakeClient.created[0].exit_cancelled is True
    assert "schließen" in err


def test_hanging_disconnect_after_timeout_still_ends_with_max_runtime_exceeded(tmp_path: Path, fx: Path, monkeypatch: pytest.MonkeyPatch):
    monkeypatch.setattr(runtime, "CLOSE_TIMEOUT", 0.5)
    binary, _ = make_stub_cli(tmp_path)
    _, events, _, _ = run_runner([start_line(str(fx), binary, max_runtime_seconds=1)], dict(BASE_ENV),
                                 client_factory=lambda o: _hang_both(o))
    (t,) = terminals(events)
    assert t["type"] == "error" and t["payload"]["code"] == "max_runtime_exceeded"


def _hang_both(options):
    client = FakeClient(options, [init_message(), Hang()])
    client.hang_exit = True
    return client
