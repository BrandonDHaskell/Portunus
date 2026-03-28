# Portunus — Security

Threat model, defense layers, known limitations, and operational security procedures.

**Last updated:** March 2026

---

## Threat model

### Target environment

Portunus is designed for makerspaces, workshops, small offices, and home labs. The system operates entirely on a private local network with no cloud connectivity or internet-facing endpoints. The server is physically accessible to the administrator (e.g., a Raspberry Pi in a network closet or under a desk).

### Adversary profile

The security model assumes a casual adversary with low-to-moderate capability — someone who might try to clone an RFID card, sniff WiFi traffic, or plug a laptop into the LAN. It does not attempt to defend against a well-resourced attacker with physical access to the ESP32 hardware, the server, or the ability to perform extended side-channel analysis.

### What Portunus protects against

| Threat | Mitigation | How |
|---|---|---|
| Network eavesdropping | TLS encryption | All module ↔ server traffic is encrypted using mbedTLS on the ESP32 and Go's `crypto/tls` on the server |
| Man-in-the-middle | Certificate pinning | The firmware validates the server certificate against an embedded CA cert (LAN deployments) or the Mozilla CA bundle (public certs) |
| Unauthorized device impersonation | HMAC-SHA256 request signing | Every request is signed with a pre-shared key; the server rejects unsigned or mis-signed requests |
| Card UID exposure from database breach | SHA-256 hashing | Raw card UIDs are never stored; the database contains only one-way hashes |
| Unauthorized admin access | Bearer token auth | Admin API requires a secret key in the Authorization header |
| Request body tampering | HMAC over body bytes | The HMAC covers the full protobuf-encoded request body; modifying any byte invalidates the signature |
| Oversized request DoS | Request body limits | Device endpoints capped at 4 KB, admin endpoints at 16 KB, with `ReadHeaderTimeout` of 5 seconds |
| Timing-based HMAC bypass | Constant-time comparison | `hmac.Equal()` (Go) prevents timing side-channels on signature verification |

### What Portunus does NOT protect against

| Threat | Current status | Notes |
|---|---|---|
| Physical access to ESP32 flash | Accepted risk (v1) | Firmware secrets (HMAC key, WiFi password) are stored in plaintext flash. Flash encryption is planned but not enabled. |
| RFID card cloning (UID-only auth) | Accepted risk (v1) | MFRC522 authenticates by UID only, which is trivially cloneable. Acceptable for the makerspace threat model. Migration to MIFARE key-based auth or more secure readers is a future phase. |
| Denial of service (WiFi jamming) | Out of scope | A jammer can prevent all module ↔ server communication. Physical security of the wireless environment is outside Portunus's control. |
| Server compromise | Out of scope | If an attacker gains root on the Pi, they control all access decisions. Standard server hardening (firewalls, SSH key-only, unattended-upgrades) is the owner's responsibility. |
| Physical bypass of door hardware | Out of scope | A door strike can be defeated with physical force. Portunus controls the electronic lock; it does not replace physical security. |
| Replay attacks | Partially mitigated | TLS prevents replay at the transport layer. Application-level replay prevention (nonces, sequence validation) is not implemented — the same signed request could theoretically be replayed if TLS were somehow stripped. |

---

## Defense layers

The system uses four independent defense mechanisms. Each addresses a different class of threat, and they compose — compromising one does not defeat the others.

```
┌──────────────────────────────────────────────────────────────┐
│  Layer 1: TLS Encryption                                     │
│  Prevents eavesdropping and tampering on the wire            │
│                                                              │
│  ┌──────────────────────────────────────────────────────────┐│
│  │  Layer 2: HMAC-SHA256 Request Signing                    ││
│  │  Proves the message came from an enrolled device         ││
│  │                                                          ││
│  │  ┌──────────────────────────────────────────────────────┐││
│  │  │  Layer 3: Device Registry (server-side)              │││
│  │  │  Only commissioned, enabled, non-revoked modules     │││
│  │  │  receive access grants                               │││
│  │  │                                                      │││
│  │  │  ┌──────────────────────────────────────────────────┐│││
│  │  │  │  Layer 4: Card Policy (server-side)              ││││
│  │  │  │  Only registered, active cards are granted       ││││
│  │  │  │  access — all decisions are audited              ││││
│  │  │  └──────────────────────────────────────────────────┘│││
│  │  └──────────────────────────────────────────────────────┘││
│  └──────────────────────────────────────────────────────────┘│
│                                                              │
│  Admin API: separate Bearer token auth (not HMAC)            │
└──────────────────────────────────────────────────────────────┘
```

An attacker would need to break TLS (to see the traffic), forge the HMAC (to impersonate a device), use a registered module ID (to pass the device registry), and present a registered card UID (to get access granted). No single vulnerability in one layer grants access.

---

## TLS implementation details

### Server side (Go)

The server uses Go's standard `crypto/tls` package via `http.Server.ListenAndServeTLS()`. The gRPC listener uses `grpc.Creds(credentials.NewTLS(...))` with `MinVersion: tls.VersionTLS12`. Both listeners share the same certificate and private key.

TLS is enabled when both `PORTUNUS_TLS_CERT_FILE` and `PORTUNUS_TLS_KEY_FILE` are set. When either is missing, the server falls back to plain HTTP and logs a warning.

### Firmware side (ESP32)

The ESP32 firmware uses ESP-IDF's mbedTLS stack. Three certificate validation modes are available, selected via Kconfig:

**Custom CA pinning (`PORTUNUS_TLS_USE_CUSTOM_CA=y`, recommended for LAN)** — The CA certificate PEM is embedded in the firmware binary via `EMBED_TXTFILES` in `CMakeLists.txt`. At TLS handshake time, mbedTLS validates the server's certificate chain against this embedded CA. If the server presents a cert not signed by this CA, the connection is refused. This is the strongest option for LAN deployments because it limits trust to a single private CA.

**Mozilla CA bundle (`PORTUNUS_TLS_USE_CUSTOM_CA=n`)** — The firmware validates against the Mozilla CA bundle shipped with ESP-IDF. Suitable when the server has a publicly-trusted certificate (e.g. Let's Encrypt).

**Skip verification (`PORTUNUS_TLS_SKIP_VERIFY=y`)** — Disables all certificate validation. The connection is encrypted but vulnerable to man-in-the-middle. Intended only for development. The firmware logs a warning: `TLS cert verification DISABLED (dev mode)`.

### Certificate generation

The `scripts/generate_certs.sh` script creates a private CA (10-year validity) and a server certificate (825-day validity, Apple's maximum) with the server's IP address as a Subject Alternative Name. The CA cert is copied into `access_module/certs/ca_cert.pem` for firmware embedding. See [Server Setup — TLS certificate setup](setup_server.md#tls-certificate-setup) for the full procedure.

---

## HMAC implementation details

### How it works

1. The ESP32 encodes the request body using Nanopb (protobuf → bytes).
2. It computes `HMAC-SHA256(pre_shared_key, body_bytes)` using mbedTLS.
3. The hex-encoded result (64 characters) is attached as the `X-Portunus-Sig` header (HTTP) or `x-portunus-sig` metadata (gRPC).
4. The server receives the request, parses the protobuf body, and re-marshals it to bytes.
5. It computes the expected HMAC using the same pre-shared key.
6. It compares the received and expected signatures using `hmac.Equal()` (constant-time).
7. If they don't match, the request is rejected with HTTP 401 / gRPC `UNAUTHENTICATED`.

### Why HMAC over the body, not a session token

A session token would prove the device authenticated at some point; HMAC over each request body proves that *this specific message* was produced by a device holding the key. If a message is intercepted and modified in transit (hypothetically, if TLS were compromised), the HMAC still catches the tampering. This is defense in depth — TLS should prevent modification, but HMAC provides a second check.

### Why re-marshal on the server

The Go server does not verify the HMAC against the raw HTTP request bytes. Instead, it parses the protobuf message, then re-marshals it with `proto.Marshal()` to get canonical bytes. This is necessary because Nanopb (C) and the Go protobuf library may produce slightly different byte orderings for the same logical message (field ordering is canonical in proto3, but optional-field presence encoding can differ). In practice, proto3 serialization is deterministic for the same field values, so the re-marshaled bytes match the original.

### Scope

HMAC is enforced on `POST` requests to device endpoints (`/v1/*`) only. Admin endpoints (`/admin/v1/*`) use Bearer token auth instead, since they originate from curl or browser, not from ESP32 firmware. `GET` requests are not HMAC-signed (there are currently no `GET` device endpoints).

---

## Card ID protection

Raw RFID card UIDs are never stored on the server. The protection flow:

**Registration** (admin API): `POST /admin/v1/cards` with `{"card_id": "04:A3:2B:1C"}` → the server computes `SHA-256("04:A3:2B:1C")` → stores the 32-byte hash in the `cards` table → returns the hex-encoded hash to the admin. The raw card ID is discarded after hashing; it exists only in the HTTP request body during processing.

**Access check** (device request): The ESP32 sends the card UID in the `AccessRequest` protobuf. The server computes `SHA-256(card_id)` and looks up the hash in the `cards` table. If found and the card status is `active`, access is granted.

**Implication**: A database breach exposes SHA-256 hashes, not raw UIDs. However, RFID UIDs are short (4–10 bytes) and the space of valid UIDs is small, so an attacker with the hash could potentially brute-force the UID offline. SHA-256 hashing provides a meaningful barrier against casual exposure but is not equivalent to password-grade hashing (bcrypt/argon2). For the target threat model, this is an acceptable tradeoff — the primary risk is physical card cloning, not database theft.

---

## Server hardening measures

The server applies several defensive measures beyond the authentication layers:

| Measure | Implementation | Purpose |
|---|---|---|
| Request body size limits | `maxRequestBody = 4096` (device), `maxAdminBody = 16384` (admin) | Prevents memory exhaustion from oversized payloads |
| Read header timeout | `ReadHeaderTimeout: 5 * time.Second` | Prevents slowloris-style connection exhaustion |
| JSON field validation | `dec.DisallowUnknownFields()` | Rejects requests with unexpected fields (catches typos and injection attempts) |
| Constant-time HMAC comparison | `hmac.Equal()` | Prevents timing side-channels on signature verification |
| Serialized DB writes | Single-goroutine write worker | Prevents SQLite lock contention under concurrent requests |
| Graceful shutdown | `signal.NotifyContext` + `Shutdown(5s timeout)` | Completes in-flight requests before stopping |
| systemd hardening | `NoNewPrivileges`, `ProtectSystem=strict`, `ProtectHome`, `PrivateTmp` | Limits process capabilities on the production Pi |

---

## Firmware security posture

### Current state (v1)

| Feature | Status | Notes |
|---|---|---|
| TLS encryption | Implemented | Channel encryption for all server communication |
| Certificate pinning | Implemented | Embedded CA cert validates server identity |
| HMAC request signing | Implemented | Per-request authentication with pre-shared key |
| Flash encryption | Not enabled | ESP32-S3 supports it natively; planned for production phase |
| Secure boot | Not enabled | ESP32-S3 supports it natively; planned for production phase |
| NVS encryption | Not implemented | Secrets stored in plaintext Kconfig/flash |
| OTA signature verification | Not implemented | OTA updates are a planned future feature |

### Flash encryption (planned)

ESP32-S3 supports AES-256 flash encryption in hardware. When enabled, the flash contents (including firmware binary and NVS partition) are encrypted at rest. This protects against physical extraction of secrets via JTAG or flash dump. Flash encryption is one-time-programmable on ESP32 — once enabled, it cannot be disabled. The production sdkconfig overlay (`sdkconfig.defaults.prod`) will enforce this.

### Secure boot (planned)

ESP32-S3 supports RSA-based secure boot. When enabled, the bootloader verifies a digital signature on the firmware image before executing it. This prevents unauthorized firmware from running on the device (e.g., modified firmware that bypasses access checks or exfiltrates the HMAC key). Like flash encryption, secure boot involves eFuse programming and must be tested carefully before production deployment.

### Secret storage (current limitation)

The HMAC pre-shared key and WiFi credentials are currently stored in the firmware binary via Kconfig. This means anyone with physical access to the ESP32 can dump the flash and extract these secrets (absent flash encryption). For the makerspace threat model, this is accepted — the devices are installed in semi-trusted environments. A future enhancement will move secrets to an encrypted NVS partition, separating identity material from the firmware image and enabling per-device secret rotation without reflashing the entire firmware.

---

## Credential rotation

### HMAC secret rotation

When to rotate: a device is decommissioned, a firmware binary may have been exposed, or on a scheduled basis (e.g., annually).

**Procedure:**

1. Generate a new secret: `openssl rand -hex 32`
2. Update the server: set `PORTUNUS_HMAC_SECRET` to the new value and restart
3. Reflash each access module with the new `CONFIG_PORTUNUS_HMAC_SECRET` in Kconfig
4. Verify heartbeats are accepted (HTTP 200 in server logs)

The server rejects all device requests between step 2 and step 3 — plan the rollout window for a time when doors can be temporarily non-functional, or rotate during a maintenance window.

The current architecture uses a single HMAC key shared across all modules. A compromised key requires reflashing every device. A future enhancement could use per-device keys (provisioned during commissioning) to limit the blast radius.

### TLS certificate rotation

When to rotate: before the certificate's `Not After` date, or when the server's IP address changes.

**Procedure:**

1. Re-run `task certs:generate -- --ip <SERVER_IP>` to create new CA and server certs
2. Copy the new server cert and key to the production server
3. Restart the server
4. Rebuild and reflash the firmware (the CA cert is embedded in the binary)

If only the server certificate is rotated (signed by the same CA), firmware reflashing is not needed — the embedded CA cert still validates the new server cert. Reflashing is only required when the CA itself is rotated.

### Admin API key rotation

1. Generate a new key: `openssl rand -hex 32`
2. Update `PORTUNUS_ADMIN_API_KEY` on the server and restart
3. Update any scripts or tools that call the admin API

No firmware change is needed — access modules never call the admin API.

---

## Production hardening checklist

Before deploying Portunus in a production environment, verify each item:

**Server:**

- [ ] `PORTUNUS_ENV=prod` (disables dev seeding)
- [ ] `PORTUNUS_TLS_CERT_FILE` and `PORTUNUS_TLS_KEY_FILE` set (TLS enabled)
- [ ] `PORTUNUS_HMAC_SECRET` set (HMAC enforcement enabled)
- [ ] `PORTUNUS_ADMIN_API_KEY` set (admin API protected)
- [ ] `PORTUNUS_ALLOW_ALL` is `false` or unset
- [ ] Server running as systemd service under dedicated `portunus` user (not root)
- [ ] Database file owned by `portunus` user with appropriate permissions
- [ ] Server cert private key has `0600` permissions
- [ ] Environment file (`portunus.env`) has `0600` permissions
- [ ] Firewall allows only the necessary ports (e.g., 8443, 50051)
- [ ] SSH access to the Pi uses key-based auth (no password login)

**Firmware:**

- [ ] `PORTUNUS_TLS_SKIP_VERIFY=n` (cert verification enabled)
- [ ] `PORTUNUS_TLS_USE_CUSTOM_CA=y` with valid CA cert embedded (LAN deployments)
- [ ] `PORTUNUS_HMAC_ENABLED=y` with correct secret
- [ ] `PORTUNUS_HMAC_SECRET` matches the server's `PORTUNUS_HMAC_SECRET` exactly
- [ ] WiFi credentials are correct for the production network
- [ ] Module ID (`PORTUNUS_MODULE_ID`) is unique per device and registered on the server
- [ ] Firmware built with prod overlay (`task firmware:build:prod`)
- [ ] Build artifacts (`.bin` files) treated as sensitive (contain HMAC key)

**Network:**

- [ ] Server and modules on the same LAN (or routable subnet)
- [ ] No device endpoints exposed to the internet
- [ ] WiFi network uses WPA2 or WPA3

**Operational:**

- [ ] All cards registered via admin API (not relying on `PORTUNUS_ALLOWED_CARD_IDS`)
- [ ] Modules commissioned via admin API
- [ ] Database backup schedule in place
- [ ] TLS certificate expiry date noted and renewal planned
- [ ] HMAC secret rotation schedule defined

---

## Related documentation

- [Architecture — Security model](architecture.md#security-model): mechanism overview and design rationale
- [Shared secrets setup guide](../access_module/shared_secrets_setup.md): step-by-step TLS and HMAC provisioning
- [Server setup — TLS certificate setup](setup_server.md#tls-certificate-setup): cert generation and deployment
- [Firmware setup — Security Configuration](setup_firmware.md#security-configuration): Kconfig security settings reference
- [API reference — Authentication](api.md#authentication): HMAC and Bearer token request formats