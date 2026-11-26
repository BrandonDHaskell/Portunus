/* Pure door-strike level mapping, shared by door_strike.cpp and the unit tier.
 * No ESP-IDF dependency. */
#pragma once

/** GPIO level that puts the strike in the requested state. */
constexpr int strike_level(bool active_low, bool energized)
{
    return (energized != active_low) ? 1 : 0;
}

/** True when a pad readback confirms the strike is in the requested state. */
constexpr bool strike_readback_matches(bool active_low, bool energized, int pad_level)
{
    return pad_level == strike_level(active_low, energized);
}
