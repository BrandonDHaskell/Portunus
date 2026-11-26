/* Component tier: ESP-IDF linux target (POSIX FreeRTOS simulator).
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
#include "freertos/semphr.h"

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

static portunus_event_t make_credential_read(void) {
    portunus_event_t e;
    memset(&e, 0, sizeof(e));
    e.id = EVENT_CREDENTIAL_READ;
    return e;
}

/* The FSM only acts on a decision that answers a pending card read, so the
 * decision tests first inject the read that the server is answering. */
static void inject_decision(SystemFSMTestFixture &fix, const portunus_event_t &decision) {
    fix.inject(make_credential_read());
    fix.inject(decision);
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

    inject_decision(fix, make_grant());

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

    inject_decision(fix, make_grant());

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

    inject_decision(fix, make_grant());

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

    inject_decision(fix, make_deny());

    TEST_ASSERT_EQUAL(0, access.unlocks);
    TEST_ASSERT_EQUAL(1, fb.count_of(feedback_type_t::ACCESS_DENIED));
}

/* Credential read while online: CARD_READ only. */
void test_credential_read_online_shows_card_read(void) {
    FakeFeedback fb;
    FakeClock    clk;

    SystemFSM fsm(nullptr, nullptr, &fb, &clk);
    TEST_ASSERT_EQUAL(PORTUNUS_OK, fsm.init());
    SystemFSMTestFixture fix(fsm);
    fix.set_network(true);
    fix.inject(make_credential_read());

    TEST_ASSERT_EQUAL(1, fb.count_of(feedback_type_t::CARD_READ));
    TEST_ASSERT_EQUAL(0, fb.count_of(feedback_type_t::SYSTEM_ERROR));
}

/* Credential read while offline: CARD_READ then SYSTEM_ERROR. */
void test_credential_read_offline_shows_card_read_and_error(void) {
    FakeFeedback fb;
    FakeClock    clk;

    SystemFSM fsm(nullptr, nullptr, &fb, &clk);
    TEST_ASSERT_EQUAL(PORTUNUS_OK, fsm.init());
    SystemFSMTestFixture fix(fsm);
    fix.set_network(false);
    fix.inject(make_credential_read());

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

    inject_decision(fix, make_grant());
    inject_decision(fix, make_deny());
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

    inject_decision(fix, make_grant());
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

    inject_decision(fix, make_grant());
    access.lock_result = PORTUNUS_FAIL;  /* set AFTER unlock so grant succeeds */

    clk.advance(UNLOCK_HOLD_MS);
    fix.tick_unlock_timer();
    TEST_ASSERT_TRUE(fix.strike_energized());  /* still armed — retry next tick */

    access.lock_result = PORTUNUS_OK;
    fix.tick_unlock_timer();
    TEST_ASSERT_FALSE(fix.strike_energized());
    TEST_ASSERT_TRUE(access.locked);
}

/* A grant with no pending card read (late or unsolicited) never unlocks. */
void test_unsolicited_grant_is_ignored(void) {
    FakeAccessPoint access;
    FakeFeedback    fb;
    FakeClock       clk;

    SystemFSM fsm(nullptr, &access, &fb, &clk);
    TEST_ASSERT_EQUAL(PORTUNUS_OK, fsm.init());
    SystemFSMTestFixture fix(fsm);

    fix.inject(make_grant());

    TEST_ASSERT_EQUAL(0, access.unlocks);
    TEST_ASSERT_FALSE(fix.strike_energized());
    TEST_ASSERT_EQUAL(0, fb.count_of(feedback_type_t::ACCESS_GRANTED));
}

/* No decision within the timeout: deny feedback, nothing left pending. */
void test_decision_timeout_shows_deny(void) {
    FakeAccessPoint access;
    FakeFeedback    fb;
    FakeClock       clk;

    SystemFSM fsm(nullptr, &access, &fb, &clk);
    TEST_ASSERT_EQUAL(PORTUNUS_OK, fsm.init());
    SystemFSMTestFixture fix(fsm);

    fix.inject(make_credential_read());
    TEST_ASSERT_EQUAL(1, fix.pending_decisions());

    clk.advance(ACCESS_DECISION_TIMEOUT_MS - 1);
    fix.tick_decision_timeout();
    TEST_ASSERT_EQUAL(0, fb.count_of(feedback_type_t::ACCESS_DENIED));

    clk.advance(1);
    fix.tick_decision_timeout();
    TEST_ASSERT_EQUAL(1, fb.count_of(feedback_type_t::ACCESS_DENIED));
    TEST_ASSERT_EQUAL(0, fix.pending_decisions());
}

/* A grant arriving after the timeout showed a deny must not unlock. */
void test_late_grant_after_timeout_is_ignored(void) {
    FakeAccessPoint access;
    FakeFeedback    fb;
    FakeClock       clk;

    SystemFSM fsm(nullptr, &access, &fb, &clk);
    TEST_ASSERT_EQUAL(PORTUNUS_OK, fsm.init());
    SystemFSMTestFixture fix(fsm);

    fix.inject(make_credential_read());
    clk.advance(ACCESS_DECISION_TIMEOUT_MS);
    fix.tick_decision_timeout();

    fix.inject(make_grant());
    TEST_ASSERT_EQUAL(0, access.unlocks);
    TEST_ASSERT_EQUAL(0, fb.count_of(feedback_type_t::ACCESS_GRANTED));
}

/* Two reads in flight (card held across a re-read): both decisions count. */
void test_each_pending_read_accepts_one_decision(void) {
    FakeAccessPoint access;
    FakeFeedback    fb;
    FakeClock       clk;

    SystemFSM fsm(nullptr, &access, &fb, &clk);
    TEST_ASSERT_EQUAL(PORTUNUS_OK, fsm.init());
    SystemFSMTestFixture fix(fsm);

    fix.inject(make_credential_read());
    fix.inject(make_credential_read());
    TEST_ASSERT_EQUAL(2, fix.pending_decisions());

    fix.inject(make_deny());
    fix.inject(make_grant());
    TEST_ASSERT_EQUAL(0, fix.pending_decisions());
    TEST_ASSERT_EQUAL(1, access.unlocks);

    fix.inject(make_grant());          /* third decision: nothing pending */
    TEST_ASSERT_EQUAL(1, access.unlocks);
}

/* ── Init, door and hold-timer tests (FSM-10..21) ────────────────────────── */
/* They inspect published events through the fake bus, so the real-bus
 * integration build skips them. */

#ifndef PORTUNUS_TEST_REAL_BUS

namespace {
enum class Mod { ABSENT, FAILING, OK };
}  // namespace

/* FSM-10: each module that is missing or fails init leaves exactly its own
 * capability flag false; the others are unaffected. All 27 combinations. */
void test_init_capability_matrix(void) {
    const Mod mods[] = {Mod::ABSENT, Mod::FAILING, Mod::OK};
    for (Mod r : mods) for (Mod a : mods) for (Mod f : mods) {
        FakeCredentialReader reader;
        FakeAccessPoint      access;
        FakeFeedback         fb;
        FakeClock            clk;
        reader.init_result = (r == Mod::FAILING) ? PORTUNUS_ERR_DEVICE_NOT_FOUND : PORTUNUS_OK;
        access.init_result = (a == Mod::FAILING) ? PORTUNUS_ERR_GPIO_INIT : PORTUNUS_OK;
        fb.init_result     = (f == Mod::FAILING) ? PORTUNUS_ERR_GPIO_INIT : PORTUNUS_OK;
        SystemFSM fsm(r == Mod::ABSENT ? nullptr : &reader,
                      a == Mod::ABSENT ? nullptr : &access,
                      f == Mod::ABSENT ? nullptr : &fb, &clk);
        TEST_ASSERT_EQUAL(PORTUNUS_OK, fsm.init());
        const system_capabilities_t caps = fsm.capabilities();
        TEST_ASSERT_EQUAL(r == Mod::OK, caps.has_reader);
        TEST_ASSERT_EQUAL(a == Mod::OK, caps.has_access_point);
        TEST_ASSERT_EQUAL(f == Mod::OK, caps.has_feedback);
    }
}

/* FSM-11 (D-T11): a failing access point is not fatal. init() succeeds, and
 * a grant still shows feedback without touching the strike. */
void test_init_nonfatal_when_access_point_fails(void) {
    FakeAccessPoint access;
    FakeFeedback    fb;
    FakeClock       clk;
    access.init_result = PORTUNUS_ERR_GPIO_INIT;
    SystemFSM fsm(nullptr, &access, &fb, &clk);
    TEST_ASSERT_EQUAL(PORTUNUS_OK, fsm.init());
    TEST_ASSERT_FALSE(fsm.capabilities().has_access_point);

    SystemFSMTestFixture fix(fsm);
    inject_decision(fix, make_grant());
    TEST_ASSERT_EQUAL(0, access.unlocks);
    TEST_ASSERT_FALSE(fix.strike_energized());
    TEST_ASSERT_EQUAL(feedback_type_t::ACCESS_GRANTED, fb.last());
}

/* FSM-12: init reads the door state, so the first poll of an unchanged door
 * publishes nothing, open or closed. */
void test_initial_door_state_read_at_init(void) {
    for (const bool open : {false, true}) {
        event_bus_fake_reset();
        event_bus_init();
        FakeAccessPoint access;
        FakeClock       clk;
        access.door_open = open;
        SystemFSM fsm(nullptr, &access, nullptr, &clk);
        TEST_ASSERT_EQUAL(PORTUNUS_OK, fsm.init());
        SystemFSMTestFixture fix(fsm);
        TEST_ASSERT_EQUAL(open, fix.last_door_open());
        fix.poll_reed();
        TEST_ASSERT_EQUAL_size_t(0, event_bus_fake_count_of(EVENT_DOOR_OPENED));
        TEST_ASSERT_EQUAL_size_t(0, event_bus_fake_count_of(EVENT_DOOR_CLOSED));
    }
}

/* FSM-13: open then close publishes DOOR_OPENED then DOOR_CLOSED, each with
 * the clock time of the poll that saw it. */
void test_door_open_close_events_with_timestamps(void) {
    FakeAccessPoint access;
    FakeClock       clk;
    SystemFSM fsm(nullptr, &access, nullptr, &clk);
    TEST_ASSERT_EQUAL(PORTUNUS_OK, fsm.init());
    SystemFSMTestFixture fix(fsm);

    clk.set(1000);
    access.door_open = true;
    fix.poll_reed();
    fix.poll_reed();                           /* unchanged: no second event */
    clk.set(1500);
    access.door_open = false;
    fix.poll_reed();

    TEST_ASSERT_EQUAL_size_t(2, event_bus_fake_count());
    TEST_ASSERT_EQUAL(EVENT_DOOR_OPENED, event_bus_fake_at(0)->id);
    TEST_ASSERT_EQUAL_INT64(1000, event_bus_fake_at(0)->payload.door_opened.timestamp_ms);
    TEST_ASSERT_EQUAL(EVENT_DOOR_CLOSED, event_bus_fake_at(1)->id);
    TEST_ASSERT_EQUAL_INT64(1500, event_bus_fake_at(1)->payload.door_closed.timestamp_ms);
}

/* Grant on a fresh FSM with an access point: strike energized at clk now. */
static void grant_and_open(SystemFSMTestFixture &fix, FakeAccessPoint &access) {
    inject_decision(fix, make_grant());
    TEST_ASSERT_TRUE(fix.strike_energized());
    access.door_open = true;
    fix.poll_reed();
}

/* FSM-14: closing the door during the hold re-locks at once and cancels the
 * timer, so no UNLOCK_TIMEOUT follows. */
void test_close_during_hold_relocks_early(void) {
    FakeAccessPoint access;
    FakeClock       clk;
    SystemFSM fsm(nullptr, &access, nullptr, &clk);
    TEST_ASSERT_EQUAL(PORTUNUS_OK, fsm.init());
    SystemFSMTestFixture fix(fsm);

    grant_and_open(fix, access);
    clk.advance(1000);
    access.door_open = false;
    fix.poll_reed();
    TEST_ASSERT_EQUAL(1, access.locks);
    TEST_ASSERT_FALSE(fix.strike_energized());
    TEST_ASSERT_EQUAL_INT64(0, fix.unlock_deadline());

    clk.advance(UNLOCK_HOLD_MS);
    fix.tick_unlock_timer();
    TEST_ASSERT_EQUAL(1, access.locks);
    TEST_ASSERT_EQUAL_size_t(0, event_bus_fake_count_of(EVENT_FSM_UNLOCK_TIMEOUT));
}

/* FSM-15: if the early re-lock fails the timer stays armed with its original
 * deadline; the timer path retries at that deadline and publishes
 * UNLOCK_TIMEOUT once. */
void test_early_relock_failure_falls_back_to_timer(void) {
    FakeAccessPoint access;
    FakeClock       clk;
    SystemFSM fsm(nullptr, &access, nullptr, &clk);
    TEST_ASSERT_EQUAL(PORTUNUS_OK, fsm.init());
    SystemFSMTestFixture fix(fsm);

    grant_and_open(fix, access);
    const int64_t deadline = fix.unlock_deadline();
    access.lock_result = PORTUNUS_ERR_GPIO_INIT;
    access.door_open   = false;
    fix.poll_reed();
    TEST_ASSERT_TRUE(fix.strike_energized());
    TEST_ASSERT_EQUAL_INT64(deadline, fix.unlock_deadline());
    TEST_ASSERT_EQUAL_size_t(1, event_bus_fake_count_of(EVENT_DOOR_CLOSED));

    access.lock_result = PORTUNUS_OK;
    clk.set(deadline - 1);
    fix.tick_unlock_timer();                  /* not yet: no early retry */
    TEST_ASSERT_EQUAL(0, access.locks);
    clk.set(deadline);
    fix.tick_unlock_timer();
    TEST_ASSERT_EQUAL(1, access.locks);
    TEST_ASSERT_FALSE(fix.strike_energized());
    fix.tick_unlock_timer();
    TEST_ASSERT_EQUAL_size_t(1, event_bus_fake_count_of(EVENT_FSM_UNLOCK_TIMEOUT));
}

/* FSM-16: a door closing while the strike is not energized never calls lock(). */
void test_close_while_locked_does_not_lock(void) {
    FakeAccessPoint access;
    FakeClock       clk;
    SystemFSM fsm(nullptr, &access, nullptr, &clk);
    TEST_ASSERT_EQUAL(PORTUNUS_OK, fsm.init());
    SystemFSMTestFixture fix(fsm);

    access.door_open = true;
    fix.poll_reed();
    access.door_open = false;
    fix.poll_reed();
    TEST_ASSERT_EQUAL(0, access.locks);
    TEST_ASSERT_EQUAL_size_t(1, event_bus_fake_count_of(EVENT_DOOR_CLOSED));
}

/* FSM-17: a second grant during the hold unlocks again and moves the
 * deadline to now + hold, so the door stays open for the second person. */
void test_second_grant_extends_hold(void) {
    FakeAccessPoint access;
    FakeClock       clk;
    SystemFSM fsm(nullptr, &access, nullptr, &clk);
    TEST_ASSERT_EQUAL(PORTUNUS_OK, fsm.init());
    SystemFSMTestFixture fix(fsm);

    clk.set(1000);
    inject_decision(fix, make_grant());
    TEST_ASSERT_EQUAL_INT64(1000 + UNLOCK_HOLD_MS, fix.unlock_deadline());
    clk.advance(UNLOCK_HOLD_MS / 2);
    inject_decision(fix, make_grant());
    TEST_ASSERT_EQUAL(2, access.unlocks);
    TEST_ASSERT_EQUAL_INT64(clk.now_ms() + UNLOCK_HOLD_MS, fix.unlock_deadline());

    clk.set(1000 + UNLOCK_HOLD_MS);           /* the first deadline: still held */
    fix.tick_unlock_timer();
    TEST_ASSERT_TRUE(fix.strike_energized());
    TEST_ASSERT_EQUAL(0, access.locks);
}

/* FSM-19: the bridge queue holds FSM_EVENT_QUEUE_LEN (8) events; the 9th is
 * dropped after the bridge's short send timeout instead of blocking the bus
 * dispatcher. */
void test_bridge_drops_ninth_event(void) {
    FakeAccessPoint access;
    FakeClock       clk;
    SystemFSM fsm(nullptr, &access, nullptr, &clk);
    TEST_ASSERT_EQUAL(PORTUNUS_OK, fsm.init());
    SystemFSMTestFixture fix(fsm);

    const portunus_event_t e = make_grant();
    for (int i = 0; i < 8; ++i) fix.bridge(e);
    TEST_ASSERT_EQUAL(8, fix.queued());
    fix.bridge(e);
    TEST_ASSERT_EQUAL(8, fix.queued());
}

/* FSM-20: an event bridged before init() (no queue yet) is dropped quietly. */
void test_bridge_before_init_is_noop(void) {
    FakeAccessPoint access;
    FakeClock       clk;
    SystemFSM fsm(nullptr, &access, nullptr, &clk);
    SystemFSMTestFixture fix(fsm);
    fix.bridge(make_grant());
    TEST_ASSERT_EQUAL(0, access.unlocks);
}

/* FSM-21: UNLOCK_TIMEOUT is published only once lock() has succeeded; while
 * it fails the strike stays energized and every tick retries. */
void test_unlock_timeout_only_after_lock_succeeds(void) {
    FakeAccessPoint access;
    FakeClock       clk;
    SystemFSM fsm(nullptr, &access, nullptr, &clk);
    TEST_ASSERT_EQUAL(PORTUNUS_OK, fsm.init());
    SystemFSMTestFixture fix(fsm);

    inject_decision(fix, make_grant());
    access.lock_result = PORTUNUS_ERR_GPIO_INIT;
    clk.advance(UNLOCK_HOLD_MS);
    for (int i = 0; i < 3; ++i) {
        fix.tick_unlock_timer();
        TEST_ASSERT_TRUE(fix.strike_energized());
    }
    TEST_ASSERT_EQUAL_size_t(0, event_bus_fake_count_of(EVENT_FSM_UNLOCK_TIMEOUT));

    access.lock_result = PORTUNUS_OK;
    fix.tick_unlock_timer();
    fix.tick_unlock_timer();
    TEST_ASSERT_FALSE(fix.strike_energized());
    TEST_ASSERT_EQUAL(1, access.locks);
    TEST_ASSERT_EQUAL_size_t(1, event_bus_fake_count_of(EVENT_FSM_UNLOCK_TIMEOUT));
}

#endif /* !PORTUNUS_TEST_REAL_BUS */

/* ── Fake bus conformance (T3): the fake follows the real bus's contract ─── */

#ifndef PORTUNUS_TEST_REAL_BUS

namespace {
int g_late_hits = 0;
void late_handler(const portunus_event_t *, void *) { ++g_late_hits; }
void subscribing_handler(const portunus_event_t *, void *) {
    event_bus_subscribe(EVENT_ACCESS_GRANTED, late_handler, nullptr);
}
void noop_handler(const portunus_event_t *, void *) {}
}  // namespace

/* A handler may subscribe mid-dispatch (the real bus snapshots under its
 * mutex for this); the new handler misses the in-flight event and gets the
 * next. The fake used to iterate the live vector, so this reallocated it
 * under the loop. */
void test_fake_bus_subscribe_from_handler(void) {
    g_late_hits = 0;
    TEST_ASSERT_EQUAL(PORTUNUS_OK,
                      event_bus_subscribe(EVENT_ACCESS_GRANTED, subscribing_handler, nullptr));
    portunus_event_t e = make_grant();
    TEST_ASSERT_EQUAL(PORTUNUS_OK, event_bus_publish(&e));
    TEST_ASSERT_EQUAL_INT(0, g_late_hits);
    TEST_ASSERT_EQUAL(PORTUNUS_OK, event_bus_publish(&e));
    TEST_ASSERT_EQUAL_INT(1, g_late_hits);
}

/* MAX_EVENT_SUBSCRIBERS are accepted, the next is refused; NULL arguments
 * and use before init return the real bus's codes. */
void test_fake_bus_limits_and_arguments(void) {
    for (int i = 0; i < MAX_EVENT_SUBSCRIBERS; ++i) {
        TEST_ASSERT_EQUAL(PORTUNUS_OK,
                          event_bus_subscribe(EVENT_HEARTBEAT, noop_handler, nullptr));
    }
    TEST_ASSERT_EQUAL(PORTUNUS_ERR_MAX_SUBSCRIBERS,
                      event_bus_subscribe(EVENT_HEARTBEAT, noop_handler, nullptr));
    TEST_ASSERT_EQUAL_size_t(MAX_EVENT_SUBSCRIBERS, event_bus_fake_subscriber_count());

    TEST_ASSERT_EQUAL(PORTUNUS_ERR_INVALID_ARG, event_bus_publish(nullptr));
    TEST_ASSERT_EQUAL(PORTUNUS_ERR_INVALID_ARG,
                      event_bus_subscribe(EVENT_HEARTBEAT, nullptr, nullptr));
    TEST_ASSERT_EQUAL(PORTUNUS_ERR_ALREADY_INIT, event_bus_init());

    event_bus_fake_reset();
    portunus_event_t e = make_grant();
    TEST_ASSERT_EQUAL(PORTUNUS_ERR_NOT_INIT, event_bus_publish(&e));
    TEST_ASSERT_EQUAL(PORTUNUS_ERR_NOT_INIT,
                      event_bus_subscribe(EVENT_HEARTBEAT, noop_handler, nullptr));
}

/* fail_next drops the next n publishes with the given error, records and
 * dispatches nothing for them, then the bus works normally again. */
void test_fake_bus_fail_next(void) {
    g_late_hits = 0;
    TEST_ASSERT_EQUAL(PORTUNUS_OK,
                      event_bus_subscribe(EVENT_ACCESS_GRANTED, late_handler, nullptr));
    event_bus_fake_fail_next(2, PORTUNUS_ERR_QUEUE_FULL);
    portunus_event_t e = make_grant();
    TEST_ASSERT_EQUAL(PORTUNUS_ERR_QUEUE_FULL, event_bus_publish(&e));
    TEST_ASSERT_EQUAL(PORTUNUS_ERR_QUEUE_FULL, event_bus_publish(&e));
    TEST_ASSERT_EQUAL(PORTUNUS_OK, event_bus_publish(&e));
    TEST_ASSERT_EQUAL_size_t(1, event_bus_fake_count());
    TEST_ASSERT_EQUAL_INT(1, g_late_hits);
}

#endif /* !PORTUNUS_TEST_REAL_BUS */

/* ── Concurrency tests (real bus, built with PORTUNUS_TEST_REAL_BUS=ON) ──── */

#ifdef PORTUNUS_TEST_REAL_BUS

void test_real_bus_publish_reaches_subscriber(void) {
    /* The real bus has no unsubscribe, so the subscriber outlives this test:
     * its context must have static storage (see decision D-T2). */
    static SemaphoreHandle_t s_rx = xSemaphoreCreateBinary();
    TEST_ASSERT_NOT_NULL(s_rx);
    TEST_ASSERT_EQUAL(PORTUNUS_OK, event_bus_subscribe(EVENT_ACCESS_GRANTED,
        [](const portunus_event_t *, void *ctx) {
            xSemaphoreGive(static_cast<SemaphoreHandle_t>(ctx));
        }, s_rx));

    portunus_event_t e = make_grant();
    TEST_ASSERT_EQUAL(PORTUNUS_OK, event_bus_publish(&e));

    /* Bounded wait on a real handoff instead of a fixed sleep on a volatile. */
    TEST_ASSERT_EQUAL(pdTRUE, xSemaphoreTake(s_rx, pdMS_TO_TICKS(1000)));
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
    RUN_TEST(test_credential_read_online_shows_card_read);
    RUN_TEST(test_credential_read_offline_shows_card_read_and_error);
    RUN_TEST(test_no_feedback_hardware_emits_nothing);
    RUN_TEST(test_strike_relocks_after_hold_expires);
    RUN_TEST(test_relock_failure_retries_on_next_tick);
    RUN_TEST(test_unsolicited_grant_is_ignored);
    RUN_TEST(test_decision_timeout_shows_deny);
    RUN_TEST(test_late_grant_after_timeout_is_ignored);
    RUN_TEST(test_each_pending_read_accepts_one_decision);
#ifndef PORTUNUS_TEST_REAL_BUS
    /* Init, door and hold timer (FSM-10..21) */
    RUN_TEST(test_init_capability_matrix);
    RUN_TEST(test_init_nonfatal_when_access_point_fails);
    RUN_TEST(test_initial_door_state_read_at_init);
    RUN_TEST(test_door_open_close_events_with_timestamps);
    RUN_TEST(test_close_during_hold_relocks_early);
    RUN_TEST(test_early_relock_failure_falls_back_to_timer);
    RUN_TEST(test_close_while_locked_does_not_lock);
    RUN_TEST(test_second_grant_extends_hold);
    RUN_TEST(test_bridge_drops_ninth_event);
    RUN_TEST(test_bridge_before_init_is_noop);
    RUN_TEST(test_unlock_timeout_only_after_lock_succeeds);
#endif

#ifndef PORTUNUS_TEST_REAL_BUS
    /* Fake bus conformance (T3) */
    RUN_TEST(test_fake_bus_subscribe_from_handler);
    RUN_TEST(test_fake_bus_limits_and_arguments);
    RUN_TEST(test_fake_bus_fail_next);
#endif

#ifdef PORTUNUS_TEST_REAL_BUS
    /* Concurrency suite — only meaningful with real async bus */
    RUN_TEST(test_real_bus_publish_reaches_subscriber);
#endif

    int failures = UNITY_END();
    exit(failures);
}
