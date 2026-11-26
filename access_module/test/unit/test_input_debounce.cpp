/* Unit tier: reed switch debounce and arm button press-edge detection.
 * No ESP-IDF, no FreeRTOS, no sdkconfig. Bare host compiler. */
#include "unity.h"
#include "debounce.hpp"
#include "arm_edge.hpp"

void setUp(void) {}
void tearDown(void) {}

static constexpr uint32_t WINDOW_MS = 50;
static constexpr int64_t  MS        = 1000;  /* microseconds */
static constexpr int64_t  T0        = 7 * MS;

/* DRV-01: a new state is accepted at exactly the window, not 1 us or 1 ms
 * earlier, and only when a sample arrives (there is no timer). */
void test_state_changes_at_exactly_the_window(void) {
    debouncer_t d;
    debouncer_init(d, false, 0);

    TEST_ASSERT_FALSE(debouncer_update(d, true, T0, WINDOW_MS));  /* window opens */
    TEST_ASSERT_FALSE(debouncer_update(d, true, T0 + (WINDOW_MS - 1) * MS, WINDOW_MS));
    TEST_ASSERT_FALSE(debouncer_update(d, true, T0 + WINDOW_MS * MS - 1, WINDOW_MS));
    TEST_ASSERT_TRUE(debouncer_update(d, true, T0 + WINDOW_MS * MS, WINDOW_MS));

    /* And back the other way with the same boundary. */
    const int64_t t1 = T0 + 200 * MS;
    TEST_ASSERT_TRUE(debouncer_update(d, false, t1, WINDOW_MS));
    TEST_ASSERT_TRUE(debouncer_update(d, false, t1 + WINDOW_MS * MS - 1, WINDOW_MS));
    TEST_ASSERT_FALSE(debouncer_update(d, false, t1 + WINDOW_MS * MS, WINDOW_MS));
}

/* DRV-01: init accepts the first sample with no settling period. */
void test_init_accepts_first_sample(void) {
    debouncer_t d;
    debouncer_init(d, true, T0);
    TEST_ASSERT_TRUE(d.stable);
    TEST_ASSERT_TRUE(debouncer_update(d, true, T0, WINDOW_MS));
}

/* DRV-02: a bounce inside the window restarts it, measured from the last
 * change; a bounce back to the stable level cancels the pending change. */
void test_bounce_restarts_window(void) {
    debouncer_t d;
    debouncer_init(d, false, 0);

    debouncer_update(d, true, T0, WINDOW_MS);
    debouncer_update(d, false, T0 + 30 * MS, WINDOW_MS);  /* bounce */
    const int64_t t_last = T0 + 40 * MS;
    debouncer_update(d, true, t_last, WINDOW_MS);         /* window restarts here */
    TEST_ASSERT_FALSE(debouncer_update(d, true, T0 + WINDOW_MS * MS, WINDOW_MS));
    TEST_ASSERT_FALSE(debouncer_update(d, true, t_last + WINDOW_MS * MS - 1, WINDOW_MS));
    TEST_ASSERT_TRUE(debouncer_update(d, true, t_last + WINDOW_MS * MS, WINDOW_MS));

    /* Pending change cancelled: stable stays true however long it runs. */
    const int64_t t1 = t_last + 500 * MS;
    debouncer_update(d, false, t1, WINDOW_MS);
    debouncer_update(d, true, t1 + 10 * MS, WINDOW_MS);
    TEST_ASSERT_TRUE(debouncer_update(d, true, t1 + 1000 * MS, WINDOW_MS));
}

/* DRV-03: with the internal pull-up, NO reads LOW when closed and NC reads
 * HIGH when closed. */
void test_no_nc_level_mapping(void) {
    TEST_ASSERT_TRUE(reed_level_is_closed(0, false));
    TEST_ASSERT_FALSE(reed_level_is_closed(1, false));
    TEST_ASSERT_TRUE(reed_level_is_closed(1, true));
    TEST_ASSERT_FALSE(reed_level_is_closed(0, true));
}

/* DRV-05: true exactly once, on the third pressed sample. */
void test_arm_fires_once_on_third_pressed_sample(void) {
    arm_edge_t e;
    TEST_ASSERT_FALSE(arm_edge_step(e, true));
    TEST_ASSERT_FALSE(arm_edge_step(e, true));
    TEST_ASSERT_TRUE(arm_edge_step(e, true));
}

/* DRV-05: a long hold never fires again and the count stays bounded. */
void test_arm_hold_never_retriggers(void) {
    arm_edge_t e;
    int fired = 0;
    for (int i = 0; i < 1000; ++i) fired += arm_edge_step(e, true);
    TEST_ASSERT_EQUAL_INT(1, fired);
    TEST_ASSERT_EQUAL_INT(ARM_EDGE_THRESHOLD, e.count);
}

/* DRV-05: release resets, so the next press fires again after three
 * samples; a press shorter than three samples never fires. */
void test_arm_release_resets(void) {
    arm_edge_t e;
    for (int i = 0; i < 5; ++i) arm_edge_step(e, true);
    TEST_ASSERT_FALSE(arm_edge_step(e, false));
    TEST_ASSERT_EQUAL_INT(0, e.count);

    TEST_ASSERT_FALSE(arm_edge_step(e, true));
    TEST_ASSERT_FALSE(arm_edge_step(e, true));
    TEST_ASSERT_FALSE(arm_edge_step(e, false));  /* short press */
    TEST_ASSERT_FALSE(arm_edge_step(e, true));
    TEST_ASSERT_FALSE(arm_edge_step(e, true));
    TEST_ASSERT_TRUE(arm_edge_step(e, true));
}

/* DRV-05: active-low (pull-up) presses read LOW, active-high read HIGH. */
void test_arm_level_mapping(void) {
    TEST_ASSERT_TRUE(arm_level_pressed(0, true));
    TEST_ASSERT_FALSE(arm_level_pressed(1, true));
    TEST_ASSERT_TRUE(arm_level_pressed(1, false));
    TEST_ASSERT_FALSE(arm_level_pressed(0, false));

    /* End to end for active-high wiring: three HIGH samples fire. */
    arm_edge_t e;
    TEST_ASSERT_FALSE(arm_edge_step(e, arm_level_pressed(1, false)));
    TEST_ASSERT_FALSE(arm_edge_step(e, arm_level_pressed(1, false)));
    TEST_ASSERT_TRUE(arm_edge_step(e, arm_level_pressed(1, false)));
}

int main(void) {
    UNITY_BEGIN();
    RUN_TEST(test_state_changes_at_exactly_the_window);
    RUN_TEST(test_init_accepts_first_sample);
    RUN_TEST(test_bounce_restarts_window);
    RUN_TEST(test_no_nc_level_mapping);
    RUN_TEST(test_arm_fires_once_on_third_pressed_sample);
    RUN_TEST(test_arm_hold_never_retriggers);
    RUN_TEST(test_arm_release_resets);
    RUN_TEST(test_arm_level_mapping);
    return UNITY_END();
}
