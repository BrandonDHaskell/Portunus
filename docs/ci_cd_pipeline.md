# Portunus: CI/CD Pipeline

How Portunus is validated, built, deployed and rolled back.

**Last updated:** November 2026

---

## Overview

| Machine | Role | Architecture |
|---|---|---|
| GitHub Actions | CI on every pull request and every push to `main` | x86_64 |
| Dev machine (Debian) | Local validation, builds, deploys, firmware flashing | x86_64 |
| Raspberry Pi 5 | Production server (systemd) | arm64 |
| ESP32-S3 | Door access module | Xtensa LX7 |

Everything is driven by [Task](https://taskfile.dev) targets in `Taskfile.yml`. CI (`.github/workflows/ci.yml`) calls the same tasks, so `task ci:all` on the dev machine runs the same checks as CI. The Pi never compiles anything: it receives a cross-compiled binary over SSH. Modules are flashed from the dev machine over USB.

```
  pull request ──► GitHub Actions (same tasks as ci:all) ──► merge to main
                                                                 │
  dev machine:  task release  (clean tree only)                  ▼
     1. task ci:all                    validate
     2. task firmware:build:prod       build + secure-build assertion
     3. task deploy:server             arm64 build ──SSH──► Pi (snapshot, install, health check)
  then:  task firmware:flash           ──USB──► ESP32  (and NVS if device config changed)
```

---

## Continuous integration

`.github/workflows/ci.yml` runs six jobs. Their names are required status checks on `main`, so renaming a job means updating the branch ruleset too.

| Job | Task(s) | Catches |
|---|---|---|
| Server | `ci:server`, `tidy:server:check`, `build:server:arm64`, `vuln:server` | vet, gofmt, tests with the race detector, TLS boot smoke, untidy `go.mod`, a CGo dependency that breaks the Pi build, known vulnerabilities reachable from server code |
| Proto drift | `proto:check` | generated Go or Nanopb code that does not match `portunus.proto` (including new, uncommitted generated files) |
| Firmware | `firmware:compile:ci` | firmware that does not compile |
| Firmware unit tests | `test:firmware:unit` (native, ASan+UBSan, 32-bit) | unit-tier failures, memory errors, ILP32 data-model bugs |
| Firmware component + integration tests | `test:firmware:component`, `test:firmware:integration`, `test:firmware:hardware:compile` | component/integration failures on the IDF linux target; hardware-tier apps that no longer compile |
| Firmware compile (prod profile) | `firmware:compile:prod` | a prod image that does not compile, or whose compiled config is not deployable (dev mode, skipped cert verification, TLS/HMAC off, CA not pinned) |

Pinning, so CI results are reproducible:

- Actions are pinned to commit SHAs, the `espressif/idf` image to a digest, Task to an exact version, and the runner to `ubuntu-24.04`. Dependabot (`.github/dependabot.yml`) proposes action and Go module updates. Bump the IDF image digest by hand when moving ESP-IDF.
- The protoc plugins are `tool` directives in `server/go.mod`, built by `proto_gen.py` with the Go toolchain `go.mod` names. protoc itself must match the version recorded in the committed stubs (3.21.12, what Ubuntu 24.04 and Debian trixie ship). A Dependabot bump of a plugin changes generated code: run `task proto:gen` on that branch and commit the result.
- The Server job uses the current stable Go, so tests, vet and govulncheck see the standard library the server is actually built with. Keep the dev machine's Go current too: release binaries are built there.

---

## Prerequisites

- **Dev machine:** Go (current stable), ESP-IDF 5.5.6, Task 3.x. See [Server Setup](setup_server.md) and [Firmware Setup](setup_firmware.md).
- **Raspberry Pi:** the systemd service and the `portunus` user, per [Server Setup: Running as a systemd service](setup_server.md#running-as-a-systemd-service).
- **SSH key auth and passwordless `sudo`** from the dev machine to the Pi (the deploy runs its remote steps with `sudo`).

```bash
ssh-keygen -t ed25519 -C "portunus-deploy"    # skip if you have a key
ssh-copy-id <user>@<PI_IP>
ssh <user>@<PI_IP> "sudo -n true && echo ok"  # must print ok without a prompt
```

The deploy target lives in the repo-root `.env` (gitignored, loaded by the Taskfile), or pass it on the command line (`task deploy:server DEPLOY_HOST=user@host`). There is no default host: the deploy tasks refuse to run without one.

```bash
# .env (repo root)
DEPLOY_HOST=pi@192.168.1.100
DEPLOY_DIR=/opt/portunus           # default
DEPLOY_SERVICE=portunus-server     # default
```

---

## Stage 1: Validate

```bash
task ci:all
```

This runs every CI job's tasks in sequence: the server checks, tidy and vulnerability checks, the arm64 cross-compile, the proto drift check, every firmware test tier (including ASan and 32-bit), the hardware-tier compile, and the CI and prod firmware compiles. The compile checks use their own build directories (`access_module/build/ci`, `build/ci-prod`), so they never touch your working `sdkconfig` or `build/`. The prod compile needs `access_module/certs/ca_cert.pem` (your CA, from `task certs:generate`).

Faster loops while developing:

```bash
task test:server              # server tests, no race detector
task test:server:service      # service layer only
task test:firmware:unit       # firmware unit tier, no ESP-IDF needed
```

---

## Stage 2: Build

```bash
task build:server             # server/bin/portunus-server (dev machine)
task build:server:arm64       # server/bin/portunus-server-linux-arm64 (Pi), CGO_ENABLED=0
task firmware:build:prod      # access_module/build/, prod overlay
task firmware:build:dev       # dev overlay
```

`firmware:build:prod` and `firmware:build:dev` always start from their overlay (they delete `sdkconfig` first, after saving your per-device hardware settings to `sdkconfig.defaults.local`), so no clean is needed when switching. `firmware:build:prod` then runs `scripts/assert_secure_build.sh --prod` on the compiled `sdkconfig.h` and fails if the image is not deployable.

No secret is compiled into the firmware. The module ID, WiFi credentials, server host, gRPC port and HMAC secret are read from NVS at boot (see [Firmware Setup: Device configuration and secrets](setup_firmware.md#device-configuration-and-secrets-nvs)).

---

## Stage 3: Deploy the server

```bash
task deploy:server
```

The steps are in `scripts/deploy_server.sh` (dev machine) and `scripts/deploy_remote.sh` (runs on the Pi as root):

1. Cross-compiles the arm64 binary and records its git version (`git describe --dirty`).
2. Copies it to a private `mktemp -d` directory on the Pi and verifies its SHA-256 there.
3. If the running service is healthy, keeps its binary as the rollback target (`bin/portunus-server.prev`). A binary that is not running healthily never replaces the rollback target, so two bad deploys in a row keep the last good one.
4. Stops the service and snapshots the database (the file plus `-wal`/`-shm`) to `<db dir>/backups/pre-deploy-<timestamp>-<version>/`. The DB path is `PORTUNUS_DB_PATH` from the unit's `EnvironmentFile`; override with `DEPLOY_DB`.
5. Installs the binary as root, records the version in `/opt/portunus/deploy/version`, starts the service.
6. Health check: the service must stay active with the same main PID for 10 seconds (`HEALTH_SECONDS`). If it does not, the previous binary goes back automatically and the deploy fails, showing the last journal lines.

The server is down from step 4 until it is back up (a few seconds), so doors cannot get decisions during a deploy. Schema migrations run on startup.

Only the binary is deployed. The database, TLS certs, `/etc/portunus/portunus.env` and the unit file stay on the Pi.

Snapshots accumulate in `<db dir>/backups/`. They are small, but prune old ones by hand now and then.

### Checking a deployment

```bash
task deploy:server:status     # systemd status + deployed version
task deploy:server:logs       # follow the journal
curl -sS --cacert certs/ca.pem -o /dev/null -w '%{http_code}\n' https://<PI_IP>:8443/admin/ui/login   # 200
```

---

## Stage 4: Flash firmware

```bash
task firmware:flash-monitor
```

If the module's device config (WiFi, server host, module ID, HMAC secret) is new or changed, write NVS first: `task firmware:nvs:gen` then `task firmware:nvs:flash`. Flashing the app does not touch NVS.

Verify on the serial monitor: WiFi connects, heartbeat succeeds with `known=1`, and a card tap produces a decision (ACCESS_POINT) or a pending authorization (PROVISIONING_CONSOLE).

The dev and prod profiles differ in compiled security settings only; which server a module talks to is its NVS config. To move a module between environments, rebuild with the other profile and write that environment's NVS image.

---

## Full release

```bash
task release
```

Refuses to run with uncommitted or untracked changes (so the deployed version names a real commit) and without a `DEPLOY_HOST`. Then: `ci:all`, a clean prod firmware build with the secure-build assertion, and only then `deploy:server`. A firmware failure therefore stops the release before the server changes. Flash modules afterwards with `task firmware:flash`.

---

## Rollback

### Server

```bash
task deploy:server:rollback               # previous binary
task deploy:server:rollback RESTORE_DB=1  # previous binary + pre-deploy DB snapshot
```

Migrations are not all additive: several drop or rebuild tables (for example `0016_drop_credentials_table`, `0018_drop_admin_user_credentials`, and the rebuilds in 0022 to 0024). An older binary on a newer schema may fail or misbehave. When the version you are rolling back ran a migration, use `RESTORE_DB=1`. That restores the snapshot taken just before that deploy and **discards every write made since** (member changes, audit log). The current files are moved aside as `*.rolled-back-<timestamp>`, not deleted.

A failed deploy already put the previous binary back on its own, but it does not restore the database. If the failed version got as far as migrating, follow up with `RESTORE_DB=1`.

### Firmware

There is no OTA rollback yet. Rebuild the previous version from git and reflash:

```bash
git checkout <previous-commit>
task firmware:build:prod
task firmware:flash
git checkout main
```

---

## Secrets and certificates

| Secret | Server | Module | Generate |
|---|---|---|---|
| HMAC pre-shared key | `PORTUNUS_HMAC_SECRET` in the server env | `hmac_secret` in NVS (`nvs_config.csv`) | `openssl rand -hex 32` |
| Credential hash secret | `PORTUNUS_CREDENTIAL_HASH_SECRET` | not used | `openssl rand -hex 32` |
| TLS CA | signs `server.pem` | pinned: embedded from `access_module/certs/ca_cert.pem` at build | `task certs:generate` (first run only) |

Use different values per environment, and never reuse one value for both secrets. The credential hash secret is permanent once any card is enrolled. To compare a secret between places, compare SHA-256 fingerprints; never print the value. Details: [Shared secrets setup](shared_secrets_setup.md).

**The CA is long-lived.** Every module pins it, so a new CA means reflashing every module. `task certs:generate` creates the CA on the first run, and afterwards refuses unless told which of these you mean:

```bash
# New server cert from the existing CA: new IP, extra SAN, renewal. No reflash.
task certs:generate -- --sign-only --ip <SERVER_IP> --san DNS:localhost --san IP:127.0.0.1

# Deliberate CA rotation. Old material moves to certs/backup-<timestamp>/.
task certs:generate -- --new-ca --ip <SERVER_IP>
```

One CA serves every environment: give the dev machine and the Pi each their own server cert with `--sign-only` (copy `certs/server.*` away between runs, since each run replaces them and keeps the previous pair as `server.{pem,key}.<timestamp>`).

---

## Command summary

| Command | What it does |
|---|---|
| `task ci:all` | Everything CI runs, locally |
| `task build:server:arm64` | Cross-compile the server for the Pi |
| `task deploy:server` | Deploy with SHA check, DB snapshot, health check, automatic binary rollback |
| `task deploy:server:status` / `:logs` | Status and deployed version / follow the journal |
| `task deploy:server:rollback [RESTORE_DB=1]` | Previous binary [and pre-deploy DB snapshot] |
| `task firmware:build:prod` / `:dev` | Profile builds (prod asserts the image is deployable) |
| `task firmware:nvs:gen` / `:nvs:flash` | Build and write the device-config NVS image |
| `task firmware:flash-monitor` | Flash the app and open the serial monitor |
| `task certs:generate -- ...` | First CA, `--sign-only` server cert, or `--new-ca` rotation |
| `task release` | Clean tree only: validate, build prod firmware, deploy the server |
