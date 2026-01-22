#include "rfid_reader.h"
#include "esp_log.h"

static const char* TAG = "rfid_reader";

extern "C" void rfid_reader_init() {
    // Stub: later replace with MFRC522 / PN532 driver, SPI/I2C init, scan task, etc.
    ESP_LOGI(TAG, "rfid_reader_init (stub)");
}
