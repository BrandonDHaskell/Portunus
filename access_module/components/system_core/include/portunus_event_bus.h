#pragma once
#include "portunus_event.h"

extern "C" {
#include "freertos/FreeRTOS.h"
#include "freertos/queue.h"
#include "esp_err.h"
}

namespace portunus {

class EventBus {
public:
  esp_err_t init(size_t depth);
  bool publish(const Event& e, TickType_t timeout = 0);
  bool receive(Event& out, TickType_t timeout = portMAX_DELAY);

private:
  QueueHandle_t q_{nullptr};
};

} // namespace portunus
