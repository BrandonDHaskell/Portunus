/**
 * @file server_comm.h
 * @brief Server communication component for the Portunus access module.
 *
 * Bridges the local event bus to the Portunus server over gRPC (HTTP/2+TLS)
 * with Nanopb-encoded protobuf payloads.
 *
 * Architecture:
 *   - A dedicated FreeRTOS task owns an internal queue.
 *   - Event bus subscriber callbacks (non-blocking) copy events into
 *     this queue.
 *   - The task dequeues events, checks wifi_mgr_is_connected(), encodes
 *     the protobuf request, performs the gRPC call, decodes the response,
 *     and publishes access decision events back to the event bus.
 *
 * Handles two event types:
 *   EVENT_HEARTBEAT       → SendHeartbeat RPC      → sync clock, log result
 *   EVENT_CREDENTIAL_READ → RequestAccess RPC      → publish
 *                           EVENT_ACCESS_GRANTED or EVENT_ACCESS_DENIED
 *
 * Clock synchronisation: each successful heartbeat response carries a
 * server_time field (RFC 3339 UTC).  server_comm applies an RTT/2 correction
 * and calls settimeofday() so the module has a usable wall-clock time.
 * server_comm_clock_synced() returns true once the first sync has succeeded.
 *
 * Call server_comm_init() after event_bus_init() and wifi_mgr_init().
 */

#pragma once

#include "portunus_types.hpp"

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

/**
 * @brief Return true if the module clock has been synchronised at least once.
 *
 * False at boot and until the first successful heartbeat response is decoded
 * and settimeofday() succeeds.  Used by the access path to decide whether
 * to populate AccessRequest.requested_at for replay protection.
 */
bool server_comm_clock_synced(void);

/**
 * @brief Stop the server communication component and release resources.
 *
 * Deletes the network task and drains/deletes the internal queue.
 * After this call no further events will be forwarded to the server
 * until server_comm_init() is called again.
 *
 * Safe to call if the component is not initialised (no-op).
 */
void server_comm_deinit(void);

#ifdef __cplusplus
}
#endif