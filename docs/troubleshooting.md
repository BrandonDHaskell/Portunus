# Portunus — Troubleshooting

Common problems and their fixes, organized by area. Jump to the relevant section or search for the error message you're seeing.

- [Server setup](#server-setup)
- [Firmware build](#firmware-build)
- [Flashing](#flashing)
- [WiFi and connectivity](#wifi-and-connectivity)
- [Authentication and security](#authentication-and-security)
- [Access decisions](#access-decisions)
- [Admin UI and API](#admin-ui-and-api)
- [gRPC](#grpc)

---

## Server setup

**`go: command not found` after install**

The Go binary is at `/usr/local/go/bin/go`. Your shell profile does not have it on the PATH yet.

```bash
export PATH=$PATH:/usr/local/go/bin
```

Add that line to `~/.bashrc` or `~/.profile` for persistence, then open a new terminal or run `source ~/.bashrc`.

---

**`task: command not found`**

Task installs to `$(go env GOPATH)/bin` (usually `~/go/bin`). Add it to your PATH:

```bash
export PATH=$PATH:$(go env GOPATH)/bin
```

Then verify with `task --version`.

---

**Server refuses to start with `PORTUNUS_CREDENTIAL_HASH_SECRET` error**

In `prod` mode, `PORTUNUS_CREDENTIAL_HASH_SECRET` is required. The server will not start without it. Generate one and export it:

```bash
export PORTUNUS_CREDENTIAL_HASH_SECRET=$(openssl rand -hex 32)
```

This variable is server-only and does not need to match anything in the firmware.

---

**`database is locked` errors**

Should not occur during normal operation because all writes are serialized through a single goroutine. If you see this, another process (e.g. a `sqlite3` CLI session) is holding a lock on the database file. Close the external connection and the error will clear.

---

**Server starts but immediately exits with a migration error**

A migration failed to apply. Check the full error output — it will name the failing migration and include the SQLite error. If the database file is new and the migration is failing on first run, delete the database file and let the server recreate it:

```bash
rm ./data/portunus.db
```

Do not do this on a database with live data.

---

## Firmware build

**`idf.py: command not found`**

The ESP-IDF environment has not been sourced in this shell. Source it before running any `idf.py` or `task firmware:*` command:

```bash
. ~/esp/esp-idf/export.sh
```

If you installed ESP-IDF to a different path, adjust accordingly. You must repeat this in every new terminal session unless you added it to your shell profile.

---

**Build fails: `access_module/certs/ca_cert.pem` not found**

`PORTUNUS_TLS_USE_CUSTOM_CA` is enabled but the CA certificate has not been generated yet. From the repo root:

```bash
task certs:generate -- --ip <SERVER_IP>
```

That script creates `certs/ca.pem` and copies it to `access_module/certs/ca_cert.pem` automatically. Rebuild after generating.

---

**`task firmware:build:prod` fails: `PORTUNUS_HMAC_SECRET must be set in .env`**

The production build reads the HMAC secret from a `.env` file in the repo root. Create it:

```bash
# .env — repo root
PORTUNUS_HMAC_SECRET=<your-64-char-hex-secret>
```

Use the same value as `PORTUNUS_HMAC_SECRET` on the server. See [Dev and prod overlay builds](setup_firmware.md#dev-and-prod-overlay-builds).

---

**Build fails with an ESP-IDF version error**

The firmware targets ESP-IDF 5.x. Check your installed version:

```bash
idf.py --version
```

If you have an older version, update ESP-IDF:

```bash
cd ~/esp/esp-idf
git fetch
git checkout v5.4    # or latest stable 5.x tag
git submodule update --init --recursive
./install.sh esp32s3
. ./export.sh
```

---

**Nanopb or protobuf errors during build**

The generated protobuf files are committed to the repo. You should not need to regenerate them unless you changed `proto/portunus/v1/portunus.proto`. If the build fails on protobuf-related files, ensure you have not accidentally modified the generated stubs. Reset them with:

```bash
git checkout -- access_module/components/portunus_proto/
```

---

## Flashing

**No serial port found (`/dev/ttyACM*` or `/dev/ttyUSB*` missing)**

The board is not detected. Try:

1. Use a different USB cable — many cables are charge-only and carry no data.
2. Try a different USB port on your machine.
3. Confirm the board has power (check for any onboard LED).
4. Run `dmesg | tail -10` immediately after plugging in and look for a USB device attachment message.

---

**`Permission denied` on the serial port**

Your user is not in the `dialout` group:

```bash
sudo usermod -a -G dialout $USER
```

Log out and back in. Verify the group is active:

```bash
groups $USER
```

`dialout` must appear in the output before you retry.

---

**Flash fails: `A fatal error occurred: Failed to connect to ESP32-S3`**

The board is not entering the bootloader. Try:

1. Hold the **BOOT** button on the board, press **RESET**, then release **BOOT**. This forces bootloader mode on most ESP32-S3 dev boards.
2. Specify the port explicitly if `idf.py flash` is picking the wrong device:
   ```bash
   idf.py -p /dev/ttyACM0 flash
   ```
3. Lower the flash baud rate if you have a marginal USB connection:
   ```bash
   idf.py -p /dev/ttyACM0 -b 115200 flash
   ```

---

**Monitor shows garbled output**

The serial monitor baud rate does not match the firmware. The ESP-IDF monitor default matches the firmware's configured `CONFIG_ESP_CONSOLE_UART_BAUDRATE` (typically 115200). If you changed this in `menuconfig`, pass the baud rate explicitly:

```bash
idf.py -p /dev/ttyACM0 -b 115200 monitor
```

---

## WiFi and connectivity

**ESP32 does not connect to WiFi**

1. Confirm `PORTUNUS_WIFI_SSID` and `PORTUNUS_WIFI_PASSWORD` are set correctly in `menuconfig`. Passwords are case-sensitive.
2. The ESP32-S3 supports 2.4 GHz WiFi only — it will not connect to a 5 GHz-only network.
3. Check the monitor output for a specific WiFi error code. A `WIFI_REASON_AUTH_FAIL` means a wrong password; `WIFI_REASON_NO_AP_FOUND` means the SSID is not visible from the board's location.

---

**ESP32 is on WiFi but cannot reach the server**

1. Confirm `PORTUNUS_SERVER_HOST` is set to the server's LAN IP address, not `localhost` or `127.0.0.1`.
2. Verify the server is listening on all interfaces. A bind address of `:8080` listens on all interfaces; `127.0.0.1:8080` listens on loopback only (unreachable from the ESP32).
3. Check whether a firewall is blocking the port. On Debian:
   ```bash
   sudo ufw allow 8443/tcp   # or whichever port you're using
   sudo ufw status
   ```
4. Ping the server from another device on the same LAN to confirm basic reachability.

---

**Heartbeat shows `"known": false`**

The module has reached the server but is not commissioned. In production mode, register the module before it can receive access decisions:

```bash
curl -s -b /tmp/portunus-cookies.txt \
  -X POST https://localhost:8443/admin/v1/modules \
  --cacert certs/ca.pem \
  -H "Content-Type: application/json" \
  -d '{"module_id": "<your-module-id>", "door_id": "door_main", "display_name": "Main entrance"}' | jq .
```

See [Register an access module](setup_server.md#register-an-access-module). In dev mode, `door-001` is registered automatically on startup.

---

## Authentication and security

**`missing_signature` / HTTP 401 on device requests**

The server has `PORTUNUS_HMAC_SECRET` set but the device request is missing or has an invalid `X-Portunus-Sig` header. Check:

1. `PORTUNUS_HMAC_ENABLED` is set to `y` in firmware `menuconfig`.
2. `CONFIG_PORTUNUS_HMAC_SECRET` in the firmware exactly matches `PORTUNUS_HMAC_SECRET` on the server — including case, no leading or trailing whitespace.
3. For `firmware:build:prod`, the secret comes from the `.env` file, not `menuconfig` directly. Confirm the `.env` value matches the server.

---

**TLS connection fails / `mbedtls` errors in the monitor**

1. **Custom CA mismatch** — the CA cert embedded in the firmware does not match the CA that signed the server's certificate. Regenerate both together:
   ```bash
   task certs:generate -- --ip <SERVER_IP>
   ```
   Then rebuild and reflash the firmware so the new `access_module/certs/ca_cert.pem` is embedded.

2. **IP address mismatch** — the server certificate was generated with a different IP than the one the firmware is connecting to. The IP used in `task certs:generate -- --ip <IP>` must match `CONFIG_PORTUNUS_SERVER_HOST` in the firmware.

3. **Using skip-verify in production** — `PORTUNUS_TLS_SKIP_VERIFY=y` disables certificate validation. This is only for temporary development testing. Set it to `n` for any real deployment.

---

**Admin API returns HTTP 403 on every request after login**

The bootstrap admin account has `must_change_pw` set. The server blocks all admin operations until the password is changed. Change it via the web UI or API:

```
http://localhost:8080/admin/ui/change-password   # dev
https://<server>:8443/admin/ui/change-password   # production
```

See [Change the bootstrap password](setup_server.md#change-the-bootstrap-password).

---

## Access decisions

**All credential taps return `denied`**

Work through these in order:

1. The module is not commissioned — check `"known"` in the heartbeat response (see [Heartbeat shows `"known": false`](#heartbeat-shows-known-false)).
2. The credential has not been registered — register it via `POST /admin/v1/credentials` or the admin UI.
3. The member the credential belongs to has no authorization for this module — add one via `POST /admin/v1/modules/{module_id}/authorizations`.
4. The member's authorization has expired — check the `expires_at` field in the authorization record.
5. In dev mode, set `PORTUNUS_ALLOW_ALL=true` to grant everything while debugging setup.

---

**`unknown_module` in access or heartbeat responses**

The `module_id` sent by the firmware is not registered in the server database. This is the same root cause as `"known": false` — see [Heartbeat shows `"known": false`](#heartbeat-shows-known-false).

---

**Door unlocks but re-locks immediately**

The reed switch is reporting that the door is already closed. Check:

1. `PORTUNUS_ENABLE_REED_SWITCH` — if enabled, the firmware re-locks as soon as it sees the door close.
2. The reed switch wiring and the `PORTUNUS_REED_SWITCH_NORMALLY_CLOSED` setting in `menuconfig` — an inverted configuration makes the door appear perpetually closed.
3. Disable the reed switch in `menuconfig` for bench testing without a physical door.

---

## Admin UI and API

**Admin UI is unreachable at `/admin/ui/`**

1. Confirm the server is running and listening on the expected address/port.
2. In dev mode the address is `http://localhost:8080/admin/ui/`.
3. In production with TLS the address is `https://<server-ip>:8443/admin/ui/`. Your browser will warn about the self-signed certificate — use `--cacert certs/ca.pem` with curl, or import `certs/ca.pem` into your browser's trust store.

---

**Session cookie not sent / admin requests return `401 Unauthorized`**

The session expires after 8 hours. Log in again:

```bash
curl -s -c /tmp/portunus-cookies.txt \
  -X POST https://localhost:8443/admin/v1/login \
  --cacert certs/ca.pem \
  -H "Content-Type: application/json" \
  -d '{"username": "admin", "password": "<your-password>"}' | jq .
```

When using curl, pass `-b /tmp/portunus-cookies.txt` on every subsequent admin request to include the cookie.

---

## gRPC

**gRPC not working / connection refused on port 50051**

gRPC is disabled by default. Enable it by setting `PORTUNUS_GRPC_ADDR=:50051` in the server environment. The server starts a gRPC listener on that address alongside the HTTP server.

gRPC also requires TLS — `PORTUNUS_TLS_CERT_FILE` and `PORTUNUS_TLS_KEY_FILE` must be set. The firmware must be built with `CONFIG_PORTUNUS_USE_GRPC=y` and `CONFIG_PORTUNUS_USE_TLS=y`.

---

**gRPC HMAC verification fails**

The gRPC path signs the raw protobuf message body and attaches the signature as request metadata. The same `PORTUNUS_HMAC_SECRET` is used for both HTTP and gRPC — confirm the values match on both sides (see [`missing_signature` / HTTP 401](#missing_signature--http-401-on-device-requests) above).
