"""Prüfung eines Skill-Snapshots (CC-35) ohne Konto und ohne Netzwerk.

Das gemeinsame Beispiel `contracts/fixtures/skill-snapshots/example/` hat Go erzeugt und Byte für Byte festgeschrieben
(`TestExampleSnapshotFixture`). Der Python-Runner muss dieselben Hashes nachrechnen; Manipulationen entstehen nur in
Temp-Kopien. Dazu: ein Snapshot, den die echte Go-CLI schreibt (übersprungen, wenn `go` fehlt).
"""

from __future__ import annotations

import hashlib
import json
import os
import shutil
import subprocess
from pathlib import Path
from typing import Any, Callable

import pytest

from claude_sdk_runner import skill_snapshot as ss
from claude_sdk_runner.diagnostics import RuntimeDiagnostic
from support import (
    EXAMPLE_ROLE, EXAMPLE_SKILLS, EXAMPLE_SNAPSHOT, EXAMPLE_SNAPSHOT_ID, REPO_ROOT, copy_example_snapshot, make_writable,
)

KNOW, TRACK = EXAMPLE_SKILLS
SECRET_CONTENT = "GEHEIMER-INHALT-4711"


@pytest.fixture
def snap(tmp_path: Path) -> Path:
    return copy_example_snapshot(tmp_path / "snap")


def _doc(snap: Path) -> dict[str, Any]:
    return json.loads((snap / "snapshot.json").read_text())


def _write_doc(snap: Path, doc: dict[str, Any]) -> None:
    (snap / "snapshot.json").write_text(json.dumps(doc, indent=2, ensure_ascii=False) + "\n")


def _fails(path: Path, code: str) -> str:
    with pytest.raises(RuntimeDiagnostic) as e:
        ss.verify_snapshot_dir(str(path))
    assert e.value.code == code and e.value.kind == "runtime", e.value.message
    return e.value.message


# --- Das gemeinsame Beispiel (Go schreibt, Python rechnet nach) ------------------------------------------------------


def test_python_recomputes_the_values_go_wrote_for_the_shared_example():
    v = ss.verify_snapshot_dir(str(EXAMPLE_SNAPSHOT))
    doc = json.loads((EXAMPLE_SNAPSHOT / "snapshot.json").read_text())
    assert v.snapshot_id == doc["snapshot_id"] == EXAMPLE_SNAPSHOT_ID
    assert v.role == EXAMPLE_ROLE == doc["role"]
    assert v.skill_ids == EXAMPLE_SKILLS
    assert v.qualified_skills == ["ccmas:ccmas-demo-know", "ccmas:ccmas-demo-track"]
    assert v.path == os.path.realpath(EXAMPLE_SNAPSHOT)


def test_hash_definitions_reproduce_the_go_values_independently():
    """Die Erwartungen stammen aus dem von Go geschriebenen Beispiel, nicht aus der Python-Implementierung."""
    doc = json.loads((EXAMPLE_SNAPSHOT / "snapshot.json").read_text())
    for sk in doc["skills"]:
        for f in sk["files"]:
            data = (EXAMPLE_SNAPSHOT / "skills" / sk["id"] / f["path"]).read_bytes()
            assert ss.file_sha256(data) == hashlib.sha256(data).hexdigest() == f["sha256"]
        assert ss.tree_sha256((f["path"], f["sha256"]) for f in sk["files"]) == sk["tree_sha256"]
    assert ss.compute_snapshot_id(doc["role"], ((s["id"], s["version"], s["tree_sha256"]) for s in doc["skills"])) == doc["snapshot_id"]
    # fest verdrahtete Werte aus der Go-Ausgabe
    assert ss.tree_sha256([("SKILL.md", "66bd4472c58a9b7d9f0f85fac4c4bcb7803a65d664d9fc8b6d0f020cd046c871")]) == \
        "dfd49d47945487e5a4b0977f46a56381b9a6636886c58c32480ac87255b35050"


def test_tree_hash_orders_paths_bytewise_not_directory_wise():
    """`SKILL.md` < `a-b.txt` < `a.txt` < `a/x.txt` (so auch im Go-Test und im `shasum`-Beispiel der Doku)."""
    names = ["a/x.txt", "a.txt", "SKILL.md", "a-b.txt", "é.txt"]
    shas = {n: hashlib.sha256(n.encode()).hexdigest() for n in names}
    ordered = ["SKILL.md", "a-b.txt", "a.txt", "a/x.txt", "é.txt"]
    expected = hashlib.sha256("".join(f"{shas[n]}  {n}\n" for n in ordered).encode()).hexdigest()
    assert ss.tree_sha256((n, shas[n]) for n in reversed(names)) == expected


def test_snapshot_id_ignores_the_order_of_the_skills_and_depends_on_the_role():
    a = ("ccmas-a", 1, "a" * 64)
    b = ("ccmas-b", 2, "b" * 64)
    expected = "sha256:" + hashlib.sha256(
        b"ccmas-skill-snapshot v1\nrole concept\nskill ccmas-a 1 " + b"a" * 64 + b"\nskill ccmas-b 2 " + b"b" * 64 + b"\n").hexdigest()
    assert ss.compute_snapshot_id("concept", [b, a]) == expected
    assert ss.compute_snapshot_id("reviewer", [a, b]) != expected


# --- Manipulationen in Temp-Kopien -------------------------------------------------------------------------------------


def _modify(snap: Path) -> None:
    (snap / "skills" / KNOW / "scripts" / "check.py").write_text(SECRET_CONTENT)


def _delete(snap: Path) -> None:
    (snap / "skills" / KNOW / "scripts" / "check.py").unlink()


def _add_file(snap: Path) -> None:
    (snap / "skills" / TRACK / "extra.md").write_text("zusätzlich")


def _add_file_in_new_dir(snap: Path) -> None:
    (snap / "skills" / TRACK / "neu").mkdir()
    (snap / "skills" / TRACK / "neu" / "x.md").write_text("x")


def _add_empty_dir(snap: Path) -> None:
    (snap / "skills" / "ccmas-demo-fremd").mkdir()


def _add_skill_dir_with_skill_md(snap: Path) -> None:
    d = snap / "skills" / "ccmas-demo-heimlich"
    d.mkdir()
    (d / "SKILL.md").write_text("---\nname: ccmas-demo-heimlich\ndescription: nicht freigegeben\n---\n")


def _replace_file_by_symlink(snap: Path) -> None:
    target = snap / "skills" / KNOW / "scripts" / "check.py"
    outside = snap.parent / "draussen.py"
    outside.write_bytes(target.read_bytes())  # gleicher Inhalt: nur der Symlink selbst ist der Befund
    target.unlink()
    target.symlink_to(outside)


def _add_symlink_out(snap: Path) -> None:
    (snap / "skills" / TRACK / "ausbruch").symlink_to("/etc")


def _symlink_skill_dir(snap: Path) -> None:
    real = snap / "skills" / TRACK
    moved = snap.parent / "woanders"
    shutil.move(str(real), str(moved))
    real.symlink_to(moved)


def _fifo(snap: Path) -> None:
    os.mkfifo(snap / "skills" / TRACK / "pipe")


def _tamper_json_file_hash(snap: Path) -> None:
    d = _doc(snap)
    d["skills"][0]["files"][0]["sha256"] = "0" * 64
    _write_doc(snap, d)


def _tamper_json_tree_hash(snap: Path) -> None:
    d = _doc(snap)
    d["skills"][1]["tree_sha256"] = "1" * 64
    _write_doc(snap, d)


def _tamper_json_snapshot_id(snap: Path) -> None:
    d = _doc(snap)
    d["snapshot_id"] = "sha256:" + "2" * 64
    _write_doc(snap, d)


def _tamper_json_role(snap: Path) -> None:
    d = _doc(snap)
    d["role"] = "reviewer"
    _write_doc(snap, d)


def _tamper_json_version(snap: Path) -> None:
    d = _doc(snap)
    d["skills"][0]["version"] = 2
    _write_doc(snap, d)


def _plugin_changed(snap: Path) -> None:
    (snap / ".claude-plugin" / "plugin.json").write_text('{"name":"fremd"}')


def _plugin_missing(snap: Path) -> None:
    shutil.rmtree(snap / ".claude-plugin")


def _skills_dir_missing(snap: Path) -> None:
    shutil.rmtree(snap / "skills")


def _skill_dir_missing(snap: Path) -> None:
    shutil.rmtree(snap / "skills" / TRACK)


def _hooks_added(snap: Path) -> None:
    (snap / "hooks").mkdir()
    (snap / "hooks" / "hooks.json").write_text("{}")


@pytest.mark.parametrize("manipulate,where", [
    (_modify, f"skills/{KNOW}/scripts/check.py (Datei verändert)"),
    (_delete, f"skills/{KNOW}/scripts/check.py (fehlt)"),
    (_add_file, f"skills/{TRACK}/extra.md (zusätzliche Datei)"),
    (_add_file_in_new_dir, f"skills/{TRACK}/neu (zusätzliches Verzeichnis)"),
    (_add_empty_dir, "skills/ccmas-demo-fremd (zusätzliches Verzeichnis)"),
    (_add_skill_dir_with_skill_md, "skills/ccmas-demo-heimlich (zusätzliches Verzeichnis)"),
    (_replace_file_by_symlink, f"skills/{KNOW}/scripts/check.py (Symlink)"),
    (_add_symlink_out, f"skills/{TRACK}/ausbruch (Symlink)"),
    (_symlink_skill_dir, f"skills/{TRACK} (Symlink)"),
    (_fifo, f"skills/{TRACK}/pipe (keine reguläre Datei)"),
    (_tamper_json_file_hash, f"skills/{KNOW}/SKILL.md (Datei verändert)"),
    (_tamper_json_tree_hash, f"tree_sha256 von Skill {TRACK} stimmt nicht"),
    (_tamper_json_snapshot_id, "snapshot_id stimmt nicht"),
    (_tamper_json_role, "snapshot_id stimmt nicht"),
    (_tamper_json_version, "snapshot_id stimmt nicht"),
    (_plugin_changed, ".claude-plugin/plugin.json (Datei verändert)"),
    (_plugin_missing, ".claude-plugin/plugin.json (fehlt)"),
    (_skills_dir_missing, f"skills/{KNOW}/SKILL.md (fehlt)"),
    (_skill_dir_missing, f"skills/{TRACK}/SKILL.md (fehlt)"),
    (_hooks_added, "hooks (zusätzliches Verzeichnis)"),
], ids=lambda x: x.__name__ if callable(x) else None)
def test_every_kind_of_manipulation_is_a_mismatch_naming_skill_and_path(snap: Path, manipulate: Callable[[Path], None], where: str):
    manipulate(snap)
    message = _fails(snap, "skill_snapshot_mismatch")
    assert where in message, message
    assert SECRET_CONTENT not in message  # nie Inhalte
    assert "0" * 64 not in message and "1" * 64 not in message  # und keine Hashwerte aus dem Dokument


def test_a_modified_file_with_an_adapted_file_hash_is_still_caught_by_the_tree_hash(snap: Path):
    f = snap / "skills" / TRACK / "SKILL.md"
    f.write_text(SECRET_CONTENT)
    d = _doc(snap)
    d["skills"][1]["files"][0]["sha256"] = hashlib.sha256(SECRET_CONTENT.encode()).hexdigest()
    _write_doc(snap, d)
    assert f"tree_sha256 von Skill {TRACK} stimmt nicht" in _fails(snap, "skill_snapshot_mismatch")


def test_a_fully_rewritten_consistent_snapshot_is_caught_by_the_id_in_the_order(snap: Path):
    """Passt jemand Datei, Datei-Hash, tree_sha256 und snapshot_id gemeinsam an, ist das Verzeichnis in sich stimmig; der
    Anker ist die `snapshot_id` im Auftrag (Go übergibt sie), gegen die `verify_for_spec` prüft."""
    (snap / "skills" / TRACK / "SKILL.md").write_text(SECRET_CONTENT)
    d = _doc(snap)
    track = d["skills"][1]
    track["files"][0]["sha256"] = ss.file_sha256(SECRET_CONTENT.encode())
    track["tree_sha256"] = ss.tree_sha256((f["path"], f["sha256"]) for f in track["files"])
    d["snapshot_id"] = ss.compute_snapshot_id(d["role"], ((s["id"], s["version"], s["tree_sha256"]) for s in d["skills"]))
    _write_doc(snap, d)
    assert ss.verify_snapshot_dir(str(snap)).snapshot_id == d["snapshot_id"]  # in sich stimmig
    message = _spec_fails(_spec(snap), "skill_snapshot_mismatch")  # Auftrag nennt die ursprüngliche Kennung
    assert "snapshot_id" in message and SECRET_CONTENT not in message


def test_wrong_hex_directory_name_is_a_mismatch(tmp_path: Path):
    wrong = copy_example_snapshot(tmp_path / ("3" * 64))
    assert "passt nicht zur snapshot_id" in _fails(wrong, "skill_snapshot_mismatch")
    right = copy_example_snapshot(tmp_path / EXAMPLE_SNAPSHOT_ID.removeprefix("sha256:"))
    assert ss.verify_snapshot_dir(str(right)).snapshot_id == EXAMPLE_SNAPSHOT_ID


def test_many_problems_are_cut_off_with_a_count(snap: Path):
    for i in range(12):
        (snap / "skills" / TRACK / f"z{i:02d}.md").write_text("x")
    message = _fails(snap, "skill_snapshot_mismatch")
    assert "(und 4 weitere)" in message and message.count("zusätzliche Datei") == 8


# --- Ungültige Dokumente und Verzeichnisse ---------------------------------------------------------------------------


def _set(mutate: Callable[[dict[str, Any]], None]) -> Callable[[Path], None]:
    def apply(snap: Path) -> None:
        d = _doc(snap)
        mutate(d)
        _write_doc(snap, d)
    return apply


def _json_missing(snap: Path) -> None:
    (snap / "snapshot.json").unlink()


def _json_garbage(snap: Path) -> None:
    (snap / "snapshot.json").write_text("{kein json")


def _json_symlink(snap: Path) -> None:
    real = snap.parent / "echt.json"
    (snap / "snapshot.json").rename(real)
    (snap / "snapshot.json").symlink_to(real)


def _json_duplicate_key(snap: Path) -> None:
    raw = (snap / "snapshot.json").read_text()
    (snap / "snapshot.json").write_text(raw.replace('"role": "concept",', '"role": "concept", "role": "reviewer",'))


def _swap_skills(d: dict[str, Any]) -> None:
    d["skills"].reverse()


def _dup_skill(d: dict[str, Any]) -> None:
    d["skills"].append(dict(d["skills"][-1]))


def _drop_skill_md(d: dict[str, Any]) -> None:
    d["skills"][1]["files"] = [{"path": "README.md", "sha256": "4" * 64}]


@pytest.mark.parametrize("manipulate,fragment", [
    (_json_missing, "snapshot.json fehlt"),
    (_json_garbage, "Klasse syntax"),
    (_json_symlink, "snapshot.json ist keine reguläre Datei"),
    (_json_duplicate_key, "Klasse syntax"),
    (_set(lambda d: d.update(extra="x")), "Klasse unknown_field"),
    (_set(lambda d: d.update(schema_version=2)), "Klasse version"),
    (_set(lambda d: d.pop("snapshot_id")), "Klasse field"),
    (_set(lambda d: d["skills"][0]["files"][0].update(path="../draussen.md")), "Klasse path"),
    (_set(lambda d: d["skills"][0]["files"][0].update(path="/etc/passwd")), "Klasse path"),
    (_set(lambda d: d["skills"][0].update(id="../x")), "Klasse id"),
    (_set(_swap_skills), "aufsteigend nach ID"),
    (_set(_dup_skill), "aufsteigend nach ID"),
    (_set(lambda d: d["skills"][0]["files"].reverse()), "aufsteigend nach Pfad"),
    (_set(_drop_skill_md), "SKILL.md fehlt in der Dateiliste"),
    (_set(lambda d: d.update(role="geheimrolle")), "Klasse role"),
], ids=lambda x: x.__name__ if callable(x) and not x.__name__.startswith("apply") else None)
def test_a_broken_document_is_invalid_not_a_mismatch(snap: Path, manipulate: Callable[[Path], None], fragment: str):
    manipulate(snap)
    message = _fails(snap, "skill_snapshot_invalid")
    assert fragment in message, message


def test_missing_directory_and_symlinked_directory_are_invalid(tmp_path: Path, snap: Path):
    assert "fehlt oder ist nicht lesbar" in _fails(tmp_path / "gibt-es-nicht", "skill_snapshot_invalid")
    link = tmp_path / "link"
    link.symlink_to(snap)
    assert "kein Verzeichnis" in _fails(link, "skill_snapshot_invalid")
    plain = tmp_path / "datei"
    plain.write_text("x")
    assert "kein Verzeichnis" in _fails(plain, "skill_snapshot_invalid")


def test_oversized_snapshot_json_is_refused_before_parsing(snap: Path):
    (snap / "snapshot.json").write_text(" " * (1024 * 1024 + 1))
    assert "zu groß" in _fails(snap, "skill_snapshot_invalid")


# --- Snapshot ohne Skills ----------------------------------------------------------------------------------------------


def _empty_snapshot(dest: Path, role: str = "reviewer") -> Path:
    """Snapshot einer Rolle ohne Skills, wie ihn Go schreibt (Layout, Dokument), von Hand mit hashlib berechnet."""
    sid = "sha256:" + hashlib.sha256(f"ccmas-skill-snapshot v1\nrole {role}\n".encode()).hexdigest()
    (dest / ".claude-plugin").mkdir(parents=True)
    (dest / "skills").mkdir()
    (dest / ".claude-plugin" / "plugin.json").write_text('{"name":"ccmas"}')
    (dest / "snapshot.json").write_text(json.dumps({
        "schema_version": 1, "snapshot_id": sid, "role": role, "created_at": "2026-11-10T12:00:00Z",
        "manifest_sha256": "5" * 64, "skills": []}))
    return dest


def test_a_snapshot_without_skills_is_valid_but_needs_its_skills_directory(tmp_path: Path):
    empty = _empty_snapshot(tmp_path / "leer")
    v = ss.verify_snapshot_dir(str(empty))
    assert v.skill_ids == () and v.role == "reviewer" and v.qualified_skills == []
    shutil.rmtree(empty / "skills")
    assert "skills (fehlt)" in _fails(empty, "skill_snapshot_mismatch")


# --- Abgleich mit dem Auftrag ----------------------------------------------------------------------------------------


def _spec(snap: Path | None, **over: Any) -> dict[str, Any]:
    spec: dict[str, Any] = {"role": EXAMPLE_ROLE, "skill_ids": list(EXAMPLE_SKILLS)}
    if snap is not None:
        spec["skill_snapshot"] = {"snapshot_id": EXAMPLE_SNAPSHOT_ID, "path": str(snap)}
    spec.update(over)
    return spec


def _spec_fails(spec: dict[str, Any], code: str) -> str:
    with pytest.raises(RuntimeDiagnostic) as e:
        ss.verify_for_spec(spec)
    assert e.value.code == code and e.value.kind == "runtime"
    return e.value.message


def test_no_skills_and_no_snapshot_is_fine(snap: Path):
    assert ss.verify_for_spec(_spec(None, skill_ids=[])) is None


def test_skill_ids_without_a_snapshot_are_a_terminal_missing_error():
    message = _spec_fails(_spec(None), "skill_snapshot_missing")
    assert "ccmas-demo-know" in message and "skill_snapshot" in message


def test_matching_snapshot_is_accepted_regardless_of_the_order_of_skill_ids(snap: Path):
    v = ss.verify_for_spec(_spec(snap, skill_ids=list(reversed(EXAMPLE_SKILLS))))
    assert v is not None and v.snapshot_id == EXAMPLE_SNAPSHOT_ID and v.path == os.path.realpath(snap)


def test_snapshot_id_in_the_order_must_equal_the_directory(snap: Path):
    spec = _spec(snap)
    spec["skill_snapshot"]["snapshot_id"] = "sha256:" + "6" * 64
    assert "snapshot_id" in _spec_fails(spec, "skill_snapshot_mismatch")


def test_role_in_the_order_must_equal_the_snapshot_role(snap: Path):
    message = _spec_fails(_spec(snap, role="implementer"), "skill_snapshot_mismatch")
    assert "Rolle concept" in message and "Rolle implementer" in message


def test_skill_set_must_equal_the_orders_skill_ids(snap: Path):
    message = _spec_fails(_spec(snap, skill_ids=[KNOW]), "skill_snapshot_mismatch")
    assert "nicht angeforderte Skills: " + TRACK in message
    message = _spec_fails(_spec(snap, skill_ids=[KNOW, TRACK, "ccmas-demo-mehr"]), "skill_snapshot_mismatch")
    assert "im Snapshot fehlen die Skills ccmas-demo-mehr" in message
    # ein Snapshot mit Skills, aber ein Auftrag ohne skill_ids, ist ebenfalls ein Widerspruch
    assert "nicht angeforderte Skills" in _spec_fails(_spec(snap, skill_ids=[]), "skill_snapshot_mismatch")


def test_a_tampered_snapshot_fails_before_the_comparison_with_the_order(snap: Path):
    _modify(snap)
    _spec_fails(_spec(snap, role="implementer"), "skill_snapshot_mismatch")  # Befund des Verzeichnisses, nicht der Rolle


# --- Ein Snapshot, den die echte Go-CLI schreibt -----------------------------------------------------------------------


@pytest.mark.skipif(shutil.which("go") is None, reason="go nicht installiert")
def test_python_verifies_a_snapshot_written_by_the_real_go_cli(tmp_path: Path):
    """Ende zu Ende ohne Konto: Test-Katalog -> `ccmas skills snapshot` (Go) -> Python-Prüfung -> Manipulation."""
    from catalog_support import build_go_snapshot

    path, doc = build_go_snapshot(tmp_path)
    try:
        v = ss.verify_snapshot_dir(str(path))
        assert v.snapshot_id == doc["snapshot_id"] and v.skill_ids == tuple(s["id"] for s in doc["skills"])
        assert os.path.basename(path) == v.snapshot_id.removeprefix("sha256:")
        assert not (Path(path).stat().st_mode & 0o222)  # Go schreibt schreibgeschützt (0555)
        make_writable(Path(path))
        (Path(path) / "skills" / v.skill_ids[0] / "SKILL.md").write_text(SECRET_CONTENT)
        assert "(Datei verändert)" in _fails(path, "skill_snapshot_mismatch")
    finally:
        make_writable(Path(path))
