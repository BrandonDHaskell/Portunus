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
 * The card-polling sub-task runs independently and publishes credential
 * events to the event bus (not directly to the FSM queue).  The FSM
 * receives those events via its event bus subscription like any other.
 *
 * Architecture layer: Core / FSM (see project plan §3.1, §5.2).
 */

#include "system_fsm.h"
#include "event_bus.h"
#include "timing_config.h"
#include "error_codes.h"
#include "credential_types.h"
#ifdef CONFIG_PORTUNUS_ENABLE_WIFI
#include "wifi_mgr.h"
#endif

#include "esp_log.h"
#include "esp_timer.h"
#include "freertos/FreeRTOS.h"
#include "freertos/task.h"
#include "freertos/queue.h"

#include <string.h>
#include <inttypes.h>

static const char *TAG = "fsm";

/* ── Task configuration ───────────────────────────────────────────────────── */

static const int FSM_TASK_STACK      = 4096;
static const int FSM_TASK_PRIORITY   = 5;   /* Above card polling, same as dispatcher */
static const int POLL_TASK_STACK     = 4096;
static const int POLL_TASK_PRIORITY  = 4;   /* Between heartbeat (3) and FSM (5) */
static const int FSM_EVENT_QUEUE_LEN = 8;

/* ── Helper: current time in milliseconds ─────────────────────────────────── */

static inline int64_t now_ms(void)
{
    return esp_timer_get_time() / 1000;
}

/* ── Constructor ──────────────────────────────────────────────────────────── */

SystemFSM::SystemFSM(ICredentialReader *reader,
                     IAccessPoint      *access,
                     IFeedback         *feedback)
    : m_reader(reader)
    , m_access(access)
    , m_feedback(feedback)
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
            m_caps.has_reader = true;
            ESP_LOGI(TAG, "Credential reader: OK");
        } else {
            m_caps.has_reader = false;
            ESP_LOGW(TAG, "Credential reader init failed (0x%04x) — card polling disabled", err);
        }
    } else {
        m_caps.has_reader = false;
        ESP_LOGW(TAG, "Credential reader: not present");
    }

    /* ── Access point ───────────────────────────────────────────────────── */
    if (m_access != nullptr) {
        portunus_err_t err = m_access->init();
        if (err == PORTUNUS_OK) {
            m_caps.has_access_point = true;
            ESP_LOGI(TAG, "Access point: OK");
        } else {
            m_caps.has_access_point = false;
            ESP_LOGW(TAG, "Access point init failed (0x%04x) — door control disabled", err);
        }
    } else {
        m_caps.has_access_point = false;
        ESP_LOGW(TAG, "Access point: not present");
    }

    /* ── Feedback ───────────────────────────────────────────────────────── */
    if (m_feedback != nullptr) {
        portunus_err_t err = m_feedback->init();
        if (err == PORTUNUS_OK) {
            m_caps.has_feedback = true;
            ESP_LOGI(TAG, "Feedback: OK");
        } else {
            m_caps.has_feedback = false;
            ESP_LOGW(TAG, "Feedback init failed (0x%04x) — LED disabled", err);
        }
    } else {
        m_caps.has_feedback = false;
        ESP_LOGW(TAG, "Feedback: not present");
    }

    /* ── Network ────────────────────────────────────────────────────────── */
#ifdef CONFIG_PORTUNUS_ENABLE_WIFI
    m_caps.has_network = wifi_mgr_is_connected();
#else
    m_caps.has_network = false;
#endif

    /* ── Initial reed switch state ──────────────────────────────────────── */
    if (m_caps.has_access_point) {
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
             m_caps.has_reader, m_caps.has_access_point,
             m_caps.has_feedback, m_caps.has_network);

    return PORTUNUS_OK;
}

/* ── start() ──────────────────────────────────────────────────────────────── */

portunus_err_t SystemFSM::start()
{
    ESP_LOGI(TAG, "Starting system FSM");

    /* ── Subscribe to event bus events ──────────────────────────────────── */
    event_bus_subscribe(EVENT_CREDENTIAL_READ, on_event_bus_event, this);
    event_bus_subscribe(EVENT_ACCESS_GRANTED,  on_event_bus_event, this);
    event_bus_subscribe(EVENT_ACCESS_DENIED,   on_event_bus_event, this);

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
        m_state = SYSTEM_STATE_ERROR;
        return PORTUNUS_ERR_TASK_CREATE;
    }

    /* ── Start card polling sub-task ────────────────────────────────────── */
    if (m_caps.has_reader) {
        ret = xTaskCreate(
            card_poll_task_entry,
            "card_poll",
            POLL_TASK_STACK,
            this,
            POLL_TASK_PRIORITY,
            &m_poll_task_handle
        );
        if (ret != pdPASS) {
            ESP_LOGE(TAG, "Failed to create card polling task");
            m_caps.has_reader = false;
        } else {
            ESP_LOGI(TAG, "Card polling task started (interval=%d ms)",
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

    if (m_caps.has_feedback) {
        m_feedback->indicate(feedback_type_t::SYSTEM_READY);
    }

    ESP_LOGI(TAG, "System FSM running — state=OPERATIONAL");
    return PORTUNUS_OK;
}

/* ── Event bus bridge ─────────────────────────────────────────────────────── */

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
        ESP_LOGW(TAG, "FSM event queue full — dropped event 0x%04x", event->id);
    }
}

/* ── Task entry points ────────────────────────────────────────────────────── */

void SystemFSM::fsm_task_entry(void *arg)
{
    auto *fsm = static_cast<SystemFSM *>(arg);
    fsm->run();
}

void SystemFSM::card_poll_task_entry(void *arg)
{
    auto *fsm = static_cast<SystemFSM *>(arg);
    fsm->poll_card();
}

/* ── FSM main loop ────────────────────────────────────────────────────────── */

void SystemFSM::run()
{
    portunus_event_t event;
    const TickType_t poll_timeout = pdMS_TO_TICKS(FSM_POLL_INTERVAL_MS);

    ESP_LOGD(TAG, "FSM task running (poll_interval=%d ms)", FSM_POLL_INTERVAL_MS);

    for (;;) {
        /* 1. Wait for an event or timeout. */
        if (xQueueReceive(m_event_queue, &event, poll_timeout) == pdTRUE) {
            /* 2. Process received event. */
            process_event(event);
        }

        /* 3. Poll reed switch for door state changes. */
        if (m_caps.has_access_point) {
            poll_reed_switch();
        }

        /* 4. Check unlock hold timer. */
        if (m_strike_energized) {
            check_unlock_timer();
        }

        /* 5. Update network capability dynamically. */
#ifdef CONFIG_PORTUNUS_ENABLE_WIFI
        m_caps.has_network = wifi_mgr_is_connected();
#endif
    }
}

/* ── Event processing ─────────────────────────────────────────────────────── */

void SystemFSM::process_event(const portunus_event_t &event)
{
    switch (event.id) {

    case EVENT_CREDENTIAL_READ: {
        const event_credential_read_t *cred = &event.payload.credential_read;
        char uid_str[CREDENTIAL_UID_HEX_STR_LEN];
        credential_uid_to_hex(&cred->credential, uid_str, sizeof(uid_str));

        ESP_LOGI(TAG, "Credential read — UID: %s (len=%d)",
                 uid_str, cred->credential.uid_len);

        /* Show intermediate feedback while waiting for server decision. */
        if (m_caps.has_feedback) {
            m_feedback->indicate(feedback_type_t::CARD_READ);
        }

        /*
         * The server_comm component is also subscribed to
         * EVENT_CREDENTIAL_READ and sends the access request to the
         * server.  The FSM waits for EVENT_ACCESS_GRANTED or
         * EVENT_ACCESS_DENIED — no action needed here beyond feedback.
         *
         * If there's no network, the credential was still logged by the
         * event bus and server_comm will handle the offline case.
         */
        if (!m_caps.has_network) {
            ESP_LOGW(TAG, "Network unavailable — credential logged locally only");
            if (m_caps.has_feedback) {
                m_feedback->indicate(feedback_type_t::SYSTEM_ERROR);
            }
        }
        break;
    }

    case EVENT_ACCESS_GRANTED: {
        const event_access_decision_t *ad = &event.payload.access_decision;
        ESP_LOGI(TAG, "ACCESS GRANTED — card=%s reason=%s", ad->card_id, ad->reason);

        if (m_caps.has_access_point) {
            portunus_err_t err = m_access->unlock();
            if (err == PORTUNUS_OK) {
                start_unlock_timer();
                ESP_LOGI(TAG, "Strike energized — hold timer started (%d ms)",
                         UNLOCK_HOLD_MS);
            } else {
                ESP_LOGE(TAG, "Failed to unlock: 0x%04x", err);
            }
        } else {
            ESP_LOGW(TAG, "Access granted but no access point — cannot unlock");
        }

        if (m_caps.has_feedback) {
            m_feedback->indicate(feedback_type_t::ACCESS_GRANTED);
        }
        break;
    }

    case EVENT_ACCESS_DENIED: {
        const event_access_decision_t *ad = &event.payload.access_decision;
        ESP_LOGW(TAG, "ACCESS DENIED — card=%s reason=%s known=%d",
                 ad->card_id, ad->reason, ad->known);

        if (m_caps.has_feedback) {
            m_feedback->indicate(feedback_type_t::ACCESS_DENIED);
        }
        /* No hardware action on deny. */
        break;
    }

    default:
        ESP_LOGD(TAG, "Unhandled event: 0x%04x", event.id);
        break;
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
            event.payload.door_opened.timestamp_ms = now_ms();
            ESP_LOGI(TAG, "Door OPENED");
        } else {
            event.id = EVENT_DOOR_CLOSED;
            event.payload.door_closed.timestamp_ms = now_ms();
            ESP_LOGI(TAG, "Door CLOSED");

            /*
             * Early re-lock: if the strike is energized and the door
             * has been opened and then closed, re-lock immediately
             * rather than waiting for the hold timer to expire.
             */
            if (m_strike_energized) {
                ESP_LOGI(TAG, "Door closed during unlock hold — re-locking early");
                m_access->lock();
                cancel_unlock_timer();
            }
        }

        event_bus_publish(&event);
    }
}

/* ── Unlock timer management ──────────────────────────────────────────────── */

void SystemFSM::start_unlock_timer()
{
    m_strike_energized   = true;
    m_unlock_deadline_ms = now_ms() + UNLOCK_HOLD_MS;
}

void SystemFSM::cancel_unlock_timer()
{
    m_strike_energized   = false;
    m_unlock_deadline_ms = 0;
}

void SystemFSM::check_unlock_timer()
{
    if (now_ms() >= m_unlock_deadline_ms) {
        ESP_LOGI(TAG, "Unlock hold timer expired — re-locking");

        if (m_caps.has_access_point) {
            m_access->lock();
        }
        cancel_unlock_timer();

        /* Publish timeout event for audit trail. */
        portunus_event_t event;
        memset(&event, 0, sizeof(event));
        event.id = EVENT_FSM_UNLOCK_TIMEOUT;
        event_bus_publish(&event);
    }
}

/* ── Card polling sub-task ────────────────────────────────────────────────── */

void SystemFSM::poll_card()
{
    const TickType_t poll_interval  = pdMS_TO_TICKS(MFRC522_POLL_INTERVAL_MS);
    const TickType_t reread_delay   = pdMS_TO_TICKS(CARD_REREAD_DELAY_MS);

    for (;;) {
        credential_t cred;
        portunus_err_t err = m_reader->read(&cred);

        if (err == PORTUNUS_OK) {
            /* Build and publish credential event via event bus. */
            portunus_event_t event;
            memset(&event, 0, sizeof(event));
            event.id = EVENT_CREDENTIAL_READ;
            event.payload.credential_read.credential   = cred;
            event.payload.credential_read.timestamp_ms = now_ms();

            event_bus_publish(&event);

            /* Halt the card to prevent re-reads while held against reader. */
            m_reader->halt();

            vTaskDelay(reread_delay);
            continue;
        }
        /* PORTUNUS_ERR_NO_CARD is expected — no card present, continue. */

        vTaskDelay(poll_interval);
    }
}
