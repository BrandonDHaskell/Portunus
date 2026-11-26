/* Pure credential-reader health policy extracted from SystemFSM::poll_credential.
 * Each call is one iteration of the poll loop: it takes the outcome of a read
 * (or of a recovery probe while degraded) and returns what the loop must do
 * next. It performs nothing itself. sdkconfig-free and FreeRTOS-free, so the
 * unit tier links it directly; the poll loop calls the same functions. */
#pragma once

#include <stdint.h>

/** Outcome of one ICredentialReader::read(). */
enum class reader_read_result_t : uint8_t {
    CREDENTIAL,     /**< A card was read. */
    NO_CREDENTIAL,  /**< No card in field; the chip answered, so it is healthy. */
    HW_ERROR,       /**< Any other error: timeout, SPI, collision, CRC. */
};

/** Health transition the loop must announce on the event bus. */
enum class reader_health_event_t : uint8_t {
    NONE,
    READ_ERROR,  /**< Entered degraded mode: publish EVENT_CREDENTIAL_READ_ERROR. */
    RECOVERED,   /**< Left degraded mode: publish EVENT_CREDENTIAL_READER_RECOVERED. */
};

struct reader_health_cfg_t {
    uint8_t  error_threshold;       /**< Consecutive HW errors before degraded. */
    uint32_t poll_interval_ms;      /**< Delay after an idle or failed read. */
    uint32_t reread_delay_ms;       /**< Delay after a successful read (debounce). */
    uint32_t recovery_interval_ms;  /**< Delay before each probe while degraded. */
};

struct reader_health_t {
    uint8_t consecutive_errors = 0;
    bool    degraded           = false;
};

/** What the loop does after this iteration, in order: publish the credential,
 *  halt the card, announce `event`, then sleep `delay_ms` (0 = no sleep). */
struct reader_step_t {
    bool                  publish_credential = false;
    bool                  halt               = false;
    reader_health_event_t event              = reader_health_event_t::NONE;
    uint32_t              delay_ms           = 0;
};

/** One read while healthy. Must not be called while `h.degraded`. */
reader_step_t reader_health_on_read(reader_health_t &h, reader_read_result_t result,
                                    const reader_health_cfg_t &cfg);

/** One recovery probe (ICredentialReader::init()) while degraded. */
reader_step_t reader_health_on_probe(reader_health_t &h, bool init_ok,
                                     const reader_health_cfg_t &cfg);
