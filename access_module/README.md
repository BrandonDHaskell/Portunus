# Portunus Access Module

ESP32-S3 firmware for the Portunus door access control system.

The access module runs on an ESP32-S3, reads RFID credentials through an MFRC522 reader, requests an access decision from the Portunus server, and controls local door hardware such as a strike, reed switch, and status LED. The current firmware is structured around a thin composition root in `main/`, a central `SystemFSM`, interface-driven hardware modules, and infrastructure services such as WiFi, heartbeat publishing, the event bus, and server communication.

**Current firmware version:** `0.1.0-mvp`  
**Framework:** ESP-IDF 5.x  
**Target MCU:** ESP32-S3

---

## What is implemented today

The current firmware includes:

- **RFID credential reading** using the MFRC522 over SPI
- **Door strike control** over GPIO
- **Door state sensing** via reed switch input
- **Status feedback** via a single GPIO LED
- **FreeRTOS event bus** for decoupled inter-component communication
- **System FSM** that owns:
  - card polling
  - access decision handling
  - unlock timing
  - reed switch monitoring
  - feedback coordination
- **WiFi station manager** with reconnect behavior
- **Heartbeat service** that publishes periodic health telemetry
- **Server communication service** that forwards heartbeat and access requests to the Portunus server
- **Two transport options** for module-to-server communication:
  - HTTP/1.1 + protobuf (default path)
  - gRPC over HTTP/2 + TLS (optional, build-time selectable)
- **TLS support** with either:
  - a private CA embedded in firmware for LAN deployments, or
  - the ESP-IDF certificate bundle for publicly trusted certificates
- **HMAC-SHA256 request signing** for application-layer authentication

This firmware is no longer just a reader + HTTP bridge. The system FSM, GPIO access point, feedback driver, and optional gRPC path are all implemented in this snapshot.

---

## Architecture at a glance

The access module is organized as a layered firmware system.

```text
main/                         Composition root
  └── constructs concrete modules and starts services

core/system_fsm/             Top-level runtime controller
  ├── owns unlock timing
  ├── polls door state
  ├── processes access decisions
  └── coordinates feedback

components/                  Shared headers and generated code
  ├── portunus_config/       Kconfig-backed configuration headers
  ├── portunus_interfaces/   Hardware abstraction interfaces
  ├── portunus_proto/        Nanopb-generated protobuf types
  └── portunus_types/        Shared event/types/error/state definitions

drivers/                     Concrete hardware implementations
  ├── access_point_gpio/     Door strike + reed switch
  ├── feedback_led/          Status LED feedback
  └── reader_mfrc522/        RFID reader

services/                    Infrastructure services
  ├── event_bus/             Publish/subscribe bus
  ├── grpc_client/           Lightweight unary gRPC client
  ├── heartbeat_service/     Periodic health publisher
  ├── server_comm/           Event bus ↔ server bridge
  └── wifi_mgr/              WiFi STA manager
```

### Runtime ownership model

- `main/main.cpp` is the **composition root**. It initializes NVS, WiFi, and the event bus, constructs concrete drivers, injects them into the FSM, starts independent services, and then returns.
- `core/system_fsm/` is the **top-level decision maker** after startup.
- Hardware is accessed through interfaces:
  - `ICredentialReader`
  - `IAccessPoint`
  - `IFeedback`
- Services communicate through the **event bus**, not by directly calling each other across layers.

---

## Current repository layout

```text
access_module/
├── CMakeLists.txt
├── README.md
├── partitions.csv
│
├── main/
│   ├── CMakeLists.txt
│   ├── Kconfig.projbuild
│   └── main.cpp
│
├── components/
│   ├── portunus_config/
│   │   ├── CMakeLists.txt
│   │   └── include/
│   │       ├── network_config.h
│   │       ├── pin_config.h
│   │       ├── security_config.h
│   │       └── timing_config.h
│   ├── portunus_interfaces/
│   │   ├── CMakeLists.txt
│   │   └── include/
│   │       ├── i_access_point.h
│   │       ├── i_credential_reader.h
│   │       └── i_feedback.h
│   ├── portunus_proto/
│   │   ├── CMakeLists.txt
│   │   ├── idf_component.yml
│   │   └── portunus/v1/portunus.pb.c
│   └── portunus_types/
│       ├── CMakeLists.txt
│       ├── include/
│       └── src/
│
├── core/
│   └── system_fsm/
│       ├── CMakeLists.txt
│       ├── include/system_fsm.h
│       └── src/system_fsm.cpp
│
├── drivers/
│   ├── README.md
│   ├── access_point_gpio/
│   ├── feedback_led/
│   └── reader_mfrc522/
│
├── services/
│   ├── event_bus/
│   ├── grpc_client/
│   ├── heartbeat_service/
│   ├── server_comm/
│   └── wifi_mgr/
│
└── scripts/
    ├── clean.py
    └── proto_gen.py
```

---

## Startup sequence

The current startup flow in `main/main.cpp` is:

1. Initialize NVS
2. Initialize and start WiFi (when enabled)
3. Initialize the event bus
4. Construct concrete modules based on enabled hardware features
5. Construct and initialize `SystemFSM`
6. Start independent services:
   - heartbeat service
   - server communication service
7. Start the FSM
8. Return from `app_main()` and let FreeRTOS tasks continue running

Important detail: `main.cpp` does **not** act as the runtime controller and does **not** subscribe to all events. That responsibility now lives in the FSM and the infrastructure services.

---

## Event flow

The event bus is the backbone of the firmware.

### Access request flow

```text
ReaderMfrc522
   └── publishes EVENT_CREDENTIAL_READ
            ├── SystemFSM shows CARD_READ feedback
            └── server_comm sends request to server
                     └── publishes EVENT_ACCESS_GRANTED or EVENT_ACCESS_DENIED
                              └── SystemFSM unlocks/denies and drives feedback
```

### Heartbeat flow

```text
heartbeat_service
   └── publishes EVENT_HEARTBEAT
            └── server_comm sends heartbeat to server
```

### Door state flow

```text
SystemFSM
   └── polls reed switch through IAccessPoint
            ├── publishes EVENT_DOOR_OPENED
            └── publishes EVENT_DOOR_CLOSED
```

### Offline behavior

When WiFi is unavailable:

- heartbeats are dropped
- credential reads are still detected locally
- the FSM logs the read and shows error feedback
- if `server_comm` is running but WiFi is down, it publishes a synthetic deny reason (`no_network`) so the FSM can clear waiting feedback cleanly

Offline mode is useful for bench testing, but it is not a substitute for the normal authorization path.

---

## System FSM responsibilities

`SystemFSM` is implemented and is central to the current firmware.

It currently:

- initializes available modules and records runtime capability flags
- subscribes to relevant event bus events
- runs an FSM task for event processing
- runs a separate card polling task when a reader is present
- reacts to `EVENT_ACCESS_GRANTED` by unlocking the strike and starting an unlock hold timer
- reacts to `EVENT_ACCESS_DENIED` by driving deny feedback
- polls the reed switch and publishes door-open / door-closed events
- re-locks the door when:
  - the unlock timer expires, or
  - the door opens and then closes during the unlock window
- updates network capability dynamically from `wifi_mgr`

This is no longer a planned subsystem. It is part of the active runtime design.

---

## Supported hardware in this snapshot

### Implemented drivers

| Driver | Interface | Hardware |
|---|---|---|
| `reader_mfrc522/` | `ICredentialReader` | MFRC522 RFID reader over SPI |
| `access_point_gpio/` | `IAccessPoint` | Door strike + reed switch over GPIO |
| `feedback_led/` | `IFeedback` | Single GPIO status LED |

### Default development pin mapping

#### MFRC522

| MFRC522 Pin | ESP32-S3 GPIO |
|---|---:|
| MOSI | 37 |
| MISO | 38 |
| SCK | 36 |
| SDA / CS | 35 |
| RST | 4 |

#### Door hardware

| Function | ESP32-S3 GPIO |
|---|---:|
| Door strike | 5 |
| Reed switch | 6 |
| Status LED | 7 |

All of these defaults are configurable through `idf.py menuconfig`.

---

## Build and flash

### Prerequisites

- ESP-IDF 5.x installed and exported in the current shell
- Python 3.9+
- ESP32-S3 board connected over USB

### From the repository root using Task

```bash
. ~/esp/esp-idf/export.sh
cd Portunus

task firmware:build
task firmware:flash-monitor
```

### Directly inside `access_module/`

```bash
. ~/esp/esp-idf/export.sh
cd Portunus/access_module

idf.py build
idf.py flash monitor
```

---

## Configuration

All firmware-specific settings are exposed through:

```bash
idf.py menuconfig
```

Navigate to:

```text
Portunus Configuration
```

### Main configuration groups

- **Feature Toggles**
  - MFRC522
  - heartbeat service
  - WiFi
  - door strike
  - reed switch
  - LED
- **Security Configuration**
  - TLS enable/disable
  - TLS port
  - skip verification (dev only)
  - custom CA pinning
  - HMAC signing
  - HMAC secret
  - gRPC transport enable/disable
  - gRPC server port
- **Network Configuration**
  - module identifier
  - WiFi SSID/password
  - connect timeout
  - reconnect interval
  - server host/port
  - request timeout
- **SPI Pin Assignments (MFRC522)**
- **Door Hardware Pin Assignments**
- **Door Configuration**
  - unlock hold time
  - reed switch debounce
  - FSM poll interval
- **Timing Configuration**
  - heartbeat interval
  - card poll interval
  - re-read delay
  - event bus timeout
- **Event Bus Configuration**
  - queue depth
  - max subscribers

Feature flags matter in the current implementation. Disabled hardware is not constructed, and the FSM adapts by receiving `nullptr` for that module.

---

## Server communication

The current firmware supports two transport paths.

### 1. HTTP/1.1 + protobuf (default)

This is the original and currently default communication path.

Requests sent by the access module:

- `POST /v1/heartbeat`
- `POST /v1/access_request`

The request and response bodies are protobuf messages encoded with Nanopb.

### 2. gRPC over HTTP/2 + TLS (optional)

When `CONFIG_PORTUNUS_USE_GRPC=y`, the firmware uses the lightweight `grpc_client` service and calls the Portunus gRPC service instead of the HTTP endpoints.

Current design characteristics:

- unary RPCs only
- persistent TLS + HTTP/2 connection reuse
- same protobuf messages carried inside gRPC framing
- HMAC signature attached as metadata
- requires TLS

### Current `server_comm` role

`services/server_comm/` is the bridge between the local firmware runtime and the server. It:

- subscribes to `EVENT_HEARTBEAT`
- subscribes to `EVENT_CREDENTIAL_READ`
- forwards those events to the server
- decodes the server response
- publishes `EVENT_ACCESS_GRANTED` or `EVENT_ACCESS_DENIED` back to the event bus

The network I/O happens on its own FreeRTOS task so the event bus dispatcher is not blocked by HTTP or gRPC round trips.

---

## Security features currently supported

The access module currently supports:

- **TLS** for encrypted transport
- **Custom CA pinning** for LAN deployments
- **ESP-IDF certificate bundle validation** for public CA deployments
- **HMAC-SHA256 request signing** using `X-Portunus-Sig`

### Generate a private CA and server certificate

From the repository root:

```bash
./scripts/generate_certs.sh --ip <SERVER_IP>
```

This script:

- creates a private CA in `certs/`
- creates a server certificate in `certs/`
- copies the CA certificate into:

```text
access_module/certs/ca_cert.pem
```

That certificate is embedded into the firmware when custom CA pinning is enabled.

### Current security caveat

The HMAC secret is currently configured through Kconfig and compiled into the firmware image. That matches the current code, but it also means firmware binaries should be treated as sensitive build artifacts.

---

## Protobuf code generation

If `proto/portunus/v1/portunus.proto` changes, regenerate the embedded C stubs.

From the repository root:

```bash
task proto:gen
```

Or from inside `access_module/`:

```bash
python scripts/proto_gen.py
```

The generated Nanopb sources are committed under:

```text
access_module/components/portunus_proto/portunus/v1/
```

---

## Current scope and limitations

To keep expectations aligned with the actual code in this snapshot:

- the firmware currently targets **MFRC522-based RFID input**
- feedback is currently a **single status LED**, not a full multi-modal system
- the module relies on the server for access authorization decisions
- offline operation is for **bench testing / degraded behavior**, not independent authorization
- secure boot, flash encryption, and NVS-based secret storage are **not part of the default implemented firmware flow in this snapshot**

---

## Recommended docs to read next

- `../README.md` — project root overview
- `../docs/architecture.md` — system-wide architecture
- `../docs/api.md` — server API details
- `../docs/setup_firmware.md` — deeper firmware setup guidance
- `drivers/README.md` — driver naming and extension patterns
- `../docs/shared_secrets_setup.md` — TLS certificate setup, HMAC shared secret, and credential hash secret configuration