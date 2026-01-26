#include "sdkconfig.h"
#include "reed_switch.h"

#if CONFIG_PORTUNUS_ENABLE_REED_SWITCH

#include "device_state.h"
#include "driver/gpio.h"
#include "esp_log.h"
#include "freertos/FreeRTOS.h"
#include "freertos/task.h"

static const char* TAG = "reed_switch";

static bool reed_active_low() {
#if CONFIG_PORTUNUS_REED_ACTIVE_LOW
    return true;
#else
    return false;
#endif
}

static void reed_task(void*) {
    while (true) {
        int level = gpio_get_level((gpio_num_t)CONFIG_PORTUNUS_REED_GPIO);
        bool door_open = reed_active_low() ? (level == 0) : (level == 1);
        device_state_set_door_open(door_open);
        vTaskDelay(pdMS_TO_TICKS(50));
    }
}

extern "C" void reed_switch_init() {
    gpio_config_t io_conf{};
    io_conf.pin_bit_mask = 1ULL << CONFIG_PORTUNUS_REED_GPIO;
    io_conf.mode = GPIO_MODE_INPUT;
    io_conf.pull_up_en = reed_active_low() ? GPIO_PULLUP_ENABLE : GPIO_PULLUP_DISABLE;
    io_conf.pull_down_en = reed_active_low() ? GPIO_PULLDOWN_DISABLE : GPIO_PULLDOWN_ENABLE;
    io_conf.intr_type = GPIO_INTR_DISABLE;
    gpio_config(&io_conf);

    xTaskCreate(reed_task, "reed_task", 2048, nullptr, 5, nullptr);
    ESP_LOGI(TAG, "reed switch enabled");
}

#else  // CONFIG_PORTUNUS_ENABLE_REED_SWITCH

extern "C" void reed_switch_init() {
    // Feature disabled: no-op
}

#endif