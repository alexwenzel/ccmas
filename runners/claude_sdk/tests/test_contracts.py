"""Vertragstests des Runners gegen die gemeinsamen Schemas und Fixtures.

Go (internal/domain) und Python validieren gegen dieselben Schema-Dateien unter
``contracts/`` und lesen dieselben Fixtures unter ``contracts/fixtures/``. Beide Seiten
müssen jede Fixture identisch akzeptieren beziehungsweise mit derselben Fehlerklasse
abweisen. Namensschema:

    valid/<ziel>--<name>.json
    invalid/<ziel>--<fehlerklasse>--<name>.json

Es findet keine Agentenausführung und kein Netzwerkzugriff statt.
"""

import copy
import json
from pathlib import Path

import pytest

from claude_sdk_runner import contracts
from claude_sdk_runner.contracts import ContractError

REPO_ROOT = Path(__file__).resolve().parents[3]
FIXTURES = REPO_ROOT / "contracts" / "fixtures"

REQUIRED_CLASSES = {
    "version", "id", "path", "model", "effort", "size", "action", "field", "unknown_field",
}


def _files(sub: str) -> list[Path]:
    files = sorted((FIXTURES / sub).iterdir())
    assert files, f"keine Fixtures in {sub}"
    for f in files:
        assert f.is_file() and f.suffix == ".json", f"unerwarteter Eintrag: {f}"
    return files


VALID = _files("valid")
INVALID = _files("invalid")


def test_contracts_dir_is_repo_contracts():
    # Python liest dieselben Dateien wie Go, relativ zum Repo-Root.
    assert contracts.contracts_dir() == REPO_ROOT / "contracts"


@pytest.mark.parametrize("path", VALID, ids=lambda p: f"valid/{p.name}")
def test_contract_fixtures_valid(path: Path):
    parts = path.stem.split("--")
    assert len(parts) == 2, f"{path.name} entspricht nicht <ziel>--<name>.json"
    assert parts[0] in contracts.TARGETS, f"unbekanntes Ziel {parts[0]}"
    contracts.validate(parts[0], path.read_bytes())


@pytest.mark.parametrize("path", INVALID, ids=lambda p: f"invalid/{p.name}")
def test_contract_fixtures_invalid(path: Path):
    parts = path.stem.split("--")
    assert len(parts) == 3, f"{path.name} entspricht nicht <ziel>--<klasse>--<name>.json"
    target, expected = parts[0], parts[1]
    assert target in contracts.TARGETS, f"unbekanntes Ziel {target}"
    assert expected in contracts.CATEGORIES, f"unbekannte Fehlerklasse {expected}"
    with pytest.raises(ContractError) as info:
        contracts.validate(target, path.read_bytes())
    assert info.value.categories == (expected,), str(info.value)
    assert info.value.category == expected


def test_contract_fixtures():
    """Sammeltest: gleiche Fixtures wie Go, vollständige Abdeckung der geforderten Klassen."""
    seen = set()
    for path in VALID:
        target = path.stem.split("--")[0]
        contracts.validate(target, path.read_bytes())
    for path in INVALID:
        target, expected, _ = path.stem.split("--")
        with pytest.raises(ContractError) as info:
            contracts.validate(target, path.read_bytes())
        assert info.value.categories == (expected,), f"{path.name}: {info.value}"
        seen.add(expected)
    assert REQUIRED_CLASSES <= seen, f"nicht belegte Fehlerklassen: {REQUIRED_CLASSES - seen}"


# ---- Hilfen -----------------------------------------------------------------


def base_event(typ="activity", payload=None):
    return {
        "protocol_version": "1",
        "project_id": "proj-1",
        "execution_id": "exec-1",
        "attempt": 1,
        "correlation_id": "corr-1",
        "type": typ,
        "timestamp": "2026-11-10T12:00:00Z",
        "payload": payload if payload is not None else {"kind": "idle", "summary": "wartet"},
    }


def check(target, value, expected):
    raw = value if isinstance(value, (bytes, str)) else json.dumps(value)
    with pytest.raises(ContractError) as info:
        contracts.validate(target, raw)
    assert info.value.categories == (expected,), str(info.value)


def test_base_event_is_valid():
    contracts.validate("from-runner", json.dumps(base_event()))


# ---- Umschlag ---------------------------------------------------------------

MUTATIONS = [
    ("falsche Version", lambda m: m.update(protocol_version="2"), "version"),
    ("Version als Zahl", lambda m: m.update(protocol_version=1), "version"),
    ("Version fehlt", lambda m: m.pop("protocol_version"), "field"),
    ("execution_id fehlt", lambda m: m.pop("execution_id"), "field"),
    ("execution_id leer", lambda m: m.update(execution_id=""), "id"),
    ("execution_id mit Pfad", lambda m: m.update(execution_id="../etc/passwd"), "id"),
    ("execution_id mit Zeilenumbruch", lambda m: m.update(execution_id="exec-1\n"), "id"),
    ("execution_id zu lang", lambda m: m.update(execution_id="a" * 129), "id"),
    ("execution_id Zahl", lambda m: m.update(execution_id=7), "id"),
    ("unbekanntes Feld", lambda m: m.update(extra=True), "unknown_field"),
    ("attempt 0", lambda m: m.update(attempt=0), "value"),
    ("unbekannter Typ", lambda m: m.update(type="teleport"), "message_type"),
    ("Typ der falschen Richtung", lambda m: m.update(type="start"), "message_type"),
    ("Zeitstempel ohne Zone", lambda m: m.update(timestamp="2026-11-10T12:00:00"), "timestamp"),
    ("Payload kein Objekt", lambda m: m.update(payload=[]), "value"),
    ("unbekanntes Payload-Feld", lambda m: m.update(payload={"kind": "idle", "summary": "x", "extra": 1}), "unknown_field"),
    ("Pflichtfeld im Payload fehlt", lambda m: m.update(payload={"kind": "idle"}), "field"),
]


@pytest.mark.parametrize("name,mutate,expected", MUTATIONS, ids=[m[0] for m in MUTATIONS])
def test_reject_invalid_envelope(name, mutate, expected):
    message = base_event()
    mutate(message)
    check("from-runner", message, expected)


def test_direction_go_to_python_rejects_runner_types():
    check("to-runner", base_event(), "message_type")


def test_size_limit_is_exact_and_read_from_schema():
    assert contracts.MAX_MESSAGE_BYTES == 1 << 20
    base = json.dumps(base_event(), separators=(",", ":")).encode()
    exact = base + b" " * (contracts.MAX_MESSAGE_BYTES - len(base))
    assert len(exact) == contracts.MAX_MESSAGE_BYTES
    contracts.validate("from-runner", exact)
    check("from-runner", exact + b" ", "size")

    huge = base_event(payload={"kind": "assistant_text", "summary": "x" * contracts.MAX_MESSAGE_BYTES})
    check("from-runner", huge, "size")


def test_strict_parsing():
    good = json.dumps(base_event(), separators=(",", ":"))
    check("from-runner", "not json", "syntax")
    check("from-runner", "", "syntax")
    check("from-runner", "[]", "syntax")
    check("from-runner", good + good, "syntax")
    check("from-runner", '{"protocol_version":"1","protocol_version":"1"}', "syntax")
    check("from-runner", good.replace('"kind":"idle"', '"kind":"idle","kind":"idle"'), "syntax")
    check("from-runner", good.replace('"attempt":1', '"attempt":NaN'), "syntax")
    check("from-runner", good.encode().replace(b"wartet", b"wart\xffet"), "syntax")
    check("from-runner", b"\xef\xbb\xbf" + good.encode(), "syntax")
    check("from-runner", good.replace('"attempt":1', '"attempt":1.0'), "value")
    check("from-runner", good.replace('"attempt":1', '"attempt":1e0'), "value")
    contracts.validate("from-runner", good + "\n  ")


def test_deep_nesting_is_a_syntax_error_not_a_crash():
    deep = "[" * 100_000 + "]" * 100_000
    check("from-runner", deep, "syntax")


def test_unknown_target_is_rejected():
    with pytest.raises(KeyError):
        contracts.validate("gibt-es-nicht", "{}")


def test_error_exposes_location():
    message = base_event()
    message["execution_id"] = ""
    with pytest.raises(ContractError) as info:
        contracts.validate("from-runner", json.dumps(message))
    assert "/execution_id" in str(info.value)


def test_decision_actions_match_the_agreed_set():
    agreed = {
        "plan_extend", "concept_work", "implementation", "review",
        "ask_question", "request_integration", "wait", "request_completion",
    }
    schema = json.loads((contracts.contracts_dir() / "decision-v1.schema.json").read_text())
    assert set(schema["$defs"]["decision"]["properties"]["action"]["enum"]) == agreed

    decision = {
        "schema_version": 1, "decision_id": "dec-1", "project_id": "proj-1", "state_revision": 7,
        "target": {"kind": "work_order", "id": "wo-1"}, "action": "wait", "preconditions": [],
        "rationale": "Kurz.",
    }
    for action in sorted(agreed - {"implementation", "concept_work", "review", "ask_question"}):
        d = copy.deepcopy(decision)
        d["action"] = action
        contracts.validate("decision", json.dumps(d))
    for bad in ["", "deploy", "merge", "PLAN_EXTEND", "wait ", "implement"]:
        d = copy.deepcopy(decision)
        d["action"] = bad
        check("decision", d, "action")


def test_timestamp_pattern_matches_calendar():
    """Das Muster des Schemas lässt genau die gültigen Kalenderdaten zu (inkl. Schaltjahre)."""
    import datetime
    import re

    common = json.loads((contracts.contracts_dir() / "common-v1.schema.json").read_text())
    pattern = re.compile(common["$defs"]["timestamp"]["pattern"])
    years = [1000, 1100, 1200, 1600, 1700, 1900, 2000, 2100, 2400, 9996, 9999, *range(1990, 2111)]
    for y in years:
        for m in range(1, 13):
            for d in range(1, 32):
                try:
                    datetime.date(y, m, d)
                    valid = True
                except ValueError:
                    valid = False
                ts = f"{y:04d}-{m:02d}-{d:02d}T00:00:00Z"
                assert bool(pattern.search(ts)) == valid, ts


def test_error_classes_are_declared_in_the_schema():
    declared = set(contracts.CATEGORIES)
    assert {"version", "id", "path", "model", "effort", "size", "action", "field", "unknown_field"} <= declared
    # Jede Fixture-Klasse und jede x-error-class der Schemas ist eine deklarierte Klasse.
    for schema_file in contracts.contracts_dir().glob("*.schema.json"):
        for annotation in _annotations(json.loads(schema_file.read_text())):
            assert annotation in declared, f"{schema_file.name}: unbekannte x-error-class {annotation}"


def _annotations(node):
    if isinstance(node, dict):
        for key, value in node.items():
            if key == "x-error-class":
                yield value
            else:
                yield from _annotations(value)
    elif isinstance(node, list):
        for item in node:
            yield from _annotations(item)


# ---- AgentProfile: angefordert, Fallback, Herkunft (seit CC-46, v1-kompatibel) ------------


def load_fixture(sub, name):
    return json.loads((FIXTURES / sub / name).read_text(encoding="utf-8"))


def test_agent_profile_without_new_fields_stays_valid():
    # Jedes bisher gültige Profil bleibt gültig: die vier Felder sind optional.
    profile = load_fixture("valid", "agent-profile--implementer.json")
    for field in ("requested_model", "requested_effort", "fallback", "value_sources"):
        assert field not in profile
    contracts.validate("agent-profile", json.dumps(profile))


def test_agent_profile_cc46_fields_are_accepted():
    for name in ("agent-profile--resolved_with_sources.json", "agent-profile--effort_fallback.json"):
        contracts.validate("agent-profile", json.dumps(load_fixture("valid", name)))


@pytest.mark.parametrize(
    ("label", "mutate", "expected"),
    [
        ("Fallback ohne requested_effort", lambda p: p.pop("requested_effort"), "field"),
        ("Fallback ohne Quelle", lambda p: p["fallback"].pop("source"), "field"),
        ("Fallback-Art unbekannt", lambda p: p["fallback"].update(kind="model_swap"), "value"),
        ("Fallback-Stufe unbekannt", lambda p: p["fallback"].update(to="mittel"), "effort"),
        ("Fallback mit zusätzlichem Feld", lambda p: p["fallback"].update(extra=1), "unknown_field"),
        ("Herkunft unbekannt", lambda p: p["value_sources"].update(effort="default"), "value"),
        ("Herkunft unvollständig", lambda p: p["value_sources"].pop("skill_ids"), "field"),
        ("angefordertes Modell mit Leerzeichen", lambda p: p.update(requested_model="a b"), "model"),
        ("angeforderter Effort unbekannt", lambda p: p.update(requested_effort="ultra"), "effort"),
    ],
)
def test_agent_profile_cc46_field_violations(label, mutate, expected):
    profile = copy.deepcopy(load_fixture("valid", "agent-profile--effort_fallback.json"))
    mutate(profile)
    check("agent-profile", profile, expected)
