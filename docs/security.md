# Portunus — Security

Current security model, implemented controls, and known limitations for the Portunus snapshot in this repository.

**Last updated:** March 2026

---

## Overview

Portunus is a LAN-first access-control system built around two main pieces:

- an **ESP32 access module** that reads an RFID card, monitors door hardware, and sends requests to the server
- a **Go server** that decides whether access should be granted and records operational state in SQLite

In the current codebase, Portunus uses a layered security model:

1. **TLS** protects traffic in transit when enabled.
2. **HMAC-SHA256 request signing** authenticates device-originated requests when configured.
3. **Module registry checks** ensure only commissioned, enabled, non-revoked modules are treated as known.
4. **Card policy checks** ensure only allowed cards are granted access.
5. **Bearer-token auth** protects the admin API when configured.

A key detail in the current implementation: several controls are **configuration-dependent**. In development, the server can run without TLS, without HMAC enforcement, and without admin API auth. In production-style deployments, those should all be enabled explicitly.

---

## What is implemented today

| Control | Current state | Where it exists |
|---|---|---|
| TLS for HTTP server | Implemented, optional | Go HTTP server via `ListenAndServeTLS()` |
| TLS for gRPC server | Implemented, optional on server | Go gRPC server via `credentials.NewTLS(...)` |
| TLS for firmware HTTP client | Implemented | ESP-IDF / mbedTLS |
| TLS for firmware gRPC client | Implemented and required | `grpc_client` uses `esp-tls` + ALPN `h2` |
| Custom CA pinning in firmware | Implemented | `access_module/certs/ca_cert.pem` embedded into firmware |
| Mozilla CA bundle fallback | Implemented | Firmware TLS configuration |
| Skip-verify dev mode | Implemented | `CONFIG_PORTUNUS_TLS_SKIP_VERIFY` |
| HMAC-SHA256 on device requests | Implemented, optional on server | HTTP middleware and gRPC interceptor |
| Admin Bearer-token auth | Implemented, optional on server | HTTP admin middleware |
| Module commissioning / enabled / revoked checks | Implemented | SQLite-backed `DeviceStore.IsKnown()` |
| SHA-256 hashing of card IDs | Implemented | Card registration, access checks, audit events |
| Request body size limits | Implemented | HTTP API handlers |
| Read-header timeout | Implemented | HTTP server |
| Serialized SQLite writes | Implemented | DB worker |

---

## Threat model

### Intended environment

Portunus is designed for private, local-network deployments such as makerspaces, workshops, small offices, and similar shared physical spaces. The server is expected to live on the same LAN as the access modules, typically on a Raspberry Pi or similar small host.

### Attacker assumptions

The current codebase is aimed at resisting:

- passive network eavesdropping
- casual LAN misuse
- unauthorized module impersonation without the shared secret
- accidental or unauthorized admin API access when the admin key is set
- casual database inspection of stored card data

It is **not** designed to fully resist:

- a determined attacker with physical access to the ESP32 hardware
- a compromised server host
- RFID cloning attacks against UID-only cards
- radio-layer denial of service such as Wi-Fi jamming
- strong anti-replay guarantees at the application layer

---

## Transport security

## HTTP server

The Go HTTP server can run in one of two modes:

- **plain HTTP** when `PORTUNUS_TLS_CERT_FILE` and `PORTUNUS_TLS_KEY_FILE` are not set
- **HTTPS** when both files are set

In other words, TLS is supported but not forced by the server binary itself. The repo currently supports secure and insecure startup depending on deployment configuration.

## gRPC server

The server can also expose an optional gRPC listener when `PORTUNUS_GRPC_ADDR` is set.

- If TLS cert/key files are configured, the gRPC server uses TLS with `MinVersion: TLS 1.2` and advertises `h2`.
- If TLS files are absent, the gRPC server still starts, but without TLS, and logs that this is not recommended for production.

## Firmware TLS modes

The ESP32 firmware supports three certificate-validation modes through Kconfig:

### 1. Custom CA pinning

Recommended for LAN deployments.

- `CONFIG_PORTUNUS_USE_TLS=y`
- `CONFIG_PORTUNUS_TLS_USE_CUSTOM_CA=y`
- `CONFIG_PORTUNUS_TLS_SKIP_VERIFY=n`

In this mode, the firmware validates the server certificate against a CA certificate embedded into the firmware image. The expected CA file path is:

```text
access_module/certs/ca_cert.pem
```

The repo includes `scripts/generate_certs.sh`, which:

- creates a private CA
- creates a server certificate signed by that CA
- copies the CA certificate into `access_module/certs/ca_cert.pem`

### 2. Mozilla CA bundle

Supported for deployments using a publicly trusted certificate chain.

- `CONFIG_PORTUNUS_USE_TLS=y`
- `CONFIG_PORTUNUS_TLS_USE_CUSTOM_CA=n`
- `CONFIG_PORTUNUS_TLS_SKIP_VERIFY=n`

### 3. Skip verification

Development-only mode.

- `CONFIG_PORTUNUS_USE_TLS=y`
- `CONFIG_PORTUNUS_TLS_SKIP_VERIFY=y`

This still encrypts the connection, but it does **not** authenticate the server certificate and therefore does not prevent man-in-the-middle attacks.

## gRPC on firmware requires TLS

The firmware’s gRPC transport is only available when TLS is enabled. In the current Kconfig, `CONFIG_PORTUNUS_USE_GRPC` depends on `CONFIG_PORTUNUS_USE_TLS`, and the custom gRPC client uses `esp-tls` with ALPN `"h2"`.

---

## Request authentication with HMAC-SHA256

Portunus currently uses a single pre-shared secret between the firmware and server for device request authentication.

### HTTP path

For HTTP device requests, the firmware:

1. encodes the protobuf request body
2. computes `HMAC-SHA256(secret, raw_body_bytes)`
3. hex-encodes the result
4. sends it as the `X-Portunus-Sig` header

The server’s HTTP middleware:

1. reads the raw request body bytes
2. computes the expected HMAC
3. compares the supplied and expected values using `hmac.Equal()`
4. rejects invalid or missing signatures with HTTP `401`

Important detail: on the **HTTP path**, the current server verifies the signature against the **raw request body bytes**, not against a re-marshaled message.

### gRPC path

For gRPC device requests, the firmware attaches the signature as lowercase metadata:

```text
x-portunus-sig
```

The server’s gRPC interceptor:

1. reads `x-portunus-sig` from incoming metadata
2. re-marshals the decoded protobuf message with Go’s protobuf library
3. computes the expected HMAC over those protobuf bytes
4. rejects invalid or missing signatures with gRPC `UNAUTHENTICATED`

This is slightly different from the HTTP middleware implementation, but both paths are implemented and both are active in the current codebase.

### Enforcement is configuration-dependent

HMAC is only enforced when the server is started with:

```text
PORTUNUS_HMAC_SECRET
```

If that variable is empty, device requests are accepted without HMAC validation. That is useful for early development, but it is not a secure production posture.

### Current key model

The current implementation uses **one shared HMAC secret across all modules**. That means a leaked secret affects every device that uses it. Per-device secrets are not yet implemented in this snapshot.

---

## Module authorization

A valid HMAC alone does not make a module trusted.

The server also checks whether the module is known. In the SQLite-backed `DeviceStore`, a module is considered known only if it is:

- present in the `modules` table
- `enabled = 1`
- has a non-null `commissioned_at_ms`
- has a null `revoked_at_ms`

This is the current server-side definition of an enrolled module.

Unknown modules are denied access, but the server still updates its last-seen timestamp entry for operational visibility.

---

## Card handling and privacy

Portunus does **not** persist raw RFID card IDs in the database.

### Registration flow

When a card is registered through the admin API, the server:

1. receives the raw `card_id`
2. computes `SHA-256(card_id)`
3. stores the 32-byte hash in the `cards` table
4. returns the hex form of that hash in the API response

### Access decision flow

When an access request arrives, the server:

1. extracts the raw card ID from the request
2. computes `SHA-256(card_id)`
3. checks whether that hash exists and is allowed in the `cards` table

### Audit trail

The current access-event write path also stores the hashed card ID in `access_events.card_id_hash` when a card ID is present.

### Limitation

This is privacy-improving, but it is not equivalent to password hashing. RFID UIDs are small enough that a motivated attacker who obtains the database could attempt offline guessing. For the current Portunus threat model, the bigger practical risk remains card cloning rather than database cryptanalysis.

---

## Admin API security

The admin API is protected separately from the device API.

When `PORTUNUS_ADMIN_API_KEY` is set, `/admin/v1/*` endpoints require:

```text
Authorization: Bearer <key>
```

If `PORTUNUS_ADMIN_API_KEY` is empty, admin routes are not protected by Bearer-token auth. That may be acceptable for isolated local development, but it should not be treated as secure deployment.

Admin routes do **not** use HMAC request signing.

---

## Defensive measures in the current server

The current server includes several security-relevant hardening measures in code:

| Measure | Current behavior |
|---|---|
| Device request size cap | `4096` bytes max via `http.MaxBytesReader` / limited body reads |
| Admin request size cap | `16384` bytes max |
| Header timeout | `ReadHeaderTimeout: 5s` |
| Constant-time HMAC compare | Uses `hmac.Equal()` |
| Unknown JSON field rejection | Admin JSON decoding uses `DisallowUnknownFields()` |
| Serialized writes | SQLite writes funnel through a single worker to reduce lock contention |
| Graceful shutdown | HTTP shutdown + gRPC graceful stop on SIGINT/SIGTERM |

These measures do not replace authentication, but they improve robustness against malformed or abusive requests.

---

## Firmware secret handling and current limitations

The current firmware stores sensitive material in build-time configuration:

- Wi-Fi SSID and password
- HMAC shared secret
- server host and related transport settings

Today, those values are effectively part of the firmware image / device flash configuration. The Kconfig help text already warns that the HMAC secret is stored in flash and that build artifacts should be protected.

### Not currently implemented as an active repo workflow

The following security features are **not** part of the current implemented Portunus workflow in this snapshot:

- ESP32 flash encryption
- ESP32 secure boot
- encrypted NVS-backed secret storage
- OTA signing / OTA verification flow
- per-device HMAC secrets
- application-layer nonce or sequence validation for anti-replay

Some of these are reasonable future hardening steps, but they should not be described as active protections in the current codebase.

---

## Replay protection

Replay protection is only partial in the current implementation.

- TLS protects traffic in transit and makes passive replay much harder in normal operation.
- Heartbeat messages include sequence and uptime fields, and those values are stored.
- However, the server does **not** currently reject requests based on reused nonces, duplicate sequence numbers, or timestamp freshness.

So, at the application layer, explicit anti-replay enforcement is **not implemented yet**.

---

## Known accepted risks in the current project

These are the most important current limitations to be clear about:

### RFID UID cloning

The current access flow is based on card UID-style identification. That is practical for the project’s current phase, but it is not strong cryptographic card authentication.

### Physical access to the ESP32

Because secure boot and flash encryption are not part of the current workflow, someone with enough physical access to the module may be able to extract firmware-stored secrets.

### Server compromise

If the server host is compromised, the attacker controls access decisions and database contents. Portunus assumes the host operating system is administered separately and sensibly.

### Wi-Fi denial of service

Portunus cannot defend against radio-layer jamming or general Wi-Fi disruption.

### Physical bypass of the door hardware

Portunus controls an electronic access path. It does not replace the need for physically robust locks, strike hardware, enclosure design, and door construction.

---

## Strongest currently supported deployment posture

For the current codebase, the strongest practical setup is:

### Server

- set `PORTUNUS_TLS_CERT_FILE` and `PORTUNUS_TLS_KEY_FILE`
- set `PORTUNUS_HMAC_SECRET`
- set `PORTUNUS_ADMIN_API_KEY`
- leave `PORTUNUS_ALLOW_ALL` unset or `false`
- commission modules through the admin API
- register cards in the database instead of relying on legacy env-var allowlists

### Firmware

- enable TLS
- keep `TLS_SKIP_VERIFY` disabled
- use `TLS_USE_CUSTOM_CA` with a generated private CA for LAN deployment
- enable HMAC signing
- ensure `CONFIG_PORTUNUS_HMAC_SECRET` exactly matches the server secret
- treat firmware binaries and config outputs as sensitive artifacts

This is the best match to the project’s current intended security posture.

---

## What this document intentionally does not claim

To stay aligned with the current repository, this document does **not** claim that Portunus already has:

- a finished production overlay workflow for ESP32 hardening
- mandatory TLS in all modes
- mandatory HMAC in all modes
- hardware-backed secret protection on the firmware side
- strong anti-replay at the application layer
- cryptographically strong smart-card authentication

Those may be good next steps, but they are not the current implemented baseline.

---

## Related documentation

- [Architecture](architecture.md)
- [API reference](api.md)
- [Server setup](setup_server.md)
- [Firmware setup](setup_firmware.md)
- [Access module setup and architecture](../access_module/README.md)
- [Shared secrets setup](../access_module/shared_secrets_setup.md)