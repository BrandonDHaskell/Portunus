/* Unit tier: LED pattern engine.
 * No ESP-IDF, no FreeRTOS, no sdkconfig. Bare host compiler.
 * Traces use '#' for on and '.' for off, one character per 50 ms tick. */
#include "unity.h"
#include "led_pattern.hpp"

#include <string>

void setUp(void) {}
void tearDown(void) {}

using ft = feedback_type_t;

static std::string run(led_engine_t &e, size_t ticks) {
    std::string out;
    for (size_t i = 0; i < ticks; ++i) out += led_engine_step(e) ? '#' : '.';
    return out;
}

static std::string trace(ft type, size_t ticks) {
    led_engine_t e;
    led_engine_indicate(e, type);
    return run(e, ticks);
}

static std::string rep(const std::string &s, size_t n) {
    std::string out;
    for (size_t i = 0; i < n; ++i) out += s;
    return out;
}

static const ft ALL[] = {
    ft::NONE, ft::ACCESS_GRANTED, ft::ACCESS_DENIED, ft::SYSTEM_READY, ft::SYSTEM_ERROR,
    ft::CARD_READ, ft::PROVISIONING_IDLE, ft::PROVISIONING_AWAITING,
    ft::PROVISIONING_SUCCESS, ft::PROVISIONING_DUPLICATE, ft::PROVISIONING_UNAUTHORIZED,
    ft::PEU_IDLE, ft::PEU_ARMED_CAPTURE, ft::PEU_ARMED_ENROLL, ft::PEU_RESULT_PENDING,
    ft::PEU_RESULT_SUCCESS, ft::PEU_RESULT_DUPLICATE, ft::PEU_RESULT_UNAUTHORIZED,
    ft::PEU_RESULT_ERROR,
};

/* DRV-06: expected trace for every pattern. One-shots are shown through their
 * end plus a few ticks of the (empty) baseline; continuous patterns over two
 * periods. */
void test_every_pattern_trace(void) {
    TEST_ASSERT_EQUAL_STRING("........", trace(ft::NONE, 8).c_str());
    TEST_ASSERT_EQUAL_STRING((std::string(20, '#') + "....").c_str(),
                             trace(ft::ACCESS_GRANTED, 24).c_str());
    TEST_ASSERT_EQUAL_STRING("##..##..##......", trace(ft::ACCESS_DENIED, 16).c_str());
    TEST_ASSERT_EQUAL_STRING(rep("#" + std::string(59, '.'), 2).c_str(),
                             trace(ft::SYSTEM_READY, 120).c_str());
    TEST_ASSERT_EQUAL_STRING(rep("####....", 2).c_str(), trace(ft::SYSTEM_ERROR, 16).c_str());
    TEST_ASSERT_EQUAL_STRING(std::string(100, '#').c_str(), trace(ft::CARD_READ, 100).c_str());
    TEST_ASSERT_EQUAL_STRING(rep("#...#" + std::string(75, '.'), 2).c_str(),
                             trace(ft::PROVISIONING_IDLE, 160).c_str());
    TEST_ASSERT_EQUAL_STRING(rep(std::string(20, '#') + std::string(20, '.'), 2).c_str(),
                             trace(ft::PROVISIONING_AWAITING, 80).c_str());
    TEST_ASSERT_EQUAL_STRING((rep("##..", 5) + "....").c_str(),
                             trace(ft::PROVISIONING_SUCCESS, 24).c_str());
    TEST_ASSERT_EQUAL_STRING((rep("####....", 2) + "....").c_str(),
                             trace(ft::PROVISIONING_DUPLICATE, 20).c_str());
    TEST_ASSERT_EQUAL_STRING((std::string(10, '#') + "...." + rep("##..", 2) + "........").c_str(),
                             trace(ft::PROVISIONING_UNAUTHORIZED, 30).c_str());
    TEST_ASSERT_EQUAL_STRING(rep("#...#" + std::string(115, '.'), 2).c_str(),
                             trace(ft::PEU_IDLE, 240).c_str());
    TEST_ASSERT_EQUAL_STRING(rep("#..#..#.." + std::string(31, '.'), 2).c_str(),
                             trace(ft::PEU_ARMED_CAPTURE, 80).c_str());
    TEST_ASSERT_EQUAL_STRING(rep(std::string(10, '#') + std::string(10, '.'), 2).c_str(),
                             trace(ft::PEU_ARMED_ENROLL, 40).c_str());
    TEST_ASSERT_EQUAL_STRING((rep("####" + std::string(12, '.'), 3) + "....").c_str(),
                             trace(ft::PEU_RESULT_PENDING, 52).c_str());
    TEST_ASSERT_EQUAL_STRING(trace(ft::PROVISIONING_SUCCESS, 24).c_str(),
                             trace(ft::PEU_RESULT_SUCCESS, 24).c_str());
    TEST_ASSERT_EQUAL_STRING(trace(ft::PROVISIONING_DUPLICATE, 20).c_str(),
                             trace(ft::PEU_RESULT_DUPLICATE, 20).c_str());
    TEST_ASSERT_EQUAL_STRING(trace(ft::PROVISIONING_UNAUTHORIZED, 30).c_str(),
                             trace(ft::PEU_RESULT_UNAUTHORIZED, 30).c_str());
    TEST_ASSERT_EQUAL_STRING(trace(ft::SYSTEM_ERROR, 16).c_str(),
                             trace(ft::PEU_RESULT_ERROR, 16).c_str());
}

/* DRV-08: a new indication preempts mid-pattern and restarts at tick 0. */
void test_preemption_resets_tick(void) {
    led_engine_t e;
    led_engine_indicate(e, ft::ACCESS_GRANTED);
    run(e, 5);
    led_engine_indicate(e, ft::ACCESS_DENIED);
    TEST_ASSERT_EQUAL_UINT32(0, e.tick);
    TEST_ASSERT_EQUAL_STRING("##..##..##..", run(e, 12).c_str());
}

/* DRV-09 (F10): continuous patterns keep tick inside their period, so the
 * counter can never overflow however long a pattern runs. */
void test_continuous_tick_stays_bounded(void) {
    for (const ft type : ALL) {
        if (led_pattern_kind(type) != led_pattern_kind_t::CONTINUOUS) continue;
        const uint32_t len = led_pattern_length(type);
        led_engine_t e;
        led_engine_indicate(e, type);
        const std::string first = run(e, len);
        for (int cycle = 0; cycle < 50; ++cycle) {
            TEST_ASSERT_LESS_THAN_UINT32(len, e.tick);
            TEST_ASSERT_EQUAL_STRING(first.c_str(), run(e, len).c_str());
        }
    }
    /* Held and off patterns do not advance tick at all. */
    led_engine_t e;
    led_engine_indicate(e, ft::CARD_READ);
    run(e, 1000);
    TEST_ASSERT_EQUAL_UINT32(0, e.tick);
}

/* DRV-10: an out-of-range value is inert (off) and does not become a baseline. */
void test_out_of_range_value_is_inert(void) {
    const ft bogus = static_cast<ft>(0xEE);
    TEST_ASSERT_EQUAL(led_pattern_kind_t::OFF, led_pattern_kind(bogus));
    TEST_ASSERT_EQUAL_STRING("........", trace(bogus, 8).c_str());
    TEST_ASSERT_FALSE(led_pattern_is_baseline(bogus));
}

/* Every enum value is classified; a new feedback_type_t must be added here
 * and to the pattern table, or this fails. */
void test_every_type_has_a_pattern(void) {
    for (const ft type : ALL) {
        const led_pattern_kind_t k = led_pattern_kind(type);
        if (type == ft::NONE) {
            TEST_ASSERT_EQUAL(led_pattern_kind_t::OFF, k);
        } else {
            TEST_ASSERT_NOT_EQUAL(led_pattern_kind_t::OFF, k);
        }
        const bool sized = k == led_pattern_kind_t::ONE_SHOT || k == led_pattern_kind_t::CONTINUOUS;
        TEST_ASSERT_EQUAL(sized, led_pattern_length(type) > 0);
    }
    TEST_ASSERT_EQUAL_INT(static_cast<int>(ft::PEU_RESULT_ERROR) + 1,
                          static_cast<int>(sizeof ALL / sizeof ALL[0]));
}

int main(void) {
    UNITY_BEGIN();
    RUN_TEST(test_every_pattern_trace);
    RUN_TEST(test_preemption_resets_tick);
    RUN_TEST(test_continuous_tick_stays_bounded);
    RUN_TEST(test_out_of_range_value_is_inert);
    RUN_TEST(test_every_type_has_a_pattern);
    return UNITY_END();
}
