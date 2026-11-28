# Portunus Access Module — TLS and Shared Secret Setup

**Last updated:** April 2026

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

There is also a third secret that lives entirely on the server:

3. **`PORTUNUS_CREDENTIAL_HASH_SECRET`** (server-only — not shared with firmware)
   - Credentials are stored as `HMAC-SHA256(secret, credential_id)` rather than bare SHA-256.
   - This prevents offline dictionary attacks against a stolen database.
   - Required in `prod` mode. Generate with `openssl rand -hex 32`.
   - The PROVISIONING_CONSOLE firmware variant hashes credentials on-device with bare SHA-256 before transmitting; the server re-hashes with its HMAC key on receipt. Neither raw credential IDs nor on-device hashes are stored.

## What this document covers

This file focuses on the security material shared between the firmware and
server, plus the server-side credential hash secret:

- generating and installing TLS certificates for the current repo workflow
- embedding a private CA certificate into the firmware for LAN use
- generating and configuring the HMAC shared secret
- generating and configuring the credential hash secret (server-only)
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

The TLS options are Kconfig, under **Portunus Configuration > Security Configuration**, and the prod profile sets them for you:

| Option | Purpose | Prod profile |
|---|---|---|
| `CONFIG_PORTUNUS_USE_TLS` | Enables TLS | `y` |
| `CONFIG_PORTUNUS_TLS_SKIP_VERIFY` | Disables certificate validation | `n` |
| `CONFIG_PORTUNUS_TLS_USE_CUSTOM_CA` | Pins to `access_module/certs/ca_cert.pem` | `y` |

Build with `task firmware:build:prod`: it applies `sdkconfig.defaults.prod` and then fails if the compiled image has TLS off, skip-verify on, or no pinned CA. The server address and port are not Kconfig options; they are NVS keys (section 4).

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

### 3.6 Credential hash secret (server-only)

The credential hash secret is separate from the HMAC secret and requires no firmware configuration. It controls how credential IDs are hashed before storage:

```bash
openssl rand -hex 32   # generate
export PORTUNUS_CREDENTIAL_HASH_SECRET="<your-64-char-hex-secret>"
```

This variable is required in `prod` mode — the server will refuse to start without it. In `dev` mode it is optional; when absent, credential IDs are still hashed but without a key (bare SHA-256).

Keep this value secret and consistent: changing it invalidates all stored credential hashes and requires re-registration of every credential.

### 3.4 Firmware HMAC options

`CONFIG_PORTUNUS_HMAC_ENABLED` (Kconfig, set to `y` by the prod profile) turns signing on. The secret itself is **not** a Kconfig option: it is the `hmac_secret` key in the module's NVS namespace, written from `access_module/nvs_config.csv` with `task firmware:nvs:gen` and `task firmware:nvs:flash` (see [Firmware setup: Device configuration and secrets](setup_firmware.md#device-configuration-and-secrets-nvs)).

It must exactly match `PORTUNUS_HMAC_SECRET` on the server. To check, compare SHA-256 fingerprints of the two values; never print the secret.

### 3.5 Storage caveat

The secret is stored in plain text in the module's NVS partition, and in `nvs_config.csv` and `build/nvs.bin` on the dev machine.

That means:

- `nvs_config.csv` and `build/nvs.bin` are secrets: gitignored, never shared, never printed or hex-dumped
- firmware images contain no secret, but anyone with physical access to a module can read its NVS unless flash encryption is enabled
- the repo does **not** provide a finished per-device provisioning flow; every module shares one HMAC secret

## 4. Current network and transport settings that must match

Current firmware always talks gRPC over TLS; there is no transport option. Where to connect comes from NVS, not Kconfig:

| NVS key | Purpose | Example |
|---|---|---|
| `server_host` | Server IP or hostname; must be covered by the server cert's SAN | `192.168.1.100` |
| `grpc_port` | Server gRPC port; must match `PORTUNUS_GRPC_ADDR` | `50051` |
| `module_id` | Must match the module registered on the server | `front-door` |

Request timeouts and reconnect backoff are Kconfig, under **Network Configuration**. The server must expose `PORTUNUS_GRPC_ADDR`; it serves gRPC and the HTTPS admin API on different ports with the same certificate.

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
export PORTUNUS_CREDENTIAL_HASH_SECRET="<your-64-char-hex-secret>"

# Optional gRPC listener
export PORTUNUS_GRPC_ADDR=:50051
```

### 5.4 Configure the module

```bash
cp access_module/nvs_config.example.csv access_module/nvs_config.csv
```

Fill in `module_id`, `wifi_ssid`, `wifi_psk`, `server_host` (`192.168.1.100`), `grpc_port` (`50051`) and `hmac_secret` (the server's secret). Set hardware options (variant, pins) with `task firmware:menuconfig` if needed; security options come from the prod profile.

### 5.5 Build and flash

```bash
task firmware:build:prod     # fails if the image is not deployable
task firmware:nvs:gen        # nvs_config.csv -> build/nvs.bin
task firmware:nvs:flash
task firmware:flash-monitor
```

---

## 6. Certificate and secret rotation

### 6.1 Rotating the HMAC secret

1. generate a new secret
2. update `PORTUNUS_HMAC_SECRET` on the server and restart it
3. update `hmac_secret` in each module's `nvs_config.csv`, then `task firmware:nvs:gen` and `task firmware:nvs:flash` (no app rebuild needed)

Every module whose NVS has not been rewritten fails HMAC verification from the moment the server restarts, and shows up as a device auth failure on the admin health page.

### 6.2 Renewing or reissuing the TLS certificate

For the repo's private-CA model, keep the CA and reissue only the server certificate:

```bash
task certs:generate -- --sign-only --ip 192.168.1.100
```

Install the new `server.pem` and `server.key` on the server and restart it. The modules' CA pin stays valid, so no reflash is needed.

Rotating the CA itself (`--new-ca`) changes `access_module/certs/ca_cert.pem`: every module must then be rebuilt and reflashed, and modules still pinning the old CA cannot connect until they are. `task certs:generate` refuses to replace an existing CA without that flag.

---

## 7. What is and is not true in the current snapshot

### Implemented now

- TLS support in firmware
- custom CA pinning for LAN deployments
- fallback to ESP-IDF certificate bundle for public CAs
- HMAC signing in firmware and verification in the Go server
- keyed HMAC-SHA256 credential hashing on the server (`PORTUNUS_CREDENTIAL_HASH_SECRET`)
- on-device SHA-256 hashing of credentials in PROVISIONING_CONSOLE firmware before transmission
- gRPC transport (the only one current firmware uses) with the same trust material
- certificate generation script that guards the CA (`--sign-only` / `--new-ca`)
- device identity and HMAC secret in NVS, not in the firmware image
- `sdkconfig.defaults`, `sdkconfig.defaults.dev`, `sdkconfig.defaults.prod`, and `sdkconfig.defaults.ci` overlay files committed in the repo

### Not a finished repo workflow yet

- secure boot enablement workflow
- flash encryption enablement workflow
- device-unique secret provisioning
- hardware-backed key storage on the module

---

## 8. Practical recommendations

For the current Portunus snapshot, use this baseline:

Firmware:

- `CONFIG_PORTUNUS_USE_TLS=y`
- `CONFIG_PORTUNUS_TLS_SKIP_VERIFY=n`
- `CONFIG_PORTUNUS_TLS_USE_CUSTOM_CA=y` for LAN/private CA deployments
- `CONFIG_PORTUNUS_HMAC_ENABLED=y`
- a fresh 32-byte HMAC secret generated with OpenSSL
- repo-generated CA and server cert for LAN use
- `PORTUNUS_GRPC_ADDR` active on the server (the firmware is gRPC-only)

Server:

- `PORTUNUS_HMAC_SECRET` — must exactly match the firmware HMAC secret
- `PORTUNUS_CREDENTIAL_HASH_SECRET` — server-only; generate independently with `openssl rand -hex 32`
- `PORTUNUS_TLS_CERT_FILE` + `PORTUNUS_TLS_KEY_FILE` — use the certs generated by `task certs:generate`

That is the security setup that best matches the current code.