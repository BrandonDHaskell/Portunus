# Portunus — Shared Secrets Setup Guide

This document covers how to generate, configure, and manage the two
shared secrets that protect communication between the access module
(ESP32) and the Portunus server:

1. **TLS certificate** — encrypts the channel so traffic cannot be read
   or modified in transit.
2. **HMAC-SHA256 pre-shared key** — authenticates each request body so
   the server can verify the message came from an enrolled access module.

Both mechanisms must be in place for a production deployment. TLS alone
protects the wire; HMAC alone does not prevent eavesdropping. Together
they provide both confidentiality and message authentication.

---

## Prerequisites

The following tools must be available on the machine used to generate
secrets and sign certificates:

```
openssl    >= 1.1.1   (key and certificate generation)
idf.py                (ESP-IDF 5.4.x, for firmware configuration)
```

---

## Part 1 — TLS Certificate

### 1.1 Choose a certificate strategy

| Scenario | Recommended approach |
|---|---|
| Public server with a registered domain | Use a CA-signed cert (e.g. Let's Encrypt via `certbot`) |
| Private / LAN server with no public domain | Generate a self-signed cert and use `PORTUNUS_TLS_SKIP_VERIFY=y` on the device — **development only** |
| Internal CA already in place | Sign a CSR against your internal CA and embed the CA cert in firmware |

For most deployments a self-signed certificate is the starting point.
The steps below cover that path. If you have a domain and use Let's
Encrypt, skip to [1.4](#14-let-s-encrypt-certificates).

---

### 1.2 Generate a self-signed TLS certificate

Run the following on the server host (or any secure workstation):

```bash
# Create a directory to hold server credentials — keep this outside the repo
mkdir -p ~/.portunus/tls
cd ~/.portunus/tls

# Generate a 2048-bit RSA private key
openssl genrsa -out server.key 2048

# Generate a self-signed certificate valid for 825 days
# Adjust -subj to match your deployment (CN can be an IP address or hostname)
openssl req -new -x509 \
    -key server.key \
    -out server.crt \
    -days 825 \
    -subj "/C=US/ST=State/L=City/O=Portunus/CN=192.168.1.100"
```

Verify the output:

```bash
openssl x509 -in server.crt -noout -text | grep -E "Subject:|Not After"
```

**File permissions — important:**

```bash
chmod 600 server.key   # private key: owner read only
chmod 644 server.crt   # certificate: readable
```

The private key (`server.key`) must never be committed to version
control or copied to the firmware build directory.

---

### 1.3 Configure the server to use the certificate

Pass the paths to the certificate and key as environment variables when
starting the server. The server reads both at startup via
`PORTUNUS_TLS_CERT_FILE` and `PORTUNUS_TLS_KEY_FILE`. When both are
set it calls `ListenAndServeTLS`; when either is missing it falls back
to plain HTTP and logs a warning.

```bash
export PORTUNUS_TLS_CERT_FILE=/home/user/.portunus/tls/server.crt
export PORTUNUS_TLS_KEY_FILE=/home/user/.portunus/tls/server.key
export PORTUNUS_HTTP_ADDR=:8443

./portunus-server
```

Expected startup log:

```
portunus-server listening (TLS) on :8443
```

If you see `listening (plain HTTP — not recommended for production)` the
environment variables are not set or the paths are wrong.

---

### 1.4 Let's Encrypt certificates

If your server has a public hostname, use `certbot` to obtain and
auto-renew a free CA-signed certificate:

```bash
# Install certbot (Debian/Ubuntu)
sudo apt install certbot

# Obtain a certificate (replace example.com with your domain)
sudo certbot certonly --standalone -d portunus.example.com

# Certificates are written to:
#   /etc/letsencrypt/live/portunus.example.com/fullchain.pem
#   /etc/letsencrypt/live/portunus.example.com/privkey.pem
```

Then set:

```bash
export PORTUNUS_TLS_CERT_FILE=/etc/letsencrypt/live/portunus.example.com/fullchain.pem
export PORTUNUS_TLS_KEY_FILE=/etc/letsencrypt/live/portunus.example.com/privkey.pem
```

Let's Encrypt certificates are signed by a CA in the Mozilla bundle,
which is already embedded in the ESP32 firmware. No further firmware
changes are needed.

---

### 1.5 Configure the access module for TLS

Open `menuconfig` from the `access_module` directory:

```bash
cd access_module
idf.py menuconfig
```

Navigate to **Portunus Configuration → Security Configuration** and
set the following:

| Option | Value |
|---|---|
| Enable TLS (HTTPS) for server communication | `y` (enabled) |
| HTTPS server port | Match `PORTUNUS_HTTP_ADDR` on the server (default `8443`) |
| Skip TLS certificate verification | `n` for CA-signed certs; `y` only for self-signed dev certs |

Also update **Network Configuration**:

| Option | Value |
|---|---|
| Portunus server host | IP address or hostname in the certificate's CN field |

> **Self-signed certificates and `PORTUNUS_TLS_SKIP_VERIFY`**
>
> When using a self-signed cert, set `PORTUNUS_TLS_SKIP_VERIFY=y` in
> `menuconfig`. This disables certificate chain validation and must
> **never** be used in production — it removes all protection against
> MITM attacks.
>
> The correct production path for a LAN deployment with no public domain
> is to run a minimal internal CA, sign the server cert against it, and
> embed the CA cert in the firmware using
> `CONFIG_MBEDTLS_CUSTOM_CERTIFICATE_BUNDLE`.

---

## Part 2 — HMAC-SHA256 Pre-Shared Key

The HMAC key is a random binary secret shared between the server and
every access module it trusts. The server and the device both hold the
same value. The device signs each request body with it; the server
verifies the signature before processing the message.

### 2.1 Generate the secret

Generate a cryptographically random 32-byte key and encode it as a
64-character hex string:

```bash
openssl rand -hex 32
```

Example output (do not use this value — generate a fresh one):

```
a3f1c8e2d94b7056f210ae3c1b8d4e7f9c2a5b6d0e1f3a8c4b7e2d9f1a0c3b5
```

> **One key per deployment, not per device.** All access modules that
> report to the same server instance share the same key. If a device
> is decommissioned or compromised, rotate the key across all remaining
> devices and the server at the same time (see [Part 3](#part-3----key-rotation)).

---

### 2.2 Configure the server

Set the secret as an environment variable before starting the server:

```bash
export PORTUNUS_HMAC_SECRET=a3f1c8e2d94b7056f210ae3c1b8d4e7f9c2a5b6d0e1f3a8c4b7e2d9f1a0c3b5
```

When `PORTUNUS_HMAC_SECRET` is non-empty, the server enforces HMAC on
every inbound POST. Requests with a missing or invalid `X-Portunus-Sig`
header are rejected with HTTP 401 before any handler logic runs.

Expected startup log line when enforcement is active:

```
portunus-server HMAC request signing enforcement: ENABLED
```

If you see `DISABLED`, the environment variable is not set.

---

### 2.3 Configure the access module firmware

Open `menuconfig` from the `access_module` directory:

```bash
cd access_module
idf.py menuconfig
```

Navigate to **Portunus Configuration → Security Configuration** and
set the following:

| Option | Value |
|---|---|
| Enable HMAC-SHA256 request signing | `y` (enabled) |
| HMAC shared secret (pre-shared key) | Paste the exact hex string generated in step 2.1 |

Save and exit `menuconfig`. The secret is written into `sdkconfig` and
compiled into the firmware binary as `CONFIG_PORTUNUS_HMAC_SECRET`.

Alternatively, set it directly in an `sdkconfig.defaults` overlay so it
applies to every clean build without going through `menuconfig`:

```
# sdkconfig.defaults (do not commit this file if it contains the real secret)
CONFIG_PORTUNUS_HMAC_ENABLED=y
CONFIG_PORTUNUS_HMAC_SECRET="a3f1c8e2d94b7056f210ae3c1b8d4e7f9c2a5b6d0e1f3a8c4b7e2d9f1a0c3b5"
```

> **Security note:** The HMAC secret is stored in flash as part of the
> compiled firmware binary. Treat `.bin` build artifacts as sensitive —
> do not publish them or store them in version control without encryption.

---

### 2.4 Build and flash

```bash
cd access_module
idf.py build
idf.py -p /dev/ttyUSB0 flash
```

On a successful access request, the server log will show a 200 response.
A 401 response means the device's `CONFIG_PORTUNUS_HMAC_SECRET` does not
match the server's `PORTUNUS_HMAC_SECRET` — go back to steps 2.1–2.3
and confirm both values are identical, character for character.

---

## Part 3 — Key Rotation

Rotate the HMAC secret whenever a device is decommissioned, a build
artifact may have been exposed, or as a scheduled security practice.

### 3.1 Generate a new secret

```bash
openssl rand -hex 32
```

### 3.2 Update the server first

```bash
export PORTUNUS_HMAC_SECRET=<new-secret>
# Restart the server process
```

> The server will reject requests from devices still using the old secret
> with HTTP 401 until they are reflashed. Plan the rollout window
> accordingly.

### 3.3 Reflash each access module

For each device:

1. Open `menuconfig` → **Security Configuration** → **HMAC shared secret**
   and paste the new value, or update `sdkconfig.defaults`.
2. Run `idf.py build && idf.py -p /dev/ttyUSBx flash`.
3. Confirm the device resumes sending accepted heartbeats (HTTP 200 in
   server logs).

### 3.4 Rotate the TLS certificate

TLS certificates expire and should be rotated before the `Not After`
date shown in `openssl x509 -in server.crt -noout -text`. For
Let's Encrypt certificates `certbot renew` handles this automatically.
For self-signed certificates, repeat the steps in [1.2](#12-generate-a-self-signed-tls-certificate)
with a new key and certificate and restart the server.

---

## Part 4 — Complete Startup Reference

The full set of environment variables for a production server start:

```bash
# Network
export PORTUNUS_HTTP_ADDR=:8443
export PORTUNUS_ENV=prod

# Database
export PORTUNUS_DB_PATH=/var/lib/portunus/portunus.db

# TLS
export PORTUNUS_TLS_CERT_FILE=/etc/portunus/tls/server.crt
export PORTUNUS_TLS_KEY_FILE=/etc/portunus/tls/server.key

# HMAC request authentication
export PORTUNUS_HMAC_SECRET=<64-char hex string>

# Device policy
export PORTUNUS_KNOWN_MODULES=door-001,door-002
export PORTUNUS_ALLOW_ALL=false
export PORTUNUS_ALLOWED_CARD_IDS=04:A3:2B:1C,04:D7:9E:3A

./portunus-server
```

And the corresponding `sdkconfig.defaults` excerpt for each access
module (values must match the server environment):

```
CONFIG_PORTUNUS_USE_TLS=y
CONFIG_PORTUNUS_TLS_SERVER_PORT=8443
CONFIG_PORTUNUS_TLS_SKIP_VERIFY=n
CONFIG_PORTUNUS_HMAC_ENABLED=y
CONFIG_PORTUNUS_HMAC_SECRET="<same 64-char hex string>"
CONFIG_PORTUNUS_SERVER_HOST="192.168.1.100"
```

---

## Summary

| Step | Action | Where |
|---|---|---|
| 1 | `openssl genrsa` + `openssl req -x509` | Server host |
| 2 | Set `PORTUNUS_TLS_CERT_FILE` + `PORTUNUS_TLS_KEY_FILE` | Server env |
| 3 | `openssl rand -hex 32` | Any secure workstation |
| 4 | Set `PORTUNUS_HMAC_SECRET` | Server env |
| 5 | Set `CONFIG_PORTUNUS_HMAC_SECRET` + TLS options | `menuconfig` or `sdkconfig.defaults` |
| 6 | `idf.py build && idf.py flash` | Build host |
| 7 | Confirm server logs show `ENABLED` and devices get HTTP 200 | Server logs |
