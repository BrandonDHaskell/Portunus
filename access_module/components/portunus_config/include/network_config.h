/**
 * @file network_config.h
 * @brief Network configuration constants for the Portunus access module.
 *
 * All values are sourced from Kconfig so that dev/prod differences and
 * per-deployment credentials are managed through sdkconfig overlays
 * rather than hardcoded strings.
 */

#pragma once

#include "sdkconfig.h"

#ifdef __cplusplus
extern "C" {
#endif

/* ── Module identity ────────────────────────────────────────────────────────── */

/** Unique name for this access module (max 32 chars). */
#define PORTUNUS_MODULE_ID                  CONFIG_PORTUNUS_MODULE_ID

/* ── WiFi station credentials ──────────────────────────────────────────────── */
#define PORTUNUS_WIFI_SSID                  CONFIG_PORTUNUS_WIFI_SSID
#define PORTUNUS_WIFI_PASSWORD              CONFIG_PORTUNUS_WIFI_PASSWORD

/* ── WiFi timing ───────────────────────────────────────────────────────────── */

/** Maximum time (ms) wifi_mgr_start() blocks waiting for an IP. */
#define PORTUNUS_WIFI_CONNECT_TIMEOUT_MS    CONFIG_PORTUNUS_WIFI_CONNECT_TIMEOUT_MS

/** Base interval (ms) between reconnection attempts after disconnect.
 *  The manager doubles this on each failure up to a 60 s ceiling. */
#define PORTUNUS_WIFI_RECONNECT_INTERVAL_MS CONFIG_PORTUNUS_WIFI_RECONNECT_INTERVAL_MS

/* ── Portunus server ───────────────────────────────────────────────────────── */

/** Hostname or IP address of the Portunus server. */
#define PORTUNUS_SERVER_HOST                CONFIG_PORTUNUS_SERVER_HOST

/** TCP port the Portunus server listens on. */
#define PORTUNUS_SERVER_PORT                CONFIG_PORTUNUS_SERVER_PORT

/** HTTP request timeout (ms) for heartbeat and access-request calls. */
#define PORTUNUS_SERVER_REQUEST_TIMEOUT_MS  CONFIG_PORTUNUS_SERVER_REQUEST_TIMEOUT_MS

/* ── gRPC transport ────────────────────────────────────────────────────────── */

/** 1 when gRPC (HTTP/2) transport is enabled; 0 for HTTP/1.1 fallback. */
#ifdef CONFIG_PORTUNUS_USE_GRPC
  #define PORTUNUS_USE_GRPC  1
#else
  #define PORTUNUS_USE_GRPC  0
#endif

/** gRPC server port (only meaningful when PORTUNUS_USE_GRPC == 1). */
#if PORTUNUS_USE_GRPC
  #define PORTUNUS_GRPC_SERVER_PORT  CONFIG_PORTUNUS_GRPC_SERVER_PORT
#endif

#ifdef __cplusplus
}
#endif