#include "debounce.hpp"

void debouncer_init(debouncer_t &d, bool sample, int64_t now_us)
{
    d.stable    = sample;
    d.candidate = sample;
    d.since_us  = now_us;
}

bool debouncer_update(debouncer_t &d, bool sample, int64_t now_us, uint32_t window_ms)
{
    if (sample != d.candidate) {
        d.candidate = sample;
        d.since_us  = now_us;
    } else if (sample != d.stable) {
        /* Whole milliseconds, truncated: 9.999 ms of a 10 ms window is not enough. */
        const int64_t elapsed_ms = (now_us - d.since_us) / 1000;
        if (elapsed_ms >= window_ms) {
            d.stable = sample;
        }
    }
    return d.stable;
}
