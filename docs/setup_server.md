# Portunus — Server Setup

Get the Portunus server running on a Debian-based Linux machine (desktop dev box or Raspberry Pi 5).

**Last updated:** April 2026

---

## Prerequisites at a glance

Before starting, confirm the following are installed or available. Each is covered in detail in the sections that follow.

| Dependency | Minimum version | Purpose | Install section |
|---|---|---|---|
| Go | 1.24+ | Build and run the server | [Install Go](#install-go) |
| Task | 3.x | Task runner (build, test, lint commands) | [Install Task](#install-task) |
| protoc | 3.21+ | Regenerate protobuf code (only if `.proto` files change) | [Protobuf tooling](#protobuf-tooling-optional) |
| protoc-gen-go / protoc-gen-go-grpc | latest | Go code generation from `.proto` | [Protobuf tooling](#protobuf-tooling-optional) |
| openssl | 1.1.1+ | Generate TLS certificates | [TLS certificate setup](#tls-certificate-setup) |
| Git | any | Clone the repository | Usually pre-installed on Debian |

The server uses **modernc.org/sqlite** (a pure-Go SQLite implementation), so there is no system-level SQLite library dependency. No C compiler or CGo is required.

---

## Install Go

The server requires Go 1.24 or newer. The version in Debian's default apt repositories is typically outdated, so install from the official tarball.

Check if Go is already installed and at the right version:

```bash
go version
```

If missing or below 1.24, install from the official release:

```bash
# Download (adjust version as needed — check https://go.dev/dl/)
wget https://go.dev/dl/go1.24.1.linux-amd64.tar.gz

# For Raspberry Pi 5 (arm64):
# wget https://go.dev/dl/go1.24.1.linux-arm64.tar.gz

# Remove any prior install and extract
sudo rm -rf /usr/local/go
sudo tar -C /usr/local -xzf go1.24.1.linux-amd64.tar.gz

# Add to PATH (append to ~/.bashrc or ~/.profile for persistence)
export PATH=$PATH:/usr/local/go/bin
export PATH=$PATH:$(go env GOPATH)/bin

# Verify
go version
```

Ensure the `export PATH` lines are in your shell profile so they persist across sessions.

---

## Install Task

[Task](https://taskfile.dev) is used as the project's build/test runner. The `Taskfile.yml` in the repo root defines all build, test, lint, and CI commands.

```bash
go install github.com/go-task/task/v3/cmd/task@latest
```

Verify it's on your PATH:

```bash
task --version
```

If `task` is not found, confirm `$(go env GOPATH)/bin` is in your PATH (see the Go install step above).

---

## Clone and build

```bash
git clone https://github.com/BrandonDHaskell/Portunus.git
cd Portunus

# Run tests first to confirm the toolchain is working
task test:server

# Build the binary
task build:server
```

The compiled binary is written to `server/bin/portunus-server`.

---

## Configuration

The server is configured entirely through environment variables. No config file is required. All variables have sensible defaults for local development.

### Required for production

| Variable | Description | Example |
|---|---|---|
| `PORTUNUS_ENV` | `dev` or `prod`. Dev mode seeds the database with a default module and door on startup. Prod mode skips seeding. | `prod` |
| `PORTUNUS_HMAC_SECRET` | Pre-shared key for HMAC-SHA256 request signing. Every device endpoint POST must include a valid `X-Portunus-Sig` header computed with this secret. Must match `CONFIG_PORTUNUS_HMAC_SECRET` in the firmware. Generate with `openssl rand -hex 32`. | `a3f1...` (64 hex chars) |
| `PORTUNUS_CREDENTIAL_HASH_SECRET` | HMAC key used to hash credential IDs before storage. Prevents rainbow-table attacks against a stolen database. Generate with `openssl rand -hex 32`. Required in prod — server refuses to start without it. | `c9d4...` (64 hex chars) |
| `PORTUNUS_TLS_CERT_FILE` | Path to the PEM-encoded server certificate. | `./certs/server.pem` |
| `PORTUNUS_TLS_KEY_FILE` | Path to the PEM-encoded server private key. | `./certs/server.key` |

### Optional

| Variable | Default | Description |
|---|---|---|
| `PORTUNUS_HTTP_ADDR` | `:8080` | Listen address for the HTTP/HTTPS server. |
| `PORTUNUS_GRPC_ADDR` | *(empty — disabled)* | Listen address for the gRPC server. Set to `:50051` to enable. |
| `PORTUNUS_DB_PATH` | `./data/portunus.db` | Path to the SQLite database file. The parent directory is created automatically. |
| `PORTUNUS_KNOWN_MODULES` | *(empty)* | Comma-separated module IDs to pre-register in dev mode (e.g. `door-001,door-002`). Only used when `PORTUNUS_ENV=dev`. |
| `PORTUNUS_ALLOW_ALL` | `false` | When `true`, all credentials are granted access. **Dev/testing only.** |
| `PORTUNUS_ALLOWED_CREDENTIAL_IDS` | *(empty)* | Comma-separated raw credential IDs for the legacy env-var allowlist. Superseded by the DB-backed member + authorization path. |
| `PORTUNUS_HEARTBEAT_RETENTION_DAYS` | `30` | Heartbeat records older than this are pruned automatically. Set to `0` to keep forever. |
| `PORTUNUS_PRUNE_INTERVAL_HOURS` | `6` | How often the background heartbeat pruner runs. |
| `PORTUNUS_EXPIRY_WORKER_INTERVAL_MINUTES` | `60` | How often the background member expiry sweep runs. |

### Dev quick-start (minimal)

For local development with no TLS and no auth enforcement, you can run with just:

```bash
cd server
PORTUNUS_ENV=dev PORTUNUS_ALLOW_ALL=true go run ./cmd/portunus-server
```

This starts the server on `:8080` with plain HTTP, seeds a default `door-001` module, and grants all credential checks. The database is created at `./data/portunus.db`.

### Production example

```bash
export PORTUNUS_ENV=prod
export PORTUNUS_HTTP_ADDR=:8443
export PORTUNUS_GRPC_ADDR=:50051
export PORTUNUS_DB_PATH=/var/lib/portunus/portunus.db
export PORTUNUS_TLS_CERT_FILE=/etc/portunus/certs/server.pem
export PORTUNUS_TLS_KEY_FILE=/etc/portunus/certs/server.key
export PORTUNUS_HMAC_SECRET=<your-64-char-hex-secret>
export PORTUNUS_CREDENTIAL_HASH_SECRET=<your-64-char-hex-secret>
export PORTUNUS_HEARTBEAT_RETENTION_DAYS=90

./server/bin/portunus-server
```

When both `PORTUNUS_TLS_CERT_FILE` and `PORTUNUS_TLS_KEY_FILE` are set, the server starts in HTTPS mode automatically. When `PORTUNUS_GRPC_ADDR` is set, a gRPC listener starts on that address alongside the HTTP server, sharing the same TLS certificate.

---

## Database

The server uses SQLite via a pure-Go driver (`modernc.org/sqlite`). There is nothing to install — the driver is compiled into the binary.

On first startup, the server creates the database file at `PORTUNUS_DB_PATH` (default `./data/portunus.db`), creates the parent directory if needed, and applies all schema migrations automatically. Migrations are embedded in the binary and run inside transactions, so a failed migration is rolled back cleanly.

Database pragmas applied per-connection: `foreign_keys=ON`, `journal_mode=WAL`, `synchronous=NORMAL`, `busy_timeout=5000ms`.

All write operations are serialized through a single-goroutine worker to avoid SQLite's "database is locked" errors under concurrent access. Read operations run directly on the connection pool (pool size = 1 for SQLite safety).

### Backup

The database is a single file. To back up a running server, use SQLite's `.backup` command or simply copy the file while the server is stopped:

```bash
cp /var/lib/portunus/portunus.db /var/lib/portunus/backups/portunus-$(date +%Y%m%d).db
```

For online backup without stopping the server (requires the `sqlite3` CLI tool):

```bash
sqlite3 /var/lib/portunus/portunus.db ".backup '/var/lib/portunus/backups/portunus-$(date +%Y%m%d).db'"
```

---

## TLS certificate setup

Production deployments should always use TLS. The repo includes a script that generates a private Certificate Authority and a server certificate signed by that CA. The CA certificate is then embedded into the ESP32 firmware for certificate pinning.

```bash
# From the repo root:
task certs:generate -- --ip 192.168.1.100

# Or with a DNS name:
task certs:generate -- --ip 192.168.1.100 --dns portunus.local
```

This creates the following files in `certs/`:

| File | Purpose | Keep secret? |
|---|---|---|
| `ca.key` | CA private key | **Yes** — only needed to sign new server certs |
| `ca.pem` | CA certificate | No — embedded in firmware for pinning |
| `server.key` | Server private key | **Yes** — used by `PORTUNUS_TLS_KEY_FILE` |
| `server.pem` | Server certificate | No — used by `PORTUNUS_TLS_CERT_FILE` |

The script also copies `ca.pem` to `access_module/certs/ca_cert.pem` so the firmware build can embed it automatically.

To verify the certificate chain:

```bash
task certs:verify
```

For production on the Raspberry Pi, copy `server.pem` and `server.key` to a secure location (e.g. `/etc/portunus/certs/`) and set the environment variables accordingly. Restrict permissions:

```bash
sudo mkdir -p /etc/portunus/certs
sudo cp certs/server.pem certs/server.key /etc/portunus/certs/
sudo chmod 600 /etc/portunus/certs/server.key
sudo chmod 644 /etc/portunus/certs/server.pem
```

---

## Protobuf tooling (optional)

Protobuf code generation is only needed if you modify `proto/portunus/v1/portunus.proto`. The generated Go and Nanopb files are committed to the repo, so most users can skip this section entirely.

If you do need to regenerate:

```bash
# Install protoc (the protobuf compiler)
sudo apt install -y protobuf-compiler

# Install Go protobuf plugins
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest

# Regenerate both Go and Nanopb stubs
task proto:gen

# Or Go only / Nanopb only
task proto:gen:go
task proto:gen:nanopb

# CI check: regenerate and fail if output differs from committed files
task proto:check
```

The Nanopb generator (`nanopb_generator` or `protoc-gen-nanopb`) must also be installed for ESP32 stub generation. See the firmware setup guide for Nanopb installation.

---

## Running as a systemd service

For production deployments on the Raspberry Pi (or any Debian server), run Portunus as a systemd service so it starts on boot and restarts on failure.

Create the service file:

```bash
sudo tee /etc/systemd/system/portunus-server.service > /dev/null << 'EOF'
[Unit]
Description=Portunus Access Control Server
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=portunus
Group=portunus
WorkingDirectory=/opt/portunus

ExecStart=/opt/portunus/bin/portunus-server
Restart=on-failure
RestartSec=5

# Environment — override these in an environment file for production
EnvironmentFile=/etc/portunus/portunus.env

# Hardening
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/var/lib/portunus
PrivateTmp=true

[Install]
WantedBy=multi-user.target
EOF
```

Create the service user and directories:

```bash
# Create a system user with no login shell
sudo useradd --system --no-create-home --shell /usr/sbin/nologin portunus

# Create data and config directories
sudo mkdir -p /opt/portunus/bin
sudo mkdir -p /var/lib/portunus
sudo mkdir -p /etc/portunus/certs

# Copy the binary
sudo cp server/bin/portunus-server /opt/portunus/bin/
sudo chmod 755 /opt/portunus/bin/portunus-server

# Set ownership
sudo chown -R portunus:portunus /var/lib/portunus
```

Create the environment file (`/etc/portunus/portunus.env`):

```bash
sudo tee /etc/portunus/portunus.env > /dev/null << 'EOF'
PORTUNUS_ENV=prod
PORTUNUS_HTTP_ADDR=:8443
PORTUNUS_GRPC_ADDR=:50051
PORTUNUS_DB_PATH=/var/lib/portunus/portunus.db
PORTUNUS_TLS_CERT_FILE=/etc/portunus/certs/server.pem
PORTUNUS_TLS_KEY_FILE=/etc/portunus/certs/server.key
PORTUNUS_HMAC_SECRET=<your-secret-here>
PORTUNUS_CREDENTIAL_HASH_SECRET=<your-credential-hash-secret-here>
PORTUNUS_HEARTBEAT_RETENTION_DAYS=90
EOF

# Restrict permissions on the env file (it contains secrets)
sudo chmod 600 /etc/portunus/portunus.env
sudo chown portunus:portunus /etc/portunus/portunus.env
```

Enable and start:

```bash
sudo systemctl daemon-reload
sudo systemctl enable portunus-server
sudo systemctl start portunus-server

# Check status
sudo systemctl status portunus-server

# Follow logs
sudo journalctl -u portunus-server -f
```

---

## Verifying the server

Once the server is running, verify it responds correctly.

### Health check (heartbeat endpoint)

Without HMAC enforcement (dev mode):

```bash
curl -s -X POST http://localhost:8080/v1/heartbeat \
  -H "Content-Type: application/json" \
  -d '{"module_id": "door-001", "firmware_version": "test", "uptime_s": 0}' | jq .
```

Expected response:

```json
{
  "ok": true,
  "known": true,
  "module_id": "door-001",
  "server_time": "2026-03-26T..."
}
```

With HMAC enforcement (production):

```bash
BODY='{"module_id":"door-001","firmware_version":"test","uptime_s":0}'
SIG=$(echo -n "$BODY" | openssl dgst -sha256 -hmac "$PORTUNUS_HMAC_SECRET" | awk '{print $2}')

curl -s -X POST https://localhost:8443/v1/heartbeat \
  --cacert certs/ca.pem \
  -H "Content-Type: application/json" \
  -H "X-Portunus-Sig: $SIG" \
  -d "$BODY" | jq .
```

### Admin API

The admin API uses session-cookie authentication. Before you can log in, you need the bootstrap password the server generated on first start.

#### First-run admin credentials

On the very first startup, if no admin account exists, the server creates one and prints a randomly generated password to stdout:

```
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
FIRST RUN — initial admin account created
  username: admin
  password: <randomly generated>
  You will be required to change this password on first login.
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
```

Copy that password before it scrolls away. If you are running as a systemd service, retrieve it from the journal:

```bash
sudo journalctl -u portunus-server | grep -A4 "FIRST RUN"
```

The account has `must_change_pw` set, which means the server will reject every admin request — including all admin UI pages — until you change the password. See [Change the bootstrap password](#change-the-bootstrap-password) below.

Log in and save the session cookie:

```bash
curl -s -c /tmp/portunus-cookies.txt \
  -X POST https://localhost:8443/admin/v1/login \
  --cacert certs/ca.pem \
  -H "Content-Type: application/json" \
  -d '{"username": "admin", "password": "<password from server output>"}' | jq .
```

Register a credential (using the saved cookie):

```bash
curl -s -b /tmp/portunus-cookies.txt \
  -X POST https://localhost:8443/admin/v1/credentials \
  --cacert certs/ca.pem \
  -H "Content-Type: application/json" \
  -d '{"credential_id": "04:A3:2B:1C", "tag": "Brandon front-door key"}' | jq .
```

List modules:

```bash
curl -s -b /tmp/portunus-cookies.txt \
  https://localhost:8443/admin/v1/modules \
  --cacert certs/ca.pem | jq .
```

Log out:

```bash
curl -s -b /tmp/portunus-cookies.txt \
  -X POST https://localhost:8443/admin/v1/logout \
  --cacert certs/ca.pem | jq .
```

For plain HTTP dev mode, omit `--cacert certs/ca.pem` and replace `https://localhost:8443` with `http://localhost:8080`.

#### Change the bootstrap password

This step is required before any other admin operation will work. The server enforces it — requests will return `403` until the password is changed.

Via the admin web UI (simplest):

```
http://localhost:8080/admin/ui/change-password    # dev
https://localhost:8443/admin/ui/change-password   # production
```

Or via the API:

```bash
curl -s -b /tmp/portunus-cookies.txt \
  -X POST https://localhost:8443/admin/v1/change-password \
  --cacert certs/ca.pem \
  -H "Content-Type: application/json" \
  -d '{"current_password": "<bootstrap password>", "new_password": "<your new password>"}' | jq .
```

Once the password is changed, `must_change_pw` is cleared and the admin API and UI are fully accessible.

See [docs/api.md](api.md) for the full endpoint reference.

---

## Register an access module

Before an ESP32 module can receive access decisions from the server, it must be registered (commissioned). The server rejects heartbeats and access requests from unregistered modules with `unknown_module`.

### Dev mode — automatic

When `PORTUNUS_ENV=dev`, the server auto-seeds a module with `module_id: door-001` on startup. If your firmware's `CONFIG_PORTUNUS_MODULE_ID` is set to `door-001`, no manual registration is needed during development.

### Production mode — manual registration required

In production, all modules must be registered via the admin API. The `module_id` in the request **must exactly match** `CONFIG_PORTUNUS_MODULE_ID` as configured in the firmware's `menuconfig`.

Log in and obtain a session cookie first (see [First-run admin credentials](#first-run-admin-credentials) above), then:

```bash
curl -s -b /tmp/portunus-cookies.txt \
  -X POST https://localhost:8443/admin/v1/modules \
  --cacert certs/ca.pem \
  -H "Content-Type: application/json" \
  -d '{
    "module_id": "door-001",
    "door_id": "door_main",
    "display_name": "Main entrance"
  }' | jq .
```

| Field | Description | Must match |
|---|---|---|
| `module_id` | Unique identifier for this device | `CONFIG_PORTUNUS_MODULE_ID` in firmware menuconfig |
| `door_id` | Logical door identifier (used for authorizations) | Any string you choose |
| `display_name` | Human-readable label shown in the admin UI | Any string you choose |

Expected response (201):

```json
{
  "ok": true,
  "module": {
    "module_id": "door-001",
    "door_id": "door_main",
    "display_name": "Main entrance",
    "enabled": true,
    "commissioned": true,
    "commissioned_at": "2026-04-28T..."
  }
}
```

Once registered, the module's heartbeats will show `"known": true` and access requests will be evaluated against the credential policy.

To confirm a module is commissioned, list all modules:

```bash
curl -s -b /tmp/portunus-cookies.txt \
  https://localhost:8443/admin/v1/modules \
  --cacert certs/ca.pem | jq .
```

For plain HTTP dev mode, omit `--cacert certs/ca.pem` and replace `https://localhost:8443` with `http://localhost:8080`.

---

## Dev vs. production differences

| Concern | Dev | Production |
|---|---|---|
| `PORTUNUS_ENV` | `dev` | `prod` |
| TLS | Optional (plain HTTP on `:8080`) | Required (`PORTUNUS_TLS_CERT_FILE` + `PORTUNUS_TLS_KEY_FILE`) |
| HMAC signing | Optional (can omit `PORTUNUS_HMAC_SECRET`) | Required — rejects unsigned device requests with 401 |
| Credential hash secret | Optional (can omit `PORTUNUS_CREDENTIAL_HASH_SECRET`) | Required — server refuses to start without it |
| Admin API auth | Session-cookie auth always active; bootstrap account created on first start | Session-cookie auth; change bootstrap password on first login (`must_change_pw` enforced) |
| Database seeding | Auto-seeds a default door and module(s) on startup | No seeding — all entities created via admin API |
| `PORTUNUS_ALLOW_ALL` | Useful for testing (grants all credentials) | Must be `false` or unset |
| Database path | `./data/portunus.db` (relative to working dir) | `/var/lib/portunus/portunus.db` (absolute, owned by service user) |
| Process management | `go run` or `./bin/portunus-server` in a terminal | systemd service with auto-restart |
| Logging | Visible in terminal stdout | `journalctl -u portunus-server` |

---

## Available task commands

All server-related commands from the project `Taskfile.yml`:

| Command | Description |
|---|---|
| `task build:server` | Build the server binary to `server/bin/portunus-server` |
| `task test:server` | Run all server tests |
| `task test:server:verbose` | Run tests with verbose output |
| `task test:server:race` | Run tests with Go's race detector |
| `task test:server:coverage` | Run tests with coverage report |
| `task test:server:service` | Run service-layer tests only |
| `task test:server:http` | Run HTTP handler tests only |
| `task test:server:store` | Run SQLite store tests only |
| `task vet:server` | Run `go vet` static analysis |
| `task fmt:server` | Check formatting (fails if unformatted) |
| `task fmt:server:fix` | Auto-format with `gofmt` |
| `task lint:server` | Run `golangci-lint` (requires separate install) |
| `task clean:server` | Remove build artifacts |
| `task ci:server` | Full CI check: vet + format + test with race detector |
| `task certs:generate` | Generate TLS certificates |
| `task certs:verify` | Verify the certificate chain |
| `task proto:gen` | Regenerate all protobuf code |
| `task proto:check` | Verify generated code is up to date (CI) |

---

## Troubleshooting

**"go: command not found" after install** — The Go binary is at `/usr/local/go/bin/go`. Confirm your shell profile exports `PATH=$PATH:/usr/local/go/bin`. Open a new terminal or run `source ~/.bashrc`.

**"task: command not found"** — Task installs to `$(go env GOPATH)/bin` (usually `~/go/bin`). Confirm that directory is in your PATH.

**"database is locked" errors** — Should not occur in normal operation because the server serializes writes through a single worker goroutine. If you see this, check for external tools (e.g. `sqlite3` CLI) holding a lock on the database file.

**"missing_signature" (HTTP 401) on device requests** — `PORTUNUS_HMAC_SECRET` is set on the server but the device is not sending the `X-Portunus-Sig` header, or the secrets don't match. Verify the firmware's `CONFIG_PORTUNUS_HMAC_SECRET` matches the server's `PORTUNUS_HMAC_SECRET` exactly.

**"unknown_module" in access responses** — The module ID in the request is not registered in the database. In dev mode, `PORTUNUS_KNOWN_MODULES` or the default `door-001` seeding handles this automatically. In production, register modules via the admin API: `POST /admin/v1/modules`.

**Server starts but ESP32 can't connect** — Verify the server's listen address is `0.0.0.0` (or `:port` which listens on all interfaces), not `127.0.0.1`. Check that the firewall allows the port. On Debian: `sudo ufw allow 8443/tcp` (or whichever port you're using).

**gRPC not working** — gRPC is disabled by default. Set `PORTUNUS_GRPC_ADDR=:50051` to enable it. gRPC uses the same TLS cert as the HTTP server, so `PORTUNUS_TLS_CERT_FILE` and `PORTUNUS_TLS_KEY_FILE` must be set. The ESP32 firmware must be built with `CONFIG_PORTUNUS_USE_GRPC=y`.