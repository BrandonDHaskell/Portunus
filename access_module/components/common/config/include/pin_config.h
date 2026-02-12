/**
 * @file pin_config.h
 * @brief GPIO pin assignments for the Portunus door access module.
 *
 * All values are sourced from Kconfig (see components/common/config/Kconfig)
 * so that dev/prod differences are managed through sdkconfig overlays
 * rather than scattered #defines.
 *
 * Default pin mapping (ESP32-S3 WROOM-1, development breadboard):
 *
 *   MFRC522 Pin  │ ESP32-S3 GPIO
 *   ─────────────┼──────────────
 *   SDA (CS)     │ GPIO 10
 *   SCK          │ GPIO 12
 *   MOSI         │ GPIO 11
 *   MISO         │ GPIO 13
 *   RST          │ GPIO  9
 *   3.3V         │ 3V3
 *   GND          │ GND
 */

#pragma once

#include "sdkconfig.h"

#ifdef __cplusplus
extern "C" {
#endif

/* ── SPI bus pins (MFRC522) ────────────────────────────────────────────────── */
#define PIN_SPI_MOSI       CONFIG_PORTUNUS_SPI_MOSI_PIN
#define PIN_SPI_MISO       CONFIG_PORTUNUS_SPI_MISO_PIN
#define PIN_SPI_SCLK       CONFIG_PORTUNUS_SPI_SCLK_PIN
#define PIN_MFRC522_CS     CONFIG_PORTUNUS_SPI_CS_PIN
#define PIN_MFRC522_RST    CONFIG_PORTUNUS_MFRC522_RST_PIN

/* ── SPI host selection ────────────────────────────────────────────────────── */
/** Use SPI2_HOST (the first general-purpose SPI peripheral on ESP32-S3). */
#define MFRC522_SPI_HOST   SPI2_HOST

#ifdef __cplusplus
}
#endif