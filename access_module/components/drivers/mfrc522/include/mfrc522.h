/**
 * @file mfrc522.h
 * @brief MFRC522 RFID reader driver — public API.
 *
 * Low-level SPI driver for the NXP MFRC522 contactless reader IC.
 * Supports card detection, anti-collision, and UID extraction for
 * MIFARE cards with 4-byte and 7-byte UIDs.
 *
 * This is a *driver* component (lowest layer). It knows only about its
 * hardware and the SPI bus. The credential_reader_module (Phase 2) will
 * wrap this driver behind an abstract ICredentialReader interface.
 */

#pragma once

#include "credential_types.h"
#include "portunus_types.h"

#ifdef __cplusplus
extern "C" {
#endif

/**
 * @brief Initialise the MFRC522 driver.
 *
 * Configures the SPI bus and device, performs a soft reset, and sets up
 * default register values (gain, timer, CRC preset). Verifies
 * communication by reading the version register.
 *
 * @return PORTUNUS_OK on success, or an error code.
 */
portunus_err_t mfrc522_init(void);

/**
 * @brief Attempt to read a card UID from the reader field.
 *
 * Performs the REQA → anti-collision → select sequence. If a card is
 * present and successfully selected, its UID is written into @p cred.
 *
 * This is a synchronous, blocking call. Typical execution time is
 * under 10 ms for a single 4-byte UID card.
 *
 * @param[out] cred  Destination for the credential data.
 * @return PORTUNUS_OK if a card was read successfully.
 * @return PORTUNUS_ERR_NO_CARD if no card is present.
 * @return PORTUNUS_ERR_CARD_COLLISION on anti-collision failure.
 * @return PORTUNUS_ERR_CARD_READ on other read failures.
 */
portunus_err_t mfrc522_read_card(credential_t *cred);

/**
 * @brief Read the MFRC522 hardware version register.
 *
 * Returns the value of VersionReg (0x37). Common values:
 *   - 0x91 = MFRC522 v1.0
 *   - 0x92 = MFRC522 v2.0
 *   - 0x88 = FM17522 clone
 *   - 0x00 or 0xFF = no communication / wiring fault
 *
 * @return The raw version byte.
 */
uint8_t mfrc522_get_version(void);

/**
 * @brief Send HLTA command to put the current card into HALT state.
 *
 * After halting, the same card will not respond to REQA until it
 * leaves and re-enters the field, preventing duplicate reads.
 */
void mfrc522_halt_card(void);

/**
 * @brief Turn the MFRC522 antenna on.
 */
void mfrc522_antenna_on(void);

/**
 * @brief Turn the MFRC522 antenna off.
 */
void mfrc522_antenna_off(void);

#ifdef __cplusplus
}
#endif