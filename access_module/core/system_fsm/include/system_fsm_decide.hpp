/* Pure decision core extracted from SystemFSM::process_event. Returns intended
 * effects as data and performs nothing; the FSM wrapper executes them against the
 * real interfaces and clock. Include graph is sdkconfig-free and FreeRTOS-free,
 * so this builds with a bare host compiler. This is the SAME function the FSM
 * calls in production — no test-only reimplementation. */
#pragma once

#include "system_states.hpp"   /* system_capabilities_t */
#include "event_types.hpp"     /* portunus_event_t, event ids */
#include "i_feedback.hpp"      /* feedback_type_t */

#include <stdint.h>

static constexpr uint8_t FSM_MAX_FEEDBACK = 2;

/** UNLOCK_AND_HOLD = attempt unlock, and only on success start the hold timer;
 *  the wrapper enforces the success-gate exactly as the original did. */
enum class door_cmd_t : uint8_t { NONE, UNLOCK_AND_HOLD, LOCK };

/** Ordered feedback list mirrors the real preemptive indicate() sequence. */
struct fsm_actions_t {
    door_cmd_t      door = door_cmd_t::NONE;
    uint8_t         feedback_count = 0;
    feedback_type_t feedback[FSM_MAX_FEEDBACK] =
        { feedback_type_t::NONE, feedback_type_t::NONE };
};

/** Pure decision: (capabilities, event) -> intended effects. Mirrors
 *  process_event. `state` is intentionally not an input: the MVP process_event
 *  does not branch on system state. */
fsm_actions_t decide_system_event(const system_capabilities_t &caps,
                                  const portunus_event_t       &event);
