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

Today the database is responsible for nine main concerns:

1. **Inventory of doors and modules**
2. **Current module snapshot data** such as last seen time, last firmware version, and last RSSI
3. **Append-only heartbeat history**
4. **Member and admin credential hashes** stored as HMAC-SHA256 values in `member_access` (door-access members) and `admin_user_credentials` (admin RFID badges used for operator identification on the provisioning path)
5. **Append-only access audit events** for grant and deny decisions
6. **RBAC roles and permissions** defining what actions admin users are authorized to perform
7. **Member lifecycle state** including enrollment status, expiry, and module authorization grants
8. **Admin user accounts and server-side sessions** for the session-cookie-based admin API
9. **Admin action audit log** recording every state-changing action performed through the admin API

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

#### Module lifecycle states

The admin API derives a `status` field for every module from the same three columns. The mapping is:

| `revoked_at_ms` | `commissioned_at_ms` | `status` |
|---|---|---|
| non-null | any | `revoked` |
| null | null | `discovered` |
| null | non-null | `active` |

- **`discovered`** — the server has seen a heartbeat from this module but an admin has not commissioned it. The device is not trusted; access requests are denied.
- **`active`** — commissioned, enabled, and not revoked. The device is trusted and access decisions are made for its requests.
- **`revoked`** — explicitly revoked by an admin. The device is not trusted regardless of the `enabled` flag.

This field is computed in `AdminService.deriveModuleStatus()` and returned in all module list and detail responses. It is not stored in the database.

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

### `audit_log`

Records every state-changing action performed through the admin API. Added in migration `0013`.

| Column | Type | Constraints | Notes |
|---|---|---|---|
| `id` | TEXT | PRIMARY KEY | Opaque event identifier |
| `occurred_at_ms` | INTEGER | NOT NULL | Unix epoch milliseconds |
| `actor_uuid` | TEXT | nullable, FK → `admin_users(uuid)` ON DELETE SET NULL | Admin who performed the action; null accommodates future system-generated events |
| `action` | TEXT | NOT NULL | Name of the action performed |
| `resource_type` | TEXT | nullable | Category of the affected resource (e.g. `member`, `module`) |
| `resource_id` | TEXT | nullable | Identifier of the affected resource |
| `details` | TEXT | nullable | JSON blob with action-specific context such as changed fields or previous values |
| `ip_address` | TEXT | nullable | Client IP address when available |
| `result` | TEXT | NOT NULL, default `success`, CHECK in `success`, `failure` | Whether the action succeeded or was rejected (e.g. denied by RBAC) |

Four indexes support queries by time, actor, action name, and resource:

| Index | Columns |
|---|---|
| `idx_audit_log_occurred` | `occurred_at_ms` |
| `idx_audit_log_actor` | `actor_uuid` |
| `idx_audit_log_action` | `action` |
| `idx_audit_log_resource` | `resource_type, resource_id` |

---

### `admin_user_credentials`

Maps RFID badge hashes to admin user accounts. Added in migration `0017`.

This is **not** the old `credentials` table. It stores HMAC-SHA256 hashes of admin RFID badges so the provisioning FSM can identify the operator on scan 1 of the two-scan enrollment flow, replacing the prior compile-time `CONFIG_PORTUNUS_OPERATOR_UUID` constant. Uses the same `HashCredentialID(secret, raw_UID_bytes)` algorithm as `member_access.credential_hash`.

| Column | Type | Constraints | Notes |
|---|---|---|---|
| `credential_hash` | BLOB | PRIMARY KEY, CHECK length = 32 | HMAC-SHA256 hash of the admin's RFID badge raw UID |
| `admin_user_uuid` | TEXT | NOT NULL, FK → `admin_users(uuid)` ON DELETE CASCADE | Owning admin account |
| `created_at_ms` | INTEGER | NOT NULL | Unix epoch milliseconds |

One index supports lookups by admin account:

| Index | Columns |
|---|---|
| `idx_admin_user_credentials_uuid` | `admin_user_uuid` |

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
| `credential_hash` | BLOB | nullable, no FK (audit column only), CHECK null or length=32 | HMAC-SHA256 of the credential ID used in the request; kept for audit history — the FK to the dropped `credentials` table was removed in migration `0016` |
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

The reason string is produced by `AccessService.Decide()` and `AccessService.decideMemberAccess()`:

| Reason | Condition |
|---|---|
| `unknown_module` | Module not found in registry or not commissioned |
| `allow_all` | `AllowAll=true` dev override |
| `invalid_credential_format` | Credential ID did not parse as a colon-hex UID |
| `credential_not_found` | No `member_access` row has this credential hash |
| `member_<status>` | Member status is not `active` (e.g. `member_suspended`, `member_expired`, `member_archived`) |
| `member_disabled` | Member `enabled=0` |
| `module_not_authorized` | No active `module_authorizations` row for this member + module |
| `authorization_revoked` | Authorization row exists but `revoked_at_ms` is set |
| `authorization_expired` | Authorization `expires_at_ms` is in the past |
| `credential_allowed` | Access granted — member active and module authorized |
| `member_lookup_error` | Database error during member or authorization lookup |
| `service_misconfigured` | `AccessService` not fully wired; `Validate()` was not called before serving traffic |

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
- Deleting a `role` cascades to `role_permissions`
- Deleting a `member_access` row cascades to `module_authorizations`
- Deleting an `admin_users` row cascades to `sessions`
- Deleting an `admin_users` row sets `audit_log.actor_uuid` to `NULL`
- Deleting an `admin_users` row cascades to `admin_user_credentials`

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

Replaced the `cards` indexes dropped with that table. Migration `0016` later dropped the `credentials` table, removing `idx_credentials_last_seen` with it; `idx_access_credential_time` was recreated on the rebuilt `access_events` table in that same migration.

| Index | Table | Columns | Notes |
|---|---|---|---|
| `idx_credentials_last_seen` | `credentials` | `last_seen_at_ms` | **Historical** — dropped when migration `0016` removed the `credentials` table |
| `idx_access_credential_time` | `access_events` | `credential_hash, received_at_ms` | Per-credential audit history — active; recreated in migration `0016` |

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
| `0012` | `0012_seed_operator_viewer_permissions.sql` | Seeds permissions for the `operator` and `viewer` system roles |
| `0013` | `0013_audit_log.sql` | Adds `audit_log` table with four indexes for time, actor, action, and resource queries |
| `0014` | `0014_seed_audit_log_permissions.sql` | Seeds `audit_log.list` permission for `admin`, `operator`, and `viewer` roles |
| `0015` | `0015_clear_credential_hashes.sql` | Clears pre-existing `credential_hash` values in `member_access` that were computed under broken schemes; affected members must re-enroll |
| `0016` | `0016_drop_credentials_table.sql` | Drops the `credentials` table, removes its `credential.*` role permissions rows, and rebuilds `access_events` without the FK to `credentials` |
| `0017` | `0017_admin_user_credentials.sql` | Adds `admin_user_credentials` table mapping admin RFID badge hashes to admin users for provisioning-operator identification |

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
- `DeviceStore.MarkSeen()`
- `MemberAccessStore` write methods
- `ModuleAuthorizationStore` write methods
- `AdminUserStore` write methods
- `SessionStore` write methods

This design reduces SQLite write contention and keeps multi-step writes grouped inside explicit transactions.

### Important current exceptions

Not every write in the codebase uses the worker.

1. **`db.SeedDev()` writes directly** during startup seeding in local mode before normal request handling begins.
2. Reads query the shared `*sql.DB` directly.

So the most accurate statement for the current codebase is:

> Portunus serializes nearly all runtime writes through a single worker. `db.SeedDev()` writes directly during startup before normal request handling begins.

### Current worker semantics

`Worker.Do()` enqueues a job and waits for the result, but it uses the caller’s context when beginning and executing the transaction. That means context cancellation can still cause the queued or running write to fail.

The code comments describe the intent clearly, but the exact behavior is determined by the same context passed into `BeginTx()` and the transaction work itself.

---

## Local seeding behavior

When `PORTUNUS_ENV=local`, the server calls `db.SeedDev()` on startup.

Current local seeding does the following:

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

Do not delete these while the server is running. Always back up using the methods below so that WAL state is safely flushed into the backup copy.

### Online backup with `sqlite3` (recommended)

The `.backup` command checkpoints the WAL and produces a consistent single-file copy while the server is running:

```bash
sqlite3 /opt/portunus/data/portunus.db ".backup '/opt/portunus/backups/portunus-$(date +%Y%m%d-%H%M%S).db'"
```

### Offline backup

If the server is stopped, a plain copy is safe:

```bash
cp /opt/portunus/data/portunus.db /opt/portunus/backups/portunus-$(date +%Y%m%d-%H%M%S).db
```

### Automated daily backups on a Raspberry Pi

Create a backup script at `/opt/portunus/scripts/backup-db.sh`:

```bash
#!/usr/bin/env bash
set -euo pipefail

DB_PATH="/opt/portunus/data/portunus.db"
BACKUP_DIR="/opt/portunus/backups"
KEEP_DAYS=30

mkdir -p "$BACKUP_DIR"

BACKUP_FILE="$BACKUP_DIR/portunus-$(date +%Y%m%d-%H%M%S).db"
sqlite3 "$DB_PATH" ".backup '$BACKUP_FILE'"

# Verify the backup is not corrupt before rotating old copies.
if sqlite3 "$BACKUP_FILE" "PRAGMA integrity_check;" | grep -q "^ok$"; then
    # Remove backups older than KEEP_DAYS days.
    find "$BACKUP_DIR" -name "portunus-*.db" -mtime "+$KEEP_DAYS" -delete
    echo "Backup OK: $BACKUP_FILE"
else
    echo "ERROR: integrity check failed on $BACKUP_FILE" >&2
    rm -f "$BACKUP_FILE"
    exit 1
fi
```

Make it executable:

```bash
chmod +x /opt/portunus/scripts/backup-db.sh
```

Add a cron entry to run it daily at 03:00:

```bash
crontab -e
```

```
0 3 * * * /opt/portunus/scripts/backup-db.sh >> /var/log/portunus-backup.log 2>&1
```

### Backup rotation

The script above keeps 30 days of backups and deletes older copies automatically. Adjust `KEEP_DAYS` to match your retention requirements. At roughly 1–5 MB per database on a typical deployment, 30 daily copies requires minimal storage.

### Backup verification

The script runs `PRAGMA integrity_check` on every backup before rotating old copies. You can also verify any backup manually:

```bash
sqlite3 /opt/portunus/backups/portunus-20260429-030001.db "PRAGMA integrity_check;"
# Expected output: ok
```

A result other than `ok` means the backup file is corrupt and should not be used for restore.

### Restore

1. Stop the server: `sudo systemctl stop portunus`
2. Copy the backup over the live database:
   ```bash
   cp /opt/portunus/backups/portunus-<timestamp>.db /opt/portunus/data/portunus.db
   ```
3. Remove stale WAL files if present:
   ```bash
   rm -f /opt/portunus/data/portunus.db-wal /opt/portunus/data/portunus.db-shm
   ```
4. Restart the server: `sudo systemctl start portunus`

On startup, the server applies any pending embedded migrations that were added after the backup was taken.

---

## Useful queries

These queries are intended for read-only inspection with `sqlite3`.

### Module status overview

```sql
SELECT
  module_id,
  display_name,
  CASE
    WHEN revoked_at_ms IS NOT NULL THEN 'revoked'
    WHEN commissioned_at_ms IS NULL THEN 'discovered'
    ELSE 'active'
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

Member RFID badges enrolled via `member_access`:

```sql
SELECT
  m.uuid AS member_uuid,
  hex(m.credential_hash) AS credential_hash,
  m.status,
  m.provisioning_status,
  datetime(m.created_at_ms / 1000, 'unixepoch') AS created_at,
  datetime(m.last_access_at_ms / 1000, 'unixepoch') AS last_access
FROM member_access m
WHERE m.credential_hash IS NOT NULL
ORDER BY m.created_at_ms DESC;
```

Admin RFID badges registered in `admin_user_credentials`:

```sql
SELECT
  auc.admin_user_uuid,
  au.username,
  hex(auc.credential_hash) AS credential_hash,
  datetime(auc.created_at_ms / 1000, 'unixepoch') AS created_at
FROM admin_user_credentials auc
JOIN admin_users au ON au.uuid = auc.admin_user_uuid
ORDER BY auc.created_at_ms DESC;
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
UNION ALL SELECT 'access_events', COUNT(*) FROM access_events
UNION ALL SELECT 'roles', COUNT(*) FROM roles
UNION ALL SELECT 'role_permissions', COUNT(*) FROM role_permissions
UNION ALL SELECT 'member_access', COUNT(*) FROM member_access
UNION ALL SELECT 'module_authorizations', COUNT(*) FROM module_authorizations
UNION ALL SELECT 'admin_users', COUNT(*) FROM admin_users
UNION ALL SELECT 'sessions', COUNT(*) FROM sessions
UNION ALL SELECT 'audit_log', COUNT(*) FROM audit_log
UNION ALL SELECT 'admin_user_credentials', COUNT(*) FROM admin_user_credentials
UNION ALL SELECT 'schema_migrations', COUNT(*) FROM schema_migrations;
```

---

## File location notes

By default, the current server uses:

- `./data/portunus.db`

That path can be overridden with:

- `PORTUNUS_DB_PATH`

The server creates the parent directory automatically if it does not exist.