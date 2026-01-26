#include "modules/door_strike_module.h"
#include "portunus_event.h"

extern "C" {
#include "door_strike.h"
#include "device_state.h"
#include "esp_log.h"
}

namespace portunus {
static const char* TAG = "strike_mod";

esp_err_t DoorStrikeModule::init() {
  door_strike_init();

  esp_timer_create_args_t args{};
  args.callback = &DoorStrikeModule::relock_cb;
  args.arg = this;
  args.dispatch_method = ESP_TIMER_TASK;
  args.name = "strike_relock";
  return esp_timer_create(&args, &timer_);
}

void DoorStrikeModule::handle(const Event& ev, EventBus&) {
  if (ev.type == EventType::UnlockRequested) {
    const uint32_t ms = ev.arg0;

    ESP_LOGI(TAG, "Unlock for %u ms", ms);
    esp_timer_stop(timer_);

    door_strike_set_unlocked(true);
    device_state_set_strike_unlocked(true);
    unlocked_ = true;

    esp_timer_start_once(timer_, (uint64_t)ms * 1000ULL);
  }

  if (ev.type == EventType::LockRequested) {
    ESP_LOGI(TAG, "LockRequested");
    esp_timer_stop(timer_);
    door_strike_set_unlocked(false);
    device_state_set_strike_unlocked(false);
    unlocked_ = false;
  }
}

void DoorStrikeModule::relock_cb(void* arg) {
  auto* self = static_cast<DoorStrikeModule*>(arg);
  if (!self->unlocked_) return;
  ESP_LOGI(TAG, "Relocking");
  door_strike_set_unlocked(false);
  device_state_set_strike_unlocked(false);
  self->unlocked_ = false;
}

} // namespace portunus
