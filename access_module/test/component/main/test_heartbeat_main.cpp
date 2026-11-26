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
#include <time.h>

/* ESP-IDF's esp_timer is header-only on the linux target, so supply
 * esp_timer_get_time(): microseconds since first call, from the host's
 * monotonic clock (the FreeRTOS tick here is 10 ms, too coarse). */
extern "C" int64_t esp_timer_get_time(void) {
    static int64_t base_us = -1;
    struct timespec ts;
    clock_gettime(CLOCK_MONOTONIC, &ts);
    const int64_t now_us = (int64_t)ts.tv_sec * 1000000 + ts.tv_nsec / 1000;
    if (base_us < 0) base_us = now_us;
    return now_us - base_us;
}

void setUp(void) {
    event_bus_fake_reset();
    event_bus_init();
}

void tearDown(void) {
    heartbeat_service_stop();
}

/* The variant builds and the service starts and publishes on this target. */
void test_heartbeat_service_runs(void) {
    TEST_ASSERT_EQUAL(PORTUNUS_OK, heartbeat_service_start());
    vTaskDelay(pdMS_TO_TICKS(HEARTBEAT_INTERVAL_MS / 2));
    TEST_ASSERT_GREATER_OR_EQUAL_size_t(1, event_bus_fake_count_of(EVENT_HEARTBEAT));
}

extern "C" void app_main(void) {
    UNITY_BEGIN();
    RUN_TEST(test_heartbeat_service_runs);
    int failures = UNITY_END();
    exit(failures);
}
