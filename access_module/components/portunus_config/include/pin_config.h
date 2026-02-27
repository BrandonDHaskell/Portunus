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
 *   MOSI         │ GPIO 37
 *   MISO         │ GPIO 38
 *   SCK          │ GPIO 36
 *   SDA (CS)     │ GPIO 35
 *   RST          │ GPIO  4
 *   3.3V         │ 3V3
 *   GND          │ GND
 *
 * These defaults match the Kconfig values in main/Kconfig.projbuild.
 * Override via `idf.py menuconfig` → Portunus Configuration → SPI Pin
 * Assignments.
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

/* ── Door hardware pins ───────────────────────────────────────────────────── */
#ifdef CONFIG_PORTUNUS_ENABLE_DOOR_STRIKE
#define PIN_DOOR_STRIKE         CONFIG_PORTUNUS_DOOR_STRIKE_PIN
/*
 * ESP-IDF only defines CONFIG_* bool macros when enabled (set to 'y').
 * When disabled, the macro is undefined (not 0). We therefore normalize
 * to a numeric 0/1 macro that can be used in C/C++ expressions.
 */
#if CONFIG_PORTUNUS_DOOR_STRIKE_ACTIVE_LOW
#define DOOR_STRIKE_ACTIVE_LOW  1
#else
#define DOOR_STRIKE_ACTIVE_LOW  0
#endif
#endif

#ifdef CONFIG_PORTUNUS_ENABLE_REED_SWITCH
#define PIN_REED_SWITCH         CONFIG_PORTUNUS_REED_SWITCH_PIN
#if CONFIG_PORTUNUS_REED_SWITCH_NC
#define REED_SWITCH_NC          1
#else
#define REED_SWITCH_NC          0
#endif
#endif

/* ── Feedback pins ────────────────────────────────────────────────────────── */
#ifdef CONFIG_PORTUNUS_ENABLE_LED
#define PIN_STATUS_LED          CONFIG_PORTUNUS_LED_PIN
#endif

#ifdef __cplusplus
}
#endif