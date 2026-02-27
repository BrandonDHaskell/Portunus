/**
 * @file led_hal.cpp
 * @brief Status LED HAL implementation — private to feedback_led.
 *
 * Controls a single LED via GPIO.  Active-high: GPIO HIGH = LED on.
 *
 */

#include "led_hal.h"
#include "pin_config.h"
#include "error_codes.h"

#include "driver/gpio.h"
#include "esp_log.h"

#include <stdbool.h>

static const char *TAG = "led";

/* ── Internal state ───────────────────────────────────────────────────────── */

static bool s_initialized = false;
static bool s_on          = false;

/* ── Public API ───────────────────────────────────────────────────────────── */

portunus_err_t led_init(void)
{
    if (s_initialized) {
        ESP_LOGW(TAG, "Already initialised");
        return PORTUNUS_OK;
    }

    gpio_config_t io_conf = {};
    io_conf.pin_bit_mask = (1ULL << PIN_STATUS_LED);
    io_conf.mode         = GPIO_MODE_OUTPUT;
    io_conf.pull_up_en   = GPIO_PULLUP_DISABLE;
    io_conf.pull_down_en = GPIO_PULLDOWN_DISABLE;
    io_conf.intr_type    = GPIO_INTR_DISABLE;

    esp_err_t err = gpio_config(&io_conf);
    if (err != ESP_OK) {
        ESP_LOGE(TAG, "GPIO config failed: %s", esp_err_to_name(err));
        return PORTUNUS_ERR_GPIO_INIT;
    }

    gpio_set_level(static_cast<gpio_num_t>(PIN_STATUS_LED), 0);
    s_on          = false;
    s_initialized = true;

    ESP_LOGI(TAG, "Initialised on GPIO %d", PIN_STATUS_LED);
    return PORTUNUS_OK;
}

void led_on(void)
{
    gpio_set_level(static_cast<gpio_num_t>(PIN_STATUS_LED), 1);
    s_on = true;
}

void led_off(void)
{
    gpio_set_level(static_cast<gpio_num_t>(PIN_STATUS_LED), 0);
    s_on = false;
}

void led_toggle(void)
{
    if (s_on) {
        led_off();
    } else {
        led_on();
    }
}

bool led_is_on(void)
{
    return s_on;
}
