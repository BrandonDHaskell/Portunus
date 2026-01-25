#include "modules/wifi_sta_module.h"
#include "portunus_event.h"

extern "C" {
#include "wifi_manager.h"
#include "device_state.h"
#include "esp_log.h"
}

namespace portunus {
static const char* TAG = "wifi_sta_mod";

esp_err_t WifiStaModule::init() {
  wifi_manager_init_sta();
  return ESP_OK;
}

esp_err_t WifiStaModule::start(EventBus& bus) {
  bus_ = &bus;
  if (task_) return ESP_OK;
  BaseType_t ok = xTaskCreate(task_entry, "wifi_mon", 3072, this, 6, &task_);
  return ok == pdPASS ? ESP_OK : ESP_FAIL;
}

void WifiStaModule::task_entry(void* arg) {
  static_cast<WifiStaModule*>(arg)->run();
}

void WifiStaModule::run() {
  while (true) {
    const bool connected = wifi_manager_wait_connected(0);

    if (connected && !last_connected_) {
      ESP_LOGI(TAG, "WiFi connected");
      bus_->publish(Event::wifi_connected());
    } else if (!connected && last_connected_) {
      ESP_LOGW(TAG, "WiFi disconnected");
      bus_->publish(Event::wifi_disconnected());
    }

    last_connected_ = connected;

    // Keep device_state RSSI fresh for heartbeat.
    if (connected) device_state_set_wifi_rssi(wifi_manager_get_rssi());

    vTaskDelay(pdMS_TO_TICKS(500));
  }
}

} // namespace portunus
