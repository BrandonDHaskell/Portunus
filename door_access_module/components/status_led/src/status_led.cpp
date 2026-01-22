#include "sdkconfig.h"
#include "status_led.h"

#if CONFIG_PORTUNUS_ENABLE_STATUS_LED

#include "driver/gpio.h"
#include "esp_log.h"

static const char* TAG = "status_led";

static bool led_active_high() {
#if CONFIG_PORTUNUS_STATUS_LED_ACTIVE_HIGH
    return true;
#else
    return false;
#endif
}

extern "C" void status_led_init() {
    gpio_config_t io_conf{};
    io_conf.pin_bit_mask = 1ULL << CONFIG_PORTUNUS_STATUS_LED_GPIO;
    io_conf.mode = GPIO_MODE_OUTPUT;
    io_conf.pull_up_en = GPIO_PULLUP_DISABLE;
    io_conf.pull_down_en = GPIO_PULLDOWN_DISABLE;
    io_conf.intr_type = GPIO_INTR_DISABLE;
    gpio_config(&io_conf);

    status_led_set(false);
    ESP_LOGI(TAG, "status LED enabled");
}

extern "C" void status_led_set(bool on) {
    const int level = (on ? 1 : 0) ^ (led_active_high() ? 0 : 1);
    gpio_set_level((gpio_num_t)CONFIG_PORTUNUS_STATUS_LED_GPIO, level);
}

#else  // CONFIG_PORTUNUS_ENABLE_STATUS_LED

extern "C" void status_led_init() {}
extern "C" void status_led_set(bool) {}

#endif
