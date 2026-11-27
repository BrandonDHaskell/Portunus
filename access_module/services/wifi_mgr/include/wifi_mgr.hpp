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
 *   - wifi_mgr_init(), wifi_mgr_start(), wifi_mgr_stop() and
 *     wifi_mgr_deinit() must be called from a single controlling task
 *     (typically app_main).
 *   - wifi_mgr_is_connected() is safe to call from any task.
 *
 * Internal tasks (created by wifi_mgr_init, ended by wifi_mgr_deinit):
 *   - "wifi_reconn": reconnect with exponential backoff (3.5 KB stack,
 *     priority 3; services/wifi_reconnect). Sleeps until woken by a
 *     disconnect event notification.
 */

#pragma once

#include "portunus_types.hpp"
#include "portunus_nvs.hpp"

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
 * Credentials (SSID and PSK) are taken from @p cfg rather than Kconfig so
 * that they can be stored in NVS instead of baked into the firmware binary.
 *
 * Requires NVS to be initialised first (for WiFi calibration data).
 *
 * @param cfg  Device configuration loaded from NVS; only wifi_ssid and
 *             wifi_psk fields are consumed here.
 * @return PORTUNUS_OK on success.
 *         PORTUNUS_ERR_ALREADY_INIT if called more than once.
 *         PORTUNUS_ERR_TASK_CREATE if the reconnect task could not start.
 *         PORTUNUS_FAIL on ESP-IDF error.
 *         On any failure, everything this call created is released again.
 */
portunus_err_t wifi_mgr_init(const portunus_device_config_t *cfg);

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
 *         PORTUNUS_FAIL          the driver rejected the config or start.
 */
portunus_err_t wifi_mgr_start(void);

/**
 * @brief Disconnect and stop the WiFi driver.
 *
 * Reconnects are suspended first, so the disconnect this causes (and any
 * reconnect already waiting out its backoff) makes no further attempt.
 * After this call wifi_mgr_is_connected() returns false and no reconnection
 * attempts are made until wifi_mgr_start() is called again. Resources stay
 * allocated; wifi_mgr_deinit() releases them.
 */
void wifi_mgr_stop(void);

/**
 * @brief Stop WiFi and release everything wifi_mgr_init() created (D-T9).
 *
 * Unregisters the event handlers (waiting out one in progress), ends the
 * reconnect task cooperatively, deinitialises the driver, and destroys the
 * station netif and event group. The default event loop and esp_netif are
 * shared and left in place. wifi_mgr_init() may be called again afterwards.
 *
 * @return PORTUNUS_OK (also if not initialised), or PORTUNUS_ERR_TIMEOUT if
 *         the reconnect task is stuck in esp_wifi_connect(): nothing past
 *         the handlers is released, and deinit may be called again.
 */
portunus_err_t wifi_mgr_deinit(void);

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