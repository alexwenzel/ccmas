"""Systemprompt und strukturierte Ausgabe im Runner (CC-14, W14.2). Ohne Konto, ohne Netzwerk.

Das Binary ist ein Stub-Skript (nur `-v` und `auth status`), die Sitzung ein Fake-Client mit echten SDK-Nachrichtentypen.
Die echte Durchleitung durch die CLI belegt der Live-Rauchlauf in Go (`internal/supervisor`, `CCMAS_LIVE_TESTS=1`).
"""

from __future__ import annotations

import json
import os
from pathlib import Path
from typing import Any

import jsonschema
import pytest

from claude_sdk_runner import contracts, runtime
from claude_sdk_runner.contracts import ContractError

import support
from support import (
    FakeClient, fake_factory, init_message, make_stub_cli, result_message, run_runner, start_line, terminals, tool_result,
    tool_use, validate_all,
)

BASE_ENV = {"PATH": os.environ.get("PATH", ""), "HOME": os.environ.get("HOME", "/tmp")}

SCHEMA: dict[str, Any] = {
    "$schema": "http://json-schema.org/draft-07/schema#",
    "type": "object",
    "properties": {"answer": {"type": "string"}, "count": {"type": "integer"}},
    "required": ["answer", "count"],
    "additionalProperties": False,
}
OUTPUT = {"answer": "ok", "count": 3}


@pytest.fixture
def fx(tmp_path: Path) -> Path:
    d = tmp_path / "work" / "fixture"
    d.mkdir(parents=True)
    return d.resolve()


def run(fx: Path, tmp_path: Path, script: list[Any], **over: Any):
    binary, log = make_stub_cli(tmp_path)
    code, events, err, out = run_runner([start_line(str(fx), binary, policy_profile="readonly", **over)], dict(BASE_ENV),
                                        client_factory=fake_factory(script))
    validate_all(out)
    return code, events, err, binary, log


def options() -> Any:
    (client,) = FakeClient.created
    return client.options


# --- Systemprompt --------------------------------------------------------------------------------------


def test_system_prompt_is_passed_to_the_sdk(fx: Path, tmp_path: Path):
    text = "Du bist der Supervisor.\nAntworte nur strukturiert."
    _, events, _, _, _ = run(fx, tmp_path, [init_message(), result_message()], system_prompt=text)
    assert options().system_prompt == text
    (t,) = terminals(events)
    assert t["type"] == "result" and t["payload"]["outcome"] == "succeeded"


def test_without_system_prompt_the_previous_behaviour_stays(fx: Path, tmp_path: Path):
    """Ohne Angabe bleibt `system_prompt=None`: das SDK startet die CLI mit `--system-prompt \"\"` (kein Standardprompt)."""
    run(fx, tmp_path, [init_message(), result_message()])
    assert options().system_prompt is None
    assert options().output_format is None


def test_sdk_turns_none_into_an_empty_system_prompt_flag(tmp_path: Path):
    """Belegt die Aussage der Dokumentation am SDK selbst (Quelltext des Transports, ohne Prozessstart)."""
    from claude_agent_sdk import ClaudeAgentOptions
    from claude_agent_sdk._internal.transport.subprocess_cli import SubprocessCLITransport

    def flags(system_prompt: str | None) -> list[str]:
        t = SubprocessCLITransport(prompt="x", options=ClaudeAgentOptions(cli_path="/bin/true", system_prompt=system_prompt))
        return t._build_command()

    cmd = flags(None)
    assert cmd[cmd.index("--system-prompt") + 1] == ""
    cmd = flags("Eigener Prompt")
    assert cmd[cmd.index("--system-prompt") + 1] == "Eigener Prompt"


# --- Ausgabeschema und strukturierte Ausgabe -------------------------------------------------------


def test_output_schema_becomes_output_format(fx: Path, tmp_path: Path):
    run(fx, tmp_path, [init_message(), result_message(structured_output=OUTPUT)], output_schema=SCHEMA)
    assert options().output_format == {"type": "json_schema", "schema": SCHEMA}


def test_structured_output_is_reported_in_the_result(fx: Path, tmp_path: Path):
    script = [
        init_message(),
        tool_use("toolu_so", "StructuredOutput", **OUTPUT),
        tool_result("toolu_so", content="Structured output provided successfully"),
        result_message(structured_output=OUTPUT, result="Fertig."),
    ]
    code, events, err, _, _ = run(fx, tmp_path, script, output_schema=SCHEMA)
    (t,) = terminals(events)
    assert t["type"] == "result" and t["payload"]["outcome"] == "succeeded" and code == 0
    assert t["payload"]["structured_output"] == OUTPUT
    # Das Werkzeug StructuredOutput ist der Mechanismus, kein fachlicher Schritt: kein tool_event, aber gezählt im Log.
    assert [e for e in events if e["type"] == "tool_event"] == []
    assert "structured_output: aufrufe=1 abgelehnt=0" in err


def test_other_tools_are_still_reported_next_to_structured_output(fx: Path, tmp_path: Path):
    script = [
        init_message(),
        tool_use("toolu_r", "Read", file_path=str(fx / "a.txt")),
        tool_result("toolu_r"),
        tool_use("toolu_so", "StructuredOutput", **OUTPUT),
        tool_result("toolu_so", content="Structured output provided successfully"),
        result_message(structured_output=OUTPUT),
    ]
    _, events, _, _, _ = run(fx, tmp_path, script, output_schema=SCHEMA)
    tools = [(e["payload"]["tool"], e["payload"]["phase"]) for e in events if e["type"] == "tool_event"]
    assert tools == [("Read", "started"), ("Read", "finished")]


def test_rejected_structured_output_calls_are_counted_not_reported(fx: Path, tmp_path: Path):
    script = [
        init_message(),
        tool_use("toolu_1", "StructuredOutput", answer=1),
        tool_result("toolu_1", is_error=True, content="answer must be string"),
        tool_use("toolu_2", "StructuredOutput", **OUTPUT),
        tool_result("toolu_2", content="Structured output provided successfully"),
        result_message(structured_output=OUTPUT),
    ]
    _, events, err, _, _ = run(fx, tmp_path, script, output_schema=SCHEMA)
    assert [e for e in events if e["type"] == "tool_event"] == []
    assert "structured_output: aufrufe=2 abgelehnt=1" in err
    assert "answer must be string" not in " ".join(json.dumps(e) for e in events)  # Inhalt gelangt nicht ins Protokoll


def test_retries_exhausted_is_a_controlled_failure_with_its_own_code(fx: Path, tmp_path: Path):
    script = [init_message(), result_message(subtype="error_max_structured_output_retries", is_error=True, result=None,
                                              errors=["Failed to provide valid structured output after 5 attempts"])]
    code, events, _, _, _ = run(fx, tmp_path, script, output_schema=SCHEMA)
    (t,) = terminals(events)
    assert t["type"] == "error" and t["payload"]["terminal"] is True and code == 0
    assert t["payload"]["code"] == "structured_output_failed" and t["payload"]["kind"] == "runtime"
    assert "after 5 attempts" in t["payload"]["message"]
    assert not any(e["type"] == "result" for e in events)


def test_success_without_structured_output_is_a_failure_when_a_schema_is_set(fx: Path, tmp_path: Path):
    _, events, _, _, _ = run(fx, tmp_path, [init_message(), result_message(result="Hier ist ein Text.")], output_schema=SCHEMA)
    (t,) = terminals(events)
    assert t["type"] == "error" and t["payload"]["code"] == "structured_output_missing" and t["payload"]["terminal"] is True


@pytest.mark.parametrize("value", ["text", ["a"], 3, True])
def test_structured_output_that_is_not_an_object_is_invalid(fx: Path, tmp_path: Path, value: Any):
    _, events, _, _, _ = run(fx, tmp_path, [init_message(), result_message(structured_output=value)], output_schema={"type": "object"})
    (t,) = terminals(events)
    assert t["type"] == "error" and t["payload"]["code"] == "structured_output_invalid"


def test_structured_output_that_violates_the_schema_is_invalid(fx: Path, tmp_path: Path):
    """Die Prüfung des SDK ist nicht der einzige Schutz: der Runner prüft die Ausgabe gegen dasselbe Schema (Draft-07)."""
    _, events, _, _, _ = run(fx, tmp_path, [init_message(), result_message(structured_output={"answer": 7, "count": "x"})],
                             output_schema=SCHEMA)
    (t,) = terminals(events)
    assert t["type"] == "error" and t["payload"]["code"] == "structured_output_invalid"
    assert "7" not in t["payload"]["message"]  # Werte der Ausgabe gehören nicht in die Fehlermeldung


def test_oversized_structured_output_is_a_controlled_failure(fx: Path, tmp_path: Path):
    big = {"answer": "x" * 270_000, "count": 1}
    _, events, _, _, _ = run(fx, tmp_path, [init_message(), result_message(structured_output=big)], output_schema=SCHEMA)
    (t,) = terminals(events)
    assert t["type"] == "error" and t["payload"]["code"] == "structured_output_too_large"


def test_structured_output_is_ignored_without_a_schema(fx: Path, tmp_path: Path):
    _, events, _, _, _ = run(fx, tmp_path, [init_message(), result_message(structured_output=OUTPUT)])
    (t,) = terminals(events)
    assert t["type"] == "result" and "structured_output" not in t["payload"]


def test_other_endings_do_not_report_structured_output(fx: Path, tmp_path: Path):
    _, events, _, _, _ = run(fx, tmp_path, [init_message(), result_message(subtype="error_max_turns", is_error=True,
                                                                           terminal_reason="max_turns", result=None)],
                             output_schema=SCHEMA)
    (t,) = terminals(events)
    assert t["type"] == "result" and t["payload"]["outcome"] == "controlled_failure" and "structured_output" not in t["payload"]


def test_session_started_reports_the_sdk_version(fx: Path, tmp_path: Path):
    _, events, _, _, _ = run(fx, tmp_path, [init_message(), result_message()])
    (started,) = [e["payload"] for e in events if e["type"] == "session_started"]
    import claude_agent_sdk
    assert started["sdk_version"] == claude_agent_sdk.__version__ == runtime.EXPECTED_SDK_VERSION


# --- Vorprüfung des Ausgabeschemas ------------------------------------------------------------------------


@pytest.mark.parametrize("schema", [{"type": "objekt"}, {"type": "object", "required": "a"}, {"type": "object", "minimum": "a"}])
def test_invalid_output_schema_ends_before_any_session(fx: Path, tmp_path: Path, schema: dict[str, Any]):
    binary, log = make_stub_cli(tmp_path)
    code, events, _, out = run_runner([start_line(str(fx), binary, policy_profile="readonly", output_schema=schema)], dict(BASE_ENV),
                                      client_factory=fake_factory([init_message(), result_message(structured_output={})]))
    validate_all(out)
    (t,) = terminals(events)
    assert t["type"] == "error" and t["payload"]["code"] == "output_schema_invalid" and t["payload"]["terminal"] is True
    assert code == 0 and not FakeClient.created
    assert not any(e["type"] in ("session_started", "tool_event", "result") for e in events)
    assert not log.exists() or "SESSION-REQUESTED" not in log.read_text()  # das Binary wurde nie als Sitzung gestartet


# --- Vertrag: Größe und Draft-07 --------------------------------------------------------------------------


def _start(**payload: Any) -> str:
    base = json.loads(start_line("/work/x", "/usr/local/bin/claude"))
    base["payload"].update(payload)
    return json.dumps(base)


def test_output_schema_size_limit_is_measured_on_the_canonical_form():
    contracts.validate("to-runner", _start(output_schema={"type": "object", "description": "a" * 32000}))
    with pytest.raises(ContractError) as info:
        contracts.validate("to-runner", _start(output_schema={"type": "object", "description": "a" * 33000}))
    assert info.value.categories == ("size",)
    # Leerraum in der Zeile zählt nicht.
    spaced = json.dumps(json.loads(_start(output_schema={"type": "object", "description": "a" * 32000})), indent=40)
    contracts.validate("to-runner", spaced)


def test_structured_output_size_limit():
    def result(value: Any) -> str:
        env = json.loads(support.envelope("result", {"outcome": "succeeded", "summary": "x", "structured_output": value}))
        return json.dumps(env)

    contracts.validate("from-runner", result({"text": "a" * 260_000}))
    with pytest.raises(ContractError) as info:
        contracts.validate("from-runner", result({"text": "a" * 263_000}))
    assert info.value.categories == ("size",)


def test_byte_limits_do_not_apply_to_other_message_types():
    env = json.loads(support.envelope("activity", {"kind": "idle", "summary": "x", "structured_output": {"a": "b" * 300000}}))
    with pytest.raises(ContractError) as info:
        contracts.validate("from-runner", json.dumps(env))
    assert info.value.categories == ("unknown_field",)


def test_the_output_schema_helper_matches_the_draft_07_metaschema():
    jsonschema.Draft7Validator.check_schema(SCHEMA)
    with pytest.raises(jsonschema.SchemaError):
        jsonschema.Draft7Validator.check_schema({"type": "objekt"})
