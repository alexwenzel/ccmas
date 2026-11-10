"""Live-Tests der Skill-Einbringung (CC-35): Go schreibt den Snapshot, der Runner lädt genau dessen Skills in die SDK-Sitzung.

Nur mit ``CCMAS_LIVE_TESTS=1`` (echte Läufe mit dem lokalen Binary und der eigenen claude.ai-Anmeldung, verbraucht
Abo-Kontingent, höchstens zwei Sitzungen)::

    CCMAS_LIVE_TESTS=1 uv run --frozen pytest -q -m live -k skill

Mit ``CCMAS_EVIDENCE_DIR=<Verzeichnis>`` entsteht der redigierte Beleg ``live-skill-snapshot.jsonl``. Der Test-Katalog
mit dem Probe-Skill entsteht zur Laufzeit im Temp-Verzeichnis (``catalog_support``), nie unter ``.claude/skills`` im
Repository.
"""

from __future__ import annotations

import json
import os
import re
import shutil
from datetime import date
from pathlib import Path
from typing import Any, Iterator

import claude_agent_sdk
import pytest

from catalog_support import OTHER_SKILL_ID, PROBE_MARKER, PROBE_ROLE, PROBE_SKILL_ID, build_go_snapshot
from support import REAL_CLAUDE, Collector, make_writable, skill_start_line, terminals, validate_all
from test_runtime_smoke import (
    LIVE_MODEL, _cli_version, _init_sources, _live_env, _run_live_runner, redact_evidence,
)

pytestmark = [
    pytest.mark.skipif(shutil.which("go") is None, reason="go nicht installiert (der Snapshot wird mit der echten Go-CLI geschrieben)"),
    pytest.mark.skipif(not os.access(REAL_CLAUDE, os.X_OK), reason="lokales claude-Binary nicht vorhanden"),
]

QUALIFIED = f"ccmas:{PROBE_SKILL_ID}"
EVIDENCE_NAME = "live-skill-snapshot"
_RUNS: list[dict[str, Any]] = []
"""Läufe dieses Testprozesses; jeder Test schreibt den Beleg mit allen bisherigen Läufen neu."""


@pytest.fixture(scope="module")
def go_snapshot(tmp_path_factory: pytest.TempPathFactory) -> Iterator[tuple[Path, dict[str, Any]]]:
    """Snapshot mit nur dem Probe-Skill; der Katalog enthält zusätzlich einen zweiten Skill, der nicht freigegeben wird."""
    base = tmp_path_factory.mktemp("skill-live")
    path, doc = build_go_snapshot(base)
    assert [s["id"] for s in doc["skills"]] == [PROBE_SKILL_ID]
    yield path, doc
    make_writable(path)


def _record(label: str, events: list[dict[str, Any]], replace: dict[str, str], stderr: bytes, extra: dict[str, Any]) -> None:
    """Hängt den Lauf an den Beleg an und schreibt die Datei (redigiert: Pfade des Temp-Baums ersetzt)."""
    counts, _ = _init_sources(stderr)
    _RUNS[:] = [r for r in _RUNS if r["label"] != label]
    _RUNS.append({"label": label, "events": [json.loads(redact_evidence(json.dumps(e, ensure_ascii=False), replace)) for e in events],
                  "init_counts": counts, **extra})
    target = os.environ.get("CCMAS_EVIDENCE_DIR")
    if not target:
        return
    previous = Path(target) / f"{EVIDENCE_NAME}.jsonl"
    if previous.exists():  # frühere Läufe anderer Tests desselben Belegs bleiben erhalten (gleiche Bezeichnung wird ersetzt)
        old = [json.loads(line) for line in previous.read_text(encoding="utf-8").splitlines()[1:]]
        _RUNS[:] = [r for r in old if r["label"] not in {x["label"] for x in _RUNS}] + _RUNS
        _RUNS.sort(key=lambda r: r["label"])
    meta = {
        "evidence": EVIDENCE_NAME, "date": date.today().isoformat(), "sdk_version": claude_agent_sdk.__version__,
        "cli_version": _cli_version(), "model_requested": LIVE_MODEL, "effort": "low", "policy_profile": "readonly",
        "invocation": "python -m claude_sdk_runner als Unterprozess, Allowlist-Umgebung; Snapshot von `ccmas skills snapshot` (Go)",
        "note": "Test-Katalog zur Laufzeit im Temp-Verzeichnis (Probe-Skill, nicht im Repository). Jede Zeile nach der Kopfzeile ist ein "
                "Lauf mit Bezeichnung, Anzahlen aus dem Init-Frame (init_counts) und den Protokollereignissen des Runners.",
        "redaction": "Pfade des temporären Baums ersetzt; Konto-, E-Mail- und Organisationsdaten kommen nicht vor. Skill-Namen: "
                     "der Probe-Skill des Tests und die in der Aufforderung genannten mitgelieferten Skills",
    }
    lines = [json.dumps(meta, ensure_ascii=False)] + [json.dumps(r, ensure_ascii=False) for r in _RUNS]
    Path(target).mkdir(parents=True, exist_ok=True)
    (Path(target) / f"{EVIDENCE_NAME}.jsonl").write_text("\n".join(lines) + "\n", encoding="utf-8")


def _run(go_snapshot: tuple[Path, dict[str, Any]], tmp_path: Path, prompt: str) -> tuple[Path, list[dict[str, Any]], Collector, Any]:
    snap_path, doc = go_snapshot
    fx = tmp_path / "work" / "fixture"
    fx.mkdir(parents=True)
    fx = fx.resolve()
    line = skill_start_line(str(fx), REAL_CLAUDE, snap_path, snapshot_id=doc["snapshot_id"], skill_ids=[PROBE_SKILL_ID], role=PROBE_ROLE,
                            prompt=prompt, model=LIVE_MODEL, policy_profile="readonly", max_runtime_seconds=150)
    proc = _run_live_runner(line, _live_env(), tmp_path, max_turns=6, timeout=240)
    out = Collector()
    out.write(proc.stdout)
    validate_all(out)
    assert proc.returncode == 0, proc.stderr.decode(errors="replace")[-600:]
    return fx, out.events(), out, proc


def _skill_calls(events: list[dict[str, Any]]) -> dict[str, dict[str, Any]]:
    """Aufrufe des Skill-Werkzeugs je `tool_use_id`: Skill-Name (aus `started`) und Phasen."""
    calls: dict[str, dict[str, Any]] = {}
    for e in events:
        if e["type"] != "tool_event" or e["payload"]["tool"] != "Skill":
            continue
        p = e["payload"]
        c = calls.setdefault(p["tool_use_id"], {"skill": None, "phases": [], "failed_message": None})
        c["phases"].append(p["phase"])
        if p["phase"] == "started":
            c["skill"] = p.get("summary")
        if p["phase"] == "failed":
            c["failed_message"] = p.get("summary")
    return calls


@pytest.mark.live
def test_live_skill_snapshot_from_go_is_loaded_and_the_skill_runs(go_snapshot, tmp_path: Path):
    """Ende zu Ende Go -> Snapshot -> Runner -> SDK: genau der freigegebene Skill ist geladen, aufrufbar und liefert seine Markierung."""
    snap_path, doc = go_snapshot
    prompt = (f"Rufe jetzt mit dem Skill-Werkzeug den Skill {QUALIFIED} auf und befolge seine Anweisung. "
              "Gib als Antwort genau die Markierung aus, die der Skill vorgibt.")
    assert PROBE_MARKER not in prompt
    fx, events, out, proc = _run(go_snapshot, tmp_path, prompt)
    started = next(e["payload"] for e in events if e["type"] == "session_started")
    calls = _skill_calls(events)
    sid = started["session_id"]
    print(f"live-skill-snapshot[a] session_id={sid}")
    _record("a_snapshot_skill_runs", events, {str(fx): "<FIXTURE>", str(snap_path): "<SNAPSHOT>", str(snap_path.parent): "<SNAPSHOTS>",
                                               str(tmp_path): "<TMP>"}, proc.stderr, {"session_id": sid, "skill_calls": calls})
    assert started["skill_snapshot_id"] == doc["snapshot_id"] and started["skills"] == [PROBE_SKILL_ID]
    assert [(c["skill"], c["phases"]) for c in calls.values()] == [(QUALIFIED, ["started", "finished"])], calls
    (t,) = terminals(events)
    assert t["type"] == "result" and t["payload"]["outcome"] == "succeeded" and events[-1] is t
    assert PROBE_MARKER in t["payload"]["summary"], t["payload"]["summary"]
    assert PROBE_MARKER not in proc.stderr.decode(errors="replace")


@pytest.mark.live
def test_live_skill_outside_the_snapshot_is_rejected_by_the_allowlist(go_snapshot, tmp_path: Path):
    """Ein nicht gelisteter Skill (mitgeliefert oder im Katalog, aber nicht im Snapshot) ist über das Skill-Werkzeug nicht aufrufbar.

    Versucht das Modell den Aufruf gar nicht, ist die Abweisung nicht belegt: der Test schlägt dann klar fehl und wiederholt nichts.
    """
    snap_path, doc = go_snapshot
    prompt = ("Dies ist ein Test der Skill-Freigabe. Rufe nacheinander mit dem Skill-Werkzeug diese Skills auf, je ein Aufruf, "
              f"auch wenn ein früherer scheitert: erstens code-review, zweitens verify, drittens {OTHER_SKILL_ID}. "
              "Versuche nie, ein Scheitern anders zu lösen. Berichte danach in einem Satz, welche Aufrufe gelungen sind.")
    fx, events, out, proc = _run(go_snapshot, tmp_path, prompt)
    started = next(e["payload"] for e in events if e["type"] == "session_started")
    calls = _skill_calls(events)
    sid = started["session_id"]
    print(f"live-skill-snapshot[b] session_id={sid}")
    _record("b_unlisted_skill_rejected", events, {str(fx): "<FIXTURE>", str(snap_path): "<SNAPSHOT>", str(snap_path.parent): "<SNAPSHOTS>",
                                                   str(tmp_path): "<TMP>"}, proc.stderr, {"session_id": sid, "skill_calls": calls})
    assert started["skills"] == [PROBE_SKILL_ID]
    unlisted = {i: c for i, c in calls.items() if c["skill"] != QUALIFIED}
    assert unlisted, f"nicht belegt: das Modell hat keinen nicht gelisteten Skill über das Skill-Werkzeug aufgerufen ({calls})"
    for i, c in unlisted.items():
        assert c["phases"] == ["started", "failed"], (i, c)  # kein nicht gelisteter Skill lief erfolgreich
    assert any("allowlist" in (c["failed_message"] or "") for c in unlisted.values()), unlisted
    assert "NICHT-FREIGEGEBEN-0000" not in " ".join(out.lines)  # der Inhalt des nicht freigegebenen Skills kam nie an
    (t,) = terminals(events)
    assert t["type"] == "result" and events[-1] is t
