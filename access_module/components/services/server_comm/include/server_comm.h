/**
 * @file server_comm.h
 * @brief Server communication component for the Portunus access module.
 *
 * Bridges the local event bus to the Portunus server over HTTP/1.1 with
 * Nanopb-encoded protobuf payloads.
 *
 * Architecture:
 *   - A dedicated FreeRTOS task owns an internal queue.
 *   - Event bus subscriber callbacks (non-blocking) copy events into
 *     this queue.
 *   - The task dequeues events, checks wifi_mgr_is_connected(), encodes
 *     the protobuf request, performs the HTTP POST, decodes the response,
 *     and publishes access decision events back to the event bus.
 *
 * Handles two event types:
 *   EVENT_HEARTBEAT       → POST /v1/heartbeat       → log result
 *   EVENT_CREDENTIAL_READ → POST /v1/access_request   → publish
 *                           EVENT_ACCESS_GRANTED or EVENT_ACCESS_DENIED
 *
 * Call server_comm_init() after event_bus_init() and wifi_mgr_init().
 */

#pragma once

#include "portunus_types.h"

#ifdef __cplusplus
extern "C" {
#endif

/**
 * @brief Initialise and start the server communication component.
 *
 * Creates the internal queue, registers event bus subscribers for
 * EVENT_HEARTBEAT and EVENT_CREDENTIAL_READ, and starts the network
 * task.
 *
 * @return PORTUNUS_OK on success.
 *         PORTUNUS_ERR_ALREADY_INIT if called more than once.
 *         PORTUNUS_ERR_QUEUE_CREATE if queue allocation failed.
 *         PORTUNUS_ERR_TASK_CREATE  if task creation failed.
 */
portunus_err_t server_comm_init(void);

#ifdef __cplusplus
}
#endif