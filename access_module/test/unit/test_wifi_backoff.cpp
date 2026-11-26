/* Unit tier: WiFi reconnect backoff policy.
 * No ESP-IDF, no FreeRTOS, no sdkconfig. Bare host compiler. */
#include "unity.h"
#include "wifi_backoff.hpp"

void setUp(void) {}
void tearDown(void) {}

static const uint32_t CEILING = 60000;  /* RECONNECT_CEILING_MS in wifi_mgr.cpp */

/* WIFI-01a: doubles per failed attempt from the Kconfig default up to 60 s. */
void test_doubles_to_ceiling(void) {
    const uint32_t expect[] = {2000, 4000, 8000, 16000, 32000, 60000, 60000};
    uint32_t cur = 1000;  /* CONFIG_PORTUNUS_WIFI_RECONNECT_INTERVAL_MS default */
    for (uint32_t e : expect) {
        cur = wifi_backoff_after_wait(cur, false, CEILING);
        TEST_ASSERT_EQUAL_UINT32(e, cur);
    }
}

/* WIFI-01b: not advanced when the link came back during the wait, so the IP
 * handler's reset survives. */
void test_not_advanced_when_already_connected(void) {
    TEST_ASSERT_EQUAL_UINT32(1000, wifi_backoff_after_wait(1000, true, CEILING));
    TEST_ASSERT_EQUAL_UINT32(8000, wifi_backoff_after_wait(8000, true, CEILING));
}

/* WIFI-01c: the Kconfig range top (10 s) still caps at the ceiling, and a
 * value near UINT32_MAX does not wrap. */
void test_no_overflow(void) {
    TEST_ASSERT_EQUAL_UINT32(20000, wifi_backoff_after_wait(10000, false, CEILING));
    TEST_ASSERT_EQUAL_UINT32(CEILING, wifi_backoff_after_wait(0xFFFFFFF0u, false, CEILING));
    TEST_ASSERT_EQUAL_UINT32(0xFFFFFFFFu,
                             wifi_backoff_after_wait(0x90000000u, false, 0xFFFFFFFFu));
}

int main(void) {
    UNITY_BEGIN();
    RUN_TEST(test_doubles_to_ceiling);
    RUN_TEST(test_not_advanced_when_already_connected);
    RUN_TEST(test_no_overflow);
    return UNITY_END();
}
