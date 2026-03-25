# Portunus — API Reference

## Overview

The Portunus server exposes two groups of HTTP endpoints:

- **Device API** (`/v1/*`) — used by ESP32 access modules for heartbeats and access requests. Authenticated via HMAC-SHA256 request signing. Accepts both JSON and protobuf (`application/x-protobuf`).
- **Admin API** (`/admin/v1/*`) — used by administrators to manage modules, cards, and doors. Authenticated via Bearer token. JSON only.

---

## Authentication

### Device endpoints (HMAC)

Every `POST` to `/v1/*` must include an `X-Portunus-Sig` header containing `HMAC-SHA256(secret, request_body)` hex-encoded. The server rejects requests with missing or invalid signatures with HTTP 401.

Set the shared secret via `PORTUNUS_HMAC_SECRET` on the server and `CONFIG_PORTUNUS_HMAC_SECRET` in the firmware.

### Admin endpoints (Bearer token)

Every request to `/admin/v1/*` must include an `Authorization: Bearer <key>` header. The key must match the `PORTUNUS_ADMIN_API_KEY` environment variable on the server.

Generate a key with: `openssl rand -hex 32`

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

Card tap event — the module sends the card UID and receives a grant/deny decision.

**Request body:**

```json
{
  "module_id": "door-001",
  "card_id": "04:A3:2B:1C",
  "door_closed": true,
  "requested_at": "2026-03-23T12:00:00Z"
}
```

**Response (200 — known module, card allowed):**

```json
{
  "ok": true,
  "known": true,
  "granted": true,
  "reason": "card_allowed",
  "module_id": "door-001",
  "server_time": "2026-03-23T12:00:00Z"
}
```

**Response (200 — known module, card denied):**

```json
{
  "ok": true,
  "known": true,
  "granted": false,
  "reason": "card_not_allowed",
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
3. If a CardStore is configured → SHA-256 hash the card ID, look up in the `cards` table, check `status = active`.
4. Fallback: check the `PORTUNUS_ALLOWED_CARD_IDS` env-var allowlist (legacy, deprecated).

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

### Cards

Cards are stored as SHA-256 hashes. The admin API accepts the raw card ID for registration but returns and addresses cards by their hash (hex-encoded).

#### POST /admin/v1/cards

Register a new card.

**Request:**

```json
{
  "card_id": "04:A3:2B:1C",
  "tag": "Alice's badge"
}
```

**Response (201):**

```json
{
  "ok": true,
  "card": {
    "card_id_hash": "a1b2c3d4e5f6...64 hex chars...",
    "tag": "Alice's badge",
    "status": "active",
    "created_at": "2026-03-23T12:00:00Z"
  }
}
```

**Response (409):** Card already registered.

#### GET /admin/v1/cards

List all registered cards.

**Response (200):**

```json
{
  "ok": true,
  "cards": [
    {
      "card_id_hash": "a1b2c3d4e5f6...",
      "tag": "Alice's badge",
      "status": "active",
      "created_at": "2026-03-23T12:00:00Z",
      "last_seen_at": "2026-03-23T14:30:00Z"
    }
  ]
}
```

#### PATCH /admin/v1/cards/{card_hash}

Change a card's status. Valid statuses: `active`, `disabled`, `lost`.

**Request:**

```json
{ "status": "disabled" }
```

**Response (200):**

```json
{ "ok": true, "card_id_hash": "a1b2c3d4e5f6...", "status": "disabled" }
```

#### DELETE /admin/v1/cards/{card_hash}

Permanently delete a card.

**Response (200):**

```json
{ "ok": true, "card_id_hash": "a1b2c3d4e5f6...", "deleted": true }
```

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

## Environment Variables

| Variable | Default | Description |
|---|---|---|
| `PORTUNUS_HTTP_ADDR` | `:8080` | Listen address |
| `PORTUNUS_ENV` | `dev` | Environment (`dev` or `prod`) |
| `PORTUNUS_DB_PATH` | `./data/portunus.db` | SQLite database path |
| `PORTUNUS_TLS_CERT_FILE` | (empty) | PEM certificate for HTTPS |
| `PORTUNUS_TLS_KEY_FILE` | (empty) | PEM private key for HTTPS |
| `PORTUNUS_HMAC_SECRET` | (empty) | HMAC-SHA256 shared secret |
| `PORTUNUS_ADMIN_API_KEY` | (empty) | Bearer token for admin API |
| `PORTUNUS_ALLOW_ALL` | `false` | Grant all access (dev only) |
| `PORTUNUS_KNOWN_MODULES` | (empty) | CSV of module IDs for dev seeding |
| `PORTUNUS_ALLOWED_CARD_IDS` | (empty) | CSV of allowed card IDs (legacy) |
| `PORTUNUS_HEARTBEAT_RETENTION_DAYS` | `30` | Heartbeat record retention |
| `PORTUNUS_PRUNE_INTERVAL_HOURS` | `6` | Pruner run interval |

---

## Quick Start (dev mode)

```bash
# Generate an admin API key
export PORTUNUS_ADMIN_API_KEY=$(openssl rand -hex 32)
echo "Admin key: $PORTUNUS_ADMIN_API_KEY"

# Start the server
PORTUNUS_ENV=dev PORTUNUS_ALLOW_ALL=true go run ./cmd/portunus-server

# Register a module
curl -s -X POST http://localhost:8080/admin/v1/modules \
  -H "Authorization: Bearer $PORTUNUS_ADMIN_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"module_id": "door-001", "door_id": "door_main", "display_name": "Workshop"}' | jq .

# Register a card
curl -s -X POST http://localhost:8080/admin/v1/cards \
  -H "Authorization: Bearer $PORTUNUS_ADMIN_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"card_id": "04:A3:2B:1C", "tag": "Alice"}' | jq .

# List registered cards
curl -s http://localhost:8080/admin/v1/cards \
  -H "Authorization: Bearer $PORTUNUS_ADMIN_API_KEY" | jq .

# Simulate an access request (no HMAC in dev)
curl -s -X POST http://localhost:8080/v1/access_request \
  -H "Content-Type: application/json" \
  -d '{"module_id": "door-001", "card_id": "04:A3:2B:1C"}' | jq .
```