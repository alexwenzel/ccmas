#!/usr/bin/env python3
"""Prüft vor einem Release, dass plugin.json und CHANGELOG.md zusammenpassen."""
import json
import re
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
TAG_PREFIX = "ccmas--v"


def parse(version: str) -> tuple[int, int, int]:
    match = re.fullmatch(r"(\d+)\.(\d+)\.(\d+)", version)
    if not match:
        raise ValueError(f"keine Version X.Y.Z: {version}")
    return tuple(int(part) for part in match.groups())


def latest_tag() -> str | None:
    out = subprocess.run(
        ["git", "tag", "--list", f"{TAG_PREFIX}*"], cwd=ROOT, check=True, capture_output=True, text=True
    ).stdout.split()
    versions = []
    for tag in out:
        try:
            versions.append(parse(tag.removeprefix(TAG_PREFIX)))
        except ValueError:
            continue
    return ".".join(map(str, max(versions))) if versions else None


def main() -> int:
    errors = []
    plugin = json.loads((ROOT / "claude-plugin/.claude-plugin/plugin.json").read_text(encoding="utf-8"))
    version = plugin["version"]
    try:
        current = parse(version)
    except ValueError as error:
        print(error)
        return 1

    last = latest_tag()
    if last and current <= parse(last):
        errors.append(f"Version {version} ist nicht größer als der letzte Tag {TAG_PREFIX}{last}")

    changelog = (ROOT / "CHANGELOG.md").read_text(encoding="utf-8")
    if not re.search(rf"^## \[{re.escape(version)}\] – \d{{4}}-\d{{2}}-\d{{2}}\s*$", changelog, re.M):
        errors.append(f"CHANGELOG.md hat keinen Abschnitt „## [{version}] – JJJJ-MM-TT“")

    unreleased = re.search(r"^## \[Unveröffentlicht\]\s*\n(.*?)(?=^## \[|\Z)", changelog, re.M | re.S)
    if not unreleased:
        errors.append("CHANGELOG.md hat keinen Abschnitt „## [Unveröffentlicht]“")
    elif re.search(r"^\s*[-*] ", unreleased.group(1), re.M):
        errors.append("„Unveröffentlicht“ im CHANGELOG ist nicht leer")

    for error in errors:
        print(error)
    print(f"Version {version}, letzter Tag: {TAG_PREFIX + last if last else 'keiner'}, {len(errors)} Fehler")
    return 1 if errors else 0


if __name__ == "__main__":
    sys.exit(main())
