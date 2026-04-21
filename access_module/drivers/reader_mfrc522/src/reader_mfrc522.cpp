/**
 * @file reader_mfrc522.cpp
 * @brief ICredentialReader implementation using the MFRC522 RFID IC.
 *
 * Delegates directly to the internal mfrc522 HAL functions.  This file
 * exists to implement the virtual interface — all hardware logic lives
 * in mfrc522_hal.cpp.
 */

#include "reader_mfrc522.h"
#include "mfrc522.h"       /* internal HAL */

#include "esp_log.h"

static const char *TAG = "reader_mfrc522";

portunus_err_t ReaderMfrc522::init()
{
    ESP_LOGI(TAG, "Initialising MFRC522 credential reader");
    return mfrc522_init();
}

portunus_err_t ReaderMfrc522::read(credential_t *cred)
{
    return mfrc522_read_credential(cred);
}

void ReaderMfrc522::halt()
{
    mfrc522_halt_credential();
}
