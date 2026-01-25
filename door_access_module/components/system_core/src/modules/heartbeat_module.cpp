#include "modules/heartbeat_module.h"

extern "C" {
#include "heartbeat.h"
}

namespace portunus {

esp_err_t HeartbeatModule::start(EventBus&) {
  heartbeat_start();
  return ESP_OK;
}

} // namespace portunus
