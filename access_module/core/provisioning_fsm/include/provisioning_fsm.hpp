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
 *                                   (wait server; error after            │
 *                                    PROVISION_TIMEOUT_MS)               │
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
#include "i_clock.hpp"
#include "i_credential_reader.hpp"
#include "i_feedback.hpp"
#include "portunus_types.hpp"

#include "queue_pressure.hpp"

#include <atomic>

#include "freertos/FreeRTOS.h"
#include "freertos/task.h"
#include "freertos/queue.h"
#include "freertos/semphr.h"

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
    /** Event ids start() subscribes to. Public so main.cpp can check the
     *  event bus subscriber budget at compile time (COMP-01). */
    static constexpr portunus_event_id_t kSubscribedEvents[] = {
        EVENT_PROVISION_SUCCESS, EVENT_PROVISION_FAILED,
        EVENT_CREDENTIAL_READ,   EVENT_ARM_REQUESTED,
    };

    /**
     * @param reader    Credential reader (must not be nullptr).
     * @param feedback  Feedback LED (nullptr disables indication).
     * @param arm       Arm button driver (nullptr → always-armed mode).
     */
    ProvisioningFSM(ICredentialReader *reader, IFeedback *feedback, IArm *arm,
                    IClock *clock);

    /** @brief Initialise hardware and create FreeRTOS primitives. */
    portunus_err_t init();

    /** @brief Start FSM task and poll task.  Call after init() and event_bus_init(). */
    portunus_err_t start();

    /**
     * @brief Stop the FSM (D-T9): remove its event bus subscriptions, stop
     *        both tasks after what each is doing, and turn the LED off.
     *        The FSM can be started again. A no-op if not running.
     *
     * @return PORTUNUS_OK once stopped (or if not running), or
     *         PORTUNUS_ERR_TIMEOUT if a task did not stop in time; call
     *         stop() again.
     */
    portunus_err_t stop();

    /** Stops the FSM (see stop()) and frees its queue. */
    ~ProvisioningFSM();

    ProvisioningFSM(const ProvisioningFSM &)            = delete;
    ProvisioningFSM &operator=(const ProvisioningFSM &) = delete;

    /** @brief Current FSM state (for diagnostics / tests). */
    peu_state_t state() const { return m_state; }

private:
    /* ── Injected dependencies ────────────────────────────────────────────── */
    ICredentialReader *m_reader;
    IFeedback         *m_feedback;
    IArm              *m_arm;
    IClock            *m_clock;

    /* ── Capability flags ─────────────────────────────────────────────────── */
    bool m_has_reader   = false;
    bool m_has_feedback = false;
    bool m_has_arm      = false;
    bool m_has_network  = false;

    /* ── FSM state ────────────────────────────────────────────────────────── */
    /* Written by the FSM task, read by the poll task (it reads cards only
     * while ARMED): atomic (F15). */
    std::atomic<peu_state_t> m_state{PEU_STATE_IDLE};
    int64_t      m_deadline_ms = 0;   /**< Multipurpose timeout (idle/arm/capture/result); 0 = none */
    /* Events lost to a full FSM queue (bus dispatcher) or a failed publish
     * from the poll task: atomic. Checked by the FSM task (D-T5). */
    std::atomic<uint32_t> m_dropped{0};
    uint32_t              m_dropped_seen = 0;   /**< FSM task only */
    queue_pressure_t      m_queue_pressure;     /**< FSM task only */

    /* ── FreeRTOS handles ─────────────────────────────────────────────────── */
    TaskHandle_t  m_fsm_task_handle  = nullptr;
    TaskHandle_t  m_poll_task_handle = nullptr;
    QueueHandle_t m_event_queue      = nullptr;

    /* ── Cooperative shutdown (D-T9) ──────────────────────────────────────── */
    std::atomic<bool> m_stop_requested{false};
    SemaphoreHandle_t m_fsm_exited  = nullptr;
    SemaphoreHandle_t m_poll_exited = nullptr;
    bool              m_subscribed  = false;

    /* ── Task entry points ────────────────────────────────────────────────── */
    static void fsm_task_entry(void *arg);
    static void poll_task_entry(void *arg);

    /* ── FSM loop and dispatch ────────────────────────────────────────────── */
    void run();
    void poll();
    void process_event(const portunus_event_t &event);
    bool receive_and_process(TickType_t timeout);  /**< run() step 1 */
    void check_dropped_events();
    void check_timeout();

    /* ── Event handlers ───────────────────────────────────────────────────── */
    void handle_arm_requested();
    void handle_credential_read(const event_credential_read_t *cred);
    void handle_provision_result(const event_provision_result_t *result);

    /* ── State transitions ────────────────────────────────────────────────── */
    void enter_initial_state();     /**< Idle if arm present, Armed if not (start()) */
    void enter_sleep();
    void enter_idle();
    void enter_armed();
    void enter_result(feedback_type_t fb);
    void enter_idle_after_result(); /**< → Idle if arm present, → Armed if not */

    /* ── Request helpers ──────────────────────────────────────────────────── */
    bool publish_capture_request(const event_credential_read_t *cred);

    /* ── Event bus bridge ─────────────────────────────────────────────────── */
    static void on_event_bus_event(const portunus_event_t *event, void *ctx);

    /* Test-only: drives the FSM synchronously without starting tasks. */
    friend class ProvisioningFSMTestFixture;
};
