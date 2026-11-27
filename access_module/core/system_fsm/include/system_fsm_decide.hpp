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
 *  the wrapper enforces the success-gate exactly as the original did.
 *
 *  LOCK = lock now. Nothing produces it today: re-locking after a grant is
 *  the FSM's own hold timer and early re-lock, not a command. It is kept
 *  for explicit or scheduled lock commands (a door held unlocked during set
 *  hours and locked outside them), decision D-T10. */
enum class door_cmd_t : uint8_t { NONE, UNLOCK_AND_HOLD, LOCK };

/** Ordered feedback list mirrors the real preemptive indicate() sequence. */
struct fsm_actions_t {
    door_cmd_t      door = door_cmd_t::NONE;
    uint8_t         feedback_count = 0;
    feedback_type_t feedback[FSM_MAX_FEEDBACK] =
        { feedback_type_t::NONE, feedback_type_t::NONE };
    /** Shown instead of `feedback` when door is UNLOCK_AND_HOLD and unlock()
     *  fails: the module knows the unlock signal was not sent, so it must not
     *  show a grant (decision D-T4). Whether the strike physically moved is
     *  not observable (no strike feedback), so a sent signal shows the grant. */
    feedback_type_t feedback_if_unlock_fails = feedback_type_t::NONE;
};

/** Pure decision: (capabilities, event) -> intended effects. Mirrors
 *  process_event. `state` is intentionally not an input: the MVP process_event
 *  does not branch on system state. */
fsm_actions_t decide_system_event(const system_capabilities_t &caps,
                                  const portunus_event_t       &event);
