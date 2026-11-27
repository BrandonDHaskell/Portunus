/**
 * @file wifi_mgr.cpp
 * @brief WiFi station manager implementation.
 *
 * Lifecycle:
 *   wifi_mgr_init()   → netif + driver + event handlers + reconnect task
 *   wifi_mgr_start()  → esp_wifi_start/connect, blocks until IP or timeout
 *   wifi_mgr_stop()   → suspend reconnects, disconnect, stop the driver
 *   wifi_mgr_deinit() → release everything init created (D-T9)
 *
 * On WIFI_EVENT_STA_DISCONNECTED the event handler notifies the reconnect
 * task (services/wifi_reconnect), which waits out an exponential backoff and
 * re-issues esp_wifi_connect(). This keeps the default event loop unblocked
 * so that IP, system, and other ESP-IDF event handlers continue to be
 * dispatched normally during backoff.
 *
 * The backoff resets on a successful connection (IP_EVENT_STA_GOT_IP).
 */

#include "wifi_mgr.hpp"
#include "wifi_reconnect.hpp"
#include "network_config.hpp"
#include "error_codes.hpp"

#include "esp_wifi.h"
#include "esp_netif.h"
#include "esp_event.h"
#include "esp_log.h"
#include "freertos/FreeRTOS.h"
#include "freertos/event_groups.h"
#include "freertos/task.h"

#include <string.h>
#include <atomic>
#include <inttypes.h>

static const char *TAG = "wifi_mgr";

/* ── Event-group bits ──────────────────────────────────────────────────────── */
#define WIFI_CONNECTED_BIT   BIT0   /* IP obtained  */
#define WIFI_FAIL_BIT        BIT1   /* connect gave up (unused for now) */

/* ── Module state ──────────────────────────────────────────────────────────── */
static EventGroupHandle_t  s_wifi_event_group = NULL;
static esp_netif_t        *s_sta_netif        = NULL;
static bool                s_wifi_inited      = false;   /* esp_wifi_init() succeeded */
static esp_event_handler_instance_t s_wifi_handler = NULL;
static esp_event_handler_instance_t s_ip_handler   = NULL;
static bool                s_initialized      = false;
static std::atomic<bool>   s_connected{false};

/* WiFi credentials stored at init from NVS, used at connect time */
static char s_wifi_ssid[PORTUNUS_NVS_WIFI_SSID_LEN];
static char s_wifi_psk[PORTUNUS_NVS_WIFI_PSK_LEN];

/* ── Reconnect task ────────────────────────────────────────────────────────── */
static const uint32_t RECONNECT_CEILING_MS = 60000;   /* 60 s hard ceiling */

#define RECONNECT_TASK_STACK_SIZE  2560
#define RECONNECT_TASK_PRIORITY    3     /* Below event dispatcher (5) */
/* The task is either waiting (ends at once) or inside esp_wifi_connect(),
 * which only queues a request to the driver task. */
#define RECONNECT_STOP_TIMEOUT_MS  2000

static void reconnect_connect(void *ctx)
{
    (void)ctx;
    esp_err_t err = esp_wifi_connect();
    if (err != ESP_OK) {
        ESP_LOGW(TAG, "esp_wifi_connect() failed: %s", esp_err_to_name(err));
    }
}

static bool reconnect_is_connected(void *ctx)
{
    (void)ctx;
    return s_connected;
}

static WifiReconnect s_reconnect({reconnect_connect, reconnect_is_connected, nullptr},
                                 PORTUNUS_WIFI_RECONNECT_INTERVAL_MS, RECONNECT_CEILING_MS);

/* ── Event handlers ────────────────────────────────────────────────────────── */

/**
 * @brief WiFi event handler — runs on the default event loop task.
 *
 * CRITICAL: This function must NEVER block.  Any delay or reconnect
 * logic is delegated to the reconnect task via task notification.
 */
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
        ESP_LOGW(TAG, "Disconnected (reason=%d) — notifying reconnect task "
                 "(next backoff=%" PRIu32 " ms)",
                 info->reason, s_reconnect.interval_ms());

        /* Ignored while wifi_mgr_stop() has reconnects suspended. */
        s_reconnect.notify_disconnected();
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
        s_reconnect.notify_connected();   /* reset backoff */

        /* Unblock wifi_mgr_start() */
        if (s_wifi_event_group) {
            xEventGroupSetBits(s_wifi_event_group, WIFI_CONNECTED_BIT);
        }
    }
}

/* ── Teardown ──────────────────────────────────────────────────────────────── */

/* Unregister both handlers. esp_event dispatches under the loop's lock, which
 * unregister also takes, so a handler in progress has returned once this
 * does: nothing touches the reconnect task afterwards. */
static void unregister_handlers(void)
{
    if (s_ip_handler != NULL) {
        esp_event_handler_instance_unregister(IP_EVENT, IP_EVENT_STA_GOT_IP, s_ip_handler);
        s_ip_handler = NULL;
    }
    if (s_wifi_handler != NULL) {
        esp_event_handler_instance_unregister(WIFI_EVENT, ESP_EVENT_ANY_ID, s_wifi_handler);
        s_wifi_handler = NULL;
    }
}

/* Release the driver, netif and event group; each step skips what was
 * never created, so init's failure paths share it. */
static void release_driver(void)
{
    if (s_wifi_inited) {
        (void)esp_wifi_stop();   /* ESP_ERR_WIFI_NOT_STARTED is fine */
        esp_wifi_deinit();
        s_wifi_inited = false;
    }
    if (s_sta_netif != NULL) {
        esp_netif_destroy_default_wifi(s_sta_netif);
        s_sta_netif = NULL;
    }
    if (s_wifi_event_group != NULL) {
        vEventGroupDelete(s_wifi_event_group);
        s_wifi_event_group = NULL;
    }
    s_connected = false;
}

/* ── Public API ────────────────────────────────────────────────────────────── */

portunus_err_t wifi_mgr_init(const portunus_device_config_t *cfg)
{
    if (s_initialized) {
        ESP_LOGW(TAG, "WiFi manager already initialised");
        return PORTUNUS_ERR_ALREADY_INIT;
    }

    strlcpy(s_wifi_ssid, cfg->wifi_ssid, sizeof(s_wifi_ssid));
    strlcpy(s_wifi_psk,  cfg->wifi_psk,  sizeof(s_wifi_psk));

    s_wifi_event_group = xEventGroupCreate();
    if (s_wifi_event_group == NULL) {
        ESP_LOGE(TAG, "Failed to create WiFi event group");
        return PORTUNUS_FAIL;
    }

    /* Initialise the TCP/IP stack (idempotent in ESP-IDF ≥ 4.1) */
    esp_err_t err = esp_netif_init();
    if (err != ESP_OK) {
        ESP_LOGE(TAG, "esp_netif_init failed: %s", esp_err_to_name(err));
        release_driver();
        return PORTUNUS_FAIL;
    }

    /* Create the default event loop if not already created.
       app_main does not create one by default in plain ESP-IDF projects. */
    err = esp_event_loop_create_default();
    if (err != ESP_OK && err != ESP_ERR_INVALID_STATE) {
        /* ESP_ERR_INVALID_STATE means the loop already exists — that's fine */
        ESP_LOGE(TAG, "Failed to create default event loop: %s",
                 esp_err_to_name(err));
        release_driver();
        return PORTUNUS_FAIL;
    }

    /* Create default WiFi station netif */
    s_sta_netif = esp_netif_create_default_wifi_sta();
    if (s_sta_netif == NULL) {
        ESP_LOGE(TAG, "Failed to create default WiFi STA netif");
        release_driver();
        return PORTUNUS_FAIL;
    }

    /* Initialise WiFi driver with default config */
    wifi_init_config_t wifi_init_cfg = WIFI_INIT_CONFIG_DEFAULT();
    err = esp_wifi_init(&wifi_init_cfg);
    if (err != ESP_OK) {
        ESP_LOGE(TAG, "esp_wifi_init failed: %s", esp_err_to_name(err));
        release_driver();
        return PORTUNUS_FAIL;
    }
    s_wifi_inited = true;

    /* Start the reconnect task before the handlers that notify it. It
     * waits on a notification, using no CPU until a disconnect. */
    portunus_err_t perr = s_reconnect.start("wifi_reconn", RECONNECT_TASK_STACK_SIZE,
                                            RECONNECT_TASK_PRIORITY);
    if (perr != PORTUNUS_OK) {
        ESP_LOGE(TAG, "Failed to create WiFi reconnect task");
        release_driver();
        return perr == PORTUNUS_ERR_TASK_CREATE ? PORTUNUS_ERR_TASK_CREATE : PORTUNUS_FAIL;
    }

    /* Register event handlers, keeping their instances so deinit can
     * unregister them. */
    err = esp_event_handler_instance_register(WIFI_EVENT, ESP_EVENT_ANY_ID,
                                              wifi_event_handler, NULL, &s_wifi_handler);
    if (err == ESP_OK) {
        err = esp_event_handler_instance_register(IP_EVENT, IP_EVENT_STA_GOT_IP,
                                                  ip_event_handler, NULL, &s_ip_handler);
    }
    if (err != ESP_OK) {
        ESP_LOGE(TAG, "Failed to register WiFi event handlers: %s", esp_err_to_name(err));
        unregister_handlers();
        (void)s_reconnect.stop(RECONNECT_STOP_TIMEOUT_MS);   /* idle: ends at once */
        release_driver();
        return PORTUNUS_FAIL;
    }

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

    /* Configure station credentials from NVS-loaded values */
    wifi_config_t wifi_cfg;
    memset(&wifi_cfg, 0, sizeof(wifi_cfg));

    strncpy((char *)wifi_cfg.sta.ssid,
            s_wifi_ssid,
            sizeof(wifi_cfg.sta.ssid) - 1);
    strncpy((char *)wifi_cfg.sta.password,
            s_wifi_psk,
            sizeof(wifi_cfg.sta.password) - 1);

    /* Require WPA2 minimum unless the password is empty (open network) */
    if (strlen(s_wifi_psk) > 0) {
        wifi_cfg.sta.threshold.authmode = WIFI_AUTH_WPA2_PSK;
    }

    esp_err_t err = esp_wifi_set_mode(WIFI_MODE_STA);
    if (err == ESP_OK) {
        err = esp_wifi_set_config(WIFI_IF_STA, &wifi_cfg);
    }
    if (err != ESP_OK) {
        ESP_LOGE(TAG, "WiFi config rejected: %s", esp_err_to_name(err));
        return PORTUNUS_FAIL;
    }

    /* Clear any previous event bits */
    xEventGroupClearBits(s_wifi_event_group,
                         WIFI_CONNECTED_BIT | WIFI_FAIL_BIT);

    /* Allow reconnects again (after a wifi_mgr_stop()), from the base
     * interval for a fresh start. */
    s_reconnect.resume();

    /* Start the WiFi driver — this triggers WIFI_EVENT_STA_START which
       calls esp_wifi_connect() in the event handler. */
    err = esp_wifi_start();
    if (err != ESP_OK) {
        ESP_LOGE(TAG, "esp_wifi_start failed: %s", esp_err_to_name(err));
        return PORTUNUS_FAIL;
    }

    ESP_LOGI(TAG, "Connecting to AP \"%s\" ...", s_wifi_ssid);

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
    /* Before the disconnect: its DISCONNECTED event must not schedule a
     * reconnect, and a reconnect waiting out its backoff is dropped. */
    s_reconnect.suspend();
    esp_wifi_disconnect();
    esp_wifi_stop();
    s_connected.store(false);
    ESP_LOGI(TAG, "WiFi manager stopped");
}

portunus_err_t wifi_mgr_deinit(void)
{
    if (!s_initialized) {
        return PORTUNUS_OK;
    }
    s_reconnect.suspend();
    unregister_handlers();   /* after this, nothing notifies the task */
    if (s_reconnect.stop(RECONNECT_STOP_TIMEOUT_MS) != PORTUNUS_OK) {
        ESP_LOGE(TAG, "Reconnect task did not stop: WiFi left initialised");
        return PORTUNUS_ERR_TIMEOUT;
    }
    release_driver();
    s_initialized = false;
    ESP_LOGI(TAG, "WiFi manager deinitialised");
    return PORTUNUS_OK;
}

bool wifi_mgr_is_connected(void)
{
    return s_connected;
}
