/* Unit tier: credential-reader health policy (degraded mode and recovery).
 * No ESP-IDF, no FreeRTOS, no sdkconfig. Bare host compiler. */
#include "unity.h"
#include "reader_health.hpp"
#include "error_codes.hpp"

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

/* RH-06: card errors (a held 10-byte card, two cards at once) never
 * degrade the reader, however long they last. */
void test_card_errors_never_degrade(void) {
    reader_health_t h;
    for (int i = 0; i < 1000; ++i) {
        const reader_step_t s = reader_health_on_read(h, reader_read_result_t::CARD_ERROR, CFG);
        TEST_ASSERT_EQUAL(reader_health_event_t::NONE, s.event);
        TEST_ASSERT_EQUAL_UINT32(CFG.poll_interval_ms, s.delay_ms);
        TEST_ASSERT_FALSE(s.publish_credential);
        TEST_ASSERT_FALSE(s.halt);
    }
    TEST_ASSERT_FALSE(h.degraded);
    TEST_ASSERT_EQUAL_UINT8(0, h.consecutive_errors);
}

/* RH-07: a card error does not reset the hardware count either, so card
 * replies garbled by a failing bus cannot hide a run of hardware errors. */
void test_card_error_does_not_reset_hw_count(void) {
    reader_health_t h;
    for (int i = 0; i < 4; ++i) fail(h);
    reader_health_on_read(h, reader_read_result_t::CARD_ERROR, CFG);
    TEST_ASSERT_EQUAL_UINT8(4, h.consecutive_errors);
    const reader_step_t s = fail(h);
    TEST_ASSERT_TRUE(h.degraded);
    TEST_ASSERT_EQUAL(reader_health_event_t::READ_ERROR, s.event);
}

/* RH-08: driver results map to health outcomes; anything unrecognised is a
 * hardware error so a new failure mode is reported, not ignored. */
void test_classify_reader_results(void) {
    TEST_ASSERT_EQUAL(reader_read_result_t::CREDENTIAL, reader_classify(PORTUNUS_OK));
    TEST_ASSERT_EQUAL(reader_read_result_t::NO_CREDENTIAL,
                      reader_classify(PORTUNUS_ERR_NO_CREDENTIAL));
    TEST_ASSERT_EQUAL(reader_read_result_t::CARD_ERROR,
                      reader_classify(PORTUNUS_ERR_CREDENTIAL_COLLISION));
    TEST_ASSERT_EQUAL(reader_read_result_t::CARD_ERROR,
                      reader_classify(PORTUNUS_ERR_CREDENTIAL_READ));
    const portunus_err_t hw[] = {
        PORTUNUS_ERR_SPI_TRANSFER, PORTUNUS_ERR_SPI_INIT, PORTUNUS_ERR_TIMEOUT,
        PORTUNUS_ERR_DEVICE_NOT_FOUND, PORTUNUS_ERR_INVALID_ARG,
        static_cast<portunus_err_t>(0x7FFF),
    };
    for (const portunus_err_t e : hw) {
        TEST_ASSERT_EQUAL(reader_read_result_t::HW_ERROR, reader_classify(e));
    }
}

int main(void) {
    UNITY_BEGIN();
    RUN_TEST(test_fifth_consecutive_error_enters_degraded_once);
    RUN_TEST(test_no_credential_resets_error_count);
    RUN_TEST(test_credential_resets_count_and_requests_halt);
    RUN_TEST(test_probe_cadence_and_recovery);
    RUN_TEST(test_second_fault_cycle_announces_again);
    RUN_TEST(test_card_errors_never_degrade);
    RUN_TEST(test_card_error_does_not_reset_hw_count);
    RUN_TEST(test_classify_reader_results);
    return UNITY_END();
}
