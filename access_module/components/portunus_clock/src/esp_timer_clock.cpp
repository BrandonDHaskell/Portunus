#include "esp_timer_clock.hpp"
#include "esp_timer.h"

int64_t EspTimerClock::now_ms() { return esp_timer_get_time() / 1000; }

IClock &default_clock() { static EspTimerClock instance; return instance; }
