# Portunus Access Module

ESP32-S3 firmware for the Portunus door access control system. Reads MIFARE RFID cards via an MFRC522 reader, communicates access decisions with the Portunus server over HTTP/protobuf, and publishes all inter-component events through a FreeRTOS queue-backed event bus.

**Firmware version:** `0.1.0-mvp`
**Target:** ESP32-S3 (ESP-IDF framework)

## Prerequisites

- [ESP-IDF](https://docs.espressif.com/projects/esp-idf/en/stable/esp32s3/get-started/) v5.x
- Python 3.9+ (for utility scripts)
- An ESP32-S3 development board with an MFRC522 RFID module wired via SPI

## Building

```bash
# Set up the ESP-IDF environment (once per terminal session)
. $HOME/esp/esp-idf/export.sh

# Configure project options (WiFi credentials, pin assignments, etc.)
idf.py menuconfig

# Build
idf.py build

# Flash and monitor serial output
idf.py -p /dev/ttyUSB0 flash monitor
```

## Kconfig Highlights

All runtime parameters live under **Portunus Configuration** in menuconfig:

| Menu                  | Key Settings                                                      |
|-----------------------|-------------------------------------------------------------------|
| Feature Toggles       | Enable/disable MFRC522, WiFi, and heartbeat independently         |
| Network Configuration | WiFi SSID/password, server host/port, module ID, request timeout  |
| SPI Pin Assignments   | GPIO pins for MOSI, MISO, SCLK, CS, and RST (MFRC522)            |
| Timing Configuration  | Heartbeat interval, card poll interval, re-read debounce delay    |
| Event Bus             | Queue depth, max subscriber count                                 |
| Task Configuration    | Stack sizes for FreeRTOS tasks                                    |

Default pin mapping (ESP32-S3, development breadboard):

| MFRC522 Pin | GPIO |
|-------------|------|
| MOSI        | 37   |
| MISO        | 38   |
| SCLK        | 36   |
| SDA (CS)    | 35   |
| RST         | 4    |

## Component Map

```
access_module/
├── main/                          Application entry point & startup sequence
│
├── components/
│   ├── common/
│   │   ├── types/                 Dependency-free shared types (credential_t,
│   │   │                          error codes, event types, system states)
│   │   └── config/                Kconfig-driven constants (pins, timing,
│   │                              network, security)
│   │
│   ├── drivers/
│   │   └── mfrc522/               SPI driver for the MFRC522 RFID reader
│   │                              (ISO 14443A, 4- and 7-byte UIDs)
│   │
│   ├── services/
│   │   ├── event_bus/             FreeRTOS queue-backed publish/subscribe bus
│   │   ├── heartbeat_service/     Periodic health telemetry (uptime, heap)
│   │   ├── wifi_mgr/             WiFi STA manager with exponential-backoff
│   │   │                          reconnection
│   │   └── server_comm/           HTTP + Nanopb bridge to the Portunus server
│   │                              (heartbeat & access request/response)
│   │
│   └── proto/                     Nanopb-generated C stubs from portunus.proto
│
├── scripts/
│   ├── proto_gen.py               Regenerate Go + Nanopb protobuf code
│   ├── check_fmt.py               Code formatting checker
│   └── clean.py                   Build artifact cleanup
│
└── partitions.csv                 Custom partition table (NVS + factory app)
```

## Startup Sequence

1. NVS flash initialisation
2. WiFi station connection (blocks until IP or timeout; reconnects in background)
3. Event bus creation and subscriber registration
4. MFRC522 driver init and card-polling task start
5. Heartbeat service start
6. Server communication component start
7. Transition to `SYSTEM_STATE_OPERATIONAL`

## Event Flow

All inter-component communication flows through the event bus:

```
MFRC522 Driver ──► EVENT_CREDENTIAL_READ ──► server_comm ──► HTTP POST /v1/access_request
                                                                  │
                                          EVENT_ACCESS_GRANTED ◄──┘
                                          EVENT_ACCESS_DENIED  ◄──┘

Heartbeat Service ──► EVENT_HEARTBEAT ──► server_comm ──► HTTP POST /v1/heartbeat
```

Subscriber callbacks in `main.cpp` log all events to the serial console.

## Server Communication

The access module communicates with the Portunus server using HTTP/1.1 with Nanopb-encoded protobuf payloads:

- **`POST /v1/heartbeat`** — periodic health report (module ID, uptime, heap, RSSI, IP)
- **`POST /v1/access_request`** — card UID presented, server returns grant/deny decision

The server host, port, and request timeout are configured via Kconfig.

## Regenerating Protobuf Code

After modifying `proto/portunus/v1/portunus.proto`, regenerate the Nanopb stubs:

```bash
python scripts/proto_gen.py --nanopb       # Nanopb C stubs only
python scripts/proto_gen.py                 # Both Go and Nanopb
python scripts/proto_gen.py --check         # Generate and verify no drift from committed files
```

## Future Phases (Planned)

The following directories are scaffolded but not yet implemented:

- `components/controllers/` — door controller, feedback controller (LED/buzzer), RFID controller abstraction
- `components/core/system_fsm/` — full system state machine with sub-states
- `components/communication/` — additional communication protocols
- `components/app/` — high-level application logic layer