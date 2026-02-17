#!/usr/bin/env python3
"""Remove files and directories passed as arguments.

Cross-platform replacement for: rm -rf dir1/ && rm -f file1 file2

Silently ignores paths that do not exist (like rm -f).
Removes directories recursively (like rm -rf).

Usage:
    python scripts/clean.py bin coverage.out
    python scripts/clean.py build/ dist/ *.log

Exit codes:
    0 — all paths removed (or already absent)
    1 — a path existed but could not be removed
"""

import os
import shutil
import sys


def remove(path: str) -> bool:
    """Remove a file or directory tree. Returns True on success."""
    if not os.path.exists(path):
        return True

    try:
        if os.path.isdir(path):
            shutil.rmtree(path)
        else:
            os.remove(path)
        return True
    except OSError as exc:
        print(f"warning: could not remove {path!r}: {exc}", file=sys.stderr)
        return False


def main():
    if len(sys.argv) < 2:
        print("usage: clean.py PATH [PATH ...]", file=sys.stderr)
        sys.exit(1)

    ok = all(remove(p) for p in sys.argv[1:])
    sys.exit(0 if ok else 1)


if __name__ == "__main__":
    main()