/* Unit tier: the access response projection the module verifies (D1).
 * No ESP-IDF, no sdkconfig. Bare host compiler.
 *
 * GOLDEN is shared with TestAccessResponseProjection_GoldenVector in
 * server/internal/grpcapi/interceptors_test.go. Change one, change both
 * (CLAUDE.md hazard #5). */
#include "unity.h"
#include "access_projection.hpp"

#include <string.h>
#include <string>

void setUp(void) {}
void tearDown(void) {}

static const char *MODULE = "door-001";
static const char *CRED   = "04:A3:2B:1C";
static const char *NONCE  = "000102030405060708090a0b0c0d0e0f";
static const char *GOLDEN_GRANT = "access_resp|door-001|04:A3:2B:1C|000102030405060708090a0b0c0d0e0f|1";
static const char *GOLDEN_DENY  = "access_resp|door-001|04:A3:2B:1C|000102030405060708090a0b0c0d0e0f|0";

/* PROTO-20 (D1): the projection carries the request nonce and the decision,
 * under a prefix distinct from the request projection ("access|"). */
void test_golden_vector(void) {
    char buf[160];
    TEST_ASSERT_TRUE(access_response_projection(buf, sizeof buf, MODULE, CRED, NONCE, true));
    TEST_ASSERT_EQUAL_STRING(GOLDEN_GRANT, buf);
    TEST_ASSERT_TRUE(access_response_projection(buf, sizeof buf, MODULE, CRED, NONCE, false));
    TEST_ASSERT_EQUAL_STRING(GOLDEN_DENY, buf);
}

/* Two requests that differ only in nonce sign different strings, so a
 * signed grant for one cannot verify for the other. */
void test_nonce_changes_projection(void) {
    char a[160], b[160];
    TEST_ASSERT_TRUE(access_response_projection(a, sizeof a, MODULE, CRED, NONCE, true));
    TEST_ASSERT_TRUE(access_response_projection(b, sizeof b, MODULE, CRED,
                                                "ffffffffffffffffffffffffffffffff", true));
    TEST_ASSERT_NOT_EQUAL(0, strcmp(a, b));
}

/* A projection that does not fit is refused and leaves an empty string: the
 * caller denies rather than verifying a truncated prefix. Exactly-fitting
 * buffers succeed. */
void test_truncation_refused(void) {
    const size_t len = strlen(GOLDEN_GRANT);
    std::string buf(len + 1, 'x');
    TEST_ASSERT_TRUE(access_response_projection(&buf[0], len + 1, MODULE, CRED, NONCE, true));
    TEST_ASSERT_EQUAL_STRING(GOLDEN_GRANT, buf.c_str());

    std::string small(len, 'x');
    TEST_ASSERT_FALSE(access_response_projection(&small[0], len, MODULE, CRED, NONCE, true));
    TEST_ASSERT_EQUAL_CHAR('\0', small[0]);
}

/* The longest inputs nanopb allows (32-char module_id, 10-byte UID as hex)
 * fit the 160-byte buffer server_comm uses. */
void test_longest_inputs_fit(void) {
    const std::string module(32, 'm');
    const std::string cred = "00:11:22:33:44:55:66:77:88:99";   /* 29 chars */
    char buf[160];
    TEST_ASSERT_TRUE(access_response_projection(buf, sizeof buf, module.c_str(), cred.c_str(),
                                                NONCE, true));
    TEST_ASSERT_EQUAL_size_t(12 + 32 + 1 + 29 + 1 + 32 + 2, strlen(buf));
}

void test_null_arguments_refused(void) {
    char buf[160] = "stale";
    TEST_ASSERT_FALSE(access_response_projection(buf, sizeof buf, nullptr, CRED, NONCE, true));
    TEST_ASSERT_EQUAL_CHAR('\0', buf[0]);
    TEST_ASSERT_FALSE(access_response_projection(buf, sizeof buf, MODULE, nullptr, NONCE, true));
    TEST_ASSERT_FALSE(access_response_projection(buf, sizeof buf, MODULE, CRED, nullptr, true));
    TEST_ASSERT_FALSE(access_response_projection(nullptr, 0, MODULE, CRED, NONCE, true));
}

int main(void) {
    UNITY_BEGIN();
    RUN_TEST(test_golden_vector);
    RUN_TEST(test_nonce_changes_projection);
    RUN_TEST(test_truncation_refused);
    RUN_TEST(test_longest_inputs_fit);
    RUN_TEST(test_null_arguments_refused);
    return UNITY_END();
}
