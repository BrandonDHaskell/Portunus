#pragma once
#include "portunus_module.h"
#include "portunus_event_bus.h"

extern "C" {
#include "freertos/FreeRTOS.h"
#include "freertos/task.h"
}

namespace portunus {

class DeviceStateMonitorModule final : public Module {
public:
  const char* name() const override { return "device_state_mon"; }
  esp_err_t init() override { return ESP_OK; }
  esp_err_t start(EventBus& bus) override;

private:
  static void task_entry(void* arg);
  void run();

  EventBus* bus_{nullptr};
  TaskHandle_t task_{nullptr};

#if CONFIG_PORTUNUS_ENABLE_REED_SWITCH
  bool last_door_open_{false};
#endif
};

} // namespace portunus
