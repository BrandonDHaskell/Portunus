/* Unit tier: credential_types pure functions.
 * No ESP-IDF, no FreeRTOS, no sdkconfig. Bare host compiler. */
#include "unity.h"
#include "credential_types.h"
#include <string.h>

void setUp(void) {}
void tearDown(void) {}

static credential_t make(const uint8_t *uid, uint8_t len) {
    credential_t c = {0};
    memcpy(c.uid, uid, len);
    c.uid_len = len;
    return c;
}

/* ── credential_uid_to_hex ────────────────────────────────────────────────── */

void test_hex_three_bytes(void) {
    const uint8_t uid[] = {0x04, 0xA3, 0x2B};
    credential_t c = make(uid, 3);
    char buf[CREDENTIAL_UID_HEX_STR_LEN];
    credential_uid_to_hex(&c, buf, sizeof(buf));
    TEST_ASSERT_EQUAL_STRING("04:A3:2B", buf);
}

void test_hex_single_byte(void) {
    const uint8_t uid[] = {0xFF};
    credential_t c = make(uid, 1);
    char buf[CREDENTIAL_UID_HEX_STR_LEN];
    credential_uid_to_hex(&c, buf, sizeof(buf));
    TEST_ASSERT_EQUAL_STRING("FF", buf);
}

void test_hex_seven_byte_uid(void) {
    const uint8_t uid[] = {0x04, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66};
    credential_t c = make(uid, 7);
    char buf[CREDENTIAL_UID_HEX_STR_LEN];
    credential_uid_to_hex(&c, buf, sizeof(buf));
    TEST_ASSERT_EQUAL_STRING("04:11:22:33:44:55:66", buf);
}

void test_hex_truncates_into_small_buffer_without_overflow(void) {
    const uint8_t uid[] = {0x04, 0xA3};
    credential_t c = make(uid, 2);
    char buf[4] = {'X', 'X', 'X', 'X'};
    credential_uid_to_hex(&c, buf, sizeof(buf));  /* room for "04" + NUL only */
    TEST_ASSERT_EQUAL_STRING("04", buf);
    TEST_ASSERT_EQUAL_CHAR('\0', buf[2]);
}

/* ── credential_uid_to_log_id ─────────────────────────────────────────────── */
/*
 * FNV-1a 32-bit expected values computed independently (not from the
 * implementation) using:
 *
 *   def fnv1a(uid):
 *       h = 2166136261
 *       for b in uid: h = ((h ^ b) * 16777619) & 0xFFFFFFFF
 *       return f"{h:08x}"
 *
 *   {0x04}               → 010c56d3
 *   {0x04, 0xA3, 0x2B}   → c72117a1
 *   {0xDE, 0xAD, 0xBE, 0xEF} → 045d4bb3
 *   {0x04, 0x11, 0x22, ..., 0x99} (10 bytes) → 688fa0ae
 */

void test_log_id_known_vector_single_byte(void) {
    const uint8_t uid[] = {0x04};
    credential_t c = make(uid, 1);
    char buf[CREDENTIAL_LOG_ID_LEN];
    credential_uid_to_log_id(&c, buf, sizeof(buf));
    TEST_ASSERT_EQUAL_STRING("010c56d3", buf);
}

void test_log_id_known_vector_three_bytes(void) {
    const uint8_t uid[] = {0x04, 0xA3, 0x2B};
    credential_t c = make(uid, 3);
    char buf[CREDENTIAL_LOG_ID_LEN];
    credential_uid_to_log_id(&c, buf, sizeof(buf));
    TEST_ASSERT_EQUAL_STRING("c72117a1", buf);
}

void test_log_id_known_vector_four_bytes(void) {
    const uint8_t uid[] = {0xDE, 0xAD, 0xBE, 0xEF};
    credential_t c = make(uid, 4);
    char buf[CREDENTIAL_LOG_ID_LEN];
    credential_uid_to_log_id(&c, buf, sizeof(buf));
    TEST_ASSERT_EQUAL_STRING("045d4bb3", buf);
}

void test_log_id_empty_uid_yields_empty_string(void) {
    credential_t c = {0};  /* uid_len == 0 */
    char buf[CREDENTIAL_LOG_ID_LEN];
    memset(buf, 'X', sizeof(buf));
    credential_uid_to_log_id(&c, buf, sizeof(buf));
    TEST_ASSERT_EQUAL_STRING("", buf);
}

void test_log_id_undersized_buffer_is_safe(void) {
    const uint8_t uid[] = {0x04};
    credential_t c = make(uid, 1);
    char buf[4];
    memset(buf, 'X', sizeof(buf));
    credential_uid_to_log_id(&c, buf, sizeof(buf));  /* < CREDENTIAL_LOG_ID_LEN */
    TEST_ASSERT_EQUAL_CHAR('\0', buf[0]);
}

void test_log_id_is_lowercase_and_distinct(void) {
    const uint8_t a[] = {0x04, 0xA3, 0x2B};
    const uint8_t b[] = {0x04, 0xA3, 0x2C};  /* one bit different */
    credential_t ca = make(a, 3), cb = make(b, 3);
    char ba[CREDENTIAL_LOG_ID_LEN], bb[CREDENTIAL_LOG_ID_LEN];
    credential_uid_to_log_id(&ca, ba, sizeof(ba));
    credential_uid_to_log_id(&cb, bb, sizeof(bb));
    TEST_ASSERT_NOT_EQUAL(0, strcmp(ba, bb));
    for (int i = 0; i < 8; i++) {
        char ch = ba[i];
        TEST_ASSERT_TRUE((ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f'));
    }
}

/* ── Boundaries and F9 hardening ──────────────────────────────────────────── */

static const uint8_t UID10[] = {0x04, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99};

/* CT-11: a 10-byte UID formats to 29 chars, which with the NUL fills the
 * 30-byte AccessRequest.credential_id (COMP-03 asserts the proto side). */
void test_hex_ten_byte_uid_is_29_chars(void) {
    credential_t c = make(UID10, 10);
    char buf[CREDENTIAL_UID_HEX_STR_LEN];
    credential_uid_to_hex(&c, buf, sizeof(buf));
    TEST_ASSERT_EQUAL_STRING("04:11:22:33:44:55:66:77:88:99", buf);
    TEST_ASSERT_EQUAL_size_t(29, strlen(buf));
}

/* CT-12 (F9): buf_len == 0 must not touch the buffer. */
void test_hex_zero_length_buffer_writes_nothing(void) {
    const uint8_t uid[] = {0x04};
    credential_t c = make(uid, 1);
    char buf[1] = {'X'};
    credential_uid_to_hex(&c, buf, 0);
    TEST_ASSERT_EQUAL_CHAR('X', buf[0]);
}

/* CT-13 */
void test_hex_empty_uid_yields_empty_string(void) {
    credential_t c = {0};
    char buf[CREDENTIAL_UID_HEX_STR_LEN];
    memset(buf, 'X', sizeof(buf));
    credential_uid_to_hex(&c, buf, sizeof(buf));
    TEST_ASSERT_EQUAL_STRING("", buf);
}

/* CT-14: truncation is byte-granular, never a split byte. */
void test_hex_truncation_boundaries(void) {
    const uint8_t uid[] = {0x04, 0xA3};
    credential_t c = make(uid, 2);
    const struct { size_t len; const char *expect; } cases[] = {
        {1, ""}, {3, ""}, {4, "04"}, {5, "04"}, {6, "04:A3"},
    };
    for (size_t i = 0; i < sizeof cases / sizeof cases[0]; i++) {
        char buf[8];
        memset(buf, 'X', sizeof(buf));
        credential_uid_to_hex(&c, buf, cases[i].len);
        TEST_ASSERT_EQUAL_STRING(cases[i].expect, buf);
    }
}

/* CT-15 (F9): an out-of-range uid_len is clamped to CREDENTIAL_UID_MAX_LEN
 * instead of reading past uid[] (host-asan catches a regression). */
void test_oversized_uid_len_is_clamped(void) {
    credential_t c = make(UID10, 10);
    c.uid_len = 255;
    char log_id[CREDENTIAL_LOG_ID_LEN];
    credential_uid_to_log_id(&c, log_id, sizeof(log_id));
    TEST_ASSERT_EQUAL_STRING("688fa0ae", log_id);   /* same as uid_len = 10 */

    char hex[CREDENTIAL_UID_HEX_STR_LEN];
    credential_uid_to_hex(&c, hex, sizeof(hex));
    TEST_ASSERT_EQUAL_STRING("04:11:22:33:44:55:66:77:88:99", hex);
}

/* CT-16: independent vector (see the fnv1a snippet above). */
void test_log_id_known_vector_ten_bytes(void) {
    credential_t c = make(UID10, 10);
    char buf[CREDENTIAL_LOG_ID_LEN];
    credential_uid_to_log_id(&c, buf, sizeof(buf));
    TEST_ASSERT_EQUAL_STRING("688fa0ae", buf);
}

/* CT-17 */
void test_log_id_buffer_size_boundary(void) {
    const uint8_t uid[] = {0x04};
    credential_t c = make(uid, 1);
    char buf[CREDENTIAL_LOG_ID_LEN];
    memset(buf, 'X', sizeof(buf));
    credential_uid_to_log_id(&c, buf, CREDENTIAL_LOG_ID_LEN);
    TEST_ASSERT_EQUAL_STRING("010c56d3", buf);
    memset(buf, 'X', sizeof(buf));
    credential_uid_to_log_id(&c, buf, CREDENTIAL_LOG_ID_LEN - 1);
    TEST_ASSERT_EQUAL_STRING("", buf);
}

int main(void) {
    UNITY_BEGIN();
    RUN_TEST(test_hex_three_bytes);
    RUN_TEST(test_hex_single_byte);
    RUN_TEST(test_hex_seven_byte_uid);
    RUN_TEST(test_hex_truncates_into_small_buffer_without_overflow);
    RUN_TEST(test_log_id_known_vector_single_byte);
    RUN_TEST(test_log_id_known_vector_three_bytes);
    RUN_TEST(test_log_id_known_vector_four_bytes);
    RUN_TEST(test_log_id_empty_uid_yields_empty_string);
    RUN_TEST(test_log_id_undersized_buffer_is_safe);
    RUN_TEST(test_log_id_is_lowercase_and_distinct);
    RUN_TEST(test_hex_ten_byte_uid_is_29_chars);
    RUN_TEST(test_hex_zero_length_buffer_writes_nothing);
    RUN_TEST(test_hex_empty_uid_yields_empty_string);
    RUN_TEST(test_hex_truncation_boundaries);
    RUN_TEST(test_oversized_uid_len_is_clamped);
    RUN_TEST(test_log_id_known_vector_ten_bytes);
    RUN_TEST(test_log_id_buffer_size_boundary);
    return UNITY_END();
}
