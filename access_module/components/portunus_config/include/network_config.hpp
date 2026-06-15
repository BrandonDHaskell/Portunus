/**
 * @file network_config.h
 * @brief Network timing constants for the Portunus access module.
 *
 * Deployment-specific values (module ID, WiFi credentials, server host/port)
 * are no longer compile-time constants.  They are loaded from NVS at boot via
 * portunus_nvs_load() and passed to wifi_mgr_init() / server_comm_init().
 *
 * This file retains only the timing and timeout values that are legitimately
 * build-time constants (not secrets, not site-specific deployment data).
 */

#pragma once

#include "sdkconfig.h"

#ifdef __cplusplus
extern "C" {
#endif

/* ── WiFi timing ───────────────────────────────────────────────────────────── */

/** Maximum time (ms) wifi_mgr_start() blocks waiting for an IP. */
#define PORTUNUS_WIFI_CONNECT_TIMEOUT_MS    CONFIG_PORTUNUS_WIFI_CONNECT_TIMEOUT_MS

/** Base interval (ms) between reconnection attempts after disconnect.
 *  The manager doubles this on each failure up to a 60 s ceiling. */
#define PORTUNUS_WIFI_RECONNECT_INTERVAL_MS CONFIG_PORTUNUS_WIFI_RECONNECT_INTERVAL_MS

/* ── Portunus server ───────────────────────────────────────────────────────── */

/** Server request timeout (ms) for gRPC connect and RPC calls. */
#define PORTUNUS_SERVER_REQUEST_TIMEOUT_MS  CONFIG_PORTUNUS_SERVER_REQUEST_TIMEOUT_MS

#ifdef __cplusplus
}
#endif