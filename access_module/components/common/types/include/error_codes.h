/**
 * @file error_codes.h
 * @brief System-wide error codes for the Portunus system.
 *
 * Error codes are grouped by subsystem using range offsets so that the
 * originating component can be identified from the code alone.
 */

#pragma once

#include "portunus_types.h"

#ifdef __cplusplus
extern "C" {
#endif

/* ── Base offsets per subsystem ─────────────────────────────────────────────── */
#define PORTUNUS_ERR_BASE_DRIVER     0x1000
#define PORTUNUS_ERR_BASE_SERVICE    0x2000
#define PORTUNUS_ERR_BASE_MODULE     0x3000

/* ── Driver errors (MFRC522, door strike, reed switch, LED) ────────────────── */
#define PORTUNUS_ERR_SPI_INIT        (PORTUNUS_ERR_BASE_DRIVER + 0x01)  /**< SPI bus initialisation failed */
#define PORTUNUS_ERR_SPI_TRANSFER    (PORTUNUS_ERR_BASE_DRIVER + 0x02)  /**< SPI read/write transfer failed */
#define PORTUNUS_ERR_DEVICE_NOT_FOUND (PORTUNUS_ERR_BASE_DRIVER + 0x03) /**< Expected hardware not detected */
#define PORTUNUS_ERR_CARD_READ       (PORTUNUS_ERR_BASE_DRIVER + 0x04)  /**< Failed to read card UID */
#define PORTUNUS_ERR_CARD_COLLISION  (PORTUNUS_ERR_BASE_DRIVER + 0x05)  /**< Anti-collision failure (multiple cards) */
#define PORTUNUS_ERR_NO_CARD         (PORTUNUS_ERR_BASE_DRIVER + 0x06)  /**< No card present in reader field */

/* ── Service errors (event bus, heartbeat, connectivity) ───────────────────── */
#define PORTUNUS_ERR_QUEUE_FULL      (PORTUNUS_ERR_BASE_SERVICE + 0x01) /**< Event bus queue is full */
#define PORTUNUS_ERR_QUEUE_CREATE    (PORTUNUS_ERR_BASE_SERVICE + 0x02) /**< Failed to create FreeRTOS queue */
#define PORTUNUS_ERR_SUBSCRIBE       (PORTUNUS_ERR_BASE_SERVICE + 0x03) /**< Subscriber registration failed */
#define PORTUNUS_ERR_MAX_SUBSCRIBERS (PORTUNUS_ERR_BASE_SERVICE + 0x04) /**< Subscriber table is full */
#define PORTUNUS_ERR_TASK_CREATE     (PORTUNUS_ERR_BASE_SERVICE + 0x05) /**< Failed to create FreeRTOS task */
#define PORTUNUS_ERR_ALREADY_INIT    (PORTUNUS_ERR_BASE_SERVICE + 0x06) /**< Component already initialised */
#define PORTUNUS_ERR_NOT_INIT        (PORTUNUS_ERR_BASE_SERVICE + 0x07) /**< Component not yet initialised */

/* ── Module errors (credential reader, access point, feedback) ─────────────── */
#define PORTUNUS_ERR_INVALID_ARG     (PORTUNUS_ERR_BASE_MODULE + 0x01)  /**< NULL pointer or out-of-range argument */
#define PORTUNUS_ERR_TIMEOUT         (PORTUNUS_ERR_BASE_MODULE + 0x02)  /**< Operation timed out */

#ifdef __cplusplus
}
#endif