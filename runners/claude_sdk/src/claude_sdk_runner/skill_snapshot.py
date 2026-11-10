"""Prüfung eines Skill-Snapshots vor dem Sitzungsstart (CC-35).

Der Go-Dienst schreibt je Ausführung einen unveränderlichen Snapshot im Layout eines Claude-Plugins
(`docs/architecture/skill-migration.md`)::

    <snapshot>/snapshot.json                 Vertrag skill-snapshot-v1
    <snapshot>/.claude-plugin/plugin.json    {"name":"ccmas"}
    <snapshot>/skills/<id>/…                 alle Dateien des Skills

Dieses Modul rechnet vor jedem Start nach, ob das Verzeichnis genau diesem Dokument entspricht: alle Datei-Hashes,
`tree_sha256` je Skill und `snapshot_id` nach den dort festgelegten Definitionen (dieselben wie in Go,
`internal/skills`), dazu fehlende oder zusätzliche Dateien und Verzeichnisse, Symlinks und Sonderdateien. Es liest nur
und startet nichts; es gibt weder Dateiinhalte noch Hashwerte in Meldungen aus, nur Skill-ID und den Pfad innerhalb des
Snapshots.

Fehlerarten (alle terminal, `RuntimeDiagnostic`):

- `skill_snapshot_missing`: der Auftrag nennt `skill_ids`, aber keinen `skill_snapshot`.
- `skill_snapshot_invalid`: das Verzeichnis oder `snapshot.json` fehlt, ist nicht lesbar oder verletzt den Vertrag.
- `skill_snapshot_mismatch`: Verzeichnis und Dokument stimmen nicht überein (Hash, fehlende, zusätzliche Dateien,
  Symlinks), oder der Snapshot passt nicht zum Auftrag (Kennung, Rolle, Skill-IDs).

Grenze: Zwischen dieser Prüfung und dem Lesen durch die CLI liegt Zeit. Der Schutz davor ist das Schreibschutz-Layout
(Dateien 0444, Verzeichnisse 0555) des Go-Dienstes; die Prüfung wird nicht wiederholt.
"""

from __future__ import annotations

import hashlib
import os
import re
import stat
from dataclasses import dataclass
from pathlib import Path
from typing import Any, Iterable

from . import contracts
from .diagnostics import (
    SKILL_SNAPSHOT_INVALID,
    SKILL_SNAPSHOT_MISMATCH,
    SKILL_SNAPSHOT_MISSING,
    RuntimeDiagnostic,
    fit,
)

PLUGIN_NAME = "ccmas"
"""Name des Plugins im Snapshot (`.claude-plugin/plugin.json`); Skills heißen in der Sitzung `ccmas:<id>`."""

SNAPSHOT_FILE = "snapshot.json"
PLUGIN_FILE = ".claude-plugin/plugin.json"
PLUGIN_DATA = b'{"name":"ccmas"}'
SKILLS_DIR = "skills"
SNAPSHOT_ID_PREFIX = "sha256:"
SNAPSHOT_ID_HEADER = "ccmas-skill-snapshot v1\n"
SKILL_FILE = "SKILL.md"

_HEX_DIR = re.compile(r"^[0-9a-f]{64}$")
_MAX_PROBLEMS_SHOWN = 8


@dataclass(frozen=True)
class VerifiedSnapshot:
    """Ein geprüfter Snapshot. `path` ist der aufgelöste Pfad (`realpath`) des Snapshot-Verzeichnisses."""

    snapshot_id: str
    role: str
    path: str
    skill_ids: tuple[str, ...]
    """Skill-IDs aufsteigend, ohne Plugin-Präfix."""

    @property
    def qualified_skills(self) -> list[str]:
        """Namen, unter denen die CLI die Skills des Plugins führt (`ccmas:<id>`)."""
        return [f"{PLUGIN_NAME}:{i}" for i in self.skill_ids]


# --- Hash-Definitionen (docs/architecture/skill-migration.md, identisch mit internal/skills) ----------------------------


def file_sha256(data: bytes) -> str:
    """SHA-256 der rohen Bytes, hexadezimal in Kleinbuchstaben."""
    return hashlib.sha256(data).hexdigest()


def tree_sha256(files: Iterable[tuple[str, str]]) -> str:
    """`tree_sha256` eines Skills aus (relativer Pfad, Datei-SHA-256): SHA-256 über die Zeilen
    `<datei-sha256>␠␠<pfad>\\n`, bytewise (UTF-8) nach Pfad sortiert (Format von `shasum -a 256`)."""
    h = hashlib.sha256()
    for path, sha in sorted(files, key=lambda f: f[0].encode("utf-8")):
        h.update(f"{sha}  {path}\n".encode("utf-8"))
    return h.hexdigest()


def compute_snapshot_id(role: str, skills: Iterable[tuple[str, int, str]]) -> str:
    """`snapshot_id` aus der Rolle und (Skill-ID, Version, `tree_sha256`): `sha256:` + SHA-256 über den Kopf
    `ccmas-skill-snapshot v1`, `role <rolle>` und je Skill (nach ID sortiert) `skill <id> <version> <tree_sha256>`."""
    h = hashlib.sha256()
    h.update(SNAPSHOT_ID_HEADER.encode("utf-8"))
    h.update(f"role {role}\n".encode("utf-8"))
    for sid, version, tree in sorted(skills, key=lambda s: s[0].encode("utf-8")):
        h.update(f"skill {sid} {version} {tree}\n".encode("utf-8"))
    return SNAPSHOT_ID_PREFIX + h.hexdigest()


# --- Prüfung des Verzeichnisses -----------------------------------------------------------------------------------------


def _invalid(message: str) -> RuntimeDiagnostic:
    return RuntimeDiagnostic(SKILL_SNAPSHOT_INVALID, fit(message, 3900))


def _mismatch(problems: list[str]) -> RuntimeDiagnostic:
    shown = problems[:_MAX_PROBLEMS_SHOWN]
    more = f" (und {len(problems) - len(shown)} weitere)" if len(problems) > len(shown) else ""
    return RuntimeDiagnostic(
        SKILL_SNAPSHOT_MISMATCH,
        fit("Der Skill-Snapshot weicht von seiner snapshot.json ab: " + "; ".join(shown) + more + ".", 3900),
    )


def _read_document(root: str) -> dict[str, Any]:
    """Liest und prüft `snapshot.json` (Vertrag `skill-snapshot`, Sortierung und Eindeutigkeit wie `SkillSnapshot.Check`)."""
    path = os.path.join(root, SNAPSHOT_FILE)
    try:
        st = os.lstat(path)
    except OSError:
        raise _invalid(f"snapshot.json fehlt im Skill-Snapshot (erwartet: {SNAPSHOT_FILE}).") from None
    if not stat.S_ISREG(st.st_mode):
        raise _invalid("snapshot.json ist keine reguläre Datei (Symlinks sind nicht erlaubt).")
    limit = contracts.max_message_bytes()
    if st.st_size > limit:
        raise _invalid(f"snapshot.json ist zu groß ({st.st_size} Bytes, erlaubt höchstens {limit}).")
    try:
        raw = Path(path).read_bytes()
    except OSError:
        raise _invalid("snapshot.json ist nicht lesbar.") from None
    try:
        doc = contracts.validate("skill-snapshot", raw)
    except contracts.ContractError as e:
        raise _invalid(f"snapshot.json verletzt den Vertrag skill-snapshot-v1 (Klasse {e.category}, bei {e.location or '/'}).") from None
    skills = doc["skills"]
    ids = [s["id"] for s in skills]
    if ids != sorted(set(ids), key=lambda i: i.encode("utf-8")):
        raise _invalid("snapshot.json: die Skills müssen aufsteigend nach ID sortiert und eindeutig sein.")
    for sk in skills:
        paths = [f["path"] for f in sk["files"]]
        if paths != sorted(set(paths), key=lambda p: p.encode("utf-8")):
            raise _invalid(f"snapshot.json, Skill {sk['id']}: die Dateien müssen aufsteigend nach Pfad sortiert und eindeutig sein.")
        if SKILL_FILE not in paths:
            raise _invalid(f"snapshot.json, Skill {sk['id']}: {SKILL_FILE} fehlt in der Dateiliste.")
    return doc


def _expected_layout(doc: dict[str, Any]) -> tuple[dict[str, str | None], set[str]]:
    """Erwartete Dateien (relativer Pfad -> SHA-256, `None` = `snapshot.json` selbst) und Verzeichnisse."""
    files: dict[str, str | None] = {SNAPSHOT_FILE: None, PLUGIN_FILE: file_sha256(PLUGIN_DATA)}
    dirs: set[str] = {".claude-plugin", SKILLS_DIR}
    for sk in doc["skills"]:
        dirs.add(f"{SKILLS_DIR}/{sk['id']}")
        for f in sk["files"]:
            rel = f"{SKILLS_DIR}/{sk['id']}/{f['path']}"
            files[rel] = f["sha256"]
            parts = rel.split("/")[:-1]
            for i in range(1, len(parts) + 1):
                dirs.add("/".join(parts[:i]))
    clash = sorted(set(files) & dirs)
    if clash:
        raise _invalid(f"snapshot.json: {clash[0]} ist zugleich Datei und Verzeichnis.")
    return files, dirs


def _walk(root: str, files: dict[str, str | None], dirs: set[str]) -> tuple[list[str], set[str]]:
    """Vergleicht den tatsächlichen Bestand mit dem erwarteten (ohne Symlinks zu folgen).
    Liefert die Befunde und die gefundenen Pfade."""
    problems: list[str] = []
    seen: set[str] = set()

    def walk(rel: str) -> None:
        full = os.path.join(root, *rel.split("/")) if rel else root
        try:
            with os.scandir(full) as it:
                entries = sorted(it, key=lambda e: e.name.encode("utf-8", "surrogateescape"))
        except OSError:
            problems.append(f"{rel or '.'} (Verzeichnis nicht lesbar)")
            return
        for e in entries:
            p = f"{rel}/{e.name}" if rel else e.name
            shown = p.encode("utf-8", "replace").decode("utf-8", "replace")
            try:
                mode = e.stat(follow_symlinks=False).st_mode
            except OSError:
                problems.append(f"{shown} (nicht lesbar)")
                continue
            if stat.S_ISLNK(mode):
                problems.append(f"{shown} (Symlink)")
            elif stat.S_ISDIR(mode):
                if p not in dirs:
                    problems.append(f"{shown} (zusätzliches Verzeichnis)")
                    continue
                seen.add(p)
                walk(p)
            elif stat.S_ISREG(mode):
                if p not in files:
                    problems.append(f"{shown} (zusätzliche Datei)")
                    continue
                seen.add(p)
                want = files[p]
                if want is None:  # snapshot.json: bereits als Vertrag gelesen
                    continue
                try:
                    data = Path(e.path).read_bytes()
                except OSError:
                    problems.append(f"{shown} (nicht lesbar)")
                    continue
                if file_sha256(data) != want:
                    problems.append(f"{shown} (Datei verändert)")
            else:
                problems.append(f"{shown} (keine reguläre Datei)")

    walk("")
    return problems, seen


def verify_snapshot_dir(path: str) -> VerifiedSnapshot:
    """Prüft das Snapshot-Verzeichnis `path` gegen seine `snapshot.json`. Löst `RuntimeDiagnostic` aus
    (`skill_snapshot_invalid` oder `skill_snapshot_mismatch`); bei Erfolg liefert es den geprüften Snapshot."""
    try:
        st = os.lstat(path)
    except OSError:
        raise _invalid("Das Snapshot-Verzeichnis fehlt oder ist nicht lesbar.") from None
    if stat.S_ISLNK(st.st_mode) or not stat.S_ISDIR(st.st_mode):
        raise _invalid("Der Snapshot-Pfad ist kein Verzeichnis (Symlinks sind nicht erlaubt).")
    real = os.path.realpath(path)
    doc = _read_document(real)
    expected_files, expected_dirs = _expected_layout(doc)

    problems: list[str] = []
    for sk in doc["skills"]:
        got = tree_sha256((f["path"], f["sha256"]) for f in sk["files"])
        if got != sk["tree_sha256"]:
            problems.append(f"{SNAPSHOT_FILE}: tree_sha256 von Skill {sk['id']} stimmt nicht")
    want_id = compute_snapshot_id(doc["role"], ((s["id"], s["version"], s["tree_sha256"]) for s in doc["skills"]))
    if want_id != doc["snapshot_id"]:
        problems.append(f"{SNAPSHOT_FILE}: snapshot_id stimmt nicht")
    name = os.path.basename(real)
    if _HEX_DIR.match(name) and name != doc["snapshot_id"].removeprefix(SNAPSHOT_ID_PREFIX):
        problems.append("Verzeichnisname passt nicht zur snapshot_id")

    found, seen = _walk(real, expected_files, expected_dirs)
    problems += found
    for p in sorted((set(expected_files) | expected_dirs) - seen):
        problems.append(f"{p} (fehlt)")
    if problems:
        raise _mismatch(problems)
    return VerifiedSnapshot(doc["snapshot_id"], doc["role"], real, tuple(s["id"] for s in doc["skills"]))


# --- Abgleich mit dem Auftrag ------------------------------------------------------------------------------------------


def verify_for_spec(spec: dict[str, Any]) -> VerifiedSnapshot | None:
    """Vorprüfung für `RunSpec` `spec` (ohne Modellaufruf): `None` ohne `skill_ids` und ohne `skill_snapshot`.

    - `skill_ids` ohne `skill_snapshot` → `skill_snapshot_missing`.
    - mit `skill_snapshot`: Verzeichnis prüfen (`verify_snapshot_dir`), danach Kennung, Rolle und Menge der Skill-IDs gegen
      den Auftrag (`skill_snapshot_mismatch`).
    """
    skill_ids = list(spec.get("skill_ids") or [])
    ref = spec.get("skill_snapshot")
    if ref is None:
        if skill_ids:
            raise RuntimeDiagnostic(
                SKILL_SNAPSHOT_MISSING,
                fit("Der Auftrag nennt skill_ids (" + ", ".join(skill_ids) + "), aber keinen skill_snapshot; "
                    "Skills werden nur aus einem geprüften Snapshot geladen.", 3900),
            )
        return None
    snap = verify_snapshot_dir(ref["path"])
    problems: list[str] = []
    if snap.snapshot_id != ref["snapshot_id"]:
        problems.append("die snapshot_id des Verzeichnisses weicht von der im Auftrag ab")
    if snap.role != spec["role"]:
        problems.append(f"der Snapshot gehört zur Rolle {snap.role}, der Auftrag hat die Rolle {spec['role']}")
    missing = sorted(set(skill_ids) - set(snap.skill_ids))
    extra = sorted(set(snap.skill_ids) - set(skill_ids))
    if missing:
        problems.append("im Snapshot fehlen die Skills " + ", ".join(missing))
    if extra:
        problems.append("der Snapshot enthält nicht angeforderte Skills: " + ", ".join(extra))
    if problems:
        raise RuntimeDiagnostic(
            SKILL_SNAPSHOT_MISMATCH, fit("Der Skill-Snapshot passt nicht zum Auftrag: " + "; ".join(problems) + ".", 3900)
        )
    return snap
