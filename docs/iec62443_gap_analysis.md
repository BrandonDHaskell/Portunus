# Portunus: Purdue Model / IEC 62443 SL2 Gap Analysis and Roadmap

**Date:** November 27, 2026
**Baseline:** `main` at `36388b5` plus the dependency bumps up to `a7dfeb0`
**Yardstick:** IEC 62443 at Security Level 2 (SL2): 3-2 (zones and conduits), 3-3 (system requirements), 4-1 (secure development lifecycle), 4-2 (component requirements: CR, EDR for embedded devices, HDR/NDR for host and network devices)
**Decisions behind this review:**
- Offline target: a signed, encrypted local allow-list on each module. Locks are fail-secure, with mechanical egress for life safety.
- Hardware root of trust: documented and checked in CI only. No board is eFuse-fused in this phase.

SL2 means protection against intentional violation using simple means, low resources, generic skills and low motivation. That fits a LAN-first door system for makerspaces and small offices. SL3 (sophisticated means, IACS-specific skills) would require the hardware items marked "documented only" below, plus per-device hardware keys.

---

## 1. Purdue level map

```
 Level 4/5  Enterprise IT / Internet        (out of scope; no conduit today)
 ─────────────────────────────────────────────────────────────────────────
 Level 3.5  IDMZ                             NOT PRESENT
            - admin UI / REST (today: same listener as devices)
            - firmware + config staging      (no OTA exists)
            - log collector                  (none; logs stay on L3 host)
 ─────────────────────────────────────────────────────────────────────────
 Level 3    Site operations                  Raspberry Pi 5, portunus-server
            - access decisions, SQLite, audit_log, access_events
            - CA private key (certs/ca.key) lives here today
 ─────────────────────────────────────────────────────────────────────────
 Level 2    Supervisory / control LAN        Wi-Fi LAN (shared with other hosts)
 ─────────────────────────────────────────────────────────────────────────
 Level 1    Controller                       ESP32-S3 access module (FreeRTOS)
            - system_fsm, server_comm, grpc_client
 ─────────────────────────────────────────────────────────────────────────
 Level 0    Process / field                  MFRC522 reader, strike/maglock GPIO,
                                             door sensor, feedback LED
```

### Zones and conduits

| Conduit | From to | Protocol today | Authn today | SL2 status |
|---|---|---|---|---|
| C1 field wiring | L0 to L1 | SPI (reader), GPIO (lock, sensor) | none (physical) | Acceptable at SL2 if the enclosure is tamper-evident. No tamper input exists (gap G5). |
| C2 device | L1 to L3 | gRPC over TLS 1.2+, module initiates | server cert pinned to private CA; HMAC-SHA256 shared secret | Partial. No device cert (G1). The direction is right: modules dial out, so field devices have no inbound port. |
| C3 admin | operator to L3 | HTTPS admin UI and REST | bcrypt plus server-side session, RBAC | Partial. Served on the same `PORTUNUS_HTTP_ADDR` listener and mux as the device HTTP API (`server/internal/httpapi/server.go:98-123`), so the admin and device zones cannot be split by firewall rule (G8). |
| C4 update | L3.5 to L1 | none | n/a | Gap. Firmware is flashed over USB, and the partition table has a single `factory` app (G3). |
| C5 logs | L3 to L3.5 | none | n/a | Gap. Audit data stays in the SQLite file on the same host it describes (G6). |

---

## 2. Practice-by-practice assessment

Status key: **Met**, **Partial**, **Gap**. Evidence is a file the claim was checked against.

### 2.1 Architectural paradigms

| Practice | 62443 ref | Status | Evidence and notes |
|---|---|---|---|
| Zone and conduit segmentation; no L3 to L4/5 direct path | 3-2 ZCR 3, 3-3 SR 5.1/5.2 | Partial | Nothing reaches L4/5 today, because no such conduit exists. But there is no documented zone model, the admin and device conduits share one listener, and the server binds `:8080`/`:8443` on every interface (`server/internal/config/config.go:130`). |
| Zero Trust at the LAN edge (mTLS or hardware-backed certs) | 3-3 SR 1.2, 4-2 CR 1.2 | Partial | The server authenticates to modules (pinned CA). Modules authenticate with a fleet-wide HMAC secret only. No `ClientAuth` appears in either TLS config (`server/cmd/portunus-server/main.go:250`, `server/internal/httpapi/server.go:257`). A module is known only if it is commissioned, enabled and not revoked (`DeviceStore.IsKnown`), which is good, but its identity is just the unauthenticated `module_id` string plus a shared secret. |
| Fail-safe, deterministic design | 3-3 SR 3.6, 4-2 CR 3.6 | Met (core), Partial (watchdog) | A decision timeout produces a deny, and relock is retried every poll until it succeeds (`access_module/core/system_fsm/src/system_fsm.cpp:493-503, 551, 700`). There are no heap-allocating protobufs on the device (Nanopb `max_size`), and the FSM uses queue-based events. The task watchdog is on but only watches the idle tasks (`CONFIG_ESP_TASK_WDT_CHECK_IDLE_TASK_CPU0/1`): no application task calls `esp_task_wdt_add`, so a deadlocked FSM or comm task that still yields would not reset the module. |
| Unidirectional, push-based telemetry | 3-3 SR 5.2 | Met | Modules open the gRPC connection and push heartbeats and requests. The server never connects to a module. |

### 2.2 Firmware and hardware (Levels 0 to 2)

| Practice | 62443 ref | Status | Evidence and notes |
|---|---|---|---|
| Hardware root of trust, secure boot | 4-2 EDR 3.14, CR 3.4 | Gap (documented only, by decision) | Compiled prod config has no `CONFIG_SECURE_BOOT`, and `CONFIG_SECURE_ROM_DL_MODE_ENABLED` is unset (`access_module/cmake-build-fw-prod/config/sdkconfig.h`). |
| Flash and secret encryption | 4-2 CR 4.1 | Gap | No flash encryption and no NVS encryption, although an `nvs_keys` partition is already reserved (`access_module/partitions.csv`). Wi-Fi credentials and the HMAC secret sit in plaintext NVS. |
| A/B partitions, atomic OTA, rollback | 4-2 EDR 3.10, CR 3.4 | Gap | A single 1.5 MB `factory` app, no `otadata`, no `CONFIG_BOOTLOADER_APP_ROLLBACK_ENABLE`, and no anti-rollback eFuse counter. |
| Memory-safe coding, static analysis | 4-1 SI-1, SVV-1 | Partial | Host unit tests run under ASan and UBSan and under a 32-bit data model in CI (`.github/workflows/ci.yml`, job `firmware-unit`). The code style forbids iostream and std::string in hot paths. There is no static analyzer (clang-tidy, cppcheck or a MISRA subset) on the firmware, and `golangci-lint` exists as `task lint:server` but is not in CI. |
| Attack surface reduction (JTAG, UART, debug) | 4-2 EDR 2.13, CR 7.7 | Gap | The prod build keeps USB-Serial-JTAG enabled (`CONFIG_USJ_ENABLE_USB_SERIAL_JTAG`), the UART0 console, log level INFO, and the ROM download mode open. `scripts/assert_secure_build.sh` checks TLS, HMAC and the CA pin, but nothing in this list. |

### 2.3 LAN and server communication (Levels 2 to 3.5)

| Practice | 62443 ref | Status | Evidence and notes |
|---|---|---|---|
| Protocol hardening, input validation | 4-2 CR 3.5 | Partial | The device body is capped at 4 KiB, the admin body at 16 KiB, `ReadHeaderTimeout` is 5 s, and unknown JSON fields are rejected. Nanopb has fixed bounds. There is no fuzzing anywhere (no `func Fuzz` in `server/`, no firmware parser fuzz harness). |
| No custom crypto | 4-1 SD-4 | Met | HMAC-SHA256 from the standard library and mbedTLS, TLS from crypto/tls and esp-tls. The signed "projection" strings are a framing convention, not a cipher, and are pinned by golden vectors (`proto/testdata/hmac_vectors.txt`). |
| Replay protection (nonce, counter, time window) | 3-3 SR 3.1, 4-2 CR 3.1 | Met (access), Partial (others) | Access requests carry a 16-byte nonce and an RFC 3339 timestamp, checked against a 60 s window in a store shared by both transports (`server/internal/replay/replay.go`, `server/cmd/portunus-server/main.go:154`). The decision is signed back bound to that nonce (`server/internal/devicesig/devicesig.go`). Limits: the replay store is in memory, so a server restart reopens a replay window of up to 60 s for captured requests. Heartbeat `sequence` is signed but not checked for monotonicity. `ProvisionCredential` has no nonce. The heartbeat response that sets the module clock (`server_comm.cpp`, `sync_clock_from_response`) is protected only by TLS, not by the app-layer signature. |
| Out-of-band staging at L3.5 | 3-3 SR 3.4, 4-1 SUM-4 | Gap | No update channel exists. Becomes relevant with OTA (G3). |

### 2.4 Physical security and resilience

| Practice | 62443 ref | Status | Evidence and notes |
|---|---|---|---|
| Tamper detection and zeroization | 4-2 EDR 3.11, CR 4.2 | Gap | No tamper input in `drivers/` and no key zeroization path. The HMAC secret stays resident in a static buffer for the life of the comm task (`server_comm.cpp`, `s_hmac_secret`). |
| Offline survivability, signed local allow-list | 3-3 SR 7.1/7.2 | Gap (target chosen) | Offline means deny-all after the decision timeout (`main.cpp:295`: "running in offline mode"). Safe for a fail-secure door, but a server outage locks everyone out. |
| Immutable audit logging to L3/3.5 collector | 3-3 SR 2.8/2.9/2.10, 4-2 CR 2.8-2.12 | Partial | `audit_log` covers every admin action with actor, peer, result and reason, and `access_events` records every decision with a credential hash, never the raw UID (`docs/security.md`, "Audit trail"). But writes are best-effort, rows live in a mutable SQLite file on the host being audited, nothing chains or signs them, and nothing forwards them off the host. |

### 2.5 Secure development lifecycle

| Practice | 62443 ref | Status | Evidence and notes |
|---|---|---|---|
| Dependency pinning | 4-1 SM-9, SM-10 | Met | Actions are pinned by SHA, the IDF image by digest, Go modules via `go.sum` with Dependabot, ESP components via hashed `access_module/dependencies.lock`, and nanopb via pip `==0.4.9.1`. |
| CVE tracking | 4-1 DM-1..5 | Partial | `govulncheck` runs on every PR (`task vuln:server`). Firmware and ESP-IDF components are not scanned, and the IDF image is bumped by hand. |
| SBOM | 4-1 SM-9 (and EO 14028 practice) | Gap | No SBOM is generated for the server or the firmware. |
| Fuzzing | 4-1 SVV-3 | Gap | See 2.3. |
| HIL and fault injection | 4-1 SVV-1/3 | Partial | A hardware tier exists for the bench ESP32-S3 (`scripts/hw_test.py`), and CI compile-checks it. It is run by hand, and there are no fault scenarios (power cut, packet drop, malformed frames). |
| Security requirements and threat model documented | 4-1 SR-2, SR-3 | Partial | `docs/security.md` has a threat model but is stale: it says application-layer replay protection is "not implemented" and that secrets live in build-time config. Both are wrong now (nonce and window checking exists; secrets moved to NVS). Its "Last updated" is June 2, 2026. |
| Secure-by-default configuration | 4-1 SG-3, 4-2 CR 7.6 | Partial | Prod server `Validate()` requires secrets, and the firmware prod build is guarded by `assert_secure_build.sh`. But the server binary still starts plain HTTP when no certs are set, and HMAC is skipped when the secret is empty (`docs/security.md`, "Enforcement is configuration-dependent"). The server is protected only by profile validation. |
| Host hardening (L3) | 4-2 HDR | Partial | The systemd unit in `docs/setup_server.md:287-299` runs as the `portunus` user with `NoNewPrivileges`, `ProtectSystem=strict`, `PrivateTmp` and `ReadWritePaths`. It is missing `CapabilityBoundingSet=`, `RestrictAddressFamilies`, `SystemCallFilter`, `ProtectKernel*` and `MemoryDenyWriteExecute`, and a firewall allowlist restricting the device port to the device VLAN or subnet. |

---

## 3. Gap register

| ID | Gap | Severity at SL2 | Why it matters |
|---|---|---|---|
| G1 | No per-device identity (fleet-wide HMAC, no mTLS) | High | One extracted module (see G2) can impersonate every module. |
| G2 | No secure boot, flash or NVS encryption; debug interfaces open | High | Physical access to any module yields the fleet HMAC secret and Wi-Fi credentials, and allows arbitrary firmware. Together with G1, this is the main path from one module to the whole system. |
| G3 | No OTA, A/B or rollback | Medium | Patching requires a USB visit to every door, so in practice firmware CVEs stay unpatched. |
| G4 | No offline allow-list | Medium (availability) | A server or Wi-Fi outage locks out every user. |
| G5 | No tamper detection | Medium | An enclosure breach is invisible to the system. |
| G6 | Audit log mutable and on-host | Medium | A compromised L3 host can rewrite its own history. |
| G7 | No SBOM, fuzzing or C++ static analysis | Medium | 4-1 practices required for an SL2 claim. |
| G8 | Admin and device on one listener; no zone model | Medium | The admin UI cannot be firewalled to a management subnet without also blocking devices. |
| G9 | Watchdog covers idle tasks only | Low-Medium | A hung FSM or comm task that still yields keeps the door unresponsive (it stays fail-secure, but it is down). |
| G10 | Replay store in memory; heartbeat sequence unchecked; clock sync TLS-only | Low | Narrow windows, and all are behind TLS. They matter more once G4 makes the module clock security-relevant for offline expiry. |
| G11 | `docs/security.md` stale | Low | The threat model is the 4-1 input for every other decision. |
| G12 | Prod server accepts plain HTTP or no HMAC when misconfigured | Low | Covered by `Validate()` in prod, but the binary itself is not secure-by-default. |

---

## 4. Roadmap

Ordered by risk reduction and dependency. Each item is sized as one PR unless noted. "Skill" names the `.claude/skills/` procedure that applies, and "Hazard" names the CLAUDE.md invariant it touches.

### P0: quick wins, no protocol change

**R0.1 Refresh `docs/security.md` (G11).**
Rewrite the replay section to describe `internal/replay` and `devicesig` accurately, move secrets to NVS, list the limitations from section 2.3 of this report, and link to this report.
*Accept:* no claim in the doc contradicts the code. "Last updated" is bumped.

**R0.2 Prod firmware attack-surface hardening (G2 partial, G9).**
In `sdkconfig.defaults.prod`:
- Set the log level to WARN, with a maximum of WARN.
- Disable the USB-Serial-JTAG console. Keep or remove UART0 as a deliberate choice, decided on the bench, because it is the only connectivity diagnostic (hazard #12).
- Set `CONFIG_ESP_TASK_WDT_PANIC=y`.
- Subscribe the FSM and comm tasks with `esp_task_wdt_add` and feed them from their loops.

Extend `assert_secure_build.sh --prod` to refuse an image where those are off.
*Skill:* firmware-component. *Hazard:* #8.
*Accept:* the prod compile job fails if any setting regresses. A host or component test proves a stalled FSM loop trips the WDT, using a test hook.

**R0.3 SBOMs in CI (G7).**
- Server: `cyclonedx-gomod app` (or `syft`) against `server/`.
- Firmware: Espressif's `esp-idf-sbom create` against the prod build's `project_description.json`.

Upload both as workflow artifacts. Add `esp-idf-sbom check` (a CVE scan of the SBOM against NVD) as a non-blocking job first.
*Accept:* every PR produces two CycloneDX/SPDX files, and the firmware SBOM lists IDF 5.5.1, nghttp and nanopb with their versions.
*Constraint:* add the job through the Taskfile, then `ci.yml` (memory: CI changes go Taskfile-first; state which checks change before pushing).

**R0.4 Go native fuzz tests (G7).**
Targets:
- `service.ParseCredentialUID`.
- `proto.Unmarshal` into each device request, then the handler conversion in `httpapi/convert.go`.
- `replay.Store.Check` timestamp parsing.
- `hmacProjection` (must never panic, and must be injective over field separators).

Seed corpora from `proto/testdata/hmac_vectors.txt`. Run with `-fuzztime=60s` per target in CI, longer on a nightly schedule.
*Accept:* each target runs in CI, and found crashers are committed to `testdata/fuzz/`.

**R0.5 Static analysis gates (G7).**
- `clang-tidy` (from `/opt/llvm` locally, the apt or LLVM image in CI) over `core/`, `services/` and `drivers/` using `compile_commands.json` from the host test preset. Start with `bugprone-*`, `cert-*`, `cppcoreguidelines-pro-bounds-*` and `clang-analyzer-*`. Treat this as the MISRA-inspired subset; full MISRA C++:2023 needs a commercial checker.
- Add `task lint:server` (golangci-lint with `gosec` enabled) to `ci:server`.

*Accept:* both run in CI as blocking checks with a committed baseline.

**R0.6 Host hardening and conduit firewall (G8 partial, HDR).**
Add these to the systemd unit in `docs/setup_server.md` and `deploy_*.sh`:
- `CapabilityBoundingSet=`
- `AmbientCapabilities=`
- `RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX`
- `SystemCallFilter=@system-service`
- `ProtectKernelTunables/Modules/Logs=true`
- `ProtectControlGroups=true`
- `MemoryDenyWriteExecute=true`
- `LockPersonality=true`

Document an nftables ruleset: allow the gRPC port only from the device subnet, and the admin port only from the management subnet. This is a user action on the epiphany host (network-affecting).
*Accept:* `systemd-analyze security portunus` scores 2.0 or lower, and the server still passes its boot smoke on the Pi.

### P1: identity, integrity, updates

**R1.1 Split the admin and device listeners (G8).**
Add `PORTUNUS_ADMIN_ADDR`. Admin routes move to their own `http.Server` and mux, and device HTTP routes stay on `PORTUNUS_HTTP_ADDR`. Prod `Validate()` requires the two to differ, so the firewall in R0.6 can separate conduits C2 and C3.
*Skill:* server-service. *Hazard:* #7 (same cert/key).
*Accept:* an admin request to the device port gets a 404 in prod, and the boot smoke checks both.

**R1.2 Per-device mTLS identity (G1).**
- Issue a client cert per module from the existing CA, with `module_id` in a URI SAN, `spiffe`-style: `portunus://module/<id>`. Add `generate_certs.sh --module <id>`.
- Store the cert and key in NVS, which moves to encrypted NVS once R1.4 lands.
- The server sets `ClientAuth: tls.RequireAndVerifyClientCert` on the gRPC listener, and an interceptor rejects any request whose claimed `module_id` differs from the cert SAN.
- Keep HMAC as a second factor during migration, then make it per-device (derived from the cert or provisioned per module) so the fleet-wide secret goes away.

*Skill:* proto-contract-change (if any field changes), deploy-and-harden, firmware-component. *Hazard:* #5, #9, #10, #11.
*Accept:* a module with a valid cert for `door-002` cannot send as `door-001`, the server refuses a TLS connection without a client cert in prod, and the cert rotation procedure is documented.
*Size:* 2 to 3 PRs (server, firmware, provisioning tooling).

**R1.3 Tamper-evident audit trail (G6).**
- Add a migration giving `audit_log` and `access_events` a `prev_hash` and `row_hash` (SHA-256 over the canonical row plus `prev_hash`), written inside `db.Worker` so the chain stays serialized.
- Add `task audit:verify` and an admin health field that reports chain integrity.
- Add an optional RFC 5424 syslog-over-TLS forwarder (`PORTUNUS_SYSLOG_ADDR`) that ships every row to an L3.5 collector, still best-effort so availability-first holds.

*Skill:* sqlite-migration, server-service, access-control-change (audit write policy).
*Accept:* editing or deleting any row is detected by `audit:verify`, and forwarded logs carry no raw UID or credential hash.

**R1.4 OTA with A/B rollback and staged pull (G3, C4).**
- New partition table: `otadata` and `ota_0`/`ota_1`, sized for the current image plus headroom. This needs a one-time USB reflash of every module.
- Enable `CONFIG_BOOTLOADER_APP_ROLLBACK_ENABLE` and mark the image valid only after the first successful signed heartbeat and access round-trip.
- Images are signed with an ECDSA P-256 key held off the Pi. The module verifies before switching and pulls from an update service the L3 server proxies from L3.5 staging.
- Add `CONFIG_BOOTLOADER_APP_ANTI_ROLLBACK` as config, with the eFuse counter documented only.

*Skill:* firmware-component, proto-contract-change (update-available signal in the heartbeat response), deploy-and-harden.
*Accept:* on the bench board, a corrupted image is rejected, an image that fails its health check rolls back after reboot, and a power cut mid-download leaves the old image running.
*Size:* 3 or more PRs.

**R1.5 Close the small replay gaps (G10).**
- Persist the replay high-water mark: on startup, reject any `requested_at` earlier than the server boot time plus the window. This closes the restart window without persisting nonces.
- Track the last heartbeat `sequence` per module and log or flag regressions. Reboots reset it, so treat it as an anomaly signal, not a rejection.
- Add a nonce to `ProvisionCredential`.
- Sign the heartbeat response projection, `heartbeat_resp|{module_id}|{sequence}|{server_time}`, so the clock source is authenticated at the app layer, which is a prerequisite for R2.1.

*Skill:* proto-contract-change. *Hazard:* #4, #5 (new vectors).

### P2: resilience and physical security

**R2.1 Signed, encrypted offline allow-list (G4).** Depends on R1.4 (encrypted NVS) and R1.5 (authenticated clock).
- The server builds a per-module snapshot of credential hashes authorized for that module, with each entry's expiry. It adds a monotonic `snapshot_version` and signs the snapshot with an Ed25519 key that only the server holds. The public key is embedded in the firmware.
- The module requests a snapshot when the heartbeat response advertises a newer version. It verifies the signature, refuses any version lower than its current one, and stores it in encrypted NVS.
- **Hashing:** the module cannot hold `PORTUNUS_CREDENTIAL_HASH_SECRET` (it is server-only and permanent, hazard #11). So the snapshot uses a separate per-module key: `HMAC(K_module, rawUID)`, where `K_module` is derived server-side from a dedicated offline-cache secret and delivered over mTLS. A stolen module's snapshot then reveals nothing reusable on other modules or on the server DB.
- Offline grant rules: the module grants only while the snapshot is younger than a configured max age (for example 24 h), and only while its clock has been synced within that age. Every offline decision is queued, then replayed to the server with an `offline=true` flag when connectivity returns, and recorded in `access_events`.
- The lock stays fail-secure. An unknown card or a stale snapshot means deny.

*Skill:* credential-hashing (this adds a second, explicitly separate scheme that must never be confused with the canonical one; the skill and hazard #1 need updating to say so), access-control-change, proto-contract-change, firmware-component.
*Accept:*
- With the server stopped, a card authorized before the outage is granted and an unauthorized card is denied.
- A card revoked before the outage is denied once the module synced the post-revocation snapshot.
- A tampered or downgraded snapshot is rejected.
- Offline events appear server-side after reconnect.
- `TestAccess_FirmwareEnrolledCredential_IsGranted` still passes.

*Size:* 4 or more PRs. Write a design doc first.

**R2.2 Tamper input and zeroization (G5).**
- Add an enclosure switch on a GPIO behind a new `ITamperSensor` interface (nullptr means absent, so the capability is off).
- On trip: `memset_s`/`mbedtls_platform_zeroize` the in-RAM HMAC secret and mTLS key, set a persistent tamper flag in NVS, send a signed `TamperAlert` once connectivity allows, and refuse access decisions until the server clears the flag.
- With flash encryption absent, the keys on flash survive, so the docs must state that zeroization is meaningful only together with the documented eFuse step.

*Skill:* firmware-component, proto-contract-change.

**R2.3 Automated HIL and fault injection (G7).**
- Promote `task test:firmware:hardware` to a self-hosted runner on the bench Pi (the lovelace bench board).
- Add scenarios: drop and delay packets with `tc netem` between the module and server, inject malformed gRPC frames from a test client, stop the server mid-decision, and cut power during OTA through a relay on a GPIO of the bench host.

See the memories on the bench tier and the HW server matrix.
*Accept:* runs nightly, and failures open an issue.

### Documented only (by decision)

**D1 Root of trust procedure.**
Write `docs/hardware_root_of_trust.md`. It should cover:
- ESP32-S3 Secure Boot v2 (RSA-3072), with the signing key kept off the Pi and key-digest revocation slots planned.
- Flash encryption in Release mode.
- NVS encryption using the existing `nvs_keys` partition.
- Disabling ROM download mode (`SECURE_DOWNLOAD`), soft or hard JTAG disable, and the anti-rollback eFuse counter.

Add a `sdkconfig.defaults.prod-fused` overlay with those options, and a CI compile-only job that builds it and checks the result with `assert_secure_build.sh --fused`. This proves the config builds without anyone burning eFuses.
*Accept:* the overlay compiles in CI, and the procedure lists every irreversible step with a bold warning and a one-board-first rule.

---

## 5. Suggested sequence

1. R0.1, R0.2, R0.3, R0.4, R0.5. These are independent and can go in parallel PRs.
2. R0.6 and R1.1. They form one firewall story.
3. D1, then R1.4. OTA needs the signing story, and the partition reflash should happen once, together with the fused overlay.
4. R1.2 and R1.5.
5. R1.3.
6. R2.1 (design doc first), R2.2, R2.3.

After step 4, Portunus could credibly claim SL2 for conduit C2 and for the L1 component, with the remaining exceptions being the documented-only hardware items.
