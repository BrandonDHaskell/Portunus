/**
 * @file portunus_nvs.hpp
 * @brief Runtime device configuration loaded from NVS at boot.
 *
 * Deployment secrets and site-specific values that were formerly baked into
 * the firmware via Kconfig are stored here instead, in the "portunus" NVS
 * namespace.  Call portunus_nvs_load() once after nvs_flash_init() succeeds,
 * then pass the resulting portunus_device_config_t to the service init
 * functions (wifi_mgr_init, server_comm_init).
 *
 * NVS keys written by the provisioning tool (firmware:nvs:gen + firmware:nvs:flash):
 *   module_id   — string, max 32 chars
 *   wifi_ssid   — string, max 32 chars
 *   wifi_psk    — string, max 64 chars  (empty string = open network)
 *   server_host — string, max 255 chars
 *   grpc_port   — u16
 *   hmac_secret — string, 64 hex chars  (empty string = HMAC disabled)
 *
 * Partition layout note: the nvs_keys partition at 0x18000 is reserved for
 * NVS encryption keys when flash encryption is enabled (future).  Until then
 * the partition exists but is unused; no application code reads it directly.
 */

#pragma once

#include <stdint.h>
#include "esp_err.h"

#ifdef __cplusplus
extern "C" {
#endif

#define PORTUNUS_NVS_NAMESPACE      "portunus"
#define PORTUNUS_NVS_MODULE_ID_LEN  33    /* 32 chars + NUL */
#define PORTUNUS_NVS_WIFI_SSID_LEN  33    /* 32 chars + NUL (802.11 SSID limit) */
#define PORTUNUS_NVS_WIFI_PSK_LEN   65    /* 64 chars + NUL (WPA2 PSK limit) */
#define PORTUNUS_NVS_SERVER_HOST_LEN 256  /* hostname or dotted-quad IP + NUL */
#define PORTUNUS_NVS_HMAC_SECRET_LEN 65   /* 64 hex chars + NUL */

typedef struct {
    char     module_id[PORTUNUS_NVS_MODULE_ID_LEN];
    char     wifi_ssid[PORTUNUS_NVS_WIFI_SSID_LEN];
    char     wifi_psk[PORTUNUS_NVS_WIFI_PSK_LEN];
    char     server_host[PORTUNUS_NVS_SERVER_HOST_LEN];
    uint16_t grpc_port;
    char     hmac_secret[PORTUNUS_NVS_HMAC_SECRET_LEN];
} portunus_device_config_t;

/**
 * @brief Load device configuration from the "portunus" NVS namespace.
 *
 * nvs_flash_init() must have been called successfully before this function.
 * On success all fields of @p out are populated.  On error @p out is left
 * unchanged so the caller can decide whether to use compile-time dev defaults
 * (PORTUNUS_ENV_DEV builds) or halt (production builds).
 *
 * @param[out] out  Receives the loaded configuration on ESP_OK.
 * @return ESP_OK                  All keys present and read successfully.
 *         ESP_ERR_NVS_NOT_FOUND   One or more required keys are absent.
 *         Other esp_err_t         NVS open or read error.
 */
esp_err_t portunus_nvs_load(portunus_device_config_t *out);

#ifdef __cplusplus
}
#endif
