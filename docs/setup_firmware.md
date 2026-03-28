# Portunus — Firmware Setup (Access Module)

Build, configure, and flash the ESP32-S3 firmware for the Portunus door access module.

**Last updated:** March 2026

---

## Prerequisites at a glance

### Software

| Dependency | Minimum version | Purpose | Install section |
|---|---|---|---|
| ESP-IDF | 5.4+ | ESP32 toolchain, build system, FreeRTOS | [Install ESP-IDF](#install-esp-idf) |
| Python | 3.9+ | ESP-IDF tools, utility scripts | Installed with ESP-IDF |
| Task | 3.x | Task runner (build, flash, menuconfig commands) | [Install Task](#install-task) |
| openssl | 1.1.1+ | Generate TLS certificates for server pinning | [TLS certificate setup](#tls-certificate-setup) |
| protoc + Nanopb | 3.21+ / 0.4.9+ | Regenerate protobuf C stubs (only if `.proto` files change) | [Protobuf tooling](#protobuf-tooling-optional) |
| Git | any | Clone the repository | Usually pre-installed on Debian |

ESP-IDF bundles its own Python virtual environment, cross-compiler toolchain (Xtensa GCC), and all necessary libraries. No separate C/C++ cross-compiler install is required.

### Hardware

| Component | Specification | Purpose |
|---|---|---|
| ESP32-S3 WROOM-1 dev board | Dual-core Xtensa LX7, WiFi, BLE, USB-UART | Microcontroller running the access module firmware |
| MFRC522 RFID module | SPI interface, 13.56 MHz, ISO 14443A | Reads MIFARE RFID cards/tags |
| Electric door strike | 12V or 24V, fail-secure | Door lock actuator (controlled via relay or MOSFET) |
| Reed switch | Normally-open or normally-closed magnetic | Door open/closed state sensing |
| Relay or MOSFET module | Logic-level compatible (3.3V trigger) | Switches the door strike from ESP32 GPIO |
| LED | Standard 3.3V or with current-limiting resistor | Status feedback (access granted, denied, system state) |
| USB cable | USB-A to USB-C or Micro-USB (matches your dev board) | Programming and serial monitor |
| Breadboard + jumper wires | — | Development wiring |

For bench testing, you can start with just the ESP32 and MFRC522. The door strike, reed switch, and LED can each be disabled independently via Kconfig so you don't need all hardware present to build and test.

---

## Wiring reference

### MFRC522 RFID reader (SPI)

| MFRC522 Pin | ESP32-S3 GPIO | Kconfig setting |
|---|---|---|
| MOSI | GPIO 37 | `PORTUNUS_SPI_MOSI_PIN` |
| MISO | GPIO 38 | `PORTUNUS_SPI_MISO_PIN` |
| SCK | GPIO 36 | `PORTUNUS_SPI_SCLK_PIN` |
| SDA (CS) | GPIO 35 | `PORTUNUS_SPI_CS_PIN` |
| RST | GPIO 4 | `PORTUNUS_MFRC522_RST_PIN` |
| 3.3V | 3V3 | — |
| GND | GND | — |

The MFRC522 operates at 3.3V. Do not connect to 5V — it will damage the module.

### Door strike

| Connection | ESP32-S3 GPIO | Kconfig setting |
|---|---|---|
| Relay/MOSFET signal | GPIO 5 | `PORTUNUS_DOOR_STRIKE_PIN` |

The ESP32 GPIO drives a relay or logic-level MOSFET that switches the door strike's power supply. The strike itself runs on its own 12V/24V supply — never power it from the ESP32. Configure active-high (default) or active-low logic via `PORTUNUS_DOOR_STRIKE_ACTIVE_LOW` in menuconfig.

### Reed switch

| Connection | ESP32-S3 GPIO | Kconfig setting |
|---|---|---|
| Reed switch signal | GPIO 6 | `PORTUNUS_REED_SWITCH_PIN` |

The firmware enables an internal pull-up on this pin. Wire the reed switch between the GPIO pin and GND. Default configuration is normally-open (circuit closed when door is shut, i.e., magnet aligned with sensor). If your reed switch is normally-closed, enable `PORTUNUS_REED_SWITCH_NC` in menuconfig. Software debounce is applied at 50ms (configurable via `PORTUNUS_REED_SWITCH_DEBOUNCE_MS`).

### Status LED

| Connection | ESP32-S3 GPIO | Kconfig setting |
|---|---|---|
| LED anode (through resistor) | GPIO 7 | `PORTUNUS_LED_PIN` |

Active-high. Use an appropriate current-limiting resistor for your LED (e.g. 220Ω for a standard 3.3V LED).

### Pin assignments are configurable

All pin assignments above are defaults. Override any of them in menuconfig under **Portunus Configuration → SPI Pin Assignments** and **Portunus Configuration → Door Hardware Pin Assignments** without changing code.

---

## Install ESP-IDF

ESP-IDF is Espressif's official development framework. It includes the Xtensa cross-compiler, FreeRTOS, drivers, and the `idf.py` build tool.

Install prerequisites (Debian/Ubuntu):

```bash
sudo apt update
sudo apt install -y git wget flex bison gperf python3 python3-pip \
  python3-venv cmake ninja-build ccache libffi-dev libssl-dev \
  dfu-util libusb-1.0-0
```

Clone and install ESP-IDF:

```bash
mkdir -p ~/esp
cd ~/esp
git clone -b v5.4.1 --recursive https://github.com/espressif/esp-idf.git
cd esp-idf
./install.sh esp32s3
```

The `install.sh` script creates a Python virtual environment and downloads the Xtensa toolchain. It does not modify your system Python.

After installation, you must source the ESP-IDF environment in every terminal session before building:

```bash
. ~/esp/esp-idf/export.sh
```

To avoid typing this every time, add an alias to your shell profile:

```bash
echo 'alias get_idf=". ~/esp/esp-idf/export.sh"' >> ~/.bashrc
```

Then run `get_idf` at the start of each session.

Verify the installation:

```bash
idf.py --version
```

---

## Install Task

[Task](https://taskfile.dev) is used as the project's build/test runner. Install it via Go or a standalone binary.

If you have Go installed:

```bash
go install github.com/go-task/task/v3/cmd/task@latest
```

If you don't have Go (and only need the firmware side), install Task directly:

```bash
sh -c "$(curl --location https://taskfile.dev/install.sh)" -- -d -b ~/.local/bin
```

Verify:

```bash
task --version
```

---

## Clone and build

```bash
git clone https://github.com/BrandonDHaskell/Portunus.git
cd Portunus

# Source the ESP-IDF environment
. ~/esp/esp-idf/export.sh

# Build the firmware
task firmware:build
```

On the first build, the IDF Component Manager automatically downloads two external dependencies from the Espressif component registry: `nikas-belogolov/nanopb` (protobuf runtime) and `espressif/nghttp` (HTTP/2, only used when gRPC is enabled). These are cached in `access_module/managed_components/` and do not require manual installation.

---

## Configuration (menuconfig)

The firmware is configured through ESP-IDF's Kconfig system. All Portunus-specific settings live under a single menu:

```bash
task firmware:menuconfig
```

This opens a terminal UI. Navigate to **Portunus Configuration** to find the following submenus:

### Feature Toggles

Enable or disable hardware components independently. This is how you build firmware for bench testing without all hardware connected.

| Setting | Default | Description |
|---|---|---|
| `PORTUNUS_ENABLE_MFRC522` | y | MFRC522 RFID reader and card polling task |
| `PORTUNUS_ENABLE_DOOR_STRIKE` | y | Door strike GPIO output |
| `PORTUNUS_ENABLE_REED_SWITCH` | y | Reed switch GPIO input and debounce |
| `PORTUNUS_ENABLE_LED` | y | Status LED and pattern task |
| `PORTUNUS_ENABLE_WIFI` | y | WiFi STA and server communication |
| `PORTUNUS_ENABLE_HEARTBEAT` | y | Periodic heartbeat reporting |

When a feature is disabled, its driver is not compiled and its FreeRTOS task is not created. The FSM receives a `nullptr` for that module and adapts its behavior accordingly.

### Network Configuration

| Setting | Default | Description |
|---|---|---|
| `PORTUNUS_MODULE_ID` | `door-001` | Unique name sent in every heartbeat and access request (max 32 chars) |
| `PORTUNUS_WIFI_SSID` | `portunus-dev` | WiFi network SSID |
| `PORTUNUS_WIFI_PASSWORD` | *(empty)* | WPA2 passphrase |
| `PORTUNUS_SERVER_HOST` | `192.168.1.100` | Portunus server IP or hostname |
| `PORTUNUS_SERVER_PORT` | `8080` | Server HTTP port (used when TLS is disabled) |
| `PORTUNUS_SERVER_REQUEST_TIMEOUT_MS` | `5000` | HTTP/gRPC request timeout |
| `PORTUNUS_WIFI_CONNECT_TIMEOUT_MS` | `15000` | Max time to wait for WiFi at boot |
| `PORTUNUS_WIFI_RECONNECT_INTERVAL_MS` | `1000` | Base reconnect delay (doubles on failure, caps at 60s) |

### Security Configuration

| Setting | Default | Description |
|---|---|---|
| `PORTUNUS_USE_TLS` | y | Enable HTTPS for server communication |
| `PORTUNUS_TLS_SERVER_PORT` | `8443` | HTTPS port (used when TLS is enabled) |
| `PORTUNUS_TLS_SKIP_VERIFY` | n | Skip certificate validation — **dev only, never in production** |
| `PORTUNUS_TLS_USE_CUSTOM_CA` | y | Pin to embedded CA cert instead of Mozilla bundle (recommended for LAN) |
| `PORTUNUS_HMAC_ENABLED` | y | Sign requests with HMAC-SHA256 |
| `PORTUNUS_HMAC_SECRET` | *(empty)* | Pre-shared HMAC key — must match `PORTUNUS_HMAC_SECRET` on the server |

### Transport Configuration

| Setting | Default | Description |
|---|---|---|
| `PORTUNUS_USE_GRPC` | n | Use gRPC (HTTP/2) instead of HTTP/1.1 (requires TLS) |
| `PORTUNUS_GRPC_SERVER_PORT` | `50051` | gRPC server port |

### Timing Configuration

| Setting | Default | Range | Description |
|---|---|---|---|
| `PORTUNUS_HEARTBEAT_INTERVAL_MS` | `10000` | 1000–60000 | Heartbeat reporting interval |
| `PORTUNUS_MFRC522_POLL_INTERVAL_MS` | `250` | 50–2000 | Card reader polling interval |
| `PORTUNUS_CARD_REREAD_DELAY_MS` | `1000` | 200–5000 | Delay after a successful card read to prevent re-reads |
| `PORTUNUS_UNLOCK_HOLD_MS` | `5000` | 1000–30000 | How long the strike stays energized after access granted |
| `PORTUNUS_FSM_POLL_INTERVAL_MS` | `100` | 50–500 | Reed switch poll and unlock timer check interval |
| `PORTUNUS_REED_SWITCH_DEBOUNCE_MS` | `50` | 20–200 | Reed switch debounce time |

### Event Bus Configuration

| Setting | Default | Range | Description |
|---|---|---|---|
| `PORTUNUS_EVENT_QUEUE_LENGTH` | `16` | 4–64 | Dispatcher queue depth |
| `PORTUNUS_MAX_EVENT_SUBSCRIBERS` | `8` | 2–32 | Maximum subscriber callbacks |
| `PORTUNUS_EVENT_QUEUE_TIMEOUT_MS` | `100` | 10–5000 | Queue send/receive timeout |

---

## Dev vs. production builds

The firmware supports sdkconfig overlays for managing dev and prod differences in a single command:

```bash
# Dev build (verbose logging, no flash encryption, TLS cert skip optional)
task firmware:build:dev

# Production build (warning-only logs, flash encryption, strict TLS)
task firmware:build:prod
```

These commands use the corresponding sdkconfig overlay files:

| Setting | Dev | Prod |
|---|---|---|
| Log verbosity | Verbose / Debug | Warning only |
| Serial console | Enabled | Disabled or restricted |
| Flash encryption | Off | Enforced |
| Secure boot | Off | Enforced |
| TLS cert verification | Can be skipped | Strict |

When switching between dev and prod overlays, do a full clean first to avoid stale sdkconfig artifacts:

```bash
task firmware:clean
task firmware:build:prod
```

---

## TLS certificate setup

For the firmware to validate the server's TLS certificate on a private LAN, it needs the CA certificate embedded in flash. This is handled automatically if you've already run the cert generation script (see [Server Setup — TLS certificate setup](setup_server.md#tls-certificate-setup)):

```bash
# From the repo root:
task certs:generate -- --ip 192.168.1.100
```

This generates `certs/ca.pem` and copies it to `access_module/certs/ca_cert.pem`. The firmware build system (`server_comm/CMakeLists.txt`) embeds this file into the binary via `EMBED_TXTFILES`. At runtime, the ESP-IDF mbedTLS stack validates the server certificate against this embedded CA.

If the file is missing and `PORTUNUS_TLS_USE_CUSTOM_CA` is enabled, the build fails with a clear error message pointing you to the cert generation script.

For development without generating certificates, you can temporarily disable cert validation:

```bash
# In menuconfig: Portunus Configuration → Security Configuration
#   → Skip TLS certificate verification = y
#
# WARNING: This is insecure. Do not use in production.
```

---

## HMAC secret provisioning

The firmware signs every outgoing request with HMAC-SHA256 using a pre-shared key. This key must match the `PORTUNUS_HMAC_SECRET` environment variable on the server.

Generate a secret:

```bash
openssl rand -hex 32
```

Set it in the firmware via menuconfig:

```
Portunus Configuration → Security Configuration → HMAC shared secret
```

And on the server via environment variable:

```bash
export PORTUNUS_HMAC_SECRET=<same-64-char-hex-string>
```

The HMAC secret is stored in flash as part of the firmware binary. Treat firmware binaries as sensitive artifacts. For production, a future enhancement will move secrets to an encrypted NVS partition.

---

## Flashing and monitoring

Connect the ESP32-S3 to your computer via USB.

```bash
# Flash the firmware
task firmware:flash

# Flash and immediately open the serial monitor
task firmware:flash-monitor

# Open the serial monitor only (firmware already flashed)
task firmware:monitor
```

If you have multiple serial devices, specify the port explicitly:

```bash
idf.py -p /dev/ttyUSB0 flash monitor
```

On Debian, you may need to add your user to the `dialout` group for serial port access:

```bash
sudo usermod -a -G dialout $USER
# Log out and back in for the change to take effect
```

### What to expect on first boot

A successful boot sequence in the serial monitor looks like:

```
Portunus Access Module v0.1.0-mvp
NVS initialised
WiFi STA started — connecting to "your-ssid"...
WiFi connected — IP: 192.168.1.50
Event bus initialised (queue depth=16, max subscribers=8)
Credential reader: OK
Access point: OK
Feedback: OK
Capabilities: reader=1 access_point=1 feedback=1 network=1
System FSM running — state=OPERATIONAL
Heartbeat OK — known=1 server_time=2026-03-26T...
```

If any hardware is disabled or fails to initialize, you'll see the corresponding capability set to 0 and a warning. The FSM adapts and continues operating with whatever hardware is available.

---

## Switching between dev and production servers

When using a single ESP32 for both development and production testing, the key settings that change between environments are:

| Setting | Dev value | Prod value |
|---|---|---|
| `PORTUNUS_SERVER_HOST` | Dev machine IP (e.g. `192.168.1.50`) | Pi IP (e.g. `192.168.1.100`) |
| `PORTUNUS_SERVER_PORT` | `8080` (or TLS port) | `8443` |
| `PORTUNUS_TLS_SERVER_PORT` | `8443` | `8443` |
| `PORTUNUS_HMAC_SECRET` | Dev secret | Prod secret |
| `PORTUNUS_TLS_SKIP_VERIFY` | Optionally `y` | Must be `n` |
| `PORTUNUS_MODULE_ID` | `door-001` | `door-001` (or unique per-door) |

The cleanest way to switch is to use the sdkconfig overlay system:

1. Run a full clean: `task firmware:clean`
2. Build with the target overlay: `task firmware:build:dev` or `task firmware:build:prod`
3. Flash: `task firmware:flash`

Alternatively, change individual settings via `task firmware:menuconfig` and rebuild.

**Important:** If the TLS CA certificate differs between your dev and prod servers (e.g., different IPs in the cert SAN), you need to regenerate and re-embed the cert when switching. Re-run `task certs:generate -- --ip <TARGET_SERVER_IP>` and rebuild.

---

## Protobuf tooling (optional)

Protobuf code generation is only needed if you modify `proto/portunus/v1/portunus.proto`. The generated Nanopb C files (`portunus.pb.c` and `portunus.pb.h`) are committed to the repo at `access_module/components/portunus_proto/portunus/v1/`.

If you do need to regenerate:

```bash
# Install protoc (the protobuf compiler)
sudo apt install -y protobuf-compiler

# Install the Nanopb generator
pip install nanopb --break-system-packages
# Or via the ESP-IDF managed component (already downloaded on first build)

# Regenerate Nanopb C stubs only
task proto:gen:nanopb

# Regenerate both Go and Nanopb stubs
task proto:gen

# CI check: regenerate and fail if output differs from committed files
task proto:check
```

The Nanopb options file at `proto/nanopb/portunus.options` controls field size limits (e.g., max string lengths for module_id, card_id, reason) to keep the generated structs fixed-size and stack-allocatable.

---

## Available task commands

All firmware-related commands from the project `Taskfile.yml`:

| Command | Description |
|---|---|
| `task firmware:build` | Build with default sdkconfig |
| `task firmware:build:dev` | Build with dev overlay (verbose logs, no encryption) |
| `task firmware:build:prod` | Build with prod overlay (minimal logs, encryption enforced) |
| `task firmware:flash` | Flash to connected ESP32 |
| `task firmware:monitor` | Open serial monitor |
| `task firmware:flash-monitor` | Flash and monitor in one step |
| `task firmware:menuconfig` | Open the Kconfig configuration UI |
| `task firmware:clean` | Full clean of build directory |
| `task certs:generate` | Generate CA + server TLS certs |
| `task certs:verify` | Verify the certificate chain |
| `task proto:gen:nanopb` | Regenerate Nanopb C stubs only |
| `task proto:gen` | Regenerate all protobuf code (Go + Nanopb) |
| `task proto:check` | Verify generated code is up to date (CI) |

---

## Component architecture

The firmware follows a layered architecture. Understanding this helps when extending or debugging.

```
main/                              Composition root — constructs modules, injects into FSM
  │
  ▼
core/system_fsm/                   Top-level state machine (BOOT → OPERATIONAL → ERROR)
  │                                Owns card polling, unlock timing, reed switch monitoring
  │                                Programs against interfaces only — no hardware knowledge
  │
  ├── components/portunus_interfaces/
  │     ICredentialReader           read() → credential_t
  │     IAccessPoint               unlock(), lock(), is_open()
  │     IFeedback                  indicate(feedback_type_t)
  │
  ▼
drivers/                           Concrete implementations of the interfaces above
  reader_mfrc522/                  ICredentialReader → SPI MFRC522 driver
  access_point_gpio/               IAccessPoint → door strike GPIO + reed switch GPIO
  feedback_led/                    IFeedback → single LED with pattern task
  │
services/                          Infrastructure (no hardware knowledge)
  event_bus/                       FreeRTOS queue-backed pub/sub
  heartbeat_service/               Periodic health telemetry
  wifi_mgr/                        WiFi STA with exponential-backoff reconnect
  server_comm/                     Event bus ↔ server bridge (HTTP or gRPC + protobuf)
  grpc_client/                     HTTP/2+TLS client using nghttp2 (conditional build)
  │
components/                        Shared, dependency-free
  portunus_types/                  credential_t, error codes, event types, system states
  portunus_config/                 Kconfig-driven constants (pins, timing, network, security)
  portunus_proto/                  Nanopb-generated protobuf message types
```

Dependencies flow strictly downward. The FSM never imports a driver directly — it uses the interface. Drivers never import other drivers. Services never import drivers. This makes it possible to swap hardware (e.g., MFRC522 → PN532, electric strike → magnetic lock) by implementing the interface without changing any business logic.

---

## Troubleshooting

**"idf.py: command not found"** — You need to source the ESP-IDF environment in your current terminal: `. ~/esp/esp-idf/export.sh`. This must be done in every new terminal session.

**"Permission denied" on /dev/ttyUSB0** — Add your user to the `dialout` group: `sudo usermod -a -G dialout $USER`. Log out and back in.

**Build fails with "Custom CA cert not found"** — The firmware is configured to pin a CA certificate (`PORTUNUS_TLS_USE_CUSTOM_CA=y`) but `access_module/certs/ca_cert.pem` doesn't exist. Either run `task certs:generate -- --ip <SERVER_IP>` or disable custom CA pinning in menuconfig.

**MFRC522 not responding (no card reads)** — Check SPI wiring. The most common issue is swapped MOSI/MISO. Verify pin assignments in menuconfig match your physical wiring. Confirm the MFRC522 module is powered at 3.3V (not 5V). Check the RST pin connection — set `PORTUNUS_MFRC522_RST_PIN` to `-1` in menuconfig if RST is not wired.

**"WiFi not connected yet — continuing startup"** — The module timed out waiting for WiFi but continues in degraded mode. It reconnects in the background with exponential backoff. Check SSID and password in menuconfig. Verify the access point is reachable and on the same network.

**Heartbeats succeed but access requests return "unknown_module"** — The module ID in the firmware (`PORTUNUS_MODULE_ID`) is not registered on the server. In dev mode, set `PORTUNUS_KNOWN_MODULES=door-001` on the server. In production, register via the admin API: `POST /admin/v1/modules`.

**HMAC signature mismatch (server returns 401)** — The `PORTUNUS_HMAC_SECRET` in the firmware Kconfig must exactly match the `PORTUNUS_HMAC_SECRET` environment variable on the server. Regenerate both from the same `openssl rand -hex 32` output. Watch for trailing whitespace or newlines.

**Door strike doesn't actuate** — Check active-high vs active-low configuration (`PORTUNUS_DOOR_STRIKE_ACTIVE_LOW`). Verify the relay/MOSFET is receiving the GPIO signal. Confirm the strike's power supply is connected and that the relay coil voltage matches your supply. The strike energizes for `PORTUNUS_UNLOCK_HOLD_MS` (default 5 seconds) then re-locks.

**Reed switch reports wrong state** — Check the normally-open vs normally-closed setting (`PORTUNUS_REED_SWITCH_NC`). The default assumes normally-open wiring (circuit closes when magnet aligns = door shut). If your readings are inverted, toggle this setting in menuconfig.

**Stack overflow crashes** — If you see `CORRUPT HEAP` or task watchdog resets, a FreeRTOS task may have exceeded its stack. Increase the relevant stack size in Kconfig (e.g., `PORTUNUS_MFRC522_TASK_STACK_SIZE`). The gRPC transport requires a larger `server_comm` stack (10KB vs 6KB for HTTP) — this is handled automatically by the build.