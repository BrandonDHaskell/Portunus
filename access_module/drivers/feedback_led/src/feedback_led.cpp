/**
 * @file feedback_led.cpp
 * @brief IFeedback implementation using a single status LED.
 *
 * The pattern task loops at a 50ms tick rate.  On each tick it:
 *   1. Checks for a new pattern via xTaskNotifyWait (non-blocking).
 *   2. Advances the current pattern state machine by one tick.
 *
 * A new indicate() call sends a task notification that is picked up
 * on the next tick, immediately preempting any in-progress pattern.
 */

#include "feedback_led.h"
#include "led_hal.h"       /* internal HAL */
#include "error_codes.h"

#include "esp_log.h"
#include "freertos/FreeRTOS.h"
#include "freertos/task.h"

#include <climits>

static const char *TAG = "feedback_led";

/* ── Pattern timing (in ticks, 1 tick = 50ms) ────────────────────────────── */

static const TickType_t TICK_MS = 50;

/* ACCESS_GRANTED: solid on for 1000ms = 20 ticks */
static const int GRANTED_ON_TICKS = 20;

/* ACCESS_DENIED: 3× blink, 100ms on + 100ms off = 12 ticks */
static const int DENIED_BLINK_ON_TICKS  = 2;
static const int DENIED_BLINK_OFF_TICKS = 2;
static const int DENIED_BLINK_COUNT     = 3;

/* SYSTEM_READY: short blink every 3s = 1 tick on + 59 ticks off */
static const int READY_ON_TICKS    = 1;
static const int READY_CYCLE_TICKS = 60;

/* SYSTEM_ERROR: rapid blink 200ms on/off = 4 ticks on + 4 ticks off */
static const int ERROR_HALF_CYCLE_TICKS = 4;

/* ── Task configuration ───────────────────────────────────────────────────── */

static const int PATTERN_TASK_STACK = 2048;
static const int PATTERN_TASK_PRIORITY = 3;

/* ── Implementation ───────────────────────────────────────────────────────── */

portunus_err_t FeedbackLed::init()
{
    ESP_LOGI(TAG, "Initialising LED feedback driver");

    portunus_err_t err = led_init();
    if (err != PORTUNUS_OK) {
        ESP_LOGE(TAG, "LED HAL init failed: 0x%04x", err);
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
    feedback_type_t current = feedback_type_t::NONE;
    int tick = 0;

    for (;;) {
        uint32_t notify_value = 0;
        BaseType_t got_notify = xTaskNotifyWait(
            0,
            ULONG_MAX,
            &notify_value,
            pdMS_TO_TICKS(TICK_MS)
        );

        if (got_notify == pdTRUE) {
            current = static_cast<feedback_type_t>(notify_value);
            tick = 0;

            if (current == feedback_type_t::NONE) {
                led_off();
                continue;
            }
        }

        switch (current) {
        case feedback_type_t::NONE:
            break;

        case feedback_type_t::ACCESS_GRANTED:
            if (tick == 0) {
                led_on();
            }
            if (tick >= GRANTED_ON_TICKS) {
                led_off();
                current = feedback_type_t::NONE;
            }
            break;

        case feedback_type_t::ACCESS_DENIED: {
            int cycle_len = DENIED_BLINK_ON_TICKS + DENIED_BLINK_OFF_TICKS;
            int total_ticks = cycle_len * DENIED_BLINK_COUNT;

            if (tick < total_ticks) {
                int pos_in_cycle = tick % cycle_len;
                if (pos_in_cycle < DENIED_BLINK_ON_TICKS) {
                    led_on();
                } else {
                    led_off();
                }
            } else {
                led_off();
                current = feedback_type_t::NONE;
            }
            break;
        }

        case feedback_type_t::SYSTEM_READY:
            {
                int pos = tick % READY_CYCLE_TICKS;
                if (pos < READY_ON_TICKS) {
                    led_on();
                } else {
                    led_off();
                }
            }
            break;

        case feedback_type_t::SYSTEM_ERROR:
            {
                int pos = tick % (ERROR_HALF_CYCLE_TICKS * 2);
                if (pos < ERROR_HALF_CYCLE_TICKS) {
                    led_on();
                } else {
                    led_off();
                }
            }
            break;

        case feedback_type_t::CARD_READ:
            if (tick == 0) {
                led_on();
            }
            break;
        }

        tick++;
    }
}
