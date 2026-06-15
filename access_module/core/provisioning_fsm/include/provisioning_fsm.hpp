/**
 * @file provisioning_fsm.h
 * @brief PEU (Provisioning & Enrollment Unit) capture-only FSM.
 *
 * State machine:
 *
 *  SLEEP ──(arm button)──► IDLE ──(arm button)──► ARMED
 *   ▲                       │  ◄──(idle timeout)──  │  ◄──(arm timeout)──┐
 *   │                       │                       │                    │
 *   └──(idle timeout)────── ┘                     card tap               │
 *                                                    │                   │
 *                                             CAPTURE_SEND               │
 *                                              (wait server)             │
 *                                                    │                   │
 *                                                 RESULT ────────────────┘
 *                                                    │
 *                                             (result timer)
 *                                                    │
 *                                                  IDLE  (or ARMED if no arm button)
 *
 * Arm button press cycle:
 *   press 1 (from IDLE) → ARMED
 *   press 2             → IDLE (cancel)
 *
 * When no IArm is wired (CONFIG_PORTUNUS_ENABLE_ARM_BUTTON=n):
 *   FSM boots directly into ARMED and returns there after every result.
 *   Sleep/Idle states are never entered.
 */

#pragma once

#include "event_types.hpp"
#include "i_arm.hpp"
#include "i_credential_reader.hpp"
#include "i_feedback.hpp"
#include "portunus_types.hpp"

#include "freertos/FreeRTOS.h"
#include "freertos/task.h"
#include "freertos/queue.h"

/** @brief PEU FSM state. */
typedef enum {
    PEU_STATE_SLEEP,         /**< Inactive: credential polling stopped, LED off */
    PEU_STATE_IDLE,          /**< Awake, waiting for arm button */
    PEU_STATE_ARMED,         /**< Armed: waiting for card scan */
    PEU_STATE_CAPTURE_SEND,  /**< Capture request sent, awaiting server response */
    PEU_STATE_RESULT,        /**< Showing result feedback before returning to Idle */
} peu_state_t;

/**
 * @brief PEU Provisioning & Enrollment Unit FSM.
 *
 * Capture-only: a single card scan creates a pending_authorization record;
 * an admin approves it later via the console.
 *
 * Requires a credential reader; feedback and arm-button are optional
 * (nullptr = absent = degrade gracefully, not crash).
 */
class ProvisioningFSM {
public:
    /**
     * @param reader    Credential reader (must not be nullptr).
     * @param feedback  Feedback LED (nullptr disables indication).
     * @param arm       Arm button driver (nullptr → always-armed mode).
     */
    ProvisioningFSM(ICredentialReader *reader, IFeedback *feedback, IArm *arm);

    /** @brief Initialise hardware and create FreeRTOS primitives. */
    portunus_err_t init();

    /** @brief Start FSM task and poll task.  Call after init() and event_bus_init(). */
    portunus_err_t start();

    /** @brief Current FSM state (for diagnostics / tests). */
    peu_state_t state() const { return m_state; }

private:
    /* ── Injected dependencies ────────────────────────────────────────────── */
    ICredentialReader *m_reader;
    IFeedback         *m_feedback;
    IArm              *m_arm;

    /* ── Capability flags ─────────────────────────────────────────────────── */
    bool m_has_reader   = false;
    bool m_has_feedback = false;
    bool m_has_arm      = false;
    bool m_has_network  = false;

    /* ── FSM state ────────────────────────────────────────────────────────── */
    peu_state_t  m_state       = PEU_STATE_IDLE;
    int64_t      m_deadline_ms = 0;   /**< Multipurpose timeout (arm/idle/result) */

    /* ── FreeRTOS handles ─────────────────────────────────────────────────── */
    TaskHandle_t  m_fsm_task_handle  = nullptr;
    TaskHandle_t  m_poll_task_handle = nullptr;
    QueueHandle_t m_event_queue      = nullptr;

    /* ── Task entry points ────────────────────────────────────────────────── */
    static void fsm_task_entry(void *arg);
    static void poll_task_entry(void *arg);

    /* ── FSM loop and dispatch ────────────────────────────────────────────── */
    void run();
    void poll();
    void process_event(const portunus_event_t &event);
    void check_timeout();

    /* ── Event handlers ───────────────────────────────────────────────────── */
    void handle_arm_requested();
    void handle_credential_read(const event_credential_read_t *cred);
    void handle_provision_result(const event_provision_result_t *result);

    /* ── State transitions ────────────────────────────────────────────────── */
    void enter_sleep();
    void enter_idle();
    void enter_armed();
    void enter_result(feedback_type_t fb);
    void enter_idle_after_result(); /**< → Idle if arm present, → Armed if not */

    /* ── Request helpers ──────────────────────────────────────────────────── */
    void publish_capture_request(const event_credential_read_t *cred);

    /* ── Event bus bridge ─────────────────────────────────────────────────── */
    static void on_event_bus_event(const portunus_event_t *event, void *ctx);
};
