/* Press-edge detection for the arm button, extracted from
 * ArmButtonGpio::poll_arm so the unit tier can drive it with synthetic
 * samples. No ESP-IDF dependency. */
#pragma once

/** Consecutive pressed samples before a press counts. */
static constexpr int ARM_EDGE_THRESHOLD = 3;

/** Pressed is LOW for active-low wiring (pull-up) and HIGH for active-high. */
constexpr bool arm_level_pressed(int level, bool active_low)
{
    return level == (active_low ? 0 : 1);
}

struct arm_edge_t {
    int count = 0;  /**< Consecutive pressed samples, capped at the threshold. */
};

/** Feed one sample. Returns true exactly once per press, on the threshold
 *  sample; a hold never fires again. Any released sample resets the count,
 *  so release is not debounced: a single released sample in the middle of a
 *  hold starts a new press. At the 100 ms poll rate that needs a contact
 *  bounce to land on a sample. */
inline bool arm_edge_step(arm_edge_t &e, bool pressed)
{
    if (!pressed) {
        e.count = 0;
        return false;
    }
    if (e.count < ARM_EDGE_THRESHOLD) {
        ++e.count;
        return e.count == ARM_EDGE_THRESHOLD;
    }
    return false;
}
