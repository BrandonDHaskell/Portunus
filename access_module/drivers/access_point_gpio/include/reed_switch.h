/**
 * @file reed_switch.h
 * @brief Reed switch HAL — private to access_point_gpio.
 *
 * GPIO-based driver for reading a magnetic reed switch that detects
 * whether a door is physically open or closed.  Includes software
 * debounce to prevent spurious state changes from vibration or
 * magnetic interference.
 *
 * Supports normally-open (NO) and normally-closed (NC) switch types
 * via Kconfig (CONFIG_PORTUNUS_REED_SWITCH_NC).
 *
 * This is an internal HAL header, private to the access_point_gpio driver.
 * AccessPointGpio exposes this functionality via IAccessPoint.
 *
 */

#pragma once

#include "portunus_types.h"

#ifdef __cplusplus
extern "C" {
#endif

/**
 * @brief Initialise the reed switch GPIO.
 *
 * Configures the pin as an input with an internal pull-up resistor
 * enabled.  The switch should short the pin to GND when activated.
 *
 * @return PORTUNUS_OK on success, or an error code if GPIO
 *         configuration fails.
 */
portunus_err_t reed_switch_init(void);

/**
 * @brief Read the debounced door-closed state.
 *
 * Performs a single GPIO read and applies timestamp-based debounce.
 * The debounced state changes only after the physical signal has
 * been continuously stable for CONFIG_PORTUNUS_REED_SWITCH_DEBOUNCE_MS
 * (wall-clock time).
 *
 * This function returns immediately — it never blocks or sleeps.
 * The debounce window spans multiple calls rather than blocking
 * inside a single call.  For correct operation, the caller should
 * invoke this function at a regular interval (e.g., the FSM poll
 * tick) that is shorter than the debounce duration.
 *
 * @return true if the door is closed (magnet aligned with sensor),
 *         false if the door is open.
 */
bool reed_switch_is_closed(void);

/**
 * @brief Read the raw (non-debounced) GPIO level.
 *
 * For diagnostic purposes only.  Production code should use
 * reed_switch_is_closed() which includes debounce logic.
 *
 * @return true if the switch circuit is closed (door shut),
 *         false if open.
 */
bool reed_switch_raw_is_closed(void);

#ifdef __cplusplus
}
#endif