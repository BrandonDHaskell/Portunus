/**
 * @file grpc_client.cpp
 * @brief Lightweight gRPC client for ESP-IDF — implementation.
 *
 * Implements unary gRPC calls over HTTP/2 using:
 *   - esp-tls for the TLS transport (mbedTLS underneath)
 *   - nghttp2 for HTTP/2 framing
 *   - Manual gRPC wire format (5-byte length-prefixed protobuf)
 *
 * The design is single-threaded: all calls must happen from the same
 * FreeRTOS task (the server_comm task).  The nghttp2 session is pumped
 * synchronously via blocking esp-tls reads/writes.
 *
 * Connection lifecycle:
 *   1. esp_tls_conn_new() with ALPN "h2"
 *   2. nghttp2_session_client_new() with send/recv callbacks
 *   3. Exchange HTTP/2 SETTINGS frames
 *   4. For each unary RPC: open stream → send HEADERS+DATA → recv DATA+trailers
 *   5. Connection kept alive between RPCs.  Any failure after a stream was
 *      submitted tears the session down (see abandon_stream()); reconnects
 *      are rate-limited by exponential backoff.
 */

#include "grpc_client.hpp"
#include "error_codes.hpp"

#include "esp_tls.h"
#include "esp_crt_bundle.h"
#include "esp_log.h"
#include "esp_timer.h"

#include "nghttp2/nghttp2.h"

#include "freertos/FreeRTOS.h"
#include "freertos/task.h"

#include <cinttypes>
#include <cstring>
#include <cstdlib>
#include <arpa/inet.h>  /* htonl / ntohl */
#include <sys/socket.h> /* setsockopt / SO_RCVTIMEO */

static const char *TAG = "grpc_client";

/* ── Constants ─────────────────────────────────────────────────────────────── */

/** gRPC frame header: 1 byte compression flag + 4 bytes message length. */
static constexpr size_t GRPC_FRAME_HEADER_LEN = 5;

/** Upper bound on outbound RPC payload; avoids heap allocation on the hot path.
 *  Sized to HeartbeatRequest_size (142) + margin. */
static constexpr size_t GRPC_MAX_REQUEST_PAYLOAD = 192;

/* ── Internal types ────────────────────────────────────────────────────────── */

/**
 * Per-stream state for tracking an in-flight unary RPC.
 * Allocated on the stack of grpc_client_unary_call() and passed as
 * nghttp2 stream user data.
 */
struct stream_state_t {
    /* Response accumulation */
    uint8_t *resp_buf;           /**< Caller's response buffer. */
    size_t   resp_cap;           /**< Capacity of resp_buf. */
    size_t   resp_len;           /**< Bytes written so far (includes gRPC frame header). */

    /* gRPC trailers */
    int      grpc_status;        /**< Parsed grpc-status from trailers (-1 = not received). */
    bool     headers_done;       /**< True after initial HEADERS frame received. */
    bool     stream_closed;      /**< True after stream is fully closed. */
    bool     got_error;          /**< True if an error occurred during the stream. */
    uint32_t stream_error_code;  /**< HTTP/2 RST_STREAM error code (0 = clean close). */

    /* Response HMAC (x-portunus-sig trailer from server) */
    char     resp_sig_hex[65];   /**< NUL-terminated hex-encoded signature, or empty. */
};

/** Main client structure (opaque to callers). */
struct grpc_client {
    grpc_client_config_t  cfg;

    /* TLS connection */
    esp_tls_t            *tls;
    bool                  connected;

    /* HTTP/2 session */
    nghttp2_session      *session;
    bool                  settings_received; /**< True after server SETTINGS frame received. */
    bool                  ping_ack_received; /**< True after the server ACKs our PING. */

    /* Reconnect backoff */
    int64_t               next_connect_us;   /**< esp_timer time before which connect is refused (0 = none). */
    uint32_t              backoff_ms;        /**< Delay applied after the next failed connect. */
};

/* ── Helper: build an nghttp2_nv from string literals / buffers ────────────── */

static nghttp2_nv make_nv(const char *name, const char *value)
{
    nghttp2_nv nv;
    nv.name     = reinterpret_cast<uint8_t *>(const_cast<char *>(name));
    nv.value    = reinterpret_cast<uint8_t *>(const_cast<char *>(value));
    nv.namelen  = strlen(name);
    nv.valuelen = strlen(value);
    nv.flags    = NGHTTP2_NV_FLAG_NONE;
    return nv;
}

/* ── gRPC frame helpers ────────────────────────────────────────────────────── */

/**
 * @brief Build a gRPC length-prefixed message frame.
 *
 * Layout: [0x00 (no compression)] [4-byte big-endian length] [protobuf bytes]
 *
 * @param proto_buf   Protobuf-encoded message.
 * @param proto_len   Length of the protobuf message.
 * @param out_buf     Output buffer (must be at least proto_len + 5).
 * @param out_cap     Capacity of out_buf.
 * @param out_len     [out] Total bytes written (5 + proto_len).
 * @return true on success, false if out_buf is too small.
 */
static bool grpc_frame_encode(const uint8_t *proto_buf, size_t proto_len,
                               uint8_t *out_buf, size_t out_cap, size_t *out_len)
{
    size_t total = GRPC_FRAME_HEADER_LEN + proto_len;
    if (total > out_cap) {
        return false;
    }

    out_buf[0] = 0x00; /* No compression */
    uint32_t len_be = htonl(static_cast<uint32_t>(proto_len));
    memcpy(&out_buf[1], &len_be, 4);
    memcpy(&out_buf[GRPC_FRAME_HEADER_LEN], proto_buf, proto_len);

    *out_len = total;
    return true;
}

/**
 * @brief Strip the 5-byte gRPC frame header from a received message.
 *
 * @param frame_buf   Buffer containing the full gRPC frame.
 * @param frame_len   Length of the frame.
 * @param proto_buf   [out] Points into frame_buf past the header.
 * @param proto_len   [out] Length of the protobuf payload.
 * @return true on success, false if frame is malformed.
 */
static bool grpc_frame_decode(const uint8_t *frame_buf, size_t frame_len,
                               const uint8_t **proto_buf, size_t *proto_len)
{
    if (frame_len < GRPC_FRAME_HEADER_LEN) {
        return false;
    }

    /* Byte 0: compression flag (we only support uncompressed). */
    if (frame_buf[0] != 0x00) {
        ESP_LOGE(TAG, "Compressed gRPC messages not supported (flag=0x%02x)",
                 frame_buf[0]);
        return false;
    }

    uint32_t msg_len;
    memcpy(&msg_len, &frame_buf[1], 4);
    msg_len = ntohl(msg_len);

    if (GRPC_FRAME_HEADER_LEN + msg_len > frame_len) {
        ESP_LOGE(TAG, "gRPC frame truncated: header says %lu bytes, have %zu",
                 static_cast<unsigned long>(msg_len),
                 frame_len - GRPC_FRAME_HEADER_LEN);
        return false;
    }

    *proto_buf = &frame_buf[GRPC_FRAME_HEADER_LEN];
    *proto_len = static_cast<size_t>(msg_len);
    return true;
}

/* ── nghttp2 callbacks ─────────────────────────────────────────────────────── */

/**
 * nghttp2 send callback: write data to the TLS socket.
 */
static ssize_t cb_send(nghttp2_session *session, const uint8_t *data,
                        size_t length, int flags, void *user_data)
{
    (void)session;
    (void)flags;
    auto *c = static_cast<grpc_client *>(user_data);

    int rv = esp_tls_conn_write(c->tls, data, length);
    if (rv <= 0) {
        if (rv == ESP_TLS_ERR_SSL_WANT_READ || rv == ESP_TLS_ERR_SSL_WANT_WRITE) {
            return NGHTTP2_ERR_WOULDBLOCK;
        }
        return NGHTTP2_ERR_CALLBACK_FAILURE;
    }
    return rv;
}

/**
 * nghttp2 recv callback: read data from the TLS socket.
 */
static ssize_t cb_recv(nghttp2_session *session, uint8_t *buf,
                        size_t length, int flags, void *user_data)
{
    (void)session;
    (void)flags;
    auto *c = static_cast<grpc_client *>(user_data);

    int rv = esp_tls_conn_read(c->tls, reinterpret_cast<char *>(buf), length);
    if (rv == 0) {
        return NGHTTP2_ERR_EOF;
    }
    if (rv < 0) {
        if (rv == ESP_TLS_ERR_SSL_WANT_READ || rv == ESP_TLS_ERR_SSL_WANT_WRITE) {
            return NGHTTP2_ERR_WOULDBLOCK;
        }
        return NGHTTP2_ERR_CALLBACK_FAILURE;
    }
    return rv;
}

/**
 * nghttp2 callback: received a header field for a stream.
 * We parse the gRPC trailers here (grpc-status).
 */
static int cb_on_header(nghttp2_session *session,
                         const nghttp2_frame *frame,
                         const uint8_t *name, size_t namelen,
                         const uint8_t *value, size_t valuelen,
                         uint8_t flags, void *user_data)
{
    (void)flags;
    (void)user_data;

    auto *ss = static_cast<stream_state_t *>(
        nghttp2_session_get_stream_user_data(session, frame->hd.stream_id));
    if (ss == nullptr) {
        return 0;
    }

    /* Look for grpc-status in trailers. */
    if (namelen == 11 && memcmp(name, "grpc-status", 11) == 0) {
        /* Parse the integer status. */
        char status_str[8] = {};
        size_t copy_len = valuelen < sizeof(status_str) - 1 ? valuelen : sizeof(status_str) - 1;
        memcpy(status_str, value, copy_len);
        ss->grpc_status = atoi(status_str);
    }

    /* Capture the response HMAC signature trailer from the server. */
    if (namelen == 14 && memcmp(name, "x-portunus-sig", 14) == 0) {
        size_t copy_len = valuelen < sizeof(ss->resp_sig_hex) - 1
                          ? valuelen
                          : sizeof(ss->resp_sig_hex) - 1;
        memcpy(ss->resp_sig_hex, value, copy_len);
        ss->resp_sig_hex[copy_len] = '\0';
    }

    /* Log :status for debugging. */
    if (namelen == 7 && memcmp(name, ":status", 7) == 0) {
        ESP_LOGD(TAG, "HTTP/2 :status = %.*s",
                 static_cast<int>(valuelen), reinterpret_cast<const char *>(value));
    }

    return 0;
}

/**
 * nghttp2 callback: received a DATA chunk for a stream.
 * Accumulates the gRPC response frame into the stream_state buffer.
 */
static int cb_on_data_chunk(nghttp2_session *session, uint8_t flags,
                             int32_t stream_id, const uint8_t *data,
                             size_t len, void *user_data)
{
    (void)flags;
    (void)user_data;

    auto *ss = static_cast<stream_state_t *>(
        nghttp2_session_get_stream_user_data(session, stream_id));
    if (ss == nullptr) {
        return 0;
    }

    size_t remaining = ss->resp_cap - ss->resp_len;
    size_t to_copy = len < remaining ? len : remaining;
    if (to_copy > 0) {
        memcpy(ss->resp_buf + ss->resp_len, data, to_copy);
        ss->resp_len += to_copy;
    }

    if (to_copy < len) {
        ESP_LOGW(TAG, "gRPC response truncated: buffer full (%zu bytes)", ss->resp_cap);
        ss->got_error = true;
    }

    return 0;
}

/**
 * nghttp2 callback: a frame has been fully received.
 * We use this to detect when headers are done.
 */
static int cb_on_frame_recv(nghttp2_session *session,
                             const nghttp2_frame *frame,
                             void *user_data)
{
    if (frame->hd.stream_id == 0) {
        /* Connection-level frames: track handshake completion and PING ACKs. */
        auto *c = static_cast<grpc_client *>(user_data);
        const bool is_ack = (frame->hd.flags & NGHTTP2_FLAG_ACK) != 0;
        if (frame->hd.type == NGHTTP2_SETTINGS && !is_ack) {
            c->settings_received = true;
        } else if (frame->hd.type == NGHTTP2_PING && is_ack) {
            c->ping_ack_received = true;
        }
        return 0;
    }

    auto *ss = static_cast<stream_state_t *>(
        nghttp2_session_get_stream_user_data(session, frame->hd.stream_id));
    if (ss == nullptr) {
        return 0;
    }

    if (frame->hd.type == NGHTTP2_HEADERS) {
        if (frame->headers.cat == NGHTTP2_HCAT_RESPONSE) {
            ss->headers_done = true;
        }
    }

    return 0;
}

/**
 * nghttp2 callback: a stream has been closed.
 */
static int cb_on_stream_close(nghttp2_session *session, int32_t stream_id,
                               uint32_t error_code, void *user_data)
{
    (void)user_data;

    auto *ss = static_cast<stream_state_t *>(
        nghttp2_session_get_stream_user_data(session, stream_id));
    if (ss == nullptr) {
        return 0;
    }

    ss->stream_closed = true;
    if (error_code != 0) {
        ss->got_error = true;
        ss->stream_error_code = error_code;
    }

    return 0;
}

/* ── Session helpers ───────────────────────────────────────────────────────── */

/**
 * @brief Create the nghttp2 session with all callbacks registered.
 */
static portunus_err_t create_nghttp2_session(grpc_client *c)
{
    nghttp2_session_callbacks *cbs = nullptr;

    int rv = nghttp2_session_callbacks_new(&cbs);
    if (rv != 0) {
        ESP_LOGE(TAG, "nghttp2_session_callbacks_new failed: %d", rv);
        return PORTUNUS_ERR_NO_MEMORY;
    }

    nghttp2_session_callbacks_set_send_callback(cbs, cb_send);
    nghttp2_session_callbacks_set_recv_callback(cbs, cb_recv);
    nghttp2_session_callbacks_set_on_header_callback(cbs, cb_on_header);
    nghttp2_session_callbacks_set_on_data_chunk_recv_callback(cbs, cb_on_data_chunk);
    nghttp2_session_callbacks_set_on_frame_recv_callback(cbs, cb_on_frame_recv);
    nghttp2_session_callbacks_set_on_stream_close_callback(cbs, cb_on_stream_close);

    rv = nghttp2_session_client_new(&c->session, cbs, c);
    nghttp2_session_callbacks_del(cbs);

    if (rv != 0) {
        ESP_LOGE(TAG, "nghttp2_session_client_new failed: %d", rv);
        return PORTUNUS_ERR_NO_MEMORY;
    }

    return PORTUNUS_OK;
}

/**
 * @brief Pump the nghttp2 session until @p done becomes true.
 *
 * Sends pending frames and processes incoming ones.  @p done is set by a
 * callback: settings_received (handshake), ping_ack_received (keepalive),
 * or a stream's stream_closed (unary RPC).  For connection-level waits the
 * pump also flushes our own pending frames (e.g. the SETTINGS ACK) before
 * returning.
 *
 * @param c           Client handle.
 * @param done        Completion flag to wait on.
 * @param flush_first If true, return only once nothing is left to send.
 * @return PORTUNUS_OK on success, PORTUNUS_ERR_TIMEOUT, or
 *         PORTUNUS_ERR_HTTP_CONNECT if the session failed.
 */
static portunus_err_t pump_session(grpc_client *c, const bool *done, bool flush_first)
{
    int64_t start_us = esp_timer_get_time();
    int64_t timeout_us = static_cast<int64_t>(c->cfg.rpc_timeout_ms) * 1000;

    while (true) {
        if (*done && (!flush_first || nghttp2_session_want_write(c->session) == 0)) {
            return PORTUNUS_OK;
        }

        if ((esp_timer_get_time() - start_us) > timeout_us) {
            ESP_LOGW(TAG, "Session pump timed out after %d ms", c->cfg.rpc_timeout_ms);
            return PORTUNUS_ERR_TIMEOUT;
        }

        /* Send any pending outbound frames. */
        int rv = nghttp2_session_send(c->session);
        if (rv != 0) {
            ESP_LOGE(TAG, "nghttp2_session_send error: %s", nghttp2_strerror(rv));
            c->connected = false;
            return PORTUNUS_ERR_HTTP_CONNECT;
        }

        /* Receive and process inbound frames. */
        rv = nghttp2_session_recv(c->session);
        if (rv != 0) {
            if (rv == NGHTTP2_ERR_EOF) {
                ESP_LOGW(TAG, "Server closed connection");
                c->connected = false;
                return PORTUNUS_ERR_HTTP_CONNECT;
            }
            ESP_LOGE(TAG, "nghttp2_session_recv error: %s", nghttp2_strerror(rv));
            c->connected = false;
            return PORTUNUS_ERR_HTTP_CONNECT;
        }

        /* Small yield to avoid starving other tasks. */
        vTaskDelay(pdMS_TO_TICKS(1));
    }
}

/* ── Failure handling ──────────────────────────────────────────────────────── */

/** Refuse connects until the current backoff elapses, then double it. */
static void schedule_reconnect_backoff(grpc_client *c)
{
    if (c->cfg.reconnect_backoff_min_ms == 0) {
        return;
    }
    if (c->backoff_ms == 0) {
        c->backoff_ms = c->cfg.reconnect_backoff_min_ms;
    }
    c->next_connect_us = esp_timer_get_time() + static_cast<int64_t>(c->backoff_ms) * 1000;
    ESP_LOGW(TAG, "Reconnect backoff: next attempt in %" PRIu32 " ms", c->backoff_ms);

    uint32_t max_ms = c->cfg.reconnect_backoff_max_ms;
    if (max_ms < c->cfg.reconnect_backoff_min_ms) {
        max_ms = c->cfg.reconnect_backoff_min_ms;
    }
    c->backoff_ms = (c->backoff_ms > max_ms / 2) ? max_ms : c->backoff_ms * 2;
}

static void reset_reconnect_backoff(grpc_client *c)
{
    c->next_connect_us = 0;
    c->backoff_ms      = 0;
}

/**
 * @brief Give up on an in-flight stream and drop the connection.
 *
 * The stream's user data points at a stream_state_t on the caller's stack.
 * Unregistering it first guarantees no later callback (late DATA, trailers,
 * or close during session teardown) can write through a dangling pointer.
 * Tearing the session down then discards whatever the server still sends
 * for that stream, so the next call starts from a clean connection.
 */
static void abandon_stream(grpc_client *c, int32_t stream_id)
{
    if (c->session != nullptr) {
        nghttp2_session_set_stream_user_data(c->session, stream_id, nullptr);
    }
    grpc_client_disconnect(c);
}

/* ── Data provider for nghttp2_submit_request ──────────────────────────────── */

/** Context for the data provider callback. */
struct data_provider_ctx_t {
    const uint8_t *data;
    size_t         len;
    size_t         offset;
};

/**
 * nghttp2 data source read callback: feeds the gRPC frame to the DATA frame.
 */
static ssize_t data_provider_read_cb(nghttp2_session *session,
                                      int32_t stream_id,
                                      uint8_t *buf, size_t length,
                                      uint32_t *data_flags,
                                      nghttp2_data_source *source,
                                      void *user_data)
{
    (void)session;
    (void)stream_id;
    (void)user_data;

    auto *ctx = static_cast<data_provider_ctx_t *>(source->ptr);
    size_t remaining = ctx->len - ctx->offset;

    if (remaining == 0) {
        *data_flags |= NGHTTP2_DATA_FLAG_EOF;
        return 0;
    }

    size_t to_copy = remaining < length ? remaining : length;
    memcpy(buf, ctx->data + ctx->offset, to_copy);
    ctx->offset += to_copy;

    if (ctx->offset >= ctx->len) {
        *data_flags |= NGHTTP2_DATA_FLAG_EOF;
    }

    return static_cast<ssize_t>(to_copy);
}

/* ── Public API ────────────────────────────────────────────────────────────── */

portunus_err_t grpc_client_init(const grpc_client_config_t *cfg,
                                 grpc_client_handle_t *handle)
{
    if (cfg == nullptr || handle == nullptr) {
        return PORTUNUS_ERR_INVALID_ARG;
    }

    auto *c = static_cast<grpc_client *>(calloc(1, sizeof(grpc_client)));
    if (c == nullptr) {
        ESP_LOGE(TAG, "Failed to allocate grpc_client");
        return PORTUNUS_ERR_NO_MEMORY;
    }

    /* Copy configuration. */
    c->cfg             = *cfg;
    c->connected       = false;
    c->tls             = nullptr;
    c->session         = nullptr;
    c->next_connect_us = 0;
    c->backoff_ms      = 0;

    *handle = c;
    ESP_LOGI(TAG, "gRPC client created for %s:%u", cfg->host, cfg->port);
    return PORTUNUS_OK;
}

void grpc_client_destroy(grpc_client_handle_t handle)
{
    if (handle == nullptr) { return; }

    grpc_client_disconnect(handle);
    free(handle);
    ESP_LOGI(TAG, "gRPC client destroyed");
}

portunus_err_t grpc_client_connect(grpc_client_handle_t c)
{
    if (c == nullptr) { return PORTUNUS_ERR_INVALID_ARG; }

    if (c->connected) {
        return PORTUNUS_OK;
    }

    if (c->next_connect_us != 0 && esp_timer_get_time() < c->next_connect_us) {
        ESP_LOGD(TAG, "Connect skipped: reconnect backoff in effect");
        return PORTUNUS_ERR_RECONNECT_BACKOFF;
    }

    /* Clean up any stale state. */
    grpc_client_disconnect(c);

    /* ── TLS connection with ALPN "h2" ─────────────────────────────────── */

    static const char *alpn_protos[] = {"h2", nullptr};

    esp_tls_cfg_t tls_cfg = {};
    tls_cfg.alpn_protos    = alpn_protos;
    tls_cfg.timeout_ms     = c->cfg.connect_timeout_ms;
    tls_cfg.non_block      = false;
    tls_cfg.skip_common_name = c->cfg.skip_cert_verify;

    if (c->cfg.skip_cert_verify) {
        /* Dev mode: accept any cert. */
        tls_cfg.skip_common_name = true;
        ESP_LOGW(TAG, "TLS cert verification DISABLED (dev mode)");
    } else if (c->cfg.ca_cert_pem != nullptr) {
        /* LAN pinning: use embedded CA cert. */
        tls_cfg.cacert_buf   = reinterpret_cast<const unsigned char *>(c->cfg.ca_cert_pem);
        tls_cfg.cacert_bytes = strlen(c->cfg.ca_cert_pem) + 1;
    } else {
        /* Public CA: validate against the ESP-IDF Mozilla CA bundle
         * (covers Let's Encrypt, DigiCert, etc.). */
        tls_cfg.crt_bundle_attach = esp_crt_bundle_attach;
    }

    ESP_LOGI(TAG, "Connecting TLS+HTTP/2 to %s:%u ...", c->cfg.host, c->cfg.port);

    c->tls = esp_tls_init();
    if (c->tls == nullptr) {
        ESP_LOGE(TAG, "esp_tls_init failed");
        schedule_reconnect_backoff(c);
        return PORTUNUS_ERR_HTTP_CONNECT;
    }

    int rv = esp_tls_conn_new_sync(c->cfg.host, strlen(c->cfg.host),
                                    c->cfg.port, &tls_cfg, c->tls);
    if (rv < 0) {
        ESP_LOGE(TAG, "TLS connection failed (rv=%d)", rv);
        grpc_client_disconnect(c);
        schedule_reconnect_backoff(c);
        return PORTUNUS_ERR_HTTP_CONNECT;
    }

    ESP_LOGI(TAG, "TLS connected, setting up HTTP/2 session");

    /* ── Set a short read timeout on the underlying socket ────────────── */
    /* Without this, esp_tls_conn_read() blocks indefinitely when no data
     * is available, which prevents nghttp2_session_recv() from returning
     * control to the pump loop.  A 100ms timeout causes the read to
     * return ESP_TLS_ERR_SSL_WANT_READ, which cb_recv maps to
     * NGHTTP2_ERR_WOULDBLOCK, allowing the pump to loop, send pending
     * frames (e.g. WINDOW_UPDATE), and retry the read. */
    {
        int sock_fd = -1;
        if (esp_tls_get_conn_sockfd(c->tls, &sock_fd) == ESP_OK && sock_fd >= 0) {
            struct timeval tv = {};
            tv.tv_sec  = 0;
            tv.tv_usec = 100000; /* 100 ms */
            setsockopt(sock_fd, SOL_SOCKET, SO_RCVTIMEO, &tv, sizeof(tv));
        } else {
            ESP_LOGW(TAG, "Could not set socket read timeout — pump may block");
        }
    }

    /* ── nghttp2 session ───────────────────────────────────────────────── */

    portunus_err_t err = create_nghttp2_session(c);
    if (err != PORTUNUS_OK) {
        grpc_client_disconnect(c);
        schedule_reconnect_backoff(c);
        return err;
    }

    /* Send the HTTP/2 client connection preface (SETTINGS frame). */
    nghttp2_settings_entry settings[2] = {};
    settings[0].settings_id = NGHTTP2_SETTINGS_MAX_CONCURRENT_STREAMS;
    settings[0].value       = 2;
    settings[1].settings_id = NGHTTP2_SETTINGS_INITIAL_WINDOW_SIZE;
    settings[1].value       = 65535;

    rv = nghttp2_submit_settings(c->session, NGHTTP2_FLAG_NONE,
                                  settings, 2);
    if (rv != 0) {
        ESP_LOGE(TAG, "nghttp2_submit_settings failed: %s", nghttp2_strerror(rv));
        grpc_client_disconnect(c);
        schedule_reconnect_backoff(c);
        return PORTUNUS_ERR_HTTP_CONNECT;
    }

    /* Pump to exchange SETTINGS — reset the flag so pump_session knows to wait. */
    c->settings_received = false;
    err = pump_session(c, &c->settings_received, true);
    if (err != PORTUNUS_OK) {
        ESP_LOGE(TAG, "HTTP/2 SETTINGS exchange failed");
        grpc_client_disconnect(c);
        schedule_reconnect_backoff(c);
        return err;
    }

    reset_reconnect_backoff(c);
    c->connected = true;
    ESP_LOGI(TAG, "HTTP/2 connection established to %s:%u", c->cfg.host, c->cfg.port);
    return PORTUNUS_OK;
}

void grpc_client_disconnect(grpc_client_handle_t c)
{
    if (c == nullptr) { return; }

    if (c->session != nullptr) {
        nghttp2_session_del(c->session);
        c->session = nullptr;
    }

    if (c->tls != nullptr) {
        esp_tls_conn_destroy(c->tls);
        c->tls = nullptr;
    }

    c->connected = false;
}

bool grpc_client_is_connected(grpc_client_handle_t c)
{
    if (c == nullptr) { return false; }
    return c->connected;
}

portunus_err_t grpc_client_send_ping(grpc_client_handle_t c)
{
    if (c == nullptr || !c->connected) {
        return PORTUNUS_ERR_INVALID_ARG;
    }

    static const uint8_t opaque[8] = {'P','O','R','T','U','N','U','S'};
    c->ping_ack_received = false;
    int rv = nghttp2_submit_ping(c->session, NGHTTP2_FLAG_NONE, opaque);
    if (rv != 0) {
        ESP_LOGE(TAG, "nghttp2_submit_ping failed: %s", nghttp2_strerror(rv));
        grpc_client_disconnect(c);
        return PORTUNUS_ERR_HTTP_CONNECT;
    }

    /* Wait for the ACK, not just for the PING to leave: a write can succeed
     * on a half-open TCP connection, an ACK cannot. */
    portunus_err_t err = pump_session(c, &c->ping_ack_received, false);
    if (err != PORTUNUS_OK) {
        ESP_LOGW(TAG, "No PING ACK from server; dropping connection");
        grpc_client_disconnect(c);
    }
    return err;
}

portunus_err_t grpc_client_unary_call(grpc_client_handle_t c,
                                       const char *service_method,
                                       const uint8_t *req_buf, size_t req_len,
                                       const grpc_metadata_t *metadata,
                                       size_t metadata_count,
                                       uint8_t *resp_buf, size_t resp_cap,
                                       int *resp_len, int *grpc_status,
                                       char *out_sig_hex)
{
    if (c == nullptr || service_method == nullptr || req_buf == nullptr ||
        resp_buf == nullptr || resp_len == nullptr || grpc_status == nullptr) {
        return PORTUNUS_ERR_INVALID_ARG;
    }
    if (metadata_count > GRPC_MAX_CALL_METADATA ||
        (metadata_count > 0 && metadata == nullptr)) {
        return PORTUNUS_ERR_INVALID_ARG;
    }
    for (size_t i = 0; i < metadata_count; i++) {
        if (metadata[i].key == nullptr || metadata[i].value == nullptr) {
            return PORTUNUS_ERR_INVALID_ARG;
        }
    }

    *resp_len = 0;
    *grpc_status = GRPC_STATUS_UNKNOWN;

    /* ── Ensure connection ─────────────────────────────────────────────── */

    if (!c->connected) {
        portunus_err_t err = grpc_client_connect(c);
        if (err != PORTUNUS_OK) {
            return err;
        }
    }

    /* ── Build gRPC frame (5-byte prefix + protobuf) ───────────────────── */

    if (req_len > GRPC_MAX_REQUEST_PAYLOAD) {
        ESP_LOGE(TAG, "Request payload too large: %zu > %zu", req_len, GRPC_MAX_REQUEST_PAYLOAD);
        return PORTUNUS_ERR_PROTO_ENCODE;
    }
    uint8_t grpc_frame[GRPC_FRAME_HEADER_LEN + GRPC_MAX_REQUEST_PAYLOAD];
    size_t grpc_frame_len = GRPC_FRAME_HEADER_LEN + req_len;

    size_t encoded_len = 0;
    if (!grpc_frame_encode(req_buf, req_len, grpc_frame, grpc_frame_len, &encoded_len)) {
        return PORTUNUS_ERR_PROTO_ENCODE;
    }

    /* ── Build HTTP/2 headers ──────────────────────────────────────────── */

    /* Base headers: :method, :scheme, :path, content-type, te */
    static constexpr size_t BASE_HDR_COUNT = 5;
    nghttp2_nv base_hdrs[BASE_HDR_COUNT] = {
        make_nv(":method",      "POST"),
        make_nv(":scheme",      "https"),
        make_nv(":path",        service_method),
        make_nv("content-type", "application/grpc"),
        make_nv("te",           "trailers"),
    };

    size_t total_hdrs = BASE_HDR_COUNT + metadata_count;
    nghttp2_nv hdrs[BASE_HDR_COUNT + GRPC_MAX_CALL_METADATA];

    memcpy(hdrs, base_hdrs, BASE_HDR_COUNT * sizeof(nghttp2_nv));
    for (size_t i = 0; i < metadata_count; i++) {
        hdrs[BASE_HDR_COUNT + i] = make_nv(metadata[i].key, metadata[i].value);
    }

    /* ── Set up data provider ──────────────────────────────────────────── */

    data_provider_ctx_t dp_ctx = {};
    dp_ctx.data   = grpc_frame;
    dp_ctx.len    = encoded_len;
    dp_ctx.offset = 0;

    nghttp2_data_provider data_prd = {};
    data_prd.source.ptr    = &dp_ctx;
    data_prd.read_callback = data_provider_read_cb;

    /* ── Set up stream state ───────────────────────────────────────────── */

    stream_state_t ss = {};
    ss.resp_buf      = resp_buf;
    ss.resp_cap      = resp_cap;
    ss.resp_len      = 0;
    ss.grpc_status   = -1;
    ss.headers_done  = false;
    ss.stream_closed = false;
    ss.got_error     = false;

    /* ── Submit the request ────────────────────────────────────────────── */

    int32_t stream_id = nghttp2_submit_request(
        c->session, nullptr, hdrs, total_hdrs, &data_prd, &ss);

    if (stream_id < 0) {
        ESP_LOGE(TAG, "nghttp2_submit_request failed: %s",
                 nghttp2_strerror(stream_id));
        grpc_client_disconnect(c);
        return PORTUNUS_ERR_HTTP_CONNECT;
    }

    ESP_LOGD(TAG, "Submitted gRPC request on stream %" PRId32 ": %s (%zu bytes)",
             stream_id, service_method, req_len);

    /* ── Pump until stream completes ───────────────────────────────────── */

    portunus_err_t err = pump_session(c, &ss.stream_closed, false);

    /* From here on `ss` must not outlive this frame inside nghttp2: every
     * failure path unregisters it and drops the connection. */
    if (err != PORTUNUS_OK) {
        abandon_stream(c, stream_id);
        return err;
    }

    if (ss.got_error) {
        /* Log the HTTP/2 error code so operators can distinguish retry vs back-off. */
        ESP_LOGW(TAG, "Stream error on %s: RST code=0x%x", service_method,
                 static_cast<unsigned>(ss.stream_error_code));
        abandon_stream(c, stream_id);
        return PORTUNUS_ERR_HTTP_CONNECT;
    }

    /* ── Parse the gRPC response ───────────────────────────────────────── */

    if (ss.grpc_status < 0) {
        /* No grpc-status trailer received — server might not be gRPC. */
        ESP_LOGW(TAG, "No grpc-status trailer received");
        *grpc_status = GRPC_STATUS_INTERNAL;
    } else {
        *grpc_status = ss.grpc_status;
    }

    /* Strip the 5-byte gRPC frame header from the response. */
    if (ss.resp_len >= GRPC_FRAME_HEADER_LEN) {
        const uint8_t *proto_ptr = nullptr;
        size_t proto_len = 0;

        if (grpc_frame_decode(resp_buf, ss.resp_len, &proto_ptr, &proto_len)) {
            /* Shift the protobuf payload to the start of resp_buf. */
            memmove(resp_buf, proto_ptr, proto_len);
            *resp_len = static_cast<int>(proto_len);
        } else {
            ESP_LOGW(TAG, "Failed to decode gRPC response frame");
            *resp_len = 0;
            return PORTUNUS_ERR_PROTO_DECODE;
        }
    } else if (ss.resp_len > 0) {
        /* Response shorter than gRPC frame header — malformed. */
        ESP_LOGW(TAG, "Response too short for gRPC frame: %zu bytes", ss.resp_len);
        *resp_len = 0;
        return PORTUNUS_ERR_PROTO_DECODE;
    } else {
        /* No response body (possible for some error statuses). */
        *resp_len = 0;
    }

    ESP_LOGD(TAG, "gRPC call %s complete: status=%d, response=%d bytes",
             service_method, *grpc_status, *resp_len);

    /* Propagate the response signature trailer to the caller. */
    if (out_sig_hex != nullptr) {
        size_t sig_len = strnlen(ss.resp_sig_hex, sizeof(ss.resp_sig_hex));
        memcpy(out_sig_hex, ss.resp_sig_hex, sig_len);
        out_sig_hex[sig_len] = '\0';
    }

    return PORTUNUS_OK;
}