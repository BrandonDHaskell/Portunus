/* Tier B host tests — ESP-IDF linux target (POSIX FreeRTOS simulator).
 *
 * Build with fake bus (behavioral):  idf.py set-target linux build
 * Build with real bus (concurrency): idf.py -DPORTUNUS_TEST_REAL_BUS=ON ... build
 *
 * The FSM is driven via SystemFSMTestFixture (friend class declared in
 * system_fsm.hpp) without starting FreeRTOS tasks. init() is called to
 * set capability flags and create the internal queue; start() is not called.
 */

#include "unity.h"
#include "system_fsm.hpp"
#include "system_fsm_decide.hpp"
#include "event_bus.hpp"
#include "timing_config.hpp"
#include "error_codes.hpp"
#include "event_types.hpp"

#include "../support/fake_clock.hpp"
#include "../support/fake_access_point.hpp"
#include "../support/fake_credential_reader.hpp"
#include "../support/fake_feedback.hpp"
#include "../support/system_fsm_test_fixture.hpp"

#ifndef PORTUNUS_TEST_REAL_BUS
#include "../components/event_bus/include/event_bus_fake.hpp"
#endif

#include <string.h>

void setUp(void) {
#ifndef PORTUNUS_TEST_REAL_BUS
    event_bus_fake_reset();
#endif
    event_bus_init();
}

void tearDown(void) {}

/* ── Helpers ──────────────────────────────────────────────────────────────── */

static portunus_event_t make_grant(void) {
    portunus_event_t e;
    memset(&e, 0, sizeof(e));
    e.id = EVENT_ACCESS_GRANTED;
    return e;
}

static portunus_event_t make_deny(void) {
    portunus_event_t e;
    memset(&e, 0, sizeof(e));
    e.id = EVENT_ACCESS_DENIED;
    return e;
}

static portunus_event_t make_credential_read(bool online) {
    /* Build an FSM with online/offline capability set manually via init. */
    (void)online;  /* caps are set by init(); caller controls via has_network */
    portunus_event_t e;
    memset(&e, 0, sizeof(e));
    e.id = EVENT_CREDENTIAL_READ;
    return e;
}

/* ── Behavioral tests (fake bus) ──────────────────────────────────────────── */

/* Grant with access point: unlock once, hold timer started, ACCESS_GRANTED feedback. */
void test_grant_unlocks_and_starts_hold_timer(void) {
    FakeAccessPoint access;
    FakeFeedback    fb;
    FakeClock       clk;

    SystemFSM fsm(nullptr, &access, &fb, &clk);
    TEST_ASSERT_EQUAL(PORTUNUS_OK, fsm.init());
    SystemFSMTestFixture fix(fsm);

    fix.inject(make_grant());

    TEST_ASSERT_EQUAL(1, access.unlocks);
    TEST_ASSERT_FALSE(access.locked);
    TEST_ASSERT_TRUE(fix.strike_energized());
    TEST_ASSERT_EQUAL(1, fb.count_of(feedback_type_t::ACCESS_GRANTED));
}

/* Grant with unlock failure: hold timer NOT started (success-gate). */
void test_grant_unlock_failure_does_not_start_timer(void) {
    FakeAccessPoint access;
    FakeFeedback    fb;
    FakeClock       clk;

    access.unlock_result = PORTUNUS_FAIL;

    SystemFSM fsm(nullptr, &access, &fb, &clk);
    TEST_ASSERT_EQUAL(PORTUNUS_OK, fsm.init());
    SystemFSMTestFixture fix(fsm);

    fix.inject(make_grant());

    TEST_ASSERT_EQUAL(0, access.unlocks);  /* unlock attempted but failed */
    TEST_ASSERT_FALSE(fix.strike_energized());
}

/* Grant without access point: no unlock, ACCESS_GRANTED feedback still shown. */
void test_grant_without_access_point_shows_feedback(void) {
    FakeFeedback fb;
    FakeClock    clk;

    SystemFSM fsm(nullptr, nullptr, &fb, &clk);
    TEST_ASSERT_EQUAL(PORTUNUS_OK, fsm.init());
    SystemFSMTestFixture fix(fsm);

    fix.inject(make_grant());

    TEST_ASSERT_EQUAL(1, fb.count_of(feedback_type_t::ACCESS_GRANTED));
    TEST_ASSERT_FALSE(fix.strike_energized());
}

/* Deny: no strike interaction, ACCESS_DENIED feedback. */
void test_deny_shows_feedback_only(void) {
    FakeAccessPoint access;
    FakeFeedback    fb;
    FakeClock       clk;

    SystemFSM fsm(nullptr, &access, &fb, &clk);
    TEST_ASSERT_EQUAL(PORTUNUS_OK, fsm.init());
    SystemFSMTestFixture fix(fsm);

    fix.inject(make_deny());

    TEST_ASSERT_EQUAL(0, access.unlocks);
    TEST_ASSERT_EQUAL(1, fb.count_of(feedback_type_t::ACCESS_DENIED));
}

/* Credential read while online: CARD_READ only. */
void test_credential_read_online_shows_card_read(void) {
    FakeFeedback fb;
    FakeClock    clk;

    SystemFSM fsm(nullptr, nullptr, &fb, &clk);
    TEST_ASSERT_EQUAL(PORTUNUS_OK, fsm.init());
    /* Manually set has_network = true after init (init sets false, WiFi disabled). */
    /* We control this via the caps struct — not exposed, so we test online = false path.
     * WiFi is disabled in sdkconfig.defaults, so has_network is always false here.
     * Online test is covered by the Tier A decide() table which is authoritative. */
    (void)0;
}

/* Credential read while offline: CARD_READ then SYSTEM_ERROR. */
void test_credential_read_offline_shows_card_read_and_error(void) {
    FakeFeedback fb;
    FakeClock    clk;

    SystemFSM fsm(nullptr, nullptr, &fb, &clk);
    TEST_ASSERT_EQUAL(PORTUNUS_OK, fsm.init());
    /* WiFi disabled in sdkconfig → has_network=false → offline path */
    SystemFSMTestFixture fix(fsm);
    fix.inject(make_credential_read(false));

    TEST_ASSERT_EQUAL(1, fb.count_of(feedback_type_t::CARD_READ));
    TEST_ASSERT_EQUAL(1, fb.count_of(feedback_type_t::SYSTEM_ERROR));
}

/* Feedback absent: no indications on any event. */
void test_no_feedback_hardware_emits_nothing(void) {
    FakeAccessPoint access;
    FakeClock       clk;

    SystemFSM fsm(nullptr, &access, nullptr, &clk);
    TEST_ASSERT_EQUAL(PORTUNUS_OK, fsm.init());
    SystemFSMTestFixture fix(fsm);

    fix.inject(make_grant());
    fix.inject(make_deny());
    /* No crash, no feedback object touched. */
    TEST_ASSERT_EQUAL(1, access.unlocks);
}

/* Re-lock after hold timer expires. */
void test_strike_relocks_after_hold_expires(void) {
    FakeAccessPoint access;
    FakeFeedback    fb;
    FakeClock       clk;

    SystemFSM fsm(nullptr, &access, &fb, &clk);
    TEST_ASSERT_EQUAL(PORTUNUS_OK, fsm.init());
    SystemFSMTestFixture fix(fsm);

    fix.inject(make_grant());
    TEST_ASSERT_EQUAL(1, access.unlocks);
    TEST_ASSERT_FALSE(access.locked);

    clk.advance(UNLOCK_HOLD_MS - 1);
    fix.tick_unlock_timer();
    TEST_ASSERT_FALSE(access.locked);  /* not yet */

    clk.advance(1);
    fix.tick_unlock_timer();           /* deadline reached */
    TEST_ASSERT_TRUE(access.locked);
    TEST_ASSERT_FALSE(fix.strike_energized());

#ifndef PORTUNUS_TEST_REAL_BUS
    TEST_ASSERT_EQUAL(1,
        (int)event_bus_fake_count_of(EVENT_FSM_UNLOCK_TIMEOUT));
#endif
}

/* Timer retry: if re-lock fails, timer remains energized for retry. */
void test_relock_failure_retries_on_next_tick(void) {
    FakeAccessPoint access;
    FakeClock       clk;

    SystemFSM fsm(nullptr, &access, nullptr, &clk);
    TEST_ASSERT_EQUAL(PORTUNUS_OK, fsm.init());
    SystemFSMTestFixture fix(fsm);

    fix.inject(make_grant());
    access.lock_result = PORTUNUS_FAIL;  /* set AFTER unlock so grant succeeds */

    clk.advance(UNLOCK_HOLD_MS);
    fix.tick_unlock_timer();
    TEST_ASSERT_TRUE(fix.strike_energized());  /* still armed — retry next tick */

    access.lock_result = PORTUNUS_OK;
    fix.tick_unlock_timer();
    TEST_ASSERT_FALSE(fix.strike_energized());
    TEST_ASSERT_TRUE(access.locked);
}

/* ── Concurrency tests (real bus, built with PORTUNUS_TEST_REAL_BUS=ON) ──── */

#ifdef PORTUNUS_TEST_REAL_BUS

void test_real_bus_publish_reaches_subscriber(void) {
    /* Real bus: publish from this task, subscriber runs on dispatcher task. */
    volatile int received = 0;
    event_bus_subscribe(EVENT_ACCESS_GRANTED,
        [](const portunus_event_t *, void *ctx) {
            (*static_cast<volatile int *>(ctx))++;
        }, (void *)&received);

    portunus_event_t e = make_grant();
    event_bus_publish(&e);

    /* Give dispatcher task time to run. */
    vTaskDelay(pdMS_TO_TICKS(50));
    TEST_ASSERT_EQUAL(1, received);
}

#endif /* PORTUNUS_TEST_REAL_BUS */

/* ── Entry point ──────────────────────────────────────────────────────────── */

extern "C" void app_main(void) {
    UNITY_BEGIN();

    /* Behavioral suite */
    RUN_TEST(test_grant_unlocks_and_starts_hold_timer);
    RUN_TEST(test_grant_unlock_failure_does_not_start_timer);
    RUN_TEST(test_grant_without_access_point_shows_feedback);
    RUN_TEST(test_deny_shows_feedback_only);
    RUN_TEST(test_credential_read_offline_shows_card_read_and_error);
    RUN_TEST(test_no_feedback_hardware_emits_nothing);
    RUN_TEST(test_strike_relocks_after_hold_expires);
    RUN_TEST(test_relock_failure_retries_on_next_tick);

#ifdef PORTUNUS_TEST_REAL_BUS
    /* Concurrency suite — only meaningful with real async bus */
    RUN_TEST(test_real_bus_publish_reaches_subscriber);
#endif

    int failures = UNITY_END();
    exit(failures);
}
