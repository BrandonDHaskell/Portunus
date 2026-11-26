#include "led_pattern.hpp"

using ft = feedback_type_t;

/* ── Pattern timing (ticks of LED_TICK_MS) ─────────────────────────────────── */

/* ACCESS_GRANTED: solid on for 1 s. */
static constexpr uint32_t GRANTED_ON = 20;

/* ACCESS_DENIED: 3x blink, 100 ms on + 100 ms off. */
static constexpr uint32_t DENIED_ON = 2, DENIED_CYCLE = 4, DENIED_COUNT = 3;

/* SYSTEM_READY: 50 ms blink every 3 s. */
static constexpr uint32_t READY_ON = 1, READY_CYCLE = 60;

/* SYSTEM_ERROR and PEU_RESULT_ERROR: 200 ms on / 200 ms off. */
static constexpr uint32_t ERROR_HALF = 4;

/* Double pulse: on, 150 ms gap, on, long off. */
static constexpr uint32_t DOUBLE_ON = 1, DOUBLE_GAP = 3;
static constexpr uint32_t PROV_IDLE_CYCLE = 80;   /* 4 s */
static constexpr uint32_t PEU_IDLE_CYCLE  = 120;  /* 6 s */

/* PROVISIONING_AWAITING: 1 s on / 1 s off. */
static constexpr uint32_t PROV_AWAIT_HALF = 20;

/* *_SUCCESS: 5x rapid blinks. */
static constexpr uint32_t SUCCESS_ON = 2, SUCCESS_CYCLE = 4, SUCCESS_COUNT = 5;

/* *_DUPLICATE: 2x medium blinks. */
static constexpr uint32_t DUP_ON = 4, DUP_CYCLE = 8, DUP_COUNT = 2;

/* *_UNAUTHORIZED: 500 ms on, one rapid cycle off as a gap, then 3x rapid. */
static constexpr uint32_t UNAUTH_LONG_ON = 10;
static constexpr uint32_t UNAUTH_RAPID_ON = 2, UNAUTH_RAPID_CYCLE = 4, UNAUTH_RAPID_COUNT = 3;
static constexpr uint32_t UNAUTH_RAPID_START = UNAUTH_LONG_ON + UNAUTH_RAPID_CYCLE;

/* PEU_ARMED_CAPTURE: three 50 ms pulses 100 ms apart, every 2 s. */
static constexpr uint32_t CAP_ON = 1, CAP_STRIDE = 3, CAP_PULSES = 3, CAP_CYCLE = 40;

/* PEU_ARMED_ENROLL: 500 ms on / 500 ms off. */
static constexpr uint32_t ENROLL_HALF = 10;

/* PEU_RESULT_PENDING: 3x slow blinks, 200 ms on / 600 ms off. */
static constexpr uint32_t PENDING_ON = 4, PENDING_CYCLE = 16, PENDING_COUNT = 3;

/* ── Pattern table ─────────────────────────────────────────────────────────── */

led_pattern_kind_t led_pattern_kind(feedback_type_t type)
{
    switch (type) {
    case ft::ACCESS_GRANTED:
    case ft::ACCESS_DENIED:
    case ft::PROVISIONING_SUCCESS:
    case ft::PROVISIONING_DUPLICATE:
    case ft::PROVISIONING_UNAUTHORIZED:
    case ft::PEU_RESULT_PENDING:
    case ft::PEU_RESULT_SUCCESS:
    case ft::PEU_RESULT_DUPLICATE:
    case ft::PEU_RESULT_UNAUTHORIZED:
        return led_pattern_kind_t::ONE_SHOT;
    case ft::SYSTEM_READY:
    case ft::SYSTEM_ERROR:
    case ft::PROVISIONING_IDLE:
    case ft::PROVISIONING_AWAITING:
    case ft::PEU_IDLE:
    case ft::PEU_ARMED_CAPTURE:
    case ft::PEU_ARMED_ENROLL:
    case ft::PEU_RESULT_ERROR:
        return led_pattern_kind_t::CONTINUOUS;
    case ft::CARD_READ:
        return led_pattern_kind_t::HELD;
    case ft::NONE:
    default:
        return led_pattern_kind_t::OFF;
    }
}

uint32_t led_pattern_length(feedback_type_t type)
{
    switch (type) {
    case ft::ACCESS_GRANTED:            return GRANTED_ON;
    case ft::ACCESS_DENIED:             return DENIED_CYCLE * DENIED_COUNT;
    case ft::PROVISIONING_SUCCESS:
    case ft::PEU_RESULT_SUCCESS:        return SUCCESS_CYCLE * SUCCESS_COUNT;
    case ft::PROVISIONING_DUPLICATE:
    case ft::PEU_RESULT_DUPLICATE:      return DUP_CYCLE * DUP_COUNT;
    case ft::PROVISIONING_UNAUTHORIZED:
    case ft::PEU_RESULT_UNAUTHORIZED:   return UNAUTH_RAPID_START + UNAUTH_RAPID_CYCLE * UNAUTH_RAPID_COUNT;
    case ft::PEU_RESULT_PENDING:        return PENDING_CYCLE * PENDING_COUNT;
    case ft::SYSTEM_READY:              return READY_CYCLE;
    case ft::SYSTEM_ERROR:
    case ft::PEU_RESULT_ERROR:          return ERROR_HALF * 2;
    case ft::PROVISIONING_IDLE:         return PROV_IDLE_CYCLE;
    case ft::PROVISIONING_AWAITING:     return PROV_AWAIT_HALF * 2;
    case ft::PEU_IDLE:                  return PEU_IDLE_CYCLE;
    case ft::PEU_ARMED_CAPTURE:         return CAP_CYCLE;
    case ft::PEU_ARMED_ENROLL:          return ENROLL_HALF * 2;
    default:                            return 0;
    }
}

static bool double_pulse(uint32_t pos)
{
    return pos < DOUBLE_ON || (pos >= DOUBLE_ON + DOUBLE_GAP && pos < 2 * DOUBLE_ON + DOUBLE_GAP);
}

bool led_pattern_level(feedback_type_t type, uint32_t tick)
{
    const uint32_t len = led_pattern_length(type);
    const uint32_t pos = (len > 0) ? tick % len : 0;

    switch (type) {
    case ft::CARD_READ:                 return true;
    case ft::ACCESS_GRANTED:            return tick < GRANTED_ON;
    case ft::ACCESS_DENIED:             return tick < len && pos % DENIED_CYCLE < DENIED_ON;
    case ft::PROVISIONING_SUCCESS:
    case ft::PEU_RESULT_SUCCESS:        return tick < len && pos % SUCCESS_CYCLE < SUCCESS_ON;
    case ft::PROVISIONING_DUPLICATE:
    case ft::PEU_RESULT_DUPLICATE:      return tick < len && pos % DUP_CYCLE < DUP_ON;
    case ft::PEU_RESULT_PENDING:        return tick < len && pos % PENDING_CYCLE < PENDING_ON;
    case ft::PROVISIONING_UNAUTHORIZED:
    case ft::PEU_RESULT_UNAUTHORIZED:
        if (tick < UNAUTH_LONG_ON) return true;
        if (tick < UNAUTH_RAPID_START || tick >= len) return false;
        return (tick - UNAUTH_RAPID_START) % UNAUTH_RAPID_CYCLE < UNAUTH_RAPID_ON;
    case ft::SYSTEM_READY:              return pos < READY_ON;
    case ft::SYSTEM_ERROR:
    case ft::PEU_RESULT_ERROR:          return pos < ERROR_HALF;
    case ft::PROVISIONING_IDLE:
    case ft::PEU_IDLE:                  return double_pulse(pos);
    case ft::PROVISIONING_AWAITING:     return pos < PROV_AWAIT_HALF;
    case ft::PEU_ARMED_CAPTURE:         return pos < CAP_STRIDE * CAP_PULSES && pos % CAP_STRIDE < CAP_ON;
    case ft::PEU_ARMED_ENROLL:          return pos < ENROLL_HALF;
    default:                            return false;
    }
}

bool led_pattern_is_baseline(feedback_type_t type)
{
    (void)type;
    return false;  /* one-shots end with the LED off */
}

/* ── Engine ────────────────────────────────────────────────────────────────── */

void led_engine_indicate(led_engine_t &e, feedback_type_t type)
{
    e.current = type;
    e.tick    = 0;
    if (type == ft::NONE) {
        e.baseline = ft::NONE;
    } else if (led_pattern_is_baseline(type)) {
        e.baseline = type;
    }
}

bool led_engine_step(led_engine_t &e)
{
    const led_pattern_kind_t kind = led_pattern_kind(e.current);
    const uint32_t           len  = led_pattern_length(e.current);

    if (kind == led_pattern_kind_t::ONE_SHOT && e.tick >= len) {
        /* Finished: off for this tick, then resume the baseline one tick in,
         * so its first pulse is not glued to the end of the one-shot. */
        e.current = e.baseline;
        e.tick    = 1;
        return false;
    }

    const bool level = led_pattern_level(e.current, e.tick);

    switch (kind) {
    case led_pattern_kind_t::CONTINUOUS:
        e.tick = (e.tick + 1) % len;
        break;
    case led_pattern_kind_t::ONE_SHOT:
        ++e.tick;  /* bounded by len, checked above */
        break;
    case led_pattern_kind_t::HELD:
    case led_pattern_kind_t::OFF:
    default:
        break;     /* tick is unused */
    }
    return level;
}
