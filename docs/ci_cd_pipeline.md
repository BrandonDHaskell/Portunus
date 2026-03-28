# Portunus — CI/CD Pipeline

Local build, test, and deployment pipeline for the Portunus server and access module firmware.

**Last updated:** March 2026

---

## Overview

Portunus runs on two machines with a single ESP32 that targets either:

| Machine | Role | OS | Architecture |
|---|---|---|---|
| Dev desktop | Development, testing, building, firmware flashing | Debian Linux | x86_64 (amd64) |
| Raspberry Pi 5 | Production server | Debian Linux (Raspberry Pi OS) | aarch64 (arm64) |
| ESP32-S3 | Door access module | — | Xtensa LX7 |

All builds happen on the dev machine. The Pi receives pre-built binaries — it does not compile anything. The ESP32 is always flashed from the dev machine via USB.

The pipeline is driven entirely by [Task](https://taskfile.dev) targets defined in `Taskfile.yml`. There is no hosted CI service (GitHub Actions, etc.) at this time. The pipeline runs locally and deploys over SSH.

---

## Pipeline stages

```
  ┌─────────────────────────────────────────────────────────────┐
  │                      Dev Machine                            │
  │                                                             │
  │  1. VALIDATE        task ci:all                             │
  │     go vet, gofmt, tests (race), proto drift check,        │
  │     firmware compile check                                  │
  │                          │                                  │
  │  2. BUILD               ▼                                   │
  │     task build:server              (native x86_64)          │
  │     task build:server:arm64        (cross-compile for Pi)   │
  │     task firmware:build:prod       (ESP32 prod firmware)    │
  │                          │                                  │
  │  3. DEPLOY SERVER        ▼                                  │
  │     task deploy:server   ──── SSH/SCP ────►  Raspberry Pi   │
  │                                              (systemd       │
  │  4. FLASH FIRMWARE                            restart)      │
  │     task firmware:flash  ──── USB ────────►  ESP32          │
  │                                                             │
  └─────────────────────────────────────────────────────────────┘
```

Each stage is independent and can be run in isolation, but the typical flow runs them in order: validate → build → deploy → flash.

---

## Prerequisites

Before using the pipeline, both machines need their base setup completed:

- **Dev machine:** Go 1.24+, ESP-IDF 5.4+, Task 3.x — see [Server Setup](setup_server.md) and [Firmware Setup](setup_firmware.md)
- **Raspberry Pi:** The Portunus systemd service installed and the `portunus` user created — see [Server Setup — Running as a systemd service](setup_server.md#running-as-a-systemd-service)
- **SSH access** from the dev machine to the Pi without a password prompt (key-based auth)

### Setting up SSH key-based auth

If you haven't already:

```bash
# On the dev machine — generate a key pair (skip if you already have one)
ssh-keygen -t ed25519 -C "portunus-deploy"

# Copy the public key to the Pi
ssh-copy-id pi@<PI_IP>

# Verify passwordless login
ssh pi@<PI_IP> "echo ok"
```

### Pipeline configuration

The deployment tasks need to know the Pi's address. Create a `.env` file in the repo root (gitignored) with your deployment target:

```bash
# .env — local deployment config (not committed)
DEPLOY_HOST=pi@192.168.1.100
DEPLOY_DIR=/opt/portunus
DEPLOY_SERVICE=portunus-server
```

---

## Stage 1: Validate

Run the full validation suite before building or deploying. This catches issues early and costs seconds.

```bash
task ci:all
```

This runs the following checks in sequence:

| Check | What it does | Fails when |
|---|---|---|
| `vet:server` | Go static analysis | Suspicious code constructs found |
| `fmt:server` | gofmt formatting check | Any `.go` file is not formatted |
| `test:server:race` | All server tests with race detector | Test failure or data race detected |
| `proto:check` | Regenerate protobuf stubs and diff | Generated code doesn't match committed files |
| `firmware:build` | Compile firmware (default config) | Firmware doesn't compile |

If any check fails, the pipeline stops. Fix the issue before proceeding.

For faster iteration during development, you can run individual checks:

```bash
task test:server              # tests only, no race detector (faster)
task test:server:service      # just the service-layer tests
task vet:server               # just go vet
```

---

## Stage 2: Build

### Server binary (native — for dev machine)

```bash
task build:server
```

Produces `server/bin/portunus-server` (x86_64 Linux).

### Server binary (cross-compile — for Raspberry Pi)

```bash
task build:server:arm64
```

Produces `server/bin/portunus-server-linux-arm64`. Go's built-in cross-compilation handles this without any extra toolchains — the pure-Go SQLite driver (`modernc.org/sqlite`) means no CGo and no C cross-compiler.

### Firmware (production)

```bash
# Source ESP-IDF environment first
. ~/esp/esp-idf/export.sh

task firmware:build:prod
```

Produces the firmware binary in `access_module/build/`. The prod overlay applies minimal logging, strict TLS, and flash encryption settings.

For dev firmware:

```bash
task firmware:build:dev
```

**Important:** When switching between dev and prod firmware builds, always clean first:

```bash
task firmware:clean
task firmware:build:prod
```

---

## Stage 3: Deploy server to Raspberry Pi

```bash
task deploy:server
```

This task:

1. Cross-compiles the server for arm64 (if not already built)
2. Copies the binary to the Pi via SCP
3. Restarts the systemd service on the Pi via SSH

The deployment is zero-downtime for the database — SQLite WAL mode allows the new process to pick up where the old one left off. Schema migrations run automatically on startup.

### What gets deployed

Only the server binary is deployed. The database, TLS certificates, environment file, and systemd service file already live on the Pi and persist across deployments. This means:

- Database contents survive deployments (no data loss)
- TLS certs don't need to be re-deployed unless they're rotated
- Environment variables (secrets, config) are managed on the Pi in `/etc/portunus/portunus.env`

### Manual deployment (if you prefer)

```bash
# Build
GOOS=linux GOARCH=arm64 go build -o server/bin/portunus-server-linux-arm64 ./server/cmd/portunus-server

# Copy
scp server/bin/portunus-server-linux-arm64 pi@192.168.1.100:/tmp/portunus-server

# SSH in and swap
ssh pi@192.168.1.100 << 'EOF'
  sudo systemctl stop portunus-server
  sudo cp /tmp/portunus-server /opt/portunus/bin/portunus-server
  sudo chmod 755 /opt/portunus/bin/portunus-server
  sudo systemctl start portunus-server
  sudo systemctl status portunus-server --no-pager
EOF
```

### Verifying a deployment

After deploying, verify the server is running and responsive:

```bash
# Check systemd status
ssh pi@192.168.1.100 "sudo systemctl status portunus-server --no-pager"

# Check the server responds (from the dev machine)
curl -sk https://192.168.1.100:8443/v1/heartbeat \
  -X POST \
  -H "Content-Type: application/json" \
  -d '{"module_id":"healthcheck"}' | jq .
```

A healthy server returns a JSON response (even if the module is unknown — the `known: false` response confirms the server is processing requests).

---

## Stage 4: Flash firmware

Connect the ESP32 to the dev machine via USB.

### Flash production firmware

```bash
task firmware:flash
```

Or flash and immediately open the serial monitor to verify boot:

```bash
task firmware:flash-monitor
```

### Switching the ESP32 between dev and prod

Since a single ESP32 targets both environments, the firmware must be rebuilt and reflashed when switching. The key differences are server address, HMAC secret, and TLS certificate.

**Switching to production:**

```bash
# 1. Ensure the prod CA cert is embedded (if server IP changed)
task certs:generate -- --ip <PROD_PI_IP>

# 2. Clean and rebuild with prod overlay
task firmware:clean
task firmware:build:prod

# 3. Verify Kconfig values match production
#    (server host, HMAC secret, TLS settings)
task firmware:menuconfig

# 4. Flash
task firmware:flash
```

**Switching back to dev:**

```bash
# 1. Re-embed the dev CA cert (if dev server IP differs)
task certs:generate -- --ip <DEV_MACHINE_IP>

# 2. Clean and rebuild with dev overlay
task firmware:clean
task firmware:build:dev

# 3. Flash
task firmware:flash
```

The clean step is essential when switching overlays. Without it, stale sdkconfig values from the previous build leak into the new one.

---

## Full deployment workflow

Here's the complete sequence for deploying a new version to production:

```bash
# 0. Source ESP-IDF (needed for firmware steps)
. ~/esp/esp-idf/export.sh

# 1. Validate everything
task ci:all

# 2. Deploy the server to the Pi
task deploy:server

# 3. Build and flash production firmware
task firmware:clean
task firmware:build:prod
task firmware:flash-monitor

# 4. Verify in the serial monitor:
#    - WiFi connects
#    - Heartbeat OK with known=1
#    - Card tap produces access granted/denied
```

---

## Rollback

### Server rollback

If a deployment breaks the server, roll back by deploying the previous binary. Keep the last known-good binary on the Pi:

```bash
# The deploy task saves the previous binary automatically.
# To roll back manually:
ssh pi@192.168.1.100 << 'EOF'
  sudo systemctl stop portunus-server
  sudo cp /opt/portunus/bin/portunus-server.prev /opt/portunus/bin/portunus-server
  sudo systemctl start portunus-server
EOF
```

Database migrations are forward-only — they add tables, columns, and indexes but never drop or rename. This means a rollback to an older server binary still works with a database that has had newer migrations applied. The older binary simply ignores columns and tables it doesn't know about.

### Firmware rollback

There is no over-the-air rollback for the ESP32 at this stage (OTA is a planned future feature). To roll back firmware, rebuild the previous version from its git commit and reflash:

```bash
git checkout <previous-commit>
task firmware:clean
task firmware:build:prod
task firmware:flash
git checkout main
```

---

## Secret management

Three secrets must be coordinated between the server and firmware:

| Secret | Server config | Firmware config | How to generate |
|---|---|---|---|
| HMAC pre-shared key | `PORTUNUS_HMAC_SECRET` env var | `CONFIG_PORTUNUS_HMAC_SECRET` in Kconfig | `openssl rand -hex 32` |
| Admin API key | `PORTUNUS_ADMIN_API_KEY` env var | Not applicable (admin API is curl/browser only) | `openssl rand -hex 32` |
| TLS CA certificate | `ca.pem` used to sign `server.pem` | Embedded in firmware at `access_module/certs/ca_cert.pem` | `task certs:generate` |

### Per-environment secrets

Dev and prod should use **different** HMAC secrets. This prevents a dev-firmware ESP32 from accidentally authenticating against the production server (or vice versa).

Maintain two secrets:

```bash
# Generate once, store securely
openssl rand -hex 32   # → dev HMAC secret
openssl rand -hex 32   # → prod HMAC secret
```

- **Dev:** Set in the dev machine's server environment and in the dev sdkconfig overlay.
- **Prod:** Set in `/etc/portunus/portunus.env` on the Pi and in the prod sdkconfig overlay.

### TLS certificates per environment

If the dev machine and Pi have different IP addresses (they almost certainly do), each needs its own server certificate with the correct IP in the Subject Alternative Name. The CA can be shared (the same `ca.key` signs both), but the server cert must match the IP the ESP32 connects to.

```bash
# Generate certs for dev
task certs:generate -- --ip <DEV_MACHINE_IP>
# → certs/server.pem has SAN=<DEV_MACHINE_IP>
# → access_module/certs/ca_cert.pem updated

# Generate certs for prod (overwrites the above)
task certs:generate -- --ip <PROD_PI_IP>
# → certs/server.pem has SAN=<PROD_PI_IP>
# → access_module/certs/ca_cert.pem updated
# → Copy server.pem + server.key to the Pi
```

Since the cert generation script overwrites in place, you need to generate and deploy each environment's certs in sequence. If you find yourself switching frequently, consider keeping environment-specific cert directories (e.g., `certs/dev/` and `certs/prod/`) and copying the appropriate files before building.

---

## New Taskfile targets

The following targets need to be added to `Taskfile.yml` to support the pipeline. Add them under the existing server and CI sections.

### Cross-compilation

```yaml
  build:server:arm64:
    desc: Cross-compile server binary for Raspberry Pi (arm64)
    dir: "{{.SERVER_DIR}}"
    env:
      GOOS: linux
      GOARCH: arm64
    cmds:
      - go build -o bin/portunus-server-linux-arm64 ./cmd/portunus-server
```

### Deployment

These targets use environment variables from a `.env` file or shell exports. Add the `dotenv` directive at the top level of the Taskfile if you want `.env` auto-loading:

```yaml
# Add near the top of Taskfile.yml, below 'version: "3"'
dotenv: ['.env']
```

Then add the deployment targets:

```yaml
  deploy:server:
    desc: Build, copy, and restart the server on the Raspberry Pi
    deps: [build:server:arm64]
    vars:
      DEPLOY_HOST: '{{.DEPLOY_HOST | default "pi@192.168.1.100"}}'
      DEPLOY_DIR: '{{.DEPLOY_DIR | default "/opt/portunus"}}'
      DEPLOY_SERVICE: '{{.DEPLOY_SERVICE | default "portunus-server"}}'
    cmds:
      - echo "Deploying to {{.DEPLOY_HOST}}:{{.DEPLOY_DIR}}"
      # Back up current binary on the Pi
      - ssh {{.DEPLOY_HOST}} "sudo cp {{.DEPLOY_DIR}}/bin/portunus-server {{.DEPLOY_DIR}}/bin/portunus-server.prev 2>/dev/null || true"
      # Copy new binary
      - scp {{.SERVER_DIR}}/bin/portunus-server-linux-arm64 {{.DEPLOY_HOST}}:/tmp/portunus-server
      # Move into place and restart
      - ssh {{.DEPLOY_HOST}} "sudo mv /tmp/portunus-server {{.DEPLOY_DIR}}/bin/portunus-server && sudo chmod 755 {{.DEPLOY_DIR}}/bin/portunus-server && sudo systemctl restart {{.DEPLOY_SERVICE}}"
      # Verify
      - ssh {{.DEPLOY_HOST}} "sudo systemctl is-active {{.DEPLOY_SERVICE}}"
      - echo "Deploy complete"

  deploy:server:status:
    desc: Check the server status on the Raspberry Pi
    vars:
      DEPLOY_HOST: '{{.DEPLOY_HOST | default "pi@192.168.1.100"}}'
      DEPLOY_SERVICE: '{{.DEPLOY_SERVICE | default "portunus-server"}}'
    cmds:
      - ssh {{.DEPLOY_HOST}} "sudo systemctl status {{.DEPLOY_SERVICE}} --no-pager"

  deploy:server:logs:
    desc: Tail server logs on the Raspberry Pi
    vars:
      DEPLOY_HOST: '{{.DEPLOY_HOST | default "pi@192.168.1.100"}}'
      DEPLOY_SERVICE: '{{.DEPLOY_SERVICE | default "portunus-server"}}'
    cmds:
      - ssh {{.DEPLOY_HOST}} "sudo journalctl -u {{.DEPLOY_SERVICE}} -f"

  deploy:server:rollback:
    desc: Roll back to the previous server binary on the Raspberry Pi
    vars:
      DEPLOY_HOST: '{{.DEPLOY_HOST | default "pi@192.168.1.100"}}'
      DEPLOY_DIR: '{{.DEPLOY_DIR | default "/opt/portunus"}}'
      DEPLOY_SERVICE: '{{.DEPLOY_SERVICE | default "portunus-server"}}'
    cmds:
      - ssh {{.DEPLOY_HOST}} "sudo systemctl stop {{.DEPLOY_SERVICE}} && sudo cp {{.DEPLOY_DIR}}/bin/portunus-server.prev {{.DEPLOY_DIR}}/bin/portunus-server && sudo systemctl start {{.DEPLOY_SERVICE}}"
      - ssh {{.DEPLOY_HOST}} "sudo systemctl is-active {{.DEPLOY_SERVICE}}"
      - echo "Rollback complete"
```

### Full CI check

```yaml
  ci:all:
    desc: Full CI — server checks + proto drift + firmware compile
    cmds:
      - task: ci:server
      - task: proto:check
      - task: firmware:build
```

### Full deploy workflow

```yaml
  release:
    desc: Full release — validate, deploy server, build prod firmware
    cmds:
      - task: ci:all
      - task: deploy:server
      - task: firmware:clean
      - task: firmware:build:prod
      - echo "Server deployed. Firmware built. Flash with 'task firmware:flash'"
```

---

## .env file

Create this file in the repo root. It is gitignored and contains machine-specific deployment config:

```bash
# .env — local deployment config (do not commit)
#
# Used by: task deploy:server, deploy:server:status, deploy:server:logs
DEPLOY_HOST=pi@192.168.1.100
DEPLOY_DIR=/opt/portunus
DEPLOY_SERVICE=portunus-server
```

Add `.env` to `.gitignore` if it's not already there:

```bash
echo '.env' >> .gitignore
```

---

## Pipeline summary

| Command | What it does | When to use |
|---|---|---|
| `task ci:all` | Validate everything (tests, lint, proto, firmware compile) | Before every commit or deploy |
| `task build:server` | Build server for dev machine (x86_64) | Local dev testing |
| `task build:server:arm64` | Cross-compile server for Pi (arm64) | Before deploying to Pi |
| `task deploy:server` | Build arm64 + copy to Pi + restart service | Deploying a new server version |
| `task deploy:server:status` | Check server status on Pi | After deploy or to diagnose issues |
| `task deploy:server:logs` | Tail server logs on Pi | Debugging production |
| `task deploy:server:rollback` | Revert to previous server binary on Pi | Deploy broke something |
| `task firmware:build:dev` | Build firmware with dev settings | Testing against dev server |
| `task firmware:build:prod` | Build firmware with prod settings | Preparing for production |
| `task firmware:flash` | Flash firmware to connected ESP32 | After building firmware |
| `task firmware:flash-monitor` | Flash and open serial monitor | Flash + verify in one step |
| `task release` | Full pipeline: validate → deploy server → build prod firmware | Production release |