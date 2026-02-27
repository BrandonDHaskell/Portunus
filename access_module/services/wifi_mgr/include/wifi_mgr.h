/**
 * @file wifi_mgr.h
 * @brief WiFi station manager for the Portunus access module.
 *
 * Manages the ESP32 WiFi station interface with automatic reconnection.
 * The typical startup sequence is:
 *
 *   1. wifi_mgr_init()   — create netif, register event handlers, start
 *                           background reconnect task
 *   2. wifi_mgr_start()  — connect to the configured AP, block until
 *                           an IP is obtained or the timeout expires
 *
 * On disconnection the manager automatically attempts to reconnect with
 * exponential backoff (base interval from Kconfig, ceiling at 60 s).
 * Reconnection runs in a dedicated FreeRTOS task ("wifi_reconn") so that
 * the backoff delay never blocks the ESP-IDF default event loop.
 *
 * Thread safety:
 *   - wifi_mgr_init() and wifi_mgr_start() must be called from a single
 *     task during startup (typically app_main).
 *   - wifi_mgr_is_connected() is safe to call from any task.
 *
 * Internal tasks (created by wifi_mgr_init):
 *   - "wifi_reconn" — reconnect with exponential backoff (2.5 KB stack,
 *     priority 3). Sleeps until woken by a disconnect event notification.
 */

#pragma once

#include "portunus_types.h"

#ifdef __cplusplus
extern "C" {
#endif

/**
 * @brief Initialise the WiFi subsystem.
 *
 * Creates the default station netif, initialises the WiFi driver with
 * default config, registers internal event handlers for WIFI_EVENT and
 * IP_EVENT groups, and starts a background reconnect task.
 *
 * Requires NVS to be initialised first (for WiFi calibration data).
 *
 * @return PORTUNUS_OK on success.
 *         PORTUNUS_ERR_ALREADY_INIT if called more than once.
 *         PORTUNUS_ERR_TASK_CREATE if the reconnect task could not start.
 *         PORTUNUS_FAIL on ESP-IDF error.
 */
portunus_err_t wifi_mgr_init(void);

/**
 * @brief Connect to the configured access point.
 *
 * Starts the WiFi driver and initiates a connection.  Blocks until one of:
 *   - An IP address is obtained (returns PORTUNUS_OK), or
 *   - The timeout (PORTUNUS_WIFI_CONNECT_TIMEOUT_MS) expires
 *     (returns PORTUNUS_ERR_TIMEOUT).
 *
 * After a successful return, wifi_mgr_is_connected() will return true.
 * If the connection drops later, the manager reconnects automatically.
 *
 * @return PORTUNUS_OK            Connected and IP obtained.
 *         PORTUNUS_ERR_TIMEOUT   Timed out waiting for IP.
 *         PORTUNUS_ERR_NOT_INIT  wifi_mgr_init() was not called.
 */
portunus_err_t wifi_mgr_start(void);

/**
 * @brief Stop the WiFi driver and release resources.
 *
 * Disconnects from the AP and stops the WiFi driver.  After this call,
 * wifi_mgr_is_connected() returns false and no reconnection attempts
 * will be made until wifi_mgr_start() is called again.
 */
void wifi_mgr_stop(void);

/**
 * @brief Check whether the station has an IP address.
 *
 * Safe to call from any task or ISR context.
 *
 * @return true if connected with a valid IP, false otherwise.
 */
bool wifi_mgr_is_connected(void);

#ifdef __cplusplus
}
#endif