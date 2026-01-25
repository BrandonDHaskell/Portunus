#pragma once
#include "portunus_module.h"
#include "portunus_event_bus.h"

extern "C" {
#include "esp_timer.h"
}

namespace portunus {

class DoorStrikeModule final : public Module {
public:
  const char* name() const override { return "door_strike"; }
  esp_err_t init() override;
  esp_err_t start(EventBus& bus) override { (void)bus; return ESP_OK; }
  void handle(const Event& ev, EventBus& bus) override;

private:
  static void relock_cb(void* arg);

  esp_timer_handle_t timer_{nullptr};
  bool unlocked_{false};
};

} // namespace portunus
