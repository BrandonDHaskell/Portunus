/* Canonical string the server signs for an AccessResponse and the module
 * verifies before acting on the decision (x-portunus-sig trailer). Must stay
 * byte-identical to accessResponseProjection() in
 * server/internal/grpcapi/interceptors.go (CLAUDE.md hazard #5); both sides
 * test the same golden vector. No ESP-IDF dependency: the unit tier links it. */
#pragma once

#include <stdbool.h>
#include <stddef.h>

/**
 * Write "access_resp|{module_id}|{credential_id}|{nonce_hex}|{1 or 0}".
 *
 * The request's nonce binds the signature to that one request, so a captured
 * signed grant cannot be replayed for a later read of the same card. The
 * "access_resp" prefix keeps a response signature from ever matching a
 * request projection ("access|...") signed with the same shared secret.
 *
 * @return false (and an empty string when cap > 0) if any argument is NULL
 *         or the projection does not fit in cap bytes; the caller must deny.
 */
bool access_response_projection(char *buf, size_t cap, const char *module_id,
                                const char *credential_id, const char *nonce_hex,
                                bool granted);
