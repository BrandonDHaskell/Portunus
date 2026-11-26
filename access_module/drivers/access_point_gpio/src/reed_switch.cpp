/**
 * @file reed_switch.cpp
 * @brief Reed switch HAL implementation — private to access_point_gpio.
 *
 * Reads a magnetic reed switch via GPIO with software debounce.
 * The switch is expected to short the input pin to GND when activated
 * (internal pull-up is enabled).
 *
 * Debounce strategy: non-blocking, timestamp-based.  Each call to
 * reed_switch_is_closed() performs a single GPIO read and compares
 * against a candidate state.  If the candidate has been stable for
 * at least REED_SWITCH_DEBOUNCE_MS (wall-clock time via esp_timer),
 * it is accepted as the new debounced state.  If the reading
 * disagrees with the candidate, the candidate resets.
 *
 * This function returns immediately on every call — it never blocks
 * or sleeps.  The debounce window spans multiple caller poll ticks
 * rather than blocking inside a single tick.
 *
 */

#include "reed_switch.hpp"
#include "debounce.hpp"
#include "pin_config.hpp"
#include "timing_config.hpp"
#include "error_codes.hpp"

#include "driver/gpio.h"
#include "esp_log.h"
#include "esp_timer.h"

#include <stdbool.h>

static const char *TAG = "reed_switch";

/* ── Internal state ───────────────────────────────────────────────────────── */

static bool        s_initialized = false;
static debouncer_t s_debounce    = {};

/* ── Helpers ──────────────────────────────────────────────────────────────── */

/** Read the physical switch state accounting for NO/NC wiring
 *  (see reed_level_is_closed). */
static bool read_physical_closed(void)
{
    return reed_level_is_closed(gpio_get_level(static_cast<gpio_num_t>(PIN_REED_SWITCH)),
                                REED_SWITCH_NC);
}

/* ── Public API ───────────────────────────────────────────────────────────── */

portunus_err_t reed_switch_init(void)
{
    if (s_initialized) {
        ESP_LOGW(TAG, "Already initialised");
        return PORTUNUS_OK;
    }

    gpio_config_t io_conf = {};
    io_conf.pin_bit_mask = (1ULL << PIN_REED_SWITCH);
    io_conf.mode         = GPIO_MODE_INPUT;
    io_conf.pull_up_en   = GPIO_PULLUP_ENABLE;
    io_conf.pull_down_en = GPIO_PULLDOWN_DISABLE;
    io_conf.intr_type    = GPIO_INTR_DISABLE;

    esp_err_t err = gpio_config(&io_conf);
    if (err != ESP_OK) {
        ESP_LOGE(TAG, "GPIO config failed: %s", esp_err_to_name(err));
        return PORTUNUS_ERR_GPIO_INIT;
    }

    /* Take an initial reading so debounce state starts valid. */
    debouncer_init(s_debounce, read_physical_closed(), esp_timer_get_time());
    s_initialized = true;

    ESP_LOGI(TAG, "Initialised on GPIO %d (%s), initial state: door %s",
             PIN_REED_SWITCH,
             REED_SWITCH_NC ? "normally-closed" : "normally-open",
             s_debounce.stable ? "CLOSED" : "OPEN");
    return PORTUNUS_OK;
}

bool reed_switch_is_closed(void)
{
    /* One GPIO read per call; the debounce window spans caller poll ticks. */
    const bool was = s_debounce.stable;
    const bool now = debouncer_update(s_debounce, read_physical_closed(), esp_timer_get_time(),
                                      REED_SWITCH_DEBOUNCE_MS);
    if (now != was) {
        ESP_LOGD(TAG, "Debounce settled: door %s", now ? "CLOSED" : "OPEN");
    }
    return now;
}

bool reed_switch_raw_is_closed(void)
{
    return read_physical_closed();
}