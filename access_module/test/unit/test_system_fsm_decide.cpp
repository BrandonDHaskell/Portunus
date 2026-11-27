/* Unit tier: SystemFSM decision core.
 * No ESP-IDF, no FreeRTOS, no sdkconfig. Bare host compiler. */
#include "unity.h"
#include "system_fsm_decide.hpp"
#include <string.h>
#include <stdio.h>

void setUp(void) {}
void tearDown(void) {}

static system_capabilities_t caps(bool reader, bool ap, bool fb, bool net) {
    system_capabilities_t c;
    c.has_reader       = reader;
    c.has_access_point = ap;
    c.has_feedback     = fb;
    c.has_network      = net;
    return c;
}

static portunus_event_t ev(portunus_event_id_t id) {
    portunus_event_t e;
    memset(&e, 0, sizeof(e));
    e.id = id;
    return e;
}

void test_granted_with_access_point_unlocks_and_holds(void) {
    fsm_actions_t a = decide_system_event(
        caps(true, true, true, true), ev(EVENT_ACCESS_GRANTED));
    TEST_ASSERT_EQUAL(door_cmd_t::UNLOCK_AND_HOLD, a.door);
    TEST_ASSERT_EQUAL_UINT8(1, a.feedback_count);
    TEST_ASSERT_EQUAL(feedback_type_t::ACCESS_GRANTED, a.feedback[0]);
    /* DEC-10 (D-T4): if the unlock signal cannot be sent, show an error. */
    TEST_ASSERT_EQUAL(feedback_type_t::SYSTEM_ERROR, a.feedback_if_unlock_fails);
}

/* DEC-10 (D-T4): with no access point there is no unlock signal to send, so
 * a grant shows an error, never green over a door that cannot open. */
void test_granted_without_access_point_shows_error(void) {
    fsm_actions_t a = decide_system_event(
        caps(true, false, true, true), ev(EVENT_ACCESS_GRANTED));
    TEST_ASSERT_EQUAL(door_cmd_t::NONE, a.door);
    TEST_ASSERT_EQUAL_UINT8(1, a.feedback_count);
    TEST_ASSERT_EQUAL(feedback_type_t::SYSTEM_ERROR, a.feedback[0]);
    TEST_ASSERT_EQUAL(feedback_type_t::NONE, a.feedback_if_unlock_fails);
}

void test_denied_never_touches_strike(void) {
    fsm_actions_t a = decide_system_event(
        caps(true, true, true, true), ev(EVENT_ACCESS_DENIED));
    TEST_ASSERT_EQUAL(door_cmd_t::NONE, a.door);
    TEST_ASSERT_EQUAL_UINT8(1, a.feedback_count);
    TEST_ASSERT_EQUAL(feedback_type_t::ACCESS_DENIED, a.feedback[0]);
}

void test_credential_read_online_shows_card_read_only(void) {
    fsm_actions_t a = decide_system_event(
        caps(true, true, true, true), ev(EVENT_CREDENTIAL_READ));
    TEST_ASSERT_EQUAL(door_cmd_t::NONE, a.door);
    TEST_ASSERT_EQUAL_UINT8(1, a.feedback_count);
    TEST_ASSERT_EQUAL(feedback_type_t::CARD_READ, a.feedback[0]);
}

void test_credential_read_offline_appends_system_error(void) {
    fsm_actions_t a = decide_system_event(
        caps(true, true, true, false), ev(EVENT_CREDENTIAL_READ));
    TEST_ASSERT_EQUAL(door_cmd_t::NONE, a.door);
    TEST_ASSERT_EQUAL_UINT8(2, a.feedback_count);
    TEST_ASSERT_EQUAL(feedback_type_t::CARD_READ,    a.feedback[0]);
    TEST_ASSERT_EQUAL(feedback_type_t::SYSTEM_ERROR, a.feedback[1]);
}

void test_feedback_absent_emits_no_indications(void) {
    fsm_actions_t a = decide_system_event(
        caps(true, true, false, false), ev(EVENT_CREDENTIAL_READ));
    TEST_ASSERT_EQUAL(door_cmd_t::NONE, a.door);
    TEST_ASSERT_EQUAL_UINT8(0, a.feedback_count);
}

void test_unknown_event_is_inert(void) {
    fsm_actions_t a = decide_system_event(
        caps(true, true, true, true), ev(EVENT_HEARTBEAT));
    TEST_ASSERT_EQUAL(door_cmd_t::NONE, a.door);
    TEST_ASSERT_EQUAL_UINT8(0, a.feedback_count);
}

/* DEC-08: exhaustive matrix, every capability combination x every event id.
 * Invariants that must hold everywhere, not just in the hand-picked cases:
 *   - the door is commanded only for GRANTED with an access point, and only
 *     ever to UNLOCK_AND_HOLD (DEC-09: LOCK is never produced, see D-T10);
 *   - no feedback without feedback hardware;
 *   - feedback_count never exceeds FSM_MAX_FEEDBACK and unused slots stay NONE;
 *   - events the FSM does not consume produce no effects at all. */
static const portunus_event_id_t ALL_EVENTS[] = {
    EVENT_NONE, EVENT_SYSTEM_BOOT_COMPLETE,
    EVENT_CREDENTIAL_READ, EVENT_CREDENTIAL_READ_ERROR, EVENT_CREDENTIAL_READER_RECOVERED,
    EVENT_HEARTBEAT,
    EVENT_ACCESS_GRANTED, EVENT_ACCESS_DENIED,
    EVENT_DOOR_OPENED, EVENT_DOOR_CLOSED,
    EVENT_FSM_UNLOCK_TIMEOUT,
    EVENT_PROVISION_REQUEST, EVENT_PROVISION_SUCCESS, EVENT_PROVISION_FAILED,
    EVENT_ARM_REQUESTED,
};

void test_exhaustive_capability_event_matrix(void) {
    char msg[96];
    for (unsigned mask = 0; mask < 16; ++mask) {
        const system_capabilities_t c =
            caps(mask & 1, (mask >> 1) & 1, (mask >> 2) & 1, (mask >> 3) & 1);
        for (const portunus_event_id_t id : ALL_EVENTS) {
            snprintf(msg, sizeof msg, "caps=0x%x event=0x%04x", mask, (unsigned)id);
            const fsm_actions_t a = decide_system_event(c, ev(id));

            const bool unlock = (id == EVENT_ACCESS_GRANTED) && c.has_access_point;
            TEST_ASSERT_EQUAL_MESSAGE(unlock ? door_cmd_t::UNLOCK_AND_HOLD : door_cmd_t::NONE,
                                      a.door, msg);
            /* A failure indication exists exactly when an unlock is attempted
             * and there is feedback hardware to show it on. */
            TEST_ASSERT_EQUAL_MESSAGE(unlock && c.has_feedback ? feedback_type_t::SYSTEM_ERROR
                                                               : feedback_type_t::NONE,
                                      a.feedback_if_unlock_fails, msg);

            TEST_ASSERT_LESS_OR_EQUAL_UINT8_MESSAGE(FSM_MAX_FEEDBACK, a.feedback_count, msg);
            for (uint8_t i = a.feedback_count; i < FSM_MAX_FEEDBACK; ++i) {
                TEST_ASSERT_EQUAL_MESSAGE(feedback_type_t::NONE, a.feedback[i], msg);
            }
            if (!c.has_feedback) {
                TEST_ASSERT_EQUAL_UINT8_MESSAGE(0, a.feedback_count, msg);
                continue;
            }

            switch (id) {
            case EVENT_CREDENTIAL_READ:
                TEST_ASSERT_EQUAL_UINT8_MESSAGE(c.has_network ? 1 : 2, a.feedback_count, msg);
                TEST_ASSERT_EQUAL_MESSAGE(feedback_type_t::CARD_READ, a.feedback[0], msg);
                if (!c.has_network) {
                    TEST_ASSERT_EQUAL_MESSAGE(feedback_type_t::SYSTEM_ERROR, a.feedback[1], msg);
                }
                break;
            case EVENT_ACCESS_GRANTED:
                /* DEC-10 (D-T4): green only when there is a strike to signal. */
                TEST_ASSERT_EQUAL_UINT8_MESSAGE(1, a.feedback_count, msg);
                TEST_ASSERT_EQUAL_MESSAGE(c.has_access_point ? feedback_type_t::ACCESS_GRANTED
                                                             : feedback_type_t::SYSTEM_ERROR,
                                          a.feedback[0], msg);
                break;
            case EVENT_ACCESS_DENIED:
                TEST_ASSERT_EQUAL_UINT8_MESSAGE(1, a.feedback_count, msg);
                TEST_ASSERT_EQUAL_MESSAGE(feedback_type_t::ACCESS_DENIED, a.feedback[0], msg);
                break;
            default:
                TEST_ASSERT_EQUAL_UINT8_MESSAGE(0, a.feedback_count, msg);
                break;
            }
        }
    }
}

int main(void) {
    UNITY_BEGIN();
    RUN_TEST(test_granted_with_access_point_unlocks_and_holds);
    RUN_TEST(test_granted_without_access_point_shows_error);
    RUN_TEST(test_denied_never_touches_strike);
    RUN_TEST(test_credential_read_online_shows_card_read_only);
    RUN_TEST(test_credential_read_offline_appends_system_error);
    RUN_TEST(test_feedback_absent_emits_no_indications);
    RUN_TEST(test_unknown_event_is_inert);
    RUN_TEST(test_exhaustive_capability_event_matrix);
    return UNITY_END();
}
