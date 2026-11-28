#!/usr/bin/env python3
"""Build, flash and run the on-target hardware tier (chunk 5).

Phase 5a (default): for each variant (active_high: active-high strike,
normally-open reed; active_low: active-low strike, normally-closed reed)
build access_module/test/hardware, flash it, wait for HW_TESTS_READY, send
the go byte, and collect output until HW_TESTS_DONE.

Phase 5b (--net): run a throwaway Portunus server on this host (ci profile,
temporary database, throwaway test CAs, random secrets held in memory),
enroll a test card for a test module through the admin API, then build and
flash access_module/test/hardware/net, send it the server config over
serial, and swap the server's certificate when the app asks (HW-07).

Unity's summary line decides the result. The apps use the firmware's
partition table and never write NVS. Wiring: access_module/test/hardware.

Run from the project root with ESP-IDF exported (. ~/esp/esp-idf/export.sh),
using the IDF venv's Python (it provides pyserial):

    python scripts/hw_test.py --port /dev/ttyACM0
    python scripts/hw_test.py --port /dev/ttyACM0 --variant active_low
    python scripts/hw_test.py --port /dev/ttyACM0 --net [--server-ip 10.0.0.59]

Exit codes:
    0  every run passed (Unity reported 0 failures)
    1  a test failed, or a build, flash, server or serial step failed
    2  bad arguments or environment
"""

import argparse
import hashlib
import hmac
import http.cookiejar
import json
import os
import re
import secrets
import shutil
import socket
import ssl
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request
from pathlib import Path

APP = Path("access_module/test/hardware")
APP_NET = APP / "net"
LOG_DIR = APP / "build" / "logs"

VARIANTS = {
    "active_high": "sdkconfig.defaults",
    "active_low": "sdkconfig.defaults;sdkconfig.defaults.active_low",
}

READY = "HW_TESTS_READY"
NEED = "HW_NEED"
DONE = "HW_TESTS_DONE"
SUMMARY = re.compile(r"^(\d+) Tests (\d+) Failures (\d+) Ignored")

# Test identities for --net. ENROLLED_UID must match ENROLLED in
# test/hardware/net/main/test_hw_net_main.cpp.
MODULE_ID = "hw-test-01"
DOOR_ID = "hw-test-door"
ENROLLED_UID = bytes([0x04, 0x48, 0x57, 0x54, 0x45, 0x53, 0x54])
GRPC_PORT = 50051
ADMIN_PORT = 18443


def idf(app: Path, build_name: str, defaults: str, *args: str) -> None:
    build = (app / "build" / build_name).resolve()
    cmd = [
        "idf.py", "-C", str(app.resolve()), "-B", str(build),
        "-D", f"SDKCONFIG={build / 'sdkconfig'}",
        "-D", f"SDKCONFIG_DEFAULTS={defaults}",
        *args,
    ]
    log = LOG_DIR / f"{build_name}-{args[-1]}.log"
    with log.open("w") as out:
        result = subprocess.run(cmd, stdout=out, stderr=subprocess.STDOUT)
    if result.returncode != 0:
        raise RuntimeError(f"idf.py {' '.join(args)} failed for {build_name}; see {log}")


def open_serial(port: str, deadline: float):
    import serial  # pyserial, shipped in the ESP-IDF venv

    # The board re-enumerates after the post-flash reset: retry the open.
    while True:
        try:
            return serial.Serial(port, 115200, timeout=0.2)
        except serial.SerialException:
            if time.monotonic() > deadline:
                raise RuntimeError(f"could not open {port}")
            time.sleep(0.2)


def run_tests(name: str, port: str, timeout_s: float, on_ready, on_need=None) -> bool:
    """Drive a flashed app: answer READY (and NEED) lines, collect results.

    on_ready() and on_need(request) return the bytes to send back. Only
    the board's output is logged, never what is sent to it.
    """
    log_path = LOG_DIR / f"{name}-run.log"
    deadline = time.monotonic() + timeout_s
    summary = None
    started = False
    last_need = None

    ser = open_serial(port, deadline)
    with ser, log_path.open("w") as log:
        buf = b""
        while time.monotonic() < deadline:
            buf += ser.read(ser.in_waiting or 1)
            *lines, buf = buf.split(b"\n")
            for raw in lines:
                line = raw.decode("utf-8", "replace").rstrip("\r")
                log.write(line + "\n")
                log.flush()
                if line.startswith(READY):
                    if not started:
                        print(f"  {line}")
                        ser.write(on_ready())
                        started = True
                    continue
                if line.startswith(NEED):
                    # The app repeats a request until answered: act on a
                    # request once, when it differs from the last one.
                    if on_need is not None and line != last_need:
                        print(f"  {line}")
                        last_need = line
                        ser.write(on_need(line))
                    continue
                if not started:
                    continue
                if line.startswith("HW_FATAL"):
                    raise RuntimeError(line)
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


# ── Phase 5a ──────────────────────────────────────────────────────────────────

def run_gpio(port: str, variants, timeout_s: float):
    failed = []
    for variant in variants:
        print(f"== {variant}")
        try:
            idf(APP, variant, VARIANTS[variant], "build")
            idf(APP, variant, VARIANTS[variant], "-p", port, "flash")
            ok = run_tests(variant, port, timeout_s, on_ready=lambda: b"g")
        except RuntimeError as err:
            print(f"  ERROR: {err}")
            ok = False
        if not ok:
            failed.append(variant)
    return failed


# ── Phase 5b: throwaway certificates and server ───────────────────────────────

CERTS = APP_NET / "certs"


def openssl(*args: str) -> None:
    subprocess.run(["openssl", *args], check=True,
                   stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)


def make_ca(name: str, cn: str) -> None:
    key, pem = CERTS / f"{name}.key", CERTS / f"{name}.pem"
    if key.exists() and pem.exists():
        return
    openssl("req", "-x509", "-newkey", "ec", "-pkeyopt", "ec_paramgen_curve:P-256",
            "-nodes", "-keyout", str(key), "-out", str(pem), "-days", "3650",
            "-subj", f"/CN={cn}", "-addext", "basicConstraints=critical,CA:TRUE",
            "-addext", "keyUsage=critical,keyCertSign,cRLSign")
    key.chmod(0o600)


def make_server_cert(ca: str, name: str, server_ip: str) -> None:
    key, pem = CERTS / f"{name}.key", CERTS / f"{name}.pem"
    marker = CERTS / f"{name}.ip"
    if key.exists() and pem.exists() and marker.exists() and marker.read_text() == server_ip:
        return
    csr = CERTS / f"{name}.csr"
    ext = CERTS / f"{name}.ext"
    ext.write_text(f"subjectAltName=IP:{server_ip},IP:127.0.0.1\n"
                   "extendedKeyUsage=serverAuth\nbasicConstraints=CA:FALSE\n")
    openssl("req", "-newkey", "ec", "-pkeyopt", "ec_paramgen_curve:P-256", "-nodes",
            "-keyout", str(key), "-out", str(csr), "-subj", "/CN=portunus-hw-test")
    openssl("x509", "-req", "-in", str(csr), "-CA", str(CERTS / f"{ca}.pem"),
            "-CAkey", str(CERTS / f"{ca}.key"), "-CAcreateserial", "-out", str(pem),
            "-days", "825", "-extfile", str(ext))
    key.chmod(0o600)
    csr.unlink()
    ext.unlink()
    marker.write_text(server_ip)


def prepare_certs(server_ip: str) -> None:
    """CA-A is pinned by the app (certs/ca_cert.pem); CA-B is the wrong one."""
    CERTS.mkdir(parents=True, exist_ok=True)
    make_ca("ca_a", "Portunus HW test CA-A (throwaway)")
    make_ca("ca_b", "Portunus HW test CA-B (throwaway, not pinned)")
    make_server_cert("ca_a", "server_a", server_ip)
    make_server_cert("ca_b", "server_b", server_ip)
    pinned = CERTS / "ca_cert.pem"
    if not pinned.exists() or pinned.read_bytes() != (CERTS / "ca_a.pem").read_bytes():
        shutil.copyfile(CERTS / "ca_a.pem", pinned)


class TestServer:
    """The Portunus server in the ci profile, on a temporary database.

    gRPC listens on the LAN address the board uses; the admin API only on
    127.0.0.1. The secrets exist only in this process and the server's
    environment.
    """

    def __init__(self, server_ip: str):
        self.server_ip = server_ip
        self.binary = (APP / "build" / "hw-server").resolve()
        self.tmp = Path(tempfile.mkdtemp(prefix="portunus-hw-"))
        self.hmac_secret = secrets.token_hex(32)
        self.cred_secret = secrets.token_hex(32)
        self.proc = None
        self.log = (LOG_DIR / "net-server.log").open("w")

    def build(self) -> None:
        # A server left over from an interrupted run would hold the ports.
        subprocess.run(["bash", "scripts/stop_local_servers.sh"], check=True,
                       stdout=self.log, stderr=subprocess.STDOUT)
        result = subprocess.run(["go", "build", "-o", str(self.binary), "./cmd/portunus-server"],
                                cwd="server", stdout=self.log, stderr=subprocess.STDOUT)
        if result.returncode != 0:
            raise RuntimeError("server build failed; see net-server.log")

    def start(self, cert: str) -> None:
        env = dict(os.environ,
                   PORTUNUS_ENV="ci",
                   PORTUNUS_DB_PATH=str(self.tmp / "portunus.db"),
                   PORTUNUS_HTTP_ADDR=f"127.0.0.1:{ADMIN_PORT}",
                   PORTUNUS_GRPC_ADDR=f"{self.server_ip}:{GRPC_PORT}",
                   PORTUNUS_TLS_CERT_FILE=str((CERTS / f"{cert}.pem").resolve()),
                   PORTUNUS_TLS_KEY_FILE=str((CERTS / f"{cert}.key").resolve()),
                   PORTUNUS_HMAC_SECRET=self.hmac_secret,
                   PORTUNUS_CREDENTIAL_HASH_SECRET=self.cred_secret)
        self.log.write(f"--- starting server with {cert}\n")
        self.log.flush()
        self.proc = subprocess.Popen([str(self.binary)], env=env,
                                     stdout=self.log, stderr=subprocess.STDOUT)
        deadline = time.monotonic() + 20
        while time.monotonic() < deadline:
            if self.proc.poll() is not None:
                raise RuntimeError("server exited at startup; see net-server.log")
            try:
                with socket.create_connection((self.server_ip, GRPC_PORT), timeout=0.5), \
                        socket.create_connection(("127.0.0.1", ADMIN_PORT), timeout=0.5):
                    return
            except OSError:
                time.sleep(0.2)
        raise RuntimeError("server did not start listening; see net-server.log")

    def stop(self) -> None:
        if self.proc is not None and self.proc.poll() is None:
            self.proc.terminate()
            try:
                self.proc.wait(timeout=10)
            except subprocess.TimeoutExpired:
                self.proc.kill()
                self.proc.wait()
        self.proc = None

    def close(self) -> None:
        self.stop()
        self.log.close()
        shutil.rmtree(self.tmp, ignore_errors=True)

    # ── Admin API (127.0.0.1, verified against CA-A) ──

    def _admin(self):
        ctx = ssl.create_default_context(cafile=str(CERTS / "ca_a.pem"))
        jar = http.cookiejar.CookieJar()
        opener = urllib.request.build_opener(urllib.request.HTTPSHandler(context=ctx),
                                             urllib.request.HTTPCookieProcessor(jar))

        def call(method: str, path: str, body=None):
            data = None if body is None else json.dumps(body).encode()
            req = urllib.request.Request(f"https://127.0.0.1:{ADMIN_PORT}{path}", data=data,
                                         method=method,
                                         headers={"Content-Type": "application/json"})
            try:
                with opener.open(req, timeout=10) as resp:
                    raw = resp.read()
            except urllib.error.HTTPError as err:
                detail = err.read().decode("utf-8", "replace")[:200]
                raise RuntimeError(f"admin {method} {path}: HTTP {err.code} {detail}") from None
            return json.loads(raw) if raw else {}
        return call

    def enroll(self) -> None:
        """Door, module, member, credential and authorization, as an admin
        would. The credential hash follows the canonical scheme
        (HMAC-SHA256 over the raw UID bytes, keyed with the hash secret), so
        a grant proves the firmware's lookup matches it (hazard #1)."""
        pw_file = self.tmp / "initial-admin-password.txt"
        deadline = time.monotonic() + 10
        while not pw_file.exists() and time.monotonic() < deadline:
            time.sleep(0.1)
        password = next(line.split(":", 1)[1].strip()
                        for line in pw_file.read_text().splitlines()
                        if line.startswith("password:"))

        call = self._admin()
        call("POST", "/admin/v1/login", {"username": "admin", "password": password})
        new_password = secrets.token_urlsafe(24)
        call("POST", "/admin/v1/change-password",
             {"current_password": password, "new_password": new_password})
        call = self._admin()
        call("POST", "/admin/v1/login", {"username": "admin", "password": new_password})

        call("POST", "/admin/v1/doors", {"door_id": DOOR_ID, "name": "HW test door"})
        call("POST", "/admin/v1/modules", {"module_id": MODULE_ID, "door_id": DOOR_ID,
                                           "display_name": "HW test module"})
        member = call("POST", "/admin/v1/members", {})["member"]["uuid"]
        cred_hash = hmac.new(self.cred_secret.encode(), ENROLLED_UID, hashlib.sha256).hexdigest()
        call("POST", f"/admin/v1/members/{member}/credential", {"credential_hash": cred_hash})
        call("POST", f"/admin/v1/modules/{MODULE_ID}/authorizations", {"member_uuid": member})


def default_server_ip() -> str:
    """The address this host uses to reach the LAN (not 127.0.0.1)."""
    with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as s:
        s.connect(("10.255.255.255", 1))
        return s.getsockname()[0]


def run_net(port: str, server_ip: str, timeout_s: float) -> bool:
    print(f"== net (server {server_ip}:{GRPC_PORT}, admin 127.0.0.1:{ADMIN_PORT})")
    prepare_certs(server_ip)
    server = TestServer(server_ip)
    try:
        server.build()
        server.start("server_a")
        server.enroll()
        idf(APP_NET, "net", "sdkconfig.defaults", "build")
        idf(APP_NET, "net", "sdkconfig.defaults", "-p", port, "flash")

        def on_ready() -> bytes:
            return f"CFG {server_ip} {GRPC_PORT} {MODULE_ID} {server.hmac_secret}\n".encode()

        def on_need(line: str) -> bytes:
            want = line.split("server=", 1)[1].strip()
            server.stop()
            server.start("server_b" if want == "wrong_ca" else "server_a")
            return b"OK\n"

        return run_tests("net", port, timeout_s, on_ready, on_need)
    except (RuntimeError, OSError, subprocess.CalledProcessError) as err:
        print(f"  ERROR: {err}")
        return False
    finally:
        server.close()


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    parser.add_argument("--port", required=True, help="serial port, e.g. /dev/ttyACM0")
    parser.add_argument("--variant", choices=[*VARIANTS, "all"], default="all",
                        help="phase 5a variant(s) (ignored with --net)")
    parser.add_argument("--net", action="store_true", help="run phase 5b (network) instead")
    parser.add_argument("--server-ip", default=None,
                        help="LAN address for the test server's gRPC port (default: auto)")
    parser.add_argument("--timeout", type=float, default=None,
                        help="seconds for the test run (default 120, or 600 with --net)")
    args = parser.parse_args()

    if not (APP / "CMakeLists.txt").exists():
        print("run from the project root", file=sys.stderr)
        return 2
    LOG_DIR.mkdir(parents=True, exist_ok=True)

    if args.net:
        server_ip = args.server_ip or default_server_ip()
        ok = run_net(args.port, server_ip, args.timeout or 600.0)
        print("PASSED: net" if ok else f"FAILED: net  (logs in {LOG_DIR})")
        return 0 if ok else 1

    variants = list(VARIANTS) if args.variant == "all" else [args.variant]
    failed = run_gpio(args.port, variants, args.timeout or 120.0)
    if failed:
        print(f"FAILED: {', '.join(failed)}  (logs in {LOG_DIR})")
        return 1
    print(f"PASSED: {', '.join(variants)}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
