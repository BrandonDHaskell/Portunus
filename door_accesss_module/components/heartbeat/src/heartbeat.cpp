#include "heartbeat.h"

#include <cstdio>
#include <cstring>

#include "cJSON.h"
#include "device_state.h"
#include "esp_log.h"
#include "esp_timer.h"
#include "freertos/FreeRTOS.h"
#include "freertos/task.h"
#include "http_client.h"
#include "portunus_config.h"
#include "wifi_manager.h"

static const char* TAG = "heartbeat";
static uint32_t s_seq = 0;

static void heartbeat_task(void*) {
    char url[256];
    std::snprintf(url, sizeof(url), "%s/v1/heartbeat", portunus_server_base_url());

    while (true) {
        if (!wifi_manager_wait_connected(pdMS_TO_TICKS(1000))) {
            ESP_LOGW(TAG, "WiFi not connected; skipping heartbeat");
            vTaskDelay(pdMS_TO_TICKS(CONFIG_PORTUNUS_HEARTBEAT_INTERVAL_MS));
            continue;
        }

        device_state_set_wifi_rssi(wifi_manager_get_rssi());
        door_module_status_t st = device_state_get_snapshot();

        cJSON* root = cJSON_CreateObject();
        cJSON_AddStringToObject(root, "module_id", portunus_module_id());
        cJSON_AddNumberToObject(root, "seq", (double)++s_seq);
        cJSON_AddNumberToObject(root, "uptime_ms", (double)(esp_timer_get_time() / 1000ULL));
        cJSON_AddStringToObject(root, "fw_version", portunus_fw_version());
        cJSON_AddNumberToObject(root, "wifi_rssi", (double)st.wifi_rssi);
        cJSON_AddBoolToObject(root, "strike_unlocked", st.strike_unlocked);

#if CONFIG_PORTUNUS_ENABLE_REED_SWITCH
        cJSON_AddBoolToObject(root, "door_open", st.door_open);
#endif

        char* body = cJSON_PrintUnformatted(root);
        cJSON_Delete(root);

        if (!body) {
            ESP_LOGE(TAG, "Failed to serialize JSON");
            vTaskDelay(pdMS_TO_TICKS(CONFIG_PORTUNUS_HEARTBEAT_INTERVAL_MS));
            continue;
        }

        char resp[256];
        esp_err_t err = http_post_json(url, body, resp, sizeof(resp));
        if (err == ESP_OK) {
            ESP_LOGI(TAG, "heartbeat ok: %s", resp);
        } else {
            ESP_LOGW(TAG, "heartbeat failed: %s", esp_err_to_name(err));
        }

        cJSON_free(body);
        vTaskDelay(pdMS_TO_TICKS(CONFIG_PORTUNUS_HEARTBEAT_INTERVAL_MS));
    }
}

extern "C" void heartbeat_start() {
    xTaskCreate(heartbeat_task, "heartbeat", 4096, nullptr, 6, nullptr);
}
