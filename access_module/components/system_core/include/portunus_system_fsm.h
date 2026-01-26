#pragma once
#include "portunus_event_bus.h"
#include "portunus_module_registry.h"

extern "C" {
#include "freertos/FreeRTOS.h"
#include "freertos/task.h"
}

namespace portunus {

class SystemFsm {
public:
  template <size_t N>
  esp_err_t start(EventBus& bus, ModuleRegistry<N>& registry,
                  uint32_t stack = 4096, UBaseType_t prio = 7) {
    bus_ = &bus;
    // type-erase registry via function pointer
    dispatch_fn_ = [](void* reg, const Event& ev, EventBus& b) {
      static_cast<ModuleRegistry<N>*>(reg)->dispatch(ev, b);
    };
    registry_ = &registry;
    return start_task(stack, prio);
  }

private:
  enum class State : uint8_t { Boot, Connecting, Running };

  EventBus* bus_{nullptr};
  void* registry_{nullptr};
  void (*dispatch_fn_)(void*, const Event&, EventBus&) = nullptr;

  TaskHandle_t task_{nullptr};
  State state_{State::Boot};
  bool wifi_connected_{false};

  static void task_entry(void* arg);
  esp_err_t start_task(uint32_t stack, UBaseType_t prio);

  void on_event(const Event& ev);
};

} // namespace portunus
