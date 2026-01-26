#pragma once
#include "portunus_module.h"
#include "portunus_event_bus.h"

namespace portunus {

class HeartbeatModule final : public Module {
public:
  const char* name() const override { return "heartbeat"; }
  esp_err_t init() override { return ESP_OK; }
  esp_err_t start(EventBus& bus) override;
};

} // namespace portunus
