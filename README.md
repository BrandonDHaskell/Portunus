# Portunus

**LAN-first door access control system** — ESP32 access modules communicate with a Go server to manage RFID-based door access for makerspaces, workshops, and small offices.

---

## How it works

```
 ┌────────────────┐                               ┌──────────────────────┐
 │  Credential    │                               │  Admin Web UI / curl │
 │  tap           │                               │                      │
 └───────┬────────┘                               └──────────┬───────────┘
         │                                                   │ session cookie
         ▼                                                   ▼
 ┌──────────────────┐  protobuf + TLS + HMAC  ┌──────────────────────────┐
 │  Access Module   │◄───────────────────────►│     Portunus Server      │
 │    (ESP32)       │                         │         (Go)             │
 │                  │  heartbeat ──────────►  │                          │
 │  MFRC522 RFID    │  access request ──────► │  SQLite DB               │
 │  Door strike     │  ◄────── grant/deny     │  Device registry         │
 │  Reed switch     │                         │  Member & credential     │
 │  Status LED      │                         │    policy engine         │
 │                  │                         │  Audit log               │
 └──────────────────┘                         │  Admin REST API + Web UI │
                                              └──────────────────────────┘
```

Two firmware variants are supported, selected at compile time:

- **ACCESS_POINT** — standard door control: reads a credential, asks the server, actuates the door strike.
- **PROVISIONING_CONSOLE** — enrollment console: two-scan flow (operator + new credential), hashes on-device, submits a provisioning request to the server.

ACCESS_POINT runtime flow:

1. A credential is tapped on the RFID reader at the door.
2. The access module sends the credential UID to the server over TLS, signed with HMAC-SHA256.
3. The server checks the module's registration, hashes the credential with HMAC-SHA256, and evaluates it against the member and authorization policy.
4. The server returns grant or deny. The module actuates the door strike and shows LED feedback.
5. Every decision is recorded in an append-only audit log.

---

## Current status

**v1 MVP — functional end-to-end.** The full credential-tap-to-door-unlock loop is working on hardware, with a full member management system and admin web UI.

### What's working

- RFID credential reading (MFRC522 via SPI) with anti-re-read debounce
- Door strike control with configurable hold timer and early re-lock on door close
- Reed switch monitoring with software debounce
- LED feedback patterns (granted, denied, credential read, system ready, error)
- ACCESS_POINT `SystemFSM` with hardware abstraction interfaces and capability-based degradation
- PROVISIONING_CONSOLE `ProvisioningFSM` — two-scan enrollment flow with on-device HMAC-SHA256 hashing
- FreeRTOS event bus (queue-backed pub/sub) for all inter-component messaging
- WiFi management with exponential-backoff reconnection
- Server communication over HTTP/1.1 + protobuf with TLS and HMAC signing
- gRPC transport (HTTP/2 + TLS via nghttp2, build-time selectable)
- Go server with heartbeat ingestion, access decision engine, member management, and admin REST API
- Admin web UI at `/admin/ui/` for managing members, credentials, modules, and authorizations
- Session-cookie-based admin authentication (login, logout, change-password) with forced first-login reset
- Member and role-based access control (RBAC) — members, roles, permissions, and per-module authorization grants
- SQLite persistence with auto-migration (11 migrations), serialized writes, and heartbeat pruning
- Credential registration with keyed HMAC-SHA256 hashing (raw UIDs never stored)
- Module commissioning, revocation, and door management via admin API
- Full Kconfig-driven configuration (module variant, pins, timing, security, network settings)
- Private CA certificate generation and firmware embedding for LAN TLS pinning
- Cross-platform Taskfile with build, test, lint, format, proto gen, cert management, and deploy

### Known limitations

- Flash encryption and secure boot are not yet enabled (ESP32-S3 supports both natively — planned)
- MFRC522 authenticates by UID only (cloneable); acceptable for makerspace threat model
- No offline/cached access policy when the server is unreachable (planned)
- No OTA firmware updates (reflash via USB required)

---

## Quick start

### Server

Requires Go 1.24+ and [Task](https://taskfile.dev).

```bash
git clone https://github.com/BrandonDHaskell/Portunus.git
cd Portunus

# Install Task
go install github.com/go-task/task/v3/cmd/task@latest

# Run tests
task test:server

# Start the server in dev mode (plain HTTP, all credentials granted)
cd server
PORTUNUS_ENV=dev PORTUNUS_ALLOW_ALL=true go run ./cmd/portunus-server
```

The server starts on `:8080` and seeds a default `door-001` module. Full setup guide: [docs/setup_server.md](docs/setup_server.md).

### Firmware

Requires [ESP-IDF 5.4+](https://docs.espressif.com/projects/esp-idf/en/stable/esp32s3/get-started/).

```bash
# Source ESP-IDF environment
. ~/esp/esp-idf/export.sh

# Configure (module variant, WiFi credentials, server host, pin assignments)
task firmware:menuconfig

# Build and flash
task firmware:flash-monitor
```

Full setup guide with wiring reference: [docs/setup_firmware.md](docs/setup_firmware.md).

---

## Repository layout

```
Portunus/
├── server/                    Go server
│   ├── cmd/portunus-server/   Entry point
│   ├── api/portunus/v1/       Generated protobuf + gRPC Go stubs
│   └── internal/
│       ├── config/            Environment variable config
│       ├── db/                SQLite open, migrate, write worker (11 migrations)
│       ├── httpapi/           HTTP handlers, middleware, admin API, admin web UI
│       ├── grpcapi/           gRPC handlers, interceptors
│       └── portunus/
│           ├── service/       Business logic (heartbeat, access, members, RBAC, admin)
│           ├── store/         Store interfaces + SQLite implementations
│           └── types/         Domain types
│
├── access_module/             ESP32-S3 firmware (ESP-IDF / C++)
│   ├── main/                  Composition root (selects FSM via Kconfig)
│   ├── core/
│   │   ├── system_fsm/        ACCESS_POINT state machine
│   │   └── provisioning_fsm/  PROVISIONING_CONSOLE two-scan enrollment FSM
│   ├── components/
│   │   ├── portunus_interfaces/   ICredentialReader, IAccessPoint, IFeedback
│   │   ├── portunus_types/        Shared types (credential_t, events, errors)
│   │   ├── portunus_config/       Kconfig-driven constants
│   │   └── portunus_proto/        Nanopb-generated protobuf C stubs
│   ├── drivers/
│   │   ├── reader_mfrc522/    MFRC522 SPI driver → ICredentialReader
│   │   ├── access_point_gpio/ Door strike + reed switch → IAccessPoint
│   │   └── feedback_led/      LED patterns → IFeedback
│   └── services/
│       ├── event_bus/         FreeRTOS queue-backed pub/sub
│       ├── heartbeat_service/ Periodic health telemetry
│       ├── wifi_mgr/          WiFi STA with auto-reconnect
│       ├── server_comm/       Event bus ↔ server bridge (HTTP/gRPC + protobuf)
│       └── grpc_client/       HTTP/2+TLS client via nghttp2
│
├── proto/                     Protobuf definitions (single source of truth)
│   ├── portunus/v1/portunus.proto
│   └── nanopb/portunus.options
│
├── scripts/
│   ├── generate_certs.sh      Private CA + server TLS cert generation
│   ├── proto_gen.py           Protobuf code generation (Go + Nanopb)
│   ├── check_fmt.py           Go format checker
│   └── clean.py               Cross-platform artifact cleanup
│
├── docs/                      Project documentation
├── Taskfile.yml               Build, test, lint, deploy task runner
└── project_plan.md            Phased development plan and design rationale
```

---

## Documentation

| Document | Description |
|---|---|
| [Server setup](docs/setup_server.md) | Install dependencies, configure, run, and deploy the Go server |
| [Firmware setup](docs/setup_firmware.md) | Install ESP-IDF, wire hardware, configure Kconfig, build and flash |
| [CI/CD pipeline](docs/ci_cd_pipeline.md) | Local build/test/deploy pipeline, cross-compilation, deployment to Raspberry Pi |
| [Architecture](docs/architecture.md) | System design, firmware layering, server structure, event bus, data flow |
| [Database](docs/database.md) | SQLite schema, migrations, write model, useful queries |
| [Security](docs/security.md) | Threat model, defense layers, TLS/HMAC details, hardening checklist |
| [API reference](docs/api.md) | HTTP and gRPC endpoint documentation (device + admin) |
| [Project plan](project_plan.md) | Phased roadmap, design decisions, hardware specs |

---

## Hardware

| Component | Specification |
|---|---|
| Microcontroller | ESP32-S3 WROOM-1 (dual-core Xtensa LX7, WiFi, BLE) |
| RFID reader | MFRC522 (SPI, 13.56 MHz, ISO 14443A) |
| Door actuator | Electric door strike via relay/MOSFET |
| Door sensor | Magnetic reed switch |
| Status indicator | LED |
| Server | Raspberry Pi 5 (or any Debian Linux host) |

See [Firmware setup — Wiring reference](docs/setup_firmware.md#wiring-reference) for pin assignments and wiring diagrams.

---

## Task commands

Common commands (run from the repo root):

```bash
task test:server          # Run all server tests
task build:server         # Build server binary
task build:server:arm64   # Cross-compile for Raspberry Pi
task deploy:server        # Deploy to Pi via SSH
task firmware:build       # Build ESP32 firmware
task firmware:flash       # Flash to connected ESP32
task firmware:menuconfig  # Open Kconfig UI
task certs:generate       # Generate TLS certificates
task ci:all               # Full validation suite
task --list               # Show all available commands
```

---

## License

[GNU General Public License v3.0](LICENSE)