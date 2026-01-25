#pragma once
#include "portunus_module.h"
#include "portunus_event_bus.h"

namespace portunus {

class StatusLedModule final : public Module {
public:
  const char* name() const override { return "status_led"; }
  esp_err_t init() override;
  esp_err_t start(EventBus& bus) override { (void)bus; return ESP_OK; }
  void handle(const Event& ev, EventBus& bus) override;

private:
  void blink(int pulses, int on_ms, int off_ms);

  uint32_t blink_gen_{0};
};

} // namespace portunus
