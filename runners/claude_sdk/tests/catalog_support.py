"""Test-Katalog und Go-CLI für die Skill-Snapshot-Tests (CC-35). Nur Testcode, nie Produktionscode.

Der Katalog entsteht zur Laufzeit in einem Temp-Verzeichnis im Layout des CCMAS-Softwareordners
(`.claude/skills/manifest.yaml` plus Skill-Ordner). Er wird bewusst nicht als `.claude/skills` ins Repository eingecheckt,
damit Entwicklungssitzungen den Probe-Skill nicht entdecken. Die Hashes im Manifest berechnet das Python-Modul des Runners
(`skill_snapshot`); den Snapshot schreibt die echte Go-CLI (`ccmas skills snapshot`).
"""

from __future__ import annotations

import atexit
import json
import shutil
import subprocess
import tempfile
from pathlib import Path
from typing import Any

from claude_sdk_runner.skill_snapshot import file_sha256, tree_sha256
from support import REPO_ROOT

PROBE_SKILL_ID = "ccmas-skillprobe"
PROBE_MARKER = "CCMAS-PROBE-MARKER-7Q4X-91"
"""Eindeutige Markierung, die nur im Inhalt des Probe-Skills steht (nie im Prompt)."""
PROBE_ROLE = "implementer"

PROBE_SKILL_MD = f"""---
name: {PROBE_SKILL_ID}
description: Probe-Skill des CCMAS-Selbsttests. Nur verwenden, wenn ausdrücklich verlangt, den Skill {PROBE_SKILL_ID} aufzurufen.
---

# Probe

Antworte nach dem Aufruf dieses Skills mit genau der Markierung {PROBE_MARKER} und sonst nichts.
"""

OTHER_SKILL_ID = "ccmas-skillother"
OTHER_SKILL_MD = f"""---
name: {OTHER_SKILL_ID}
description: Zweiter Skill des Test-Katalogs; er gehört nie in den Snapshot der Live-Tests.
---

# Anderer Skill

Antworte mit der Markierung NICHT-FREIGEGEBEN-0000.
"""

_GO: dict[str, Path] = {}


def write_catalog(root: Path, skills: dict[str, str]) -> None:
    """Legt `<root>/.claude/skills/manifest.yaml` und je Skill einen Ordner mit `SKILL.md` an (Rolle `PROBE_ROLE`,
    Kategorie `knowledge`, status `approved`, keine Standardausstattung). Die Rollenregeln `rules/roles.yaml` kommen
    aus dem Repository: `ccmas skills snapshot` lädt seit CC-14 geprüft (`skills.LoadChecked`) und braucht sie."""
    rules = root / "rules"
    rules.mkdir(parents=True)
    shutil.copyfile(REPO_ROOT / "rules" / "roles.yaml", rules / "roles.yaml")
    base = root / ".claude" / "skills"
    entries = []
    for sid, body in skills.items():
        (base / sid).mkdir(parents=True)
        (base / sid / "SKILL.md").write_text(body, encoding="utf-8")
        sha = file_sha256(body.encode("utf-8"))
        tree = tree_sha256([("SKILL.md", sha)])
        entries.append(f"""  - id: {sid}
    version: 1
    status: approved
    category: knowledge
    roles: [{PROBE_ROLE}]
    default_for: []
    external_writes: []
    tree_sha256: {tree}
    files:
      - path: SKILL.md
        sha256: {sha}
    source:
      package: Fachwissen/Claude/Skills/{sid.removeprefix('ccmas-')}.skill
      package_sha256: {'ab' * 32}
      derived: "2026-11-10"
      changes: "Test-Skill, nur zur Laufzeit im Temp-Verzeichnis erzeugt."
""")
    (base / "manifest.yaml").write_text("schema_version: 1\nskills:\n" + "".join(entries), encoding="utf-8")


def go_binary() -> Path:
    """Baut `./cmd/ccmas` einmal je Testprozess in ein Temp-Verzeichnis (Go-Cache wird wiederverwendet)."""
    if "bin" not in _GO:
        out = Path(tempfile.mkdtemp(prefix="ccmas-go-"))
        atexit.register(shutil.rmtree, out, ignore_errors=True)
        subprocess.run(["go", "build", "-o", str(out / "ccmas"), "./cmd/ccmas"], cwd=REPO_ROOT, check=True,
                       capture_output=True, text=True, timeout=300)
        _GO["bin"] = out / "ccmas"
    return _GO["bin"]


def build_go_snapshot(tmp: Path, skills: dict[str, str] | None = None, select: list[str] | None = None,
                      role: str = PROBE_ROLE) -> tuple[Path, dict[str, Any]]:
    """Test-Katalog erzeugen und mit der echten Go-CLI einen Snapshot schreiben. Liefert den Snapshot-Pfad und die
    `snapshot.json`. Standard: Katalog mit Probe-Skill und zweitem Skill, Snapshot nur mit dem Probe-Skill."""
    skills = skills or {PROBE_SKILL_ID: PROBE_SKILL_MD, OTHER_SKILL_ID: OTHER_SKILL_MD}
    select = [PROBE_SKILL_ID] if select is None else select
    root = tmp / "katalog"
    write_catalog(root, skills)
    out = tmp / "snapshots"
    cmd = [str(go_binary()), "skills", "snapshot", "--root", str(root), "--role", role, "--out", str(out), "--json"]
    for sid in select:
        cmd += ["--skill", sid]
    proc = subprocess.run(cmd, capture_output=True, text=True, timeout=60)
    assert proc.returncode == 0, proc.stderr
    path = Path(json.loads(proc.stdout)["path"])
    return path, json.loads((path / "snapshot.json").read_text())
