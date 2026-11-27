/**
 * @file door_strike.cpp
 * @brief Door strike HAL implementation — private to access_point_gpio.
 *
 * Controls an electric door strike via a single GPIO pin connected to
 * a relay or MOSFET.  Supports active-high and active-low
 * configurations via Kconfig.
 *
 */

#include "door_strike.hpp"
#include "door_strike_levels.hpp"
#include "pin_config.hpp"
#include "error_codes.hpp"

#include "driver/gpio.h"
#include "esp_log.h"

#include <stdbool.h>

static const char *TAG = "door_strike";

/* ── Internal state ───────────────────────────────────────────────────────── */

static bool s_initialized = false;
static bool s_energized   = false;

/* ── Active level helpers ─────────────────────────────────────────────────── */

static constexpr int STRIKE_LEVEL_ACTIVE   = strike_level(DOOR_STRIKE_ACTIVE_LOW, true);
static constexpr int STRIKE_LEVEL_INACTIVE = strike_level(DOOR_STRIKE_ACTIVE_LOW, false);

/* ── Public API ───────────────────────────────────────────────────────────── */

portunus_err_t door_strike_init(void)
{
    if (s_initialized) {
        ESP_LOGW(TAG, "Already initialised");
        return PORTUNUS_OK;
    }

    /* Set safe inactive level in the output register *before* enabling the pin
       as an output.  Without this, active-low configurations briefly pulse the
       actuator between gpio_config (output enabled, level defaults to LOW) and
       the subsequent gpio_set_level call. */
    gpio_set_level(static_cast<gpio_num_t>(PIN_DOOR_STRIKE), STRIKE_LEVEL_INACTIVE);

    gpio_config_t io_conf = {};
    io_conf.pin_bit_mask = (1ULL << PIN_DOOR_STRIKE);
    /* INPUT_OUTPUT, not OUTPUT: gpio_get_level() reads the pad through the
       input buffer, and IDF returns 0 for a pad without input enabled. With
       plain OUTPUT the de-energize readback was vacuous on active-high wiring
       and always failed on active-low wiring (F1). */
    io_conf.mode         = GPIO_MODE_INPUT_OUTPUT;
    io_conf.pull_up_en   = GPIO_PULLUP_DISABLE;
    io_conf.pull_down_en = GPIO_PULLDOWN_DISABLE;
    io_conf.intr_type    = GPIO_INTR_DISABLE;

    esp_err_t err = gpio_config(&io_conf);
    if (err != ESP_OK) {
        ESP_LOGE(TAG, "GPIO config failed: %s", esp_err_to_name(err));
        return PORTUNUS_ERR_GPIO_INIT;
    }
    s_energized   = false;
    s_initialized = true;

    ESP_LOGI(TAG, "Initialised on GPIO %d (active-%s)",
             PIN_DOOR_STRIKE, DOOR_STRIKE_ACTIVE_LOW ? "low" : "high");
    return PORTUNUS_OK;
}

portunus_err_t door_strike_energize(void)
{
    esp_err_t err = gpio_set_level(static_cast<gpio_num_t>(PIN_DOOR_STRIKE), STRIKE_LEVEL_ACTIVE);
    if (err != ESP_OK) {
        ESP_LOGE(TAG, "Strike energize gpio_set_level failed: %s", esp_err_to_name(err));
        return PORTUNUS_ERR_GPIO_INIT;
    }
    /* Read the pad back: if it did not reach the active level, the unlock
     * signal never left the module (pin held by a short or a miswired
     * driver stage), so report it rather than a grant. This checks the
     * signal sent, not that the strike moved: there is no strike feedback. */
    const int readback = gpio_get_level(static_cast<gpio_num_t>(PIN_DOOR_STRIKE));
    if (!strike_readback_matches(DOOR_STRIKE_ACTIVE_LOW, true, readback)) {
        ESP_LOGE(TAG, "Strike energize readback mismatch: expected %d got %d; unlock signal not sent",
                 STRIKE_LEVEL_ACTIVE, readback);
        gpio_set_level(static_cast<gpio_num_t>(PIN_DOOR_STRIKE), STRIKE_LEVEL_INACTIVE);
        s_energized = false;
        return PORTUNUS_ERR_GPIO_INIT;
    }
    s_energized = true;
    ESP_LOGI(TAG, "Strike ENERGIZED (unlocked)");
    return PORTUNUS_OK;
}

portunus_err_t door_strike_deenergize(void)
{
    esp_err_t err = gpio_set_level(static_cast<gpio_num_t>(PIN_DOOR_STRIKE), STRIKE_LEVEL_INACTIVE);
    if (err != ESP_OK) {
        ESP_LOGE(TAG, "Strike de-energize gpio_set_level failed: %s", esp_err_to_name(err));
        return PORTUNUS_ERR_GPIO_INIT;
    }
    /* Read the pad back to confirm the write landed. */
    const int readback = gpio_get_level(static_cast<gpio_num_t>(PIN_DOOR_STRIKE));
    if (!strike_readback_matches(DOOR_STRIKE_ACTIVE_LOW, false, readback)) {
        ESP_LOGE(TAG, "Strike de-energize readback mismatch: expected %d got %d; door may be unlocked",
                 STRIKE_LEVEL_INACTIVE, readback);
        s_energized = true; /* reflect actual hardware state */
        return PORTUNUS_ERR_GPIO_INIT;
    }
    s_energized = false;
    ESP_LOGI(TAG, "Strike DE-ENERGIZED (locked)");
    return PORTUNUS_OK;
}

bool door_strike_is_energized(void)
{
    return s_energized;
}
