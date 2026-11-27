/**
 * @file system_fsm.cpp
 * @brief System FSM implementation.
 *
 * The FSM runs a single FreeRTOS task (fsm_task) that:
 *   1. Waits for events on an internal queue (timeout = FSM_POLL_INTERVAL_MS).
 *   2. Processes any received event (access decisions, door state, etc.).
 *   3. Polls the reed switch for door state changes.
 *   4. Checks the unlock hold timer.
 *
 * Events arrive via an event bus bridge: the FSM subscribes to relevant
 * event bus event types, and the bridge callback copies each event into
 * the FSM's internal queue.  This decouples the FSM's processing from
 * the event bus dispatcher task.
 *
 * The credential-polling sub-task runs independently and publishes credential
 * events to the event bus (not directly to the FSM queue).  The FSM
 * receives those events via its event bus subscription like any other.
 *
 * Architecture layer: Core / FSM (see project plan §3.1, §5.2).
 */

#include "system_fsm.hpp"
#include "system_fsm_decide.hpp"
#include "reader_health.hpp"
#include "event_bus.hpp"
#include "timing_config.hpp"
#include "error_codes.hpp"
#include "credential_types.h"
#ifdef CONFIG_PORTUNUS_ENABLE_WIFI
#include "wifi_mgr.hpp"
#endif

#include "esp_log.h"
#include "freertos/FreeRTOS.h"
#include "freertos/task.h"
#include "freertos/queue.h"
#include "freertos/semphr.h"

#include <string.h>
#include <inttypes.h>

static const char *TAG = "fsm";

/* stop() waits this long for each task: the FSM task wakes at once from its
 * queue wait, and neither task blocks for longer than one event bus publish
 * or one reader transaction. */
#define FSM_STOP_TIMEOUT_MS  2000

/* ── Task configuration ───────────────────────────────────────────────────── */

static const int FSM_TASK_STACK             = 4096;
static const int FSM_TASK_PRIORITY          = 5;   /* Above card polling, same as dispatcher */
static const int POLL_TASK_STACK            = 4096;
static const int POLL_TASK_PRIORITY         = 4;   /* Between heartbeat (3) and FSM (5) */
static const int FSM_EVENT_QUEUE_LEN        = 8;
static const int READER_HW_ERROR_THRESHOLD  = 5;    /* hardware errors (not card errors) before degraded */
static const int READER_RECOVERY_INTERVAL_MS = 5000; /* ms between reconnection attempts when degraded */

/* ── Constructor ──────────────────────────────────────────────────────────── */

SystemFSM::SystemFSM(ICredentialReader *reader,
                     IAccessPoint      *access,
                     IFeedback         *feedback,
                     IClock            *clock)
    : m_reader(reader)
    , m_access(access)
    , m_feedback(feedback)
    , m_clock(clock)
{
}

/* ── init() ───────────────────────────────────────────────────────────────── */

portunus_err_t SystemFSM::init()
{
    ESP_LOGI(TAG, "Initialising system FSM");
    m_state = SYSTEM_STATE_INITIALIZING;

    /* ── Credential reader ──────────────────────────────────────────────── */
    if (m_reader != nullptr) {
        portunus_err_t err = m_reader->init();
        if (err == PORTUNUS_OK) {
            m_has_reader = true;
            ESP_LOGI(TAG, "Credential reader: OK");
        } else {
            m_has_reader = false;
            ESP_LOGW(TAG, "Credential reader init failed (0x%" PRIx32 ") — credential polling disabled", (uint32_t)err);
        }
    } else {
        m_has_reader = false;
        ESP_LOGW(TAG, "Credential reader: not present");
    }

    /* ── Access point ───────────────────────────────────────────────────── */
    if (m_access != nullptr) {
        portunus_err_t err = m_access->init();
        if (err == PORTUNUS_OK) {
            m_has_access_point = true;
            ESP_LOGI(TAG, "Access point: OK");
        } else {
            m_has_access_point = false;
            ESP_LOGW(TAG, "Access point init failed (0x%" PRIx32 ") — door control disabled", (uint32_t)err);
        }
    } else {
        m_has_access_point = false;
        ESP_LOGW(TAG, "Access point: not present");
    }

    /* ── Feedback ───────────────────────────────────────────────────────── */
    if (m_feedback != nullptr) {
        portunus_err_t err = m_feedback->init();
        if (err == PORTUNUS_OK) {
            m_has_feedback = true;
            ESP_LOGI(TAG, "Feedback: OK");
        } else {
            m_has_feedback = false;
            ESP_LOGW(TAG, "Feedback init failed (0x%" PRIx32 ") — LED disabled", (uint32_t)err);
        }
    } else {
        m_has_feedback = false;
        ESP_LOGW(TAG, "Feedback: not present");
    }

    /* ── Network ────────────────────────────────────────────────────────── */
#ifdef CONFIG_PORTUNUS_ENABLE_WIFI
    m_has_network = wifi_mgr_is_connected();
#else
    m_has_network = false;
#endif

    /* ── Initial reed switch state ──────────────────────────────────────── */
    if (m_has_access_point) {
        m_last_door_open = m_access->is_open();
        ESP_LOGI(TAG, "Initial door state: %s", m_last_door_open ? "OPEN" : "CLOSED");
    }

    /* ── Create internal event queue ────────────────────────────────────── */
    m_event_queue = xQueueCreate(FSM_EVENT_QUEUE_LEN, sizeof(portunus_event_t));
    if (m_event_queue == nullptr) {
        ESP_LOGE(TAG, "Failed to create FSM event queue");
        m_state = SYSTEM_STATE_ERROR;
        return PORTUNUS_ERR_QUEUE_CREATE;
    }

    /* Report capabilities */
    ESP_LOGI(TAG, "Capabilities: reader=%d access_point=%d feedback=%d network=%d",
             (int)m_has_reader.load(), (int)m_has_access_point.load(),
             (int)m_has_feedback.load(), (int)m_has_network.load());

    return PORTUNUS_OK;
}

/* ── start() ──────────────────────────────────────────────────────────────── */

portunus_err_t SystemFSM::start()
{
    ESP_LOGI(TAG, "Starting system FSM");

    if (m_fsm_task_handle != nullptr || m_poll_task_handle != nullptr) {
        return PORTUNUS_ERR_ALREADY_INIT;
    }
    if (m_fsm_exited == nullptr) {
        m_fsm_exited = xSemaphoreCreateBinary();
    }
    if (m_poll_exited == nullptr) {
        m_poll_exited = xSemaphoreCreateBinary();
    }
    if (m_event_queue == nullptr || m_fsm_exited == nullptr || m_poll_exited == nullptr) {
        m_state = SYSTEM_STATE_ERROR;
        return m_event_queue == nullptr ? PORTUNUS_ERR_NOT_INIT : PORTUNUS_ERR_TASK_CREATE;
    }
    m_stop_requested = false;

    /* ── Subscribe to event bus events ──────────────────────────────────── */
    /* A missing subscription would silently stop grants or card reads from
     * reaching the FSM, so treat any failure as fatal. */
    for (portunus_event_id_t id : kSubscribedEvents) {
        portunus_err_t err = event_bus_subscribe(id, on_event_bus_event, this);
        if (err != PORTUNUS_OK) {
            ESP_LOGE(TAG, "Subscribe to event 0x%04x failed (0x%" PRIx32 ")",
                     (unsigned)id, (uint32_t)err);
            for (portunus_event_id_t done : kSubscribedEvents) {
                if (done == id) {
                    break;
                }
                (void)event_bus_unsubscribe(done, on_event_bus_event, this);
            }
            m_state = SYSTEM_STATE_ERROR;
            return PORTUNUS_ERR_SUBSCRIBE;
        }
    }
    m_subscribed = true;

    /* ── Start FSM task ─────────────────────────────────────────────────── */
    BaseType_t ret = xTaskCreate(
        fsm_task_entry,
        "fsm",
        FSM_TASK_STACK,
        this,
        FSM_TASK_PRIORITY,
        &m_fsm_task_handle
    );
    if (ret != pdPASS) {
        ESP_LOGE(TAG, "Failed to create FSM task");
        m_fsm_task_handle = nullptr;
        (void)stop();
        m_state = SYSTEM_STATE_ERROR;
        return PORTUNUS_ERR_TASK_CREATE;
    }

    /* ── Start credential polling sub-task ──────────────────────────────── */
    if (m_has_reader) {
        ret = xTaskCreate(
            credential_poll_task_entry,
            "credential_poll",
            POLL_TASK_STACK,
            this,
            POLL_TASK_PRIORITY,
            &m_poll_task_handle
        );
        if (ret != pdPASS) {
            ESP_LOGE(TAG, "Failed to create credential polling task");
            m_poll_task_handle = nullptr;
            m_has_reader = false;
        } else {
            ESP_LOGI(TAG, "Credential polling task started (interval=%d ms)",
                     MFRC522_POLL_INTERVAL_MS);
        }
    }

    /* ── Transition to OPERATIONAL ──────────────────────────────────────── */
    m_state = SYSTEM_STATE_OPERATIONAL;

    /* Publish boot-complete event */
    portunus_event_t boot_event;
    memset(&boot_event, 0, sizeof(boot_event));
    boot_event.id = EVENT_SYSTEM_BOOT_COMPLETE;
    event_bus_publish(&boot_event);

    if (m_has_feedback) {
        m_feedback->indicate(feedback_type_t::SYSTEM_READY);
    }

    ESP_LOGI(TAG, "System FSM running — state=OPERATIONAL");
    return PORTUNUS_OK;
}

system_capabilities_t SystemFSM::caps() const
{
    system_capabilities_t c = {};
    c.has_reader       = m_has_reader.load();
    c.has_access_point = m_has_access_point.load();
    c.has_feedback     = m_has_feedback.load();
    c.has_network      = m_has_network.load();
    return c;
}

system_capabilities_t SystemFSM::capabilities() const
{
    return caps();
}

/* ── stop() ───────────────────────────────────────────────────────────────── */

portunus_err_t SystemFSM::stop()
{
    if (m_fsm_task_handle == nullptr && m_poll_task_handle == nullptr && !m_subscribed) {
        return PORTUNUS_OK;
    }

    /* No new events once these return (unsubscribe waits out a delivery). */
    if (m_subscribed) {
        for (portunus_event_id_t id : kSubscribedEvents) {
            (void)event_bus_unsubscribe(id, on_event_bus_event, this);
        }
        m_subscribed = false;
    }

    m_stop_requested = true;
    if (m_poll_task_handle != nullptr) {
        xTaskNotifyGive(m_poll_task_handle);
        if (xSemaphoreTake(m_poll_exited, pdMS_TO_TICKS(FSM_STOP_TIMEOUT_MS)) != pdTRUE) {
            ESP_LOGE(TAG, "Credential poll task did not stop within %d ms", FSM_STOP_TIMEOUT_MS);
            return PORTUNUS_ERR_TIMEOUT;
        }
        m_poll_task_handle = nullptr;
    }
    if (m_fsm_task_handle != nullptr) {
        const portunus_event_t wake = {};          /* EVENT_NONE: cut the queue wait short */
        (void)xQueueSendToFront(m_event_queue, &wake, 0);
        if (xSemaphoreTake(m_fsm_exited, pdMS_TO_TICKS(FSM_STOP_TIMEOUT_MS)) != pdTRUE) {
            /* The FSM task still owns the strike and its hold timer: leave it. */
            ESP_LOGE(TAG, "FSM task did not stop within %d ms", FSM_STOP_TIMEOUT_MS);
            return PORTUNUS_ERR_TIMEOUT;
        }
        m_fsm_task_handle = nullptr;
    }

    /* Nothing else drives the strike now. Fail secure: lock it. */
    portunus_err_t result = PORTUNUS_OK;
    if (m_has_access_point) {
        const portunus_err_t lock_err = m_access->lock();
        if (lock_err != PORTUNUS_OK) {
            ESP_LOGE(TAG, "Strike did not lock on stop (0x%" PRIx32 "): door may be unlocked",
                     (uint32_t)lock_err);
            m_lock_fault = true;
            if (m_has_feedback) {
                m_feedback->indicate(feedback_type_t::SYSTEM_ERROR);
            }
            result = PORTUNUS_ERR_LOCK_FAILED;
        } else {
            m_lock_fault = false;
        }
    }

    /* Ready for start(): nothing pending, no hold running. */
    (void)xQueueReset(m_event_queue);
    cancel_unlock_timer();
    m_pending_decisions    = 0;
    m_decision_deadline_ms = 0;
    m_state                = SYSTEM_STATE_INITIALIZING;

    ESP_LOGI(TAG, "System FSM stopped");
    return result;
}

SystemFSM::~SystemFSM()
{
    if (stop() == PORTUNUS_ERR_TIMEOUT) {
        /* A task still uses this object: freeing its queue would be worse. */
        ESP_LOGE(TAG, "SystemFSM destroyed while a task is still running");
        return;
    }
    if (m_event_queue != nullptr) {
        vQueueDelete(m_event_queue);
    }
    if (m_fsm_exited != nullptr) {
        vSemaphoreDelete(m_fsm_exited);
    }
    if (m_poll_exited != nullptr) {
        vSemaphoreDelete(m_poll_exited);
    }
}

/* ── Event bus bridge ─────────────────────────────────────────────────────── */

bool SystemFSM::receive_and_process(TickType_t timeout)
{
    portunus_event_t event;
    if (xQueueReceive(m_event_queue, &event, timeout) != pdTRUE) {
        return false;
    }
    switch (queue_pressure_sample(m_queue_pressure, uxQueueMessagesWaiting(m_event_queue),
                                  FSM_EVENT_QUEUE_LEN)) {
    case queue_pressure_event_t::HIGH:
        ESP_LOGW(TAG, "FSM event queue consistently near full (%d deep): events arriving faster "
                 "than they are handled", FSM_EVENT_QUEUE_LEN);
        break;
    case queue_pressure_event_t::RECOVERED:
        ESP_LOGI(TAG, "FSM event queue pressure cleared");
        break;
    default:
        break;
    }
    process_event(event);
    return true;
}

/* A lost card read means the server's decision will arrive unsolicited and be
 * ignored; a lost decision means the user waits for the decision timeout.
 * Either way the user must not be left guessing: show a comm error (D-T5). */
void SystemFSM::check_dropped_events()
{
    const uint32_t dropped = m_dropped.load();
    if (dropped == m_dropped_seen) {
        return;
    }
    ESP_LOGE(TAG, "%" PRIu32 " event(s) lost to a full queue (%" PRIu32 " since boot)",
             dropped - m_dropped_seen, dropped);
    m_dropped_seen = dropped;
    if (m_has_feedback) {
        m_feedback->indicate(feedback_type_t::SYSTEM_ERROR);
    }
}

void SystemFSM::publish_credential_read(const credential_t &cred)
{
    portunus_event_t event;
    memset(&event, 0, sizeof(event));
    event.id                                   = EVENT_CREDENTIAL_READ;
    event.payload.credential_read.credential   = cred;
    event.payload.credential_read.timestamp_ms = m_clock->now_ms();
    if (event_bus_publish(&event) != PORTUNUS_OK) {
        ESP_LOGE(TAG, "Card read not published (event bus full)");
        m_dropped++;
    }
}

void SystemFSM::on_event_bus_event(const portunus_event_t *event, void *ctx)
{
    auto *fsm = static_cast<SystemFSM *>(ctx);
    if (fsm->m_event_queue == nullptr) {
        return;
    }

    /*
     * Copy the event into the FSM's internal queue.  Use a short
     * timeout — if the queue is full we drop the event rather than
     * blocking the event bus dispatcher (which would stall all
     * other subscribers).
     */
    if (xQueueSend(fsm->m_event_queue, event, pdMS_TO_TICKS(10)) != pdTRUE) {
        ESP_LOGW(TAG, "FSM event queue full: dropped event 0x%04x", event->id);
        fsm->m_dropped++;
    }
}

/* ── Task entry points ────────────────────────────────────────────────────── */

void SystemFSM::fsm_task_entry(void *arg)
{
    auto *fsm = static_cast<SystemFSM *>(arg);
    fsm->run();
}

void SystemFSM::credential_poll_task_entry(void *arg)
{
    auto *fsm = static_cast<SystemFSM *>(arg);
    fsm->poll_credential();
}

/* ── FSM main loop ────────────────────────────────────────────────────────── */

void SystemFSM::run()
{
    const TickType_t poll_timeout = pdMS_TO_TICKS(FSM_POLL_INTERVAL_MS);

    ESP_LOGD(TAG, "FSM task running (poll_interval=%d ms)", FSM_POLL_INTERVAL_MS);

    while (!m_stop_requested) {
        /* 1-2. Wait for an event or timeout, and process it. */
        (void)receive_and_process(poll_timeout);

        /* 2b. Show any events lost to a full queue since the last check. */
        check_dropped_events();

        /* 3. Poll reed switch for door state changes. */
        if (m_has_access_point) {
            poll_reed_switch();
        }

        /* 3b. Retry a failed early re-lock every poll until it succeeds. */
        if (m_relock_retry) {
            retry_relock();
        }

        /* 4. Check unlock hold timer. */
        if (m_strike_energized) {
            check_unlock_timer();
        }

        /* 5. Give up on decisions the server never delivered. */
        check_decision_timeout();

        /* 6. Update network capability dynamically. */
#ifdef CONFIG_PORTUNUS_ENABLE_WIFI
        m_has_network = wifi_mgr_is_connected();
#endif
    }

    xSemaphoreGive(m_fsm_exited);
    vTaskDelete(nullptr);
}

/* ── Event processing ─────────────────────────────────────────────────────── */

/**
 * @brief Update pending-decision bookkeeping for @p event.
 *
 * @return false if the event is an access decision with no card read waiting
 *         for it; the caller must then ignore the event entirely.
 */
bool SystemFSM::track_decision(const portunus_event_t &event)
{
    switch (event.id) {
    case EVENT_CREDENTIAL_READ:
        if (m_pending_decisions < UINT8_MAX) {
            m_pending_decisions++;
        }
        m_decision_deadline_ms = m_clock->now_ms() + ACCESS_DECISION_TIMEOUT_MS;
        return true;

    case EVENT_ACCESS_GRANTED:
    case EVENT_ACCESS_DENIED:
        if (m_pending_decisions == 0) {
            ESP_LOGW(TAG, "Ignoring %s with no pending request (late or unsolicited): credential=%s",
                     event.id == EVENT_ACCESS_GRANTED ? "GRANT" : "DENY",
                     event.payload.access_decision.credential_id);
            return false;
        }
        if (--m_pending_decisions == 0) {
            m_decision_deadline_ms = 0;
        }
        return true;

    default:
        return true;
    }
}

void SystemFSM::check_decision_timeout()
{
    if (m_pending_decisions == 0 || m_clock->now_ms() < m_decision_deadline_ms) {
        return;
    }

    ESP_LOGW(TAG, "No access decision within %d ms (%u pending); showing deny",
             ACCESS_DECISION_TIMEOUT_MS, (unsigned)m_pending_decisions);
    m_pending_decisions    = 0;
    m_decision_deadline_ms = 0;

    if (m_has_feedback) {
        m_feedback->indicate(feedback_type_t::ACCESS_DENIED);
    }
}

void SystemFSM::process_event(const portunus_event_t &event)
{
    if (!track_decision(event)) {
        return;
    }

    const fsm_actions_t actions = decide_system_event(caps(), event);

    /* Audit logging — preserved verbatim from the original handlers. */
    switch (event.id) {
    case EVENT_CREDENTIAL_READ: {
        const event_credential_read_t *cred = &event.payload.credential_read;
        char log_id[CREDENTIAL_LOG_ID_LEN];
        credential_uid_to_log_id(&cred->credential, log_id, sizeof(log_id));
        ESP_LOGI(TAG, "Credential read — id=%s", log_id);
        if (!m_has_network) {
            ESP_LOGW(TAG, "Network unavailable — credential logged locally only");
        }
        break;
    }
    case EVENT_ACCESS_GRANTED: {
        const event_access_decision_t *ad = &event.payload.access_decision;
        ESP_LOGI(TAG, "ACCESS GRANTED — credential=%s reason=%s",
                 ad->credential_id, ad->reason);
        if (!m_has_access_point) {
            ESP_LOGW(TAG, "Access granted but no access point: cannot unlock, showing error");
        }
        break;
    }
    case EVENT_ACCESS_DENIED: {
        const event_access_decision_t *ad = &event.payload.access_decision;
        ESP_LOGW(TAG, "ACCESS DENIED — credential=%s reason=%s known=%d",
                 ad->credential_id, ad->reason, ad->known);
        break;
    }
    default:
        ESP_LOGD(TAG, "Unhandled event: 0x%04x", event.id);
        break;
    }

    /* Door command: start the hold timer only once the unlock signal is out. */
    bool unlock_failed = false;
    if (actions.door == door_cmd_t::UNLOCK_AND_HOLD) {
        portunus_err_t err = m_access->unlock();
        if (err == PORTUNUS_OK) {
            start_unlock_timer();
            ESP_LOGI(TAG, "Strike energized, hold timer started (%d ms)", UNLOCK_HOLD_MS);
        } else {
            ESP_LOGE(TAG, "Unlock signal not sent (0x%" PRIx32 "): showing error, not a grant",
                     (uint32_t)err);
            unlock_failed = true;
        }
    } else if (actions.door == door_cmd_t::LOCK) {
        (void)m_access->lock();
    }

    /* Feedback: the decided indications in order, or the decided failure
     * indication if the unlock signal could not be sent. */
    if (m_has_feedback) {
        if (unlock_failed) {
            m_feedback->indicate(actions.feedback_if_unlock_fails);
        } else {
            for (uint8_t i = 0; i < actions.feedback_count; i++) {
                m_feedback->indicate(actions.feedback[i]);
            }
        }
    }
}

/* ── Reed switch polling ──────────────────────────────────────────────────── */

void SystemFSM::poll_reed_switch()
{
    bool door_open = m_access->is_open();

    if (door_open != m_last_door_open) {
        m_last_door_open = door_open;

        /* Publish door state change event. */
        portunus_event_t event;
        memset(&event, 0, sizeof(event));

        if (door_open) {
            event.id = EVENT_DOOR_OPENED;
            event.payload.door_opened.timestamp_ms = m_clock->now_ms();
            ESP_LOGI(TAG, "Door OPENED");
        } else {
            event.id = EVENT_DOOR_CLOSED;
            event.payload.door_closed.timestamp_ms = m_clock->now_ms();
            ESP_LOGI(TAG, "Door CLOSED");

            /*
             * Early re-lock: if the strike is energized and the door
             * has been opened and then closed, re-lock immediately
             * rather than waiting for the hold timer to expire.
             */
            if (m_strike_energized) {
                ESP_LOGI(TAG, "Door closed during unlock hold — re-locking early");
                portunus_err_t lock_err = m_access->lock();
                if (lock_err != PORTUNUS_OK) {
                    ESP_LOGE(TAG, "Early re-lock failed (0x%" PRIx32 "); retrying every poll",
                             (uint32_t)lock_err);
                    /* The door is shut but may still be unlocked: retry on every
                     * poll (retry_relock) rather than waiting out the hold. */
                    m_relock_retry = true;
                    note_lock_result(false);
                } else {
                    cancel_unlock_timer();
                    note_lock_result(true);
                }
            }
        }

        event_bus_publish(&event);
    }
}

/* ── Unlock timer management ──────────────────────────────────────────────── */

void SystemFSM::start_unlock_timer()
{
    m_strike_energized   = true;
    m_unlock_deadline_ms = m_clock->now_ms() + UNLOCK_HOLD_MS;
    m_relock_retry       = false;   /* a new grant unlocks on purpose */
}

void SystemFSM::cancel_unlock_timer()
{
    m_strike_energized   = false;
    m_unlock_deadline_ms = 0;
    m_relock_retry       = false;
}

void SystemFSM::retry_relock()
{
    if (!m_strike_energized) {
        m_relock_retry = false;
        return;
    }
    if (m_access->lock() == PORTUNUS_OK) {
        ESP_LOGI(TAG, "Re-lock retry succeeded");
        cancel_unlock_timer();       /* the hold was cut short: no UNLOCK_TIMEOUT */
        note_lock_result(true);
    }
}

/* lock() reports only that the lock signal was sent and the pin read back
 * locked; there is no strike sensor. A failure means the door may be stuck
 * unlocked: show it until a retry succeeds. */
void SystemFSM::note_lock_result(bool locked)
{
    if (!locked && !m_lock_fault) {
        m_lock_fault = true;
        ESP_LOGE(TAG, "Strike did not lock; retrying, door may be unlocked");
        if (m_has_feedback) {
            m_feedback->indicate(feedback_type_t::SYSTEM_ERROR);
        }
    } else if (locked && m_lock_fault) {
        m_lock_fault = false;
        ESP_LOGI(TAG, "Strike locked after retrying");
        if (m_has_feedback) {
            m_feedback->indicate(feedback_type_t::SYSTEM_READY);
        }
    }
}

void SystemFSM::check_unlock_timer()
{
    if (m_clock->now_ms() >= m_unlock_deadline_ms) {
        ESP_LOGI(TAG, "Unlock hold timer expired, re-locking");

        if (m_has_access_point) {
            portunus_err_t lock_err = m_access->lock();
            if (lock_err != PORTUNUS_OK) {
                ESP_LOGE(TAG, "Re-lock after timer expiry failed (0x%" PRIx32 "), will retry",
                         (uint32_t)lock_err);
                note_lock_result(false);
                return; /* Leave timer expired so next tick retries. */
            }
            note_lock_result(true);
        }
        cancel_unlock_timer();

        /* Publish timeout event for audit trail. */
        portunus_event_t event;
        memset(&event, 0, sizeof(event));
        event.id = EVENT_FSM_UNLOCK_TIMEOUT;
        event_bus_publish(&event);
    }
}

/* ── Credential polling sub-task ──────────────────────────────────────────── */

void SystemFSM::poll_credential()
{
    static const reader_health_cfg_t cfg = {
        .error_threshold      = READER_HW_ERROR_THRESHOLD,
        .poll_interval_ms     = MFRC522_POLL_INTERVAL_MS,
        .reread_delay_ms      = CARD_REREAD_DELAY_MS,
        .recovery_interval_ms = READER_RECOVERY_INTERVAL_MS,
    };
    reader_health_t health;

    while (!m_stop_requested) {
        reader_step_t  step;
        credential_t   cred;
        portunus_err_t err = PORTUNUS_OK;

        if (health.degraded) {
            /* Recovery mode: chip absent, probe for reconnection. */
            step = reader_health_on_probe(health, m_reader->init() == PORTUNUS_OK, cfg);
            if (step.event == reader_health_event_t::NONE) {
                ESP_LOGD(TAG, "Credential reader still absent; retrying in %d ms",
                         READER_RECOVERY_INTERVAL_MS);
            }
        } else {
            err = m_reader->read(&cred);
            const reader_read_result_t result = reader_classify(err);
            if (result == reader_read_result_t::CARD_ERROR) {
                ESP_LOGD(TAG, "Card read failed (0x%" PRIx32 "); not a reader fault",
                         (uint32_t)err);
            }
            step = reader_health_on_read(health, result, cfg);
        }

        if (step.publish_credential) {
            publish_credential_read(cred);
        }
        if (step.halt) {
            m_reader->halt();
        }

        if (step.event != reader_health_event_t::NONE) {
            portunus_event_t health_event;
            memset(&health_event, 0, sizeof(health_event));
            if (step.event == reader_health_event_t::READ_ERROR) {
                ESP_LOGW(TAG, "Credential reader hardware fault after %d errors "
                         "(last err=0x%x); entering degraded mode",
                         READER_HW_ERROR_THRESHOLD, err);
                m_has_reader = false;
                health_event.id   = EVENT_CREDENTIAL_READ_ERROR;
            } else {
                ESP_LOGI(TAG, "Credential reader recovered; resuming polling");
                m_has_reader = true;
                health_event.id   = EVENT_CREDENTIAL_READER_RECOVERED;
            }
            event_bus_publish(&health_event);
        }

        if (step.delay_ms > 0) {
            /* A delay stop() can cut short (the recovery probe waits 5 s). */
            (void)ulTaskNotifyTake(pdTRUE, pdMS_TO_TICKS(step.delay_ms));
        }
    }

    xSemaphoreGive(m_poll_exited);
    vTaskDelete(nullptr);
}
