# Portunus — Architecture

System architecture for the Portunus door access control system.

**Last updated:** April 2026

---

## System overview

Portunus is a LAN-first door access control system with two primary components: an ESP32-based **access module** at each door and a Go-based **server** on the local network.

```
                          Local Network
                               │
         ┌─────────────────────┼─────────────────────────┐
         │                     │                         │
    ┌────┴─────┐         ┌─────┴──────┐          ┌───────┴───────┐
    │  Access  │  proto  │  Portunus  │   JSON   │  Admin Web UI │
    │  Module  │◄───────►│   Server   │◄────────►│   / curl      │
    │  (ESP32) │  + TLS  │   (Go)     │  + TLS   │               │
    └────┬─────┘  + HMAC └─────┬──────┘  + Session└──────────────┘
         │                     │
    ┌────┴─────┐         ┌─────┴──────┐
    │ Hardware │         │  SQLite DB │
    │ RFID     │         │ modules    │
    │ Strike   │         │ cards      │
    │ Reed SW  │         │ heartbeats │
    │ LED      │         │ access_log │
    └──────────┘         └────────────┘
```

The access module reads RFID cards, sends access requests to the server, and actuates the door strike based on the server's grant/deny decision. The server manages device registration, card policies, and maintains an audit trail. All communication is encrypted (TLS) and authenticated (HMAC-SHA256).

---

## Access module (firmware)

### Module variants

The firmware supports two build-time variants, selected via Kconfig (`PORTUNUS_MODULE_TYPE`):

| Variant | Kconfig | FSM | Hardware |
|---|---|---|---|
| ACCESS_POINT | `CONFIG_PORTUNUS_MODULE_TYPE_ACCESS_POINT` | `SystemFSM` | RFID reader, door strike, reed switch, LED |
| PROVISIONING_CONSOLE | `CONFIG_PORTUNUS_MODULE_TYPE_PROVISIONING_CONSOLE` | `ProvisioningFSM` | RFID reader, LED (no door strike or reed switch) |

`main.cpp` branches on `CONFIG_PORTUNUS_MODULE_TYPE_*` at compile time and instantiates the appropriate FSM. All other layers (interfaces, drivers, services) are shared between both variants. The rest of this section describes the shared architecture; variant-specific behavior is called out where it differs.

### Layered architecture

The firmware follows a domain-driven layering strategy designed to separate hardware concerns from business logic. Dependencies flow strictly downward — no layer imports from a layer above it.

```
┌───────────────────────────────────────────────────────────────────┐
│                        main.cpp                                   │
│              Composition root — constructs concrete modules,      │
│              selects FSM variant, injects dependencies,           │
│              starts services                                      │
└───────────────────────┬───────────────────────────────────────────┘
                        │ constructs & injects
                        ▼
┌──────────────────────────────────┐  ┌──────────────────────────────┐
│   SystemFSM  (core/system_fsm/)  │  │ ProvisioningFSM              │
│   ACCESS_POINT variant           │  │ (core/provisioning_fsm/)     │
│   Owns card polling, unlock      │  │ PROVISIONING_CONSOLE variant │
│   timing, reed switch, feedback. │  │ Two-scan enrollment flow.    │
└──────┬──────────────┬────────────┘  └──────┬──────────────────────┘
       │              │                      │
       ▼              ▼                      ▼
  ICredential    IAccessPoint           ICredential
    Reader        (AP only)               Reader
  IFeedback      event_bus             IFeedback
  (interfaces/)  (services/)           event_bus
       │              │                      │
       ▼              ▼                      ▼
  ReaderMfrc522  AccessPointGpio        ReaderMfrc522
  FeedbackLed    (drivers/)             FeedbackLed
  (drivers/)                            (drivers/)
       │              │                      │
       ▼              ▼                      ▼
  mfrc522_hal    door_strike            mfrc522_hal
  (SPI)          reed_switch            (SPI)
                 (GPIO)
```

### Layer responsibilities

**Application (main.cpp)** — The composition root. Initializes platform services (NVS, WiFi, event bus), constructs concrete module instances, and starts independent services. At compile time it branches on `CONFIG_PORTUNUS_MODULE_TYPE_*` to instantiate either `SystemFSM` (ACCESS_POINT) or `ProvisioningFSM` (PROVISIONING_CONSOLE). After startup, `app_main` returns and FreeRTOS tasks take over.

**Core (system_fsm/ and provisioning_fsm/)** — The firmware has two FSM implementations, one per variant.

- *SystemFSM* (ACCESS_POINT) — Transitions through `BOOT → INITIALIZING → OPERATIONAL → ERROR`. In the operational state it runs two FreeRTOS tasks: the FSM main loop (event processing, reed switch polling, unlock timer management) and a card polling sub-task. Programs against `ICredentialReader`, `IAccessPoint`, and `IFeedback`.

- *ProvisioningFSM* (PROVISIONING_CONSOLE) — Implements a two-scan credential enrollment flow: `IDLE → AWAITING_CREDENTIAL → SENDING → IDLE`. Scan 1 (operator presence) advances state. Scan 2 causes the new credential's UID to be SHA-256 hashed on-device via mbedTLS; the hash is bundled into an `EVENT_PROVISION_REQUEST` and published to the event bus for `server_comm` to forward. Programs against `ICredentialReader` and `IFeedback` — no door-strike hardware is needed or used.

**Interfaces (portunus_interfaces/)** — Pure virtual C++ classes defining the contracts between the FSM and hardware. `ICredentialReader` exposes `read()` and `halt()`. `IAccessPoint` exposes `unlock()`, `lock()`, and `is_open()`. `IFeedback` exposes `indicate(feedback_type_t)`. Any pointer may be `nullptr` to indicate absent hardware — the FSM sets the corresponding capability flag to false and adapts.

**Drivers** — Concrete implementations of the interfaces. Each driver wraps a hardware-specific HAL (SPI for MFRC522, GPIO for door strike/reed switch/LED). The driver layer is the only code that calls ESP-IDF hardware APIs directly. Swapping hardware (e.g., MFRC522 → PN532, electric strike → magnetic lock) means writing a new driver that implements the same interface — no FSM changes required.

**Services** — Infrastructure components with no hardware knowledge. The event bus provides inter-component messaging. The heartbeat service emits periodic health events. The WiFi manager handles connection and reconnection. The server comm component bridges the event bus to the Portunus server over HTTP or gRPC.

**Common (portunus_types/, portunus_config/)** — Dependency-free shared definitions. Type definitions (`credential_t`, event types, error codes, system states) and Kconfig-driven configuration constants (pin assignments, timing parameters, network settings, security settings). Every other layer can import from common; common imports from nothing.

### Event bus

All inter-component communication flows through a FreeRTOS queue-backed publish/subscribe event bus. This is the messaging backbone that decouples the FSM from services and from server communication.

The event bus uses a single dispatcher queue (MVP topology). A dedicated FreeRTOS task dequeues events and invokes matching subscriber callbacks. Callbacks execute on the dispatcher task's stack, so they must be short and non-blocking. Components that need to do blocking work (like HTTP I/O) copy the event into their own internal queue and process it on their own task.

Event types are statically defined in `event_types.h`, grouped by subsystem:

| Group | Events | Published by | Consumed by | Variant |
|---|---|---|---|---|
| System | `BOOT_COMPLETE` | FSM | — | Both |
| Credential | `CREDENTIAL_READ`, `CREDENTIAL_READ_ERROR` | Card poll task | SystemFSM, server_comm¹ | Both |
| Heartbeat | `HEARTBEAT` | Heartbeat service | server_comm | Both |
| Access | `ACCESS_GRANTED`, `ACCESS_DENIED` | server_comm | SystemFSM | AP only |
| Door state | `DOOR_OPENED`, `DOOR_CLOSED` | SystemFSM | — | AP only |
| FSM | `UNLOCK_TIMEOUT` | SystemFSM | — | AP only |
| Provisioning | `EVENT_PROVISION_REQUEST` | ProvisioningFSM | server_comm | PC only |
| Provisioning | `EVENT_PROVISION_SUCCESS`, `EVENT_PROVISION_FAILED` | server_comm | ProvisioningFSM | PC only |

¹ In ACCESS_POINT, `server_comm` subscribes to `CREDENTIAL_READ` to forward access requests. In PROVISIONING_CONSOLE, `server_comm` subscribes to `EVENT_PROVISION_REQUEST` instead; `ProvisioningFSM` consumes `CREDENTIAL_READ` internally to drive its two-scan state machine.

*AP = ACCESS_POINT variant. PC = PROVISIONING_CONSOLE variant.*

Events are fixed-size structs (`portunus_event_t`) copied into the queue by value — no heap allocation. The event envelope contains an ID and a union of typed payloads.

### Access request flow (card tap to door unlock)

```
Card in field
     │
     ▼
[card_poll task]  mfrc522 → read() → credential_t
     │
     ▼ EVENT_CREDENTIAL_READ (event bus)
     │
     ├──► [FSM]          indicate(CARD_READ) → LED solid on
     │
     └──► [server_comm]  encode protobuf → POST /v1/access_request
                              │
                              ▼
                         [Portunus Server]  → Decide() → granted/denied
                              │
                              ▼
                         decode protobuf response
                              │
                              ▼ EVENT_ACCESS_GRANTED or EVENT_ACCESS_DENIED (event bus)
                              │
                              └──► [FSM]
                                     │
                                     ├─ granted: unlock() → start hold timer → indicate(ACCESS_GRANTED)
                                     │              │
                                     │              └─ timer expires or door opens+closes → lock()
                                     │
                                     └─ denied:  indicate(ACCESS_DENIED)
```

If the network is unavailable when a card is tapped, `server_comm` publishes `EVENT_ACCESS_DENIED` with reason `no_network` so the FSM always clears the CARD_READ feedback and shows an error indication.

### Provisioning flow (PROVISIONING_CONSOLE variant — credential enrollment)

```
Scan 1: operator places any credential
     │
     ▼
[card_poll task]  mfrc522 → read() → credential_t
     │
     ▼ EVENT_CREDENTIAL_READ (event bus)
     │
     └──► [ProvisioningFSM]  indicate(PROVISIONING_AWAITING) → LED slow pulse
                                   │  start PORTUNUS_PROVISION_TIMEOUT_MS timer
                                   │
                   (timeout) ──────┘ → IDLE
                                   │
                    Scan 2: new credential placed within timeout
                                   │
                                   ▼
[card_poll task]  mfrc522 → read() → credential_t
                                   │
                          EVENT_CREDENTIAL_READ (event bus)
                                   │
                                   └──► [ProvisioningFSM]
                                            SHA-256(uid) on-device via mbedTLS
                                            publish EVENT_PROVISION_REQUEST
                                                │
                                                ▼
                                         [server_comm]
                                            encode ProvisionCredentialRequest (nanopb)
                                            POST /v1/provision_credential
                                                │
                                                ▼
                                         [Portunus Server]
                                            (endpoint pending implementation)
                                                │
                                                ▼
                                         decode response
                                                │
                                  ┌─────────────┴─────────────┐
                          EVENT_PROVISION_SUCCESS        EVENT_PROVISION_FAILED
                                  │                           │
                        [ProvisioningFSM]            [ProvisioningFSM]
                         indicate(SUCCESS/DUPLICATE/  indicate(UNAUTHORIZED/
                         ...)  → return to IDLE        COMM_ERROR)  → IDLE
```

The operator credential (scan 1) is discarded after advancing state — it serves only as a physical presence confirmation. Only the enrolling credential (scan 2) is hashed and sent.

### FreeRTOS task map

| Task | Priority | Stack | Responsibility | Variant |
|---|---|---|---|---|
| `evt_dispatch` | 5 | 4 KB | Event bus dispatcher — invokes subscriber callbacks | Both |
| `fsm` | 5 | 4 KB | FSM main loop — SystemFSM (AP): event processing, reed switch polling, unlock timer; ProvisioningFSM (PC): two-scan state machine | Both |
| `card_poll` | 4 | 4 KB | MFRC522 polling — SPI reads, publishes `CREDENTIAL_READ` events | Both |
| `led_pattern` | 3 | 2 KB | LED blink patterns — non-blocking, preemptive | Both |
| `heartbeat` | 3 | 2 KB | Periodic heartbeat event generation | Both |
| `server_comm` | 2 | 6–10 KB | HTTP/gRPC I/O — blocking network calls on a dedicated stack | Both |

The `server_comm` task uses a larger stack (10 KB) when gRPC is enabled to accommodate the nghttp2 HTTP/2 session state. In PROVISIONING_CONSOLE, `server_comm` handles `EVENT_PROVISION_REQUEST` events instead of `CREDENTIAL_READ` events; the `fsm` and `card_poll` tasks run with the same priorities and stacks but drive the ProvisioningFSM two-scan flow.

---

## Server

### Layered architecture

The server follows a conventional Go layered architecture: transport → service → store.

```
┌──────────────────────────────────────────────────────────────────┐
│                         cmd/main.go                              │
│  Composition root — wires stores, services, transports, starts   │
│  listeners, handles graceful shutdown                            │
└───────┬───────────────────────────────┬──────────────────────────┘
        │                               │
        ▼                               ▼
  ┌────────────┐                  ┌────────────┐
  │  httpapi   │                  │  grpcapi   │
  │            │                  │            │
  │ POST /v1/* │                  │ gRPC RPCs  │
  │ /admin/v1/*│                  │ (protobuf) │
  │ (JSON +    │                  │            │
  │  protobuf) │                  │            │
  └─────┬──────┘                  └──────┬─────┘
        │  middleware:                   │  interceptors:
        │  logging, HMAC,                │  logging, HMAC
        │  admin Bearer auth             │
        │                                │
        └──────────┬─────────────────────┘
                   │ domain types
                   ▼
        ┌─────────────────────┐
        │   Service layer     │
        │                     │
        │ HeartbeatService    │    records telemetry, checks device registry
        │ AccessService       │    evaluates credential against policy, records audit event
        │ AdminService        │    CRUD for modules, credentials, doors
        │ AuthService         │    session-based admin authentication
        │ AdminUserService    │    admin user accounts and role assignment
        │ RoleService         │    role and permission management
        │ MemberAccessService │    member lifecycle (provision, disable, archive)
        │ ModuleAuthService   │    per-member per-module authorization grants
        │ DeviceRegistry      │    IsKnown() / NoteSeen() for module identity
        │ HeartbeatPruner     │    background goroutine, retention-based cleanup
        │ ExpiryWorker        │    background goroutine, member expiry sweeps
        └─────────┬───────────┘
                  │ store interfaces
                  ▼
        ┌─────────────────────┐
        │   Store layer       │
        │                     │
        │ HeartbeatStore      │    UpsertHeartbeat, PruneOlderThan
        │ DeviceStore         │    IsKnown, MarkSeen
        │ AccessEventStore    │    RecordEvent (append-only audit log)
        │ CredentialStore     │    RegisterCredential, IsCredentialAllowed, SetStatus
        │ ModuleAdminStore    │    CommissionModule, RevokeModule, door CRUD
        │ AdminUserStore      │    admin user account records
        │ SessionStore        │    active session persistence
        │ RoleStore           │    roles and permission sets
        │ MemberAccessStore   │    member lifecycle state and credential links
        │ ModuleAuthStore     │    per-member per-module authorization records
        └─────────┬───────────┘
                  │ sql.DB
                  ▼
        ┌─────────────────────┐
        │   SQLite (WAL mode) │
        │   via modernc.org   │
        │   /sqlite (pure Go) │
        └─────────────────────┘
```

### Transport layer

The server exposes two concurrent transport interfaces, both delegating to the same service layer:

**HTTP (httpapi/)** — Handles device endpoints (`POST /v1/heartbeat`, `POST /v1/access_request`) and admin endpoints (`/admin/v1/*`). An admin web UI is served at `/admin/ui/`. Device endpoints accept both JSON and protobuf (`application/x-protobuf`) request bodies. Admin API endpoints are JSON-only. The middleware chain applies logging → session resolution → HMAC signature verification (device paths only) → routing.

**gRPC (grpcapi/)** — Implements `PortunusService` with `SendHeartbeat` and `RequestAccess` RPCs. Runs on a separate port and co-exists with the HTTP server. Uses interceptors for logging and HMAC verification. Enabled by setting `PORTUNUS_GRPC_ADDR`.

Both transports convert protobuf/JSON requests into domain types (`types.HeartbeatRequest`, `types.AccessRequest`), call the same service methods, and convert the response back. This means the business logic is transport-agnostic and tested without HTTP or gRPC in the loop.

### Service layer

**HeartbeatService** — Validates the module ID, checks the device registry, upserts the heartbeat record, and returns a response indicating whether the module is known. Every heartbeat updates `last_seen_at` on the module record regardless of known status (so the server can track unknown devices trying to connect).

**AccessService** — The access decision engine. Validates module ID and credential ID, checks module registration via `DeviceRegistry.IsKnown()`, then evaluates the credential against the active policy. The policy lookup sequence is: (1) `AllowAll` flag (dev/testing bypass), (2) `MemberAccessStore` + `ModuleAuthorizationStore` path — look up the member by hashed credential, verify active status, verify an active module authorization (production path), (3) `CredentialStore` legacy DB lookup, (4) legacy `AllowedCredentialIDs` env-var map (deprecated fallback). Every decision is recorded in the access event audit log.

**AdminService** — CRUD operations for modules (commission, revoke, delete), credentials (register, set status, delete), and doors (register, delete). Credential IDs are hashed with keyed HMAC-SHA256 before storage — the raw credential UID is never persisted.

**AuthService / AdminUserService / RoleService** — Session-based admin authentication. `AuthService` handles login, logout, and session validation via a session cookie. `AdminUserService` manages admin user accounts and their role assignments. `RoleService` manages roles and the permission sets attached to them.

**MemberAccessService** — Member lifecycle management. Handles provisioning new members (assigning a role and optional expiry), attaching a credential hash, disabling, and archiving. Interacts with the `ExpiryWorker` for time-based expiry.

**ModuleAuthorizationService** — Manages per-member per-module authorization grants. An authorization records which member is allowed on which module, who granted it, an optional expiry, and an optional time-restriction policy. Revocation records `revoked_at` and the revoking admin's UUID.

**DeviceRegistry** — Thin wrapper around `DeviceStore` that defines the "known" predicate: a module is known if it is commissioned, enabled, and not revoked. Unknown modules still get their heartbeats and access attempts recorded (for observability), but access is always denied.

**HeartbeatPruner** — Background goroutine that periodically deletes heartbeat records older than the configured retention period. Runs on a configurable interval (default 6 hours). Disabled when retention is set to 0.

### Store layer

All store interfaces are defined in `store/` as Go interfaces. The production implementation (`store/sqlite/`) uses the serialized write worker for mutations and direct reads for queries.

**Write serialization:** All write operations go through a `db.Worker` — a single goroutine that processes transactions sequentially via a buffered channel. This eliminates SQLite's "database is locked" errors under concurrent access without requiring external locking. The worker executes each write inside a transaction; callers block until their transaction commits or the context expires.

**Module auto-creation:** When a heartbeat or access request arrives from an unregistered module ID, `ensureModule()` creates a row with `enabled=0` and no `commissioned_at_ms`. This satisfies foreign key constraints for heartbeat and access event records while keeping the module in an "unknown" state. Only an explicit admin action (or dev seeding) promotes a module to commissioned.

---

## Communication protocol

### Protobuf contract

A single `.proto` file (`proto/portunus/v1/portunus.proto`) is the source of truth for the wire format between the server and access modules. Both sides generate code from this file:

| Side | Generator | Output |
|---|---|---|
| Server (Go) | `protoc` + `protoc-gen-go` / `protoc-gen-go-grpc` | `server/api/portunus/v1/*.pb.go` |
| ESP32 (C) | `protoc` + Nanopb generator | `access_module/components/portunus_proto/portunus/v1/*.pb.c/.h` |

Generated files are committed to the repo. Regeneration is only needed when the `.proto` file changes. A CI check (`task proto:check`) verifies generated code hasn't drifted from the committed version.

Compatibility rules: only append new fields (never reorder or reuse a field number), mark removed fields as `reserved`, keep optional semantics where the ESP32 may not have the data.

### Dual transport

The access module supports two transport modes, selected at build time via Kconfig:

**HTTP/1.1 + protobuf (default)** — The module POSTs Nanopb-encoded protobuf bodies with `Content-Type: application/x-protobuf` and an HMAC signature header. The server's HTTP handler detects the content type and decodes protobuf instead of JSON.

**gRPC over HTTP/2+TLS (`CONFIG_PORTUNUS_USE_GRPC=y`)** — The module uses a custom gRPC client built on `nghttp2` + `esp-tls`. It speaks the gRPC wire protocol (5-byte length-prefixed protobuf in HTTP/2 DATA frames) directly, without a full gRPC library. HMAC signatures are attached as custom gRPC metadata (`x-portunus-sig`).

Both transports encode identical protobuf messages. The server can run both listeners simultaneously — HTTP for legacy modules and admin API, gRPC for modules with gRPC firmware.

### Message types

| RPC | Request | Response | Purpose |
|---|---|---|---|
| `SendHeartbeat` | `HeartbeatRequest` (module_id, firmware_version, uptime, rssi, ip, free_heap, sequence) | `HeartbeatResponse` (ok, known, module_id, server_time) | Periodic health telemetry |
| `RequestAccess` | `AccessRequest` (module_id, credential_id, door_closed, requested_at) | `AccessResponse` (ok, known, granted, reason, module_id, server_time) | Credential tap → access decision |
| `ProvisionCredential` | `ProvisionCredentialRequest` (module_id, credential_hash, operator_uuid, role_id) | `ProvisionCredentialResponse` (ok, reason, member_uuid) | Two-scan enrollment → member creation (server-side endpoint pending) |

---

## Data model

### Entities

```
┌──────────┐       ┌──────────────┐       ┌──────────────────┐
│  doors   │       │   modules    │       │ module_heartbeats│
│          │1─────*│              │1─────*│                  │
│ door_id  │       │ module_id    │       │ heartbeat_id     │
│ name     │       │ door_id (FK) │       │ module_id (FK)   │
│ location │       │ display_name │       │ received_at_ms   │
│          │       │ enabled      │       │ seq, uptime,     │
│          │       │ commissioned │       │ fw_version,      │
│          │       │ revoked      │       │ rssi, ip,        │
│          │       │ last_seen    │       │ free_heap        │
└──────────┘       │ last_ip      │       └──────────────────┘
                   │ last_fw_ver  │
                   │ last_rssi    │        ┌──────────────────┐
                   └──────┬───────┘        │  access_events   │
                          │                │                  │
                          │          *────1│ access_event_id  │
                          └────────────────│ module_id (FK)   │
                                           │ door_id (FK)     │
                   ┌─────────────┐         │ cred_hash(FK)    │
                   │ credentials │   *────1│ decision_granted │
                   │             │─────────│ decision_reason  │
                   │ cred_hash   │         │ decided_at_ms    │
                   │ tag         │         └──────────────────┘
                   │ status      │
                   └─────────────┘
```

**doors** — Physical door locations. A door can have zero or more modules installed.

**modules** — Registered access module devices. Each module is optionally assigned to a door. A module's lifecycle is: auto-created (unknown, `enabled=0`) → commissioned (via admin API, `enabled=1`, `commissioned_at_ms` set) → optionally revoked (`revoked_at_ms` set). A module is "known" only when commissioned, enabled, and not revoked.

**module_heartbeats** — Append-only telemetry log. Each row records a single heartbeat with the module's reported health data. Subject to retention-based pruning.

**credentials** — RFID credential registrations. Credential IDs are stored as keyed HMAC-SHA256 hashes (32 bytes) — the raw credential UID is never persisted. Each credential has a status (`active`, `disabled`, `lost`) and an optional human-readable tag.

**access_events** — Append-only audit log. Every access decision — granted or denied — is recorded with the module ID, credential hash, decision reason, and timestamps. This is the "who/what/when" trail.

### SQLite configuration

The database uses WAL (Write-Ahead Logging) journal mode for concurrent read/write access, `synchronous=NORMAL` for a performance/safety balance, and a 5-second busy timeout. Foreign keys are enforced. Indexes cover the primary query patterns: module lookups by last_seen, heartbeat queries by module+time, access event queries by module+time and card+time, and retention-based pruning by timestamp.

Schema migrations are embedded in the server binary and applied automatically on startup inside transactions. Each migration is tracked in a `schema_migrations` table. Migrations are forward-only (add, never drop or rename) so that a rollback to an older server binary remains compatible with a newer schema.

---

## Security model

### Transport encryption (TLS)

All communication between access modules and the server is encrypted using TLS. The ESP32 firmware uses ESP-IDF's mbedTLS stack with one of three certificate validation modes:

| Mode | Kconfig | Use case |
|---|---|---|
| Custom CA pinning | `PORTUNUS_TLS_USE_CUSTOM_CA=y` | LAN deployments with a private CA (recommended) |
| Mozilla CA bundle | `PORTUNUS_TLS_USE_CUSTOM_CA=n` | Servers with publicly-trusted certs (Let's Encrypt, etc.) |
| Skip verification | `PORTUNUS_TLS_SKIP_VERIFY=y` | Development only — disables all cert validation |

For LAN deployments, the `scripts/generate_certs.sh` script creates a private CA and server certificate. The CA certificate is embedded in the firmware binary for certificate pinning. The server certificate includes the server's IP address as a Subject Alternative Name.

### Message authentication (HMAC-SHA256)

TLS protects the transport; HMAC authenticates each message. Every outgoing request from the access module includes an `X-Portunus-Sig` header containing `HMAC-SHA256(pre_shared_key, request_body_bytes)` hex-encoded. The server rejects requests with missing or invalid signatures with HTTP 401 or gRPC `UNAUTHENTICATED`.

The HMAC is computed over the raw protobuf-encoded body (not the HTTP headers or URL). On the server side, the request body is re-marshalled from the parsed protobuf message and the expected HMAC is computed for comparison using constant-time comparison to prevent timing attacks.

### Credential ID protection

Raw RFID credential UIDs are never stored on the server. When a credential is registered via the admin API, the raw UID is hashed with keyed HMAC-SHA256 (secret: `PORTUNUS_CREDENTIAL_HASH_SECRET`) before insertion into the `credentials` table. When an access request arrives, the server hashes the incoming credential ID and compares against stored hashes. The keyed HMAC prevents offline rainbow-table attacks against a stolen database. In the PROVISIONING_CONSOLE variant, the on-device SHA-256 hash is sent instead of the raw UID — the server re-hashes with the keyed secret on receipt.

### Admin API authentication

Admin endpoints (`/admin/v1/*`) are protected by session-based authentication. Administrators log in via `POST /admin/v1/login` (username + password) and receive a `portunus_session` cookie. All subsequent admin requests must include that cookie. Sessions are invalidated by explicit logout or server-enforced expiry. Admin requests bypass HMAC verification (they originate from browsers or curl, not from ESP32 firmware).

### Firmware secrets

The HMAC pre-shared key and WiFi credentials are stored in the firmware's flash memory via Kconfig. For the current phase, this is acceptable for the target threat model (makerspace/workshop). A planned enhancement will move secrets to an encrypted NVS partition, separating identity material from the firmware binary.

---

## Key design decisions

| Decision | Rationale |
|---|---|
| ESP-IDF over Arduino | Full access to FreeRTOS SMP, hardware security APIs (flash encryption, secure boot), and the component build system. |
| Module abstraction (interfaces) | Enables hardware substitution (MFRC522 → PN532, strike → mag lock) without changing FSM logic. Enables unit testing with mock implementations. |
| FreeRTOS event bus | Natural fit for ESP-IDF's SMP FreeRTOS on the dual-core ESP32-S3. Decouples components without shared mutable state. |
| Single dispatcher queue (MVP) | Simpler than per-subscriber queues. Sufficient for the current subscriber count. Documented as a scaling decision to revisit. |
| `nullptr` for absent hardware | The FSM adapts to missing hardware via capability flags rather than conditional compilation. Supports bench testing and incremental hardware integration. |
| Go for server | Strong concurrency model, single-binary deployment, excellent cross-compilation (arm64 for Pi from x86_64 dev machine with no extra toolchains). |
| Pure-Go SQLite (modernc.org) | No CGo dependency means trivial cross-compilation and no C toolchain required on the deployment target. |
| Serialized write worker | Eliminates SQLite's "database is locked" without requiring WAL-only or connection pooling complexity. Single goroutine processes all writes sequentially. |
| Protobuf as wire format | Strongly-typed contract, compact binary encoding (important for ESP32 memory), code generation for both Go and C (Nanopb). |
| Dual transport (HTTP + gRPC) | HTTP/1.1 is simpler to debug and works with curl. gRPC provides bidirectional streaming for future features. Both share the same protobuf messages. (HTTP/1.1 should never be used in prod) |
| HMAC over request body | Application-level authentication independent of TLS. Verifies that the message came from an enrolled device, not just any TLS client. |
| Keyed HMAC-SHA256 credential hashing | Protects credential UIDs at rest with a server-side secret. Unlike bare SHA-256, keyed hashing prevents offline rainbow-table attacks on a stolen database. Secret set via `PORTUNUS_CREDENTIAL_HASH_SECRET`; required in prod. |
| On-device hashing in PROVISIONING_CONSOLE | The ProvisioningFSM hashes the raw credential UID on-device (SHA-256 via mbedTLS) before publishing `EVENT_PROVISION_REQUEST`. The raw UID never leaves the ESP32, even over an encrypted channel. |
| Two firmware variants, shared layers | ACCESS_POINT and PROVISIONING_CONSOLE select different FSMs at compile time via Kconfig but share all drivers, interfaces, and services. Adding a new variant only requires a new core FSM — no changes to hardware or transport layers. |
| Member + module authorization model | Access decisions use a two-table check: member status (active, enabled) and a per-module authorization record. This allows fine-grained access control (member X can use door A but not door B) without duplicating credential registrations. |
| Session-based admin authentication | Admin API and UI use a session cookie obtained via `POST /admin/v1/login`. The single-key Bearer token model did not support per-user identity, audit attribution (who archived this member), or role-based permission enforcement. |
| Forward-only migrations | Server binary rollbacks remain compatible with newer database schemas. Older code ignores columns and tables it doesn't know about. |
| Server-side access decisions | The ESP32 never decides access locally — it always asks the server. This centralizes policy, simplifies the firmware, and ensures the audit trail is complete. Offline behavior is a planned future enhancement. |
| WiFi as service, not module | WiFi is platform-intrinsic on ESP32. A service is sufficient unless alternative transports (Ethernet, cellular) are introduced, at which point it could be promoted to a full module. |
| Kconfig for all configuration | Dev/prod differences are managed through sdkconfig overlays. No `#define` scattered across source files. All parameters are tunable via `menuconfig`. |