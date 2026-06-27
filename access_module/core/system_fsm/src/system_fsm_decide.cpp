#include "system_fsm_decide.hpp"

static inline void push_feedback(fsm_actions_t &a, feedback_type_t f) {
    if (a.feedback_count < FSM_MAX_FEEDBACK) a.feedback[a.feedback_count++] = f;
}

fsm_actions_t decide_system_event(const system_capabilities_t &caps,
                                  const portunus_event_t       &event)
{
    fsm_actions_t a;
    switch (event.id) {
    case EVENT_CREDENTIAL_READ:
        if (caps.has_feedback) {
            push_feedback(a, feedback_type_t::CARD_READ);
            if (!caps.has_network) push_feedback(a, feedback_type_t::SYSTEM_ERROR);
        }
        break;
    case EVENT_ACCESS_GRANTED:
        if (caps.has_access_point) a.door = door_cmd_t::UNLOCK_AND_HOLD;
        if (caps.has_feedback)     push_feedback(a, feedback_type_t::ACCESS_GRANTED);
        break;
    case EVENT_ACCESS_DENIED:
        if (caps.has_feedback)     push_feedback(a, feedback_type_t::ACCESS_DENIED);
        break;
    default:
        break;
    }
    return a;
}
