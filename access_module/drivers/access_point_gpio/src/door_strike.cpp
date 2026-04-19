/**
 * @file door_strike.cpp
 * @brief Door strike HAL implementation — private to access_point_gpio.
 *
 * Controls an electric door strike via a single GPIO pin connected to
 * a relay or MOSFET.  Supports active-high and active-low
 * configurations via Kconfig.
 *
 */

#include "door_strike.h"
#include "pin_config.h"
#include "error_codes.h"

#include "driver/gpio.h"
#include "esp_log.h"

#include <stdbool.h>

static const char *TAG = "door_strike";

/* ── Internal state ───────────────────────────────────────────────────────── */

static bool s_initialized = false;
static bool s_energized   = false;

/* ── Active level helpers ─────────────────────────────────────────────────── */

#if DOOR_STRIKE_ACTIVE_LOW
    #define STRIKE_LEVEL_ACTIVE    0
    #define STRIKE_LEVEL_INACTIVE  1
#else
    #define STRIKE_LEVEL_ACTIVE    1
    #define STRIKE_LEVEL_INACTIVE  0
#endif

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
    io_conf.mode         = GPIO_MODE_OUTPUT;
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
    gpio_set_level(static_cast<gpio_num_t>(PIN_DOOR_STRIKE), STRIKE_LEVEL_ACTIVE);
    s_energized = true;
    ESP_LOGI(TAG, "Strike ENERGIZED (unlocked)");
    return PORTUNUS_OK;
}

portunus_err_t door_strike_deenergize(void)
{
    gpio_set_level(static_cast<gpio_num_t>(PIN_DOOR_STRIKE), STRIKE_LEVEL_INACTIVE);
    s_energized = false;
    ESP_LOGI(TAG, "Strike DE-ENERGIZED (locked)");
    return PORTUNUS_OK;
}

bool door_strike_is_energized(void)
{
    return s_energized;
}
