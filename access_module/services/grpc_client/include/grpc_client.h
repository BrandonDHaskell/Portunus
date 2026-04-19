/**
 * @file grpc_client.h
 * @brief Lightweight gRPC client for ESP-IDF (unary RPCs only).
 *
 * Implements the gRPC wire protocol over HTTP/2 using nghttp2 + esp-tls.
 * Designed for the Portunus access module's two unary RPCs:
 *   - /portunus.v1.PortunusService/SendHeartbeat
 *   - /portunus.v1.PortunusService/RequestAccess
 *
 * Architecture:
 *   The client maintains a persistent TLS+HTTP/2 connection to the server.
 *   Each unary call opens an HTTP/2 stream, sends the gRPC-framed request,
 *   receives the gRPC-framed response + trailers, and closes the stream.
 *   The connection is reused across calls and automatically re-established
 *   on failure.
 *
 * gRPC wire format (sent in HTTP/2 DATA frames):
 *   [1 byte: compression flag (0x00)] [4 bytes: message length (big-endian)]
 *   [N bytes: protobuf-encoded message]
 *
 * Call grpc_client_init() after WiFi is connected.  All calls are blocking
 * and must be made from the same task (the server_comm task).
 */

#pragma once

#include "portunus_types.h"
#include <stddef.h>
#include <stdint.h>
#include <stdbool.h>

#ifdef __cplusplus
extern "C" {
#endif

/* ── Configuration ─────────────────────────────────────────────────────────── */

typedef struct {
    const char *host;           /**< Server hostname or IP address. */
    uint16_t    port;           /**< Server port (e.g. 50051 or 8443). */

    /* TLS settings */
    const char *ca_cert_pem;    /**< PEM CA certificate for pinning (NULL = use bundle). */
    bool        skip_cert_verify; /**< INSECURE: skip TLS cert verification (dev only). */

    /* Timeouts */
    int         connect_timeout_ms; /**< TCP + TLS handshake timeout. */
    int         rpc_timeout_ms;     /**< Per-RPC timeout (send + receive). */
} grpc_client_config_t;

/* ── Handle ────────────────────────────────────────────────────────────────── */

/** Opaque handle to a gRPC client instance. */
typedef struct grpc_client *grpc_client_handle_t;

/* ── gRPC status codes (subset used by Portunus) ──────────────────────────── */

#define GRPC_STATUS_OK                  0
#define GRPC_STATUS_CANCELLED           1
#define GRPC_STATUS_UNKNOWN             2
#define GRPC_STATUS_INVALID_ARGUMENT    3
#define GRPC_STATUS_DEADLINE_EXCEEDED   4
#define GRPC_STATUS_NOT_FOUND           5
#define GRPC_STATUS_PERMISSION_DENIED   7
#define GRPC_STATUS_UNAUTHENTICATED    16
#define GRPC_STATUS_UNAVAILABLE        14
#define GRPC_STATUS_INTERNAL           13

/* ── Lifecycle ─────────────────────────────────────────────────────────────── */

/**
 * @brief Create a gRPC client instance.
 *
 * Does NOT open the connection — that happens lazily on the first RPC call
 * or can be triggered explicitly with grpc_client_connect().
 *
 * @param[in]  cfg    Client configuration (copied internally).
 * @param[out] handle Receives the new client handle on success.
 * @return PORTUNUS_OK on success, PORTUNUS_ERR_NO_MEMORY on allocation failure.
 */
portunus_err_t grpc_client_init(const grpc_client_config_t *cfg,
                                 grpc_client_handle_t *handle);

/**
 * @brief Destroy a gRPC client and release all resources.
 *
 * Closes the underlying TLS connection and frees the nghttp2 session.
 * Safe to call with a NULL handle (no-op).
 */
void grpc_client_destroy(grpc_client_handle_t handle);

/* ── Connection management ─────────────────────────────────────────────────── */

/**
 * @brief Explicitly open (or re-open) the HTTP/2+TLS connection.
 *
 * This is called automatically by grpc_client_unary_call() if the
 * connection is not established, so explicit use is optional.
 *
 * @return PORTUNUS_OK on success.
 *         PORTUNUS_ERR_HTTP_CONNECT on TLS or HTTP/2 handshake failure.
 */
portunus_err_t grpc_client_connect(grpc_client_handle_t handle);

/**
 * @brief Close the HTTP/2 connection (if open).
 *
 * The connection can be re-established by calling grpc_client_connect()
 * or implicitly on the next grpc_client_unary_call().
 */
void grpc_client_disconnect(grpc_client_handle_t handle);

/**
 * @brief Check whether the HTTP/2 connection is currently open.
 */
bool grpc_client_is_connected(grpc_client_handle_t handle);

/* ── RPC ───────────────────────────────────────────────────────────────────── */

/**
 * @brief Perform a unary gRPC call (blocking).
 *
 * Sends a protobuf-encoded request and receives the protobuf-encoded
 * response.  The gRPC 5-byte length prefix is added/stripped internally.
 *
 * If the connection is not open, it is established automatically.
 * If the connection drops mid-call, the call fails and the connection
 * is marked as closed for the next attempt to reconnect.
 *
 * @param handle         Client handle from grpc_client_init().
 * @param service_method Full gRPC method path, e.g.
 *                       "/portunus.v1.PortunusService/SendHeartbeat".
 * @param req_buf        Protobuf-encoded request body (no gRPC prefix).
 * @param req_len        Length of req_buf in bytes.
 * @param resp_buf       Buffer to receive the protobuf response (prefix stripped).
 * @param resp_cap       Capacity of resp_buf in bytes.
 * @param resp_len       [out] Actual protobuf bytes written to resp_buf.
 * @param grpc_status    [out] gRPC status code from trailers (0 = OK).
 *
 * @return PORTUNUS_OK        Round-trip succeeded (check grpc_status for app errors).
 *         PORTUNUS_ERR_HTTP_CONNECT  Could not establish connection.
 *         PORTUNUS_ERR_TIMEOUT       RPC timed out.
 *         PORTUNUS_ERR_PROTO_ENCODE  gRPC frame encoding error.
 *         PORTUNUS_ERR_PROTO_DECODE  gRPC frame decoding error (bad response).
 */
portunus_err_t grpc_client_unary_call(grpc_client_handle_t handle,
                                       const char *service_method,
                                       const uint8_t *req_buf, size_t req_len,
                                       uint8_t *resp_buf, size_t resp_cap,
                                       int *resp_len, int *grpc_status);

/**
 * @brief Send an HTTP/2 PING and wait for the ACK (B18).
 *
 * Call periodically during idle periods to prevent NAT/firewall entries
 * from expiring between heartbeats.  No-op and returns
 * PORTUNUS_ERR_INVALID_ARG if the client is not connected.
 *
 * @return PORTUNUS_OK on success (ACK received within rpc_timeout_ms).
 *         PORTUNUS_ERR_HTTP_CONNECT if the ping fails (connection gone).
 */
portunus_err_t grpc_client_send_ping(grpc_client_handle_t handle);

/**
 * @brief Set custom metadata (headers) sent with every RPC.
 *
 * Used to attach the HMAC signature header.  The key/value are copied
 * internally.  Call again with NULL value to remove a key.
 *
 * @param handle Client handle.
 * @param key    Header name (e.g. "x-portunus-sig").
 * @param value  Header value (or NULL to remove).
 */
portunus_err_t grpc_client_set_metadata(grpc_client_handle_t handle,
                                         const char *key,
                                         const char *value);

#ifdef __cplusplus
}
#endif