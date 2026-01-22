#include "esp_log.h"
#include "nvs_flash.h"

#include "device_state.h"
#include "door_strike.h"
#include "heartbeat.h"
#include "reed_switch.h"
#include "rfid_reader.h"
#include "status_led.h"
#include "wifi_manager.h"

static const char* TAG = "app_main";

extern "C" void app_main() {
    ESP_LOGI(TAG, "Portunus door_access_module boot");

    // NVS is needed for WiFi and later for provisioning storage.
    esp_err_t err = nvs_flash_init();
    if (err == ESP_ERR_NVS_NO_FREE_PAGES || err == ESP_ERR_NVS_NEW_VERSION_FOUND) {
        ESP_ERROR_CHECK(nvs_flash_erase());
        ESP_ERROR_CHECK(nvs_flash_init());
    }

    device_state_init();

    // Required components
    door_strike_init();
    rfid_reader_init();

    // Optional components (internally no-op if disabled)
    reed_switch_init();
    status_led_init();

    // WiFi + heartbeat
    wifi_manager_init_sta();
    heartbeat_start();

    ESP_LOGI(TAG, "Init complete");
}
