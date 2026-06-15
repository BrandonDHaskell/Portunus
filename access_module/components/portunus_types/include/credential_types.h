/**
 * @file credential_types.h
 * @brief Credential data structures for the Portunus system.
 *
 * MIFARE UIDs can be 4, 7, or 10 bytes. The credential structure stores
 * the raw bytes and actual length so that all UID sizes are handled
 * uniformly throughout the system.
 */

#pragma once

#include <stdint.h>
#include <stddef.h>

#ifdef __cplusplus
extern "C" {
#endif

/** @brief Maximum UID length in bytes (10-byte triple-size MIFARE UID). */
#define CREDENTIAL_UID_MAX_LEN  10

/**
 * @brief Raw credential read from an RFID card.
 */
typedef struct {
    uint8_t uid[CREDENTIAL_UID_MAX_LEN];  /**< Raw UID bytes */
    uint8_t uid_len;                       /**< Actual UID length (4, 7, or 10) */
} credential_t;

/** Buffer size sufficient for any UID formatted as "XX:XX:...:XX\0". */
#define CREDENTIAL_UID_HEX_STR_LEN  (CREDENTIAL_UID_MAX_LEN * 3 + 1)

/** Buffer size for the 8-character FNV-1a log fingerprint plus NUL. */
#define CREDENTIAL_LOG_ID_LEN  9

/**
 * @brief Format a credential UID as a colon-separated hex string.
 *
 *   {0x04, 0xA3, 0x2B}  →  "04:A3:2B"
 *
 * @param cred    Source credential.
 * @param buf     Destination buffer (recommend CREDENTIAL_UID_HEX_STR_LEN).
 * @param buf_len Size of buf in bytes.
 */
void credential_uid_to_hex(const credential_t *cred, char *buf, size_t buf_len);

/**
 * @brief Produce a short, non-reversible log fingerprint of a credential UID.
 *
 * Computes FNV-1a 32-bit over the raw UID bytes and formats the result as
 * 8 lowercase hex characters.  Suitable for log correlation without exposing
 * the raw UID.  Buf must be at least CREDENTIAL_LOG_ID_LEN bytes.
 *
 * @param cred    Source credential.
 * @param buf     Destination buffer (recommend CREDENTIAL_LOG_ID_LEN).
 * @param buf_len Size of buf in bytes.
 */
void credential_uid_to_log_id(const credential_t *cred, char *buf, size_t buf_len);

#ifdef __cplusplus
}
#endif