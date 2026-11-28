#!/usr/bin/env python3
"""Regenerate protobuf code for the Portunus server (Go) and firmware (Nanopb).

Usage:
    python scripts/proto_gen.py              # generate both Go and Nanopb
    python scripts/proto_gen.py --go         # generate Go stubs only
    python scripts/proto_gen.py --nanopb     # generate Nanopb stubs only
    python scripts/proto_gen.py --check      # generate both, then fail if
                                             # output differs from committed

Tool versions are pinned so local runs and CI produce identical output:
  - protoc-gen-go / protoc-gen-go-grpc: `tool` directives in server/go.mod,
    built from the module here (nothing needs to be on PATH), with the Go
    toolchain go.mod names. The plugins gofmt their output with the go/printer
    of whichever Go built them, and its comment layout changes between
    releases (1.27 differs from 1.25), so the toolchain is part of the pin.
  - protoc: must match the version recorded in the committed Go stubs.
  - nanopb: the version CI installs (.github/workflows/ci.yml).

Exit codes:
    0  generation succeeded (and --check found no drift)
    1  generation failed or a required tool is missing or the wrong version
    2  --check found differences from the committed files
"""

import argparse
import os
import re
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

PROTO_DIR = Path("proto")
PROTO_FILE = PROTO_DIR / "portunus" / "v1" / "portunus.proto"
NANOPB_OPTIONS = PROTO_DIR / "nanopb" / "portunus.options"

SERVER_DIR = Path("server")
GO_OUT_DIR = SERVER_DIR / "api"
GO_STUB = GO_OUT_DIR / "portunus" / "v1" / "portunus.pb.go"
# Both generators mirror the proto package path (portunus/v1/) under this dir.
NANOPB_OUT_DIR = Path("access_module") / "components" / "portunus_proto"

GO_PLUGINS = {
    "protoc-gen-go": "google.golang.org/protobuf/cmd/protoc-gen-go",
    "protoc-gen-go-grpc": "google.golang.org/grpc/cmd/protoc-gen-go-grpc",
}


def fail(msg: str) -> None:
    print(f"error: {msg}", file=sys.stderr)
    sys.exit(1)


def require_tool(name: str, install_hint: str) -> str:
    path = shutil.which(name)
    if path is None:
        fail(f"{name!r} not found on PATH\n       install: {install_hint}")
    return path


def run(cmd: list[str], what: str, cwd: Path | None = None,
        env: dict[str, str] | None = None) -> bool:
    result = subprocess.run(cmd, capture_output=True, text=True, cwd=cwd, env=env)
    if result.returncode != 0:
        print(f"error: {what} failed:\n{result.stderr}", file=sys.stderr)
        return False
    if result.stderr:
        print(result.stderr, end="")
    return True


def check_protoc_version(protoc: str, project_root: Path) -> None:
    """Fail early when protoc differs from the version in the committed stubs.

    protoc-gen-go writes the protoc version into every generated file, so a
    mismatch shows up as drift on every line header rather than a clear error.
    """
    stub = project_root / GO_STUB
    if not stub.exists():
        return
    m = re.search(r"^//\s+protoc\s+v(\S+)$", stub.read_text(), re.MULTILINE)
    if m is None:
        return
    want = m.group(1)
    out = subprocess.run([protoc, "--version"], capture_output=True, text=True)
    have = out.stdout.strip().removeprefix("libprotoc ")
    if have != want:
        fail(
            f"protoc {have} does not match {want}, the version that generated "
            f"the committed stubs.\n       Install protoc {want}, or regenerate "
            "deliberately and update CI to the same version."
        )


def go_toolchain(project_root: Path) -> str:
    """The toolchain go.mod names (toolchain directive, else go directive)."""
    text = (project_root / SERVER_DIR / "go.mod").read_text()
    m = re.search(r"^toolchain\s+(go\S+)$", text, re.MULTILINE)
    if m:
        return m.group(1)
    m = re.search(r"^go\s+(\S+)$", text, re.MULTILINE)
    if m is None:
        fail("no go directive in server/go.mod")
    return f"go{m.group(1)}"


def generate_go(project_root: Path, plugin_dir: Path) -> bool:
    protoc = require_tool("protoc", "https://github.com/protocolbuffers/protobuf/releases")
    check_protoc_version(protoc, project_root)
    require_tool("go", "https://go.dev/dl/")

    toolchain = go_toolchain(project_root)
    env = {**os.environ, "GOTOOLCHAIN": toolchain}
    plugin_args = []
    for name, pkg in GO_PLUGINS.items():
        exe = plugin_dir / name
        if not run(["go", "build", "-o", str(exe), pkg], f"building {name} with {toolchain}",
                   cwd=project_root / SERVER_DIR, env=env):
            return False
        plugin_args.append(f"--plugin={name}={exe}")

    go_out = project_root / GO_OUT_DIR
    cmd = [
        protoc,
        *plugin_args,
        f"-I{project_root / PROTO_DIR}",
        f"--go_out={go_out}",
        "--go_opt=paths=source_relative",
        f"--go-grpc_out={go_out}",
        "--go-grpc_opt=paths=source_relative",
        str(project_root / PROTO_FILE),
    ]
    print(f"[proto:go] generating → {go_out}")
    if not run(cmd, "protoc (Go)"):
        return False
    print("[proto:go] done")
    return True


def find_nanopb_generator() -> str:
    # The interpreter's own bin/ first: a venv install (e.g. the ESP-IDF venv)
    # is often not on PATH.
    beside = Path(sys.executable).parent / "nanopb_generator"
    if beside.exists():
        return str(beside)
    on_path = shutil.which("nanopb_generator")
    if on_path is None:
        fail("nanopb_generator not found\n"
             "       install: pip install nanopb==<version pinned in .github/workflows/ci.yml>")
    return on_path


def generate_nanopb(project_root: Path) -> bool:
    nanopb_out = project_root / NANOPB_OUT_DIR
    nanopb_out.mkdir(parents=True, exist_ok=True)
    cmd = [
        find_nanopb_generator(),
        f"-I{project_root / PROTO_DIR}",
        f"-D{nanopb_out}",
        f"-f{project_root / NANOPB_OPTIONS}",
        str(project_root / PROTO_FILE),
    ]
    print(f"[proto:nanopb] generating → {nanopb_out}")
    if not run(cmd, "nanopb generation"):
        return False
    print("[proto:nanopb] done")
    return True


def check_drift(project_root: Path) -> bool:
    """Return True if generated files match what's committed in git.

    git status (not git diff) so a newly generated, untracked file counts as
    drift too.
    """
    git = require_tool("git", "apt install git")
    paths = [str(project_root / GO_OUT_DIR), str(project_root / NANOPB_OUT_DIR)]
    result = subprocess.run(
        [git, "status", "--porcelain", "--untracked-files=all", "--", *paths],
        capture_output=True, text=True, cwd=project_root,
    )
    if result.returncode != 0:
        fail(f"git status failed:\n{result.stderr}")
    if result.stdout.strip():
        print(
            "\nerror: generated protobuf code differs from committed files.\n"
            "       Run 'task proto:gen' and commit the changes.\n",
            file=sys.stderr,
        )
        print(result.stdout)
        subprocess.run([git, "--no-pager", "diff", "--", *paths], cwd=project_root)
        return False
    print("[proto:check] generated code is up to date")
    return True


def main() -> None:
    parser = argparse.ArgumentParser(
        description="Generate protobuf code for Portunus (Go + Nanopb).",
    )
    parser.add_argument("--go", action="store_true", help="Generate Go stubs only")
    parser.add_argument("--nanopb", action="store_true", help="Generate Nanopb C stubs only")
    parser.add_argument("--check", action="store_true",
                        help="After generating, fail if output differs from committed files")
    args = parser.parse_args()

    both = not args.go and not args.nanopb
    project_root = Path(__file__).resolve().parent.parent
    if not (project_root / PROTO_FILE).exists():
        fail(f"cannot find {PROTO_FILE} under {project_root}")

    ok = True
    if args.go or both:
        with tempfile.TemporaryDirectory() as plugin_dir:
            ok = generate_go(project_root, Path(plugin_dir)) and ok
    if args.nanopb or both:
        ok = generate_nanopb(project_root) and ok
    if not ok:
        sys.exit(1)
    if args.check and not check_drift(project_root):
        sys.exit(2)


if __name__ == "__main__":
    main()
