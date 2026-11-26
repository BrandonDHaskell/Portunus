/* Timestamp-based debounce for the reed switch, extracted from
 * reed_switch.cpp so the unit tier can drive it with synthetic samples and
 * times. No ESP-IDF or FreeRTOS dependency. Private to access_point_gpio. */
#pragma once

#include <stdint.h>

/** NO/NC wiring with the internal pull-up: a normally-open switch closes
 *  (pin LOW) when the magnet is aligned, a normally-closed one opens (pin
 *  HIGH). Either way the result is true when the door is closed. */
constexpr bool reed_level_is_closed(int level, bool normally_closed)
{
    return normally_closed ? (level == 1) : (level == 0);
}

struct debouncer_t {
    bool    stable;     /**< Last accepted state. */
    bool    candidate;  /**< Most recent raw sample. */
    int64_t since_us;   /**< When the candidate first appeared. */
};

/** Start with sample accepted as stable, so there is no settling period at boot. */
void debouncer_init(debouncer_t &d, bool sample, int64_t now_us);

/** Feed one sample and return the debounced state. A new state is accepted
 *  once it has been sampled continuously for at least window_ms; any change
 *  in between restarts the window. Never blocks. */
bool debouncer_update(debouncer_t &d, bool sample, int64_t now_us, uint32_t window_ms);
