/* Device-protocol logic of server_comm, kept free of I/O so the unit tier can
 * test it: the HMAC projections both ends sign (CLAUDE.md hazard #5),
 * verification of the server's access-decision signature, mapping server
 * replies to firmware events, and parsing / formatting wire timestamps.
 *
 * No ESP-IDF, FreeRTOS, mbedTLS or Nanopb dependency: the HMAC arrives as a
 * function pointer and proto enum values as plain integers (server_comm.cpp
 * static_asserts them against the generated Nanopb enums). */
#pragma once

#include "event_types.hpp"

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>
#include <sys/time.h>

/* ── HMAC projections ────────────────────────────────────────────────────────
 * Each must stay byte-identical to the server: hmacProjection() in
 * server/internal/grpcapi/interceptors.go for requests, and
 * server/internal/devicesig for the access-decision signature. All return
 * false and leave "" in buf (cap > 0) on a NULL argument or when the result
 * does not fit: never sign or verify a truncated string. */

/** "heartbeat|{module_id}|{sequence}" */
bool proto_heartbeat_projection(char *buf, size_t cap, const char *module_id, uint32_t sequence);

/** "access|{module_id}|{credential_id}|{nonce_hex}|{requested_at}"
 *  (requested_at may be empty before the clock is synced). */
bool proto_access_projection(char *buf, size_t cap, const char *module_id,
                             const char *credential_id, const char *nonce_hex,
                             const char *requested_at);

/** "provision|{module_id}|{lowercase hex of uid}" */
bool proto_provision_projection(char *buf, size_t cap, const char *module_id,
                                const uint8_t *uid, size_t uid_len);

/** "access_resp|{module_id}|{credential_id}|{nonce_hex}|{1 or 0}"
 *
 * The request's nonce binds the signature to that one request, so a captured
 * signed grant cannot be replayed for a later read of the same card. The
 * "access_resp" prefix keeps a response signature from ever matching a
 * request projection signed with the same shared secret. */
bool access_response_projection(char *buf, size_t cap, const char *module_id,
                                const char *credential_id, const char *nonce_hex,
                                bool granted);

/** Lowercase hex of n bytes into out (2n + 1 bytes needed). False if it
 *  does not fit, leaving "" (cap > 0). */
bool proto_hex_lower(const uint8_t *bytes, size_t n, char *out, size_t cap);

/* ── Access-decision signature ───────────────────────────────────────────── */

/** Buffer access_check_response_sig uses for the response projection;
 *  server_comm.cpp asserts it holds the longest one Nanopb allows. */
static constexpr size_t PROTO_RESPONSE_PROJECTION_CAP = 160;

/** Length of an HMAC-SHA256 as lowercase hex, without the NUL. */
static constexpr size_t PROTO_HMAC_HEX_CHARS = 64;

/** Writes HMAC-SHA256(secret, data) as 64 lowercase hex chars plus NUL.
 *  server_comm passes an mbedTLS implementation; tests pass a fake. */
typedef bool (*proto_hmac_fn)(const uint8_t *data, size_t len,
                              char out_hex[PROTO_HMAC_HEX_CHARS + 1], void *ctx);

/** Constant-time comparison of two signatures; both must be exactly 64
 *  chars. Inspects all 64 positions whatever the first mismatch. */
bool proto_sig_hex_equal(const char *a, const char *b);

/**
 * Check the server's signature on an access decision before acting on it.
 *
 * @param received_sig  x-portunus-sig trailer value; NULL or "" if absent.
 * @return NULL if the signature verifies, otherwise the deny reason to
 *         report: "missing_response_sig", "invalid_response_sig" or
 *         "sig_compute_error". The firmware must deny on any non-NULL.
 */
const char *access_check_response_sig(proto_hmac_fn hmac, void *hmac_ctx,
                                      const char *received_sig, const char *module_id,
                                      const char *credential_id, const char *nonce_hex,
                                      bool granted);

/* ── Server replies to firmware events ───────────────────────────────────── */

/** Fill an access-decision payload. credential_id must be the credential's
 *  log fingerprint, never its raw or hex UID; reason is truncated to fit. */
void proto_fill_decision(event_access_decision_t *out, const char *log_id,
                         bool granted, bool known, const char *reason);

/** ProvisionStatus values from portunus.proto (append-only, so fixed). */
enum proto_provision_status_t : int {
    PROTO_PROVISION_UNSPECIFIED        = 0,
    PROTO_PROVISION_DUPLICATE_ACTIVE   = 2,
    PROTO_PROVISION_DUPLICATE_INACTIVE = 3,
    PROTO_PROVISION_DUPLICATE_PENDING  = 4,
    PROTO_PROVISION_UNAUTHORIZED       = 5,
    PROTO_PROVISION_PENDING_CREATED    = 7,
};

/** Map a ProvisionCredentialResponse.status to the firmware reason; anything
 *  unrecognised (including UNSPECIFIED and the retired 1 and 6) is
 *  PROVISION_RESULT_COMM_ERROR. */
provision_result_reason_t proto_provision_reason(int status);

/* ── Wire timestamps ─────────────────────────────────────────────────────── */

/** Parse the server's RFC 3339 UTC time ("2026-01-02T03:04:05.123456789Z",
 *  fraction of 1 to 9 digits optional) into seconds since the epoch and
 *  microseconds. Strict: rejects offsets, a missing "Z", out-of-range
 *  fields and trailing bytes. Independent of the process TZ. */
bool proto_parse_server_time(const char *s, struct timeval *out);

/** Add half the round-trip to a server timestamp, carrying microseconds. */
void proto_add_half_rtt(struct timeval *tv, int64_t rtt_us);

/** Format requested_at as "YYYY-MM-DDTHH:MM:SS.uuuuuuZ" (UTC). False if it
 *  does not fit (28 bytes with NUL), leaving "" (cap > 0). */
bool proto_format_requested_at(char *buf, size_t cap, const struct timeval *tv);
