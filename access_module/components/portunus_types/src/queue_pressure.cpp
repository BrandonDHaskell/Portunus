#include "queue_pressure.hpp"

queue_pressure_event_t queue_pressure_sample(queue_pressure_t &qp, size_t depth, size_t capacity)
{
    if (capacity == 0) {
        return queue_pressure_event_t::NONE;
    }
    const bool high = depth * 4 >= capacity * 3;   /* >= 75% */
    const bool low  = depth * 2 < capacity;        /* <  50% */

    if (high) {
        if (qp.streak < QUEUE_PRESSURE_STREAK) {
            ++qp.streak;
        }
        if (qp.streak >= QUEUE_PRESSURE_STREAK && !qp.warned) {
            qp.warned = true;
            return queue_pressure_event_t::HIGH;
        }
        return queue_pressure_event_t::NONE;
    }

    qp.streak = 0;
    if (low && qp.warned) {
        qp.warned = false;
        return queue_pressure_event_t::RECOVERED;
    }
    return queue_pressure_event_t::NONE;
}
