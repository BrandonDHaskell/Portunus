/**
 * @file provisioning_fsm.h
 * @brief Provisioning console FSM — two-scan credential enrollment orchestrator.
 *
 * Implements the PROVISIONING_CONSOLE firmware variant FSM:
 *
 *   IDLE ──(any credential read)──► AWAITING_CREDENTIAL
 *                                        │  (30s timeout)
 *                                        ▼
 *                                   ──(timeout)──► IDLE
 *                                        │
 *                                   (new credential read)
 *                                        │
 *                                        ▼
 *                                      SENDING
 *                                   (EVENT_PROVISION_REQUEST published)
 *                                        │
 *                          ┌─────────────┴──────────────┐
 *                      SUCCESS                        FAILED
 *                          │                             │
 *                        IDLE ◄──────────────────────── IDLE
 *
 * Scan 1 (in IDLE): any credential read advances the FSM and starts the
 * mandatory timeout. The credential is discarded — it serves only as a
 * physical presence confirmation.
 *
 * Scan 2 (in AWAITING_CREDENTIAL): the new credential's raw UID bytes are
 * SHA-256 hashed on-device via mbedTLS. The hash, the pre-configured
 * operator UUID (Kconfig), and the default role ID are bundled into an
 * EVENT_PROVISION_REQUEST and published to the event bus. server_comm
 * encodes and sends the ProvisionCredentialRequest RPC.
 *
 * Architecture layer: Core / FSM (mirrors system_fsm architecture).
 */

#pragma once

#include "event_types.h"
#include "i_credential_reader.h"
#include "i_feedback.h"
#include "portunus_types.h"

#include "freertos/FreeRTOS.h"
#include "freertos/task.h"
#include "freertos/queue.h"

/**
 * @brief Provisioning FSM internal state.
 */
typedef enum {
    PROV_STATE_IDLE,                 /**< Awaiting operator scan */
    PROV_STATE_AWAITING_CREDENTIAL,  /**< Operator scan done, waiting for new credential */
    PROV_STATE_SENDING,              /**< Request sent, waiting for server response */
} prov_state_t;

/**
 * @brief Provisioning console FSM.
 *
 * Drop-in replacement for SystemFSM when built with
 * CONFIG_PORTUNUS_MODULE_TYPE_PROVISIONING_CONSOLE.
 * Requires a credential reader and feedback LED; no access point needed.
 */
class ProvisioningFSM {
public:
    /**
     * @param reader   Credential reader (must not be nullptr).
     * @param feedback Feedback module (nullptr disables LED indication).
     */
    ProvisioningFSM(ICredentialReader *reader, IFeedback *feedback);

    /**
     * @brief Initialise modules and set capability flags.
     * @return PORTUNUS_OK on success.
     */
    portunus_err_t init();

    /**
     * @brief Start the FSM task and credential polling sub-task.
     *
     * Must be called after init() and after event_bus_init().
     * @return PORTUNUS_OK on success.
     */
    portunus_err_t start();

    /** @brief Current provisioning FSM state. */
    prov_state_t state() const { return m_prov_state; }

private:
    /* ── Injected dependencies ────────────────────────────────────────────── */
    ICredentialReader *m_reader;
    IFeedback         *m_feedback;

    /* ── Capability flags ─────────────────────────────────────────────────── */
    bool m_has_reader   = false;
    bool m_has_feedback = false;
    bool m_has_network  = false;

    /* ── FSM state ────────────────────────────────────────────────────────── */
    prov_state_t m_prov_state        = PROV_STATE_IDLE;
    int64_t      m_timeout_deadline_ms = 0;

    /* ── FreeRTOS handles ─────────────────────────────────────────────────── */
    TaskHandle_t  m_fsm_task_handle  = nullptr;
    TaskHandle_t  m_poll_task_handle = nullptr;
    QueueHandle_t m_event_queue      = nullptr;

    /* ── Task entry points ────────────────────────────────────────────────── */
    static void fsm_task_entry(void *arg);
    static void credential_poll_task_entry(void *arg);

    /* ── Internal methods ─────────────────────────────────────────────────── */
    void run();
    void poll_credential();
    void process_event(const portunus_event_t &event);
    void handle_credential_read(const event_credential_read_t *cred);
    void handle_provision_result(const event_provision_result_t *result);
    void handle_timeout();
    void transition_to_idle();

    /* ── Event bus bridge ─────────────────────────────────────────────────── */
    static void on_event_bus_event(const portunus_event_t *event, void *ctx);
};
