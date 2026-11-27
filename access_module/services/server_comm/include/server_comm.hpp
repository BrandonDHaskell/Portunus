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
#include "sdkconfig.h"

#include <sys/time.h>
#include <stdint.h>

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

/**
 * @brief Events lost since boot: requests or heartbeats dropped because the
 * comm queue was full, and decisions the event bus refused. Dropped
 * requests are answered (deny or COMM_ERROR); this counts them for logs and
 * future telemetry (D-T5).
 */
uint32_t server_comm_dropped_events(void);

/**
 * @brief Stop server_comm and release everything init created (D-T9).
 *
 * Removes its event bus subscriptions, asks the comm task to stop and waits
 * for it to finish the request in hand (up to the RPC timeouts), answers any
 * request still queued (deny or COMM_ERROR, reason "shutdown"), destroys the
 * gRPC client and clears its state. server_comm_init() may then run again.
 *
 * @return PORTUNUS_OK once stopped (or if not initialised), or
 *         PORTUNUS_ERR_TIMEOUT if the task did not finish in time; it is then
 *         left running, unsubscribed, and deinit may be called again.
 */
portunus_err_t server_comm_deinit(void);

#ifdef CONFIG_PORTUNUS_TEST_HOOKS
/* Test-only entry points. CONFIG_PORTUNUS_TEST_HOOKS is defined only by the
 * component tier's Kconfig, so no firmware build contains these. */

/**
 * @brief Return server_comm to its pre-init state: delete the task and
 * queue, destroy the gRPC client and clear all state. Call only while the
 * component is idle. Event bus subscriptions are not removed; reset the bus
 * as well.
 */
/* How long server_comm_deinit() waits for the comm task; <= 0 restores the
 * default. Reset by server_comm_reset_for_test(). */
void server_comm_set_stop_timeout_for_test(int ms);
void server_comm_reset_for_test(void);

/**
 * @brief Replace settimeofday() for clock syncs (NULL restores it), so tests
 * on the linux target never try to set the host clock.
 */
void server_comm_set_clock_setter_for_test(int (*set_time)(const struct timeval *tv));
#endif

#ifdef __cplusplus
}
#endif