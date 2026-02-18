#!/usr/bin/env python3
"""Check that all Go files in the current directory are gofmt-formatted.

Cross-platform replacement for: test -z "$(gofmt -l .)"

Usage:
    python scripts/check_fmt.py          # check current directory
    python scripts/check_fmt.py src/     # check specific directory

Exit codes:
    0 — all files formatted
    1 — unformatted files found
    2 — gofmt not found or other error
"""

import subprocess
import sys


def main():
    target = sys.argv[1] if len(sys.argv) > 1 else "."

    try:
        result = subprocess.run(
            ["gofmt", "-l", target],
            capture_output=True,
            text=True,
        )
    except FileNotFoundError:
        print("error: gofmt not found on PATH", file=sys.stderr)
        print("Install Go from https://go.dev/dl/", file=sys.stderr)
        sys.exit(2)

    if result.returncode != 0:
        print("error: gofmt failed:", result.stderr.strip(), file=sys.stderr)
        sys.exit(2)

    unformatted = result.stdout.strip()
    if unformatted:
        print("The following files are not gofmt-formatted:\n")
        print(unformatted)
        print("\nRun 'task fmt:server:fix' to auto-format.")
        sys.exit(1)


if __name__ == "__main__":
    main()