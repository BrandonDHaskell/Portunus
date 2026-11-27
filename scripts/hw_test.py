#!/usr/bin/env python3
"""Build, flash and run the on-target hardware tier (chunk 5).

For each variant (active_high: active-high strike, normally-open reed;
active_low: active-low strike, normally-closed reed) this builds
access_module/test/hardware, flashes it to the ESP32-S3, waits for the app's
HW_TESTS_READY line, sends the go byte, and collects output until
HW_TESTS_DONE. Unity's summary line decides the result.

The app uses the firmware's partition table and never writes NVS, so the
board's device config survives. Wiring: access_module/test/hardware/README.md.

Run from the project root with ESP-IDF exported (. ~/esp/esp-idf/export.sh),
using the IDF venv's Python (it provides pyserial):

    python scripts/hw_test.py --port /dev/ttyACM0
    python scripts/hw_test.py --port /dev/ttyACM0 --variant active_low

Exit codes:
    0  every variant ran and Unity reported 0 failures
    1  a test failed, or a build, flash or serial step failed
    2  bad arguments or environment
"""

import argparse
import re
import subprocess
import sys
import time
from pathlib import Path

try:
    import serial  # pyserial, shipped in the ESP-IDF venv
except ImportError:
    print("pyserial not found: run with the ESP-IDF venv Python", file=sys.stderr)
    sys.exit(2)

APP = Path("access_module/test/hardware")
LOG_DIR = APP / "build" / "logs"

VARIANTS = {
    "active_high": "sdkconfig.defaults",
    "active_low": "sdkconfig.defaults;sdkconfig.defaults.active_low",
}

READY = "HW_TESTS_READY"
DONE = "HW_TESTS_DONE"
SUMMARY = re.compile(r"^(\d+) Tests (\d+) Failures (\d+) Ignored")


def idf(variant: str, *args: str) -> None:
    build = (APP / "build" / variant).resolve()
    cmd = [
        "idf.py", "-C", str(APP.resolve()), "-B", str(build),
        "-D", f"SDKCONFIG={build / 'sdkconfig'}",
        "-D", f"SDKCONFIG_DEFAULTS={VARIANTS[variant]}",
        *args,
    ]
    log = LOG_DIR / f"{variant}-{args[-1]}.log"
    with log.open("w") as out:
        result = subprocess.run(cmd, stdout=out, stderr=subprocess.STDOUT)
    if result.returncode != 0:
        raise RuntimeError(f"idf.py {' '.join(args)} failed for {variant}; see {log}")


def run_tests(variant: str, port: str, timeout_s: float) -> bool:
    """Drive one flashed app through the READY/go/DONE protocol."""
    log_path = LOG_DIR / f"{variant}-run.log"
    deadline = time.monotonic() + timeout_s
    summary = None
    started = False

    # The board re-enumerates after the post-flash reset: retry the open.
    ser = None
    while ser is None:
        try:
            ser = serial.Serial(port, 115200, timeout=0.2)
        except serial.SerialException:
            if time.monotonic() > deadline:
                raise RuntimeError(f"could not open {port}")
            time.sleep(0.2)

    with ser, log_path.open("w") as log:
        buf = b""
        while time.monotonic() < deadline:
            buf += ser.read(ser.in_waiting or 1)
            *lines, buf = buf.split(b"\n")
            for raw in lines:
                line = raw.decode("utf-8", "replace").rstrip("\r")
                log.write(line + "\n")
                if line.startswith(READY):
                    if not started:
                        print(f"  {line}")
                        ser.write(b"g")
                        started = True
                    continue
                if not started:
                    continue
                if ":PASS" in line or ":FAIL" in line or ":IGNORE" in line:
                    print(f"  {line}")
                elif m := SUMMARY.match(line):
                    summary = tuple(int(x) for x in m.groups())
                    print(f"  {line}")
                elif line.startswith(DONE):
                    return summary is not None and summary[1] == 0
        raise RuntimeError(
            f"timed out after {timeout_s:.0f} s ({'no ' + READY if not started else 'no ' + DONE});"
            f" see {log_path}")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    parser.add_argument("--port", required=True, help="serial port, e.g. /dev/ttyACM0")
    parser.add_argument("--variant", choices=[*VARIANTS, "all"], default="all")
    parser.add_argument("--timeout", type=float, default=120.0,
                        help="seconds per variant for the test run (default 120)")
    args = parser.parse_args()

    if not (APP / "CMakeLists.txt").exists():
        print("run from the project root", file=sys.stderr)
        return 2
    LOG_DIR.mkdir(parents=True, exist_ok=True)

    variants = list(VARIANTS) if args.variant == "all" else [args.variant]
    failed = []
    for variant in variants:
        print(f"== {variant}")
        try:
            idf(variant, "build")
            idf(variant, "-p", args.port, "flash")
            ok = run_tests(variant, args.port, args.timeout)
        except RuntimeError as err:
            print(f"  ERROR: {err}")
            ok = False
        if not ok:
            failed.append(variant)

    if failed:
        print(f"FAILED: {', '.join(failed)}  (logs in {LOG_DIR})")
        return 1
    print(f"PASSED: {', '.join(variants)}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
