#include "grpc_wire.hpp"

#include <string.h>

bool grpc_frame_encode(const uint8_t *proto_buf, size_t proto_len,
                       uint8_t *out_buf, size_t out_cap, size_t *out_len)
{
    if (out_cap < GRPC_FRAME_HEADER_LEN || proto_len > out_cap - GRPC_FRAME_HEADER_LEN ||
        proto_len > UINT32_MAX) {
        return false;
    }

    const uint32_t len = static_cast<uint32_t>(proto_len);
    out_buf[0] = 0x00;  /* no compression */
    out_buf[1] = static_cast<uint8_t>(len >> 24);
    out_buf[2] = static_cast<uint8_t>(len >> 16);
    out_buf[3] = static_cast<uint8_t>(len >> 8);
    out_buf[4] = static_cast<uint8_t>(len);
    if (proto_len > 0) {
        memcpy(&out_buf[GRPC_FRAME_HEADER_LEN], proto_buf, proto_len);
    }

    *out_len = GRPC_FRAME_HEADER_LEN + proto_len;
    return true;
}

grpc_frame_status_t grpc_frame_decode(const uint8_t *frame_buf, size_t frame_len,
                                      const uint8_t **proto_buf, size_t *proto_len)
{
    if (frame_len < GRPC_FRAME_HEADER_LEN) {
        return grpc_frame_status_t::SHORT_HEADER;
    }
    if (frame_buf[0] != 0x00) {
        return grpc_frame_status_t::COMPRESSED;
    }

    const uint32_t msg_len = (static_cast<uint32_t>(frame_buf[1]) << 24) |
                             (static_cast<uint32_t>(frame_buf[2]) << 16) |
                             (static_cast<uint32_t>(frame_buf[3]) << 8)  |
                              static_cast<uint32_t>(frame_buf[4]);

    /* Subtract on the side that cannot underflow (frame_len >= header here). */
    if (msg_len > frame_len - GRPC_FRAME_HEADER_LEN) {
        return grpc_frame_status_t::TRUNCATED;
    }

    *proto_buf = &frame_buf[GRPC_FRAME_HEADER_LEN];
    *proto_len = static_cast<size_t>(msg_len);
    return grpc_frame_status_t::OK;
}

int grpc_parse_status(const uint8_t *value, size_t len)
{
    if (value == nullptr || len == 0 || len > 2) {
        return -1;
    }
    int v = 0;
    for (size_t i = 0; i < len; ++i) {
        if (value[i] < '0' || value[i] > '9') {
            return -1;
        }
        v = v * 10 + (value[i] - '0');
    }
    return v <= 16 ? v : -1;
}

uint32_t grpc_backoff_on_failure(uint32_t *state_ms, uint32_t min_ms, uint32_t max_ms)
{
    if (min_ms == 0) {
        return 0;
    }
    if (*state_ms == 0) {
        *state_ms = min_ms;
    }
    const uint32_t delay = *state_ms;

    const uint32_t cap = max_ms < min_ms ? min_ms : max_ms;
    *state_ms = (*state_ms > cap / 2) ? cap : *state_ms * 2;
    return delay;
}
