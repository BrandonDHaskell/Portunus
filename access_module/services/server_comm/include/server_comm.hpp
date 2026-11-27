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
 * Handles:
 *   EVENT_HEARTBEAT         → SendHeartbeat RPC       → sync clock, log result
 *   EVENT_CREDENTIAL_READ   → RequestAccess RPC       → publish
 *                             EVENT_ACCESS_GRANTED or EVENT_ACCESS_DENIED
 *                             (ACCESS_POINT; every read gets exactly one decision)
 *   EVENT_PROVISION_REQUEST → ProvisionCredential RPC → publish provision result
 *                             (PROVISIONING_CONSOLE)
 *
 * Clock synchronisation: each successful heartbeat response carries a
 * server_time field (RFC 3339 UTC).  server_comm applies an RTT/2 correction
 * and calls settimeofday() so the module has a usable wall-clock time.  Before
 * an access request, if the clock was never synced or the last sync is over an
 * hour old, server_comm sends a heartbeat first, because the server rejects
 * access requests without a current requested_at.
 * server_comm_clock_synced() returns true once the first sync has succeeded.
 *
 * Call server_comm_init() after event_bus_init() and wifi_mgr_init().
 */

#pragma once

#include "portunus_types.hpp"
#include "portunus_nvs.hpp"

#ifdef __cplusplus
extern "C" {
#endif

/**
 * @brief Event bus subscriptions server_comm_init() makes, in either variant:
 * heartbeat, reader fault, reader recovery, and the variant's request event
 * (credential read or provision request). main.cpp checks the subscriber
 * budget against it (COMP-01); server_comm.cpp asserts it matches its list.
 */
#define SERVER_COMM_SUBSCRIPTION_COUNT 4

/**
 * @brief Initialise and start the server communication component.
 *
 * Creates the internal queue, registers event bus subscribers for
 * EVENT_HEARTBEAT and EVENT_CREDENTIAL_READ, and starts the network
 * task.
 *
 * Deployment values (module_id, server_host, grpc_port, hmac_secret) are
 * taken from @p cfg rather than Kconfig so that they can be stored in NVS
 * instead of baked into the firmware binary.
 *
 * @param cfg  Device configuration loaded from NVS.
 * @return PORTUNUS_OK on success.
 *         PORTUNUS_ERR_ALREADY_INIT if called more than once.
 *         PORTUNUS_ERR_QUEUE_CREATE if queue allocation failed.
 *         PORTUNUS_ERR_SUBSCRIBE    if an event bus subscription failed.
 *         PORTUNUS_ERR_TASK_CREATE  if task creation failed.
 *         PORTUNUS_FAIL             if HMAC is enabled but hmac_secret is empty.
 */
portunus_err_t server_comm_init(const portunus_device_config_t *cfg);

/**
 * @brief Return true if the module clock has been synchronised at least once.
 *
 * False at boot and until the first successful heartbeat response is decoded
 * and settimeofday() succeeds.  Used by the access path to decide whether
 * to populate AccessRequest.requested_at for replay protection.
 */
bool server_comm_clock_synced(void);

#ifdef __cplusplus
}
#endif