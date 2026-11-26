/* Unit tier: credential-reader health policy (degraded mode and recovery).
 * No ESP-IDF, no FreeRTOS, no sdkconfig. Bare host compiler. */
#include "unity.h"
#include "reader_health.hpp"

void setUp(void) {}
void tearDown(void) {}

/* Production values from system_fsm.cpp and the default Kconfig timings. */
static const reader_health_cfg_t CFG = {
    .error_threshold      = 5,
    .poll_interval_ms     = 100,
    .reread_delay_ms      = 2000,
    .recovery_interval_ms = 5000,
};

static reader_step_t fail(reader_health_t &h) {
    return reader_health_on_read(h, reader_read_result_t::HW_ERROR, CFG);
}

/* RH-01: four consecutive hardware errors stay healthy; the fifth enters
 * degraded mode and announces READ_ERROR exactly once. */
void test_fifth_consecutive_error_enters_degraded_once(void) {
    reader_health_t h;
    for (int i = 0; i < 4; ++i) {
        const reader_step_t s = fail(h);
        TEST_ASSERT_FALSE(h.degraded);
        TEST_ASSERT_EQUAL(reader_health_event_t::NONE, s.event);
        TEST_ASSERT_EQUAL_UINT32(CFG.poll_interval_ms, s.delay_ms);
        TEST_ASSERT_FALSE(s.publish_credential);
    }
    const reader_step_t s = fail(h);
    TEST_ASSERT_TRUE(h.degraded);
    TEST_ASSERT_EQUAL(reader_health_event_t::READ_ERROR, s.event);
    TEST_ASSERT_EQUAL_UINT32(CFG.recovery_interval_ms, s.delay_ms);

    /* While degraded the loop only probes; a failed probe announces nothing. */
    const reader_step_t p = reader_health_on_probe(h, false, CFG);
    TEST_ASSERT_TRUE(h.degraded);
    TEST_ASSERT_EQUAL(reader_health_event_t::NONE, p.event);
}

/* RH-02: an idle read (no card) proves the chip is alive and resets the count. */
void test_no_credential_resets_error_count(void) {
    reader_health_t h;
    for (int i = 0; i < 4; ++i) fail(h);
    const reader_step_t s = reader_health_on_read(h, reader_read_result_t::NO_CREDENTIAL, CFG);
    TEST_ASSERT_EQUAL_UINT8(0, h.consecutive_errors);
    TEST_ASSERT_EQUAL_UINT32(CFG.poll_interval_ms, s.delay_ms);
    TEST_ASSERT_FALSE(s.publish_credential);
    TEST_ASSERT_FALSE(s.halt);
    for (int i = 0; i < 4; ++i) fail(h);
    TEST_ASSERT_FALSE(h.degraded);
}

/* RH-03: a successful read resets the count, publishes, halts the card and
 * waits the re-read delay. */
void test_credential_resets_count_and_requests_halt(void) {
    reader_health_t h;
    for (int i = 0; i < 4; ++i) fail(h);
    const reader_step_t s = reader_health_on_read(h, reader_read_result_t::CREDENTIAL, CFG);
    TEST_ASSERT_EQUAL_UINT8(0, h.consecutive_errors);
    TEST_ASSERT_TRUE(s.publish_credential);
    TEST_ASSERT_TRUE(s.halt);
    TEST_ASSERT_EQUAL(reader_health_event_t::NONE, s.event);
    TEST_ASSERT_EQUAL_UINT32(CFG.reread_delay_ms, s.delay_ms);
}

/* RH-04: degraded mode probes every recovery interval; a successful probe
 * announces RECOVERED once, resets, and resumes reading with no delay. */
void test_probe_cadence_and_recovery(void) {
    reader_health_t h;
    for (int i = 0; i < 5; ++i) fail(h);
    TEST_ASSERT_TRUE(h.degraded);

    for (int i = 0; i < 3; ++i) {
        const reader_step_t p = reader_health_on_probe(h, false, CFG);
        TEST_ASSERT_EQUAL_UINT32(CFG.recovery_interval_ms, p.delay_ms);
        TEST_ASSERT_FALSE(p.publish_credential);
    }
    const reader_step_t ok = reader_health_on_probe(h, true, CFG);
    TEST_ASSERT_FALSE(h.degraded);
    TEST_ASSERT_EQUAL_UINT8(0, h.consecutive_errors);
    TEST_ASSERT_EQUAL(reader_health_event_t::RECOVERED, ok.event);
    TEST_ASSERT_EQUAL_UINT32(0, ok.delay_ms);
}

/* RH-05: after recovery, five more faults announce a second READ_ERROR. */
void test_second_fault_cycle_announces_again(void) {
    reader_health_t h;
    int read_errors = 0;
    for (int cycle = 0; cycle < 2; ++cycle) {
        for (int i = 0; i < 5; ++i) {
            if (fail(h).event == reader_health_event_t::READ_ERROR) ++read_errors;
        }
        TEST_ASSERT_TRUE(h.degraded);
        reader_health_on_probe(h, true, CFG);
    }
    TEST_ASSERT_EQUAL_INT(2, read_errors);
}

int main(void) {
    UNITY_BEGIN();
    RUN_TEST(test_fifth_consecutive_error_enters_degraded_once);
    RUN_TEST(test_no_credential_resets_error_count);
    RUN_TEST(test_credential_resets_count_and_requests_halt);
    RUN_TEST(test_probe_cadence_and_recovery);
    RUN_TEST(test_second_fault_cycle_announces_again);
    return UNITY_END();
}
