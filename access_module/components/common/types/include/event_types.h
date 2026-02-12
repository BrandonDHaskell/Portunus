/**
 * @file event_types.h
 * @brief Typed event IDs and payload structures for the Portunus event bus.
 *
 * Every event that flows through the event bus has a typed ID and an
 * associated payload struct. Events are fixed-size so they can be copied
 * into FreeRTOS queues by value without heap allocation.
 */

#pragma once

#include <stdint.h>
#include "credential_types.h"

#ifdef __cplusplus
extern "C" {
#endif

/* ── Event IDs ─────────────────────────────────────────────────────────────── */

/**
 * @brief Event type identifiers.
 *
 * Grouped by subsystem. New IDs should be appended within their group
 * to preserve backwards compatibility with any logged event traces.
 */
typedef enum {
    /* System events: 0x00xx */
    EVENT_NONE = 0x0000,               /**< Sentinel / invalid event */
    EVENT_SYSTEM_BOOT_COMPLETE,        /**< Startup sequence finished */

    /* Credential events: 0x01xx */
    EVENT_CREDENTIAL_READ = 0x0100,    /**< Card UID successfully read */
    EVENT_CREDENTIAL_READ_ERROR,       /**< Card read attempted but failed */

    /* Heartbeat events: 0x02xx */
    EVENT_HEARTBEAT = 0x0200,          /**< Periodic health tick */

    /* Access events: 0x03xx  (Phase 2+) */
    EVENT_ACCESS_GRANTED = 0x0300,     /**< Server granted access */
    EVENT_ACCESS_DENIED,               /**< Server denied access */
} portunus_event_id_t;

/* ── Event payloads ────────────────────────────────────────────────────────── */

/**
 * @brief Payload for EVENT_CREDENTIAL_READ.
 */
typedef struct {
    credential_t credential;           /**< The credential that was read */
    int64_t      timestamp_ms;         /**< Reading timestamp (esp_timer) */
} event_credential_read_t;

/**
 * @brief Payload for EVENT_HEARTBEAT.
 */
typedef struct {
    uint32_t sequence;                 /**< Monotonic heartbeat counter */
    uint32_t uptime_sec;               /**< Seconds since boot */
    uint32_t free_heap_bytes;          /**< Free heap at time of heartbeat */
} event_heartbeat_t;

/* ── Generic event envelope ────────────────────────────────────────────────── */

/**
 * @brief Fixed-size event envelope passed through the event bus queue.
 *
 * The union keeps all payloads in a single flat structure so that events
 * can be copied into a FreeRTOS queue by value (xQueueSend) with no
 * dynamic allocation.
 */
typedef struct {
    portunus_event_id_t id;            /**< Which event this is */
    union {
        event_credential_read_t credential_read;
        event_heartbeat_t       heartbeat;
    } payload;
} portunus_event_t;

#ifdef __cplusplus
}
#endif