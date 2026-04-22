/**
 * @file provisioning_fsm.cpp
 * @brief Provisioning console FSM implementation.
 *
 * Two-scan flow:
 *   1. Any credential read in IDLE → start 30s timeout, move to AWAITING.
 *   2. Any credential read in AWAITING → compute SHA-256(uid), publish
 *      EVENT_PROVISION_REQUEST, move to SENDING.
 *   3. Timeout in AWAITING → return to IDLE.
 *   4. EVENT_PROVISION_SUCCESS / EVENT_PROVISION_FAILED → show feedback,
 *      return to IDLE.
 *
 * The operator_uuid and role_id are read from Kconfig at build time and
 * embedded in every ProvisionCredentialRequest.
 *
 * SHA-256 is computed via mbedTLS (already present for TLS). The raw
 * credential UID bytes never leave the device — only the hash is sent.
 */

#include "provisioning_fsm.h"
#include "event_bus.h"
#include "error_codes.h"
#include "credential_types.h"
#include "sdkconfig.h"

#ifdef CONFIG_PORTUNUS_ENABLE_WIFI
#include "wifi_mgr.h"
#endif

#include "mbedtls/sha256.h"
#include "esp_log.h"
#include "esp_timer.h"
#include "freertos/FreeRTOS.h"
#include "freertos/task.h"
#include "freertos/queue.h"

#include <string.h>
#include <inttypes.h>

static const char *TAG = "prov_fsm";

/* ── Task configuration ───────────────────────────────────────────────────── */

static const int FSM_TASK_STACK      = 4096;
static const int FSM_TASK_PRIORITY   = 5;
static const int POLL_TASK_STACK     = 4096;
static const int POLL_TASK_PRIORITY  = 4;
static const int FSM_EVENT_QUEUE_LEN = 8;
static const int FSM_POLL_INTERVAL_MS = 100;

/* Credential poll timing — same cadence as the access module. */
static const int MFRC522_POLL_INTERVAL_MS = 250;
static const int CARD_REREAD_DELAY_MS     = 1000;

/* ── Helpers ──────────────────────────────────────────────────────────────── */

static inline int64_t now_ms(void)
{
    return esp_timer_get_time() / 1000;
}

/**
 * @brief Compute SHA-256(data, len) into out[32].
 * @return true on success.
 */
static bool sha256(const uint8_t *data, size_t len, uint8_t out[32])
{
    mbedtls_sha256_context ctx;
    mbedtls_sha256_init(&ctx);
    int rc = mbedtls_sha256_starts(&ctx, 0 /* is_224=false */);
    if (rc == 0) { rc = mbedtls_sha256_update(&ctx, data, len); }
    if (rc == 0) { rc = mbedtls_sha256_finish(&ctx, out); }
    mbedtls_sha256_free(&ctx);
    return (rc == 0);
}

/* ── Constructor ──────────────────────────────────────────────────────────── */

ProvisioningFSM::ProvisioningFSM(ICredentialReader *reader, IFeedback *feedback)
    : m_reader(reader)
    , m_feedback(feedback)
{
}

/* ── init() ───────────────────────────────────────────────────────────────── */

portunus_err_t ProvisioningFSM::init()
{
    ESP_LOGI(TAG, "Initialising provisioning FSM");

    if (m_reader != nullptr) {
        portunus_err_t err = m_reader->init();
        if (err == PORTUNUS_OK) {
            m_has_reader = true;
            ESP_LOGI(TAG, "Credential reader: OK");
        } else {
            ESP_LOGE(TAG, "Credential reader init failed (0x%" PRIx32 ") — cannot provision",
                     (uint32_t)err);
            /* Reader is mandatory for a provisioning console. */
            return err;
        }
    } else {
        ESP_LOGE(TAG, "Credential reader not present — provisioning console requires a reader");
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

#ifdef CONFIG_PORTUNUS_ENABLE_WIFI
    m_has_network = wifi_mgr_is_connected();
#endif

    m_event_queue = xQueueCreate(FSM_EVENT_QUEUE_LEN, sizeof(portunus_event_t));
    if (m_event_queue == nullptr) {
        ESP_LOGE(TAG, "Failed to create FSM event queue");
        return PORTUNUS_ERR_QUEUE_CREATE;
    }

    ESP_LOGI(TAG, "Capabilities: reader=%d feedback=%d network=%d",
             m_has_reader, m_has_feedback, m_has_network);

    return PORTUNUS_OK;
}

/* ── start() ──────────────────────────────────────────────────────────────── */

portunus_err_t ProvisioningFSM::start()
{
    ESP_LOGI(TAG, "Starting provisioning FSM");

    event_bus_subscribe(EVENT_CREDENTIAL_READ,   on_event_bus_event, this);
    event_bus_subscribe(EVENT_PROVISION_SUCCESS, on_event_bus_event, this);
    event_bus_subscribe(EVENT_PROVISION_FAILED,  on_event_bus_event, this);

    BaseType_t ret = xTaskCreate(
        fsm_task_entry,
        "prov_fsm",
        FSM_TASK_STACK,
        this,
        FSM_TASK_PRIORITY,
        &m_fsm_task_handle
    );
    if (ret != pdPASS) {
        ESP_LOGE(TAG, "Failed to create FSM task");
        return PORTUNUS_ERR_TASK_CREATE;
    }

    ret = xTaskCreate(
        credential_poll_task_entry,
        "prov_poll",
        POLL_TASK_STACK,
        this,
        POLL_TASK_PRIORITY,
        &m_poll_task_handle
    );
    if (ret != pdPASS) {
        ESP_LOGE(TAG, "Failed to create credential polling task");
        return PORTUNUS_ERR_TASK_CREATE;
    }

    if (m_has_feedback) {
        m_feedback->indicate(feedback_type_t::PROVISIONING_IDLE);
    }

    ESP_LOGI(TAG, "Provisioning FSM running — state=IDLE");
    ESP_LOGI(TAG, "Operator UUID: %s  Role: %s  Timeout: %d ms",
             CONFIG_PORTUNUS_OPERATOR_UUID,
             CONFIG_PORTUNUS_DEFAULT_ROLE_ID,
             CONFIG_PORTUNUS_PROVISION_TIMEOUT_MS);

    return PORTUNUS_OK;
}

/* ── Event bus bridge ─────────────────────────────────────────────────────── */

void ProvisioningFSM::on_event_bus_event(const portunus_event_t *event, void *ctx)
{
    auto *fsm = static_cast<ProvisioningFSM *>(ctx);
    if (fsm->m_event_queue == nullptr) { return; }

    if (xQueueSend(fsm->m_event_queue, event, pdMS_TO_TICKS(10)) != pdTRUE) {
        ESP_LOGW(TAG, "FSM event queue full — dropped event 0x%04x", event->id);
    }
}

/* ── Task entry points ────────────────────────────────────────────────────── */

void ProvisioningFSM::fsm_task_entry(void *arg)
{
    static_cast<ProvisioningFSM *>(arg)->run();
}

void ProvisioningFSM::credential_poll_task_entry(void *arg)
{
    static_cast<ProvisioningFSM *>(arg)->poll_credential();
}

/* ── FSM main loop ────────────────────────────────────────────────────────── */

void ProvisioningFSM::run()
{
    portunus_event_t event;
    const TickType_t poll_timeout = pdMS_TO_TICKS(FSM_POLL_INTERVAL_MS);

    for (;;) {
        if (xQueueReceive(m_event_queue, &event, poll_timeout) == pdTRUE) {
            process_event(event);
        }

        /* Check both the awaiting timeout and the deferred idle re-arm. */
        if (m_timeout_deadline_ms != 0 && now_ms() >= m_timeout_deadline_ms) {
            handle_timeout();
        }

#ifdef CONFIG_PORTUNUS_ENABLE_WIFI
        m_has_network = wifi_mgr_is_connected();
#endif
    }
}

/* ── Event processing ─────────────────────────────────────────────────────── */

void ProvisioningFSM::process_event(const portunus_event_t &event)
{
    switch (event.id) {
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

void ProvisioningFSM::handle_credential_read(const event_credential_read_t *cred)
{
    char uid_str[CREDENTIAL_UID_HEX_STR_LEN];
    credential_uid_to_hex(&cred->credential, uid_str, sizeof(uid_str));

    switch (m_prov_state) {

    case PROV_STATE_IDLE:
        /* Scan 1: operator presence confirmed. Start timeout. */
        ESP_LOGI(TAG, "Scan 1 (operator) — UID: %s — awaiting new credential", uid_str);
        m_prov_state          = PROV_STATE_AWAITING_CREDENTIAL;
        m_timeout_deadline_ms = now_ms() + CONFIG_PORTUNUS_PROVISION_TIMEOUT_MS;
        if (m_has_feedback) {
            m_feedback->indicate(feedback_type_t::PROVISIONING_AWAITING);
        }
        break;

    case PROV_STATE_AWAITING_CREDENTIAL: {
        /* Scan 2: new credential. Compute SHA-256 and publish request. */
        ESP_LOGI(TAG, "Scan 2 (new credential) — UID: %s — sending provision request", uid_str);

        uint8_t hash[32];
        if (!sha256(cred->credential.uid, cred->credential.uid_len, hash)) {
            ESP_LOGE(TAG, "SHA-256 computation failed — aborting provisioning");
            transition_to_idle();
            return;
        }

        if (!m_has_network) {
            ESP_LOGW(TAG, "Network unavailable — cannot provision");
            transition_to_idle();
            return;
        }

        portunus_event_t req_evt;
        memset(&req_evt, 0, sizeof(req_evt));
        req_evt.id = EVENT_PROVISION_REQUEST;

        strncpy(req_evt.payload.provision_request.operator_uuid,
                CONFIG_PORTUNUS_OPERATOR_UUID,
                sizeof(req_evt.payload.provision_request.operator_uuid) - 1);

        memcpy(req_evt.payload.provision_request.credential_hash, hash, 32);

        strncpy(req_evt.payload.provision_request.role_id,
                CONFIG_PORTUNUS_DEFAULT_ROLE_ID,
                sizeof(req_evt.payload.provision_request.role_id) - 1);

        event_bus_publish(&req_evt);

        m_prov_state = PROV_STATE_SENDING;
        if (m_has_feedback) {
            m_feedback->indicate(feedback_type_t::CARD_READ);
        }
        break;
    }

    case PROV_STATE_SENDING:
        /* Ignore credential reads while waiting for server response. */
        ESP_LOGD(TAG, "Credential read ignored — provisioning in progress");
        break;
    }
}

void ProvisioningFSM::handle_provision_result(const event_provision_result_t *result)
{
    if (m_prov_state != PROV_STATE_SENDING) {
        return;
    }

    switch (result->reason) {
    case PROVISION_RESULT_SUCCESS:
        ESP_LOGI(TAG, "Provisioning SUCCESS — member_uuid=%s", result->member_uuid);
        if (m_has_feedback) {
            m_feedback->indicate(feedback_type_t::PROVISIONING_SUCCESS);
        }
        break;

    case PROVISION_RESULT_DUPLICATE_ACTIVE:
    case PROVISION_RESULT_DUPLICATE_INACTIVE:
    case PROVISION_RESULT_DUPLICATE_PENDING:
        ESP_LOGW(TAG, "Provisioning DUPLICATE — detail=%s", result->detail);
        if (m_has_feedback) {
            m_feedback->indicate(feedback_type_t::PROVISIONING_DUPLICATE);
        }
        break;

    case PROVISION_RESULT_UNAUTHORIZED:
        ESP_LOGW(TAG, "Provisioning UNAUTHORIZED — detail=%s", result->detail);
        if (m_has_feedback) {
            m_feedback->indicate(feedback_type_t::PROVISIONING_UNAUTHORIZED);
        }
        break;

    case PROVISION_RESULT_INVALID_ROLE:
        ESP_LOGE(TAG, "Provisioning INVALID_ROLE — check PORTUNUS_DEFAULT_ROLE_ID");
        if (m_has_feedback) {
            m_feedback->indicate(feedback_type_t::PROVISIONING_UNAUTHORIZED);
        }
        break;

    default:
        ESP_LOGE(TAG, "Provisioning comm error — detail=%s", result->detail);
        if (m_has_feedback) {
            m_feedback->indicate(feedback_type_t::SYSTEM_ERROR);
        }
        break;
    }

    /*
     * Return to IDLE after a brief delay so the one-shot feedback
     * patterns have time to complete before the idle pattern resumes.
     * The FSM main loop will call transition_to_idle() after the
     * queue drains; we set state now so no further scan 2s are accepted.
     */
    m_prov_state = PROV_STATE_IDLE;

    /* Re-arm idle feedback after one-shot pattern duration (~1.5 s). */
    m_timeout_deadline_ms = now_ms() + 1500;
}

void ProvisioningFSM::handle_timeout()
{
    if (m_prov_state == PROV_STATE_AWAITING_CREDENTIAL) {
        ESP_LOGW(TAG, "Provisioning timeout — returning to IDLE");
        transition_to_idle();
        return;
    }

    /* Used as a deferred re-arm for idle feedback after result patterns. */
    if (m_prov_state == PROV_STATE_IDLE && m_timeout_deadline_ms != 0 &&
        now_ms() >= m_timeout_deadline_ms) {
        m_timeout_deadline_ms = 0;
        if (m_has_feedback) {
            m_feedback->indicate(feedback_type_t::PROVISIONING_IDLE);
        }
    }
}

void ProvisioningFSM::transition_to_idle()
{
    m_prov_state          = PROV_STATE_IDLE;
    m_timeout_deadline_ms = 0;
    if (m_has_feedback) {
        m_feedback->indicate(feedback_type_t::PROVISIONING_IDLE);
    }
    ESP_LOGI(TAG, "FSM → IDLE");
}

/* ── Credential polling sub-task ──────────────────────────────────────────── */

void ProvisioningFSM::poll_credential()
{
    const TickType_t poll_interval = pdMS_TO_TICKS(MFRC522_POLL_INTERVAL_MS);
    const TickType_t reread_delay  = pdMS_TO_TICKS(CARD_REREAD_DELAY_MS);

    for (;;) {
        credential_t cred;
        portunus_err_t err = m_reader->read(&cred);

        if (err == PORTUNUS_OK) {
            portunus_event_t event;
            memset(&event, 0, sizeof(event));
            event.id = EVENT_CREDENTIAL_READ;
            event.payload.credential_read.credential   = cred;
            event.payload.credential_read.timestamp_ms =
                esp_timer_get_time() / 1000;

            event_bus_publish(&event);

            m_reader->halt();
            vTaskDelay(reread_delay);
            continue;
        }

        vTaskDelay(poll_interval);
    }
}
