/**
 * @file provisioning_fsm.h
 * @brief PEU (Provisioning & Enrollment Unit) 7-state FSM.
 *
 * State machine:
 *
 *   ┌─────── arm button ──────────────────────────────────────────────┐
 *   │                                                                 │
 *   ▼                                                                 │
 *  SLEEP ──(arm button)──► IDLE ──(arm button)──► ARMED
 *   ▲                       │  ◄──(idle timeout)──  │  ◄──(arm timeout)──┐
 *   │                       │                       │                    │
 *   └──(idle timeout)────── ┘                       │ (2nd press:toggle) │
 *                                                   │                    │
 *                     CAPTURE mode:                 │ ENROLL mode:       │
 *                       card tap                    │   card tap         │
 *                          │                        │      │             │
 *                          ▼                        │      ▼             │
 *                    CAPTURE_SEND               ENROLL_SCAN2 ─(timeout)─┘
 *                     (wait server)                 │
 *                          │                      card tap
 *                          │                        │
 *                          ▼                        ▼
 *                       RESULT ◄───── ENROLL_SEND (wait server)
 *                          │
 *                   (result timer)
 *                          │
 *                          ▼
 *                        IDLE  (or ARMED if no arm button present)
 *
 * Arm button press cycle (when in ARMED):
 *   press 1 (from IDLE)  → ARMED(CAPTURE)
 *   press 2              → ARMED(OPERATOR_ENROLL)
 *   press 3              → IDLE (cancel)
 *
 * When no IArm is wired (CONFIG_PORTUNUS_ENABLE_ARM_BUTTON=n):
 *   FSM boots directly into ARMED(CAPTURE) and returns there after every
 *   result. Sleep/Idle states are never entered.
 */

#pragma once

#include "event_types.h"
#include "i_arm.h"
#include "i_credential_reader.h"
#include "i_feedback.h"
#include "portunus_types.h"

#include "freertos/FreeRTOS.h"
#include "freertos/task.h"
#include "freertos/queue.h"

/** @brief PEU FSM state. */
typedef enum {
    PEU_STATE_SLEEP,         /**< Inactive: credential polling stopped, LED off */
    PEU_STATE_IDLE,          /**< Awake, waiting for arm button */
    PEU_STATE_ARMED,         /**< Armed: waiting for first card scan */
    PEU_STATE_CAPTURE_SEND,  /**< Capture request sent, awaiting server response */
    PEU_STATE_ENROLL_SCAN2,  /**< Operator scan done, waiting for new-member card */
    PEU_STATE_ENROLL_SEND,   /**< Enrol request sent, awaiting server response */
    PEU_STATE_RESULT,        /**< Showing result feedback before returning to Idle */
} peu_state_t;

/** @brief Provisioning mode active in ARMED state. */
typedef enum {
    PEU_MODE_CAPTURE,         /**< Next card → capture path (no operator UID sent) */
    PEU_MODE_OPERATOR_ENROLL, /**< Next card → scan-1 operator badge */
} peu_mode_t;

/**
 * @brief PEU Provisioning & Enrollment Unit FSM.
 *
 * Drop-in replacement for the old ProvisioningFSM when built with
 * CONFIG_PORTUNUS_MODULE_TYPE_PROVISIONING_CONSOLE. Requires a credential
 * reader; feedback and arm-button are optional (nullptr = absent = degrade
 * gracefully, not crash).
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
    peu_state_t  m_state        = PEU_STATE_IDLE;
    peu_mode_t   m_mode         = PEU_MODE_CAPTURE;
    int64_t      m_deadline_ms  = 0;   /**< Multipurpose timeout (arm/idle/scan2/result) */
    credential_t m_operator_cred = {}; /**< Operator badge UID stored at scan-1 (enrol mode) */

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
    void enter_armed(peu_mode_t mode);
    void enter_result(feedback_type_t fb);
    void enter_idle_after_result(); /**< → Idle if arm present, → Armed(CAPTURE) if not */

    /* ── Request helpers ──────────────────────────────────────────────────── */
    void publish_capture_request(const event_credential_read_t *cred);
    void publish_enroll_request(const event_credential_read_t *cred);

    /* ── Event bus bridge ─────────────────────────────────────────────────── */
    static void on_event_bus_event(const portunus_event_t *event, void *ctx);
};
