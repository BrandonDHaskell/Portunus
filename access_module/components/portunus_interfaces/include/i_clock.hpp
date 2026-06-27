#pragma once
#include <stdint.h>

/** Monotonic millisecond clock. The FSM owns all timing decisions and reads time
 *  only through this seam, so timing is deterministic under test. */
class IClock {
public:
    virtual ~IClock() = default;
    virtual int64_t now_ms() = 0;
};
