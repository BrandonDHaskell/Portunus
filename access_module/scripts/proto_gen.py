#!/usr/bin/env python3
"""Regenerate protobuf code for the Portunus server (Go) and firmware (Nanopb).

Cross-platform replacement for the shell-based protoc / nanopb_generator
invocations.  Validates that required tools are installed before running,
and reports clear errors when they are not.

Usage:
    python scripts/proto_gen.py              # generate both Go and Nanopb
    python scripts/proto_gen.py --go         # generate Go stubs only
    python scripts/proto_gen.py --nanopb     # generate Nanopb stubs only
    python scripts/proto_gen.py --check      # generate both, then fail if
                                             # output differs from committed

Must be run from the project root (the directory containing proto/).

Exit codes:
    0 — generation succeeded (and --check found no drift)
    1 — generation failed or a required tool is missing
    2 — --check found uncommitted differences
"""

import argparse
import os
import shutil
import subprocess
import sys
from pathlib import Path

# ── Project layout (relative to project root) ──────────────────────────────

PROTO_DIR       = Path("proto")
PROTO_FILE      = PROTO_DIR / "portunus" / "v1" / "portunus.proto"
NANOPB_OPTIONS  = PROTO_DIR / "nanopb" / "portunus.options"

GO_OUT_DIR      = Path("server") / "api"
NANOPB_OUT_DIR  = Path("access_module") / "components" / "proto"


# ── Tool discovery ──────────────────────────────────────────────────────────

def find_tool(name: str) -> str | None:
    """Return the full path to a tool, or None if not found."""
    return shutil.which(name)


def require_tool(name: str, install_hint: str) -> str:
    """Return the tool path or exit with an actionable error message."""
    path = find_tool(name)
    if path is None:
        print(f"error: {name!r} not found on PATH", file=sys.stderr)
        print(f"       install: {install_hint}", file=sys.stderr)
        sys.exit(1)
    return path


def find_nanopb_generator() -> list[str]:
    """Resolve the nanopb generator command.

    Nanopb can be installed in several ways:
      1. Standalone CLI:        nanopb_generator
      2. Python module (pip):   python -m grpc_tools.protoc --nanopb_out=...
      3. ESP-IDF managed component (build-time only, not usable here)

    We prefer (1) if available, fall back to (2).
    """
    if find_tool("nanopb_generator") is not None:
        return ["nanopb_generator"]

    # Try the Python module path.
    python = sys.executable
    result = subprocess.run(
        [python, "-c", "import grpc_tools.protoc"],
        capture_output=True,
    )
    if result.returncode == 0:
        return [python, "-m", "grpc_tools.protoc"]

    print(
        "error: nanopb generator not found.\n"
        "       Install one of:\n"
        "         pip install nanopb           (provides nanopb_generator CLI)\n"
        "         pip install grpcio-tools     (provides grpc_tools.protoc)\n"
        "       Or install nanopb as an ESP-IDF managed component.",
        file=sys.stderr,
    )
    sys.exit(1)


# ── Generation commands ─────────────────────────────────────────────────────

def generate_go(project_root: Path) -> bool:
    """Generate Go protobuf stubs.  Returns True on success."""
    protoc = require_tool(
        "protoc",
        "https://github.com/protocolbuffers/protobuf/releases",
    )
    require_tool(
        "protoc-gen-go",
        "go install google.golang.org/protobuf/cmd/protoc-gen-go@latest",
    )

    proto_dir  = project_root / PROTO_DIR
    proto_file = project_root / PROTO_FILE
    go_out     = project_root / GO_OUT_DIR

    cmd = [
        protoc,
        f"-I{proto_dir}",
        f"--go_out={go_out}",
        "--go_opt=paths=source_relative",
        str(proto_file),
    ]

    print(f"[proto:go] generating → {go_out}")
    result = subprocess.run(cmd, capture_output=True, text=True)

    if result.returncode != 0:
        print(f"error: protoc (Go) failed:\n{result.stderr}", file=sys.stderr)
        return False

    if result.stderr:
        print(result.stderr, end="")

    print("[proto:go] done")
    return True


def generate_nanopb(project_root: Path) -> bool:
    """Generate Nanopb C stubs for ESP32.  Returns True on success."""
    nanopb_cmd = find_nanopb_generator()

    proto_dir      = project_root / PROTO_DIR
    proto_file     = project_root / PROTO_FILE
    nanopb_options = project_root / NANOPB_OPTIONS
    nanopb_out     = project_root / NANOPB_OUT_DIR

    # nanopb_generator CLI and grpc_tools.protoc have different invocations.
    if nanopb_cmd[-1] == "nanopb_generator":
        cmd = [
            *nanopb_cmd,
            f"-I{proto_dir}",
            f"-D{nanopb_out}",
            f"-f{nanopb_options}",
            str(proto_file),
        ]
    else:
        # grpc_tools.protoc path: uses --nanopb_out with embedded options path.
        cmd = [
            *nanopb_cmd,
            f"--proto_path={proto_dir}",
            f"--nanopb_out=--options-path={nanopb_options}:{nanopb_out}",
            str(proto_file),
        ]

    print(f"[proto:nanopb] generating → {nanopb_out}")
    result = subprocess.run(cmd, capture_output=True, text=True)

    if result.returncode != 0:
        print(
            f"error: nanopb generation failed:\n{result.stderr}",
            file=sys.stderr,
        )
        return False

    if result.stderr:
        print(result.stderr, end="")

    print("[proto:nanopb] done")
    return True


def check_drift(project_root: Path) -> bool:
    """Return True if generated files match what's committed in git."""
    git = find_tool("git")
    if git is None:
        print("warning: git not found, skipping drift check", file=sys.stderr)
        return True

    paths = [
        str(project_root / GO_OUT_DIR),
        str(project_root / NANOPB_OUT_DIR),
    ]
    result = subprocess.run(
        [git, "diff", "--exit-code", *paths],
        capture_output=True,
        text=True,
    )

    if result.returncode != 0:
        print(
            "\nerror: generated protobuf code differs from committed files.\n"
            "       Run 'task proto:gen' and commit the changes.\n",
            file=sys.stderr,
        )
        if result.stdout:
            print(result.stdout)
        return False

    print("[proto:check] generated code is up to date")
    return True


# ── CLI ─────────────────────────────────────────────────────────────────────

def main():
    parser = argparse.ArgumentParser(
        description="Generate protobuf code for Portunus (Go + Nanopb).",
    )
    parser.add_argument(
        "--go", action="store_true",
        help="Generate Go stubs only",
    )
    parser.add_argument(
        "--nanopb", action="store_true",
        help="Generate Nanopb C stubs only",
    )
    parser.add_argument(
        "--check", action="store_true",
        help="After generating, fail if output differs from committed files",
    )
    args = parser.parse_args()

    # Default: generate both if neither --go nor --nanopb specified.
    gen_go     = args.go or (not args.go and not args.nanopb)
    gen_nanopb = args.nanopb or (not args.go and not args.nanopb)

    # Resolve project root: the script lives in <root>/scripts/.
    script_dir   = Path(__file__).resolve().parent
    project_root = script_dir.parent

    # Validate that we're in the right place.
    if not (project_root / PROTO_FILE).exists():
        print(
            f"error: cannot find {PROTO_FILE} relative to {project_root}\n"
            f"       run this script from the project root or from scripts/",
            file=sys.stderr,
        )
        sys.exit(1)

    ok = True

    if gen_go:
        ok = generate_go(project_root) and ok

    if gen_nanopb:
        ok = generate_nanopb(project_root) and ok

    if not ok:
        sys.exit(1)

    if args.check:
        if not check_drift(project_root):
            sys.exit(2)


if __name__ == "__main__":
    main()