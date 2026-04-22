# Portunus — API Reference

## Overview

The Portunus server exposes two groups of HTTP endpoints:

- **Device API** (`/v1/*`) — used by ESP32 access modules for heartbeats and access requests. Authenticated via HMAC-SHA256 request signing. Accepts both JSON and protobuf (`application/x-protobuf`).
- **Admin API** (`/admin/v1/*`) — used by administrators to manage modules, credentials, doors, members, and authorizations. Authenticated via session cookie. JSON only.

An **Admin UI** is also available at `/admin/ui/` for browser-based management.

---

## Authentication

### Device endpoints (HMAC)

Every `POST` to `/v1/*` must include an `X-Portunus-Sig` header containing `HMAC-SHA256(secret, request_body)` hex-encoded. The server rejects requests with missing or invalid signatures with HTTP 401.

Set the shared secret via `PORTUNUS_HMAC_SECRET` on the server and `CONFIG_PORTUNUS_HMAC_SECRET` in the firmware.

### Admin endpoints (session cookie)

All `/admin/v1/*` endpoints (except `POST /admin/v1/login`) require a valid session. Obtain a session by calling `POST /admin/v1/login` with username and password. The server responds with a `Set-Cookie: portunus_session=...` header. Include that cookie in subsequent requests.

Sessions are invalidated by `POST /admin/v1/logout` or when the server removes them on expiry. HMAC request signing is not used for admin endpoints.

---

## Device API

### POST /v1/heartbeat

Periodic health check from an access module.

**Request body:**

```json
{
  "module_id": "door-001",
  "firmware_version": "0.1.0",
  "uptime_s": 3600,
  "free_heap_bytes": 180000,
  "sequence": 42,
  "ip": "192.168.1.50",
  "rssi_dbm": -45
}
```

**Response (200):**

```json
{
  "ok": true,
  "known": true,
  "module_id": "door-001",
  "server_time": "2026-03-23T12:00:00Z"
}
```

`known` is `true` when the module is commissioned, enabled, and not revoked.

### POST /v1/access_request

Credential tap event — the module sends the credential UID and receives a grant/deny decision.

**Request body:**

```json
{
  "module_id": "door-001",
  "credential_id": "04:A3:2B:1C",
  "door_closed": true,
  "requested_at": "2026-03-23T12:00:00Z"
}
```

**Response (200 — known module, access granted):**

```json
{
  "ok": true,
  "known": true,
  "granted": true,
  "reason": "credential_allowed",
  "module_id": "door-001",
  "server_time": "2026-03-23T12:00:00Z"
}
```

**Response (200 — known module, access denied):**

```json
{
  "ok": true,
  "known": true,
  "granted": false,
  "reason": "module_not_authorized",
  "module_id": "door-001",
  "server_time": "2026-03-23T12:00:00Z"
}
```

**Response (403 — unknown module):**

```json
{
  "ok": false,
  "known": false,
  "granted": false,
  "reason": "unknown_module",
  "module_id": "door-999",
  "server_time": "2026-03-23T12:00:00Z"
}
```

**Access decision logic (in priority order):**

1. If the module is not commissioned/enabled → deny with `unknown_module`.
2. If `PORTUNUS_ALLOW_ALL=true` → grant with `allow_all`.
3. Member access + module authorization path (production, when member stores are configured):
   - Hash the credential ID, look up the member in `member_access`.
   - If not found → deny with `credential_not_found`.
   - If member status is not `active` → deny with `member_<status>` (e.g. `member_archived`).
   - If member `enabled=false` → deny with `member_disabled`.
   - Look up an active authorization for this member and module.
   - If none found → deny with `module_not_authorized`.
   - If authorization is revoked → deny with `authorization_revoked`.
   - If authorization is expired → deny with `authorization_expired`.
   - Otherwise → grant with `credential_allowed`.
4. Legacy credential store path (if member stores are not configured): hash the credential ID, look up in the `credentials` table, check `status = active`. Grant with `credential_allowed` or deny with `credential_not_allowed`.
5. Legacy env-var fallback: check `PORTUNUS_ALLOWED_CREDENTIAL_IDS` list.

### POST /v1/provision_credential

Used by PROVISIONING_CONSOLE firmware variant to enroll a new credential on the server. The firmware hashes the credential UID on-device (SHA-256 via mbedTLS) before sending, so raw UIDs never leave the device.

> **Note:** This endpoint is called by PROVISIONING_CONSOLE firmware but is not yet implemented on the server. Provisioning console deployments are blocked until this endpoint is added.

---

## Admin API

All admin endpoints return JSON. Errors follow this format:

```json
{
  "ok": false,
  "error": "error_code",
  "message": "Human-readable description"
}
```

All endpoints require a valid session cookie (obtained via `POST /admin/v1/login`) and sufficient role permissions. A missing or expired session returns HTTP 401. Insufficient permissions return HTTP 403.

### Session management

#### POST /admin/v1/login

Authenticate as an admin user. Returns a session cookie.

**Request:**

```json
{
  "username": "admin",
  "password": "your-password"
}
```

**Response (200):**

```json
{ "ok": true }
```

On success, the server sets `Set-Cookie: portunus_session=<token>; HttpOnly; SameSite=Strict` (with the `Secure` flag when TLS is enabled). Include this cookie in all subsequent admin requests.

**Response (401):** Invalid username or password.

#### POST /admin/v1/logout

Invalidate the current session.

**Response (200):**

```json
{ "ok": true }
```

The `portunus_session` cookie is cleared regardless of whether the session was valid.

#### POST /admin/v1/change-password

Change the authenticated user's password. Requires a valid session; no additional permission is needed.

**Request:**

```json
{
  "current_password": "old-password",
  "new_password": "new-password"
}
```

**Response (200):**

```json
{ "ok": true }
```

**Response (401):** Current password is incorrect.

---

### Modules

#### POST /admin/v1/modules

Commission (register) an access module. If the module row already exists from a prior heartbeat, it is promoted to commissioned status.

**Request:**

```json
{
  "module_id": "door-001",
  "door_id": "door_main",
  "display_name": "Main entrance"
}
```

**Response (201):**

```json
{
  "ok": true,
  "module": {
    "module_id": "door-001",
    "door_id": "door_main",
    "display_name": "Main entrance",
    "enabled": true,
    "commissioned": true,
    "commissioned_at": "2026-03-23T12:00:00Z",
    "created_at": "2026-03-23T12:00:00Z"
  }
}
```

#### GET /admin/v1/modules

List all modules.

**Response (200):**

```json
{
  "ok": true,
  "modules": [
    {
      "module_id": "door-001",
      "door_id": "door_main",
      "display_name": "Main entrance",
      "enabled": true,
      "commissioned": true,
      "commissioned_at": "2026-03-23T12:00:00Z",
      "last_seen_at": "2026-03-23T14:30:00Z",
      "last_ip": "192.168.1.50",
      "last_fw_version": "0.1.0",
      "last_wifi_rssi": -45,
      "created_at": "2026-03-23T12:00:00Z"
    }
  ]
}
```

#### GET /admin/v1/modules/{module_id}

Get details for a single module.

**Response (200):** Same shape as one entry in the list above.

**Response (404):** Module not found.

#### POST /admin/v1/modules/{module_id}/revoke

Revoke a module (sets `enabled=0`, records `revoked_at`). The module will receive `known=false` on subsequent heartbeats and access requests.

**Response (200):**

```json
{ "ok": true, "module_id": "door-001", "status": "revoked" }
```

#### DELETE /admin/v1/modules/{module_id}

Permanently delete a module and its associated heartbeat/access records (via CASCADE).

**Response (200):**

```json
{ "ok": true, "module_id": "door-001", "deleted": true }
```

---

### Credentials

Credentials are stored as SHA-256 hashes. The admin API accepts the raw credential UID for registration but returns and addresses credentials by their hash (hex-encoded).

#### POST /admin/v1/credentials

Register a new credential.

**Request:**

```json
{
  "credential_id": "04:A3:2B:1C",
  "tag": "Alice's badge"
}
```

**Response (201):**

```json
{
  "ok": true,
  "credential": {
    "credential_hash": "a1b2c3d4e5f6...64 hex chars...",
    "tag": "Alice's badge",
    "status": "active",
    "created_at": "2026-03-23T12:00:00Z"
  }
}
```

**Response (409):** Credential already registered.

#### GET /admin/v1/credentials

List all registered credentials.

**Response (200):**

```json
{
  "ok": true,
  "credentials": [
    {
      "credential_hash": "a1b2c3d4e5f6...",
      "tag": "Alice's badge",
      "status": "active",
      "created_at": "2026-03-23T12:00:00Z",
      "last_seen_at": "2026-03-23T14:30:00Z"
    }
  ]
}
```

#### PATCH /admin/v1/credentials/{credential_hash}

Change a credential's status. Valid statuses: `active`, `disabled`, `lost`.

**Request:**

```json
{ "status": "disabled" }
```

**Response (200):**

```json
{ "ok": true, "credential_hash": "a1b2c3d4e5f6...", "status": "disabled" }
```

#### DELETE /admin/v1/credentials/{credential_hash}

Permanently delete a credential.

**Response (200):**

```json
{ "ok": true, "credential_hash": "a1b2c3d4e5f6...", "deleted": true }
```

---

### Doors

#### POST /admin/v1/doors

Register a door. Upserts if the `door_id` already exists.

**Request:**

```json
{
  "door_id": "door_main",
  "name": "Main Entrance",
  "location": "Building A, Floor 1"
}
```

**Response (201):**

```json
{
  "ok": true,
  "door": {
    "door_id": "door_main",
    "name": "Main Entrance",
    "location": "Building A, Floor 1",
    "created_at": "2026-03-23T12:00:00Z"
  }
}
```

#### GET /admin/v1/doors

List all doors.

#### DELETE /admin/v1/doors/{door_id}

Delete a door. Modules referencing this door will have their `door_id` set to NULL (via `ON DELETE SET NULL`).

---

### Members

Members represent physical people enrolled in the access system. A member has a role, a lifecycle status, and optionally a credential (RFID UID hash) and per-module authorizations.

Member records are created in two ways:
- **Admin-provisioned:** `POST /admin/v1/members` creates the member directly.
- **Console-provisioned:** A PROVISIONING_CONSOLE firmware device submits a credential and the server creates a pending member. An admin then reviews and grants authorizations via the pending queue.

#### GET /admin/v1/members

List all members.

**Response (200):**

```json
{
  "ok": true,
  "members": [
    {
      "uuid": "550e8400-e29b-41d4-a716-446655440000",
      "role_id": "member",
      "credential_hash": "a1b2c3d4e5f6ab78…",
      "status": "active",
      "enabled": true,
      "provisioning_status": "active",
      "expires_at": "2027-01-01T00:00:00Z",
      "inactivity_limit_days": 90,
      "last_access_at": "2026-04-20T08:30:00Z",
      "created_at": "2026-03-01T00:00:00Z",
      "created_by_uuid": "admin-uuid"
    }
  ]
}
```

`credential_hash` is an 8-byte (16 hex character) prefix followed by `…` — enough to cross-reference without exposing the full hash.

#### GET /admin/v1/members/pending

List members provisioned via a PROVISIONING_CONSOLE that are awaiting authorization assignment. Ordered oldest-first (FIFO).

**Response (200):** Same shape as `GET /admin/v1/members`.

#### GET /admin/v1/members/{member_uuid}

Get a single member record.

**Response (200):**

```json
{
  "ok": true,
  "member": { "...same fields as list..." }
}
```

**Response (404):** Member not found.

#### POST /admin/v1/members

Provision a new member. Creates the member with `provisioning_status: active`.

**Request:**

```json
{
  "role_id": "member",
  "expires_at": "2027-01-01T00:00:00Z",
  "inactivity_limit_days": 90
}
```

`expires_at` (RFC 3339) and `inactivity_limit_days` are optional. Omit for no hard deadline or inactivity policy.

**Response (201):**

```json
{
  "ok": true,
  "member": {
    "uuid": "550e8400-e29b-41d4-a716-446655440000",
    "role_id": "member",
    "status": "active",
    "enabled": true,
    "provisioning_status": "active",
    "created_at": "2026-04-22T10:00:00Z"
  }
}
```

**Response (400):** `role_id` is required or the specified role does not exist.

#### POST /admin/v1/members/{member_uuid}/credential

Attach a registered credential to a member. The credential must already exist in the credentials table. Supply the full 64-character hex credential hash.

**Request:**

```json
{
  "credential_hash": "a1b2c3d4e5f6...64 hex chars..."
}
```

**Response (200):**

```json
{ "ok": true, "member_uuid": "550e8400-..." }
```

**Response (404):** Member not found.

**Response (409):** The credential is already assigned to another member (active, pending, or inactive — the specific conflict is described in the error code).

#### PUT /admin/v1/members/{member_uuid}/role

Change a member's role.

**Request:**

```json
{ "role_id": "keyholder" }
```

**Response (200):**

```json
{ "ok": true, "member_uuid": "550e8400-...", "role_id": "keyholder" }
```

#### POST /admin/v1/members/{member_uuid}/disable

Disable a member. The member record is retained but `enabled` is set to `false`. Access requests for this member's credential will be denied with `member_disabled`.

**Response (200):**

```json
{ "ok": true, "member_uuid": "550e8400-...", "enabled": false }
```

#### POST /admin/v1/members/{member_uuid}/archive

Archive a member. Sets `status` to `archived` and records `archived_at` and `archived_by_uuid` from the session. Archived members cannot be re-activated — provision a new member if access needs to be restored.

**Response (200):**

```json
{ "ok": true, "member_uuid": "550e8400-...", "status": "archived" }
```

---

### Module Authorizations

A module authorization grants a specific member access to a specific module. A member may be authorized on multiple modules. Authorizations can optionally include an expiry time and a time-restriction policy.

#### GET /admin/v1/modules/{module_id}/authorizations

List all authorizations for a module (active and revoked).

**Response (200):**

```json
{
  "ok": true,
  "authorizations": [
    {
      "authorization_id": 42,
      "member_uuid": "550e8400-...",
      "module_id": "door-001",
      "granted_at": "2026-03-01T00:00:00Z",
      "granted_by_uuid": "admin-uuid",
      "expires_at": "2027-01-01T00:00:00Z",
      "revoked_at": null,
      "time_restriction": null
    }
  ]
}
```

#### POST /admin/v1/modules/{module_id}/authorizations

Grant a member access to a module.

**Request:**

```json
{
  "member_uuid": "550e8400-...",
  "expires_at": "2027-01-01T00:00:00Z",
  "time_restriction": null
}
```

`expires_at` and `time_restriction` are optional. `granted_by_uuid` is filled automatically from the session if omitted.

**Response (201):**

```json
{ "ok": true, "member_uuid": "550e8400-...", "module_id": "door-001" }
```

**Response (409):** An active authorization already exists for this member and module.

#### DELETE /admin/v1/modules/{module_id}/authorizations/{member_uuid}

Revoke a member's access to a module. Records `revoked_at` and `revoked_by_uuid` from the session.

**Response (200):**

```json
{ "ok": true, "member_uuid": "550e8400-...", "module_id": "door-001", "status": "revoked" }
```

**Response (404):** No active authorization found for this member and module.

#### GET /admin/v1/members/{member_uuid}/authorizations

List all module authorizations for a member.

**Response (200):** Same shape as `GET /admin/v1/modules/{module_id}/authorizations`.

---

## Environment Variables

| Variable | Default | Description |
|---|---|---|
| `PORTUNUS_HTTP_ADDR` | `:8080` | Listen address |
| `PORTUNUS_ENV` | `dev` | Environment (`dev` or `prod`) |
| `PORTUNUS_DB_PATH` | `./data/portunus.db` | SQLite database path |
| `PORTUNUS_TLS_CERT_FILE` | (empty) | PEM certificate for HTTPS |
| `PORTUNUS_TLS_KEY_FILE` | (empty) | PEM private key for HTTPS |
| `PORTUNUS_HMAC_SECRET` | (empty) | HMAC-SHA256 shared secret for device request signing |
| `PORTUNUS_CREDENTIAL_HASH_SECRET` | (empty) | Keyed HMAC-SHA256 secret for credential ID hashing. Required in prod. Generate with `openssl rand -hex 32` |
| `PORTUNUS_ALLOW_ALL` | `false` | Grant all access (dev only) |
| `PORTUNUS_KNOWN_MODULES` | (empty) | CSV of module IDs for dev seeding |
| `PORTUNUS_ALLOWED_CREDENTIAL_IDS` | (empty) | CSV of allowed credential UIDs (legacy fallback, deprecated) |
| `PORTUNUS_HEARTBEAT_RETENTION_DAYS` | `30` | Heartbeat record retention |
| `PORTUNUS_PRUNE_INTERVAL_HOURS` | `6` | Pruner run interval |
| `PORTUNUS_EXPIRY_WORKER_INTERVAL_MINUTES` | `60` | How often the member expiry worker runs |
| `PORTUNUS_GRPC_ADDR` | (empty — disabled) | gRPC listen address (e.g. `:50051`) |

---

## Quick Start (dev mode)

```bash
# Start the server
PORTUNUS_ENV=dev PORTUNUS_ALLOW_ALL=true go run ./cmd/portunus-server
```

The server seeds a default `door-001` module and `door_main` door on startup.

```bash
# Log in and save the session cookie
curl -s -c cookies.txt -X POST http://localhost:8080/admin/v1/login \
  -H "Content-Type: application/json" \
  -d '{"username": "admin", "password": "admin"}' | jq .

# Register a module
curl -s -b cookies.txt -X POST http://localhost:8080/admin/v1/modules \
  -H "Content-Type: application/json" \
  -d '{"module_id": "door-001", "door_id": "door_main", "display_name": "Workshop"}' | jq .

# Register a credential
curl -s -b cookies.txt -X POST http://localhost:8080/admin/v1/credentials \
  -H "Content-Type: application/json" \
  -d '{"credential_id": "04:A3:2B:1C", "tag": "Alice"}' | jq .

# List registered credentials
curl -s -b cookies.txt http://localhost:8080/admin/v1/credentials | jq .

# Simulate an access request (no HMAC in dev)
curl -s -X POST http://localhost:8080/v1/access_request \
  -H "Content-Type: application/json" \
  -d '{"module_id": "door-001", "credential_id": "04:A3:2B:1C"}' | jq .
```

For browser-based management, navigate to `http://localhost:8080/admin/ui/`.
