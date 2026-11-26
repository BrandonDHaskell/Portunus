/**
 * @file feedback_led.cpp
 * @brief IFeedback implementation using a single status LED.
 *
 * The pattern task loops at LED_TICK_MS.  On each tick it:
 *   1. Takes a new pattern from xTaskNotifyWait, if any.
 *   2. Asks the pure engine (led_pattern.cpp) for this tick's level and
 *      drives the LED only when the level changes.
 *
 * A new indicate() call sends a task notification that is picked up
 * on the next tick, immediately preempting any in-progress pattern.
 */

#include "feedback_led.hpp"
#include "led_hal.hpp"       /* internal HAL */
#include "led_pattern.hpp"
#include "error_codes.hpp"

#include <inttypes.h>
#include "esp_log.h"
#include "freertos/FreeRTOS.h"
#include "freertos/task.h"

#include <climits>

static const char *TAG = "feedback_led";

/* ── Task configuration ───────────────────────────────────────────────────── */

static const int PATTERN_TASK_STACK = 2048;
static const int PATTERN_TASK_PRIORITY = 3;

/* ── Implementation ───────────────────────────────────────────────────────── */

portunus_err_t FeedbackLed::init()
{
    ESP_LOGI(TAG, "Initialising LED feedback driver");

    portunus_err_t err = led_init();
    if (err != PORTUNUS_OK) {
        ESP_LOGE(TAG, "LED HAL init failed: 0x%" PRIx32, (uint32_t)err);
        return err;
    }

    BaseType_t ret = xTaskCreate(
        pattern_task,
        "led_pattern",
        PATTERN_TASK_STACK,
        this,
        PATTERN_TASK_PRIORITY,
        &m_task_handle
    );

    if (ret != pdPASS) {
        ESP_LOGE(TAG, "Failed to create pattern task");
        return PORTUNUS_ERR_TASK_CREATE;
    }

    ESP_LOGI(TAG, "LED feedback driver initialised");
    return PORTUNUS_OK;
}

void FeedbackLed::indicate(feedback_type_t type)
{
    if (m_task_handle == nullptr) {
        return;
    }

    xTaskNotify(m_task_handle,
                static_cast<uint32_t>(type),
                eSetValueWithOverwrite);
}

void FeedbackLed::pattern_task(void *arg)
{
    auto *self = static_cast<FeedbackLed *>(arg);
    self->run_patterns();
}

void FeedbackLed::run_patterns()
{
    led_engine_t engine;
    bool         lit = false;

    led_off();
    for (;;) {
        uint32_t notify_value = 0;
        if (xTaskNotifyWait(0, ULONG_MAX, &notify_value,
                            pdMS_TO_TICKS(LED_TICK_MS)) == pdTRUE) {
            led_engine_indicate(engine, static_cast<feedback_type_t>(notify_value));
        }

        const bool want = led_engine_step(engine);
        if (want != lit) {
            want ? led_on() : led_off();
            lit = want;
        }
    }
}
