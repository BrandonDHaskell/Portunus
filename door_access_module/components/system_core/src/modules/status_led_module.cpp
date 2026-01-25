#include "modules/status_led_module.h"
#include "portunus_event.h"

extern "C" {
#include "status_led.h"
#include "freertos/FreeRTOS.h"
#include "freertos/task.h"
}

namespace portunus {

esp_err_t StatusLedModule::init() {
  status_led_init();
  status_led_set(false);
  return ESP_OK;
}

void StatusLedModule::handle(const Event& ev, EventBus&) {
  if (ev.type != EventType::Feedback) return;

  const auto k = (FeedbackKind)ev.arg0;
  switch (k) {
    case FeedbackKind::Armed:         status_led_set(true); break;
    case FeedbackKind::Online:        blink(2, 80, 80); break;
    case FeedbackKind::Offline:       status_led_set(false); break;
    case FeedbackKind::AccessGranted: blink(3, 60, 60); break;
    case FeedbackKind::AccessDenied:  blink(1, 200, 80); break;
    default:                          blink(5, 40, 40); break;
  }
}

static void blink_task(void* arg) {
  auto* p = static_cast<uint32_t*>(arg);
  // packed: [gen(16bits)][pulses(8bits)][on_ms/10(8bits)] etc… keep it simple? nope.
  // We’ll just not use arg packing; easiest is lambda capture, but no.
  vTaskDelete(nullptr);
}

void StatusLedModule::blink(int pulses, int on_ms, int off_ms) {
  // Minimal: do blocking blink in a short-lived task, with a generation guard.
  ++blink_gen_;
  const uint32_t gen = blink_gen_;

  struct Ctx { StatusLedModule* self; uint32_t gen; int pulses; int on_ms; int off_ms; };
  auto* ctx = new Ctx{this, gen, pulses, on_ms, off_ms};

  xTaskCreate(
    [](void* p) {
      auto* c = static_cast<Ctx*>(p);
      for (int i = 0; i < c->pulses; ++i) {
        if (c->self->blink_gen_ != c->gen) break;
        status_led_set(true);
        vTaskDelay(pdMS_TO_TICKS(c->on_ms));
        status_led_set(false);
        vTaskDelay(pdMS_TO_TICKS(c->off_ms));
      }
      delete c;
      vTaskDelete(nullptr);
    },
    "led_blink", 2048, ctx, 5, nullptr
  );
}

} // namespace portunus
