/**
 * @file system_fsm.h
 * @brief System FSM — top-level decision maker for the Portunus access module.
 *
 * The FSM orchestrates all module interactions:
 *   - Initialises modules and records capability flags.
 *   - Owns the credential-polling sub-task.
 *   - Subscribes to event bus events and processes them.
 *   - Manages unlock timing (energize → hold → re-lock).
 *   - Polls the reed switch and publishes door state change events.
 *   - Coordinates feedback indications.
 *
 * Modules are injected via the constructor as abstract interface pointers.
 * A nullptr for any module indicates that hardware is absent — the FSM
 * sets the corresponding capability flag to false and adapts behaviour.
 *
 * Architecture layer: Core / FSM (see project plan §3.1, §5.2).
 */

#pragma once

#include "system_states.hpp"
#include "event_types.hpp"
#include "i_credential_reader.hpp"
#include "i_access_point.hpp"
#include "i_feedback.hpp"
#include "i_clock.hpp"

#include "queue_pressure.hpp"

#include <atomic>

#include "freertos/FreeRTOS.h"
#include "freertos/task.h"
#include "freertos/queue.h"
#include "freertos/semphr.h"

/**
 * @brief System FSM — coordinates modules and manages system state.
 */
class SystemFSM {
public:
    /** Event ids start() subscribes to. Public so main.cpp can check the
     *  event bus subscriber budget at compile time (COMP-01). */
    static constexpr portunus_event_id_t kSubscribedEvents[] = {
        EVENT_CREDENTIAL_READ, EVENT_ACCESS_GRANTED, EVENT_ACCESS_DENIED,
    };

    /**
     * @brief Construct the FSM with injected module dependencies.
     *
     * Any pointer may be nullptr to indicate absent hardware.
     * Module lifetime is managed by the caller (main.cpp).
     *
     * @param reader   Credential reader module (or nullptr).
     * @param access   Access point module (or nullptr).
     * @param feedback Feedback module (or nullptr).
     */
    SystemFSM(ICredentialReader *reader,
              IAccessPoint      *access,
              IFeedback         *feedback,
              IClock            *clock);

    /**
     * @brief Initialise modules and set capability flags.
     *
     * Calls init() on each non-null module.  Sets has_reader,
     * has_access_point, has_feedback based on init results.
     * has_network is set from the WiFi manager's current state.
     *
     * A missing or failing module is not fatal, the access point included:
     * its capability flag stays false and the FSM degrades (a grant shows
     * feedback but cannot unlock). The module keeps running and sending
     * heartbeats, so the server still sees it rather than losing contact
     * with a broken door (decision D-T11).
     *
     * @return PORTUNUS_OK unless the FSM's own event queue cannot be
     *         created (PORTUNUS_ERR_QUEUE_CREATE).
     */
    portunus_err_t init();

    /**
     * @brief Start the FSM task and credential-polling sub-task.
     *
     * Must be called after init() and after the event bus is initialised.
     * Subscribes to relevant event bus events and starts the FreeRTOS
     * tasks.
     *
     * @return PORTUNUS_OK on success.
     */
    portunus_err_t start();

    /**
     * @brief Stop the FSM and leave the door locked (D-T9).
     *
     * Removes the event bus subscriptions, asks both tasks to stop and waits
     * for each to finish what it is doing, then, with nothing else driving
     * the strike, locks it (always: locking a locked strike is harmless).
     * The FSM can be started again. A no-op if not running.
     *
     * @return PORTUNUS_OK once stopped and locked (or if not running);
     *         PORTUNUS_ERR_LOCK_FAILED if the lock did not take (logged and
     *         shown as STRIKE_FAULT: the door may be unlocked and nothing is
     *         left to retry); PORTUNUS_ERR_TIMEOUT if a task did not stop in
     *         time. The strike is then not touched: the still-running FSM
     *         task keeps enforcing the hold timer. Call stop() again.
     */
    portunus_err_t stop();

    /** Stops the FSM (see stop()) and frees its queue. */
    ~SystemFSM();

    SystemFSM(const SystemFSM &)            = delete;
    SystemFSM &operator=(const SystemFSM &) = delete;

    /** @brief Get the current system state. */
    system_state_t state() const { return m_state; }

    /** @brief Get the current capability flags. */
    system_capabilities_t capabilities() const;

private:
    /* Component-tier test fixture needs access to process_event and check_unlock_timer
     * to inject events and advance the timer without starting FreeRTOS tasks. */
    friend class SystemFSMTestFixture;

    /* ── Injected dependencies ────────────────────────────────────────────── */
    ICredentialReader *m_reader;
    IAccessPoint      *m_access;
    IFeedback         *m_feedback;
    IClock            *m_clock;

    /* ── FSM state ────────────────────────────────────────────────────────── */
    system_state_t        m_state = SYSTEM_STATE_BOOT;
    /* Capability flags. has_reader is flipped by the credential poll task
     * (degraded / recovered) and has_network by the FSM task, while either
     * may be read from another task (capabilities()): atomic (F15). The
     * decision core gets a consistent snapshot through caps(). */
    std::atomic<bool> m_has_reader{false};
    /** init() brought the reader up. Unlike m_has_reader, degraded mode does
     *  not clear it, so a restart while degraded still polls (and probes). */
    bool              m_reader_inited = false;
    std::atomic<bool> m_has_access_point{false};
    std::atomic<bool> m_has_feedback{false};
    std::atomic<bool> m_has_network{false};
    system_capabilities_t caps() const;

    /* ── Unlock timer state ───────────────────────────────────────────────── */
    bool    m_strike_energized = false;
    int64_t m_unlock_deadline_ms = 0;  /**< esp_timer timestamp when hold expires */
    /** An early re-lock (door closed during the hold) failed: retry every
     *  poll until lock() succeeds, without waiting for the hold to expire. */
    bool    m_relock_retry = false;
    /** lock() has failed and not yet succeeded since: the door may be stuck
     *  unlocked, so STRIKE_FAULT is shown until it locks. */
    bool    m_lock_fault = false;

    /* ── Reed switch tracking ─────────────────────────────────────────────── */
    bool m_last_door_open = false;

    /* ── Pending access decisions ─────────────────────────────────────────── */
    /* Card reads awaiting a server decision.  A decision with nothing pending
     * (late, after the timeout already showed a deny) is ignored, so the door
     * never unlocks for a request the user was told failed. */
    uint8_t m_pending_decisions    = 0;
    int64_t m_decision_deadline_ms = 0;  /**< Clock time when the oldest wait expires */
    /* ── Lost events (D-T5) ───────────────────────────────────────────────── */
    /* Events lost because the FSM queue was full (bus dispatcher) or a card
     * read could not be published (poll task): atomic. The FSM task shows an
     * error when it grows. */
    std::atomic<uint32_t> m_dropped{0};
    uint32_t              m_dropped_seen = 0;   /**< FSM task only */
    queue_pressure_t      m_queue_pressure;     /**< FSM task only */

    /* ── FreeRTOS handles ─────────────────────────────────────────────────── */
    TaskHandle_t  m_fsm_task_handle  = nullptr;
    TaskHandle_t  m_poll_task_handle = nullptr;
    QueueHandle_t m_event_queue      = nullptr;  /**< Internal event queue */

    /* ── Cooperative shutdown (D-T9) ──────────────────────────────────────── */
    /* stop() sets the flag and wakes both tasks; each exits at the top of its
     * loop and gives its semaphore. */
    std::atomic<bool> m_stop_requested{false};
    SemaphoreHandle_t m_fsm_exited  = nullptr;
    SemaphoreHandle_t m_poll_exited = nullptr;
    bool              m_subscribed  = false;

    /* ── Task entry points ────────────────────────────────────────────────── */
    static void fsm_task_entry(void *arg);
    static void credential_poll_task_entry(void *arg);

    /* ── Internal methods ─────────────────────────────────────────────────── */
    void run();                                  /**< FSM main loop */
    bool receive_and_process(TickType_t timeout); /**< run() steps 1-2 */
    void check_dropped_events();
    void publish_credential_read(const credential_t &cred);
    void publish_status(portunus_event_id_t id);
    void poll_credential();                      /**< Credential polling loop */
    void process_event(const portunus_event_t &event);
    void poll_reed_switch();
    void check_unlock_timer();
    void retry_relock();
    void note_lock_result(bool locked);
    void start_unlock_timer();
    void cancel_unlock_timer();
    void check_decision_timeout();
    bool track_decision(const portunus_event_t &event);

    /* ── Event bus bridge ─────────────────────────────────────────────────── */
    static void on_event_bus_event(const portunus_event_t *event, void *ctx);
};
