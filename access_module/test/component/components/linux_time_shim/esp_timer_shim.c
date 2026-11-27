#include "esp_timer.h"

#include <time.h>

/* Microseconds since the first call, from the host's monotonic clock (the
 * POSIX FreeRTOS tick here is 10 ms, too coarse). */
int64_t esp_timer_get_time(void)
{
    static int64_t base_us = -1;
    struct timespec ts;
    clock_gettime(CLOCK_MONOTONIC, &ts);
    const int64_t now_us = (int64_t)ts.tv_sec * 1000000 + ts.tv_nsec / 1000;
    if (base_us < 0) {
        base_us = now_us;
    }
    return now_us - base_us;
}
