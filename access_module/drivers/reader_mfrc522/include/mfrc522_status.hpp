/* Outcome of an MFRC522 Transceive, decoded from ComIrqReg and ErrorReg
 * (MFRC522 datasheet sections 9.3.1.5 and 9.3.1.7). Pure, so the unit tier
 * checks the decoding without SPI. Private to reader_mfrc522. */
#pragma once

#include "error_codes.hpp"

#include <stdint.h>

/* ComIrqReg bits. */
static constexpr uint8_t MFRC522_IRQ_RX    = 0x20;
static constexpr uint8_t MFRC522_IRQ_IDLE  = 0x10;
static constexpr uint8_t MFRC522_IRQ_ERR   = 0x02;
static constexpr uint8_t MFRC522_IRQ_TIMER = 0x01;

/** Any of these ends the Transceive wait loop. */
static constexpr uint8_t MFRC522_IRQ_DONE =
    MFRC522_IRQ_RX | MFRC522_IRQ_IDLE | MFRC522_IRQ_ERR | MFRC522_IRQ_TIMER;

/* ErrorReg bits. */
static constexpr uint8_t MFRC522_ERR_PROTOCOL = 0x01;
static constexpr uint8_t MFRC522_ERR_PARITY   = 0x02;
static constexpr uint8_t MFRC522_ERR_COLL     = 0x08;
static constexpr uint8_t MFRC522_ERR_BUF_OVFL = 0x10;

/** Receive errors other than a collision. */
static constexpr uint8_t MFRC522_ERR_READ =
    MFRC522_ERR_BUF_OVFL | MFRC522_ERR_PARITY | MFRC522_ERR_PROTOCOL;

/** Map a finished Transceive's IRQ and error registers to a result. A timer
 *  IRQ means no card answered. CollErr is checked on its own: when it was
 *  only consulted inside the other-errors branch, a bare collision passed
 *  this check and surfaced later as a BCC read error or a select nobody
 *  answered, never as a collision. */
constexpr portunus_err_t mfrc522_transceive_status(uint8_t com_irq, uint8_t error_reg)
{
    if (com_irq & MFRC522_IRQ_TIMER) {
        return PORTUNUS_ERR_NO_CREDENTIAL;
    }
    if (error_reg & MFRC522_ERR_COLL) {
        return PORTUNUS_ERR_CREDENTIAL_COLLISION;
    }
    if (error_reg & MFRC522_ERR_READ) {
        return PORTUNUS_ERR_CREDENTIAL_READ;
    }
    return PORTUNUS_OK;
}

/** VersionReg values that mean nothing is answering on SPI: a bus that
 *  floats or is pulled high reads 0xFF, one held low reads 0x00. Any real
 *  chip, clones included, reports something else. */
constexpr bool mfrc522_version_is_dead(uint8_t version)
{
    return version == 0x00 || version == 0xFF;
}
