/* Component tier, provisioning console (PEU) variant: ESP-IDF linux target.
 *
 * Built from the same app with sdkconfig.defaults.peu layered on, which
 * selects CONFIG_PORTUNUS_MODULE_TYPE_PROVISIONING_CONSOLE; main/CMakeLists.txt
 * then compiles this file instead of test_main.cpp. Always uses the fake
 * event bus.
 *
 * The FSM is driven via ProvisioningFSMTestFixture (friend class declared in
 * provisioning_fsm.hpp) without starting FreeRTOS tasks: init() sets the
 * capability flags and creates the queue, the fixture enters the state
 * start() would, and tests inject events and tick timeouts under FakeClock.
 */

#include "unity.h"
#include "provisioning_fsm.hpp"
#include "event_bus.hpp"
#include "error_codes.hpp"
#include "event_types.hpp"
#include "sdkconfig.h"

#include "../support/fake_arm.hpp"
#include "../support/fake_clock.hpp"
#include "../support/fake_credential_reader.hpp"
#include "../support/fake_feedback.hpp"
#include "../support/provisioning_fsm_test_fixture.hpp"
#include "../components/event_bus/include/event_bus_fake.hpp"

#include <stdlib.h>
#include <string.h>

static constexpr int64_t IDLE_MS    = CONFIG_PORTUNUS_IDLE_TIMEOUT_MS;
static constexpr int64_t ARM_MS     = CONFIG_PORTUNUS_ARM_TIMEOUT_MS;
static constexpr int64_t RESULT_MS  = CONFIG_PORTUNUS_RESULT_DISPLAY_MS;
static constexpr int64_t PROV_MS    = CONFIG_PORTUNUS_PROVISION_TIMEOUT_MS;
static constexpr int64_t T_START    = 1000;

void setUp(void) {
    event_bus_fake_reset();
    event_bus_init();
}

void tearDown(void) {}

/* ── Rig and event helpers ────────────────────────────────────────────────── */

/* One PEU with fakes. with_arm=false wires no button (always-armed mode). */
struct Rig {
    FakeCredentialReader       reader;
    FakeFeedback               feedback;
    FakeArm                    arm;
    FakeClock                  clock;
    ProvisioningFSM            fsm;
    ProvisioningFSMTestFixture fix;

    explicit Rig(bool with_arm)
        : fsm(&reader, &feedback, with_arm ? &arm : nullptr, &clock), fix(fsm) {
        clock.set(T_START);
    }

    /* init() then the state start() enters. */
    void start() {
        TEST_ASSERT_EQUAL(PORTUNUS_OK, fsm.init());
        fix.enter_initial_state();
    }
};

static portunus_event_t make_arm(void) {
    portunus_event_t e;
    memset(&e, 0, sizeof(e));
    e.id = EVENT_ARM_REQUESTED;
    return e;
}

static const uint8_t UID7[] = {0x04, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66};

static portunus_event_t make_card(const uint8_t *uid = UID7, uint8_t len = sizeof UID7) {
    portunus_event_t e;
    memset(&e, 0, sizeof(e));
    e.id = EVENT_CREDENTIAL_READ;
    const size_t n = len < sizeof e.payload.credential_read.credential.uid
                         ? len : sizeof e.payload.credential_read.credential.uid;
    memcpy(e.payload.credential_read.credential.uid, uid, n);
    e.payload.credential_read.credential.uid_len = len;
    return e;
}

static portunus_event_t make_result(int reason) {
    portunus_event_t e;
    memset(&e, 0, sizeof(e));
    e.id = (reason == PROVISION_RESULT_PENDING_CREATED) ? EVENT_PROVISION_SUCCESS
                                                         : EVENT_PROVISION_FAILED;
    e.payload.provision_result.reason = static_cast<provision_result_reason_t>(reason);
    return e;
}

/* Armed rig with a card read: now in CAPTURE_SEND. */
static void to_capture_send(Rig &r) {
    if (r.fsm.state() != PEU_STATE_ARMED) r.fix.inject(make_arm());
    TEST_ASSERT_EQUAL(PEU_STATE_ARMED, r.fsm.state());
    r.fix.inject(make_card());
    TEST_ASSERT_EQUAL(PEU_STATE_CAPTURE_SEND, r.fsm.state());
}

/* ── Tests ────────────────────────────────────────────────────────────────── */

/* PEU-01: the reader is mandatory; a missing or failing one fails init. */
void test_init_requires_working_reader(void) {
    FakeFeedback feedback;
    FakeClock    clock;

    ProvisioningFSM no_reader(nullptr, &feedback, nullptr, &clock);
    TEST_ASSERT_NOT_EQUAL(PORTUNUS_OK, no_reader.init());

    FakeCredentialReader bad;
    bad.init_result = PORTUNUS_ERR_DEVICE_NOT_FOUND;
    ProvisioningFSM failing(&bad, &feedback, nullptr, &clock);
    TEST_ASSERT_EQUAL(PORTUNUS_ERR_DEVICE_NOT_FOUND, failing.init());

    FakeCredentialReader good;
    ProvisioningFSM ok(&good, &feedback, nullptr, &clock);
    TEST_ASSERT_EQUAL(PORTUNUS_OK, ok.init());
}

/* PEU-02: with an arm button the PEU starts IDLE, without one (or with one
 * that fails init) it starts ARMED, each with its deadline and feedback. */
void test_initial_state_depends_on_arm(void) {
    Rig with(true);
    with.start();
    TEST_ASSERT_TRUE(with.fix.has_arm());
    TEST_ASSERT_EQUAL(PEU_STATE_IDLE, with.fsm.state());
    TEST_ASSERT_EQUAL(feedback_type_t::PEU_IDLE, with.feedback.last());
    TEST_ASSERT_EQUAL_INT64(T_START + IDLE_MS, with.fix.deadline());

    Rig without(false);
    without.start();
    TEST_ASSERT_EQUAL(PEU_STATE_ARMED, without.fsm.state());
    TEST_ASSERT_EQUAL(feedback_type_t::PEU_ARMED_CAPTURE, without.feedback.last());
    TEST_ASSERT_EQUAL_INT64(T_START + ARM_MS, without.fix.deadline());

    Rig broken(true);
    broken.arm.init_result = PORTUNUS_ERR_GPIO_INIT;
    broken.start();
    TEST_ASSERT_FALSE(broken.fix.has_arm());
    TEST_ASSERT_EQUAL(PEU_STATE_ARMED, broken.fsm.state());
}

/* PEU-03: the arm button wakes from SLEEP, arms from IDLE, cancels from ARMED. */
void test_arm_button_cycle(void) {
    Rig r(true);
    r.start();
    r.clock.advance(IDLE_MS);
    r.fix.tick_timeout();
    TEST_ASSERT_EQUAL(PEU_STATE_SLEEP, r.fsm.state());
    TEST_ASSERT_EQUAL(feedback_type_t::NONE, r.feedback.last());

    r.fix.inject(make_arm());
    TEST_ASSERT_EQUAL(PEU_STATE_IDLE, r.fsm.state());
    r.fix.inject(make_arm());
    TEST_ASSERT_EQUAL(PEU_STATE_ARMED, r.fsm.state());
    TEST_ASSERT_EQUAL(feedback_type_t::PEU_ARMED_CAPTURE, r.feedback.last());
    r.fix.inject(make_arm());
    TEST_ASSERT_EQUAL(PEU_STATE_IDLE, r.fsm.state());
    TEST_ASSERT_EQUAL(feedback_type_t::PEU_IDLE, r.feedback.last());
}

/* PEU-04: the arm button is ignored while a capture is in flight and while
 * the result is shown. */
void test_arm_ignored_in_capture_send_and_result(void) {
    Rig r(true);
    r.start();
    to_capture_send(r);
    r.feedback.reset();
    r.fix.inject(make_arm());
    TEST_ASSERT_EQUAL(PEU_STATE_CAPTURE_SEND, r.fsm.state());
    TEST_ASSERT_TRUE(r.feedback.indications.empty());

    r.fix.inject(make_result(PROVISION_RESULT_PENDING_CREATED));
    TEST_ASSERT_EQUAL(PEU_STATE_RESULT, r.fsm.state());
    r.feedback.reset();
    r.fix.inject(make_arm());
    TEST_ASSERT_EQUAL(PEU_STATE_RESULT, r.fsm.state());
    TEST_ASSERT_TRUE(r.feedback.indications.empty());
}

/* PEU-05: a card while ARMED publishes exactly one PROVISION_REQUEST carrying
 * the raw UID bytes and length, enters CAPTURE_SEND and shows CARD_READ. */
void test_card_in_armed_publishes_one_request(void) {
    Rig r(false);
    r.start();
    r.fix.inject(make_card());

    TEST_ASSERT_EQUAL(PEU_STATE_CAPTURE_SEND, r.fsm.state());
    TEST_ASSERT_EQUAL(feedback_type_t::CARD_READ, r.feedback.last());
    TEST_ASSERT_EQUAL_size_t(1, event_bus_fake_count_of(EVENT_PROVISION_REQUEST));
    const portunus_event_t *req = event_bus_fake_at(0);
    TEST_ASSERT_NOT_NULL(req);
    TEST_ASSERT_EQUAL(EVENT_PROVISION_REQUEST, req->id);
    TEST_ASSERT_EQUAL_UINT8(sizeof UID7, req->payload.provision_request.credential_uid_len);
    TEST_ASSERT_EQUAL_HEX8_ARRAY(UID7, req->payload.provision_request.credential_uid, sizeof UID7);
}

/* PEU-06: a card outside ARMED publishes nothing and changes nothing. */
void test_card_outside_armed_publishes_nothing(void) {
    Rig r(true);
    r.start();                                    /* IDLE */
    r.fix.inject(make_card());
    TEST_ASSERT_EQUAL(PEU_STATE_IDLE, r.fsm.state());

    r.clock.advance(IDLE_MS);
    r.fix.tick_timeout();                         /* SLEEP */
    r.fix.inject(make_card());
    TEST_ASSERT_EQUAL(PEU_STATE_SLEEP, r.fsm.state());
    TEST_ASSERT_EQUAL_size_t(0, event_bus_fake_count_of(EVENT_PROVISION_REQUEST));

    r.fix.inject(make_arm());
    to_capture_send(r);                           /* one request */
    r.fix.inject(make_card());                    /* second card while sending */
    TEST_ASSERT_EQUAL(PEU_STATE_CAPTURE_SEND, r.fsm.state());
    r.fix.inject(make_result(PROVISION_RESULT_PENDING_CREATED));
    r.fix.inject(make_card());                    /* card while showing the result */
    TEST_ASSERT_EQUAL(PEU_STATE_RESULT, r.fsm.state());
    TEST_ASSERT_EQUAL_size_t(1, event_bus_fake_count_of(EVENT_PROVISION_REQUEST));
}

/* PEU-07: every result reason, the two reserved values and an unknown one,
 * map to the documented feedback. Anything unrecognised is an error. */
void test_result_reason_mapping(void) {
    const struct { int reason; feedback_type_t fb; } cases[] = {
        {PROVISION_RESULT_PENDING_CREATED,    feedback_type_t::PEU_RESULT_PENDING},
        {PROVISION_RESULT_DUPLICATE_ACTIVE,   feedback_type_t::PEU_RESULT_DUPLICATE},
        {PROVISION_RESULT_DUPLICATE_INACTIVE, feedback_type_t::PEU_RESULT_DUPLICATE},
        {PROVISION_RESULT_DUPLICATE_PENDING,  feedback_type_t::PEU_RESULT_DUPLICATE},
        {PROVISION_RESULT_UNAUTHORIZED,       feedback_type_t::PEU_RESULT_UNAUTHORIZED},
        {PROVISION_RESULT_COMM_ERROR,         feedback_type_t::PEU_RESULT_ERROR},
        {0,                                   feedback_type_t::PEU_RESULT_ERROR},  /* reserved */
        {5,                                   feedback_type_t::PEU_RESULT_ERROR},  /* reserved */
        {99,                                  feedback_type_t::PEU_RESULT_ERROR},  /* unknown */
    };
    for (const auto &c : cases) {
        Rig r(false);
        r.start();
        to_capture_send(r);
        r.fix.inject(make_result(c.reason));
        TEST_ASSERT_EQUAL(PEU_STATE_RESULT, r.fsm.state());
        TEST_ASSERT_EQUAL(c.fb, r.feedback.last());
        TEST_ASSERT_EQUAL_INT64(r.clock.now_ms() + RESULT_MS, r.fix.deadline());
    }
}

/* PEU-08: a result that arrives outside CAPTURE_SEND (late, or unsolicited)
 * changes neither state nor feedback. */
void test_result_outside_capture_send_ignored(void) {
    Rig r(true);
    r.start();                                     /* IDLE */
    r.feedback.reset();
    r.fix.inject(make_result(PROVISION_RESULT_PENDING_CREATED));
    TEST_ASSERT_EQUAL(PEU_STATE_IDLE, r.fsm.state());

    r.fix.inject(make_arm());                      /* ARMED */
    r.feedback.reset();
    r.fix.inject(make_result(PROVISION_RESULT_UNAUTHORIZED));
    TEST_ASSERT_EQUAL(PEU_STATE_ARMED, r.fsm.state());
    TEST_ASSERT_TRUE(r.feedback.indications.empty());
}

/* PEU-09: IDLE sleeps at exactly IDLE_TIMEOUT_MS, not one millisecond early. */
void test_idle_to_sleep_at_exact_timeout(void) {
    Rig r(true);
    r.start();
    r.clock.advance(IDLE_MS - 1);
    r.fix.tick_timeout();
    TEST_ASSERT_EQUAL(PEU_STATE_IDLE, r.fsm.state());
    r.clock.advance(1);
    r.fix.tick_timeout();
    TEST_ASSERT_EQUAL(PEU_STATE_SLEEP, r.fsm.state());
    TEST_ASSERT_EQUAL_INT64(0, r.fix.deadline());
}

/* PEU-10: an ARMED timeout returns to IDLE with a button, and re-arms with a
 * fresh deadline without one. */
void test_armed_timeout(void) {
    Rig with(true);
    with.start();
    with.fix.inject(make_arm());
    with.clock.advance(ARM_MS - 1);
    with.fix.tick_timeout();
    TEST_ASSERT_EQUAL(PEU_STATE_ARMED, with.fsm.state());
    with.clock.advance(1);
    with.fix.tick_timeout();
    TEST_ASSERT_EQUAL(PEU_STATE_IDLE, with.fsm.state());

    Rig without(false);
    without.start();
    without.clock.advance(ARM_MS);
    without.fix.tick_timeout();
    TEST_ASSERT_EQUAL(PEU_STATE_ARMED, without.fsm.state());
    TEST_ASSERT_EQUAL_INT64(without.clock.now_ms() + ARM_MS, without.fix.deadline());
}

/* PEU-11: the result shows for RESULT_DISPLAY_MS, then returns to IDLE with
 * a button or ARMED without one. */
void test_result_returns_after_display_time(void) {
    for (const bool with_arm : {true, false}) {
        Rig r(with_arm);
        r.start();
        to_capture_send(r);
        r.fix.inject(make_result(PROVISION_RESULT_PENDING_CREATED));
        r.clock.advance(RESULT_MS - 1);
        r.fix.tick_timeout();
        TEST_ASSERT_EQUAL(PEU_STATE_RESULT, r.fsm.state());
        r.clock.advance(1);
        r.fix.tick_timeout();
        TEST_ASSERT_EQUAL(with_arm ? PEU_STATE_IDLE : PEU_STATE_ARMED, r.fsm.state());
    }
}

/* PEU-12 (F3): a capture the server never answers (request dropped while
 * WiFi is down, queue full, lost reply) times out after PROVISION_TIMEOUT_MS
 * and shows an error, instead of wedging in CAPTURE_SEND until reboot. */
void test_capture_send_times_out(void) {
    TEST_IGNORE_MESSAGE("F3: CAPTURE_SEND has no timeout; un-ignored by the fix");
    Rig r(true);
    r.start();
    to_capture_send(r);
    TEST_ASSERT_EQUAL_INT64(r.clock.now_ms() + PROV_MS, r.fix.deadline());

    r.clock.advance(PROV_MS - 1);
    r.fix.tick_timeout();
    TEST_ASSERT_EQUAL(PEU_STATE_CAPTURE_SEND, r.fsm.state());
    r.clock.advance(1);
    r.fix.tick_timeout();
    TEST_ASSERT_EQUAL(PEU_STATE_RESULT, r.fsm.state());
    TEST_ASSERT_EQUAL(feedback_type_t::PEU_RESULT_ERROR, r.feedback.last());

    /* Then the normal result path: back to IDLE, and a late reply is ignored. */
    r.clock.advance(RESULT_MS);
    r.fix.tick_timeout();
    TEST_ASSERT_EQUAL(PEU_STATE_IDLE, r.fsm.state());
    r.fix.inject(make_result(PROVISION_RESULT_PENDING_CREATED));
    TEST_ASSERT_EQUAL(PEU_STATE_IDLE, r.fsm.state());
}

/* PEU-13: an out-of-range uid_len is clamped to the request's UID buffer. */
void test_oversize_uid_len_clamped(void) {
    Rig r(false);
    r.start();
    const uint8_t uid10[] = {1, 2, 3, 4, 5, 6, 7, 8, 9, 10};
    r.fix.inject(make_card(uid10, 200));
    const portunus_event_t *req = event_bus_fake_at(0);
    TEST_ASSERT_NOT_NULL(req);
    TEST_ASSERT_EQUAL_UINT8(sizeof req->payload.provision_request.credential_uid,
                            req->payload.provision_request.credential_uid_len);
    TEST_ASSERT_EQUAL_HEX8_ARRAY(uid10, req->payload.provision_request.credential_uid,
                                 sizeof uid10);
}

extern "C" void app_main(void) {
    UNITY_BEGIN();
    RUN_TEST(test_init_requires_working_reader);
    RUN_TEST(test_initial_state_depends_on_arm);
    RUN_TEST(test_arm_button_cycle);
    RUN_TEST(test_arm_ignored_in_capture_send_and_result);
    RUN_TEST(test_card_in_armed_publishes_one_request);
    RUN_TEST(test_card_outside_armed_publishes_nothing);
    RUN_TEST(test_result_reason_mapping);
    RUN_TEST(test_result_outside_capture_send_ignored);
    RUN_TEST(test_idle_to_sleep_at_exact_timeout);
    RUN_TEST(test_armed_timeout);
    RUN_TEST(test_result_returns_after_display_time);
    RUN_TEST(test_capture_send_times_out);
    RUN_TEST(test_oversize_uid_len_clamped);
    int failures = UNITY_END();
    exit(failures);
}
