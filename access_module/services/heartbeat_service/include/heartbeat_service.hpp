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

#include "portunus_types.hpp"

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
 * Asks the task to stop and waits for it to finish its current heartbeat
 * and exit; it is never deleted mid-publish. A later start begins again at
 * sequence 0. Safe to call when not running.
 *
 * @return PORTUNUS_OK once stopped (or if not running), or
 *         PORTUNUS_ERR_TIMEOUT if the task did not exit in time; it is then
 *         left running and stop may be called again.
 */
portunus_err_t heartbeat_service_stop(void);

#ifdef __cplusplus
}
#endif