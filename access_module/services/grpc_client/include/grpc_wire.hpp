/**
 * @file grpc_wire.hpp
 * @brief Pure gRPC wire helpers extracted from grpc_client.cpp.
 *
 * No ESP-IDF, nghttp2, esp-tls, or FreeRTOS dependency: the unit tier links
 * grpc_wire.cpp directly, and grpc_client.cpp calls the same functions in
 * production (no test-only reimplementation).
 */

#pragma once

#include <stddef.h>
#include <stdint.h>

/** gRPC frame header: 1 byte compression flag + 4 bytes big-endian length. */
static constexpr size_t GRPC_FRAME_HEADER_LEN = 5;

/** Upper bound on an outbound RPC payload; grpc_client frames requests in a
 *  fixed stack buffer of GRPC_FRAME_HEADER_LEN + this, so no heap on the hot
 *  path. server_comm static_asserts that every request message fits. */
static constexpr size_t GRPC_MAX_REQUEST_PAYLOAD = 192;

/** Result of decoding one length-prefixed gRPC message. */
enum class grpc_frame_status_t : uint8_t {
    OK,
    SHORT_HEADER,   /**< Fewer than GRPC_FRAME_HEADER_LEN bytes. */
    COMPRESSED,     /**< Compression flag set; unsupported. */
    TRUNCATED,      /**< Declared length exceeds the bytes received. */
};

/**
 * @brief Build [0x00][len BE32][proto bytes] into @p out_buf.
 * @return false if @p out_cap cannot hold header + payload.
 */
bool grpc_frame_encode(const uint8_t *proto_buf, size_t proto_len,
                       uint8_t *out_buf, size_t out_cap, size_t *out_len);

/**
 * @brief Validate a received gRPC frame and locate its payload.
 *
 * The length check is written so it cannot wrap when size_t is 32 bits
 * (ESP32): msg_len is compared against the bytes remaining after the header,
 * never added to the header length.
 */
grpc_frame_status_t grpc_frame_decode(const uint8_t *frame_buf, size_t frame_len,
                                      const uint8_t **proto_buf, size_t *proto_len);

/**
 * @brief Parse a grpc-status header value.
 * @return The status in [0, 16], or -1 if the value is empty, non-decimal,
 *         or out of range. Callers map -1 to GRPC_STATUS_INTERNAL so an
 *         unparsable status can never read as OK.
 */
int grpc_parse_status(const uint8_t *value, size_t len);

/**
 * @brief Advance the reconnect backoff after one failed connect.
 *
 * @param state_ms  Backoff state; 0 means no failure since the last success.
 * @return Delay in ms to apply before the next attempt, or 0 when backoff is
 *         disabled (min_ms == 0). Sequence: min, 2*min, 4*min, ... capped at
 *         max(min_ms, max_ms).
 */
uint32_t grpc_backoff_on_failure(uint32_t *state_ms, uint32_t min_ms, uint32_t max_ms);
