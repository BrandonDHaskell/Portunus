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
#include "wifi_mgr_fake.hpp"

#ifndef PORTUNUS_TEST_REAL_BUS
#include "../components/event_bus/include/event_bus_fake.hpp"
#endif

#include <string.h>
#include <vector>
#include "freertos/semphr.h"

void setUp(void) {
#ifdef PORTUNUS_TEST_REAL_BUS
    event_bus_reset_for_test();   /* every real-bus test starts from a fresh bus */
#else
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
/* The pattern a one-shot would return to on the LED: the most recent
 * baseline indication (see led_pattern_is_baseline), or NONE. */
static feedback_type_t last_baseline(const FakeFeedback &fb) {
    for (auto it = fb.indications.rbegin(); it != fb.indications.rend(); ++it) {
        if (*it == feedback_type_t::SYSTEM_READY || *it == feedback_type_t::STRIKE_FAULT) {
            return *it;
        }
    }
    return feedback_type_t::NONE;
}

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
/* FSM-18 (D-T4): when the unlock signal cannot be sent, no hold timer starts
 * and the LED shows an error, never the grant. */
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
    TEST_ASSERT_EQUAL(0, fb.count_of(feedback_type_t::ACCESS_GRANTED));
    TEST_ASSERT_EQUAL(feedback_type_t::SYSTEM_ERROR, fb.last());
}

/* FSM-18 (D-T4): with no access point a grant shows an error. */
void test_grant_without_access_point_shows_error(void) {
    FakeFeedback fb;
    FakeClock    clk;

    SystemFSM fsm(nullptr, nullptr, &fb, &clk);
    TEST_ASSERT_EQUAL(PORTUNUS_OK, fsm.init());
    SystemFSMTestFixture fix(fsm);

    inject_decision(fix, make_grant());

    TEST_ASSERT_EQUAL(0, fb.count_of(feedback_type_t::ACCESS_GRANTED));
    TEST_ASSERT_EQUAL(feedback_type_t::SYSTEM_ERROR, fb.last());
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
    FakeFeedback    fb;
    FakeClock       clk;

    SystemFSM fsm(nullptr, &access, &fb, &clk);
    TEST_ASSERT_EQUAL(PORTUNUS_OK, fsm.init());
    SystemFSMTestFixture fix(fsm);

    inject_decision(fix, make_grant());
    access.lock_result = PORTUNUS_FAIL;  /* set AFTER unlock so grant succeeds */
    fb.reset();                          /* the offline card read showed an error */

    clk.advance(UNLOCK_HOLD_MS);
    fix.tick_unlock_timer();
    TEST_ASSERT_TRUE(fix.strike_energized());  /* still armed, retry next tick */
    fix.tick_unlock_timer();
    TEST_ASSERT_EQUAL(feedback_type_t::STRIKE_FAULT, fb.last());   /* shown once */
    TEST_ASSERT_EQUAL(1, fb.count_of(feedback_type_t::STRIKE_FAULT));
#ifndef PORTUNUS_TEST_REAL_BUS
    TEST_ASSERT_EQUAL_size_t(1, event_bus_fake_count_of(EVENT_STRIKE_FAULT));  /* for the heartbeat */
#endif

    /* A card tapped during the fault gets its one-shot, but nothing tells the
     * LED the door is fine: the one-shot ends on STRIKE_FAULT (DRV-07c). */
    inject_decision(fix, make_deny());
    TEST_ASSERT_EQUAL(feedback_type_t::ACCESS_DENIED, fb.last());
    TEST_ASSERT_EQUAL(feedback_type_t::STRIKE_FAULT, last_baseline(fb));
    TEST_ASSERT_TRUE(fix.lock_fault());

    access.lock_result = PORTUNUS_OK;
    fix.tick_unlock_timer();
    TEST_ASSERT_FALSE(fix.strike_energized());
    TEST_ASSERT_TRUE(access.locked);
    TEST_ASSERT_FALSE(fix.lock_fault());
    TEST_ASSERT_EQUAL(feedback_type_t::SYSTEM_READY, fb.last());
    TEST_ASSERT_EQUAL(feedback_type_t::SYSTEM_READY, last_baseline(fb));
#ifndef PORTUNUS_TEST_REAL_BUS
    TEST_ASSERT_EQUAL_size_t(1, event_bus_fake_count_of(EVENT_STRIKE_FAULT));
    TEST_ASSERT_EQUAL_size_t(1, event_bus_fake_count_of(EVENT_STRIKE_RECOVERED));
#endif
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
 * a grant shows an error without touching the strike (D-T4). */
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
    TEST_ASSERT_EQUAL(feedback_type_t::SYSTEM_ERROR, fb.last());
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

/* FSM-15: if the early re-lock fails, the FSM retries on every poll until
 * lock() succeeds, without waiting for the hold to expire. While it fails
 * the LED shows STRIKE_FAULT (the door may be stuck unlocked); once locked
 * it returns to SYSTEM_READY. The hold was cut short, so no UNLOCK_TIMEOUT. */
void test_early_relock_failure_retries_every_poll(void) {
    FakeAccessPoint access;
    FakeFeedback    fb;
    FakeClock       clk;
    SystemFSM fsm(nullptr, &access, &fb, &clk);
    TEST_ASSERT_EQUAL(PORTUNUS_OK, fsm.init());
    SystemFSMTestFixture fix(fsm);

    grant_and_open(fix, access);
    fb.reset();                                   /* the offline card read showed an error */
    access.lock_result = PORTUNUS_ERR_GPIO_INIT;
    access.door_open   = false;
    fix.poll_reed();
    TEST_ASSERT_TRUE(fix.strike_energized());
    TEST_ASSERT_TRUE(fix.relock_retry());
    TEST_ASSERT_TRUE(fix.lock_fault());
    TEST_ASSERT_EQUAL(feedback_type_t::STRIKE_FAULT, fb.last());
    TEST_ASSERT_EQUAL_size_t(1, event_bus_fake_count_of(EVENT_DOOR_CLOSED));

    for (int i = 0; i < 3; ++i) {                 /* still failing: no repeat error */
        clk.advance(100);
        fix.tick_relock_retry();
        TEST_ASSERT_TRUE(fix.strike_energized());
    }
    TEST_ASSERT_EQUAL(1, fb.count_of(feedback_type_t::STRIKE_FAULT));
    TEST_ASSERT_EQUAL_size_t(1, event_bus_fake_count_of(EVENT_STRIKE_FAULT));
    TEST_ASSERT_EQUAL_size_t(0, event_bus_fake_count_of(EVENT_STRIKE_RECOVERED));

    access.lock_result = PORTUNUS_OK;
    clk.advance(100);                             /* well inside the hold */
    fix.tick_relock_retry();
    TEST_ASSERT_EQUAL(1, access.locks);
    TEST_ASSERT_FALSE(fix.strike_energized());
    TEST_ASSERT_FALSE(fix.relock_retry());
    TEST_ASSERT_FALSE(fix.lock_fault());
    TEST_ASSERT_EQUAL(feedback_type_t::SYSTEM_READY, fb.last());
    TEST_ASSERT_EQUAL_size_t(1, event_bus_fake_count_of(EVENT_STRIKE_RECOVERED));

    clk.advance(UNLOCK_HOLD_MS);
    fix.tick_unlock_timer();
    TEST_ASSERT_EQUAL(1, access.locks);
    TEST_ASSERT_EQUAL_size_t(0, event_bus_fake_count_of(EVENT_FSM_UNLOCK_TIMEOUT));
}

/* A new grant while a re-lock retry is pending unlocks on purpose: the retry
 * stops and the hold timer governs the next re-lock. */
void test_new_grant_cancels_relock_retry(void) {
    FakeAccessPoint access;
    FakeClock       clk;
    SystemFSM fsm(nullptr, &access, nullptr, &clk);
    TEST_ASSERT_EQUAL(PORTUNUS_OK, fsm.init());
    SystemFSMTestFixture fix(fsm);

    grant_and_open(fix, access);
    access.lock_result = PORTUNUS_ERR_GPIO_INIT;
    access.door_open   = false;
    fix.poll_reed();
    TEST_ASSERT_TRUE(fix.relock_retry());

    access.lock_result = PORTUNUS_OK;
    inject_decision(fix, make_grant());
    TEST_ASSERT_FALSE(fix.relock_retry());
    TEST_ASSERT_TRUE(fix.strike_energized());
    fix.tick_relock_retry();                      /* no-op now */
    TEST_ASSERT_EQUAL(0, access.locks);
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

/* D-T5: an event lost to the full bridge queue is counted, and the FSM's
 * next pass shows a comm error, once per batch of losses. */
void test_dropped_event_shows_comm_error(void) {
    FakeAccessPoint access;
    FakeFeedback    fb;
    FakeClock       clk;
    SystemFSM fsm(nullptr, &access, &fb, &clk);
    TEST_ASSERT_EQUAL(PORTUNUS_OK, fsm.init());
    SystemFSMTestFixture fix(fsm);

    const portunus_event_t e = make_credential_read();
    for (int i = 0; i < 9; ++i) fix.bridge(e);     /* 8 fit, the 9th is lost */
    TEST_ASSERT_EQUAL_UINT32(1, fix.dropped());
    TEST_ASSERT_EQUAL_UINT32(1, event_bus_lost_events());   /* reported for the heartbeat */
    fix.check_dropped();
    TEST_ASSERT_EQUAL(feedback_type_t::SYSTEM_ERROR, fb.last());
    TEST_ASSERT_EQUAL(1, fb.count_of(feedback_type_t::SYSTEM_ERROR));
    fix.check_dropped();                            /* nothing new: no repeat */
    TEST_ASSERT_EQUAL(1, fb.count_of(feedback_type_t::SYSTEM_ERROR));
}

/* D-T5: a card read the reader task cannot publish (bus full) is counted
 * and shown the same way, so a tap never vanishes without a sign. */
void test_failed_card_publish_shows_comm_error(void) {
    FakeAccessPoint access;
    FakeFeedback    fb;
    FakeClock       clk;
    SystemFSM fsm(nullptr, &access, &fb, &clk);
    TEST_ASSERT_EQUAL(PORTUNUS_OK, fsm.init());
    SystemFSMTestFixture fix(fsm);

    const credential_t card = {{0x04, 0xA3, 0x2B, 0x1C}, 4};
    event_bus_fake_fail_next(1, PORTUNUS_ERR_QUEUE_FULL);
    fix.publish_credential(card);
    TEST_ASSERT_EQUAL_size_t(0, event_bus_fake_count_of(EVENT_CREDENTIAL_READ));
    TEST_ASSERT_EQUAL_UINT32(1, fix.dropped());
    fix.check_dropped();
    TEST_ASSERT_EQUAL(feedback_type_t::SYSTEM_ERROR, fb.last());

    fix.publish_credential(card);                   /* bus healthy again */
    TEST_ASSERT_EQUAL_size_t(1, event_bus_fake_count_of(EVENT_CREDENTIAL_READ));
    TEST_ASSERT_EQUAL_UINT32(1, fix.dropped());
}

/* D-T5: a queue kept near full warns once (queue_pressure), and the warning
 * clears once it drains. Nothing is dropped along the way. */
void test_queue_pressure_warns_when_kept_full(void) {
    FakeAccessPoint access;
    FakeClock       clk;
    SystemFSM fsm(nullptr, &access, nullptr, &clk);
    TEST_ASSERT_EQUAL(PORTUNUS_OK, fsm.init());
    SystemFSMTestFixture fix(fsm);

    const portunus_event_t e = make_grant();        /* unsolicited: ignored */
    for (int i = 0; i < 8; ++i) fix.bridge(e);
    for (int i = 0; i < 4; ++i) {                   /* consumer keeps it at 7 of 8 */
        TEST_ASSERT_TRUE(fix.drain_one());
        fix.bridge(e);
        TEST_ASSERT_FALSE(fix.queue_pressure_warned());
    }
    TEST_ASSERT_TRUE(fix.drain_one());              /* fifth high sample */
    TEST_ASSERT_TRUE(fix.queue_pressure_warned());

    while (fix.drain_one()) {}
    TEST_ASSERT_FALSE(fix.queue_pressure_warned());
    TEST_ASSERT_EQUAL_UINT32(0, fix.dropped());
    TEST_ASSERT_EQUAL(0, access.unlocks);
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

/* ── Lifecycle: start() and stop() with real tasks (FSM-22, D-T9) ─────── */

/* Poll until pred() holds, giving the FSM tasks time to run. */
template <typename Pred>
static bool wait_until(Pred pred, int timeout_ms = 2000) {
    for (int t = 0; t < timeout_ms; t += 10) {
        if (pred()) return true;
        vTaskDelay(pdMS_TO_TICKS(10));
    }
    return pred();
}

struct LiveRig {
    FakeCredentialReader reader;
    FakeAccessPoint      access;
    FakeFeedback         fb;
    FakeClock            clk;
    SystemFSM            fsm{&reader, &access, &fb, &clk};
};

/* FSM-22: start() subscribes the FSM's three events, publishes BOOT_COMPLETE,
 * shows SYSTEM_READY and goes OPERATIONAL; a second start() is refused. */
void test_start_subscribes_and_reports_ready(void) {
    LiveRig r;
    TEST_ASSERT_EQUAL(PORTUNUS_OK, r.fsm.init());
    TEST_ASSERT_EQUAL(PORTUNUS_OK, r.fsm.start());
    TEST_ASSERT_EQUAL_size_t(3, event_bus_fake_subscriber_count());
    for (portunus_event_id_t id : SystemFSM::kSubscribedEvents) {
        TEST_ASSERT_EQUAL_size_t(1, event_bus_fake_subscribers_for(id));
    }
    TEST_ASSERT_EQUAL_size_t(1, event_bus_fake_count_of(EVENT_SYSTEM_BOOT_COMPLETE));
    TEST_ASSERT_EQUAL(feedback_type_t::SYSTEM_READY, r.fb.last());
    TEST_ASSERT_EQUAL(SYSTEM_STATE_OPERATIONAL, r.fsm.state());
    /* Initial health for the heartbeat: the door state (reed fitted in this
     * build); no READ_ERROR, since the reader came up. */
    TEST_ASSERT_EQUAL_size_t(1, event_bus_fake_count_of(EVENT_DOOR_CLOSED));
    TEST_ASSERT_EQUAL_size_t(0, event_bus_fake_count_of(EVENT_DOOR_OPENED));
    TEST_ASSERT_EQUAL_size_t(0, event_bus_fake_count_of(EVENT_CREDENTIAL_READ_ERROR));
    TEST_ASSERT_EQUAL(PORTUNUS_ERR_ALREADY_INIT, r.fsm.start());
    TEST_ASSERT_EQUAL(PORTUNUS_OK, r.fsm.stop());
}

/* A reader that failed init never enters degraded mode through the poll
 * loop, so start() reports it once (reader_ok=false in heartbeats), and an
 * open door is reported as open. */
void test_start_reports_dead_reader_and_open_door(void) {
    LiveRig r;
    r.reader.init_result = PORTUNUS_ERR_SPI_INIT;
    r.access.door_open   = true;
    TEST_ASSERT_EQUAL(PORTUNUS_OK, r.fsm.init());
    TEST_ASSERT_EQUAL(PORTUNUS_OK, r.fsm.start());
    TEST_ASSERT_EQUAL_size_t(1, event_bus_fake_count_of(EVENT_CREDENTIAL_READ_ERROR));
    TEST_ASSERT_EQUAL_size_t(1, event_bus_fake_count_of(EVENT_DOOR_OPENED));
    TEST_ASSERT_EQUAL_size_t(0, event_bus_fake_count_of(EVENT_DOOR_CLOSED));
    TEST_ASSERT_EQUAL(PORTUNUS_OK, r.fsm.stop());
}

/* A stop() while the reader is degraded must not leave it unpolled after the
 * restart: degraded mode clears has_reader, and start() used to skip the
 * poll task on it, so the reader was never probed again. The new task starts
 * in recovery mode and reports RECOVERED once the reader answers. */
void test_restart_while_degraded_probes_reader(void) {
    LiveRig r;
    TEST_ASSERT_EQUAL(PORTUNUS_OK, r.fsm.init());
    r.reader.init_result = PORTUNUS_ERR_SPI_INIT;            /* probes fail: stays degraded */
    for (int i = 0; i < 5; ++i) r.reader.enqueue_error(PORTUNUS_ERR_SPI_TRANSFER);
    TEST_ASSERT_EQUAL(PORTUNUS_OK, r.fsm.start());
    TEST_ASSERT_TRUE(wait_until([] {
        return event_bus_fake_count_of(EVENT_CREDENTIAL_READ_ERROR) >= 1;
    }, 5000));
    TEST_ASSERT_FALSE(r.fsm.capabilities().has_reader);
    TEST_ASSERT_EQUAL(PORTUNUS_OK, r.fsm.stop());

    r.reader.init_result = PORTUNUS_OK;                      /* the reader is back */
    TEST_ASSERT_EQUAL(PORTUNUS_OK, r.fsm.start());
    TEST_ASSERT_TRUE(wait_until([] {
        return event_bus_fake_count_of(EVENT_CREDENTIAL_READER_RECOVERED) >= 1;
    }, 2000));
    TEST_ASSERT_TRUE(wait_until([&r] { return r.fsm.capabilities().has_reader; }, 1000));
    TEST_ASSERT_EQUAL(PORTUNUS_OK, r.fsm.stop());
}

/* D-T9: stop() unsubscribes, ends both tasks promptly, locks the strike,
 * and leaves the FSM ready to start again; a second stop() is a no-op. */
void test_stop_locks_and_allows_restart(void) {
    LiveRig r;
    TEST_ASSERT_EQUAL(PORTUNUS_OK, r.fsm.init());
    TEST_ASSERT_EQUAL(PORTUNUS_OK, r.fsm.start());
    vTaskDelay(pdMS_TO_TICKS(100));                /* both tasks inside their loops */

    const TickType_t t0 = xTaskGetTickCount();
    TEST_ASSERT_EQUAL(PORTUNUS_OK, r.fsm.stop());
    TEST_ASSERT_LESS_THAN_UINT32(500, (xTaskGetTickCount() - t0) * portTICK_PERIOD_MS);
    TEST_ASSERT_EQUAL_size_t(0, event_bus_fake_subscriber_count());
    TEST_ASSERT_EQUAL(1, r.access.locks);
    TEST_ASSERT_TRUE(r.access.locked);

    TEST_ASSERT_EQUAL(PORTUNUS_OK, r.fsm.stop());
    TEST_ASSERT_EQUAL(1, r.access.locks);

    TEST_ASSERT_EQUAL(PORTUNUS_OK, r.fsm.start());
    TEST_ASSERT_EQUAL_size_t(3, event_bus_fake_subscriber_count());
    TEST_ASSERT_EQUAL_size_t(2, event_bus_fake_count_of(EVENT_SYSTEM_BOOT_COMPLETE));
    TEST_ASSERT_EQUAL(PORTUNUS_OK, r.fsm.stop());
}

/* D-T9 decision 1: stopping during an unlock hold locks the strike rather
 * than leaving the door open with no task left to re-lock it. */
void test_stop_during_hold_locks_strike(void) {
    wifi_mgr_fake_set_connected(true);
    LiveRig r;
    TEST_ASSERT_EQUAL(PORTUNUS_OK, r.fsm.init());
    TEST_ASSERT_EQUAL(PORTUNUS_OK, r.fsm.start());

    r.reader.enqueue({{0x04, 0xA3, 0x2B, 0x1C}, 4});      /* the poll task reads it */
    TEST_ASSERT_TRUE(wait_until([] { return event_bus_fake_count_of(EVENT_CREDENTIAL_READ) == 1; }));
    portunus_event_t grant = make_grant();
    TEST_ASSERT_EQUAL(PORTUNUS_OK, event_bus_publish(&grant));
    TEST_ASSERT_TRUE(wait_until([&] { return r.access.unlocks == 1; }));
    TEST_ASSERT_FALSE(r.access.locked);                   /* hold running (fake clock) */

    TEST_ASSERT_EQUAL(PORTUNUS_OK, r.fsm.stop());
    TEST_ASSERT_TRUE(r.access.locked);
    TEST_ASSERT_EQUAL(1, r.access.locks);
    wifi_mgr_fake_set_connected(false);
}

/* D-T9: stop() lets the poll task finish a reader transaction instead of
 * deleting it mid-read (on hardware: mid SPI transfer, holding the bus). */
void test_stop_waits_for_poll_task(void) {
    LiveRig r;
    r.reader.read_delay_ms = 300;
    TEST_ASSERT_EQUAL(PORTUNUS_OK, r.fsm.init());
    TEST_ASSERT_EQUAL(PORTUNUS_OK, r.fsm.start());
    TEST_ASSERT_TRUE(wait_until([&] { return r.reader.in_read; }));

    TEST_ASSERT_EQUAL(PORTUNUS_OK, r.fsm.stop());
    TEST_ASSERT_FALSE(r.reader.in_read);                  /* the read finished */
}

/* D-T9 decision 1: a lock that fails on stop is reported, not hidden: the
 * error code says so and the LED shows STRIKE_FAULT, also after a restart,
 * until a lock succeeds. */
void test_stop_reports_lock_failure(void) {
    LiveRig r;
    TEST_ASSERT_EQUAL(PORTUNUS_OK, r.fsm.init());
    TEST_ASSERT_EQUAL(PORTUNUS_OK, r.fsm.start());
    r.access.lock_result = PORTUNUS_ERR_GPIO_INIT;
    TEST_ASSERT_EQUAL(PORTUNUS_ERR_LOCK_FAILED, r.fsm.stop());
    TEST_ASSERT_EQUAL(feedback_type_t::STRIKE_FAULT, r.fb.last());
    TEST_ASSERT_EQUAL_size_t(0, event_bus_fake_subscriber_count());
    TEST_ASSERT_EQUAL_size_t(1, event_bus_fake_count_of(EVENT_STRIKE_FAULT));

    TEST_ASSERT_EQUAL(PORTUNUS_OK, r.fsm.start());
    TEST_ASSERT_EQUAL(feedback_type_t::STRIKE_FAULT, r.fb.last());

    r.access.lock_result = PORTUNUS_OK;
    TEST_ASSERT_EQUAL(PORTUNUS_OK, r.fsm.stop());
    TEST_ASSERT_EQUAL_size_t(1, event_bus_fake_count_of(EVENT_STRIKE_RECOVERED));
    TEST_ASSERT_EQUAL(PORTUNUS_OK, r.fsm.start());
    TEST_ASSERT_EQUAL(feedback_type_t::SYSTEM_READY, r.fb.last());
    TEST_ASSERT_EQUAL(PORTUNUS_OK, r.fsm.stop());
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

/* D-T9: the fake supports unsubscribe with the real bus's contract. */
void test_fake_bus_unsubscribe(void) {
    g_late_hits = 0;
    TEST_ASSERT_EQUAL(PORTUNUS_OK, event_bus_subscribe(EVENT_ACCESS_GRANTED, late_handler, nullptr));
    TEST_ASSERT_EQUAL(PORTUNUS_OK, event_bus_unsubscribe(EVENT_ACCESS_GRANTED, late_handler, nullptr));
    portunus_event_t e = make_grant();
    TEST_ASSERT_EQUAL(PORTUNUS_OK, event_bus_publish(&e));
    TEST_ASSERT_EQUAL_INT(0, g_late_hits);
    TEST_ASSERT_EQUAL(PORTUNUS_ERR_NOT_SUBSCRIBED,
                      event_bus_unsubscribe(EVENT_ACCESS_GRANTED, late_handler, nullptr));
    TEST_ASSERT_EQUAL_size_t(0, event_bus_fake_subscriber_count());
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
    TEST_ASSERT_EQUAL_UINT32(2, event_bus_dropped_events());
    TEST_ASSERT_EQUAL_size_t(1, event_bus_fake_count());
    TEST_ASSERT_EQUAL_INT(1, g_late_hits);
}

#endif /* !PORTUNUS_TEST_REAL_BUS */

/* ── Concurrency tests (real bus, built with PORTUNUS_TEST_REAL_BUS=ON) ──── */

#ifdef PORTUNUS_TEST_REAL_BUS

void test_real_bus_publish_reaches_subscriber(void) {
    /* setUp's reset drops this subscriber before the next test, but the
     * semaphore is created once and reused. */
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

/* ── Event bus on the real dispatcher (BUS-01, 03, 05, 07, 08) ────────────── */

namespace {

static constexpr TickType_t WAIT = pdMS_TO_TICKS(1000);

/* Records heartbeat sequence numbers as the dispatcher delivers them. */
std::vector<uint32_t> g_seen;
SemaphoreHandle_t     g_delivered = nullptr;   /* counting: one give per delivery */

void recorder(const portunus_event_t *e, void *) {
    g_seen.push_back(e->payload.heartbeat.sequence);
    xSemaphoreGive(g_delivered);
}

void reset_recorder() {
    g_seen.clear();
    if (g_delivered == nullptr) g_delivered = xSemaphoreCreateCounting(64, 0);
    while (xSemaphoreTake(g_delivered, 0) == pdTRUE) {}
}

bool wait_deliveries(int n) {
    for (int i = 0; i < n; ++i) {
        if (xSemaphoreTake(g_delivered, WAIT) != pdTRUE) return false;
    }
    return true;
}

portunus_err_t publish_heartbeat(uint32_t seq) {
    portunus_event_t e;
    memset(&e, 0, sizeof(e));
    e.id = EVENT_HEARTBEAT;
    e.payload.heartbeat.sequence = seq;
    return event_bus_publish(&e);
}

}  // namespace

/* BUS-01: a second init is refused and leaves the running bus intact. */
void test_real_bus_second_init_refused(void) {
    TEST_ASSERT_EQUAL(PORTUNUS_ERR_ALREADY_INIT, event_bus_init());
    reset_recorder();
    TEST_ASSERT_EQUAL(PORTUNUS_OK, event_bus_subscribe(EVENT_HEARTBEAT, recorder, nullptr));
    TEST_ASSERT_EQUAL(PORTUNUS_OK, publish_heartbeat(7));
    TEST_ASSERT_TRUE(wait_deliveries(1));
}

/* BUS-03 (F12): subscribing before init is refused with NOT_INIT instead of
 * taking the not-yet-created mutex; publishing is refused too. */
void test_real_bus_subscribe_before_init(void) {
    event_bus_reset_for_test();
    TEST_ASSERT_EQUAL(PORTUNUS_ERR_NOT_INIT,
                      event_bus_subscribe(EVENT_HEARTBEAT, recorder, nullptr));
    TEST_ASSERT_EQUAL(PORTUNUS_ERR_NOT_INIT, publish_heartbeat(0));
    TEST_ASSERT_EQUAL(PORTUNUS_OK, event_bus_init());
}

/* BUS-05: the dispatcher delivers events in publish order. */
void test_real_bus_delivers_in_publish_order(void) {
    reset_recorder();
    TEST_ASSERT_EQUAL(PORTUNUS_OK, event_bus_subscribe(EVENT_HEARTBEAT, recorder, nullptr));
    for (uint32_t i = 0; i < 10; ++i) TEST_ASSERT_EQUAL(PORTUNUS_OK, publish_heartbeat(i));
    TEST_ASSERT_TRUE(wait_deliveries(10));
    for (uint32_t i = 0; i < 10; ++i) TEST_ASSERT_EQUAL_UINT32(i, g_seen[i]);
}

namespace {
SemaphoreHandle_t g_entered = nullptr;
SemaphoreHandle_t g_release = nullptr;
bool              g_block_once = false;

/* Holds the dispatcher inside the first delivery until released. */
void blocking_recorder(const portunus_event_t *e, void *ctx) {
    if (g_block_once) {
        g_block_once = false;
        xSemaphoreGive(g_entered);
        xSemaphoreTake(g_release, portMAX_DELAY);
    }
    recorder(e, ctx);
}
}  // namespace

/* BUS-07: with the dispatcher held in a handler, EVENT_QUEUE_LENGTH more
 * publishes queue up, and the next returns QUEUE_FULL only after waiting at
 * least EVENT_QUEUE_TIMEOUT_MS. Released, the dispatcher delivers them all. */
void test_real_bus_queue_full_after_timeout(void) {
    if (g_entered == nullptr) g_entered = xSemaphoreCreateBinary();
    if (g_release == nullptr) g_release = xSemaphoreCreateBinary();
    reset_recorder();
    g_block_once = true;
    TEST_ASSERT_EQUAL(PORTUNUS_OK,
                      event_bus_subscribe(EVENT_HEARTBEAT, blocking_recorder, nullptr));

    TEST_ASSERT_EQUAL(PORTUNUS_OK, publish_heartbeat(0));
    TEST_ASSERT_EQUAL(pdTRUE, xSemaphoreTake(g_entered, WAIT));   /* dispatcher held */

    for (uint32_t i = 1; i <= EVENT_QUEUE_LENGTH; ++i) {
        TEST_ASSERT_EQUAL(PORTUNUS_OK, publish_heartbeat(i));
    }
    const TickType_t t0 = xTaskGetTickCount();
    TEST_ASSERT_EQUAL_UINT32(0, event_bus_dropped_events());
    TEST_ASSERT_EQUAL(PORTUNUS_ERR_QUEUE_FULL, publish_heartbeat(999));
    const uint32_t waited_ms = (xTaskGetTickCount() - t0) * portTICK_PERIOD_MS;
    TEST_ASSERT_GREATER_OR_EQUAL_UINT32(EVENT_QUEUE_TIMEOUT_MS, waited_ms);
    TEST_ASSERT_EQUAL_UINT32(1, event_bus_dropped_events());   /* D-T5: counted */

    xSemaphoreGive(g_release);
    TEST_ASSERT_TRUE(wait_deliveries(EVENT_QUEUE_LENGTH + 1));
    for (uint32_t i = 0; i <= EVENT_QUEUE_LENGTH; ++i) TEST_ASSERT_EQUAL_UINT32(i, g_seen[i]);
}

namespace {
int  g_late_calls = 0;
void late_recorder(const portunus_event_t *e, void *ctx) { ++g_late_calls; recorder(e, ctx); }
void subscribing_recorder(const portunus_event_t *e, void *ctx) {
    if (e->payload.heartbeat.sequence == 0) {
        event_bus_subscribe(EVENT_HEARTBEAT, late_recorder, nullptr);
    }
    recorder(e, ctx);
}
}  // namespace

/* BUS-08: a handler subscribing from the dispatcher does not deadlock on the
 * subscriber mutex; the new handler misses the event in flight and gets the
 * next. */
void test_real_bus_subscribe_from_handler(void) {
    reset_recorder();
    g_late_calls = 0;
    TEST_ASSERT_EQUAL(PORTUNUS_OK,
                      event_bus_subscribe(EVENT_HEARTBEAT, subscribing_recorder, nullptr));
    TEST_ASSERT_EQUAL(PORTUNUS_OK, publish_heartbeat(0));
    TEST_ASSERT_TRUE(wait_deliveries(1));
    TEST_ASSERT_EQUAL_INT(0, g_late_calls);

    TEST_ASSERT_EQUAL(PORTUNUS_OK, publish_heartbeat(1));
    TEST_ASSERT_TRUE(wait_deliveries(2));                         /* both handlers */
    TEST_ASSERT_EQUAL_INT(1, g_late_calls);
}

namespace {
volatile bool g_slow_done = false;
void slow_handler(const portunus_event_t *e, void *ctx) {
    xSemaphoreGive(g_entered);                     /* delivery has started */
    vTaskDelay(pdMS_TO_TICKS(300));                /* still running when unsubscribe is called */
    g_slow_done = true;
    recorder(e, ctx);
}
void self_unsubscriber(const portunus_event_t *e, void *ctx) {
    recorder(e, ctx);
    event_bus_unsubscribe(EVENT_HEARTBEAT, self_unsubscriber, nullptr);
}
}  // namespace

/* BUS-10 (D-T9): unsubscribe returns only after a delivery already reaching
 * the handler has finished, and the handler never runs again, so the caller
 * may free its context. */
void test_real_bus_unsubscribe_waits_for_delivery(void) {
    if (g_entered == nullptr) g_entered = xSemaphoreCreateBinary();
    reset_recorder();
    g_slow_done = false;
    TEST_ASSERT_EQUAL(PORTUNUS_OK, event_bus_subscribe(EVENT_HEARTBEAT, slow_handler, nullptr));
    TEST_ASSERT_EQUAL(PORTUNUS_OK, publish_heartbeat(1));
    TEST_ASSERT_EQUAL(pdTRUE, xSemaphoreTake(g_entered, WAIT));        /* handler running */

    TEST_ASSERT_EQUAL(PORTUNUS_OK, event_bus_unsubscribe(EVENT_HEARTBEAT, slow_handler, nullptr));
    TEST_ASSERT_TRUE(g_slow_done);                                      /* it waited */

    TEST_ASSERT_EQUAL(PORTUNUS_OK, publish_heartbeat(2));
    vTaskDelay(pdMS_TO_TICKS(200));
    TEST_ASSERT_EQUAL_size_t(1, g_seen.size());                         /* only event 1 */
}

/* BUS-11 (D-T9): a handler may unsubscribe itself from the dispatcher
 * without deadlocking; it then misses later events. */
void test_real_bus_unsubscribe_from_handler(void) {
    reset_recorder();
    TEST_ASSERT_EQUAL(PORTUNUS_OK, event_bus_subscribe(EVENT_HEARTBEAT, self_unsubscriber, nullptr));
    TEST_ASSERT_EQUAL(PORTUNUS_OK, publish_heartbeat(1));
    TEST_ASSERT_TRUE(wait_deliveries(1));
    TEST_ASSERT_EQUAL(PORTUNUS_OK, publish_heartbeat(2));
    TEST_ASSERT_EQUAL(PORTUNUS_OK, publish_heartbeat(3));
    vTaskDelay(pdMS_TO_TICKS(200));
    TEST_ASSERT_EQUAL_size_t(1, g_seen.size());
}

/* BUS-12 (D-T9): unsubscribe's error contract on the real bus. */
void test_real_bus_unsubscribe_errors(void) {
    TEST_ASSERT_EQUAL(PORTUNUS_ERR_NOT_SUBSCRIBED,
                      event_bus_unsubscribe(EVENT_HEARTBEAT, recorder, nullptr));
    TEST_ASSERT_EQUAL(PORTUNUS_ERR_INVALID_ARG, event_bus_unsubscribe(EVENT_HEARTBEAT, nullptr, nullptr));
    event_bus_reset_for_test();
    TEST_ASSERT_EQUAL(PORTUNUS_ERR_NOT_INIT, event_bus_unsubscribe(EVENT_HEARTBEAT, recorder, nullptr));
    TEST_ASSERT_EQUAL(PORTUNUS_OK, event_bus_init());
}

#endif /* PORTUNUS_TEST_REAL_BUS */

/* ── Entry point ──────────────────────────────────────────────────────────── */

extern "C" void app_main(void) {
    UNITY_BEGIN();

    /* Behavioral suite */
    RUN_TEST(test_grant_unlocks_and_starts_hold_timer);
    RUN_TEST(test_grant_unlock_failure_does_not_start_timer);
    RUN_TEST(test_grant_without_access_point_shows_error);
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
    RUN_TEST(test_early_relock_failure_retries_every_poll);
    RUN_TEST(test_new_grant_cancels_relock_retry);
    RUN_TEST(test_close_while_locked_does_not_lock);
    RUN_TEST(test_second_grant_extends_hold);
    RUN_TEST(test_bridge_drops_ninth_event);
    RUN_TEST(test_bridge_before_init_is_noop);
    RUN_TEST(test_dropped_event_shows_comm_error);
    RUN_TEST(test_failed_card_publish_shows_comm_error);
    RUN_TEST(test_queue_pressure_warns_when_kept_full);
    RUN_TEST(test_unlock_timeout_only_after_lock_succeeds);
    /* Lifecycle (FSM-22, D-T9) */
    RUN_TEST(test_start_subscribes_and_reports_ready);
    RUN_TEST(test_start_reports_dead_reader_and_open_door);
    RUN_TEST(test_stop_locks_and_allows_restart);
    RUN_TEST(test_restart_while_degraded_probes_reader);
    RUN_TEST(test_stop_during_hold_locks_strike);
    RUN_TEST(test_stop_reports_lock_failure);
    RUN_TEST(test_stop_waits_for_poll_task);
#endif

#ifndef PORTUNUS_TEST_REAL_BUS
    /* Fake bus conformance (T3) */
    RUN_TEST(test_fake_bus_subscribe_from_handler);
    RUN_TEST(test_fake_bus_limits_and_arguments);
    RUN_TEST(test_fake_bus_fail_next);
    RUN_TEST(test_fake_bus_unsubscribe);
#endif

#ifdef PORTUNUS_TEST_REAL_BUS
    /* Concurrency suite — only meaningful with real async bus */
    RUN_TEST(test_real_bus_publish_reaches_subscriber);
    RUN_TEST(test_real_bus_second_init_refused);
    RUN_TEST(test_real_bus_subscribe_before_init);
    RUN_TEST(test_real_bus_delivers_in_publish_order);
    RUN_TEST(test_real_bus_queue_full_after_timeout);
    RUN_TEST(test_real_bus_subscribe_from_handler);
    RUN_TEST(test_real_bus_unsubscribe_waits_for_delivery);
    RUN_TEST(test_real_bus_unsubscribe_from_handler);
    RUN_TEST(test_real_bus_unsubscribe_errors);
#endif

    int failures = UNITY_END();
    exit(failures);
}
