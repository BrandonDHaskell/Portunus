# Portunus — Database

SQLite schema, migration system, write model, and operational reference for the Portunus server.

**Last updated:** March 2026

---

## Overview

The Portunus server uses SQLite as its sole data store, accessed through a pure-Go driver (`modernc.org/sqlite`) that requires no CGo or system SQLite library. The database file lives at the path specified by `PORTUNUS_DB_PATH` (default `./data/portunus.db`). The server creates the file, parent directory, and all tables automatically on first startup.

Connection pragmas applied on every open: `foreign_keys=ON`, `journal_mode=WAL`, `synchronous=NORMAL`, `busy_timeout=5000`. The connection pool is fixed at one connection (`MaxOpenConns=1`, `MaxIdleConns=1`) for SQLite safety.

---

## Schema reference

### doors

Physical door locations. A door can have zero or more modules installed at it.

| Column | Type | Constraints | Description |
|---|---|---|---|
| `door_id` | TEXT | PRIMARY KEY | Unique identifier (e.g. `door_main`, `workshop_east`) |
| `name` | TEXT | NOT NULL | Human-readable name |
| `location` | TEXT | | Optional location description |
| `created_at_ms` | INTEGER | NOT NULL | Unix epoch milliseconds |
| `updated_at_ms` | INTEGER | NOT NULL | Unix epoch milliseconds |

### modules

Registered access module devices. Each module is optionally assigned to a door.

| Column | Type | Constraints | Description |
|---|---|---|---|
| `module_id` | TEXT | PRIMARY KEY | Unique identifier matching the firmware's `PORTUNUS_MODULE_ID` |
| `door_id` | TEXT | FK → doors, ON DELETE SET NULL | Door this module is installed at (nullable) |
| `display_name` | TEXT | | Human-readable label |
| `enabled` | INTEGER | NOT NULL, CHECK (0 or 1) | Whether the module is active |
| `commissioned_at_ms` | INTEGER | | When the module was commissioned (nullable = not yet commissioned) |
| `revoked_at_ms` | INTEGER | | When the module was revoked (nullable = not revoked) |
| `last_seen_at_ms` | INTEGER | | Timestamp of most recent heartbeat or access request |
| `last_ip` | TEXT | | Last reported IP address |
| `last_fw_version` | TEXT | | Last reported firmware version string |
| `last_wifi_rssi` | INTEGER | | Last reported WiFi RSSI in dBm |
| `last_strike_unlocked` | INTEGER | CHECK (0 or 1) | Last reported strike state |
| `created_at_ms` | INTEGER | NOT NULL | Unix epoch milliseconds |
| `updated_at_ms` | INTEGER | NOT NULL | Unix epoch milliseconds |

**Module lifecycle:** A module row can be created in two ways. The admin API (`POST /admin/v1/modules`) creates a commissioned, enabled module. Alternatively, when an unknown module sends a heartbeat or access request, `ensureModule()` auto-creates a row with `enabled=0` and no `commissioned_at_ms` — this satisfies foreign key constraints while keeping the module in an "unknown" state. Only an admin action (or dev seeding) promotes it to commissioned.

**"Known" predicate:** A module is considered "known" by the device registry when `enabled=1` AND `commissioned_at_ms` is not null AND `revoked_at_ms` is null. Unknown modules have their heartbeats and access attempts recorded, but access is always denied.

### module_heartbeats

Append-only telemetry log. Each row records one heartbeat from a module. Subject to retention-based pruning.

| Column | Type | Constraints | Description |
|---|---|---|---|
| `heartbeat_id` | INTEGER | PRIMARY KEY AUTOINCREMENT | Row ID |
| `module_id` | TEXT | NOT NULL, FK → modules, ON DELETE CASCADE | Reporting module |
| `received_at_ms` | INTEGER | NOT NULL | Server-side receive timestamp |
| `seq` | INTEGER | | Module's monotonic heartbeat counter (resets on reboot) |
| `uptime_ms` | INTEGER | | Module uptime in milliseconds |
| `fw_version` | TEXT | | Firmware version string |
| `wifi_rssi` | INTEGER | | WiFi signal strength in dBm |
| `strike_unlocked` | INTEGER | CHECK (0 or 1) | Door strike state at time of heartbeat |
| `ip` | TEXT | | Module's IP address |
| `free_heap_bytes` | INTEGER | | Free heap memory in bytes (added in migration 0003) |

When a heartbeat is recorded, the `modules` row is also updated with `last_seen_at_ms`, `last_ip`, `last_fw_version`, and `last_wifi_rssi` as a snapshot for fast status queries without scanning the heartbeat table.

### cards

RFID card registrations. Card IDs are stored as SHA-256 hashes — the raw card UID is never persisted.

| Column | Type | Constraints | Description |
|---|---|---|---|
| `card_id_hash` | BLOB | PRIMARY KEY, CHECK (length = 32) | SHA-256 hash of the raw card UID |
| `card_tag` | TEXT | | Human-readable label (e.g. "Brandon's key") |
| `status` | TEXT | NOT NULL, DEFAULT 'active', CHECK (active/disabled/lost) | Card status |
| `created_at_ms` | INTEGER | NOT NULL | When the card was registered |
| `updated_at_ms` | INTEGER | NOT NULL | Last status change |
| `last_seen_at_ms` | INTEGER | | Last time this card was used for an access request |

**Status values:** `active` (access allowed), `disabled` (temporarily blocked), `lost` (permanently blocked, retained for audit trail). Only `active` cards pass the `IsCardAllowed()` check.

### access_events

Append-only audit log. Every access decision — granted or denied — is recorded here. This is the "who/what/when" trail.

| Column | Type | Constraints | Description |
|---|---|---|---|
| `access_event_id` | INTEGER | PRIMARY KEY AUTOINCREMENT | Row ID |
| `module_id` | TEXT | NOT NULL, FK → modules, ON DELETE CASCADE | Module that reported the card tap |
| `door_id` | TEXT | FK → doors, ON DELETE SET NULL | Door the module was assigned to at the time |
| `received_at_ms` | INTEGER | NOT NULL | Server-side receive timestamp |
| `requested_at_ms` | INTEGER | | Optional device-reported timestamp |
| `door_closed` | INTEGER | CHECK (0 or 1) | Reed switch state at time of card tap |
| `card_id_hash` | BLOB | FK → cards, ON DELETE SET NULL, CHECK (null or length = 32) | SHA-256 hash of the card UID |
| `decision_granted` | INTEGER | NOT NULL, CHECK (0 or 1) | 1 = access granted, 0 = denied |
| `decision_reason` | TEXT | NOT NULL | Why the decision was made (e.g. `card_allowed`, `card_not_allowed`, `unknown_module`) |
| `policy_version` | INTEGER | | Reserved for future policy versioning |
| `decided_at_ms` | INTEGER | NOT NULL | When the server made the decision |

**Foreign key behavior:** If a module is deleted, its heartbeats and access events are cascade-deleted. If a door is deleted, the `door_id` in modules and access events is set to null (the records survive). If a card is deleted, the `card_id_hash` in access events is set to null (the decision record survives).

### schema_migrations

Tracks which migrations have been applied. Managed by the migration system, not by application code.

| Column | Type | Constraints | Description |
|---|---|---|---|
| `version` | INTEGER | PRIMARY KEY | Migration version number |
| `applied_at_ms` | INTEGER | NOT NULL | When the migration was applied |

---

## Indexes

Created in migration `0002_indexes.sql`. These cover the primary query and pruning patterns.

| Index | Table | Columns | Purpose |
|---|---|---|---|
| `idx_modules_last_seen` | modules | `last_seen_at_ms` | Find stale or recently active modules |
| `idx_heartbeats_module_time` | module_heartbeats | `module_id, received_at_ms` | Query heartbeat history for a specific module |
| `idx_heartbeats_time` | module_heartbeats | `received_at_ms` | Retention pruning (delete rows older than cutoff) |
| `idx_cards_last_seen` | cards | `last_seen_at_ms` | Find recently used or unused cards |
| `idx_access_module_time` | access_events | `module_id, received_at_ms` | Query access history for a specific module |
| `idx_access_card_time` | access_events | `card_id_hash, received_at_ms` | Query access history for a specific card |
| `idx_access_time` | access_events | `received_at_ms` | Time-range queries and future retention pruning |

---

## Migration system

Migrations are SQL files embedded in the server binary at compile time (Go `embed`). They live in `server/internal/db/migrations/` and follow the naming convention `NNNN_description.sql` (e.g. `0001_init.sql`).

On startup, the server creates the `schema_migrations` tracking table (if it doesn't exist), reads all embedded `.sql` files, sorts them by version number, and applies any that haven't been applied yet. Each migration runs inside a transaction — if the SQL fails, the transaction is rolled back and the migration version is not recorded. The server exits with an error if a migration fails.

### Current migrations

| Version | File | Description |
|---|---|---|
| 1 | `0001_init.sql` | Creates all tables: doors, modules, module_heartbeats, cards, access_events |
| 2 | `0002_indexes.sql` | Adds indexes for query and pruning performance |
| 3 | `0003_add_free_heap.sql` | Adds `free_heap_bytes` column to module_heartbeats |

### Adding a new migration

1. Create a new file: `server/internal/db/migrations/0004_description.sql`
2. Write idempotent SQL (use `IF NOT EXISTS` for creates, `ALTER TABLE` for additions)
3. Rebuild the server — the file is embedded automatically via `//go:embed migrations/*.sql`
4. On the next startup, the new migration is applied and tracked

**Compatibility rule:** Migrations are forward-only. They add tables, columns, and indexes but never drop, rename, or remove. This ensures an older server binary remains compatible with a database that has had newer migrations applied, which makes rollbacks safe.

---

## Write model

All database writes are serialized through a single-goroutine worker (`db.Worker`). The worker runs a loop that reads `TxFn` closures from a buffered channel (capacity 256), executes each inside a transaction, and sends the result back to the caller.

```
  Goroutine A ──► worker.Do(ctx, fn) ──► chan job ──► Worker loop ──► tx.Begin
  Goroutine B ──► worker.Do(ctx, fn) ──►            │                 tx.Exec
  Goroutine C ──► worker.Do(ctx, fn) ──►            │                 tx.Commit
                                                    │               send result
                                                    ▼
                                              sequential execution
```

This eliminates SQLite's "database is locked" errors without requiring external locking or connection-per-goroutine pooling. The caller blocks until their transaction commits or the context expires. If a caller's context expires while queued, the write is still executed (the result is discarded) — this prevents half-finished transactions.

Read operations bypass the worker and query the database directly on the shared `*sql.DB` connection. WAL mode allows concurrent reads with the serialized writer.

---

## Heartbeat pruning

The `HeartbeatPruner` is a background goroutine that deletes heartbeat records older than a configurable retention period. It runs an immediate prune on startup and then repeats on a timer.

| Config variable | Default | Description |
|---|---|---|
| `PORTUNUS_HEARTBEAT_RETENTION_DAYS` | 30 | Days of heartbeat history to keep. Set to 0 to disable pruning. |
| `PORTUNUS_PRUNE_INTERVAL_HOURS` | 6 | How often the pruner runs. |

The pruner uses the `idx_heartbeats_time` index for an efficient range delete. It logs the count of deleted rows on each pass.

Access events are not pruned — they are an append-only audit trail intended to be retained indefinitely. If access event pruning becomes necessary in the future, it should follow the same pattern as heartbeat pruning with its own retention config.

---

## Timestamp convention

All timestamps in the database are stored as Unix epoch milliseconds (INTEGER). This avoids SQLite's lack of a native datetime type and makes time-range queries simple integer comparisons. The Go server converts between `time.Time` and `int64` milliseconds at the store layer boundary — all application code above the store works with `time.Time`.

The ESP32 firmware does not have a real-time clock. The `requested_at_ms` field in access events is optional (nullable) and populated only if the device reports a timestamp. The `received_at_ms` field is always populated by the server using its own wall clock.

---

## Backup and restore

### Offline backup (server stopped)

```bash
cp /var/lib/portunus/portunus.db /var/lib/portunus/backups/portunus-$(date +%Y%m%d).db
```

### Online backup (server running)

WAL mode allows a safe online backup using the SQLite CLI:

```bash
sqlite3 /var/lib/portunus/portunus.db ".backup '/var/lib/portunus/backups/portunus-$(date +%Y%m%d).db'"
```

This creates a consistent snapshot without stopping the server. The backup includes all WAL-committed data.

### Restore

Stop the server, replace the database file, and restart:

```bash
sudo systemctl stop portunus-server
cp /var/lib/portunus/backups/portunus-20260315.db /var/lib/portunus/portunus.db
sudo chown portunus:portunus /var/lib/portunus/portunus.db
sudo systemctl start portunus-server
```

The server applies any missing migrations on startup, so restoring an older backup to a newer server binary is safe.

---

## Useful queries

These queries can be run with the `sqlite3` CLI against the database file. Useful for debugging, reporting, and operational monitoring.

**Caution:** Do not run write queries with the `sqlite3` CLI while the server is running — this bypasses the serialized write worker and can cause "database is locked" errors. Read queries are safe at any time.

### Module status overview

```sql
SELECT
  module_id,
  display_name,
  CASE WHEN enabled = 1 AND commissioned_at_ms IS NOT NULL AND revoked_at_ms IS NULL
       THEN 'known' ELSE 'unknown' END AS status,
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
  datetime(ae.received_at_ms / 1000, 'unixepoch') AS time,
  ae.module_id,
  CASE ae.decision_granted WHEN 1 THEN 'GRANTED' ELSE 'DENIED' END AS decision,
  ae.decision_reason,
  hex(ae.card_id_hash) AS card_hash
FROM access_events ae
ORDER BY ae.received_at_ms DESC
LIMIT 50;
```

### Access events for a specific card

```sql
-- First, find the card hash from a known card UID:
-- In Python: hashlib.sha256(b"04:A3:2B:1C").hexdigest()
-- Or register the card via the admin API and note the returned hash.

SELECT
  datetime(ae.received_at_ms / 1000, 'unixepoch') AS time,
  ae.module_id,
  CASE ae.decision_granted WHEN 1 THEN 'GRANTED' ELSE 'DENIED' END AS decision,
  ae.decision_reason
FROM access_events ae
WHERE ae.card_id_hash = X'<64-char-hex-hash>'
ORDER BY ae.received_at_ms DESC;
```

### Cards and their status

```sql
SELECT
  hex(card_id_hash) AS card_hash,
  card_tag,
  status,
  datetime(created_at_ms / 1000, 'unixepoch') AS registered,
  datetime(last_seen_at_ms / 1000, 'unixepoch') AS last_used
FROM cards
ORDER BY created_at_ms DESC;
```

### Heartbeat history for a module

```sql
SELECT
  datetime(received_at_ms / 1000, 'unixepoch') AS time,
  seq,
  uptime_ms / 1000 AS uptime_sec,
  wifi_rssi,
  ip,
  free_heap_bytes
FROM module_heartbeats
WHERE module_id = 'door-001'
ORDER BY received_at_ms DESC
LIMIT 20;
```

### Modules that haven't checked in recently

```sql
-- Modules not seen in the last 5 minutes (300000 ms)
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

### Access grant/deny counts by day

```sql
SELECT
  date(received_at_ms / 1000, 'unixepoch') AS day,
  SUM(decision_granted) AS granted,
  COUNT(*) - SUM(decision_granted) AS denied,
  COUNT(*) AS total
FROM access_events
GROUP BY day
ORDER BY day DESC
LIMIT 30;
```

### Database size and table row counts

```sql
SELECT
  'doors' AS tbl, COUNT(*) AS rows FROM doors
UNION ALL SELECT
  'modules', COUNT(*) FROM modules
UNION ALL SELECT
  'module_heartbeats', COUNT(*) FROM module_heartbeats
UNION ALL SELECT
  'cards', COUNT(*) FROM cards
UNION ALL SELECT
  'access_events', COUNT(*) FROM access_events
UNION ALL SELECT
  'schema_migrations', COUNT(*) FROM schema_migrations;
```

---

## File location and permissions

| Environment | Path | Owner | Permissions |
|---|---|---|---|
| Dev | `./data/portunus.db` (relative to server working dir) | Current user | Default |
| Production | `/var/lib/portunus/portunus.db` | `portunus:portunus` | `0644` (file), `0755` (directory) |

The server creates the parent directory and database file automatically on first startup. In production, the systemd service runs as the `portunus` user with `ReadWritePaths=/var/lib/portunus` — see [Server Setup — Running as a systemd service](setup_server.md#running-as-a-systemd-service).

SQLite also creates `-wal` and `-shm` companion files alongside the main database file during WAL mode operation. These are managed by SQLite and should not be deleted manually while the server is running.