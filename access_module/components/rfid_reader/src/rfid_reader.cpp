#include "rfid_reader.h"

#include <cstring>

extern "C" {
#include "esp_log.h"
#include "esp_timer.h"
#include "freertos/task.h"
}

namespace portunus {

static const char* TAG = "rfid_reader";

struct TaskCtx {
  RfidReaderConfig cfg{};
  Mfrc522 reader{};
  QueueHandle_t q = nullptr;

  RfidUid last_uid{};
  int64_t last_emit_us = 0;
};

static uint32_t uptime_ms() {
  return static_cast<uint32_t>(esp_timer_get_time() / 1000);
}

static bool uid_equal(const RfidUid& a, const RfidUid& b) {
  if (a.size != b.size) return false;
  return std::memcmp(a.bytes.data(), b.bytes.data(), a.size) == 0;
}

static void task_fn(void* arg) {
  auto* ctx = static_cast<TaskCtx*>(arg);

  if (ctx->reader.init(ctx->cfg.spi_host, ctx->cfg.pins, ctx->cfg.spi_clock_hz) != ESP_OK) {
    ESP_LOGE(TAG, "MFRC522 init failed");
    vTaskDelete(nullptr);
    return;
  }

  while (true) {
    RfidUid uid{};
    if (ctx->reader.read_uid(uid)) {
      const int64_t now_us = esp_timer_get_time();
      const int64_t window_us = static_cast<int64_t>(ctx->cfg.dedupe_window_ms) * 1000;

      const bool is_dup = uid_equal(uid, ctx->last_uid) && ((now_us - ctx->last_emit_us) < window_us);
      if (!is_dup) {
        ctx->last_uid = uid;
        ctx->last_emit_us = now_us;

        RfidEvent ev{};
        ev.uid = uid;
        ev.uptime_ms = uptime_ms();

        (void)xQueueSend(ctx->q, &ev, 0);
        ESP_LOGI(TAG, "Card UID: %02X%02X%02X%02X",
                 uid.bytes[0], uid.bytes[1], uid.bytes[2], uid.bytes[3]);
      }
    }

    vTaskDelay(pdMS_TO_TICKS(ctx->cfg.poll_ms));
  }
}

esp_err_t rfid_reader_start(const RfidReaderConfig& cfg, QueueHandle_t* out_queue) {
  if (!out_queue) return ESP_ERR_INVALID_ARG;

  auto* ctx = new TaskCtx{};
  ctx->cfg = cfg;
  ctx->q = xQueueCreate(8, sizeof(RfidEvent));
  if (!ctx->q) {
    delete ctx;
    return ESP_ERR_NO_MEM;
  }

  *out_queue = ctx->q;

  BaseType_t ok = xTaskCreatePinnedToCore(
      task_fn,
      "rfid_reader",
      4096,
      ctx,
      5,
      nullptr,
      0);

  if (ok != pdPASS) {
    vQueueDelete(ctx->q);
    delete ctx;
    return ESP_FAIL;
  }

  return ESP_OK;
}

} // namespace portunus