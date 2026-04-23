# Portunus — Database

Current-state reference for the Portunus server database: schema, migrations, write behavior, retention, and practical operational notes.

**Last updated:** April 2026

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

Today the database is responsible for eight main concerns:

1. **Inventory of doors and modules**
2. **Current module snapshot data** such as last seen time, last firmware version, and last RSSI
3. **Append-only heartbeat history**
4. **Registered credential storage** using keyed HMAC-SHA256 hashes rather than raw credential IDs
5. **Append-only access audit events** for grant and deny decisions
6. **RBAC roles and permissions** defining what actions admin users are authorized to perform
7. **Member lifecycle state** including enrollment status, expiry, and module authorization grants
8. **Admin user accounts and server-side sessions** for the session-cookie-based admin API

It is also used by the admin API for module, door, credential, member, and authorization management.

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

### `credentials`

Stores registered RFID credentials as keyed HMAC-SHA256 hashes rather than raw credential IDs. Renamed from `cards` in migration `0004`.

| Column | Type | Constraints | Notes |
|---|---|---|---|
| `credential_hash` | BLOB | PRIMARY KEY, CHECK length = 32 | HMAC-SHA256 hash bytes |
| `credential_tag` | TEXT | nullable | Human-readable label |
| `status` | TEXT | NOT NULL, default `active`, CHECK in `active`, `disabled`, `lost` | Current credential state |
| `created_at_ms` | INTEGER | NOT NULL | Registration time |
| `updated_at_ms` | INTEGER | NOT NULL | Last modification time |
| `last_seen_at_ms` | INTEGER | nullable | Updated asynchronously when an active credential is checked |

#### Current credential behavior

- Admin credential registration hashes the supplied credential ID with HMAC-SHA256 (keyed with `PORTUNUS_CREDENTIAL_HASH_SECRET`) before insertion.
- `IsCredentialAllowed()` looks up the hash and returns `true` only when `status = 'active'`.
- When an active credential is checked, `last_seen_at_ms` is updated asynchronously via a background goroutine dispatched to `writer.Do()` with a 5-second timeout.

#### Current status semantics

- `active`: eligible to grant access
- `disabled`: present but denied
- `lost`: present but denied

---

### `roles`

Defines named RBAC roles that can be assigned to members and admin users. Added in migration `0005`.

| Column | Type | Constraints | Notes |
|---|---|---|---|
| `role_id` | TEXT | PRIMARY KEY | Stable identifier such as `admin`, `member`, `guest` |
| `name` | TEXT | NOT NULL UNIQUE | Human-readable display name |
| `description` | TEXT | nullable | Optional description |
| `is_system` | INTEGER | NOT NULL default `0`, CHECK 0/1 | System roles cannot be deleted |
| `default_expiry_days` | INTEGER | nullable | Default membership duration when assigned this role |
| `default_inactivity_days` | INTEGER | nullable | Inactivity window after which status changes to expired |
| `created_at_ms` | INTEGER | NOT NULL | Unix epoch milliseconds |
| `updated_at_ms` | INTEGER | NOT NULL | Unix epoch milliseconds |

Five system roles are seeded by migration `0008`: `admin`, `operator`, `viewer`, `member`, `guest`.

---

### `role_permissions`

Maps roles to named permission constants. Added in migration `0005`.

| Column | Type | Constraints | Notes |
|---|---|---|---|
| `role_id` | TEXT | NOT NULL, FK → `roles(role_id)` ON DELETE CASCADE | Owning role |
| `permission` | TEXT | NOT NULL | Named permission constant |
| `granted_at_ms` | INTEGER | NOT NULL | Unix epoch milliseconds |

Primary key is `(role_id, permission)`. Twenty-nine permissions are seeded into the `admin` role by migration `0010`.

---

### `member_access`

Unified identity table for members and guests. A guest is a member row with the `guest` role; promotion to member is a role reassignment — the UUID and credential hash never change. Added in migration `0006`.

| Column | Type | Constraints | Notes |
|---|---|---|---|
| `uuid` | TEXT | PRIMARY KEY | Stable member identifier |
| `role_id` | TEXT | NOT NULL, FK → `roles(role_id)` | Current role assignment |
| `credential_hash` | BLOB | nullable UNIQUE, CHECK null or length=32 | HMAC-SHA256 of the enrolled credential; null until physical enrollment |
| `status` | TEXT | NOT NULL default `active`, CHECK in `active`, `suspended`, `expired`, `archived` | Lifecycle status |
| `enabled` | INTEGER | NOT NULL default `1`, CHECK 0/1 | Manual enable/disable flag independent of status |
| `expires_at_ms` | INTEGER | nullable | Hard expiry timestamp; null means no expiry |
| `inactivity_limit_days` | INTEGER | nullable | Inactivity window override; null inherits from role |
| `last_access_at_ms` | INTEGER | nullable | Updated on every granted access event |
| `created_at_ms` | INTEGER | NOT NULL | Unix epoch milliseconds |
| `created_by_uuid` | TEXT | nullable | Admin UUID who created the member |
| `promoted_from_uuid` | TEXT | nullable | Source guest UUID on guest-to-member promotion |
| `provisioning_status` | TEXT | NOT NULL default `active`, CHECK in `pending_authorization`, `active`, `incomplete` | Enrollment queue state |
| `archived_at_ms` | INTEGER | nullable | Set when status transitions to `archived` |
| `archived_by_uuid` | TEXT | nullable | Admin UUID who archived the member |

#### provisioning_status semantics

- `pending_authorization`: enrolled via PROVISIONING_CONSOLE but not yet approved by an admin
- `active`: fully authorized; access decisions use this row
- `incomplete`: enrollment started but not completed

---

### `module_authorizations`

Records which members are authorized to access which modules. Default-deny: no row means no access regardless of member status. Added in migration `0007`.

| Column | Type | Constraints | Notes |
|---|---|---|---|
| `authorization_id` | INTEGER | PRIMARY KEY AUTOINCREMENT | Row ID |
| `member_uuid` | TEXT | NOT NULL, FK → `member_access(uuid)` ON DELETE CASCADE | Authorized member |
| `module_id` | TEXT | NOT NULL, FK → `modules(module_id)` ON DELETE CASCADE | Target module |
| `granted_at_ms` | INTEGER | NOT NULL | Unix epoch milliseconds |
| `granted_by_uuid` | TEXT | nullable | Admin UUID who created the grant |
| `expires_at_ms` | INTEGER | nullable | Authorization-level expiry; null means no expiry |
| `revoked_at_ms` | INTEGER | nullable | Set on soft-delete revocation; null means active |
| `revoked_by_uuid` | TEXT | nullable | Admin UUID who revoked the grant |
| `time_restriction` | TEXT | nullable | JSON policy for time-of-day restrictions; null means unrestricted |

Revocation is a soft-delete: `revoked_at_ms` is set and the row is kept for audit history. A partial UNIQUE index on `(member_uuid, module_id) WHERE revoked_at_ms IS NULL` enforces one active grant per member per module while allowing historical revoked rows to accumulate.

---

### `admin_users`

Server operator accounts. Passwords are bcrypt-hashed. Added in migration `0009`; `role_id` and `enabled` added in migration `0011`.

| Column | Type | Constraints | Notes |
|---|---|---|---|
| `uuid` | TEXT | PRIMARY KEY | Stable admin identifier |
| `username` | TEXT | NOT NULL UNIQUE | Login name |
| `password_hash` | TEXT | NOT NULL | bcrypt hash |
| `must_change_pw` | INTEGER | NOT NULL default `1`, CHECK 0/1 | Forces password reset on first login |
| `role_id` | TEXT | nullable, FK → `roles(role_id)` | RBAC role; existing users default to `admin` role on upgrade |
| `enabled` | INTEGER | NOT NULL default `1`, CHECK 0/1 | Disabled accounts cannot log in |
| `created_at_ms` | INTEGER | NOT NULL | Unix epoch milliseconds |
| `updated_at_ms` | INTEGER | NOT NULL | Unix epoch milliseconds |
| `last_login_at_ms` | INTEGER | nullable | Updated on successful login |

---

### `sessions`

Server-side session store for cookie-based admin authentication. Added in migration `0009`.

| Column | Type | Constraints | Notes |
|---|---|---|---|
| `session_id` | TEXT | PRIMARY KEY | Opaque session token stored in the `portunus_session` cookie |
| `admin_uuid` | TEXT | NOT NULL, FK → `admin_users(uuid)` ON DELETE CASCADE | Owning admin account |
| `created_at_ms` | INTEGER | NOT NULL | Unix epoch milliseconds |
| `expires_at_ms` | INTEGER | NOT NULL | Session expiry; `DeleteExpiredSessions()` sweeps expired rows |

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
| `credential_hash` | BLOB | nullable, FK → `credentials(credential_hash)` ON DELETE SET NULL, CHECK null or length=32 | HMAC-SHA256 of the credential ID used in the request |
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
- HMAC-SHA256 `credential_hash`
- `decision_granted`
- `decision_reason`
- `decided_at_ms`

`AccessEventStore.RecordEvent()` resolves `door_id` from the module’s current assignment in the `modules` table at insert time.

#### Current decision reasons seen in code

The reason string depends on which access path handled the request:

| Reason | Path |
|---|---|
| `unknown_module` | All paths — module not known |
| `allow_all` | `AllowAll=true` dev override |
| `credential_allowed` | Credential store or member path — access granted |
| `credential_not_allowed` | Credential store — hash not found or not active |
| `credential_lookup_error` | Credential store — database error |
| `credential_not_found` | Member path — no member row with this credential hash |
| `member_<status>` | Member path — member status is not `active` (e.g. `member_suspended`) |
| `member_disabled` | Member path — member `enabled=0` |
| `module_not_authorized` | Member path — no active authorization row for this member+module |
| `authorization_revoked` | Member path — authorization exists but `revoked_at_ms` is set |
| `authorization_expired` | Member path — authorization `expires_at_ms` is in the past |
| `member_lookup_error` | Member path — database error during member or authorization lookup |

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
- Deleting a `module` cascades to `module_authorizations`
- Deleting a `credential` sets `access_events.credential_hash` to `NULL`
- Deleting a `role` cascades to `role_permissions`
- Deleting a `member_access` row cascades to `module_authorizations`
- Deleting an `admin_users` row cascades to `sessions`

This preserves audit history where practical while still allowing entities to be removed.

---

## Indexes

### Migration `0002` — base indexes

| Index | Table | Columns | Current purpose |
|---|---|---|---|
| `idx_modules_last_seen` | `modules` | `last_seen_at_ms` | Find recently active or stale modules |
| `idx_heartbeats_module_time` | `module_heartbeats` | `module_id, received_at_ms` | Per-module heartbeat history |
| `idx_heartbeats_time` | `module_heartbeats` | `received_at_ms` | Heartbeat retention pruning |
| `idx_access_module_time` | `access_events` | `module_id, received_at_ms` | Per-module audit history |
| `idx_access_time` | `access_events` | `received_at_ms` | Time-range reporting |

### Migration `0004` — credential rename

Replaced the `cards` indexes dropped with that table:

| Index | Table | Columns | Current purpose |
|---|---|---|---|
| `idx_credentials_last_seen` | `credentials` | `last_seen_at_ms` | Credential usage lookups |
| `idx_access_credential_time` | `access_events` | `credential_hash, received_at_ms` | Per-credential audit history |

### Migration `0005` — RBAC

| Index | Table | Columns | Current purpose |
|---|---|---|---|
| `idx_role_permissions_role` | `role_permissions` | `role_id` | Role permission lookups |

### Migration `0006` — member access (partial indexes)

| Index | Table | Filter | Columns | Current purpose |
|---|---|---|---|---|
| `idx_member_access_credential_active` | `member_access` | `status = 'active'` | `credential_hash` | Fast access-check path: credential → active member |
| `idx_member_access_expires` | `member_access` | `status = 'active' AND expires_at_ms IS NOT NULL` | `expires_at_ms` | Hard-expiry sweep |
| `idx_member_access_last_access` | `member_access` | `status = 'active'` | `last_access_at_ms` | Inactivity sweep |
| `idx_member_access_pending` | `member_access` | `provisioning_status = 'pending_authorization'` | `created_at_ms` | Pending enrollment queue ordered by arrival time |

### Migration `0007` — module authorizations (partial indexes)

| Index | Table | Filter | Columns | Current purpose |
|---|---|---|---|---|
| `uidx_module_auth_active_grant` | `module_authorizations` | `revoked_at_ms IS NULL` | `member_uuid, module_id` | Enforces one active grant per member per module (UNIQUE) |
| `idx_module_auth_active` | `module_authorizations` | `revoked_at_ms IS NULL` | `module_id, member_uuid` | Fast access-check path: module + member, non-revoked only |
| `idx_module_auth_expires` | `module_authorizations` | `revoked_at_ms IS NULL AND expires_at_ms IS NOT NULL` | `expires_at_ms` | Authorization expiry sweep |

### Migration `0009` — sessions

| Index | Table | Columns | Current purpose |
|---|---|---|---|
| `idx_sessions_admin_uuid` | `sessions` | `admin_uuid` | Sessions for a given admin user |
| `idx_sessions_expires` | `sessions` | `expires_at_ms` | Session expiry cleanup |

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
| `0001` | `0001_init.sql` | Base schema: `doors`, `modules`, `module_heartbeats`, `cards`, `access_events`, `schema_migrations` |
| `0002` | `0002_indexes.sql` | Query and pruning indexes |
| `0003` | `0003_add_free_heap.sql` | Adds `free_heap_bytes` to `module_heartbeats` |
| `0004` | `0004_rename_cards_to_credentials.sql` | Renames `cards` → `credentials` (columns and FK references); renames `card_id_hash` → `credential_hash` in `access_events` |
| `0005` | `0005_roles_rbac.sql` | Adds `roles` and `role_permissions` tables |
| `0006` | `0006_member_access.sql` | Adds `member_access` table with partial indexes for the access-check and expiry-sweep hot paths |
| `0007` | `0007_module_authorizations.sql` | Adds `module_authorizations` table with soft-delete partial UNIQUE index |
| `0008` | `0008_seed_default_roles.sql` | Seeds five built-in roles: `admin`, `operator`, `viewer`, `member`, `guest` |
| `0009` | `0009_admin_users_sessions.sql` | Adds `admin_users` and `sessions` tables for session-cookie-based admin auth |
| `0010` | `0010_seed_admin_role_permissions.sql` | Seeds 29 named permissions into the `admin` role |
| `0011` | `0011_admin_users_add_role.sql` | Adds `role_id` FK and `enabled` column to `admin_users`; backfills existing users to `admin` role |

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
- `CredentialStore.RegisterCredential()`
- `CredentialStore.SetCredentialStatus()`
- `CredentialStore.DeleteCredential()`
- `DeviceStore.MarkSeen()`
- `MemberAccessStore` write methods
- `ModuleAuthorizationStore` write methods
- `AdminUserStore` write methods
- `SessionStore` write methods

This design reduces SQLite write contention and keeps multi-step writes grouped inside explicit transactions.

### Important current exceptions

Not every write in the codebase uses the worker.

1. **`CredentialStore.IsCredentialAllowed()` updates `credentials.last_seen_at_ms`** via a background goroutine that dispatches to `writer.Do()` with a 5-second timeout. The update is fire-and-forget relative to the access decision path — a timeout or cancellation is logged but does not fail the access check.
2. **`db.SeedDev()` writes directly** during startup seeding in dev mode before normal request handling begins.
3. Reads query the shared `*sql.DB` directly.

So the most accurate statement for the current codebase is:

> Portunus serializes nearly all runtime writes through a single worker. The one notable exception is the `last_seen_at_ms` update in the hot access-check path, which is dispatched asynchronously to the worker so it cannot delay the access response.

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
  hex(credential_hash) AS credential_hash,
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
  hex(credential_hash) AS credential_hash
FROM access_events
WHERE module_id = 'door-001'
ORDER BY received_at_ms DESC;
```

### Access events for a specific credential hash

```sql
SELECT
  datetime(received_at_ms / 1000, 'unixepoch') AS time,
  module_id,
  CASE decision_granted WHEN 1 THEN 'GRANTED' ELSE 'DENIED' END AS decision,
  decision_reason
FROM access_events
WHERE credential_hash = X'<64-char-hex-hash>'
ORDER BY received_at_ms DESC;
```

### Registered credentials

```sql
SELECT
  hex(credential_hash) AS credential_hash,
  credential_tag,
  status,
  datetime(created_at_ms / 1000, 'unixepoch') AS created_at,
  datetime(last_seen_at_ms / 1000, 'unixepoch') AS last_seen
FROM credentials
ORDER BY created_at_ms DESC;
```

### All members with role and credential status

```sql
SELECT
  m.uuid,
  m.status,
  m.provisioning_status,
  m.enabled,
  r.name AS role,
  CASE WHEN m.credential_hash IS NULL THEN 'not enrolled' ELSE 'enrolled' END AS credential_status,
  datetime(m.last_access_at_ms / 1000, 'unixepoch') AS last_access,
  datetime(m.expires_at_ms / 1000, 'unixepoch') AS expires_at
FROM member_access m
LEFT JOIN roles r ON r.role_id = m.role_id
ORDER BY m.created_at_ms DESC;
```

### Active module authorizations for a member

```sql
SELECT
  ma.authorization_id,
  ma.module_id,
  datetime(ma.granted_at_ms / 1000, 'unixepoch') AS granted_at,
  datetime(ma.expires_at_ms / 1000, 'unixepoch') AS expires_at
FROM module_authorizations ma
WHERE ma.member_uuid = '<member-uuid>'
  AND ma.revoked_at_ms IS NULL
ORDER BY ma.granted_at_ms DESC;
```

### Members pending provisioning approval

```sql
SELECT
  uuid,
  datetime(created_at_ms / 1000, 'unixepoch') AS submitted_at,
  hex(credential_hash) AS credential_hash
FROM member_access
WHERE provisioning_status = 'pending_authorization'
ORDER BY created_at_ms ASC;
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
UNION ALL SELECT 'credentials', COUNT(*) FROM credentials
UNION ALL SELECT 'access_events', COUNT(*) FROM access_events
UNION ALL SELECT 'roles', COUNT(*) FROM roles
UNION ALL SELECT 'role_permissions', COUNT(*) FROM role_permissions
UNION ALL SELECT 'member_access', COUNT(*) FROM member_access
UNION ALL SELECT 'module_authorizations', COUNT(*) FROM module_authorizations
UNION ALL SELECT 'admin_users', COUNT(*) FROM admin_users
UNION ALL SELECT 'sessions', COUNT(*) FROM sessions
UNION ALL SELECT 'schema_migrations', COUNT(*) FROM schema_migrations;
```

---

## File location notes

By default, the current server uses:

- `./data/portunus.db`

That path can be overridden with:

- `PORTUNUS_DB_PATH`

The server creates the parent directory automatically if it does not exist.