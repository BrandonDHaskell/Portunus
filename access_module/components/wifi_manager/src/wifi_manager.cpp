#include "wifi_manager.h"

#include <cstring>

#include "esp_event.h"
#include "esp_log.h"
#include "esp_netif.h"
#include "esp_wifi.h"
#include "freertos/event_groups.h"

static const char* TAG = "wifi_manager";

static EventGroupHandle_t s_wifi_event_group;
static constexpr int WIFI_CONNECTED_BIT = BIT0;
static constexpr int WIFI_FAIL_BIT      = BIT1;

static int s_retry_num = 0;
static constexpr int MAX_RETRY = 10;

static int s_last_rssi = 0;

static esp_netif_t* s_sta_netif = nullptr;

static void wifi_event_handler(void*,
                               esp_event_base_t event_base,
                               int32_t event_id,
                               void*) {
    if (event_base == WIFI_EVENT && event_id == WIFI_EVENT_STA_START) {
        esp_wifi_connect();
    } else if (event_base == WIFI_EVENT && event_id == WIFI_EVENT_STA_DISCONNECTED) {
        if (s_retry_num < MAX_RETRY) {
            s_retry_num++;
            ESP_LOGW(TAG, "retrying WiFi connect (%d/%d)", s_retry_num, MAX_RETRY);
            esp_wifi_connect();
        } else {
            xEventGroupSetBits(s_wifi_event_group, WIFI_FAIL_BIT);
        }
    } else if (event_base == IP_EVENT && event_id == IP_EVENT_STA_GOT_IP) {
        s_retry_num = 0;
        xEventGroupSetBits(s_wifi_event_group, WIFI_CONNECTED_BIT);
    }
}

extern "C" void wifi_manager_init_sta() {
    s_wifi_event_group = xEventGroupCreate();

    ESP_ERROR_CHECK(esp_netif_init());
    ESP_ERROR_CHECK(esp_event_loop_create_default());
    s_sta_netif = esp_netif_create_default_wifi_sta();

    wifi_init_config_t cfg = WIFI_INIT_CONFIG_DEFAULT();
    ESP_ERROR_CHECK(esp_wifi_init(&cfg));

    ESP_ERROR_CHECK(esp_event_handler_instance_register(
        WIFI_EVENT, ESP_EVENT_ANY_ID, &wifi_event_handler, nullptr, nullptr));
    ESP_ERROR_CHECK(esp_event_handler_instance_register(
        IP_EVENT, IP_EVENT_STA_GOT_IP, &wifi_event_handler, nullptr, nullptr));

    wifi_config_t wifi_config{};
    std::strncpy(reinterpret_cast<char*>(wifi_config.sta.ssid),
                 CONFIG_PORTUNUS_WIFI_SSID,
                 sizeof(wifi_config.sta.ssid));
    std::strncpy(reinterpret_cast<char*>(wifi_config.sta.password),
                 CONFIG_PORTUNUS_WIFI_PASSWORD,
                 sizeof(wifi_config.sta.password));

    wifi_config.sta.threshold.authmode = WIFI_AUTH_WPA2_PSK;
    wifi_config.sta.pmf_cfg.capable = true;
    wifi_config.sta.pmf_cfg.required = false;

    ESP_ERROR_CHECK(esp_wifi_set_mode(WIFI_MODE_STA));
    ESP_ERROR_CHECK(esp_wifi_set_config(WIFI_IF_STA, &wifi_config));
    ESP_ERROR_CHECK(esp_wifi_start());

    ESP_LOGI(TAG, "wifi_manager_init_sta done");
}

extern "C" bool wifi_manager_wait_connected(TickType_t timeout_ticks) {
    EventBits_t bits = xEventGroupWaitBits(
        s_wifi_event_group,
        WIFI_CONNECTED_BIT | WIFI_FAIL_BIT,
        pdFALSE,
        pdFALSE,
        timeout_ticks);

    return (bits & WIFI_CONNECTED_BIT) != 0;
}

extern "C" int wifi_manager_get_rssi() {
    wifi_ap_record_t ap_info{};
    if (esp_wifi_sta_get_ap_info(&ap_info) == ESP_OK) {
        s_last_rssi = ap_info.rssi;
        return s_last_rssi;
    }
    return 0;
}

extern "C" bool wifi_manager_get_ip4(char* out, size_t out_len) {
    if (!s_sta_netif || !out || out_len < 16) return false;

    esp_netif_ip_info_t ip_info{};
    if (esp_netif_get_ip_info(s_sta_netif, &ip_info) != ESP_OK) return false;

    snprintf(out, out_len, IPSTR, IP2STR(&ip_info.ip));
    return true;
}