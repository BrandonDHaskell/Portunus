/**
 * @file led_hal.h
 * @brief Status LED HAL — private to feedback_led.
 *
 * GPIO-based driver for a single status LED.  Provides raw on/off/toggle
 * control.  Pattern logic (blink rates, pulse sequences) lives in the
 * feedback_module's LedFeedback implementation, not here.
 *
 * This is an internal HAL header, private to the feedback_led driver.
 * FeedbackLed exposes this functionality via IFeedback.
 *
 */

#pragma once

#include "portunus_types.h"

#ifdef __cplusplus
extern "C" {
#endif

/**
 * @brief Initialise the status LED GPIO.
 *
 * Configures the pin as a push-pull output and sets it LOW (off).
 *
 * @return PORTUNUS_OK on success, or an error code if GPIO
 *         configuration fails.
 */
portunus_err_t led_init(void);

/**
 * @brief Turn the LED on (GPIO HIGH).
 */
void led_on(void);

/**
 * @brief Turn the LED off (GPIO LOW).
 */
void led_off(void);

/**
 * @brief Toggle the LED state.
 */
void led_toggle(void);

/**
 * @brief Read the current LED state.
 *
 * @return true if the LED is currently on.
 */
bool led_is_on(void);

#ifdef __cplusplus
}
#endif
