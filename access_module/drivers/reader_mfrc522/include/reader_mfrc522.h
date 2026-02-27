/**
 * @file reader_mfrc522.h
 * @brief ICredentialReader implementation using the NXP MFRC522 RFID IC.
 *
 * Wraps the SPI-based MFRC522 HAL behind the standard ICredentialReader
 * interface.  The FSM and event system see only this header — the
 * register-level SPI driver is internal to this component.
 *
 * Interface: ICredentialReader (portunus_interfaces)
 */

#pragma once

#include "i_credential_reader.h"

/**
 * @brief Concrete credential reader backed by the MFRC522 RFID IC.
 */
class ReaderMfrc522 : public ICredentialReader {
public:
    ReaderMfrc522() = default;

    portunus_err_t init() override;
    portunus_err_t read(credential_t *cred) override;
    void           halt() override;
};
