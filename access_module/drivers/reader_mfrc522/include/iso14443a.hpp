/* ISO/IEC 14443-3 Type A card selection (REQA, anticollision, select, HLTA),
 * independent of the reader chip. The chip is reached only through
 * iso14443a_io_t, so the unit tier drives this code against a scripted card.
 * No ESP-IDF or FreeRTOS dependency. Private to reader_mfrc522. */
#pragma once

#include "credential_types.h"
#include "portunus_types.hpp"

#include <stdint.h>

/* PICC commands (ISO/IEC 14443-3 section 6.4). */
static constexpr uint8_t PICC_REQA        = 0x26;
static constexpr uint8_t PICC_SEL_CL1     = 0x93;
static constexpr uint8_t PICC_SEL_CL2     = 0x95;
static constexpr uint8_t PICC_SEL_CL3     = 0x97;
static constexpr uint8_t PICC_HLTA        = 0x50;
static constexpr uint8_t PICC_CASCADE_TAG = 0x88;

/** NVB values: SEL plus NVB only (anticollision), and the full select frame. */
static constexpr uint8_t PICC_NVB_ANTICOLL = 0x20;
static constexpr uint8_t PICC_NVB_SELECT   = 0x70;

/** SAK b3 (0x04): UID not complete, the next cascade level follows. */
static constexpr uint8_t PICC_SAK_CASCADE = 0x04;

/** Chip operations the protocol needs. mfrc522_hal.cpp implements them over
 *  SPI; the unit tier implements them with a scripted card. */
struct iso14443a_io_t {
    void *ctx;

    /** Send tx_len bytes (tx_last_bits valid bits in the last byte, 0 means
     *  all 8) and receive up to *rx_len bytes into rx; *rx_len becomes the
     *  number received. rx may be NULL when no reply is wanted. Returns
     *  PORTUNUS_ERR_NO_CREDENTIAL when nothing answered before the timeout. */
    portunus_err_t (*transceive)(void *ctx, const uint8_t *tx, uint8_t tx_len,
                                 uint8_t tx_last_bits, uint8_t *rx, uint8_t *rx_len);

    /** CRC_A of data (ISO/IEC 14443-3 Annex B), low byte first in out. */
    portunus_err_t (*crc_a)(void *ctx, const uint8_t *data, uint8_t len, uint8_t out[2]);
};

/**
 * Detect one card and read its UID: REQA, then anticollision and select for
 * each cascade level. On success writes the UID and its length to *cred; on
 * any failure *cred is left zeroed.
 *
 * @return PORTUNUS_OK, PORTUNUS_ERR_NO_CREDENTIAL (no card),
 *         PORTUNUS_ERR_CREDENTIAL_COLLISION, PORTUNUS_ERR_CREDENTIAL_READ,
 *         PORTUNUS_ERR_INVALID_ARG (cred is NULL), or a chip error passed
 *         through from io.
 */
portunus_err_t iso14443a_read_uid(const iso14443a_io_t &io, credential_t *cred);

/** Send HLTA so the selected card stays silent until it leaves the field.
 *  The card does not answer HLTA, so there is nothing to report. */
void iso14443a_halt(const iso14443a_io_t &io);
