/* Unit tier: door-strike level mapping and readback check.
 * No ESP-IDF, no FreeRTOS, no sdkconfig. Bare host compiler. */
#include "unity.h"
#include "door_strike_levels.hpp"

void setUp(void) {}
void tearDown(void) {}

/* DRV-04a: level for each wiring polarity and state. */
void test_levels_for_both_polarities(void) {
    TEST_ASSERT_EQUAL_INT(1, strike_level(false, true));   /* active-high, energized */
    TEST_ASSERT_EQUAL_INT(0, strike_level(false, false));  /* active-high, locked    */
    TEST_ASSERT_EQUAL_INT(0, strike_level(true, true));    /* active-low,  energized */
    TEST_ASSERT_EQUAL_INT(1, strike_level(true, false));   /* active-low,  locked    */
}

/* DRV-04b (F1): a pad reading the inactive level confirms the lock on both
 * polarities. Under the old OUTPUT-only pin mode the pad always read 0, which
 * this check reports as a failed lock on active-low wiring: that is the F1
 * symptom, and why the pin is now INPUT_OUTPUT. */
void test_readback_confirms_lock_on_both_polarities(void) {
    TEST_ASSERT_TRUE(strike_readback_matches(false, false, 0));
    TEST_ASSERT_TRUE(strike_readback_matches(true, false, 1));
    TEST_ASSERT_FALSE(strike_readback_matches(true, false, 0));   /* old always-0 read */
    TEST_ASSERT_FALSE(strike_readback_matches(false, false, 1));  /* still energized   */
}

/* Locked and energized are always distinct levels (fail-secure means locked
 * is never accidentally the same level as unlocked). */
static_assert(strike_level(false, true) != strike_level(false, false));
static_assert(strike_level(true, true) != strike_level(true, false));

int main(void) {
    UNITY_BEGIN();
    RUN_TEST(test_levels_for_both_polarities);
    RUN_TEST(test_readback_confirms_lock_on_both_polarities);
    return UNITY_END();
}
