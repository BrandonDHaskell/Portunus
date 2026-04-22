/**
 * @file i_feedback.h
 * @brief Abstract interface for user-facing feedback hardware.
 *
 * Provides visual (and eventually audible) feedback for system state
 * and access decisions.  The FSM calls indicate() with a semantic
 * feedback type — the concrete implementation decides how to render
 * it (LED blink pattern, buzzer tone, OLED message, etc.).
 *
 * indicate() is non-blocking and preemptive: a new call cancels any
 * in-progress pattern and starts the new one immediately.  The FSM
 * must never block on cosmetic operations.
 *
 * Current implementation: LedFeedback (wraps led driver, owns pattern task).
 *
 * Architecture layer: Module (see project plan §3.1–3.2).
 */

#pragma once

#include "portunus_types.h"

#include <stdint.h>

/**
 * @brief Semantic feedback types.
 *
 * The FSM expresses intent ("access was granted") and the feedback
 * module decides presentation ("blink green for 1 second").  This
 * decoupling means adding an RGB LED or buzzer doesn't change the
 * FSM at all — only the concrete IFeedback implementation.
 */
enum class feedback_type_t : uint8_t {
    NONE,               /**< Clear any active pattern, output off */
    ACCESS_GRANTED,     /**< Grant indication (one-shot) */
    ACCESS_DENIED,      /**< Deny indication (one-shot) */
    SYSTEM_READY,       /**< Operational idle (continuous until replaced) */
    SYSTEM_ERROR,       /**< Error state (continuous until replaced) */
    CARD_READ,          /**< Card detected, awaiting server (held until replaced) */

    /* Provisioning console states (PROVISIONING_CONSOLE firmware only) */
    PROVISIONING_IDLE,          /**< Awaiting operator scan — double heartbeat pulse (continuous) */
    PROVISIONING_AWAITING,      /**< Operator scan done, awaiting new credential — slow 50/50 pulse (continuous) */
    PROVISIONING_SUCCESS,       /**< Provisioning succeeded — 5× rapid blinks (one-shot) */
    PROVISIONING_DUPLICATE,     /**< Credential already exists — 2× medium blinks (one-shot) */
    PROVISIONING_UNAUTHORIZED,  /**< Operator not authorised — long on + 3× rapid blinks (one-shot) */
};

/**
 * @brief Abstract feedback interface.
 */
class IFeedback {
public:
    virtual ~IFeedback() = default;

    /**
     * @brief Initialise the feedback hardware.
     *
     * All outputs must be off (inactive) after init.
     *
     * @return PORTUNUS_OK on success, or a driver-specific error code.
     */
    virtual portunus_err_t init() = 0;

    /**
     * @brief Trigger a feedback indication.
     *
     * Non-blocking.  If a pattern is already in progress, it is
     * cancelled immediately and the new pattern starts.  The FSM
     * does not track pattern completion.
     *
     * One-shot patterns (ACCESS_GRANTED, ACCESS_DENIED) run to
     * completion and return to idle automatically.  Continuous
     * patterns (SYSTEM_READY, SYSTEM_ERROR) loop until replaced
     * by another indicate() call.  CARD_READ holds until replaced.
     *
     * @param type  The semantic feedback to indicate.
     */
    virtual void indicate(feedback_type_t type) = 0;
};
