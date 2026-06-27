/* Tier A host test: credential_types pure functions.
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
    return UNITY_END();
}
