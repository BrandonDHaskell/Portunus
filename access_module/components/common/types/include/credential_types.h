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

#ifdef __cplusplus
}
#endif