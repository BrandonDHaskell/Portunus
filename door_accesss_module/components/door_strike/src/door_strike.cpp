#include "door_strike.h"

#include "device_state.h"
#include "driver/gpio.h"

static bool is_active_high() {
#if CONFIG_PORTUNUS_STRIKE_ACTIVE_HIGH
    return true;
#else
    return false;
#endif
}

extern "C" void door_strike_init() {
    gpio_config_t io_conf{};
    io_conf.pin_bit_mask = 1ULL << CONFIG_PORTUNUS_STRIKE_GPIO;
    io_conf.mode = GPIO_MODE_OUTPUT;
    io_conf.pull_up_en = GPIO_PULLUP_DISABLE;
    io_conf.pull_down_en = GPIO_PULLDOWN_DISABLE;
    io_conf.intr_type = GPIO_INTR_DISABLE;
    gpio_config(&io_conf);

    // Default: locked
    door_strike_set_unlocked(false);
}

extern "C" void door_strike_set_unlocked(bool unlocked) {
    const int level = (unlocked ? 1 : 0) ^ (is_active_high() ? 0 : 1);
    gpio_set_level((gpio_num_t)CONFIG_PORTUNUS_STRIKE_GPIO, level);
    device_state_set_strike_unlocked(unlocked);
}
