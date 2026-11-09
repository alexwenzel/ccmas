"""Findet Wikilinks in Obsidian-Seiten, deren Zielseite oder Überschrift nicht existiert.

Aufruf im Projektordner des Vaults:  python3 linkcheck.py "Seite A.md" "Ordner/Seite B.md"
Ohne Argumente werden alle .md-Dateien unterhalb des aktuellen Ordners geprüft.

- Links in Code-Blöcken und Inline-Code werden übersprungen (dort stehen oft Beispiele).
- Block-Verweise (#^id) werden nur auf die Seite geprüft.
- Zielüberschriften mit Formatierung (Backticks, Fettdruck) werden als Hinweis gemeldet, weil verlinkte
  Überschriften schlicht bleiben sollen.
- Seiten außerhalb des aktuellen Ordners erscheinen als fehlend; diese Treffer von Hand bewerten.
"""

import pathlib
import re
import sys

root = pathlib.Path(".")
pages = {p.stem: p for p in root.rglob("*.md")}
_heads = {}


def heads(p):
    if p not in _heads:
        _heads[p] = {
            re.sub(r"^#+\s*", "", line).strip()
            for line in p.read_text(encoding="utf-8").splitlines()
            if line.startswith("#")
        }
    return _heads[p]


def code_spans(text):
    """Bereiche von Inline-Code, die nicht innerhalb eines Wikilinks beginnen."""
    spans = []
    for m in re.finditer(r"`[^`\n]*`", text):
        before = text[: m.start()]
        if before.rfind("[[") > before.rfind("]]"):
            continue  # Backticks gehören zu einem Link-Ziel
        spans.append((m.start(), m.end()))
    return spans


files = sys.argv[1:] or [str(p) for p in sorted(pages.values())]
problems = 0
hints = 0
for f in files:
    src = pathlib.Path(f)
    text = re.sub(r"```.*?```", lambda m: " " * len(m.group(0)), src.read_text(encoding="utf-8"), flags=re.S)
    spans = code_spans(text)
    for m in re.finditer(r"\[\[([^\]|]+?)(?:\\?\|[^\]]*)?\]\]", text):
        if any(a <= m.start() < b for a, b in spans):
            continue
        target = m.group(1).replace("\\", "").strip()
        page, _, head = target.partition("#")
        page = page.split("/")[-1] or src.stem
        p = pages.get(page)
        if not p:
            print(f"{f}: Seite fehlt: {target}")
            problems += 1
            continue
        if not head or head.startswith("^"):
            continue
        if head not in heads(p):
            print(f"{f}: Überschrift fehlt: {target}")
            problems += 1
        elif re.search(r"`|\*\*", head):
            print(f"{f}: Hinweis, Überschrift mit Formatierung: {target}")
            hints += 1
print(f"geprüft: {len(files)} Datei(en), {problems} Fehler, {hints} Hinweis(e)")
