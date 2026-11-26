#include "arm_button_gpio.hpp"
#include "arm_edge.hpp"
#include "error_codes.hpp"
#include "driver/gpio.h"
#include "esp_log.h"

static const char *TAG = "arm_button";

ArmButtonGpio::ArmButtonGpio(gpio_num_t pin, bool active_low)
    : m_pin(pin), m_active_low(active_low)
{}

portunus_err_t ArmButtonGpio::init()
{
    gpio_config_t cfg = {};
    cfg.pin_bit_mask  = (1ULL << m_pin);
    cfg.mode          = GPIO_MODE_INPUT;
    cfg.pull_up_en    = m_active_low ? GPIO_PULLUP_ENABLE : GPIO_PULLUP_DISABLE;
    cfg.pull_down_en  = m_active_low ? GPIO_PULLDOWN_DISABLE : GPIO_PULLDOWN_ENABLE;
    cfg.intr_type     = GPIO_INTR_DISABLE;

    esp_err_t ret = gpio_config(&cfg);
    if (ret != ESP_OK) {
        ESP_LOGE(TAG, "gpio_config failed: %s", esp_err_to_name(ret));
        return PORTUNUS_ERR_GPIO_INIT;
    }

    ESP_LOGI(TAG, "arm button on GPIO %d (active %s)", m_pin, m_active_low ? "low" : "high");
    return PORTUNUS_OK;
}

bool ArmButtonGpio::poll_arm()
{
    return arm_edge_step(m_edge, arm_level_pressed(gpio_get_level(m_pin), m_active_low));
}
