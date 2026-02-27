/**
 * @file heartbeat_service.h
 * @brief Periodic heartbeat service.
 *
 * Publishes EVENT_HEARTBEAT to the event bus at a configurable interval
 * (HEARTBEAT_INTERVAL_MS from timing_config.h). Each heartbeat carries
 * a monotonic sequence number, uptime, and free heap — enough telemetry
 * for the MVP. Server transmission is added in Phase 3.
 */

#pragma once

#include "portunus_types.h"

#ifdef __cplusplus
extern "C" {
#endif

/**
 * @brief Start the heartbeat service.
 *
 * Creates a FreeRTOS task that periodically publishes heartbeat events.
 * The event bus must be initialised before calling this function.
 *
 * @return PORTUNUS_OK on success, or an error code.
 */
portunus_err_t heartbeat_service_start(void);

/**
 * @brief Stop the heartbeat service.
 *
 * Deletes the heartbeat task. Safe to call if the service is not running.
 */
void heartbeat_service_stop(void);

#ifdef __cplusplus
}
#endif