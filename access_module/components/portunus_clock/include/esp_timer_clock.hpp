#pragma once
#include "i_clock.hpp"

class EspTimerClock : public IClock {
public:
    int64_t now_ms() override;
};

/** Process-wide production clock — pass &default_clock() to the FSM constructor. */
IClock &default_clock();
