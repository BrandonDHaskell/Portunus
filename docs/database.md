# Portunus — Database

Current-state reference for the Portunus server database: schema, migrations, write behavior, retention, and practical operational notes.

**Last updated:** March 29, 2026

---

## Overview

The Portunus server currently uses **SQLite** as its only persistent datastore. The Go server opens the database through the pure-Go `modernc.org/sqlite` driver, creates the parent directory if needed, applies embedded migrations on startup, and in `dev` mode seeds a starter door plus at least one commissioned module.

Default database path:

- `./data/portunus.db`

The following PRAGMAs are applied through the connection DSN every time the database is opened:

- `foreign_keys=ON`
- `journal_mode=WAL`
- `synchronous=NORMAL`
- `busy_timeout=5000`

The connection pool is intentionally constrained to a **single SQLite connection**:

- `MaxOpenConns=1`
- `MaxIdleConns=1`
- `ConnMaxLifetime=0`

This matches the current server design and reduces lock contention in a single-process deployment.

---

## Current database responsibilities

Today the database is responsible for five main concerns:

1. **Inventory of doors and modules**
2. **Current module snapshot data** such as last seen time, last firmware version, and last RSSI
3. **Append-only heartbeat history**
4. **Registered card storage** using SHA-256 card hashes rather than raw card IDs
5. **Append-only access audit events** for grant and deny decisions

It is also used by the admin API for module, door, and card management.

---

## Schema reference

### `doors`

Represents physical door locations. A door can exist even if no module is assigned to it yet.

| Column | Type | Constraints | Notes |
|---|---|---|---|
| `door_id` | TEXT | PRIMARY KEY | Stable door identifier such as `door_main` |
| `name` | TEXT | NOT NULL | Human-readable door name |
| `location` | TEXT | nullable | Optional freeform location text |
| `created_at_ms` | INTEGER | NOT NULL | Unix epoch milliseconds |
| `updated_at_ms` | INTEGER | NOT NULL | Unix epoch milliseconds |

---

### `modules`

Represents access modules known to the server.

| Column | Type | Constraints | Notes |
|---|---|---|---|
| `module_id` | TEXT | PRIMARY KEY | Must match the device module ID |
| `door_id` | TEXT | FK → `doors(door_id)` ON DELETE SET NULL | Optional door assignment |
| `display_name` | TEXT | nullable | Admin-facing label |
| `enabled` | INTEGER | NOT NULL, default `1`, CHECK 0/1 | Used as part of the “known module” check |
| `commissioned_at_ms` | INTEGER | nullable | Null means not commissioned |
| `revoked_at_ms` | INTEGER | nullable | Null means not revoked |
| `last_seen_at_ms` | INTEGER | nullable | Most recent module activity recorded by the server |
| `last_ip` | TEXT | nullable | Last reported IP string |
| `last_fw_version` | TEXT | nullable | Last reported firmware version |
| `last_wifi_rssi` | INTEGER | nullable | Last reported RSSI |
| `last_strike_unlocked` | INTEGER | nullable, CHECK 0/1 | **Present in schema but not currently populated by the active write path** |
| `created_at_ms` | INTEGER | NOT NULL | Unix epoch milliseconds |
| `updated_at_ms` | INTEGER | NOT NULL | Unix epoch milliseconds |

#### Current module lifecycle behavior

The current code uses two module creation paths:

- **Admin commissioning path**: `AdminService.RegisterModule()` calls `CommissionModule()`, which inserts or promotes a module to `enabled=1` and sets `commissioned_at_ms`.
- **Auto-create path for unknown modules**: heartbeat and device “seen” updates call `ensureModule()`, which inserts a minimal row with `enabled=0` and no `commissioned_at_ms` if the module does not already exist.

This means unknown modules are still represented in the database so that heartbeat rows and access audit rows can satisfy foreign key constraints.

#### Current “known module” rule

`DeviceStore.IsKnown()` currently treats a module as known only when all of the following are true:

- `enabled = 1`
- `commissioned_at_ms IS NOT NULL`
- `revoked_at_ms IS NULL`

That is the rule used by the device registry and therefore the access decision path.

---

### `module_heartbeats`

Append-only heartbeat history from access modules.

| Column | Type | Constraints | Notes |
|---|---|---|---|
| `heartbeat_id` | INTEGER | PRIMARY KEY AUTOINCREMENT | Row ID |
| `module_id` | TEXT | NOT NULL, FK → `modules(module_id)` ON DELETE CASCADE | Reporting module |
| `received_at_ms` | INTEGER | NOT NULL | Server receive time |
| `seq` | INTEGER | nullable | Device sequence number when provided |
| `uptime_ms` | INTEGER | nullable | Stored in milliseconds; derived from firmware `uptime_s` |
| `fw_version` | TEXT | nullable | Firmware version string |
| `wifi_rssi` | INTEGER | nullable | Reported RSSI |
| `strike_unlocked` | INTEGER | nullable, CHECK 0/1 | **Present in schema but not currently written by `HeartbeatStore.UpsertHeartbeat()`** |
| `ip` | TEXT | nullable | Reported IP string |
| `free_heap_bytes` | INTEGER | nullable | Added in migration `0003` |

#### What the current heartbeat path actually stores

`HeartbeatStore.UpsertHeartbeat()` currently writes:

- `module_id`
- `received_at_ms`
- `seq` when non-zero
- `uptime_ms` when non-zero
- `fw_version`
- `wifi_rssi`
- `ip`
- `free_heap_bytes` when non-zero

It also updates the module snapshot in `modules` with:

- `last_seen_at_ms`
- `last_ip`
- `last_fw_version`
- `last_wifi_rssi`
- `updated_at_ms`

#### Important current limitation

The current heartbeat request type includes fields such as `DoorClosed`, and the schema includes strike-related columns, but the active SQLite heartbeat write path **does not persist door state or strike state today**.

So the schema is slightly ahead of the data actually being written.

---

### `cards`

Stores registered RFID cards as SHA-256 hashes rather than raw card IDs.

| Column | Type | Constraints | Notes |
|---|---|---|---|
| `card_id_hash` | BLOB | PRIMARY KEY, CHECK length = 32 | SHA-256 hash bytes |
| `card_tag` | TEXT | nullable | Human-readable label |
| `status` | TEXT | NOT NULL, default `active`, CHECK in `active`, `disabled`, `lost` | Current card state |
| `created_at_ms` | INTEGER | NOT NULL | Registration time |
| `updated_at_ms` | INTEGER | NOT NULL | Last modification time |
| `last_seen_at_ms` | INTEGER | nullable | Updated when an active card is successfully checked |

#### Current card behavior

- Admin card registration hashes the supplied card ID with SHA-256 before insertion.
- `IsCardAllowed()` looks up the hash and returns `true` only when `status = 'active'`.
- When an active card is checked, the current implementation updates `last_seen_at_ms` and `updated_at_ms` as a side effect.

#### Current status semantics

- `active`: eligible to grant access
- `disabled`: present but denied
- `lost`: present but denied

---

### `access_events`

Append-only audit log of access decisions.

| Column | Type | Constraints | Notes |
|---|---|---|---|
| `access_event_id` | INTEGER | PRIMARY KEY AUTOINCREMENT | Row ID |
| `module_id` | TEXT | NOT NULL, FK → `modules(module_id)` ON DELETE CASCADE | Reporting module |
| `door_id` | TEXT | FK → `doors(door_id)` ON DELETE SET NULL | Resolved from the module’s current door assignment at write time |
| `received_at_ms` | INTEGER | NOT NULL | Server-side event time |
| `requested_at_ms` | INTEGER | nullable | Optional device-reported timestamp when parseable |
| `door_closed` | INTEGER | nullable, CHECK 0/1 | Door state from the access request when provided |
| `card_id_hash` | BLOB | nullable, FK → `cards(card_id_hash)` ON DELETE SET NULL, CHECK null or length=32 | SHA-256 of the card ID used in the request |
| `decision_granted` | INTEGER | NOT NULL, CHECK 0/1 | Grant/deny flag |
| `decision_reason` | TEXT | NOT NULL | Reason string from the decision path |
| `policy_version` | INTEGER | nullable | Present in schema but not currently written by `AccessEventStore` |
| `decided_at_ms` | INTEGER | NOT NULL | Server-side decision time |

#### What the current access path records

`AccessService.recordEvent()` currently records:

- `module_id`
- `received_at_ms`
- `requested_at_ms` when the device timestamp parses successfully
- `door_closed` when provided
- SHA-256 `card_id_hash`
- `decision_granted`
- `decision_reason`
- `decided_at_ms`

`AccessEventStore.RecordEvent()` resolves `door_id` from the module’s current assignment in the `modules` table at insert time.

#### Current decision reasons seen in code

Examples of reason strings currently emitted by the service layer include:

- `unknown_module`
- `allow_all`
- `card_allowed`
- `card_not_allowed`
- `card_lookup_error`

---

### `schema_migrations`

Tracks applied schema migrations.

| Column | Type | Constraints | Notes |
|---|---|---|---|
| `version` | INTEGER | PRIMARY KEY | Migration version |
| `applied_at_ms` | INTEGER | NOT NULL | Application time in Unix epoch milliseconds |

This table is created by `db.Migrate()` before versioned migrations are applied.

---

## Foreign key behavior

Current foreign key behavior in the schema:

- Deleting a `door` sets referencing `modules.door_id` to `NULL`
- Deleting a `door` sets referencing `access_events.door_id` to `NULL`
- Deleting a `module` cascades to `module_heartbeats`
- Deleting a `module` cascades to `access_events`
- Deleting a `card` sets `access_events.card_id_hash` to `NULL`

This preserves audit history where practical while still allowing entities to be removed.

---

## Indexes

The following indexes are created in migration `0002_indexes.sql`.

| Index | Table | Columns | Current purpose |
|---|---|---|---|
| `idx_modules_last_seen` | `modules` | `last_seen_at_ms` | Find recently active or stale modules |
| `idx_heartbeats_module_time` | `module_heartbeats` | `module_id, received_at_ms` | Per-module heartbeat history |
| `idx_heartbeats_time` | `module_heartbeats` | `received_at_ms` | Heartbeat retention pruning |
| `idx_cards_last_seen` | `cards` | `last_seen_at_ms` | Card usage lookups |
| `idx_access_module_time` | `access_events` | `module_id, received_at_ms` | Per-module audit history |
| `idx_access_card_time` | `access_events` | `card_id_hash, received_at_ms` | Per-card audit history |
| `idx_access_time` | `access_events` | `received_at_ms` | Time-range reporting |

---

## Migration system

Migrations live in:

- `server/internal/db/migrations/`

They are embedded into the server binary with Go `embed` and applied at startup by `db.Migrate()`.

Current migration naming convention:

- `NNNN_description.sql`

Current flow:

1. Ensure `schema_migrations` exists
2. Read embedded `.sql` migration files
3. Parse version from filename
4. Sort by version
5. Skip versions already recorded in `schema_migrations`
6. Apply each pending migration inside a transaction
7. Insert the applied version into `schema_migrations`

If a migration fails, the transaction is rolled back and server startup fails.

### Current migrations

| Version | File | Purpose |
|---|---|---|
| `0001` | `0001_init.sql` | Base schema |
| `0002` | `0002_indexes.sql` | Query and pruning indexes |
| `0003` | `0003_add_free_heap.sql` | Adds `free_heap_bytes` to `module_heartbeats` |

### Current compatibility note

The migration system is **forward-only** in practice. The current migration set only adds schema and does not implement rollback scripts.

---

## Write behavior

### Serialized write worker

Most runtime database writes are intentionally funneled through `db.Worker`, which executes transaction closures sequentially on a buffered job channel.

This is the current pattern used by:

- `HeartbeatStore.UpsertHeartbeat()`
- `HeartbeatStore.PruneOlderThan()`
- `AccessEventStore.RecordEvent()`
- `ModuleAdminStore` write methods
- `CardStore.RegisterCard()`
- `CardStore.SetCardStatus()`
- `CardStore.DeleteCard()`
- `DeviceStore.MarkSeen()`

This design reduces SQLite write contention and keeps multi-step writes grouped inside explicit transactions.

### Important current exceptions

Not every write in the codebase uses the worker.

1. **`CardStore.IsCardAllowed()` updates `cards.last_seen_at_ms` directly via `s.db.ExecContext()`** when the card is active.
2. **`db.SeedDev()` writes directly** during startup seeding in dev mode before normal request handling begins.
3. Reads query the shared `*sql.DB` directly.

So the most accurate statement for the current codebase is:

> Portunus serializes most runtime writes through a single worker, but there are a small number of direct write-side effects that bypass the worker today.

### Current worker semantics

`Worker.Do()` enqueues a job and waits for the result, but it uses the caller’s context when beginning and executing the transaction. That means context cancellation can still cause the queued or running write to fail.

The code comments describe the intent clearly, but the exact behavior is determined by the same context passed into `BeginTx()` and the transaction work itself.

---

## Dev seeding behavior

When `PORTUNUS_ENV=dev`, the server calls `db.SeedDev()` on startup.

Current dev seeding does the following:

- Ensures a starter door exists:
  - `door_id = 'door_main'`
  - `name = 'Main Door'`
  - `location = 'Dev'`
- Creates at least one commissioned module assigned to `door_main`
- If `PORTUNUS_KNOWN_MODULES` is empty, it seeds a default module:
  - `door-001`

This is useful for local development and smoke testing, but it should not be treated as production provisioning behavior.

---

## Heartbeat pruning

Heartbeat retention is implemented by `HeartbeatPruner`, which periodically deletes old rows from `module_heartbeats`.

Current environment variables:

| Variable | Default | Meaning |
|---|---|---|
| `PORTUNUS_HEARTBEAT_RETENTION_DAYS` | `30` | Number of days of heartbeat history to keep; `0` disables pruning |
| `PORTUNUS_PRUNE_INTERVAL_HOURS` | `6` | How often the pruner runs |

Current behavior:

- Runs an immediate prune on startup
- Runs on a repeating timer after that
- Deletes rows where `received_at_ms` is older than the computed cutoff
- Logs how many rows were deleted when the count is non-zero

Only heartbeat history is pruned today. `access_events` are retained indefinitely by the current implementation.

---

## Timestamp conventions

All timestamps are stored as **Unix epoch milliseconds** in SQLite `INTEGER` columns.

This is the current convention across the schema and matches the Go server’s store layer.

Important details from the current implementation:

- `received_at_ms` values are always generated by the server
- `requested_at_ms` in `access_events` is optional and only stored when the device timestamp parses as RFC3339 or RFC3339Nano
- `decided_at_ms` is generated by the server
- For access events, `received_at_ms` and `decided_at_ms` are currently usually the same moment because `AccessService.recordEvent()` uses the server decision time for both
- For heartbeat rows, the firmware’s `uptime_s` is converted to `uptime_ms`

---

## Current backup guidance

SQLite WAL mode creates companion files next to the main database file:

- `portunus.db-wal`
- `portunus.db-shm`

Do not delete these while the server is running.

### Offline backup

```bash
cp ./data/portunus.db ./data/portunus-backup.db
```

### Online backup with `sqlite3`

```bash
sqlite3 ./data/portunus.db ".backup './data/portunus-backup.db'"
```

### Restore

1. Stop the server
2. Replace the database file with the backup copy
3. Restart the server

On startup, the server will apply any pending embedded migrations.

---

## Useful queries

These queries are intended for read-only inspection with `sqlite3`.

### Module status overview

```sql
SELECT
  module_id,
  display_name,
  CASE
    WHEN enabled = 1 AND commissioned_at_ms IS NOT NULL AND revoked_at_ms IS NULL
      THEN 'known'
    ELSE 'unknown'
  END AS status,
  datetime(last_seen_at_ms / 1000, 'unixepoch') AS last_seen,
  last_ip,
  last_fw_version,
  last_wifi_rssi
FROM modules
ORDER BY last_seen_at_ms DESC;
```

### Recent access events

```sql
SELECT
  datetime(received_at_ms / 1000, 'unixepoch') AS time,
  module_id,
  CASE decision_granted WHEN 1 THEN 'GRANTED' ELSE 'DENIED' END AS decision,
  decision_reason,
  hex(card_id_hash) AS card_hash,
  door_id
FROM access_events
ORDER BY received_at_ms DESC
LIMIT 50;
```

### Access events for a specific module

```sql
SELECT
  datetime(received_at_ms / 1000, 'unixepoch') AS time,
  CASE decision_granted WHEN 1 THEN 'GRANTED' ELSE 'DENIED' END AS decision,
  decision_reason,
  hex(card_id_hash) AS card_hash
FROM access_events
WHERE module_id = 'door-001'
ORDER BY received_at_ms DESC;
```

### Access events for a specific card hash

```sql
SELECT
  datetime(received_at_ms / 1000, 'unixepoch') AS time,
  module_id,
  CASE decision_granted WHEN 1 THEN 'GRANTED' ELSE 'DENIED' END AS decision,
  decision_reason
FROM access_events
WHERE card_id_hash = X'<64-char-hex-hash>'
ORDER BY received_at_ms DESC;
```

### Registered cards

```sql
SELECT
  hex(card_id_hash) AS card_hash,
  card_tag,
  status,
  datetime(created_at_ms / 1000, 'unixepoch') AS created_at,
  datetime(last_seen_at_ms / 1000, 'unixepoch') AS last_seen
FROM cards
ORDER BY created_at_ms DESC;
```

### Heartbeat history for one module

```sql
SELECT
  datetime(received_at_ms / 1000, 'unixepoch') AS time,
  seq,
  uptime_ms / 1000 AS uptime_seconds,
  fw_version,
  wifi_rssi,
  ip,
  free_heap_bytes
FROM module_heartbeats
WHERE module_id = 'door-001'
ORDER BY received_at_ms DESC
LIMIT 20;
```

### Modules not seen recently

```sql
SELECT
  module_id,
  display_name,
  datetime(last_seen_at_ms / 1000, 'unixepoch') AS last_seen,
  (strftime('%s', 'now') * 1000 - last_seen_at_ms) / 1000 AS seconds_ago
FROM modules
WHERE last_seen_at_ms IS NOT NULL
  AND last_seen_at_ms < (strftime('%s', 'now') * 1000 - 300000)
ORDER BY last_seen_at_ms ASC;
```

### Row counts by table

```sql
SELECT 'doors' AS table_name, COUNT(*) AS rows FROM doors
UNION ALL SELECT 'modules', COUNT(*) FROM modules
UNION ALL SELECT 'module_heartbeats', COUNT(*) FROM module_heartbeats
UNION ALL SELECT 'cards', COUNT(*) FROM cards
UNION ALL SELECT 'access_events', COUNT(*) FROM access_events
UNION ALL SELECT 'schema_migrations', COUNT(*) FROM schema_migrations;
```

---

## File location notes

By default, the current server uses:

- `./data/portunus.db`

That path can be overridden with:

- `PORTUNUS_DB_PATH`

The server creates the parent directory automatically if it does not exist.