#pragma once
#include "i_clock.hpp"

class FakeClock : public IClock {
public:
    int64_t now_ms() override { return m_now; }
    void advance(int64_t ms)  { m_now += ms; }
    void set(int64_t ms)      { m_now = ms; }
private:
    int64_t m_now = 0;
};
