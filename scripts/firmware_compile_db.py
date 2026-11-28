#!/usr/bin/env python3
"""Merge firmware compile databases into one that clang-tidy can read.

The component and integration tiers build most firmware sources for the
ESP-IDF linux target with the host GCC. Their compile_commands.json files
carry GCC-only flags that clang rejects, and each variant covers a different
subset of sources. This script merges them, keeps only first-party sources
(not managed components, generated Nanopb stubs or test harnesses), drops
the GCC-only flags, and writes one compile_commands.json.

Usage: firmware_compile_db.py OUT_DIR DB_DIR [DB_DIR ...]
"""

import json
import os
import shlex
import sys

# GCC options clang does not accept.
GCC_ONLY = {
    "-fno-tree-switch-conversion",
    "-fstrict-volatile-bitfields",
    "-fno-shrink-wrap",
    "-mlongcalls",
}
SKIP_PARTS = ("/managed_components/", "/test/", "/build/", "/portunus_proto/")


def first_party(path: str) -> bool:
    return "/access_module/" in path and not any(p in path for p in SKIP_PARTS)


def clean(entry: dict) -> dict:
    args = entry.get("arguments") or shlex.split(entry["command"])
    args = [a for a in args if a not in GCC_ONLY]
    return {"directory": entry["directory"], "file": entry["file"], "arguments": args}


def main() -> int:
    if len(sys.argv) < 3:
        print(__doc__, file=sys.stderr)
        return 2
    out_dir, db_dirs = sys.argv[1], sys.argv[2:]
    merged: dict[str, dict] = {}
    for d in db_dirs:
        path = os.path.join(d, "compile_commands.json")
        if not os.path.isfile(path):
            continue
        with open(path, encoding="utf-8") as f:
            for entry in json.load(f):
                src = os.path.realpath(os.path.join(entry["directory"], entry["file"]))
                if first_party(src) and src not in merged:
                    merged[src] = clean(entry)
    if not merged:
        print("ERROR: no first-party sources found; build the component tier first", file=sys.stderr)
        return 1
    os.makedirs(out_dir, exist_ok=True)
    with open(os.path.join(out_dir, "compile_commands.json"), "w", encoding="utf-8") as f:
        json.dump(sorted(merged.values(), key=lambda e: e["file"]), f, indent=1)
    for src in sorted(merged):
        print(src)
    return 0


if __name__ == "__main__":
    sys.exit(main())
