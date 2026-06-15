/**
 * @file provisioning_fsm.cpp
 * @brief PEU (Provisioning & Enrollment Unit) capture-only FSM.
 *
 * Capture path:
 *   Armed → card tap → CAPTURE_SEND → server parks as pending_authorization.
 *   No operator badge involved; admin approves later via the web UI.
 *
 * The arm button cycles: IDLE → ARMED → IDLE (press again to cancel).
 * Timeouts auto-cancel any armed state and return to Idle (or Armed if
 * no arm button is present).
 */

#include "provisioning_fsm.hpp"
#include "event_bus.hpp"
#include "error_codes.hpp"
#include "credential_types.h"
#include "sdkconfig.h"

#ifdef CONFIG_PORTUNUS_ENABLE_WIFI
#include "wifi_mgr.hpp"
#endif

#include "esp_log.h"
#include "esp_timer.h"
#include "freertos/FreeRTOS.h"
#include "freertos/task.h"
#include "freertos/queue.h"

#include <string.h>
#include <inttypes.h>

static const char *TAG = "peu_fsm";

/* ── Task configuration ───────────────────────────────────────────────────── */

static const int FSM_TASK_STACK      = 4096;
static const int FSM_TASK_PRIORITY   = 5;
static const int POLL_TASK_STACK     = 4096;
static const int POLL_TASK_PRIORITY  = 4;
static const int FSM_EVENT_QUEUE_LEN = 8;
static const int FSM_POLL_INTERVAL_MS = 100;

static const int MFRC522_POLL_INTERVAL_MS = 250;
static const int CARD_REREAD_DELAY_MS     = 1000;

/* ── Helpers ──────────────────────────────────────────────────────────────── */

static inline int64_t now_ms()
{
    return esp_timer_get_time() / 1000;
}

/* ── Constructor ──────────────────────────────────────────────────────────── */

ProvisioningFSM::ProvisioningFSM(ICredentialReader *reader,
                                 IFeedback         *feedback,
                                 IArm              *arm)
    : m_reader(reader)
    , m_feedback(feedback)
    , m_arm(arm)
{}

/* ── init() ───────────────────────────────────────────────────────────────── */

portunus_err_t ProvisioningFSM::init()
{
    ESP_LOGI(TAG, "Initialising PEU FSM");

    if (m_reader != nullptr) {
        portunus_err_t err = m_reader->init();
        if (err == PORTUNUS_OK) {
            m_has_reader = true;
            ESP_LOGI(TAG, "Credential reader: OK");
        } else {
            ESP_LOGE(TAG, "Credential reader init failed (0x%" PRIx32 ")", (uint32_t)err);
            return err; // reader is mandatory for PEU
        }
    } else {
        ESP_LOGE(TAG, "Credential reader not present — PEU requires a reader");
        return PORTUNUS_FAIL;
    }

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

    if (m_arm != nullptr) {
        portunus_err_t err = m_arm->init();
        if (err == PORTUNUS_OK) {
            m_has_arm = true;
            ESP_LOGI(TAG, "Arm button: OK");
        } else {
            m_has_arm = false;
            ESP_LOGW(TAG, "Arm button init failed (0x%" PRIx32 ") — always-armed mode",
                     (uint32_t)err);
        }
    } else {
        m_has_arm = false;
        ESP_LOGW(TAG, "Arm button: not present — always-armed mode");
    }

#ifdef CONFIG_PORTUNUS_ENABLE_WIFI
    m_has_network = wifi_mgr_is_connected();
#endif

    m_event_queue = xQueueCreate(FSM_EVENT_QUEUE_LEN, sizeof(portunus_event_t));
    if (m_event_queue == nullptr) {
        ESP_LOGE(TAG, "Failed to create FSM event queue");
        return PORTUNUS_ERR_QUEUE_CREATE;
    }

    ESP_LOGI(TAG, "Capabilities: reader=%d feedback=%d arm=%d network=%d",
             m_has_reader, m_has_feedback, m_has_arm, m_has_network);

    return PORTUNUS_OK;
}

/* ── start() ──────────────────────────────────────────────────────────────── */

portunus_err_t ProvisioningFSM::start()
{
    ESP_LOGI(TAG, "Starting PEU FSM");

    event_bus_subscribe(EVENT_PROVISION_SUCCESS, on_event_bus_event, this);
    event_bus_subscribe(EVENT_PROVISION_FAILED,  on_event_bus_event, this);
    event_bus_subscribe(EVENT_CREDENTIAL_READ,   on_event_bus_event, this);
    event_bus_subscribe(EVENT_ARM_REQUESTED,     on_event_bus_event, this);

    BaseType_t ret = xTaskCreate(
        fsm_task_entry, "peu_fsm",
        FSM_TASK_STACK, this, FSM_TASK_PRIORITY, &m_fsm_task_handle);
    if (ret != pdPASS) {
        ESP_LOGE(TAG, "Failed to create FSM task");
        return PORTUNUS_ERR_TASK_CREATE;
    }

    ret = xTaskCreate(
        poll_task_entry, "peu_poll",
        POLL_TASK_STACK, this, POLL_TASK_PRIORITY, &m_poll_task_handle);
    if (ret != pdPASS) {
        ESP_LOGE(TAG, "Failed to create poll task");
        return PORTUNUS_ERR_TASK_CREATE;
    }

    // Initial state: Idle if arm button present, Armed otherwise.
    if (m_has_arm) {
        enter_idle();
    } else {
        enter_armed();
    }

    ESP_LOGI(TAG, "PEU FSM running — scan_timeout=%dms  arm_timeout=%dms",
             CONFIG_PORTUNUS_PROVISION_TIMEOUT_MS,
             CONFIG_PORTUNUS_ARM_TIMEOUT_MS);

    return PORTUNUS_OK;
}

/* ── Event bus bridge ─────────────────────────────────────────────────────── */

void ProvisioningFSM::on_event_bus_event(const portunus_event_t *event, void *ctx)
{
    auto *fsm = static_cast<ProvisioningFSM *>(ctx);
    if (fsm->m_event_queue == nullptr) { return; }
    if (xQueueSend(fsm->m_event_queue, event, pdMS_TO_TICKS(10)) != pdTRUE) {
        ESP_LOGW(TAG, "FSM queue full — dropped event 0x%04x", event->id);
    }
}

/* ── Task entry points ────────────────────────────────────────────────────── */

void ProvisioningFSM::fsm_task_entry(void *arg)
{
    static_cast<ProvisioningFSM *>(arg)->run();
}

void ProvisioningFSM::poll_task_entry(void *arg)
{
    static_cast<ProvisioningFSM *>(arg)->poll();
}

/* ── Poll task: arm button + credential reader ────────────────────────────── */

void ProvisioningFSM::poll()
{
    const TickType_t poll_interval = pdMS_TO_TICKS(FSM_POLL_INTERVAL_MS);
    const TickType_t reread_delay  = pdMS_TO_TICKS(CARD_REREAD_DELAY_MS);

    for (;;) {
        // Always poll the arm button — it needs to fire from SLEEP to wake up.
        if (m_has_arm && m_arm->poll_arm()) {
            portunus_event_t evt;
            memset(&evt, 0, sizeof(evt));
            evt.id = EVENT_ARM_REQUESTED;
            event_bus_publish(&evt);
        }

        // Poll the credential reader only when armed and waiting for a card.
        if (m_state == PEU_STATE_ARMED) {
            credential_t cred;
            if (m_reader->read(&cred) == PORTUNUS_OK) {
                portunus_event_t evt;
                memset(&evt, 0, sizeof(evt));
                evt.id                               = EVENT_CREDENTIAL_READ;
                evt.payload.credential_read.credential   = cred;
                evt.payload.credential_read.timestamp_ms = now_ms();
                event_bus_publish(&evt);

                m_reader->halt();
                vTaskDelay(reread_delay);
                continue;
            }
        }

        vTaskDelay(poll_interval);
    }
}

/* ── FSM main loop ────────────────────────────────────────────────────────── */

void ProvisioningFSM::run()
{
    portunus_event_t event;
    const TickType_t timeout = pdMS_TO_TICKS(FSM_POLL_INTERVAL_MS);

    for (;;) {
        if (xQueueReceive(m_event_queue, &event, timeout) == pdTRUE) {
            process_event(event);
        }

        check_timeout();

#ifdef CONFIG_PORTUNUS_ENABLE_WIFI
        m_has_network = wifi_mgr_is_connected();
#endif
    }
}

/* ── Event processing ─────────────────────────────────────────────────────── */

void ProvisioningFSM::process_event(const portunus_event_t &event)
{
    switch (event.id) {
    case EVENT_ARM_REQUESTED:
        handle_arm_requested();
        break;
    case EVENT_CREDENTIAL_READ:
        handle_credential_read(&event.payload.credential_read);
        break;
    case EVENT_PROVISION_SUCCESS:
    case EVENT_PROVISION_FAILED:
        handle_provision_result(&event.payload.provision_result);
        break;
    default:
        break;
    }
}

void ProvisioningFSM::check_timeout()
{
    if (m_deadline_ms == 0 || now_ms() < m_deadline_ms) {
        return;
    }
    m_deadline_ms = 0;

    switch (m_state) {
    case PEU_STATE_IDLE:
        ESP_LOGI(TAG, "Idle timeout — entering Sleep");
        enter_sleep();
        break;

    case PEU_STATE_ARMED:
        ESP_LOGW(TAG, "Arm timeout — cancelling armed state");
        if (m_has_arm) {
            enter_idle();
        } else {
            enter_armed(); // no button: reset, don't go idle
        }
        break;

    case PEU_STATE_RESULT:
        enter_idle_after_result();
        break;

    default:
        break;
    }
}

/* ── Event handlers ───────────────────────────────────────────────────────── */

void ProvisioningFSM::handle_arm_requested()
{
    switch (m_state) {
    case PEU_STATE_SLEEP:
        ESP_LOGI(TAG, "Arm button (from SLEEP) → IDLE");
        enter_idle();
        break;

    case PEU_STATE_IDLE:
        ESP_LOGI(TAG, "Arm button (from IDLE) → ARMED");
        enter_armed();
        break;

    case PEU_STATE_ARMED:
        ESP_LOGI(TAG, "Arm button (from ARMED) → IDLE (cancel)");
        enter_idle();
        break;

    default:
        // Arm button ignored during SEND and RESULT states.
        break;
    }
}

void ProvisioningFSM::handle_credential_read(const event_credential_read_t *cred)
{
    char uid_str[CREDENTIAL_UID_HEX_STR_LEN];
    credential_uid_to_hex(&cred->credential, uid_str, sizeof(uid_str));

    if (m_state != PEU_STATE_ARMED) {
        ESP_LOGD(TAG, "Credential read ignored in state %d", (int)m_state);
        return;
    }

    ESP_LOGI(TAG, "Card read — UID: %s", uid_str);
    publish_capture_request(cred);
    m_state       = PEU_STATE_CAPTURE_SEND;
    m_deadline_ms = 0;
    if (m_has_feedback) {
        m_feedback->indicate(feedback_type_t::CARD_READ);
    }
}

void ProvisioningFSM::handle_provision_result(const event_provision_result_t *result)
{
    if (m_state != PEU_STATE_CAPTURE_SEND) {
        return;
    }

    feedback_type_t fb;
    switch (result->reason) {
    case PROVISION_RESULT_PENDING_CREATED:
        ESP_LOGI(TAG, "Capture accepted — pending admin approval (member_uuid=%s)",
                 result->member_uuid);
        fb = feedback_type_t::PEU_RESULT_PENDING;
        break;

    case PROVISION_RESULT_DUPLICATE_ACTIVE:
    case PROVISION_RESULT_DUPLICATE_INACTIVE:
    case PROVISION_RESULT_DUPLICATE_PENDING:
        ESP_LOGW(TAG, "Provisioning DUPLICATE — detail=%s", result->detail);
        fb = feedback_type_t::PEU_RESULT_DUPLICATE;
        break;

    case PROVISION_RESULT_UNAUTHORIZED:
        ESP_LOGW(TAG, "Provisioning UNAUTHORIZED — detail=%s", result->detail);
        fb = feedback_type_t::PEU_RESULT_UNAUTHORIZED;
        break;

    default:
        ESP_LOGE(TAG, "Provisioning comm error — detail=%s", result->detail);
        fb = feedback_type_t::PEU_RESULT_ERROR;
        break;
    }

    enter_result(fb);
}

/* ── State transitions ────────────────────────────────────────────────────── */

void ProvisioningFSM::enter_sleep()
{
    m_state       = PEU_STATE_SLEEP;
    m_deadline_ms = 0;
    if (m_has_feedback) {
        m_feedback->indicate(feedback_type_t::NONE);
    }
    ESP_LOGI(TAG, "FSM → SLEEP");
}

void ProvisioningFSM::enter_idle()
{
    m_state       = PEU_STATE_IDLE;
    m_deadline_ms = now_ms() + CONFIG_PORTUNUS_IDLE_TIMEOUT_MS;
    if (m_has_feedback) {
        m_feedback->indicate(feedback_type_t::PEU_IDLE);
    }
    ESP_LOGI(TAG, "FSM → IDLE (idle_timeout=%dms)", CONFIG_PORTUNUS_IDLE_TIMEOUT_MS);
}

void ProvisioningFSM::enter_armed()
{
    m_state       = PEU_STATE_ARMED;
    m_deadline_ms = now_ms() + CONFIG_PORTUNUS_ARM_TIMEOUT_MS;
    if (m_has_feedback) {
        m_feedback->indicate(feedback_type_t::PEU_ARMED_CAPTURE);
    }
    ESP_LOGI(TAG, "FSM → ARMED  arm_timeout=%dms", CONFIG_PORTUNUS_ARM_TIMEOUT_MS);
}

void ProvisioningFSM::enter_result(feedback_type_t fb)
{
    m_state       = PEU_STATE_RESULT;
    m_deadline_ms = now_ms() + CONFIG_PORTUNUS_RESULT_DISPLAY_MS;
    if (m_has_feedback) {
        m_feedback->indicate(fb);
    }
    ESP_LOGI(TAG, "FSM → RESULT (display=%dms)", CONFIG_PORTUNUS_RESULT_DISPLAY_MS);
}

void ProvisioningFSM::enter_idle_after_result()
{
    if (m_has_arm) {
        enter_idle();
    } else {
        enter_armed();
    }
}

/* ── Request helpers ──────────────────────────────────────────────────────── */

void ProvisioningFSM::publish_capture_request(const event_credential_read_t *cred)
{
    portunus_event_t evt;
    memset(&evt, 0, sizeof(evt));
    evt.id = EVENT_PROVISION_REQUEST;

    memcpy(evt.payload.provision_request.credential_uid,
           cred->credential.uid, cred->credential.uid_len);
    evt.payload.provision_request.credential_uid_len = cred->credential.uid_len;

    event_bus_publish(&evt);
}
