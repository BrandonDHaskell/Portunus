/**
 * @file server_comm.cpp
 * @brief Server communication component implementation.
 *
 * Architecture:
 *   Event bus callbacks (on_heartbeat_event / on_credential_event) copy
 *   incoming events into s_comm_queue.  The comm_task drains the queue,
 *   encodes protobuf, performs the HTTP POST, and publishes the result
 *   (access decisions) back to the event bus.
 *
 *   HTTP I/O is blocking and runs entirely on the comm_task stack, so
 *   the event bus dispatcher is never blocked.
 */

#include "server_comm.h"
#include "event_bus.h"
#include "event_types.h"
#include "error_codes.h"
#include "network_config.h"
#include "wifi_mgr.h"
#include "portunus_types.h"
#include "credential_types.h"

/* Nanopb */
#include "portunus/v1/portunus.pb.h"
#include <pb_encode.h>
#include <pb_decode.h>

/* ESP-IDF */
#include "esp_http_client.h"
#include "esp_wifi.h"
#include "esp_netif.h"
#include "esp_log.h"
#include "esp_timer.h"
#include "esp_system.h"
#include "freertos/FreeRTOS.h"
#include "freertos/task.h"
#include "freertos/queue.h"

#include <string.h>
#include <stdio.h>
#include <inttypes.h>

static const char *TAG = "server_comm";

/* ── Configuration ─────────────────────────────────────────────────────────── */
#define COMM_TASK_STACK_SIZE    6144
#define COMM_TASK_PRIORITY      2       /* Below heartbeat(3) and card_poll(4) */
#define COMM_QUEUE_LENGTH       8       /* Pending events waiting for HTTP I/O */
#define URL_MAX_LEN            128

/* ── Module state ──────────────────────────────────────────────────────────── */
static QueueHandle_t  s_comm_queue   = NULL;
static TaskHandle_t   s_comm_task    = NULL;
static bool           s_initialized  = false;

/* Reusable URL buffers built once at init */
static char s_heartbeat_url[URL_MAX_LEN];
static char s_access_url[URL_MAX_LEN];

/* ── Forward declarations ──────────────────────────────────────────────────── */
static void comm_task(void *arg);
static void handle_heartbeat(const event_heartbeat_t *hb);
static void handle_credential(const event_credential_read_t *cred);

/* ── Helpers ───────────────────────────────────────────────────────────────── */

/**
 * @brief Get the station IP as a dotted-quad string.
 *        Returns false if the interface is down or has no IP.
 */
static bool get_sta_ip_str(char *buf, size_t len)
{
    esp_netif_t *netif = esp_netif_get_handle_from_ifkey("WIFI_STA_DEF");
    if (netif == NULL) { return false; }

    esp_netif_ip_info_t info;
    if (esp_netif_get_ip_info(netif, &info) != ESP_OK) { return false; }
    if (info.ip.addr == 0) { return false; }

    snprintf(buf, len, IPSTR, IP2STR(&info.ip));
    return true;
}

/**
 * @brief Get WiFi RSSI.  Returns false if unavailable.
 */
static bool get_rssi(int32_t *out)
{
    wifi_ap_record_t ap;
    if (esp_wifi_sta_get_ap_info(&ap) != ESP_OK) { return false; }
    *out = ap.rssi;
    return true;
}

/**
 * @brief Format a credential UID as a colon-separated hex string.
 *
 *   {0x04, 0xA3, 0x2B}  →  "04:A3:2B"
 */
static void uid_to_hex_str(const credential_t *cred, char *buf, size_t len)
{
    buf[0] = '\0';
    for (uint8_t i = 0; i < cred->uid_len && (i * 3 + 2) < len; i++) {
        char seg[4];
        snprintf(seg, sizeof(seg), "%s%02X", (i > 0) ? ":" : "", cred->uid[i]);
        strncat(buf, seg, len - strlen(buf) - 1);
    }
}

/* ── HTTP helper ───────────────────────────────────────────────────────────── */

/**
 * @brief POST a protobuf-encoded buffer to url, read the response into
 *        resp_buf.
 *
 * @param url        Full URL (e.g. "http://192.168.1.100:8080/v1/heartbeat")
 * @param req_buf    Encoded protobuf request body
 * @param req_len    Length of req_buf
 * @param resp_buf   Buffer to receive the response body
 * @param resp_cap   Capacity of resp_buf
 * @param resp_len   [out] Actual bytes written to resp_buf
 * @param http_status [out] HTTP status code
 *
 * @return PORTUNUS_OK on successful round-trip (even if http_status != 200).
 */
static portunus_err_t http_post_proto(const char *url,
                                      const uint8_t *req_buf, size_t req_len,
                                      uint8_t *resp_buf, size_t resp_cap,
                                      int *resp_len, int *http_status)
{
    esp_http_client_config_t cfg = {};
    cfg.url = url;
    cfg.timeout_ms = PORTUNUS_SERVER_REQUEST_TIMEOUT_MS;
    cfg.disable_auto_redirect = true;

    esp_http_client_handle_t client = esp_http_client_init(&cfg);
    if (client == NULL) {
        ESP_LOGE(TAG, "http_client_init failed");
        return PORTUNUS_ERR_HTTP_CONNECT;
    }

    esp_http_client_set_method(client, HTTP_METHOD_POST);
    esp_http_client_set_header(client, "Content-Type", "application/x-protobuf");

    esp_err_t err = esp_http_client_open(client, (int)req_len);
    if (err != ESP_OK) {
        ESP_LOGE(TAG, "HTTP open failed: %s", esp_err_to_name(err));
        esp_http_client_cleanup(client);
        return PORTUNUS_ERR_HTTP_CONNECT;
    }

    int written = esp_http_client_write(client, (const char *)req_buf, (int)req_len);
    if (written < 0 || (size_t)written != req_len) {
        ESP_LOGE(TAG, "HTTP write failed (wrote %d / %zu)", written, req_len);
        esp_http_client_close(client);
        esp_http_client_cleanup(client);
        return PORTUNUS_ERR_HTTP_CONNECT;
    }

    int content_length = esp_http_client_fetch_headers(client);
    (void)content_length;  /* May be -1 for chunked; we read until done */

    *http_status = esp_http_client_get_status_code(client);

    int total_read = 0;
    int read_len;
    while ((read_len = esp_http_client_read(client,
                                            (char *)resp_buf + total_read,
                                            (int)(resp_cap - total_read))) > 0) {
        total_read += read_len;
        if ((size_t)total_read >= resp_cap) { break; }
    }
    *resp_len = total_read;

    esp_http_client_close(client);
    esp_http_client_cleanup(client);

    return PORTUNUS_OK;
}

/* ── Event bus subscriber callbacks ────────────────────────────────────────── */
/* These run on the event bus dispatcher task and must be non-blocking.
   They simply copy the event into the server_comm queue. */

static void on_heartbeat_event(const portunus_event_t *event, void *ctx)
{
    (void)ctx;
    if (s_comm_queue == NULL) { return; }
    /* Best-effort: drop if queue is full rather than blocking dispatcher */
    xQueueSend(s_comm_queue, event, 0);
}

static void on_credential_event(const portunus_event_t *event, void *ctx)
{
    (void)ctx;
    if (s_comm_queue == NULL) { return; }
    xQueueSend(s_comm_queue, event, 0);
}

/* ── Event handlers (run on comm_task) ─────────────────────────────────────── */

static void handle_heartbeat(const event_heartbeat_t *hb)
{
    /* Build protobuf request */
    portunus_v1_HeartbeatRequest req = portunus_v1_HeartbeatRequest_init_zero;

    strncpy(req.module_id, PORTUNUS_MODULE_ID, sizeof(req.module_id) - 1);
    strncpy(req.firmware_version, PORTUNUS_FW_VERSION, sizeof(req.firmware_version) - 1);
    req.uptime_s        = hb->uptime_sec;
    req.free_heap_bytes = hb->free_heap_bytes;
    req.sequence        = hb->sequence;

    if (get_sta_ip_str(req.ip, sizeof(req.ip))) {
        /* ip populated */
    }

    int32_t rssi;
    if (get_rssi(&rssi)) {
        req.has_rssi_dbm = true;
        req.rssi_dbm     = rssi;
    }

    /* Encode */
    uint8_t req_buf[portunus_v1_HeartbeatRequest_size];
    pb_ostream_t ostream = pb_ostream_from_buffer(req_buf, sizeof(req_buf));
    if (!pb_encode(&ostream, portunus_v1_HeartbeatRequest_fields, &req)) {
        ESP_LOGE(TAG, "Heartbeat encode failed: %s", PB_GET_ERROR(&ostream));
        return;
    }

    /* POST */
    uint8_t resp_buf[portunus_v1_HeartbeatResponse_size + 16];  /* small margin */
    int resp_len = 0, status = 0;

    portunus_err_t err = http_post_proto(s_heartbeat_url,
                                         req_buf, ostream.bytes_written,
                                         resp_buf, sizeof(resp_buf),
                                         &resp_len, &status);
    if (err != PORTUNUS_OK) {
        ESP_LOGW(TAG, "Heartbeat HTTP failed: err=0x%04x", (unsigned)err);
        return;
    }

    if (status != 200) {
        ESP_LOGW(TAG, "Heartbeat server returned HTTP %d", status);
        return;
    }

    /* Decode response */
    portunus_v1_HeartbeatResponse resp = portunus_v1_HeartbeatResponse_init_zero;
    pb_istream_t istream = pb_istream_from_buffer(resp_buf, (size_t)resp_len);
    if (!pb_decode(&istream, portunus_v1_HeartbeatResponse_fields, &resp)) {
        ESP_LOGW(TAG, "Heartbeat decode failed: %s", PB_GET_ERROR(&istream));
        return;
    }

    ESP_LOGI(TAG, "Heartbeat OK — known=%d server_time=%s",
             resp.known, resp.server_time);
}

static void handle_credential(const event_credential_read_t *cred)
{
    /* Build protobuf request */
    portunus_v1_AccessRequest req = portunus_v1_AccessRequest_init_zero;

    strncpy(req.module_id, PORTUNUS_MODULE_ID, sizeof(req.module_id) - 1);
    uid_to_hex_str(&cred->credential, req.card_id, sizeof(req.card_id));

    /* Encode */
    uint8_t req_buf[portunus_v1_AccessRequest_size];
    pb_ostream_t ostream = pb_ostream_from_buffer(req_buf, sizeof(req_buf));
    if (!pb_encode(&ostream, portunus_v1_AccessRequest_fields, &req)) {
        ESP_LOGE(TAG, "Access encode failed: %s", PB_GET_ERROR(&ostream));
        return;
    }

    /* POST */
    uint8_t resp_buf[portunus_v1_AccessResponse_size + 16];
    int resp_len = 0, status = 0;

    portunus_err_t err = http_post_proto(s_access_url,
                                         req_buf, ostream.bytes_written,
                                         resp_buf, sizeof(resp_buf),
                                         &resp_len, &status);
    if (err != PORTUNUS_OK) {
        ESP_LOGW(TAG, "Access HTTP failed: err=0x%04x", (unsigned)err);
        return;
    }

    /* Accept 200 (granted/denied) and 403 (unknown module) */
    if (status != 200 && status != 403) {
        ESP_LOGW(TAG, "Access server returned HTTP %d", status);
        return;
    }

    /* Decode response */
    portunus_v1_AccessResponse resp = portunus_v1_AccessResponse_init_zero;
    pb_istream_t istream = pb_istream_from_buffer(resp_buf, (size_t)resp_len);
    if (!pb_decode(&istream, portunus_v1_AccessResponse_fields, &resp)) {
        ESP_LOGW(TAG, "Access decode failed: %s", PB_GET_ERROR(&istream));
        return;
    }

    ESP_LOGI(TAG, "Access decision — card=%s granted=%d reason=%s known=%d",
             req.card_id, resp.granted, resp.reason, resp.known);

    /* Publish decision event back to the bus */
    portunus_event_t decision;
    memset(&decision, 0, sizeof(decision));
    decision.id = resp.granted ? EVENT_ACCESS_GRANTED : EVENT_ACCESS_DENIED;

    strncpy(decision.payload.access_decision.card_id, req.card_id,
            sizeof(decision.payload.access_decision.card_id) - 1);
    strncpy(decision.payload.access_decision.reason, resp.reason,
            sizeof(decision.payload.access_decision.reason) - 1);
    decision.payload.access_decision.granted = resp.granted;
    decision.payload.access_decision.known   = resp.known;

    event_bus_publish(&decision);
}

/* ── Task ──────────────────────────────────────────────────────────────────── */

static void comm_task(void *arg)
{
    (void)arg;
    portunus_event_t event;

    ESP_LOGI(TAG, "Server comm task started");

    for (;;) {
        if (xQueueReceive(s_comm_queue, &event, pdMS_TO_TICKS(1000)) != pdTRUE) {
            continue;   /* Idle tick — nothing queued */
        }

        if (!wifi_mgr_is_connected()) {
            ESP_LOGD(TAG, "WiFi not connected — dropping event 0x%04x",
                     (unsigned)event.id);
            continue;
        }

        switch (event.id) {
        case EVENT_HEARTBEAT:
            handle_heartbeat(&event.payload.heartbeat);
            break;
        case EVENT_CREDENTIAL_READ:
            handle_credential(&event.payload.credential_read);
            break;
        default:
            ESP_LOGW(TAG, "Unexpected event 0x%04x in comm queue",
                     (unsigned)event.id);
            break;
        }
    }
}

/* ── Public API ────────────────────────────────────────────────────────────── */

portunus_err_t server_comm_init(void)
{
    if (s_initialized) {
        ESP_LOGW(TAG, "server_comm already initialised");
        return PORTUNUS_ERR_ALREADY_INIT;
    }

    /* Build URLs once */
    snprintf(s_heartbeat_url, sizeof(s_heartbeat_url),
             "http://%s:%d/v1/heartbeat",
             PORTUNUS_SERVER_HOST, PORTUNUS_SERVER_PORT);
    snprintf(s_access_url, sizeof(s_access_url),
             "http://%s:%d/v1/access_request",
             PORTUNUS_SERVER_HOST, PORTUNUS_SERVER_PORT);

    ESP_LOGI(TAG, "Heartbeat URL: %s", s_heartbeat_url);
    ESP_LOGI(TAG, "Access URL:    %s", s_access_url);

    /* Create internal queue */
    s_comm_queue = xQueueCreate(COMM_QUEUE_LENGTH, sizeof(portunus_event_t));
    if (s_comm_queue == NULL) {
        ESP_LOGE(TAG, "Failed to create comm queue");
        return PORTUNUS_ERR_QUEUE_CREATE;
    }

    /* Subscribe to events */
    event_bus_subscribe(EVENT_HEARTBEAT, on_heartbeat_event, NULL);
    event_bus_subscribe(EVENT_CREDENTIAL_READ, on_credential_event, NULL);

    /* Start task */
    BaseType_t ret = xTaskCreate(
        comm_task,
        "server_comm",
        COMM_TASK_STACK_SIZE,
        NULL,
        COMM_TASK_PRIORITY,
        &s_comm_task
    );
    if (ret != pdPASS) {
        ESP_LOGE(TAG, "Failed to create comm task");
        return PORTUNUS_ERR_TASK_CREATE;
    }

    s_initialized = true;
    ESP_LOGI(TAG, "Server comm initialised");
    return PORTUNUS_OK;
}