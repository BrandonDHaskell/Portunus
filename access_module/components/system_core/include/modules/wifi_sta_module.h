#pragma once
#include "portunus_module.h"
#include "portunus_event_bus.h"

extern "C" {
#include "freertos/FreeRTOS.h"
#include "freertos/task.h"
}

namespace portunus {

class WifiStaModule final : public Module {
public:
  const char* name() const override { return "wifi_sta"; }
  esp_err_t init() override;
  esp_err_t start(EventBus& bus) override;

private:
  static void task_entry(void* arg);
  void run();

  EventBus* bus_{nullptr};
  TaskHandle_t task_{nullptr};
  bool last_connected_{false};
};

} // namespace portunus
