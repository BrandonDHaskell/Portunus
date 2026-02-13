/**
 * @file wifi_mgr.cpp
 * @brief WiFi station manager implementation.
 *
 * Lifecycle:
 *   wifi_mgr_init()  → creates netif + event handlers (one-time)
 *   wifi_mgr_start() → esp_wifi_start/connect, blocks until IP or timeout
 *
 * On WIFI_EVENT_STA_DISCONNECTED the handler re-issues esp_wifi_connect()
 * with exponential backoff (base = PORTUNUS_WIFI_RECONNECT_INTERVAL_MS,
 * ceiling = 60 s).  The backoff resets on a successful connection.
 */

#include "wifi_mgr.h"
#include "network_config.h"
#include "error_codes.h"

#include "esp_wifi.h"
#include "esp_netif.h"
#include "esp_event.h"
#include "esp_log.h"
#include "freertos/FreeRTOS.h"
#include "freertos/event_groups.h"

#include <string.h>
#include <algorithm>   /* std::min */

static const char *TAG = "wifi_mgr";

/* ── Event-group bits ──────────────────────────────────────────────────────── */
#define WIFI_CONNECTED_BIT   BIT0   /* IP obtained  */
#define WIFI_FAIL_BIT        BIT1   /* connect gave up (unused for now) */

/* ── Module state ──────────────────────────────────────────────────────────── */
static EventGroupHandle_t  s_wifi_event_group = NULL;
static esp_netif_t        *s_sta_netif        = NULL;
static bool                s_initialized      = false;
static volatile bool       s_connected        = false;

/* Reconnect backoff state */
static uint32_t s_reconnect_interval_ms = PORTUNUS_WIFI_RECONNECT_INTERVAL_MS;
static const uint32_t RECONNECT_CEILING_MS = 60000;   /* 60 s hard ceiling */

/* ── Forward declarations ──────────────────────────────────────────────────── */
static void wifi_event_handler(void *arg, esp_event_base_t base,
                               int32_t event_id, void *event_data);
static void ip_event_handler(void *arg, esp_event_base_t base,
                             int32_t event_id, void *event_data);

/* ── Event handlers ────────────────────────────────────────────────────────── */

static void wifi_event_handler(void *arg, esp_event_base_t base,
                               int32_t event_id, void *event_data)
{
    (void)arg;
    (void)base;

    switch (event_id) {

    case WIFI_EVENT_STA_START:
        ESP_LOGI(TAG, "STA started — initiating connection");
        esp_wifi_connect();
        break;

    case WIFI_EVENT_STA_DISCONNECTED: {
        s_connected = false;

        wifi_event_sta_disconnected_t *info =
            (wifi_event_sta_disconnected_t *)event_data;
        ESP_LOGW(TAG, "Disconnected (reason=%d) — retrying in %" PRIu32 " ms",
                 info->reason, s_reconnect_interval_ms);

        vTaskDelay(pdMS_TO_TICKS(s_reconnect_interval_ms));

        /* Exponential backoff: double the interval, cap at ceiling */
        s_reconnect_interval_ms =
            std::min(s_reconnect_interval_ms * 2, RECONNECT_CEILING_MS);

        esp_wifi_connect();
        break;
    }

    case WIFI_EVENT_STA_CONNECTED:
        ESP_LOGI(TAG, "Associated with AP — waiting for IP");
        break;

    default:
        break;
    }
}

static void ip_event_handler(void *arg, esp_event_base_t base,
                             int32_t event_id, void *event_data)
{
    (void)arg;
    (void)base;

    if (event_id == IP_EVENT_STA_GOT_IP) {
        ip_event_got_ip_t *info = (ip_event_got_ip_t *)event_data;
        ESP_LOGI(TAG, "Obtained IP: " IPSTR, IP2STR(&info->ip_info.ip));

        s_connected = true;

        /* Reset backoff on successful connection */
        s_reconnect_interval_ms = PORTUNUS_WIFI_RECONNECT_INTERVAL_MS;

        /* Unblock wifi_mgr_start() */
        if (s_wifi_event_group) {
            xEventGroupSetBits(s_wifi_event_group, WIFI_CONNECTED_BIT);
        }
    }
}

/* ── Public API ────────────────────────────────────────────────────────────── */

portunus_err_t wifi_mgr_init(void)
{
    if (s_initialized) {
        ESP_LOGW(TAG, "WiFi manager already initialised");
        return PORTUNUS_ERR_ALREADY_INIT;
    }

    s_wifi_event_group = xEventGroupCreate();
    if (s_wifi_event_group == NULL) {
        ESP_LOGE(TAG, "Failed to create WiFi event group");
        return PORTUNUS_FAIL;
    }

    /* Initialise the TCP/IP stack (idempotent in ESP-IDF ≥ 4.1) */
    ESP_ERROR_CHECK(esp_netif_init());

    /* Create the default event loop if not already created.
       app_main does not create one by default in plain ESP-IDF projects. */
    esp_err_t err = esp_event_loop_create_default();
    if (err != ESP_OK && err != ESP_ERR_INVALID_STATE) {
        /* ESP_ERR_INVALID_STATE means the loop already exists — that's fine */
        ESP_LOGE(TAG, "Failed to create default event loop: %s",
                 esp_err_to_name(err));
        return PORTUNUS_FAIL;
    }

    /* Create default WiFi station netif */
    s_sta_netif = esp_netif_create_default_wifi_sta();
    if (s_sta_netif == NULL) {
        ESP_LOGE(TAG, "Failed to create default WiFi STA netif");
        return PORTUNUS_FAIL;
    }

    /* Initialise WiFi driver with default config */
    wifi_init_config_t wifi_init_cfg = WIFI_INIT_CONFIG_DEFAULT();
    ESP_ERROR_CHECK(esp_wifi_init(&wifi_init_cfg));

    /* Register event handlers */
    ESP_ERROR_CHECK(esp_event_handler_instance_register(
        WIFI_EVENT, ESP_EVENT_ANY_ID,
        wifi_event_handler, NULL, NULL));

    ESP_ERROR_CHECK(esp_event_handler_instance_register(
        IP_EVENT, IP_EVENT_STA_GOT_IP,
        ip_event_handler, NULL, NULL));

    s_initialized = true;
    ESP_LOGI(TAG, "WiFi manager initialised");
    return PORTUNUS_OK;
}

portunus_err_t wifi_mgr_start(void)
{
    if (!s_initialized) {
        ESP_LOGE(TAG, "wifi_mgr_start() called before init");
        return PORTUNUS_ERR_NOT_INIT;
    }

    /* Configure station credentials from Kconfig */
    wifi_config_t wifi_cfg;
    memset(&wifi_cfg, 0, sizeof(wifi_cfg));

    /* Copy SSID and password from Kconfig — these are compile-time strings */
    strncpy((char *)wifi_cfg.sta.ssid,
            PORTUNUS_WIFI_SSID,
            sizeof(wifi_cfg.sta.ssid) - 1);
    strncpy((char *)wifi_cfg.sta.password,
            PORTUNUS_WIFI_PASSWORD,
            sizeof(wifi_cfg.sta.password) - 1);

    /* Require WPA2 minimum unless the password is empty (open network) */
    if (strlen(PORTUNUS_WIFI_PASSWORD) > 0) {
        wifi_cfg.sta.threshold.authmode = WIFI_AUTH_WPA2_PSK;
    }

    ESP_ERROR_CHECK(esp_wifi_set_mode(WIFI_MODE_STA));
    ESP_ERROR_CHECK(esp_wifi_set_config(WIFI_IF_STA, &wifi_cfg));

    /* Clear any previous event bits */
    xEventGroupClearBits(s_wifi_event_group,
                         WIFI_CONNECTED_BIT | WIFI_FAIL_BIT);

    /* Reset backoff for a fresh start */
    s_reconnect_interval_ms = PORTUNUS_WIFI_RECONNECT_INTERVAL_MS;

    /* Start the WiFi driver — this triggers WIFI_EVENT_STA_START which
       calls esp_wifi_connect() in the event handler. */
    ESP_ERROR_CHECK(esp_wifi_start());

    ESP_LOGI(TAG, "Connecting to AP \"%s\" ...", PORTUNUS_WIFI_SSID);

    /* Block until connected or timeout */
    EventBits_t bits = xEventGroupWaitBits(
        s_wifi_event_group,
        WIFI_CONNECTED_BIT | WIFI_FAIL_BIT,
        pdFALSE,          /* don't clear on exit */
        pdFALSE,          /* wait for ANY bit    */
        pdMS_TO_TICKS(PORTUNUS_WIFI_CONNECT_TIMEOUT_MS));

    if (bits & WIFI_CONNECTED_BIT) {
        ESP_LOGI(TAG, "WiFi connected successfully");
        return PORTUNUS_OK;
    }

    ESP_LOGW(TAG, "WiFi connection timed out after %d ms "
             "(reconnect will continue in background)",
             PORTUNUS_WIFI_CONNECT_TIMEOUT_MS);
    return PORTUNUS_ERR_TIMEOUT;
}

void wifi_mgr_stop(void)
{
    if (!s_initialized) {
        return;
    }
    esp_wifi_disconnect();
    esp_wifi_stop();
    s_connected = false;
    ESP_LOGI(TAG, "WiFi manager stopped");
}

bool wifi_mgr_is_connected(void)
{
    return s_connected;
}