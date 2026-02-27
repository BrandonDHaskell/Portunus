/**
 * @file door_strike.h
 * @brief Door strike HAL — private to access_point_gpio.
 *
 * GPIO-based driver for controlling an electric door strike via a
 * relay or MOSFET.  The driver manages a single output pin and knows
 * nothing about timing or access policy — it simply energizes or
 * de-energizes the strike on command.
 *
 * Fail-secure by default: init() leaves the strike de-energized
 * (locked).  The active level (HIGH or LOW) is configurable via
 * Kconfig (CONFIG_PORTUNUS_DOOR_STRIKE_ACTIVE_LOW).
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
 * @brief Initialise the door strike GPIO.
 *
 * Configures the pin as a push-pull output and sets it to the
 * de-energized (locked) state.
 *
 * @return PORTUNUS_OK on success, or an error code if GPIO
 *         configuration fails.
 */
portunus_err_t door_strike_init(void);

/**
 * @brief Energize the door strike (unlock the door).
 *
 * Sets the GPIO to the active level.  Returns immediately — the
 * caller (FSM via IAccessPoint) is responsible for timing.
 *
 * @return PORTUNUS_OK on success.
 */
portunus_err_t door_strike_energize(void);

/**
 * @brief De-energize the door strike (lock the door).
 *
 * Sets the GPIO to the inactive level.  Returns immediately.
 *
 * @return PORTUNUS_OK on success.
 */
portunus_err_t door_strike_deenergize(void);

/**
 * @brief Read the current output state of the strike GPIO.
 *
 * @return true if the strike is currently energized (unlocked).
 */
bool door_strike_is_energized(void);

#ifdef __cplusplus
}
#endif
