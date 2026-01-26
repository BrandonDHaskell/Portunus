#pragma once

#include <cstdint>

extern "C" {
#include "freertos/FreeRTOS.h"
#include "freertos/queue.h"
#include "esp_err.h"
}

#include "mfrc522.h"

namespace portunus {

struct RfidEvent {
  RfidUid uid;
  uint32_t uptime_ms = 0;
};

struct RfidReaderConfig {
  spi_host_device_t spi_host = SPI2_HOST;

  Mfrc522Pins pins{
    .cs   = GPIO_NUM_35,
    .sck  = GPIO_NUM_36,
    .mosi = GPIO_NUM_37,
    .miso = GPIO_NUM_38,
    .rst  = GPIO_NUM_4,
  };

  int spi_clock_hz = 2'000'000;
  uint32_t poll_ms = 50;
  uint32_t dedupe_window_ms = 1000; // don't spam same UID while card stays present
};

// Returns a FreeRTOS queue handle that emits RfidEvent.
esp_err_t rfid_reader_start(const RfidReaderConfig& cfg, QueueHandle_t* out_queue);

} // namespace portunus
