#include "reader_health.hpp"

reader_step_t reader_health_on_read(reader_health_t &h, reader_read_result_t result,
                                    const reader_health_cfg_t &cfg)
{
    reader_step_t s;
    switch (result) {
    case reader_read_result_t::CREDENTIAL:
        h.consecutive_errors = 0;
        s.publish_credential = true;
        s.halt               = true;
        s.delay_ms           = cfg.reread_delay_ms;
        return s;

    case reader_read_result_t::NO_CREDENTIAL:
        h.consecutive_errors = 0;
        s.delay_ms           = cfg.poll_interval_ms;
        return s;

    case reader_read_result_t::HW_ERROR:
    default:
        if (h.consecutive_errors < UINT8_MAX) {
            ++h.consecutive_errors;
        }
        if (h.consecutive_errors >= cfg.error_threshold) {
            h.degraded = true;
            s.event    = reader_health_event_t::READ_ERROR;
            /* The first probe waits a full recovery interval. */
            s.delay_ms = cfg.recovery_interval_ms;
            return s;
        }
        s.delay_ms = cfg.poll_interval_ms;
        return s;
    }
}

reader_step_t reader_health_on_probe(reader_health_t &h, bool init_ok,
                                     const reader_health_cfg_t &cfg)
{
    reader_step_t s;
    if (init_ok) {
        h.degraded           = false;
        h.consecutive_errors = 0;
        s.event              = reader_health_event_t::RECOVERED;
        s.delay_ms           = 0;  /* resume reading immediately */
        return s;
    }
    s.delay_ms = cfg.recovery_interval_ms;
    return s;
}
