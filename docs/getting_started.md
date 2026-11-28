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
| Go 1.26+ | Build and run the server | [setup_server.md — Install Go](setup_server.md#install-go) |
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
go version    # should print go1.26 or newer
task --version
```

### 3. Start the server in local mode

```bash
cd server
PORTUNUS_ENV=local PORTUNUS_ALLOW_ALL=true go run ./cmd/portunus-server
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

## Post-Clone Setup

A clone does not contain everything you need to run Portunus. What is missing falls into three buckets:

- **Generated build output** (`build/`, `bin/`, `data/`). Created by the build and ignored by git. Nothing to do.
- **Command-generated material.** TLS certificates (`task certs:generate`) and the firmware overlay files (`sdkconfig.defaults*`, committed) are produced by tooling, not copied by hand.
- **Per-user files you create.** Secret and machine-specific config that cannot be committed: the repo-root `.env`, `server/.env.prod`, and each module's NVS device config (`access_module/nvs_config.csv`). These are covered below.

The two non-secret server profiles, `server/.env.local` and `server/.env.ci`, are committed and ship in the clone. You do not create them; they are documented under "Server environment files" only so you know what they do.

### Server environment files

The server selects a runtime profile via `PORTUNUS_ENV`. Each profile has an env file under `server/`.

| File | Committed? | Task | Profile behaviour |
|---|---|---|---|
| `server/.env.local` | Yes (no secrets) | `task run:server:local` | Plain HTTP on `:8080`, dev data seeded. Fast iteration. |
| `server/.env.ci` | Yes (no secrets) | `task run:server:ci` | TLS + gRPC over an in-process ephemeral cert, in-memory DB. Used by CI. |
| `server/.env.prod` | No — you create it | `task run:server:prod` | Hardened. Real certs and secrets. |

`server/.env.local` and `server/.env.ci` already exist after cloning. Create `server/.env.prod` yourself for the mirror loop (production config pointed at localhost on your dev machine):

```bash
# server/.env.prod — gitignored, you create this. Holds real secrets.
# Loaded by `task run:server:prod` (after the repo-root .env, which holds only deploy targets).
# The Raspberry Pi does NOT use this file; it reads /etc/portunus/portunus.env
# via systemd — see setup_server.md › Running as a systemd service.
PORTUNUS_ENV=prod
PORTUNUS_HTTP_ADDR=:8443
PORTUNUS_GRPC_ADDR=:50051
PORTUNUS_DB_PATH=./data/portunus.db
# Cert paths are relative to server/, where the task runs (certs:generate writes to repo-root certs/):
PORTUNUS_TLS_CERT_FILE=../certs/server.pem
PORTUNUS_TLS_KEY_FILE=../certs/server.key
PORTUNUS_HMAC_SECRET=<64-char hex, same value as the modules' NVS hmac_secret>
PORTUNUS_CREDENTIAL_HASH_SECRET=<64-char hex>
```

Setting `PORTUNUS_ENV=prod` here is required: the server validates the prod profile at startup and refuses to boot without TLS, the HMAC secret, and the credential hash secret. A prod env that forgets `PORTUNUS_ENV=prod` would otherwise boot as `local`.

### Deploy targets (repo-root `.env`)

The repo-root `.env` is loaded automatically by the Taskfile (`dotenv: ['.env']`). It holds your deployment target. The deploy tasks have no default host and refuse to run without one.

```bash
# .env (repo root, gitignored). Create this yourself.
# Used by task deploy:server, deploy:server:status, deploy:server:logs, deploy:server:rollback.
DEPLOY_HOST=pi@192.168.1.100
DEPLOY_DIR=/opt/portunus
DEPLOY_SERVICE=portunus-server
```

No secret belongs here. The HMAC secret goes in the server environment and in each module's NVS (see [Secrets](#secrets)); nothing reads it from this file. For HMAC and TLS in depth, including rotation, see [Shared secrets setup](shared_secrets_setup.md).

### TLS certificates

Generate a CA and server certificate for your server's LAN IP. Do this once: every module pins this CA. If the IP changes later, reissue only the server cert with `--sign-only` (see [Reissuing the server certificate](setup_server.md#reissuing-the-server-certificate-without-rotating-the-ca)).

```bash
task certs:generate -- --ip 192.168.1.100            # add --dns portunus.local if you have one
```

This writes `certs/ca.pem`, `certs/server.pem`, `certs/server.key`, and copies the CA into `access_module/certs/ca_cert.pem` for firmware embedding. For the full file list and TLS modes, see [TLS certificate setup](setup_server.md#tls-certificate-setup) and [Shared secrets setup](shared_secrets_setup.md).

### Secrets

Two secrets, both 32-byte hex, and they must be different values. The HMAC secret is shared with the modules (server env + each module's NVS `hmac_secret`); the credential hash secret is server-only.

```bash
openssl rand -hex 32   # → PORTUNUS_HMAC_SECRET (server env + module NVS)
openssl rand -hex 32   # → PORTUNUS_CREDENTIAL_HASH_SECRET (server only)
```

Record both. Keep them out of source control. Both are required in the `prod` profile.

### Firmware device settings

Two places, for two kinds of setting:

- **Device identity and secrets** (module ID, WiFi, server host, gRPC port, HMAC secret) live in NVS. Copy `access_module/nvs_config.example.csv` to `nvs_config.csv` (gitignored), fill it in, then `task firmware:nvs:gen` and `task firmware:nvs:flash`. See [Device configuration and secrets (NVS)](setup_firmware.md#device-configuration-and-secrets-nvs).
- **Hardware and behaviour** (module variant, pins, polarity, timing) are Kconfig, set with `task firmware:menuconfig` and saved to `sdkconfig` (gitignored). The profile builds keep these in `sdkconfig.defaults.local`. Security settings (TLS, HMAC, dev mode) come from the profile overlay, not menuconfig. For the menu reference and GPIO defaults, see [Firmware setup](setup_firmware.md).

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

You need your server's LAN IP before this step. Generate certs as described in [Post-Clone Setup › TLS certificates](#tls-certificates):

```bash
task certs:generate -- --ip 192.168.1.100
```

### 4. Generate secrets

Generate the HMAC secret and the credential hash secret as described in [Post-Clone Setup › Secrets](#secrets). Record both; you use them in steps 5 and 9.

### 5. Start the server

Put the production values, including both secrets, in `server/.env.prod`, as described in [Post-Clone Setup › Server environment files](#server-environment-files). Then start the server:

```bash
task run:server:prod
```

On first start, copy the bootstrap admin password from the server output. See [Production example](setup_server.md#production-example) for the full environment variable reference, and [Running as a systemd service](setup_server.md#running-as-a-systemd-service) for a persistent deployment on a Raspberry Pi.

### 6. Change the bootstrap admin password

```
https://<server-ip>:8443/admin/ui/change-password
```

Or via the API — see [Change the bootstrap password](setup_server.md#change-the-bootstrap-password).

### 7. Register the access module

Choose a module ID (e.g. `front-door`). This value must match `module_id` in the module's NVS config (step 9).

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

### 9. Create the module's NVS config

```bash
cp access_module/nvs_config.example.csv access_module/nvs_config.csv
```

Fill in `module_id` (`front-door`, as in step 7), `wifi_ssid`, `wifi_psk`, `server_host` (your server's LAN IP, covered by its cert), `grpc_port` (`50051`) and `hmac_secret` (the **same value** you set on the server in step 5). The file is gitignored; never commit or print it.

### 10. Configure the firmware hardware

```bash
task firmware:menuconfig
```

Under **Portunus Configuration**, set the **Module Variant** (`ACCESS_POINT` for a door) and, if your wiring differs from the defaults, the pin and polarity options. Save and exit. TLS, HMAC and CA pinning are set by the prod profile in the next step, not here.

### 11. Build and flash

```bash
task firmware:build:prod
task firmware:nvs:gen
task firmware:nvs:flash
task firmware:flash-monitor
```

`firmware:build:prod` fails if the compiled image is not deployable (dev mode, TLS or HMAC off, CA not pinned). The NVS steps write the device config from step 9; reflashing the app later does not touch it. Find your serial port first if needed: see [Finding your serial port](setup_firmware.md#finding-your-serial-port).

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

- **Enroll members and cards**: use the admin UI at `/admin/ui/`, a provisioning console module, or `POST /admin/v1/members/{member_uuid}/credential`
- **Authorize members** — grant specific members access to specific modules via `POST /admin/v1/modules/{module_id}/authorizations`
- **Deploy to a Raspberry Pi**: see [Running as a systemd service](setup_server.md#running-as-a-systemd-service) and [CI/CD pipeline](ci_cd_pipeline.md)
- **Add more modules**: repeat steps 7 and 9 to 12 with a new `module_id` for each additional door

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
