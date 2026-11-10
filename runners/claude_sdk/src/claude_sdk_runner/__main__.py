"""`python -m claude_sdk_runner`: Protokoll auf stdin/stdout, Logs auf stderr."""

import argparse
import os
import sys
from typing import Sequence

from .entrypoint import main

MAX_TURNS_LIMIT = 200


def _max_turns(text: str) -> int:
    try:
        value = int(text)
    except ValueError:
        raise argparse.ArgumentTypeError("ganze Zahl erwartet") from None
    if not 1 <= value <= MAX_TURNS_LIMIT:
        raise argparse.ArgumentTypeError(f"muss zwischen 1 und {MAX_TURNS_LIMIT} liegen")
    return value


def parse_args(argv: Sequence[str]) -> argparse.Namespace:
    """Ungültige Argumente beenden den Prozess mit Exit-Code 2, ohne etwas auf stdout zu schreiben."""
    parser = argparse.ArgumentParser(prog="python -m claude_sdk_runner", description=__doc__)
    parser.add_argument("--max-turns", type=_max_turns, metavar="N", default=None,
                        help=f"höchstens N Runden je Sitzung (1 bis {MAX_TURNS_LIMIT}); begrenzt Kosten, zum Beispiel in Live-Tests")
    return parser.parse_args(list(argv))


def _run(argv: Sequence[str]) -> int:
    args = parse_args(argv)
    out = sys.stdout.buffer
    sys.stdout = sys.stderr  # versehentliche print()-Ausgaben dürfen das Protokoll nie stören
    code = main(sys.stdin.buffer, out, sys.stderr, max_turns=args.max_turns)
    out.flush()
    sys.stderr.flush()
    return code


if __name__ == "__main__":
    # Hartes Ende: der stdin-Lesethread darf das Beenden nicht blockieren.
    os._exit(_run(sys.argv[1:]))
