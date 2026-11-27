/* Unit tier: queue_pressure, the "queue stays near full" detector (D-T5).
 * No ESP-IDF, no FreeRTOS. Bare host compiler. */
#include "unity.h"
#include "queue_pressure.hpp"

void setUp(void) {}
void tearDown(void) {}

using qpe = queue_pressure_event_t;

static qpe feed(queue_pressure_t &qp, size_t depth, size_t cap, int times) {
    qpe last = qpe::NONE;
    for (int i = 0; i < times; ++i) last = queue_pressure_sample(qp, depth, cap);
    return last;
}

/* QP-01: 75% is high (6 of 8), just under is not (5 of 8). */
void test_threshold(void) {
    queue_pressure_t a, b;
    TEST_ASSERT_EQUAL(qpe::HIGH, feed(a, 6, 8, QUEUE_PRESSURE_STREAK));
    TEST_ASSERT_EQUAL(qpe::NONE, feed(b, 5, 8, 100));
    TEST_ASSERT_FALSE(b.warned);
}

/* QP-02: HIGH needs QUEUE_PRESSURE_STREAK samples in a row; one dip below
 * 75% restarts the streak. */
void test_streak_must_be_consecutive(void) {
    queue_pressure_t qp;
    TEST_ASSERT_EQUAL(qpe::NONE, feed(qp, 8, 8, QUEUE_PRESSURE_STREAK - 1));
    TEST_ASSERT_EQUAL(qpe::NONE, queue_pressure_sample(qp, 5, 8));
    TEST_ASSERT_EQUAL(qpe::NONE, feed(qp, 8, 8, QUEUE_PRESSURE_STREAK - 1));
    TEST_ASSERT_EQUAL(qpe::HIGH, queue_pressure_sample(qp, 8, 8));
}

/* QP-03: one warning per episode, however long it lasts. */
void test_warns_once_per_episode(void) {
    queue_pressure_t qp;
    int highs = 0;
    for (int i = 0; i < 1000; ++i) highs += queue_pressure_sample(qp, 16, 16) == qpe::HIGH;
    TEST_ASSERT_EQUAL_INT(1, highs);
}

/* QP-04: RECOVERED only once below 50%; between 50% and 75% neither
 * recovers nor re-warns, and after recovery a new episode warns again. */
void test_recovery_and_rearm(void) {
    queue_pressure_t qp;
    TEST_ASSERT_EQUAL(qpe::HIGH, feed(qp, 7, 8, QUEUE_PRESSURE_STREAK));
    TEST_ASSERT_EQUAL(qpe::NONE, queue_pressure_sample(qp, 4, 8));      /* exactly 50% */
    TEST_ASSERT_TRUE(qp.warned);
    TEST_ASSERT_EQUAL(qpe::RECOVERED, queue_pressure_sample(qp, 3, 8));
    TEST_ASSERT_EQUAL(qpe::NONE, queue_pressure_sample(qp, 0, 8));
    TEST_ASSERT_EQUAL(qpe::HIGH, feed(qp, 8, 8, QUEUE_PRESSURE_STREAK));
}

/* QP-05: no RECOVERED without a prior warning, and a zero capacity is inert. */
void test_quiet_cases(void) {
    queue_pressure_t qp;
    TEST_ASSERT_EQUAL(qpe::NONE, feed(qp, 0, 8, 10));
    TEST_ASSERT_EQUAL(qpe::NONE, feed(qp, 3, 0, 10));
}

int main(void) {
    UNITY_BEGIN();
    RUN_TEST(test_threshold);
    RUN_TEST(test_streak_must_be_consecutive);
    RUN_TEST(test_warns_once_per_episode);
    RUN_TEST(test_recovery_and_rearm);
    RUN_TEST(test_quiet_cases);
    return UNITY_END();
}
