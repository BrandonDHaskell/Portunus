# Portunus Access Module — TLS and Shared Secret Setup

This guide explains how the current Portunus snapshot secures communication
between the ESP32 access module and the Go server.

For the access module, there are **two active security layers** you can
configure today:

1. **TLS server authentication and transport encryption**
   - The access module can talk to the server over HTTPS.
   - For LAN deployments, the recommended model is a **private CA** whose CA
     certificate is embedded in the firmware and used to validate the
     server certificate.
   - For publicly trusted certificates, the firmware can use the ESP-IDF
     certificate bundle instead.

2. **HMAC-SHA256 request signing**
   - The module signs each outbound protobuf payload with a pre-shared secret.
   - The server verifies the `X-Portunus-Sig` header before accepting the
     request.

These protections apply to the current HTTP/protobuf path and to the optional
**gRPC over HTTP/2 + TLS** path. The transport changes, but the trust material
is the same:

- **TLS** protects the channel.
- **HMAC** authenticates the request body.

## What this document covers

This file focuses only on the security material shared between the firmware and
server:

- generating and installing TLS certificates for the current repo workflow
- embedding a private CA certificate into the firmware for LAN use
- generating and configuring the HMAC shared secret
- current Kconfig settings that control TLS, CA pinning, HMAC, and optional
  gRPC transport

This file does **not** cover:

- secure boot
- flash encryption
- per-device hardware secret storage
- production provisioning infrastructure

Those are important hardening topics, but they are **not implemented as a full
repo workflow in this snapshot**.

---

## 1. Current secure deployment model

For the current project, the recommended secure LAN setup is:

1. Generate a **private CA** and a **server certificate** using the repo script.
2. Start the server with that certificate and key.
3. Embed the generated **CA certificate** into the firmware.
4. Enable **TLS** and **HMAC** in the firmware.
5. Set the same HMAC secret on the server.
6. Build and flash the module.

That results in:

- encrypted traffic
- server certificate validation on the ESP32
- signed request bodies
- compatibility with both:
  - HTTP/1.1 + protobuf device endpoints
  - optional gRPC over HTTP/2 + TLS

---

## 2. TLS setup

### 2.1 Recommended approach for the current repo

For a typical Portunus LAN deployment, use the repo-provided certificate
script:

```bash
task certs:generate -- --ip 192.168.1.100
```

You can also add a local DNS name:

```bash
task certs:generate -- --ip 192.168.1.100 --dns portunus.local
```

This uses `scripts/generate_certs.sh`, which:

- creates a private CA certificate and key under `certs/`
- creates a server certificate and key under `certs/`
- signs the server certificate with the private CA
- copies the CA certificate into the firmware tree at:

```text
access_module/certs/ca_cert.pem
```

If `access_module/certs/` does not exist yet, the script creates it.

### 2.2 Output files

After running the script, the repo contains:

```text
certs/ca.key
certs/ca.pem
certs/server.key
certs/server.csr
certs/server.pem
access_module/certs/ca_cert.pem
```

Meaning:

- `ca.key` — private CA key, keep secret
- `ca.pem` — private CA certificate
- `server.key` — server private key, keep secret
- `server.pem` — server certificate used by the Go server
- `access_module/certs/ca_cert.pem` — firmware copy of the CA certificate

Do **not** commit private keys.

### 2.3 Server environment for TLS

Start the server with the generated certificate and key:

```bash
export PORTUNUS_HTTP_ADDR=:8443
export PORTUNUS_TLS_CERT_FILE="$PWD/certs/server.pem"
export PORTUNUS_TLS_KEY_FILE="$PWD/certs/server.key"
```

Then run the server normally.

If you also want the optional gRPC listener, set a gRPC address too:

```bash
export PORTUNUS_GRPC_ADDR=:50051
```

The current server uses the **same TLS certificate and key** for both:

- HTTPS device/admin traffic
- optional gRPC traffic

### 2.4 Current firmware TLS options

Open firmware configuration:

```bash
cd access_module
idf.py menuconfig
```

Navigate to:

```text
Portunus Configuration
  Security Configuration
```

Relevant current options are:

| Option | Purpose | Recommended value for LAN deployment |
|---|---|---|
| `CONFIG_PORTUNUS_USE_TLS` | Enables HTTPS/TLS | `y` |
| `CONFIG_PORTUNUS_TLS_SERVER_PORT` | HTTPS port | `8443` |
| `CONFIG_PORTUNUS_TLS_SKIP_VERIFY` | Disables certificate validation | `n` |
| `CONFIG_PORTUNUS_TLS_USE_CUSTOM_CA` | Pins to `access_module/certs/ca_cert.pem` | `y` |

For LAN deployments using the repo-generated CA, the correct secure setup is:

- TLS enabled
- skip verify disabled
- custom CA enabled

### 2.5 Public CA certificates

The firmware can also validate a publicly trusted server certificate using the
ESP-IDF certificate bundle.

For that case:

- keep `CONFIG_PORTUNUS_USE_TLS=y`
- set `CONFIG_PORTUNUS_TLS_SKIP_VERIFY=n`
- set `CONFIG_PORTUNUS_TLS_USE_CUSTOM_CA=n`

That tells the firmware to use the built-in CA bundle instead of
`access_module/certs/ca_cert.pem`.

### 2.6 Development-only skip verify mode

`CONFIG_PORTUNUS_TLS_SKIP_VERIFY=y` exists in the current firmware, but it is
strictly a development-only escape hatch.

When enabled, the module does **not** properly validate the server certificate.
That is useful only for temporary testing with mismatched or self-signed certs
that are not pinned correctly.

Do not use this mode for any real deployment.

---

## 3. HMAC shared secret setup

### 3.1 What the HMAC secret does

When HMAC is enabled, the firmware computes:

```text
HMAC-SHA256(secret, raw_protobuf_body)
```

and sends the resulting hex digest in:

```text
X-Portunus-Sig
```

The server verifies the same signature before it processes the message.

In the current codebase:

- the HTTP path signs the raw protobuf request body
- the gRPC path also signs the raw protobuf message body and sends the
  signature as metadata

This means the same HMAC secret works for both transports.

### 3.2 Generate a secret

Generate a 32-byte hex secret:

```bash
openssl rand -hex 32
```

Example format:

```text
9e56d5a0d7a9f4d0d1d57e2a89f65e4db4f5e3324a0c1c8a28d9c4d1c57e8f92
```

Generate your own value. Do not reuse example strings.

### 3.3 Server environment for HMAC

Set the server secret:

```bash
export PORTUNUS_HMAC_SECRET="<your-64-char-hex-secret>"
```

When this environment variable is set, the current server enables HMAC
verification for inbound POSTs and for gRPC requests if gRPC is enabled.

### 3.4 Firmware HMAC options

In `menuconfig`, navigate to:

```text
Portunus Configuration
  Security Configuration
```

Relevant current options are:

| Option | Purpose | Recommended value |
|---|---|---|
| `CONFIG_PORTUNUS_HMAC_ENABLED` | Enables HMAC request signing | `y` |
| `CONFIG_PORTUNUS_HMAC_SECRET` | Shared secret | same value as server |

The secret configured in firmware must exactly match:

```text
PORTUNUS_HMAC_SECRET
```

on the server.

### 3.5 Storage caveat

In the current snapshot, `CONFIG_PORTUNUS_HMAC_SECRET` is compiled into the
firmware image and stored in flash.

That means:

- firmware binaries should be treated as sensitive
- `sdkconfig` should not be committed with real secrets in it
- the current repo does **not** provide a finished secure provisioning flow for
  injecting secrets per device at manufacturing time

---

## 4. Current network and transport settings that must match

Besides TLS and HMAC, the module still needs the correct server location.

Under:

```text
Portunus Configuration
  Network Configuration
```

set:

| Option | Purpose | Example |
|---|---|---|
| `CONFIG_PORTUNUS_SERVER_HOST` | Server IP or hostname | `192.168.1.100` |
| `CONFIG_PORTUNUS_SERVER_PORT` | HTTP port when TLS is disabled | `8080` |
| `CONFIG_PORTUNUS_SERVER_REQUEST_TIMEOUT_MS` | request timeout | `5000` |

If TLS is enabled, the module uses `CONFIG_PORTUNUS_TLS_SERVER_PORT` for the
HTTP/protobuf path.

If gRPC is enabled, also configure under:

```text
Portunus Configuration
  Transport Configuration
```

| Option | Purpose | Example |
|---|---|---|
| `CONFIG_PORTUNUS_USE_GRPC` | Use gRPC instead of HTTP/protobuf | `y` or `n` |
| `CONFIG_PORTUNUS_GRPC_SERVER_PORT` | gRPC server port | `50051` |

Important current behavior:

- `CONFIG_PORTUNUS_USE_GRPC` depends on TLS being enabled
- the server must expose `PORTUNUS_GRPC_ADDR` when gRPC is enabled in the
  firmware
- HTTP and gRPC can coexist on different ports

---

## 5. Current end-to-end secure setup example

### 5.1 Generate certificates

From the repo root:

```bash
task certs:generate -- --ip 192.168.1.100
```

### 5.2 Generate an HMAC secret

```bash
openssl rand -hex 32
```

Save the output somewhere secure.

### 5.3 Start the server

Example secure LAN configuration:

```bash
export PORTUNUS_ENV=prod
export PORTUNUS_DB_PATH=./data/portunus.db

export PORTUNUS_HTTP_ADDR=:8443
export PORTUNUS_TLS_CERT_FILE="$PWD/certs/server.pem"
export PORTUNUS_TLS_KEY_FILE="$PWD/certs/server.key"

export PORTUNUS_HMAC_SECRET="<your-64-char-hex-secret>"

# Optional gRPC listener
export PORTUNUS_GRPC_ADDR=:50051
```

### 5.4 Configure the firmware

In `idf.py menuconfig`, set at minimum:

```text
CONFIG_PORTUNUS_USE_TLS=y
CONFIG_PORTUNUS_TLS_SERVER_PORT=8443
CONFIG_PORTUNUS_TLS_SKIP_VERIFY=n
CONFIG_PORTUNUS_TLS_USE_CUSTOM_CA=y
CONFIG_PORTUNUS_HMAC_ENABLED=y
CONFIG_PORTUNUS_HMAC_SECRET="<same secret as server>"
CONFIG_PORTUNUS_SERVER_HOST="192.168.1.100"
```

If using gRPC, also set:

```text
CONFIG_PORTUNUS_USE_GRPC=y
CONFIG_PORTUNUS_GRPC_SERVER_PORT=50051
```

### 5.5 Build and flash

```bash
cd access_module
idf.py build
idf.py -p /dev/ttyACM0 flash
idf.py monitor
```

---

## 6. Certificate and secret rotation

### 6.1 Rotating the HMAC secret

To rotate the HMAC secret in the current implementation:

1. generate a new secret
2. update `PORTUNUS_HMAC_SECRET` on the server
3. update `CONFIG_PORTUNUS_HMAC_SECRET` in each module
4. rebuild and reflash each device

Until a device is reflashed, it will fail HMAC verification.

### 6.2 Rotating the TLS certificate

For the repo’s private-CA model:

- regenerate the certs with `task certs:generate`
- restart the server with the new `server.pem` and `server.key`
- reflash firmware only if the CA changed and therefore
  `access_module/certs/ca_cert.pem` changed

If only the server leaf certificate changes but it is still signed by the same
CA, the existing firmware CA pin remains valid.

---

## 7. What is and is not true in the current snapshot

### Implemented now

- TLS support in firmware
- custom CA pinning for LAN deployments
- fallback to ESP-IDF certificate bundle for public CAs
- HMAC signing in firmware
- HMAC verification in the Go server
- optional gRPC transport using the same trust material
- certificate generation script in the repo

### Not a finished repo workflow yet

- secure boot enablement workflow
- flash encryption enablement workflow
- device-unique secret provisioning
- hardware-backed key storage on the module
- production sdkconfig overlay files committed in the repo

The `Taskfile` includes `firmware:build:dev` and `firmware:build:prod`, but the
expected `sdkconfig.defaults*` overlay files are not present in this snapshot.
Use `menuconfig` and your local build environment for real security settings.

---

## 8. Practical recommendations

For the current Portunus snapshot, use this baseline:

- `CONFIG_PORTUNUS_USE_TLS=y`
- `CONFIG_PORTUNUS_TLS_SKIP_VERIFY=n`
- `CONFIG_PORTUNUS_TLS_USE_CUSTOM_CA=y` for LAN/private CA deployments
- `CONFIG_PORTUNUS_HMAC_ENABLED=y`
- a fresh 32-byte HMAC secret generated with OpenSSL
- repo-generated CA and server cert for LAN use
- optional gRPC only after confirming `PORTUNUS_GRPC_ADDR` is active on the
  server

That is the security setup that best matches the current code.