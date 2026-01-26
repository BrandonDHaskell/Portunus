#include "modules/device_state_monitor_module.h"
#include "portunus_event.h"

extern "C" {
#include "device_state.h"
}

namespace portunus {

esp_err_t DeviceStateMonitorModule::start(EventBus& bus) {
  bus_ = &bus;
  if (task_) return ESP_OK;
  BaseType_t ok = xTaskCreate(task_entry, "state_mon", 3072, this, 5, &task_);
  return ok == pdPASS ? ESP_OK : ESP_FAIL;
}

void DeviceStateMonitorModule::task_entry(void* arg) {
  static_cast<DeviceStateMonitorModule*>(arg)->run();
}

void DeviceStateMonitorModule::run() {
#if CONFIG_PORTUNUS_ENABLE_REED_SWITCH
  last_door_open_ = device_state_get_snapshot().door_open;
#endif

  while (true) {
#if CONFIG_PORTUNUS_ENABLE_REED_SWITCH
    const auto st = device_state_get_snapshot();
    if (st.door_open != last_door_open_) {
      last_door_open_ = st.door_open;
      Event e{};
      e.ts_us = Event::now_us();
      e.type = st.door_open ? EventType::DoorOpened : EventType::DoorClosed;
      e.arg0 = st.door_open ? 1u : 0u;
      bus_->publish(e);
    }
#endif
    vTaskDelay(pdMS_TO_TICKS(100));
  }
}

} // namespace portunus
