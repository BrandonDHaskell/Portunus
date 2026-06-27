/* Tier A host test: SystemFSM decision core.
 * No ESP-IDF, no FreeRTOS, no sdkconfig. Bare host compiler. */
#include "unity.h"
#include "system_fsm_decide.hpp"
#include <string.h>

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
}

void test_granted_without_access_point_does_not_unlock(void) {
    fsm_actions_t a = decide_system_event(
        caps(true, false, true, true), ev(EVENT_ACCESS_GRANTED));
    TEST_ASSERT_EQUAL(door_cmd_t::NONE, a.door);
    TEST_ASSERT_EQUAL_UINT8(1, a.feedback_count);
    TEST_ASSERT_EQUAL(feedback_type_t::ACCESS_GRANTED, a.feedback[0]);
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

int main(void) {
    UNITY_BEGIN();
    RUN_TEST(test_granted_with_access_point_unlocks_and_holds);
    RUN_TEST(test_granted_without_access_point_does_not_unlock);
    RUN_TEST(test_denied_never_touches_strike);
    RUN_TEST(test_credential_read_online_shows_card_read_only);
    RUN_TEST(test_credential_read_offline_appends_system_error);
    RUN_TEST(test_feedback_absent_emits_no_indications);
    RUN_TEST(test_unknown_event_is_inert);
    return UNITY_END();
}
