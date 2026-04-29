# Portunus — Getting Started

This guide walks a new developer through standing up a complete Portunus system from scratch: a running Go server and a flashed ESP32-S3 access module that successfully exchanges messages with it.

Two paths are covered:

- **[Development quickstart](#development-quickstart)** — plain HTTP, no TLS, all credentials granted. Get something working in under an hour with minimal configuration.
- **[Production setup](#production-setup)** — TLS certificate pinning, HMAC request signing, and proper module registration. Use this for any real deployment.

If this is your first time, start with the development quickstart to confirm your toolchain and hardware work before adding security configuration.

---

## What you need

### Hardware

| Item | Notes |
|---|---|
| ESP32-S3 development board | The firmware targets the ESP32-S3 specifically |
| MFRC522 RFID reader | Connected over SPI |
| Door strike | Driven via relay or MOSFET; can be omitted for bench testing |
| Reed switch | Door-state sensor; can be omitted for bench testing |
| Status LED + resistor | Single GPIO indicator |
| USB cable | For flashing and serial monitor |
| Server machine | Raspberry Pi 5 or any Debian/Ubuntu Linux host |

Individual hardware features can be disabled in `menuconfig` if you want to bench-test without a fully wired door. See the [firmware wiring reference](setup_firmware.md#default-wiring-values-currently-reflected-in-kconfig).

### Software (development machine)

| Tool | Purpose | Install guide |
|---|---|---|
| Go 1.24+ | Build and run the server | [setup_server.md — Install Go](setup_server.md#install-go) |
| Task 3.x | Build runner used throughout both setups | [setup_firmware.md — Install Task](setup_firmware.md#install-task) |
| ESP-IDF 5.x | ESP32 toolchain (2–5 GB, 10–30 min install) | [setup_firmware.md — Install ESP-IDF](setup_firmware.md#install-esp-idf) |
| OpenSSL | TLS certificate generation (production) | Usually pre-installed on Debian |
| Git | Clone the repository | Usually pre-installed on Debian |

---

## Development quickstart

No TLS. No HMAC. The server grants all credential checks automatically. Use this to verify your hardware and toolchain before production configuration.

### 1. Clone the repo

```bash
git clone https://github.com/BrandonDHaskell/Portunus.git
cd Portunus
```

### 2. Install Go and Task

Follow [Install Go](setup_server.md#install-go) and [Install Task](setup_server.md#install-task) in the server setup guide.

Verify:

```bash
go version    # should print go1.24 or newer
task --version
```

### 3. Start the server in dev mode

```bash
cd server
PORTUNUS_ENV=dev PORTUNUS_ALLOW_ALL=true go run ./cmd/portunus-server
```

The server starts on `:8080`. It seeds a default `door-001` module automatically and grants all credential checks. **Leave this terminal open.**

On startup, the server prints a randomly generated admin password:

```
FIRST RUN — initial admin account created
  username: admin
  password: <randomly generated>
```

**Copy that password now.** You will need it in the next step.

### 4. Change the bootstrap admin password

The server blocks all admin operations until the password is changed. Open a browser and navigate to:

```
http://localhost:8080/admin/ui/change-password
```

Log in with `admin` and the printed password, then set a new one. See [Change the bootstrap password](setup_server.md#change-the-bootstrap-password) for the API alternative.

### 5. Install ESP-IDF

Follow [Install ESP-IDF](setup_firmware.md#install-esp-idf) in the firmware setup guide. This step downloads 2–5 GB and takes 10–30 minutes — plan accordingly.

```bash
mkdir -p ~/esp && cd ~/esp
git clone --recursive https://github.com/espressif/esp-idf.git
cd esp-idf
./install.sh esp32s3
. ./export.sh
```

Return to the repo root when done:

```bash
cd ~/path/to/Portunus
```

### 6. Configure the firmware

```bash
task firmware:menuconfig
```

Set the following at minimum under **Portunus Configuration**:

| Setting | Value | Menu location |
|---|---|---|
| Module variant | `ACCESS_POINT` | Module Variant |
| `PORTUNUS_MODULE_ID` | `door-001` | Network Configuration |
| `PORTUNUS_WIFI_SSID` | your WiFi SSID | Network Configuration |
| `PORTUNUS_WIFI_PASSWORD` | your WiFi password | Network Configuration |
| `PORTUNUS_SERVER_HOST` | your server's LAN IP | Network Configuration |
| `PORTUNUS_SERVER_PORT` | `8080` | Network Configuration |
| `PORTUNUS_USE_TLS` | disabled | Security Configuration |
| `PORTUNUS_HMAC_ENABLED` | disabled | Security Configuration |

Save and exit menuconfig.

### 7. Build and flash

Connect the ESP32-S3 over USB. Find its serial port:

```bash
ls /dev/ttyACM* /dev/ttyUSB* 2>/dev/null
```

Then build and flash:

```bash
task firmware:flash-monitor
```

The monitor output will show the startup sequence. With WiFi enabled, you should see a successful connection and heartbeat messages reaching the server.

### 8. Verify

On the server terminal, look for incoming heartbeat messages from `door-001`. To confirm via the API:

```bash
curl -s http://localhost:8080/v1/heartbeat \
  -H "Content-Type: application/json" \
  -d '{"module_id": "door-001", "firmware_version": "test", "uptime_s": 0}' | jq .
```

A response of `"known": true` confirms the module is communicating with the server. Tap an RFID card — the server will grant access because `PORTUNUS_ALLOW_ALL=true`.

---

## Production setup

TLS certificate pinning, HMAC request signing, and explicit module registration. The server and firmware share two values that must be configured together: the **server IP** (used in the TLS certificate) and the **HMAC secret**.

### 1. Clone the repo

```bash
git clone https://github.com/BrandonDHaskell/Portunus.git
cd Portunus
```

### 2. Install Go and Task

Follow [Install Go](setup_server.md#install-go) and [Install Task](setup_server.md#install-task) in the server setup guide.

### 3. Generate TLS certificates

You need your server's LAN IP address before this step. Generating certs after the server IP is decided means you don't need to regenerate them unless the IP changes.

```bash
task certs:generate -- --ip 192.168.1.100
```

Replace `192.168.1.100` with your actual server IP. Optionally add a DNS name:

```bash
task certs:generate -- --ip 192.168.1.100 --dns portunus.local
```

This creates `certs/ca.pem`, `certs/server.pem`, `certs/server.key`, and copies the CA cert to `access_module/certs/ca_cert.pem` for firmware embedding. See [TLS certificate setup](setup_server.md#tls-certificate-setup) for the full file list.

### 4. Generate secrets

Generate the HMAC secret (shared between server and firmware) and the credential hash secret (server-only):

```bash
openssl rand -hex 32   # → PORTUNUS_HMAC_SECRET
openssl rand -hex 32   # → PORTUNUS_CREDENTIAL_HASH_SECRET
```

**Record both values.** You will use them in steps 5 and 9. Keep them out of source control.

### 5. Start the server

```bash
export PORTUNUS_ENV=prod
export PORTUNUS_HTTP_ADDR=:8443
export PORTUNUS_TLS_CERT_FILE="$PWD/certs/server.pem"
export PORTUNUS_TLS_KEY_FILE="$PWD/certs/server.key"
export PORTUNUS_HMAC_SECRET=<your-hmac-secret-from-step-4>
export PORTUNUS_CREDENTIAL_HASH_SECRET=<your-hash-secret-from-step-4>

cd server && go run ./cmd/portunus-server
```

On first start, copy the bootstrap admin password from the server output. See [Production example](setup_server.md#production-example) for the full environment variable reference, and [Running as a systemd service](setup_server.md#running-as-a-systemd-service) for a persistent deployment on a Raspberry Pi.

### 6. Change the bootstrap admin password

```
https://<server-ip>:8443/admin/ui/change-password
```

Or via the API — see [Change the bootstrap password](setup_server.md#change-the-bootstrap-password).

### 7. Register the access module

Choose a module ID (e.g. `front-door`). This value must match `PORTUNUS_MODULE_ID` in the firmware you flash in step 10.

```bash
curl -s -c /tmp/portunus-cookies.txt \
  -X POST https://localhost:8443/admin/v1/login \
  --cacert certs/ca.pem \
  -H "Content-Type: application/json" \
  -d '{"username": "admin", "password": "<your-password>"}' | jq .

curl -s -b /tmp/portunus-cookies.txt \
  -X POST https://localhost:8443/admin/v1/modules \
  --cacert certs/ca.pem \
  -H "Content-Type: application/json" \
  -d '{"module_id": "front-door", "door_id": "door_main", "display_name": "Main entrance"}' | jq .
```

See [Register an access module](setup_server.md#register-an-access-module) for the full field reference.

### 8. Install ESP-IDF

Follow [Install ESP-IDF](setup_firmware.md#install-esp-idf). This downloads 2–5 GB and takes 10–30 minutes.

```bash
mkdir -p ~/esp && cd ~/esp
git clone --recursive https://github.com/espressif/esp-idf.git
cd esp-idf
./install.sh esp32s3
. ./export.sh
```

### 9. Create the .env file

The production firmware build reads `PORTUNUS_HMAC_SECRET` from a `.env` file in the repo root:

```bash
# .env — repo root, gitignored
PORTUNUS_HMAC_SECRET=<same-hmac-secret-as-step-4>
```

Use the **same value** you set on the server in step 5. See [Dev and prod overlay builds](setup_firmware.md#dev-and-prod-overlay-builds).

### 10. Configure the firmware

```bash
task firmware:menuconfig
```

Set the following under **Portunus Configuration**:

| Setting | Value | Menu location |
|---|---|---|
| Module variant | `ACCESS_POINT` | Module Variant |
| `PORTUNUS_MODULE_ID` | `front-door` (must match step 7) | Network Configuration |
| `PORTUNUS_WIFI_SSID` | your WiFi SSID | Network Configuration |
| `PORTUNUS_WIFI_PASSWORD` | your WiFi password | Network Configuration |
| `PORTUNUS_SERVER_HOST` | your server's LAN IP | Network Configuration |
| `PORTUNUS_USE_TLS` | enabled | Security Configuration |
| `PORTUNUS_TLS_SERVER_PORT` | `8443` | Security Configuration |
| `PORTUNUS_TLS_SKIP_VERIFY` | disabled | Security Configuration |
| `PORTUNUS_TLS_USE_CUSTOM_CA` | enabled | Security Configuration |
| `PORTUNUS_HMAC_ENABLED` | enabled | Security Configuration |
| `PORTUNUS_HMAC_SECRET` | your HMAC secret | Security Configuration |

Save and exit menuconfig.

### 11. Build and flash

```bash
task firmware:build:prod
task firmware:flash-monitor
```

`firmware:build:prod` reads the HMAC secret from `.env` and injects it at build time. Find your serial port first if needed — see [Finding your serial port](setup_firmware.md#finding-your-serial-port).

### 12. Verify end-to-end

On the server terminal, watch for a heartbeat from `front-door`. To confirm via the API:

```bash
BODY='{"module_id":"front-door","firmware_version":"0.1.0","uptime_s":0}'
SIG=$(echo -n "$BODY" | openssl dgst -sha256 -hmac "$PORTUNUS_HMAC_SECRET" | awk '{print $2}')

curl -s -X POST https://localhost:8443/v1/heartbeat \
  --cacert certs/ca.pem \
  -H "Content-Type: application/json" \
  -H "X-Portunus-Sig: $SIG" \
  -d "$BODY" | jq .
```

`"known": true` in the response confirms the module is commissioned and communicating over a verified TLS connection with a signed request.

---

## Next steps

With the system running:

- **Register credentials** — use the admin UI at `/admin/ui/` or `POST /admin/v1/credentials` to add RFID cards
- **Authorize members** — grant specific members access to specific modules via `POST /admin/v1/modules/{module_id}/authorizations`
- **Deploy to a Raspberry Pi** — see [Running as a systemd service](setup_server.md#running-as-a-systemd-service) and `task deploy:server`
- **Add more modules** — repeat steps 7 and 10–12 with a new `module_id` for each additional door

---

## Reference docs

| Document | When to read it |
|---|---|
| [Server setup](setup_server.md) | Full server configuration, env vars, systemd deployment |
| [Firmware setup](setup_firmware.md) | Full Kconfig reference, wiring diagrams, transport options |
| [Shared secrets setup](shared_secrets_setup.md) | TLS and HMAC in depth, certificate rotation |
| [API reference](api.md) | All HTTP and gRPC endpoints with request/response shapes |
| [Architecture](architecture.md) | System design, firmware layering, event flow |
| [Security](security.md) | Threat model, hardening checklist, known limitations |
| [Troubleshooting](troubleshooting.md) | Common errors and fixes for server, firmware, connectivity, and auth |
