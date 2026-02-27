/**
 *  main.cpp
 *  Portunus access module — application entry point.
 *
 * main.cpp is now a thin composition root.  It:
 *   1. Initialises platform services (NVS, WiFi, event bus).
 *   2. Constructs concrete module instances.
 *   3. Injects modules into the System FSM.
 *   4. Starts independent services (heartbeat, server comm).
 *   5. Starts the FSM (which owns card polling, event processing,
 *      unlock timing, door state monitoring, and feedback).
 *
 * All inter-component communication flows through the event bus.
 * The FSM is the top-level decision maker — main.cpp does not
 * subscribe to events or manage system state directly.
 */

#include "sdkconfig.h"

#include "portunus_types.h"
#include "error_codes.h"
#include "event_types.h"
#include "system_states.h"
#include "timing_config.h"
#include "event_bus.h"
#include "system_fsm.h"

/* Optional services (feature-gated) */
#ifdef CONFIG_PORTUNUS_ENABLE_HEARTBEAT
#include "heartbeat_service.h"
#endif

#ifdef CONFIG_PORTUNUS_ENABLE_WIFI
#include "wifi_mgr.h"
#include "network_config.h"
#include "server_comm.h"
#endif

/* Concrete module implementations */
#ifdef CONFIG_PORTUNUS_ENABLE_MFRC522
#include "reader_mfrc522.h"
#endif
#ifdef CONFIG_PORTUNUS_ENABLE_DOOR_STRIKE
#include "access_point_gpio.h"
#endif
#ifdef CONFIG_PORTUNUS_ENABLE_LED
#include "feedback_led.h"
#endif

#include "nvs_flash.h"
#include "esp_log.h"
#include "esp_system.h"
#include "freertos/FreeRTOS.h"
#include "freertos/task.h"

#include <inttypes.h>

static const char *TAG = "portunus";

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

/* ── Application entry point ──────────────────────────────────────────────── */

extern "C" void app_main(void)
{
    ESP_LOGI(TAG, "========================================");
    ESP_LOGI(TAG, "  Portunus Access Module v%s", PORTUNUS_FW_VERSION);
    ESP_LOGI(TAG, "========================================");

    /* ── 1. NVS flash ────────────────────────────────────────────────────── */
    if (init_nvs() != PORTUNUS_OK) {
        ESP_LOGE(TAG, "System halted: NVS init failure");
        return;
    }

    /* ── 2. WiFi connection ──────────────────────────────────────────────── */
#ifdef CONFIG_PORTUNUS_ENABLE_WIFI
    if (wifi_mgr_init() != PORTUNUS_OK) {
        ESP_LOGE(TAG, "System halted: WiFi init failure");
        return;
    }

    portunus_err_t wifi_err = wifi_mgr_start();
    if (wifi_err == PORTUNUS_ERR_TIMEOUT) {
        ESP_LOGW(TAG, "WiFi not connected yet — continuing startup");
    } else if (wifi_err != PORTUNUS_OK) {
        ESP_LOGE(TAG, "System halted: WiFi start failure");
        return;
    }
#else
    ESP_LOGW(TAG, "WiFi disabled by configuration — running offline");
#endif

    /* ── 3. Event bus ────────────────────────────────────────────────────── */
    if (event_bus_init() != PORTUNUS_OK) {
        ESP_LOGE(TAG, "System halted: event bus init failure");
        return;
    }

    /* ── 4. Construct module instances ───────────────────────────────────── */

    /*
     * Each module has static storage duration so it outlives
     * app_main's stack frame.  The FSM and its FreeRTOS tasks hold
     * pointers to these objects, so they must remain valid for the
     * lifetime of the program — static guarantees that.
     *
     * Modules whose hardware is disabled by Kconfig are not
     * constructed; a nullptr is passed to the FSM instead.
     */

#ifdef CONFIG_PORTUNUS_ENABLE_MFRC522
    static ReaderMfrc522 reader;
    ICredentialReader *reader_ptr = &reader;
#else
    ESP_LOGW(TAG, "MFRC522 disabled by configuration");
    ICredentialReader *reader_ptr = nullptr;
#endif

#ifdef CONFIG_PORTUNUS_ENABLE_DOOR_STRIKE
    static AccessPointGpio access_point;
    IAccessPoint *access_ptr = &access_point;
#else
    ESP_LOGW(TAG, "Door strike disabled by configuration");
    IAccessPoint *access_ptr = nullptr;
#endif

#ifdef CONFIG_PORTUNUS_ENABLE_LED
    static FeedbackLed feedback;
    IFeedback *feedback_ptr = &feedback;
#else
    ESP_LOGW(TAG, "LED disabled by configuration");
    IFeedback *feedback_ptr = nullptr;
#endif

    /* ── 5. Construct and initialise FSM ─────────────────────────────────── */
    static SystemFSM fsm(reader_ptr, access_ptr, feedback_ptr);

    if (fsm.init() != PORTUNUS_OK) {
        ESP_LOGE(TAG, "System halted: FSM init failure");
        return;
    }

    /* ── 6. Start independent services ───────────────────────────────────── */

#ifdef CONFIG_PORTUNUS_ENABLE_HEARTBEAT
    if (heartbeat_service_start() != PORTUNUS_OK) {
        ESP_LOGE(TAG, "Heartbeat service start failed — continuing without heartbeat");
    }
#else
    ESP_LOGW(TAG, "Heartbeat service disabled by configuration");
#endif

#ifdef CONFIG_PORTUNUS_ENABLE_WIFI
    if (server_comm_init() != PORTUNUS_OK) {
        ESP_LOGE(TAG, "Server comm init failed — running in offline mode");
    }
#endif

    /* ── 7. Start FSM ────────────────────────────────────────────────────── */
    if (fsm.start() != PORTUNUS_OK) {
        ESP_LOGE(TAG, "System halted: FSM start failure");
        return;
    }

    ESP_LOGI(TAG, "System operational — FSM running");
    ESP_LOGI(TAG, "Free heap: %" PRIu32 " bytes", esp_get_free_heap_size());

    /* app_main returns; FreeRTOS scheduler continues running tasks. */
}