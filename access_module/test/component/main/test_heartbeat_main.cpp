/* Component tier, heartbeat variant: ESP-IDF linux target.
 *
 * Built with sdkconfig.defaults.hb layered on (CONFIG_PORTUNUS_TEST_HEARTBEAT,
 * short HEARTBEAT_INTERVAL_MS). Runs the real heartbeat_service task on the
 * POSIX FreeRTOS scheduler against the fake event bus, so timing assertions
 * wait real time with tolerances of a fraction of an interval.
 */

#include "unity.h"
#include "heartbeat_service.hpp"
#include "event_bus.hpp"
#include "error_codes.hpp"
#include "event_types.hpp"
#include "timing_config.hpp"

#include "../components/event_bus/include/event_bus_fake.hpp"

#include "freertos/FreeRTOS.h"
#include "freertos/task.h"

#include <stdlib.h>
#include <vector>

void setUp(void) {
    event_bus_fake_reset();
    event_bus_init();
}

void tearDown(void) {
    heartbeat_service_stop();
}

static constexpr uint32_t INTERVAL_MS = HEARTBEAT_INTERVAL_MS;

/* Kconfig silently replaces an out-of-range value with the 10 s default, and
 * every test here waits a few intervals: fail the build rather than run for
 * minutes. */
static_assert(INTERVAL_MS <= 1000,
              "heartbeat variant needs a short interval: check sdkconfig.defaults.hb "
              "against the HEARTBEAT_INTERVAL_MS range in main/Kconfig.projbuild");

static void wait_ms(uint32_t ms) { vTaskDelay(pdMS_TO_TICKS(ms)); }

/* Heartbeats recorded so far, copied out under the fake bus's lock. */
static std::vector<event_heartbeat_t> heartbeats(void) {
    std::vector<event_heartbeat_t> out;
    portunus_event_t e;
    for (size_t i = 0; event_bus_fake_copy(i, &e); ++i) {
        if (e.id == EVENT_HEARTBEAT) out.push_back(e.payload.heartbeat);
    }
    return out;
}

static void assert_consecutive_from(uint32_t first, const std::vector<event_heartbeat_t> &hb) {
    for (size_t i = 0; i < hb.size(); ++i) {
        TEST_ASSERT_EQUAL_UINT32(first + i, hb[i].sequence);
    }
}

/* HB-01: the first heartbeat is sequence 0 and goes out at start, not one
 * interval later (#80: it registers the module and syncs its clock). */
void test_first_heartbeat_at_start(void) {
    TEST_ASSERT_EQUAL(PORTUNUS_OK, heartbeat_service_start());
    wait_ms(INTERVAL_MS / 2);
    const auto hb = heartbeats();
    TEST_ASSERT_EQUAL_size_t(1, hb.size());
    TEST_ASSERT_EQUAL_UINT32(0, hb[0].sequence);
}

/* HB-02: one heartbeat per interval, sequence +1 each, no gaps. */
void test_sequence_increments_per_publish(void) {
    TEST_ASSERT_EQUAL(PORTUNUS_OK, heartbeat_service_start());
    wait_ms(INTERVAL_MS * 7 / 2);              /* t = 0, 1, 2, 3 intervals */
    const auto hb = heartbeats();
    TEST_ASSERT_GREATER_OR_EQUAL_size_t(3, hb.size());
    TEST_ASSERT_LESS_OR_EQUAL_size_t(5, hb.size());
    assert_consecutive_from(0, hb);
}

/* HB-03: a failed publish is retried with the same sequence on the next
 * interval, so the server sees no gap. Covers a failure on the very first
 * heartbeat and one mid-run. */
void test_failed_publish_retries_same_sequence(void) {
    event_bus_fake_fail_next(1, PORTUNUS_ERR_QUEUE_FULL);   /* first one fails */
    TEST_ASSERT_EQUAL(PORTUNUS_OK, heartbeat_service_start());
    wait_ms(INTERVAL_MS * 3 / 2);
    auto hb = heartbeats();
    TEST_ASSERT_EQUAL_size_t(1, hb.size());
    TEST_ASSERT_EQUAL_UINT32(0, hb[0].sequence);

    event_bus_fake_fail_next(1, PORTUNUS_ERR_QUEUE_FULL);   /* the next one fails */
    wait_ms(INTERVAL_MS * 2);
    hb = heartbeats();
    TEST_ASSERT_EQUAL_size_t(2, hb.size());
    assert_consecutive_from(0, hb);
}

/* HB-04: a second start is refused, stop is idempotent, and a restart
 * begins again at sequence 0. */
void test_start_stop_lifecycle(void) {
    TEST_ASSERT_EQUAL(PORTUNUS_OK, heartbeat_service_start());
    TEST_ASSERT_EQUAL(PORTUNUS_ERR_ALREADY_INIT, heartbeat_service_start());
    wait_ms(INTERVAL_MS * 3 / 2);
    TEST_ASSERT_GREATER_OR_EQUAL_size_t(2, heartbeats().size());

    heartbeat_service_stop();
    heartbeat_service_stop();                  /* second stop is a no-op */
    event_bus_fake_reset();
    event_bus_init();
    wait_ms(INTERVAL_MS * 2);
    TEST_ASSERT_EQUAL_size_t(0, heartbeats().size());   /* really stopped */

    TEST_ASSERT_EQUAL(PORTUNUS_OK, heartbeat_service_start());
    wait_ms(INTERVAL_MS / 2);
    const auto hb = heartbeats();
    TEST_ASSERT_EQUAL_size_t(1, hb.size());
    TEST_ASSERT_EQUAL_UINT32(0, hb[0].sequence);
}

/* HB-05: uptime_sec never goes backwards and advances with real time. */
void test_uptime_non_decreasing(void) {
    TEST_ASSERT_EQUAL(PORTUNUS_OK, heartbeat_service_start());
    wait_ms(1000 + INTERVAL_MS * 5 / 2);       /* crosses at least one second */
    const auto hb = heartbeats();
    TEST_ASSERT_GREATER_OR_EQUAL_size_t(2, hb.size());
    for (size_t i = 1; i < hb.size(); ++i) {
        TEST_ASSERT_GREATER_OR_EQUAL_UINT32(hb[i - 1].uptime_sec, hb[i].uptime_sec);
    }
    TEST_ASSERT_GREATER_THAN_UINT32(hb.front().uptime_sec, hb.back().uptime_sec);
}

extern "C" void app_main(void) {
    UNITY_BEGIN();
    RUN_TEST(test_first_heartbeat_at_start);
    RUN_TEST(test_sequence_increments_per_publish);
    RUN_TEST(test_failed_publish_retries_same_sequence);
    RUN_TEST(test_start_stop_lifecycle);
    RUN_TEST(test_uptime_non_decreasing);
    int failures = UNITY_END();
    exit(failures);
}
