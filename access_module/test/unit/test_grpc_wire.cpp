/* Unit tier: gRPC wire helpers. No ESP-IDF, no FreeRTOS, no sdkconfig.
 * Built twice by CI: native (64-bit size_t) and host-m32 (32-bit size_t, the
 * ESP32 width). WIRE-04 only fails on the m32 build if the overflow returns. */
#include "unity.h"
#include "grpc_wire.hpp"

#include <string.h>

void setUp(void) {}
void tearDown(void) {}

static void put_len(uint8_t *f, uint32_t n) {
    f[1] = (uint8_t)(n >> 24); f[2] = (uint8_t)(n >> 16);
    f[3] = (uint8_t)(n >> 8);  f[4] = (uint8_t)n;
}

/* WIRE-01 */
void test_encode_decode_roundtrip(void) {
    const uint8_t payload[] = {0x0A, 0x03, 'a', 'b', 'c'};
    uint8_t frame[32];
    size_t frame_len = 0;
    TEST_ASSERT_TRUE(grpc_frame_encode(payload, sizeof payload, frame, sizeof frame, &frame_len));
    TEST_ASSERT_EQUAL_size_t(GRPC_FRAME_HEADER_LEN + sizeof payload, frame_len);
    const uint8_t expect_hdr[] = {0x00, 0x00, 0x00, 0x00, 0x05};
    TEST_ASSERT_EQUAL_UINT8_ARRAY(expect_hdr, frame, 5);

    const uint8_t *p = nullptr; size_t n = 0;
    TEST_ASSERT_EQUAL(grpc_frame_status_t::OK, grpc_frame_decode(frame, frame_len, &p, &n));
    TEST_ASSERT_EQUAL_size_t(sizeof payload, n);
    TEST_ASSERT_EQUAL_UINT8_ARRAY(payload, p, n);
}

/* WIRE-02 */
void test_encode_rejects_small_buffer(void) {
    const uint8_t payload[4] = {0};
    uint8_t frame[8];
    size_t frame_len = 0;
    TEST_ASSERT_FALSE(grpc_frame_encode(payload, sizeof payload, frame, sizeof frame, &frame_len));
    TEST_ASSERT_FALSE(grpc_frame_encode(payload, 0, frame, 4, &frame_len));
}

/* WIRE-03 */
void test_decode_rejects_short_compressed_and_truncated(void) {
    uint8_t f[8] = {0x00, 0, 0, 0, 3, 1, 2, 3};
    const uint8_t *p = nullptr; size_t n = 0;
    TEST_ASSERT_EQUAL(grpc_frame_status_t::SHORT_HEADER, grpc_frame_decode(f, 4, &p, &n));
    f[0] = 0x01;
    TEST_ASSERT_EQUAL(grpc_frame_status_t::COMPRESSED, grpc_frame_decode(f, 8, &p, &n));
    f[0] = 0x00; put_len(f, 4);
    TEST_ASSERT_EQUAL(grpc_frame_status_t::TRUNCATED, grpc_frame_decode(f, 8, &p, &n));
}

/* WIRE-04: F2 regression. A declared length near UINT32_MAX wrapped the old
 * `HEADER + msg_len > frame_len` check on 32-bit size_t and was accepted. */
void test_decode_rejects_length_that_wraps_32bit_size_t(void) {
    const uint32_t lens[] = {0xFFFFFFFBu, 0xFFFFFFFCu, 0xFFFFFFFFu, 0x80000000u};
    for (size_t i = 0; i < sizeof lens / sizeof lens[0]; ++i) {
        uint8_t f[16] = {0};
        put_len(f, lens[i]);
        const uint8_t *p = nullptr; size_t n = 0;
        TEST_ASSERT_EQUAL_MESSAGE(grpc_frame_status_t::TRUNCATED,
                                  grpc_frame_decode(f, sizeof f, &p, &n), "wrapped length accepted");
        TEST_ASSERT_EQUAL_size_t(0, n);
    }
}

/* WIRE-05: trailing bytes after one message are tolerated today; pinned so a
 * change of policy is deliberate. */
void test_decode_ignores_trailing_bytes(void) {
    uint8_t f[10] = {0x00, 0, 0, 0, 2, 0xAA, 0xBB, 0xCC, 0xDD, 0xEE};
    const uint8_t *p = nullptr; size_t n = 0;
    TEST_ASSERT_EQUAL(grpc_frame_status_t::OK, grpc_frame_decode(f, sizeof f, &p, &n));
    TEST_ASSERT_EQUAL_size_t(2, n);
}

/* WIRE-06: empty body is a valid frame. */
void test_decode_zero_length_message(void) {
    uint8_t f[5] = {0x00, 0, 0, 0, 0};
    const uint8_t *p = nullptr; size_t n = 99;
    TEST_ASSERT_EQUAL(grpc_frame_status_t::OK, grpc_frame_decode(f, sizeof f, &p, &n));
    TEST_ASSERT_EQUAL_size_t(0, n);
}

/* WIRE-07: grpc-status parsing never maps garbage to OK (0). */
void test_parse_status(void) {
    TEST_ASSERT_EQUAL_INT(0,  grpc_parse_status((const uint8_t *)"0", 1));
    TEST_ASSERT_EQUAL_INT(16, grpc_parse_status((const uint8_t *)"16", 2));
    TEST_ASSERT_EQUAL_INT(-1, grpc_parse_status((const uint8_t *)"", 0));
    TEST_ASSERT_EQUAL_INT(-1, grpc_parse_status((const uint8_t *)"ok", 2));
    TEST_ASSERT_EQUAL_INT(-1, grpc_parse_status((const uint8_t *)"0x", 2));
    TEST_ASSERT_EQUAL_INT(-1, grpc_parse_status((const uint8_t *)"17", 2));
    TEST_ASSERT_EQUAL_INT(-1, grpc_parse_status((const uint8_t *)"-1", 2));
    TEST_ASSERT_EQUAL_INT(-1, grpc_parse_status((const uint8_t *)"000", 3));
    TEST_ASSERT_EQUAL_INT(-1, grpc_parse_status(nullptr, 1));
}

/* WIRE-08: backoff schedule matches the pre-extraction behaviour. */
void test_backoff_doubles_and_caps(void) {
    uint32_t st = 0;
    const uint32_t expect[] = {1000, 2000, 4000, 8000, 16000, 30000, 30000};
    for (size_t i = 0; i < sizeof expect / sizeof expect[0]; ++i) {
        TEST_ASSERT_EQUAL_UINT32(expect[i], grpc_backoff_on_failure(&st, 1000, 30000));
    }
}

/* WIRE-09 */
void test_backoff_disabled_and_inverted_bounds(void) {
    uint32_t st = 0;
    TEST_ASSERT_EQUAL_UINT32(0, grpc_backoff_on_failure(&st, 0, 30000));
    TEST_ASSERT_EQUAL_UINT32(0, st);
    st = 0;  /* max < min clamps to min */
    TEST_ASSERT_EQUAL_UINT32(5000, grpc_backoff_on_failure(&st, 5000, 1000));
    TEST_ASSERT_EQUAL_UINT32(5000, grpc_backoff_on_failure(&st, 5000, 1000));
}

/* WIRE-10: no uint32 overflow at the top of the Kconfig range. */
void test_backoff_large_bounds_do_not_overflow(void) {
    uint32_t st = 0;
    uint32_t last = 0;
    for (int i = 0; i < 40; ++i) last = grpc_backoff_on_failure(&st, 60000, 300000);
    TEST_ASSERT_EQUAL_UINT32(300000, last);
}

int main(void) {
    UNITY_BEGIN();
    RUN_TEST(test_encode_decode_roundtrip);
    RUN_TEST(test_encode_rejects_small_buffer);
    RUN_TEST(test_decode_rejects_short_compressed_and_truncated);
    RUN_TEST(test_decode_rejects_length_that_wraps_32bit_size_t);
    RUN_TEST(test_decode_ignores_trailing_bytes);
    RUN_TEST(test_decode_zero_length_message);
    RUN_TEST(test_parse_status);
    RUN_TEST(test_backoff_doubles_and_caps);
    RUN_TEST(test_backoff_disabled_and_inverted_bounds);
    RUN_TEST(test_backoff_large_bounds_do_not_overflow);
    return UNITY_END();
}
