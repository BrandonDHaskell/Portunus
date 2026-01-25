#pragma once
#include "portunus_module.h"
#include "portunus_event_bus.h"

extern "C" {
#include "freertos/FreeRTOS.h"
#include "freertos/task.h"
#include "freertos/queue.h"
}

namespace portunus {

class RfidReaderModule final : public Module {
public:
  const char* name() const override { return "rfid_reader"; }
  esp_err_t init() override;
  esp_err_t start(EventBus& bus) override;

private:
  static void task_entry(void* arg);
  void run();

  EventBus* bus_{nullptr};
  QueueHandle_t rfid_q_{nullptr};
  TaskHandle_t task_{nullptr};
};

} // namespace portunus
