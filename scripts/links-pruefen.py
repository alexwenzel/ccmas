#!/usr/bin/env python3
"""Prüft, dass jeder relative Markdown-Link auf eine vorhandene Datei und Überschrift zeigt."""
import re
import subprocess
import sys
from pathlib import Path
from urllib.parse import unquote

ROOT = Path(__file__).resolve().parent.parent
LINK = re.compile(r"(?<!\!)\[[^\]]*\]\(([^)\s]+)(?:\s+\"[^\"]*\")?\)")
HEADING = re.compile(r"^#{1,6}\s+(.*?)\s*#*\s*$")


def slug(text: str) -> str:
    text = re.sub(r"`([^`]*)`", r"\1", text)
    text = re.sub(r"\[([^\]]*)\]\([^)]*\)", r"\1", text).lower()
    text = re.sub(r"[^\w\- ]", "", text)
    return text.replace(" ", "-")


def anchors(path: Path) -> set[str]:
    found, seen, fence = set(), {}, False
    for line in path.read_text(encoding="utf-8").splitlines():
        if line.lstrip().startswith("```"):
            fence = not fence
        if fence:
            continue
        match = HEADING.match(line)
        if match:
            base = slug(match.group(1))
            n = seen.get(base, 0)
            seen[base] = n + 1
            found.add(base if n == 0 else f"{base}-{n}")
    return found


def markdown_files() -> list[Path]:
    out = subprocess.run(
        ["git", "ls-files", "*.md"], cwd=ROOT, check=True, capture_output=True, text=True
    ).stdout.split()
    return [ROOT / name for name in out]


def main() -> int:
    errors = []
    cache: dict[Path, set[str]] = {}
    for md in markdown_files():
        fence = False
        for number, line in enumerate(md.read_text(encoding="utf-8").splitlines(), 1):
            if line.lstrip().startswith("```"):
                fence = not fence
            if fence:
                continue
            for target in LINK.findall(line):
                if re.match(r"^[a-z][a-z0-9+.-]*:", target) or target.startswith("mailto:"):
                    continue
                name, _, fragment = target.partition("#")
                dest = md if not name else (md.parent / unquote(name)).resolve()
                where = f"{md.relative_to(ROOT)}:{number}"
                if not dest.exists():
                    errors.append(f"{where}: Datei fehlt: {target}")
                elif fragment and dest.suffix == ".md":
                    if dest not in cache:
                        cache[dest] = anchors(dest)
                    if unquote(fragment).lower() not in cache[dest]:
                        errors.append(f"{where}: Überschrift fehlt: {target}")
    for error in errors:
        print(error)
    print(f"{len(errors)} defekte Links")
    return 1 if errors else 0


if __name__ == "__main__":
    sys.exit(main())
