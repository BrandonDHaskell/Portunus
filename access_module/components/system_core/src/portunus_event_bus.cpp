#include "portunus_event_bus.h"

extern "C" {
#include "esp_log.h"
}

namespace portunus {
static const char* TAG = "event_bus";

esp_err_t EventBus::init(size_t depth) {
  if (q_) return ESP_OK;
  q_ = xQueueCreate((UBaseType_t)depth, sizeof(Event));
  if (!q_) {
    ESP_LOGE(TAG, "xQueueCreate failed");
    return ESP_ERR_NO_MEM;
  }
  return ESP_OK;
}

bool EventBus::publish(const Event& e, TickType_t timeout) {
  if (!q_) return false;
  if (xQueueSend(q_, &e, timeout) != pdTRUE) {
    // intentionally drop if full; you can add counters later
    return false;
  }
  return true;
}

bool EventBus::receive(Event& out, TickType_t timeout) {
  if (!q_) return false;
  return xQueueReceive(q_, &out, timeout) == pdTRUE;
}

} // namespace portunus
