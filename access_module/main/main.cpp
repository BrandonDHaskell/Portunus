/**
 * @file main.cpp
 * @brief Portunus access module — application entry point.
 *
 * Startup sequence:
 *   1. NVS flash initialisation (ESP-IDF standard API — no custom HAL for MVP)
 *   2. WiFi station connection (blocks until IP or timeout)
 *   3. Event bus creation and subscriber registration
 *   4. MFRC522 driver initialisation
 *   5. Heartbeat service start
 *   6. Card-polling task start
 *   7. Transition to OPERATIONAL state
 *
 * All inter-component communication flows through the event bus. The
 * card-polling task reads cards via the mfrc522 driver and publishes
 * credential events; the heartbeat service publishes periodic health
 * events. Subscriber callbacks log both to the serial console.
 */

#include "sdkconfig.h"
#include "portunus_types.h"
#include "error_codes.h"
#include "event_types.h"
#include "system_states.h"
#include "credential_types.h"
#include "timing_config.h"
#include "event_bus.h"
#include "heartbeat_service.h"
#include "wifi_mgr.h"
#include "network_config.h"
#include "mfrc522.h"

#include "nvs_flash.h"
#include "esp_log.h"
#include "esp_timer.h"
#include "freertos/FreeRTOS.h"
#include "freertos/task.h"

#include <string.h>
#include <inttypes.h>

static const char *TAG = "portunus";

static system_state_t s_system_state = SYSTEM_STATE_BOOT;

/* ── Event bus subscriber callbacks ────────────────────────────────────────── */

/**
 * @brief Log credential read events to the serial console.
 */
static void on_credential_read(const portunus_event_t *event, void *ctx)
{
    (void)ctx;
    const event_credential_read_t *cred = &event->payload.credential_read;

    /* Format UID as hex string for logging */
    char uid_str[CREDENTIAL_UID_MAX_LEN * 3 + 1];
    uid_str[0] = '\0';
    for (uint8_t i = 0; i < cred->credential.uid_len; i++) {
        char byte_str[4];
        snprintf(byte_str, sizeof(byte_str), "%s%02X",
                 (i > 0) ? ":" : "", cred->credential.uid[i]);
        strncat(uid_str, byte_str, sizeof(uid_str) - strlen(uid_str) - 1);
    }

    ESP_LOGI(TAG, "Card read — UID: %s (len=%d)", uid_str, cred->credential.uid_len);
}

/**
 * @brief Log heartbeat events to the serial console.
 */
static void on_heartbeat(const portunus_event_t *event, void *ctx)
{
    (void)ctx;
    const event_heartbeat_t *hb = &event->payload.heartbeat;

    ESP_LOGD(TAG, "Heartbeat event received — seq=%" PRIu32 " uptime=%" PRIu32
             "s heap=%" PRIu32, hb->sequence, hb->uptime_sec, hb->free_heap_bytes);
}

/* ── Card-polling task ─────────────────────────────────────────────────────── */

#ifdef CONFIG_PORTUNUS_ENABLE_MFRC522

static void card_poll_task(void *arg)
{
    (void)arg;
    TickType_t poll_interval = pdMS_TO_TICKS(MFRC522_POLL_INTERVAL_MS);

    ESP_LOGI(TAG, "Card polling task started (interval=%d ms)", MFRC522_POLL_INTERVAL_MS);

    for (;;) {
        credential_t cred;
        portunus_err_t err = mfrc522_read_card(&cred);

        if (err == PORTUNUS_OK) {
            /* Build and publish credential event */
            portunus_event_t event;
            memset(&event, 0, sizeof(event));
            event.id = EVENT_CREDENTIAL_READ;
            event.payload.credential_read.credential  = cred;
            event.payload.credential_read.timestamp_ms = esp_timer_get_time() / 1000;

            event_bus_publish(&event);

            /* Halt the card so it isn't re-read on the next poll cycle */
            mfrc522_halt_card();

            /* Brief extra delay after a successful read to avoid rapid re-reads
               if the user holds the card against the reader */
            vTaskDelay(pdMS_TO_TICKS(1000));
        }
        /* PORTUNUS_ERR_NO_CARD is expected (no card present) — silently continue */

        vTaskDelay(poll_interval);
    }
}

#endif /* CONFIG_PORTUNUS_ENABLE_MFRC522 */

/* ── Initialisation helpers ────────────────────────────────────────────────── */

/**
 * @brief Initialise NVS flash.
 *
 * Uses the standard ESP-IDF NVS API directly per the MVP design
 * (no custom HAL wrapper — see project plan §5.1).
 */
static portunus_err_t init_nvs(void)
{
    esp_err_t ret = nvs_flash_init();
    if (ret == ESP_ERR_NVS_NO_FREE_PAGES || ret == ESP_ERR_NVS_NEW_VERSION_FOUND) {
        ESP_LOGW(TAG, "NVS partition truncated or version mismatch — erasing");
        ESP_ERROR_CHECK(nvs_flash_erase());
        ret = nvs_flash_init();
    }
    if (ret != ESP_OK) {
        ESP_LOGE(TAG, "NVS init failed: %s", esp_err_to_name(ret));
        return PORTUNUS_FAIL;
    }
    ESP_LOGI(TAG, "NVS initialised");
    return PORTUNUS_OK;
}

/* ── Application entry point ───────────────────────────────────────────────── */

extern "C" void app_main(void)
{
    ESP_LOGI(TAG, "========================================");
    ESP_LOGI(TAG, "  Portunus Access Module v%s", PORTUNUS_FW_VERSION);
    ESP_LOGI(TAG, "========================================");

    s_system_state = SYSTEM_STATE_INITIALIZING;

    /* ── 1. NVS flash ────────────────────────────────────────────────────── */
    if (init_nvs() != PORTUNUS_OK) {
        s_system_state = SYSTEM_STATE_ERROR;
        ESP_LOGE(TAG, "System halted: NVS init failure");
        return;
    }

    /* ── 2. WiFi connection ─────────────────────────────────────────────── */
#ifdef CONFIG_PORTUNUS_ENABLE_WIFI
    s_system_state = SYSTEM_STATE_CONNECTING;

    if (wifi_mgr_init() != PORTUNUS_OK) {
        s_system_state = SYSTEM_STATE_ERROR;
        ESP_LOGE(TAG, "System halted: WiFi init failure");
        return;
    }

    portunus_err_t wifi_err = wifi_mgr_start();
    if (wifi_err == PORTUNUS_ERR_TIMEOUT) {
        /* Non-fatal: the module continues booting and the WiFi manager
           will keep reconnecting in the background.  Network-dependent
           services (server_comm) check wifi_mgr_is_connected() before
           making calls. */
        ESP_LOGW(TAG, "WiFi not connected yet — continuing startup");
    } else if (wifi_err != PORTUNUS_OK) {
        s_system_state = SYSTEM_STATE_ERROR;
        ESP_LOGE(TAG, "System halted: WiFi start failure");
        return;
    }
#else
    ESP_LOGW(TAG, "WiFi disabled by configuration — running offline");
#endif

    s_system_state = SYSTEM_STATE_INITIALIZING;

    /* ── 3. Event bus ────────────────────────────────────────────────────── */
    if (event_bus_init() != PORTUNUS_OK) {
        s_system_state = SYSTEM_STATE_ERROR;
        ESP_LOGE(TAG, "System halted: event bus init failure");
        return;
    }

    /* Register event subscribers for serial console logging */
    event_bus_subscribe(EVENT_CREDENTIAL_READ, on_credential_read, NULL);
    event_bus_subscribe(EVENT_HEARTBEAT, on_heartbeat, NULL);

    /* ── 4. MFRC522 RFID reader ─────────────────────────────────────────── */
#ifdef CONFIG_PORTUNUS_ENABLE_MFRC522
    if (mfrc522_init() != PORTUNUS_OK) {
        ESP_LOGE(TAG, "MFRC522 init failed — card reading disabled");
        /* Non-fatal for MVP: continue without the reader so other subsystems
           can still be tested. */
    } else {
        BaseType_t ret = xTaskCreate(
            card_poll_task,
            "card_poll",
            CONFIG_PORTUNUS_MFRC522_TASK_STACK_SIZE,
            NULL,
            4,  /* Priority between heartbeat (3) and event dispatcher (5) */
            NULL
        );
        if (ret != pdPASS) {
            ESP_LOGE(TAG, "Failed to create card polling task");
        }
    }
#else
    ESP_LOGW(TAG, "MFRC522 disabled by configuration");
#endif

    /* ── 5. Heartbeat service ────────────────────────────────────────────── */
#ifdef CONFIG_PORTUNUS_ENABLE_HEARTBEAT
    if (heartbeat_service_start() != PORTUNUS_OK) {
        ESP_LOGE(TAG, "Heartbeat service start failed — continuing without heartbeat");
    }
#else
    ESP_LOGW(TAG, "Heartbeat service disabled by configuration");
#endif

    /* ── 6. Startup complete ─────────────────────────────────────────────── */
    s_system_state = SYSTEM_STATE_OPERATIONAL;

    /* Publish boot-complete event */
    portunus_event_t boot_event;
    memset(&boot_event, 0, sizeof(boot_event));
    boot_event.id = EVENT_SYSTEM_BOOT_COMPLETE;
    event_bus_publish(&boot_event);

    ESP_LOGI(TAG, "System operational — entering idle loop");
    ESP_LOGI(TAG, "Free heap: %" PRIu32 " bytes", esp_get_free_heap_size());

    /* app_main returns; FreeRTOS scheduler continues running the tasks. */
}