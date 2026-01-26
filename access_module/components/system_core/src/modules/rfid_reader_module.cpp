#include "modules/rfid_reader_module.h"
#include "portunus_event.h"

extern "C" {
#include "esp_log.h"
}

#include "rfid_reader.h"  // C++ header in your repo

namespace portunus {
static const char* TAG = "rfid_mod";

static inline UidBytes to_uidbytes(const portunus::RfidUid& uid) {
  UidBytes out{};
  out.size = uid.size;
  for (int i = 0; i < 10; ++i) out.bytes[i] = uid.bytes[(size_t)i];
  return out;
}

esp_err_t RfidReaderModule::init() {
  portunus::RfidReaderConfig cfg{};
  return portunus::rfid_reader_start(cfg, &rfid_q_);
}

esp_err_t RfidReaderModule::start(EventBus& bus) {
  bus_ = &bus;
  if (task_) return ESP_OK;
  BaseType_t ok = xTaskCreate(task_entry, "rfid_evt", 4096, this, 6, &task_);
  return ok == pdPASS ? ESP_OK : ESP_FAIL;
}

void RfidReaderModule::task_entry(void* arg) {
  static_cast<RfidReaderModule*>(arg)->run();
}

void RfidReaderModule::run() {
  portunus::RfidEvent ev{};
  while (true) {
    if (xQueueReceive(rfid_q_, &ev, portMAX_DELAY) == pdTRUE) {
      Event e{};
      e.type = EventType::CardScanned;
      e.ts_us = Event::now_us();
      e.arg0 = ev.uptime_ms;
      e.uid = to_uidbytes(ev.uid);
      bus_->publish(e);
      ESP_LOGI(TAG, "CardScanned uid_size=%u uptime_ms=%u", e.uid.size, e.arg0);
    }
  }
}

} // namespace portunus
