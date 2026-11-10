"""`python -m claude_sdk_runner`: Protokoll auf stdin/stdout, Logs auf stderr."""

import os
import sys

from .entrypoint import main


def _run() -> int:
    out = sys.stdout.buffer
    sys.stdout = sys.stderr  # versehentliche print()-Ausgaben dürfen das Protokoll nie stören
    code = main(sys.stdin.buffer, out, sys.stderr)
    out.flush()
    sys.stderr.flush()
    return code


if __name__ == "__main__":
    # Hartes Ende: der stdin-Lesethread darf das Beenden nicht blockieren.
    os._exit(_run())
