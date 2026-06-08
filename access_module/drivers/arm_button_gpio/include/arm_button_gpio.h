#pragma once

#include "i_arm.h"
#include "driver/gpio.h"

/**
 * @brief GPIO momentary-button implementation of IArm.
 *
 * Polls a GPIO pin and returns a single true on the leading edge of a press,
 * after debounce. Active-low (pulled high, pressed = low) by default; set
 * active_low=false for active-high wiring.
 *
 * Call poll_arm() at a regular interval (100 ms recommended). Returns true
 * exactly once per press — the count resets after the button is released.
 */
class ArmButtonGpio : public IArm {
public:
    /**
     * @param pin         GPIO number connected to the button.
     * @param active_low  true = button pulls pin low when pressed (default).
     */
    explicit ArmButtonGpio(gpio_num_t pin, bool active_low = true);

    portunus_err_t init() override;
    bool poll_arm() override;

private:
    static constexpr int k_debounce_threshold = 3; // consecutive same-level reads required

    gpio_num_t m_pin;
    bool       m_active_low;
    int        m_debounce_count = 0;
};
