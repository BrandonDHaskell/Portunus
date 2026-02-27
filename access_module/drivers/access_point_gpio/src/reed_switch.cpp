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

#include "reed_switch.h"
#include "pin_config.h"
#include "timing_config.h"
#include "error_codes.h"

#include "driver/gpio.h"
#include "esp_log.h"
#include "esp_timer.h"

#include <stdbool.h>

static const char *TAG = "reed_switch";

/* ── Internal state ───────────────────────────────────────────────────────── */

static bool    s_initialized       = false;
static bool    s_last_debounced    = false;  /* Last accepted debounced state */
static bool    s_candidate         = false;  /* Current candidate reading     */
static int64_t s_candidate_since_us = 0;     /* Timestamp when candidate first appeared */

/* ── Helpers ──────────────────────────────────────────────────────────────── */

/**
 * @brief Read the physical switch state accounting for NO/NC wiring.
 *
 * With internal pull-up enabled:
 *   - Pin reads HIGH when the switch circuit is open.
 *   - Pin reads LOW  when the switch circuit is closed (shorted to GND).
 *
 * For a normally-open (NO) switch (default):
 *   - Door closed → magnet aligns → switch closes → pin LOW  → "closed"
 *   - Door open   → magnet away  → switch opens  → pin HIGH → "open"
 *
 * For a normally-closed (NC) switch (CONFIG_PORTUNUS_REED_SWITCH_NC):
 *   - Door closed → magnet aligns → switch opens  → pin HIGH → "closed"
 *   - Door open   → magnet away   → switch closes → pin LOW  → "open"
 */
static bool read_physical_closed(void)
{
    int level = gpio_get_level(static_cast<gpio_num_t>(PIN_REED_SWITCH));

#if REED_SWITCH_NC
    /* NC: HIGH = switch open = door closed (magnet aligned) */
    return (level == 1);
#else
    /* NO (default): LOW = switch closed = door closed (magnet aligned) */
    return (level == 0);
#endif
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
    s_last_debounced    = read_physical_closed();
    s_candidate         = s_last_debounced;
    s_candidate_since_us = esp_timer_get_time();
    s_initialized       = true;

    ESP_LOGI(TAG, "Initialised on GPIO %d (%s), initial state: door %s",
             PIN_REED_SWITCH,
             REED_SWITCH_NC ? "normally-closed" : "normally-open",
             s_last_debounced ? "CLOSED" : "OPEN");
    return PORTUNUS_OK;
}

bool reed_switch_is_closed(void)
{
    /*
     * Non-blocking timestamp-based debounce.
     *
     * Each call reads the GPIO once and returns immediately.
     *
     *   1. If the reading matches the current candidate, check whether
     *      the candidate has been stable for REED_SWITCH_DEBOUNCE_MS.
     *      If so, accept it as the new debounced state.
     *
     *   2. If the reading differs from the candidate, reset the
     *      candidate and restart the debounce window.
     *
     * The debounced state only changes after the physical signal has
     * been continuously stable for the full debounce duration.
     */
    bool    current = read_physical_closed();
    int64_t now_us  = esp_timer_get_time();

    if (current != s_candidate) {
        /* Reading changed — start a new debounce window. */
        s_candidate          = current;
        s_candidate_since_us = now_us;
    } else if (current != s_last_debounced) {
        /* Candidate is stable and differs from accepted state —
           check whether the debounce window has elapsed. */
        int64_t elapsed_ms = (now_us - s_candidate_since_us) / 1000;
        if (elapsed_ms >= REED_SWITCH_DEBOUNCE_MS) {
            s_last_debounced = current;
            ESP_LOGD(TAG, "Debounce settled: door %s",
                     s_last_debounced ? "CLOSED" : "OPEN");
        }
    }
    /* else: reading matches both candidate and debounced — no work. */

    return s_last_debounced;
}

bool reed_switch_raw_is_closed(void)
{
    return read_physical_closed();
}