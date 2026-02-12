# Portunus — Project Plan

**Door Security & Access Control System**

| | |
|---|---|
| **Version** | 1.0 |
| **Date** | February 2026 |
| **Status** | Active Development |

---

## 1. Executive Summary

Portunus is a modular, self-contained door security and access control system designed for makerspaces, small workshops, and hobbyist environments. Named after the Roman god of keys and doors, the system consists of two primary components: a centralized **server** that manages access policies, monitors connected door modules, and resolves access requests; and distributed **door access modules** that handle credential reading, door hardware control, and real-time status reporting.

The system operates entirely on a local network with no cloud dependency. The architecture is designed to scale from a single-door prototype to a multi-door deployment without firmware rewrites.

**Target environments:** Makerspaces, hobbyist workshops, small offices, and home labs requiring physical access control with local-network-only operation.

---

## 2. Project Scope

### 2.1 System Components

**Server:** A Go-based application running on a Raspberry Pi 5 (with integrated SSD) or a generic Linux server on the local network. The server tracks connected modules, resolves access requests against stored policies and permissions, manages device provisioning, and provides administrative interfaces. REST APIs are currently operational for heartbeat and card reader data; gRPC will be adopted as the target protocol.

**Door Access Module:** An ESP32-S3 WROOM-1 based embedded controller mounted at each door. Each module reads RFID credentials (MFRC522), controls a door strike (electric unlock actuator), monitors door state (reed switch), and reports health telemetry and access events to the server. Firmware is developed in C++ using Espressif's ESP-IDF framework.

### 2.2 In Scope

- Server application for access policy management and module monitoring (Go)
- ESP32 door module firmware for RFID reading, door control, and telemetry (C++/ESP-IDF)
- Encrypted wireless communication between server and modules over local WiFi
- Device provisioning model with server-built, per-device firmware binaries
- Heartbeat and health monitoring between modules and server
- Administrative interface for managing doors, credentials, and policies
- Developer-friendly and production-friendly configuration environments

### 2.3 Out of Scope (Current Phase)

- Cloud connectivity or remote access
- Multi-factor authentication
- Biometric readers (fingerprint, retinal)
- Tamper detection hardware
- Mobile application
- Integration with third-party access control systems

---

## 3. Architecture

### 3.1 Design Principles

The firmware architecture follows a domain-driven layering strategy intended to scale without rewrites as features expand. Three layers define the system:

**System FSM (Core):** A long-lived finite state machine that tracks system-level truths — is the network available? Is the device commissioned? Is it healthy enough to operate? Should all access be denied? The FSM is the top-level decision maker and orchestrates all module interactions.

**Modules (Stable Interfaces):** The FSM communicates with hardware through abstract module interfaces. A `credential_reader_module` exposes `read()` without caring whether the underlying driver is an MFRC522, PN532, or fingerprint scanner. An `access_point_module` exposes `unlock()`, `lock()`, and `is_open()` regardless of whether the hardware is an electric strike or magnetic lock. This layer enables future hardware changes without modifying business logic. Module interfaces should be defined as pure virtual C++ classes (e.g., `ICredentialReader` with `virtual read() = 0`) from the outset, enabling test doubles without refactoring.

**Drivers (Hardware Abstraction):** The lowest layer, directly interfacing with hardware through ESP-IDF APIs — SPI, GPIO, I2C, etc. Each driver is a self-contained ESP-IDF component that knows only about its specific hardware.

### 3.2 FSM → Module → Driver Relationship

```
┌─────────────────────────────────────────────────────────────────────┐
│                          System FSM                                 │
│         (commissioned? connected? healthy? access policy?)          │
└──────────┬──────────────┬──────────────┬──────────────┬─────────────┘
           │              │              │              │
           ▼              ▼              ▼              ▼
   ┌──────────────┐ ┌──────────────┐ ┌──────────────┐ ┌──────────────┐
   │ credential   │ │ access_point │ │   feedback   │ │ connectivity │
   │ _reader      │ │   _module    │ │   _module    │ │   _service   │
   │ _module      │ │              │ │              │ │              │
   │ read()→UID   │ │ unlock()     │ │ indicate()   │ │ is_online()  │
   │              │ │ lock()       │ │              │ │              │
   │              │ │ is_open()    │ │              │ │              │
   └──────┬───────┘ └───────┬──────┘ └──────┬───────┘ └──────┬───────┘
          │                 │               │                │
    ┌─────┴─────┐     ┌─────┴─────┐   ┌─────┴─────┐    ┌─────┴─────┐
    │  mfrc522  │     │door_strike│   │    led    │    │   wifi    │
    │  (pn532)  │     │reed_switch│   │  (buzzer) │    │(ESP-IDF)  │
    └───────────┘     └───────────┘   └───────────┘    └───────────┘
       DRIVERS           DRIVERS         DRIVERS          ESP-IDF
```

**Note on connectivity:** Since WiFi is intrinsic to the ESP32 platform, network management is handled by a `connectivity_service` at the services layer rather than a full module. The FSM queries connectivity exclusively through the service's `is_online()` interface and never calls `esp_wifi_*` APIs directly. This preserves the option to promote connectivity to a full module if alternative transports (Ethernet, cellular) are added in future hardware variants.

### 3.3 ESP-IDF Build Strategy

- **Leaf-only components:** Only leaf directories are registered as ESP-IDF components. Grouping folders (`modules/`, `drivers/`, `services/`) are organizational only and contain no `CMakeLists.txt` that calls `idf_component_register()`.
- **Explicit component inclusion:** The root `CMakeLists.txt` uses explicit `COMPONENT_DIRS` to control which components enter the build. This prevents accidental discovery of unfinished or placeholder components.
- **Conventional main location:** The `main/` application entry point sits at the project root following ESP-IDF convention, not nested under `components/`.

### 3.4 Component Dependency Model

Dependencies flow strictly downward. Cross-layer upward dependencies are prohibited.

| Layer | Components | Depends On |
|---|---|---|
| Application | `main` | core/system_fsm, services |
| Core | `system_fsm` | modules, services, communication |
| Modules | credential_reader, access_point, feedback | drivers, common |
| Services | event_bus, heartbeat, access, connectivity, provisioning, crypto | common, ESP-IDF APIs |
| Communication | protocol, client, handlers | services, common |
| Drivers | mfrc522, door_strike, reed_switch, led | common/config, ESP-IDF APIs |
| Common | types, config | ESP-IDF APIs only |

### 3.5 Event Bus Design

The event bus is the inter-component messaging backbone. Key design decisions:

- **Backed by FreeRTOS queues**, leveraging ESP-IDF's SMP FreeRTOS for dual-core operation on the ESP32-S3.
- **Pub/sub with typed event IDs**, defined in `common/types/event_types.h`.
- **Queue topology** must be decided and documented: single dispatcher queue (simpler, potential bottleneck with slow subscribers) vs. per-subscriber queues (more memory, better isolation). For the MVP with few subscribers, a single dispatcher is sufficient, but the decision should be recorded so it can be revisited.

### 3.6 Server Architecture

The server is a Go application currently operational with REST APIs for heartbeat data ingestion and card reader event processing. The target communication protocol is gRPC. The server manages door registrations, credential databases, access policies, and module health status. It also serves as the device provisioning authority.

**Recommendation:** Define `.proto` files now, even before implementing gRPC on either side. The protobuf message definitions serve as a contract between server and door module, and force concrete decisions about wire format (heartbeat payload fields, access request/response structures, provisioning handshake messages). The current HTTP APIs can remain in use for the MVP while the message semantics are locked down.

---

## 4. Security Model

### 4.1 Provisioning Strategy

Portunus uses a **server-built binary provisioning model**. The server, acting as the provisioning authority, compiles a unique firmware binary for each door module through an administrator-initiated process. Only devices with server-built firmware are recognized as legitimate, since identity material is embedded at build time.

#### Recommended Identity Material

The following should be considered for embedding in each device binary or its associated NVS partition:

- A unique device identifier (UUID) generated by the server at provisioning time
- A device-specific client certificate signed by a server-controlled certificate authority, enabling mutual TLS authentication
- The server CA certificate for certificate pinning (the module trusts only this CA)
- An initial pre-shared key or token for first-boot registration handshake

#### Alternative: NVS Partition-Based Provisioning

Rather than building a unique firmware binary per device, consider building a **single common firmware image** and generating a **unique NVS partition image per device** using ESP-IDF's `nvs_partition_gen.py`. The unique NVS partition would contain the device identity material (UUID, certificates, keys). This approach:

- Separates firmware development from device commissioning
- Avoids requiring the full ESP-IDF toolchain on the server at runtime
- Simplifies firmware updates since the common image can be updated independently of the identity partition

### 4.2 Communication Security

All communication between door modules and the server is encrypted over local WiFi using TLS. Both endpoints authenticate: the server verifies the module's identity via its embedded certificate, and the module verifies the server via certificate pinning. This prevents sniffing (encryption) and spoofing (mutual authentication).

### 4.3 Hardware Security

The ESP32-S3 supports flash encryption and secure boot natively. For production deployments, flash encryption should be enabled to protect embedded credentials from physical extraction. Tamper detection hardware is planned for a future phase.

---

## 5. Development Phases

### 5.1 Phase 1: MVP (Current)

**Objective:** Produce a reliable, flashable ESP32 firmware build that demonstrates the core architecture with minimal functionality. Validates the build system, component model, and basic hardware integration.

| Deliverable | Description | Status |
|---|---|---|
| Clean initialization | NVS flash init, FreeRTOS task creation, system startup sequence | In Progress |
| MFRC522 card reading | Read card UIDs and log them to serial console | In Progress |
| Periodic heartbeat | Emit heartbeat events (log or publish via event bus) | In Progress |
| Event bus foundation | FreeRTOS queue-backed pub/sub for inter-component events | In Progress |

**MVP build components** (controlled by explicit `COMPONENT_DIRS`):

- `main` — application entry point
- `platform/drivers/mfrc522` — RFID reader driver
- `services/heartbeat_service` — periodic health reporting
- `services/event_bus` — inter-component messaging

**MVP uses ESP-IDF's standard NVS API** (`nvs_flash.h`) directly, without a custom HAL wrapper, to eliminate unnecessary layers during this phase.

### 5.2 Phase 2: Hardware Integration

**Objective:** Bring all door module hardware online and implement the module abstraction layer.

| Deliverable | Description |
|---|---|
| Door strike driver + module | GPIO-based door strike control via `access_point_module` interface (`unlock()`/`lock()`) |
| Reed switch driver + module | Door state monitoring with software debounce (50ms minimum) via `access_point_module`. Even at MVP level, debounce should be included to prevent spurious state changes in logs. |
| LED driver + module | Status indication via `feedback_module` interface |
| Credential reader module | Abstract interface wrapping MFRC522 driver with `read()` returning credential data |
| System FSM v1 | Basic state machine: INIT → OPERATIONAL → ERROR states with module coordination |

### 5.3 Phase 3: Server Communication

**Objective:** Establish secure, reliable communication between door modules and the server.

| Deliverable | Description |
|---|---|
| WiFi connectivity service | Network management with connection monitoring and reconnection logic; `is_online()` interface for FSM |
| gRPC integration | Protocol buffer definitions and gRPC client implementation for server communication |
| Heartbeat transmission | Periodic health data reporting to server over encrypted channel |
| Access request flow | Credential read triggers server query; server response controls door strike |
| TLS with mutual auth | Encrypted transport with certificate-based device and server authentication |

### 5.4 Phase 4: Provisioning & Security

**Objective:** Implement the device provisioning pipeline and production security features.

| Deliverable | Description |
|---|---|
| Provisioning pipeline | Server-side build process generating per-device firmware or NVS partition images |
| Certificate management | Server-side CA, per-device certificate generation and signing |
| Flash encryption | ESP32-S3 flash encryption enabled for production builds |
| Secure boot | Verified boot chain to prevent unauthorized firmware |
| Crypto service | On-device cryptographic operations for message signing/verification |

### 5.5 Phase 5: Reliability & Production Readiness

**Objective:** Harden the system for production deployment.

| Deliverable | Description |
|---|---|
| Watchdog & recovery | FreeRTOS task watchdog configuration, automatic recovery from firmware hangs |
| Door state machine | Advanced states: closed, opened, held-open (alarm), forced-open (alarm). Configurable fail-secure vs. fail-open policy per door. |
| OTA firmware updates | Over-the-air update capability to avoid physical access for firmware changes |
| Server-offline behavior | Defined behavior when server is unreachable: configurable deny-all or cached-policy fallback |
| Unit testing | Module interface mocking via pure virtual classes, driver-level unit tests, integration tests |

### 5.6 Future Phases (Not Scheduled)

These items are architecturally accounted for but have no implementation timeline:

- Multi-factor authentication
- Biometric readers (fingerprint, retinal) — will require re-evaluating the credential reader module interface to support confidence scores
- Tamper detection module (accelerometer, enclosure switch)
- Audio feedback (buzzer driver)
- Display feedback (OLED/LCD driver)
- Alternative lock hardware (magnetic lock driver)

---

## 6. Hardware Specifications

### 6.1 Door Access Module (Development)

| Component | Specification |
|---|---|
| Microcontroller | ESP32-S3 WROOM-1 (dual-core Xtensa LX7, WiFi, BLE) |
| RFID Reader | MFRC522 (SPI interface, 13.56 MHz, MIFARE compatible) |
| Door Actuator | Electric door strike (GPIO-controlled via relay/MOSFET) |
| Door Sensor | Reed switch (magnetic, normally-open or normally-closed) |
| Status Indicator | LED (GPIO-controlled) |
| Development Platform | Breadboard integration |

### 6.2 Server (Development)

| Component | Specification |
|---|---|
| Hardware | Raspberry Pi 5 with integrated SSD |
| OS | Raspberry Pi OS / Linux |
| Runtime | Go |
| Alternative | Any generic Linux server on the local network |

---

## 7. Project Structure

```
access_module/
├── CMakeLists.txt                     # Root build config (explicit COMPONENT_DIRS)
├── sdkconfig                          # ESP-IDF SDK configuration
├── sdkconfig.defaults                 # Shared default settings
├── sdkconfig.defaults.dev             # Dev overrides (verbose logging, no flash encryption)
├── sdkconfig.defaults.prod            # Prod overrides (warning-only logs, flash encryption on)
├── README.md
├── .gitignore
│
├── main/                              # Application entry point (ESP-IDF convention)
│   ├── CMakeLists.txt
│   ├── idf_component.yml
│   ├── main.cpp
│   └── Kconfig.projbuild
│
├── components/
│   ├── common/
│   │   ├── types/                     # Dependency-free shared definitions
│   │   │   └── include/
│   │   │       ├── portunus_types.h
│   │   │       ├── error_codes.h
│   │   │       ├── event_types.h
│   │   │       ├── credential_types.h
│   │   │       └── system_states.h
│   │   └── config/                    # Build-time configuration (Kconfig-driven)
│   │       └── include/
│   │           ├── pin_config.h
│   │           ├── network_config.h
│   │           ├── timing_config.h
│   │           └── security_config.h
│   │
│   ├── core/
│   │   └── system_fsm/
│   │
│   ├── modules/
│   │   ├── credential_reader_module/
│   │   ├── access_point_module/
│   │   └── feedback_module/
│   │
│   ├── drivers/
│   │   ├── mfrc522/
│   │   ├── door_strike/
│   │   ├── reed_switch/
│   │   └── led/
│   │
│   ├── services/
│   │   ├── event_bus/
│   │   ├── heartbeat_service/
│   │   ├── access_service/
│   │   ├── provisioning_service/
│   │   ├── connectivity_service/
│   │   └── crypto_service/
│   │
│   └── communication/
│       ├── protocol/
│       ├── client/
│       └── handlers/
│
├── proto/                             # Protobuf definitions (shared contract)
│   ├── heartbeat.proto
│   ├── access.proto
│   └── provisioning.proto
│
├── scripts/
│   ├── build.sh
│   ├── flash.sh
│   ├── monitor.sh
│   ├── clean.sh
│   ├── menuconfig.sh
│   ├── provision.py
│   └── generate_certs.sh
│
├── test/
│   ├── unit/
│   │   ├── CMakeLists.txt
│   │   ├── mocks/
│   │   └── test_*.cpp
│   └── target/
│       ├── CMakeLists.txt
│       └── test_*.cpp
│
└── docs/
    ├── architecture.md
    ├── event_bus_design.md            # Queue topology, event type registry
    ├── hardware_setup.md
    ├── build_guide.md
    ├── provisioning.md
    └── api/
```

**Key structural notes:**

- `main/` is at the project root per ESP-IDF convention, not nested under `components/`.
- Each leaf directory under `components/` has its own `CMakeLists.txt` and `idf_component.yml`.
- Grouping directories (`modules/`, `drivers/`, `services/`, etc.) are organizational only — no CMake registration.
- A `proto/` directory is included for protobuf definitions that serve as the server-device communication contract.

---

## 8. Configuration Strategy

### 8.1 Environment Split

The project supports two configuration environments built on ESP-IDF's sdkconfig overlay mechanism:

**Build command (dev):**
```bash
idf.py -D SDKCONFIG_DEFAULTS="sdkconfig.defaults;sdkconfig.defaults.dev" build
```

**Build command (prod):**
```bash
idf.py -D SDKCONFIG_DEFAULTS="sdkconfig.defaults;sdkconfig.defaults.prod" build
```

ESP-IDF merges overlay files in order; the environment-specific file wins on conflicts.

| Setting | Dev | Prod |
|---|---|---|
| Log verbosity | Verbose / Debug | Warning only |
| Serial console | Enabled | Disabled or restricted |
| Flash encryption | Off | Enforced |
| Secure boot | Off | Enforced |
| OTA validation | Relaxed | Strict signature verification |

### 8.2 Config Header Strategy

Headers in `common/config/include/` (e.g., `pin_config.h`, `timing_config.h`) should pull values from Kconfig where possible rather than hardcoding. This keeps the dev/prod split managed in one place (the sdkconfig overlay files) rather than scattered across headers and build flags.

---

## 9. Key Design Decisions & Rationale

| Decision | Rationale |
|---|---|
| ESP-IDF over Arduino | Full access to FreeRTOS SMP, hardware security APIs, and the component build system needed for this architecture. |
| Leaf-only ESP-IDF components | Avoids component discovery issues, missing `idf_component_register()`, and dependency resolution chaos that are common ESP-IDF pitfalls. |
| Module abstraction over direct driver access | Enables hardware substitution (e.g., MFRC522 → PN532, strike → mag lock) without changing FSM logic. Future-proofs for biometrics. |
| FreeRTOS queue-backed event bus | Natural fit for ESP-IDF's SMP FreeRTOS on the dual-core ESP32-S3. Enables async, decoupled inter-component communication. |
| Server-built provisioning | Self-contained trust model — no external PKI required. Server is the single authority for device identity. |
| Go for server | Strong concurrency model, single-binary deployment, well-suited for gRPC. Runs easily on Raspberry Pi. |
| gRPC as target protocol | Strongly-typed contract (protobuf), bidirectional streaming (useful for real-time events), built-in TLS support. |
| No custom HAL for MVP | Eliminates unnecessary abstraction layers during early development. Direct ESP-IDF API use reduces debugging surface. |
| Connectivity as service, not module | WiFi is platform-intrinsic on ESP32. A service is sufficient unless alternative transports are introduced. |

---

## 10. Risks & Mitigations

| Risk | Impact | Mitigation |
|---|---|---|
| ESP-IDF toolchain required on server for per-device builds | Provisioning pipeline complexity, version management burden | Consider NVS partition-based provisioning as alternative (single firmware + unique NVS image per device) |
| Event bus becomes bottleneck under load | Dropped events, delayed access responses | Document queue topology decision now; design for per-subscriber queues as scaling option |
| Server unreachable during operation | Doors cannot authenticate, potential lockout or security gap | Define fail-secure/fail-open policy per door; implement cached credential fallback in Phase 5 |
| MFRC522 UID-only auth is weak | Card cloning possible | Acceptable for MVP/makerspace threat model; mitigated by MIFARE key-based authentication or migration to more secure readers in future phases |
| Flash encryption one-time-programmable on ESP32 | Cannot be reversed if misconfigured | Test thoroughly in dev environment before enabling in production; document process carefully |
| Scope creep from future features | Delays MVP and core functionality | Strict phase gating; future features (biometrics, tamper, multi-factor) are architecturally planned but not scheduled |

---

## 11. Open Items

These items require design decisions before or during their respective phases:

1. **Event bus queue topology:** Single dispatcher vs. per-subscriber queues. Document the decision and the criteria for revisiting it.
2. **Provisioning approach finalization:** Full per-device binary build vs. common firmware + unique NVS partition. Evaluate ESP-IDF toolchain burden on the server.
3. **Protobuf message definitions:** Define `.proto` files for heartbeat, access request/response, and provisioning handshake. This can begin immediately and informs both server and firmware work.
4. **Server-offline policy defaults:** What should a door module do when it cannot reach the server? This must be decided before Phase 3 integration.
5. **Biometric credential scoring model:** The current `read() → UID` interface assumes exact-match credentials. Biometric readers produce confidence scores. The module interface will need to accommodate this when biometrics are introduced.
6. **gRPC integration path for ESP32:** Evaluate available gRPC or protobuf-c libraries compatible with ESP-IDF. This is a dependency for Phase 3.
