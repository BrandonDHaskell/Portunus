/**
 * @file timing_config.h
 * @brief Timing constants for the Portunus door access module.
 *
 * All values are sourced from Kconfig so that timing can be tuned via
 * menuconfig or sdkconfig overlays without editing code.
 */

#pragma once

#include "sdkconfig.h"

#ifdef __cplusplus
extern "C" {
#endif

/* ── Heartbeat ─────────────────────────────────────────────────────────────── */
#define HEARTBEAT_INTERVAL_MS       CONFIG_PORTUNUS_HEARTBEAT_INTERVAL_MS

/* ── MFRC522 card polling ──────────────────────────────────────────────────── */
#define MFRC522_POLL_INTERVAL_MS    CONFIG_PORTUNUS_MFRC522_POLL_INTERVAL_MS

/* ── Card re-read debounce ─────────────────────────────────────────────────── */
#define CARD_REREAD_DELAY_MS        CONFIG_PORTUNUS_CARD_REREAD_DELAY_MS

/* ── Event bus ─────────────────────────────────────────────────────────────── */
#define EVENT_QUEUE_TIMEOUT_MS      CONFIG_PORTUNUS_EVENT_QUEUE_TIMEOUT_MS
#define EVENT_QUEUE_LENGTH          CONFIG_PORTUNUS_EVENT_QUEUE_LENGTH
#define MAX_EVENT_SUBSCRIBERS       CONFIG_PORTUNUS_MAX_EVENT_SUBSCRIBERS

#ifdef __cplusplus
}
#endif