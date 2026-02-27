/**
 * @file system_fsm.h
 * @brief System FSM — top-level decision maker for the Portunus access module.
 *
 * The FSM orchestrates all module interactions:
 *   - Initialises modules and records capability flags.
 *   - Owns the card-polling sub-task.
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

#include "system_states.h"
#include "event_types.h"
#include "i_credential_reader.h"
#include "i_access_point.h"
#include "i_feedback.h"

#include "freertos/FreeRTOS.h"
#include "freertos/task.h"
#include "freertos/queue.h"

/**
 * @brief System FSM — coordinates modules and manages system state.
 */
class SystemFSM {
public:
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
              IFeedback         *feedback);

    /**
     * @brief Initialise modules and set capability flags.
     *
     * Calls init() on each non-null module.  Sets has_reader,
     * has_access_point, has_feedback based on init results.
     * has_network is set from the WiFi manager's current state.
     *
     * @return PORTUNUS_OK if at least the access point initialised.
     *         Returns an error code only if a critical failure prevents
     *         the system from operating at all.
     */
    portunus_err_t init();

    /**
     * @brief Start the FSM task and card-polling sub-task.
     *
     * Must be called after init() and after the event bus is initialised.
     * Subscribes to relevant event bus events and starts the FreeRTOS
     * tasks.
     *
     * @return PORTUNUS_OK on success.
     */
    portunus_err_t start();

    /** @brief Get the current system state. */
    system_state_t state() const { return m_state; }

    /** @brief Get the current capability flags. */
    system_capabilities_t capabilities() const { return m_caps; }

private:
    /* ── Injected dependencies ────────────────────────────────────────────── */
    ICredentialReader *m_reader;
    IAccessPoint      *m_access;
    IFeedback         *m_feedback;

    /* ── FSM state ────────────────────────────────────────────────────────── */
    system_state_t        m_state = SYSTEM_STATE_BOOT;
    system_capabilities_t m_caps  = {};

    /* ── Unlock timer state ───────────────────────────────────────────────── */
    bool    m_strike_energized = false;
    int64_t m_unlock_deadline_ms = 0;  /**< esp_timer timestamp when hold expires */

    /* ── Reed switch tracking ─────────────────────────────────────────────── */
    bool m_last_door_open = false;

    /* ── FreeRTOS handles ─────────────────────────────────────────────────── */
    TaskHandle_t  m_fsm_task_handle  = nullptr;
    TaskHandle_t  m_poll_task_handle = nullptr;
    QueueHandle_t m_event_queue      = nullptr;  /**< Internal event queue */

    /* ── Task entry points ────────────────────────────────────────────────── */
    static void fsm_task_entry(void *arg);
    static void card_poll_task_entry(void *arg);

    /* ── Internal methods ─────────────────────────────────────────────────── */
    void run();                                  /**< FSM main loop */
    void poll_card();                            /**< Card polling loop */
    void process_event(const portunus_event_t &event);
    void poll_reed_switch();
    void check_unlock_timer();
    void start_unlock_timer();
    void cancel_unlock_timer();

    /* ── Event bus bridge ─────────────────────────────────────────────────── */
    static void on_event_bus_event(const portunus_event_t *event, void *ctx);
};
