/**
 * @file i_credential_reader.h
 * @brief Abstract interface for credential reading hardware.
 *
 * The FSM and event system program against this interface, never
 * against a specific driver (MFRC522, PN532, etc.).  Concrete
 * implementations wrap a hardware driver and live alongside this
 * header in the credential_reader_module component.
 *
 * Current implementation: Mfrc522CredentialReader (wraps mfrc522 driver).
 *
 * Architecture layer: Module (see project plan §3.1–3.2).
 */

#pragma once

#include "credential_types.h"
#include "portunus_types.h"

/**
 * @brief Abstract credential reader interface.
 *
 * All methods are synchronous.  The FSM calls read() from a dedicated
 * polling task so that SPI I/O does not block event processing.
 */
class ICredentialReader {
public:
    virtual ~ICredentialReader() = default;

    /**
     * @brief Initialise the underlying reader hardware.
     *
     * @return PORTUNUS_OK on success, or a driver-specific error code.
     */
    virtual portunus_err_t init() = 0;

    /**
     * @brief Attempt to read a credential from the reader field.
     *
     * Performs whatever hardware-specific sequence is required (e.g.,
     * REQA → anti-collision → select for ISO 14443A).  If a credential
     * is present and successfully read, it is written into @p cred.
     *
     * @param[out] cred  Destination for the credential data.
     * @return PORTUNUS_OK           Credential read successfully.
     * @return PORTUNUS_ERR_NO_CARD  No credential present in the field.
     * @return Other                 Hardware-specific read error.
     */
    virtual portunus_err_t read(credential_t *cred) = 0;

    /**
     * @brief Halt / deselect the current credential.
     *
     * Prevents the same credential from being re-read on the next
     * poll cycle.  The credential must leave and re-enter the field
     * before it will be detected again.
     */
    virtual void halt() = 0;
};
