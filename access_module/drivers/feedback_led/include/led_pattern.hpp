/* Pure LED pattern engine extracted from FeedbackLed::run_patterns.
 * Every pattern is a function of (type, tick): the task only feeds it
 * indications and ticks and copies the returned level to the LED. No ESP-IDF
 * or FreeRTOS dependency, so the unit tier links led_pattern.cpp directly. */
#pragma once

#include "i_feedback.hpp"   /* feedback_type_t */

#include <stdint.h>

/** Tick period of the pattern task. */
static constexpr uint32_t LED_TICK_MS = 50;

enum class led_pattern_kind_t : uint8_t {
    OFF,         /**< NONE or an unknown value: LED off. */
    ONE_SHOT,    /**< Runs `length` ticks, then returns to the baseline. */
    CONTINUOUS,  /**< Repeats every `length` ticks until replaced. */
    HELD,        /**< Solid on until replaced (CARD_READ). */
};

led_pattern_kind_t led_pattern_kind(feedback_type_t type);

/** One-shot duration or continuous period in ticks; 0 for OFF and HELD. */
uint32_t led_pattern_length(feedback_type_t type);

/** LED level for `type` at `tick` (tick < length for one-shots). */
bool led_pattern_level(feedback_type_t type, uint32_t tick);

/** Patterns a one-shot returns to when it ends. Only the access-point idle
 *  blink: SYSTEM_ERROR is excluded so one offline card read cannot leave the
 *  error blink on for good, and the PEU FSM re-indicates its own states. */
bool led_pattern_is_baseline(feedback_type_t type);

struct led_engine_t {
    feedback_type_t current  = feedback_type_t::NONE;
    feedback_type_t baseline = feedback_type_t::NONE;
    uint32_t        tick     = 0;
};

/** A new indication preempts the current pattern and restarts at tick 0.
 *  NONE also clears the baseline. */
void led_engine_indicate(led_engine_t &e, feedback_type_t type);

/** Level for this tick, then advance. Continuous patterns keep tick within
 *  their period, so it never grows without bound. */
bool led_engine_step(led_engine_t &e);
