# Portunus — Firmware Setup (Access Module)

Build, configure, flash, and test the ESP32-S3 firmware for the Portunus access module.

---

## What the firmware does today

The current access module firmware runs on an ESP32-S3 and provides:

- MFRC522 RFID credential reading over SPI
- door strike control over GPIO
- reed switch door-state sensing over GPIO
- single-LED status feedback
- WiFi station connectivity
- periodic heartbeat publishing
- access-request submission to the Portunus server
- two transport modes:
  - **HTTP/1.1 + protobuf** (default)
  - **gRPC over HTTP/2 + TLS** (optional)
- TLS support
- HMAC-SHA256 request signing
- a central `SystemFSM` that coordinates credential flow, access decisions, unlock timing, door-state handling, and feedback

The firmware is built with **ESP-IDF** and uses **Kconfig** for Portunus-specific settings.

---

## Current project layout

The firmware lives under `access_module/`:

```text
access_module/
├── CMakeLists.txt
├── README.md
├── partitions.csv
├── main/
│   ├── CMakeLists.txt
│   ├── Kconfig.projbuild
│   └── main.cpp
├── components/
│   ├── portunus_config/
│   ├── portunus_interfaces/
│   ├── portunus_proto/
│   └── portunus_types/
├── core/
│   └── system_fsm/
├── drivers/
│   ├── access_point_gpio/
│   ├── feedback_led/
│   └── reader_mfrc522/
├── services/
│   ├── event_bus/
│   ├── grpc_client/
│   ├── heartbeat_service/
│   ├── server_comm/
│   └── wifi_mgr/
└── scripts/
    ├── clean.py
    └── proto_gen.py
```

`main/main.cpp` is the composition root. It initializes the platform, constructs the enabled drivers and services, injects them into `SystemFSM`, and starts the runtime.

---

## Prerequisites

### Software

You need:

- **ESP-IDF 5.x** with ESP32-S3 support
- **Python 3**
- **CMake** and **Ninja**
- **Task** if you want to use the repo task wrappers
- **OpenSSL** if you want to generate a private CA and server certs for TLS pinning
- **protobuf tooling + Nanopb** only if you change the `.proto` contract

On Debian/Ubuntu, a typical ESP-IDF host setup looks like:

```bash
sudo apt update
sudo apt install -y git wget flex bison gperf python3 python3-pip \
  python3-venv cmake ninja-build ccache libffi-dev libssl-dev \
  dfu-util libusb-1.0-0
```

### Hardware

For full end-to-end firmware testing, the currently supported hardware is:

- ESP32-S3 development board
- MFRC522 RFID reader
- door strike driven through an appropriate relay or MOSFET stage
- reed switch
- single status LED with resistor
- USB cable for flash + serial monitor

For partial bench testing, you can disable individual hardware features in `menuconfig`.

---

## Install ESP-IDF

Install ESP-IDF using Espressif’s normal process. Example:

```bash
mkdir -p ~/esp
cd ~/esp
git clone --recursive https://github.com/espressif/esp-idf.git
cd esp-idf
./install.sh esp32s3
. ./export.sh
```

Verify:

```bash
idf.py --version
```

You must source the ESP-IDF environment in each shell before building unless you have wrapped it in your shell profile.

---

## Clone the repo and build the default firmware

```bash
git clone https://github.com/BrandonDHaskell/Portunus.git
cd Portunus
. ~/esp/esp-idf/export.sh

task firmware:build
```

That task runs `idf.py build` from `access_module/`.

You can also build directly without Task:

```bash
cd access_module
idf.py build
```

---

## Configuration workflow

The implemented configuration path is **ESP-IDF menuconfig**.

Open it with:

```bash
task firmware:menuconfig
```

or:

```bash
cd access_module
idf.py menuconfig
```

All Portunus-specific settings are defined in:

```text
access_module/main/Kconfig.projbuild
```

### Important current-state note about dev/prod overlay builds

The root `Taskfile.yml` defines these commands:

- `task firmware:build:dev`
- `task firmware:build:prod`

Those commands expect `sdkconfig.defaults`, `sdkconfig.defaults.dev`, and `sdkconfig.defaults.prod` to exist under `access_module/`.

Those files are **not present in this repository snapshot**.

So, in the current state of the repo, the reliable configuration workflow is:

1. use `menuconfig`
2. save the generated `sdkconfig`
3. build with `task firmware:build` or `idf.py build`

Do not rely on the dev/prod overlay tasks unless you add those overlay files yourself.

---

## Portunus configuration menus currently implemented

The current Kconfig menus are:

- **Feature Toggles**
- **Security Configuration**
- **Transport Configuration**
- **Network Configuration**
- **Task Configuration**
- **SPI Pin Assignments (MFRC522)**
- **Door Hardware Pin Assignments**
- **Door Configuration**
- **Timing Configuration**
- **Event Bus Configuration**

### Feature Toggles

These currently control which firmware subsystems are enabled:

- `PORTUNUS_ENABLE_MFRC522`
- `PORTUNUS_ENABLE_HEARTBEAT`
- `PORTUNUS_ENABLE_WIFI`
- `PORTUNUS_ENABLE_DOOR_STRIKE`
- `PORTUNUS_ENABLE_REED_SWITCH`
- `PORTUNUS_ENABLE_LED`

This matters at build time because `main/CMakeLists.txt` conditionally pulls in the matching drivers and services based on these toggles.

### Security Configuration

The current security-related options include:

- `PORTUNUS_USE_TLS`
- `PORTUNUS_TLS_SERVER_PORT`
- `PORTUNUS_TLS_SKIP_VERIFY`
- `PORTUNUS_TLS_USE_CUSTOM_CA`
- `PORTUNUS_HMAC_ENABLED`
- `PORTUNUS_HMAC_SECRET`

Current behavior:

- TLS is supported today.
- You can pin to a custom CA cert embedded in the firmware.
- You can fall back to the ESP-IDF Mozilla CA bundle when not using a custom CA.
- HMAC signing is implemented today and must match the server-side shared secret.

### Transport Configuration

The transport options currently implemented are:

- `PORTUNUS_USE_GRPC`
- `PORTUNUS_GRPC_SERVER_PORT`

Current behavior:

- when `PORTUNUS_USE_GRPC=n`, the firmware uses the HTTP path in `services/server_comm/`
- when `PORTUNUS_USE_GRPC=y`, the firmware uses the `grpc_client` path
- gRPC currently depends on TLS being enabled

### Network Configuration

The key network settings currently exposed are:

- `PORTUNUS_MODULE_ID`
- `PORTUNUS_WIFI_SSID`
- `PORTUNUS_WIFI_PASSWORD`
- `PORTUNUS_WIFI_CONNECT_TIMEOUT_MS`
- `PORTUNUS_WIFI_RECONNECT_INTERVAL_MS`
- `PORTUNUS_SERVER_HOST`
- `PORTUNUS_SERVER_PORT`
- `PORTUNUS_SERVER_REQUEST_TIMEOUT_MS`

### Hardware and timing configuration

The current Kconfig file also exposes:

- MFRC522 SPI pin selection
- door strike pin + active-low option
- reed switch pin + normally-closed option
- LED pin
- unlock hold duration
- reed debounce duration
- FSM poll interval
- heartbeat interval
- RFID poll interval
- card re-read delay
- event bus timeout/depth/subscriber limits

---

## Default wiring values currently reflected in Kconfig

These are the current default GPIO assignments from `Kconfig.projbuild`.

### MFRC522

| MFRC522 Pin | Default ESP32-S3 GPIO |
|---|---:|
| MOSI | 37 |
| MISO | 38 |
| SCLK | 36 |
| SDA / CS | 35 |
| RST | 4 |

### Door hardware and LED

| Function | Default ESP32-S3 GPIO |
|---|---:|
| Door strike | 5 |
| Reed switch | 6 |
| Status LED | 7 |

The reed switch defaults to an internal-pullup style configuration, and the strike defaults to active-high unless changed in `menuconfig`.

---

## TLS certificate setup

The current repo supports LAN TLS pinning with a private CA.

Generate certificates from the repo root:

```bash
task certs:generate -- --ip 192.168.1.100
```

That script:

- creates `certs/ca.pem`
- creates `certs/server.pem` and `certs/server.key`
- copies the CA certificate into:

```text
access_module/certs/ca_cert.pem
```

When `PORTUNUS_TLS_USE_CUSTOM_CA=y`, `services/server_comm/CMakeLists.txt` embeds that PEM into the firmware image at build time. If the file is missing, the build fails with a clear error.

### Current TLS modes

The implemented TLS modes are:

1. **Custom CA pinning**
   - recommended for private LAN deployments
   - requires `access_module/certs/ca_cert.pem`

2. **Public CA bundle**
   - uses the ESP-IDF certificate bundle
   - appropriate when the server uses a publicly trusted certificate

3. **Skip verify**
   - implemented for development only
   - insecure
   - should not be used outside of temporary local testing

---

## HMAC shared secret setup

The firmware can sign outgoing requests with HMAC-SHA256.

Generate a shared secret:

```bash
openssl rand -hex 32
```

Set that same value in:

- firmware `menuconfig` under `PORTUNUS_HMAC_SECRET`
- server environment as `PORTUNUS_HMAC_SECRET`

Current-state caveat: the shared secret is stored in the firmware image / flash. The repo does **not** currently implement encrypted secret storage on the device.

---

## Flashing and monitoring

From the repo root:

```bash
task firmware:flash
task firmware:monitor
task firmware:flash-monitor
```

Or directly:

```bash
cd access_module
idf.py flash
idf.py monitor
idf.py flash monitor
```

If needed, specify the serial port explicitly:

```bash
idf.py -p /dev/ttyACM0 flash monitor
```

On Debian-based systems, make sure your user has serial-port access:

```bash
sudo usermod -a -G dialout $USER
```

Then log out and back in.

---

## First-boot expectations

On a healthy build with WiFi enabled, the normal sequence is:

1. NVS initializes
2. WiFi starts
3. the event bus initializes
4. enabled drivers are constructed
5. `SystemFSM` initializes and starts
6. heartbeat service starts if enabled
7. server communication starts if WiFi is enabled

At runtime:

- credential reads publish events
- `server_comm` forwards heartbeat and access messages to the server
- the server response turns into `EVENT_ACCESS_GRANTED` or `EVENT_ACCESS_DENIED`
- `SystemFSM` unlocks or denies locally and drives the LED feedback path

---

## Current transport behavior

### Default path: HTTP/1.1 + protobuf

This is the current default mode.

`server_comm`:

- subscribes to heartbeat and credential events
- encodes protobuf messages with Nanopb
- posts them to the server
- decodes the protobuf response
- republishes access decision events back to the event bus

### Optional path: gRPC over HTTP/2 + TLS

This path is implemented and selected through Kconfig.

Use it only when the server is also configured with its gRPC listener.

The firmware still uses the same underlying protobuf message contract; what changes is the transport layer.

---

## Protobuf regeneration

You only need protobuf tooling if you change the shared `.proto` file.

Current task commands:

```bash
task proto:gen
task proto:gen:go
task proto:gen:nanopb
```

The repo includes:

- a root generator script: `scripts/proto_gen.py`
- a firmware-local helper: `access_module/scripts/proto_gen.py`

For day-to-day firmware building, you do **not** need to regenerate protobuf files unless the contract changed.

---

## What is not fully represented as a finished firmware workflow yet

To keep this guide aligned with the current repo state, the following should be treated as **not yet fully wired into a committed firmware setup flow**:

- committed `sdkconfig.defaults` overlay files for dev/prod builds
- a completed secure-boot workflow in the firmware setup process
- a completed flash-encryption workflow in the firmware setup process
- encrypted on-device storage for HMAC secrets

Those may exist as design intent elsewhere, but they are not part of the current, ready-to-run firmware setup path in this snapshot.

---

## Recommended current workflow

For the current repo state, the most accurate firmware workflow is:

1. install and source ESP-IDF
2. run `task firmware:menuconfig`
3. set WiFi, module ID, server host, TLS, and HMAC options
4. if using private-CA TLS, run `task certs:generate -- --ip <SERVER_IP>`
5. build with `task firmware:build`
6. flash with `task firmware:flash-monitor`
7. verify that the server is reachable and that heartbeat + access requests succeed

---

## Related docs

- `access_module/README.md` — firmware architecture and runtime overview
- `docs/api.md` — current server endpoints and request/response behavior
- `docs/security.md` — current security model and limitations
- `proto/README.md` — shared protobuf contract used by the firmware and server