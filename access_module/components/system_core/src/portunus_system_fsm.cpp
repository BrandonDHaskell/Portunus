#include "portunus_system_fsm.h"
#include "portunus_event.h"

extern "C" {
#include "esp_log.h"
}

namespace portunus {
static const char* TAG = "system_fsm";

void SystemFsm::task_entry(void* arg) {
  auto* self = static_cast<SystemFsm*>(arg);

  self->bus_->publish(Event::boot());
  self->bus_->publish(Event::feedback(FeedbackKind::Armed));

  Event ev{};
  while (true) {
    if (!self->bus_->receive(ev, portMAX_DELAY)) continue;

    // 1) FSM transitions / “policy brain”
    self->on_event(ev);

    // 2) Let modules react (strike, LED, etc.)
    if (self->dispatch_fn_) self->dispatch_fn_(self->registry_, ev, *self->bus_);
  }
}

esp_err_t SystemFsm::start_task(uint32_t stack, UBaseType_t prio) {
  if (!bus_ || !registry_ || !dispatch_fn_) return ESP_ERR_INVALID_STATE;
  if (task_) return ESP_OK;

  BaseType_t ok = xTaskCreate(task_entry, "system_fsm", stack, this, prio, &task_);
  return ok == pdPASS ? ESP_OK : ESP_FAIL;
}

void SystemFsm::on_event(const Event& ev) {
  switch (ev.type) {
    case EventType::Boot:
      state_ = State::Connecting;
      ESP_LOGI(TAG, "BOOT -> CONNECTING");
      break;

    case EventType::WifiConnected:
      wifi_connected_ = true;
      if (state_ == State::Connecting) {
        state_ = State::Running;
        ESP_LOGI(TAG, "CONNECTING -> RUNNING");
      }
      bus_->publish(Event::feedback(FeedbackKind::Online));
      break;

    case EventType::WifiDisconnected:
      wifi_connected_ = false;
      if (state_ == State::Running) {
        state_ = State::Connecting; // simple model; you can add DEGRADED later
        ESP_LOGW(TAG, "RUNNING -> CONNECTING (wifi lost)");
      }
      bus_->publish(Event::feedback(FeedbackKind::Offline));
      break;

    case EventType::CardScanned:
      if (state_ != State::Running) break;

      // Always publish AccessRequest for future server module.
      {
        Event req = ev;
        req.type = EventType::AccessRequest;
        bus_->publish(req);
      }

      if (!wifi_connected_) {
        bus_->publish(Event::feedback(FeedbackKind::AccessDenied));
        break;
      }

      // Temporary local policy: auto-grant for skeleton demo.
      bus_->publish(Event::feedback(FeedbackKind::AccessGranted));
      bus_->publish(Event::unlock_requested(3000));
      break;

    default:
      break;
  }
}

} // namespace portunus
