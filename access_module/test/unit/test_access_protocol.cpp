/* Unit tier: server_comm's device-protocol logic (access_protocol).
 * No ESP-IDF, no mbedTLS, no Nanopb, no sdkconfig. Bare host compiler.
 *
 * The response-projection golden vector is shared with
 * server/internal/devicesig/devicesig_test.go. Change one, change both
 * (CLAUDE.md hazard #5). */
#include "unity.h"
#include "access_protocol.hpp"
#include "error_codes.hpp"

#include <ctype.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>
#include <string>

/* Production sets TZ=UTC0 in server_comm_init; tests start from the same. */
void setUp(void) {
    setenv("TZ", "UTC0", 1);
    tzset();
}
void tearDown(void) {}

static const char *MODULE = "door-001";
static const char *CRED   = "04:A3:2B:1C";
static const char *NONCE  = "000102030405060708090a0b0c0d0e0f";
static const char *GOLDEN_GRANT = "access_resp|door-001|04:A3:2B:1C|000102030405060708090a0b0c0d0e0f|1";
static const char *GOLDEN_DENY  = "access_resp|door-001|04:A3:2B:1C|000102030405060708090a0b0c0d0e0f|0";

/* ── A stand-in HMAC: keyed, input-sensitive, 64 lowercase hex chars ──────── */

static bool g_hmac_fails = false;

static bool fake_hmac(const uint8_t *data, size_t len, char out[PROTO_HMAC_HEX_CHARS + 1], void *) {
    if (g_hmac_fails) return false;
    static const char digits[] = "0123456789abcdef";
    for (int round = 0; round < 4; ++round) {
        uint64_t h = 1469598103934665603ULL ^ (0x9E3779B97F4A7C15ULL * (round + 1));
        for (size_t i = 0; i < len; ++i) { h ^= data[i]; h *= 1099511628211ULL; }
        for (int k = 0; k < 16; ++k) out[round * 16 + k] = digits[(h >> (60 - 4 * k)) & 0xF];
    }
    out[PROTO_HMAC_HEX_CHARS] = '\0';
    return true;
}

/* The signature the server would send for this decision. */
static std::string server_sig(const char *module, const char *cred, const char *nonce, bool granted) {
    char proj[PROTO_RESPONSE_PROJECTION_CAP];
    TEST_ASSERT_TRUE(access_response_projection(proj, sizeof proj, module, cred, nonce, granted));
    char sig[PROTO_HMAC_HEX_CHARS + 1];
    TEST_ASSERT_TRUE(fake_hmac(reinterpret_cast<const uint8_t *>(proj), strlen(proj), sig, nullptr));
    return sig;
}

static const char *check(const std::string &sig, const char *module, const char *cred,
                         const char *nonce, bool granted) {
    return access_check_response_sig(fake_hmac, nullptr, sig.c_str(), module, cred, nonce, granted);
}

/* ── Request projections ──────────────────────────────────────────────────── */

/* PROTO-01 */
void test_heartbeat_projection(void) {
    char buf[64];
    TEST_ASSERT_TRUE(proto_heartbeat_projection(buf, sizeof buf, MODULE, 42));
    TEST_ASSERT_EQUAL_STRING("heartbeat|door-001|42", buf);
    TEST_ASSERT_TRUE(proto_heartbeat_projection(buf, sizeof buf, MODULE, UINT32_MAX));
    TEST_ASSERT_EQUAL_STRING("heartbeat|door-001|4294967295", buf);
}

/* PROTO-02: the nonce is 32 lowercase hex chars; requested_at may be empty
 * (before the first clock sync) or populated. */
void test_access_projection(void) {
    const uint8_t nonce[16] = {0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07,
                               0x08, 0x09, 0xAB, 0xCD, 0xEF, 0x0D, 0x0E, 0x0F};
    char hex[33];
    TEST_ASSERT_TRUE(proto_hex_lower(nonce, sizeof nonce, hex, sizeof hex));
    TEST_ASSERT_EQUAL_size_t(32, strlen(hex));
    TEST_ASSERT_EQUAL_STRING("00010203040506070809abcdef0d0e0f", hex);

    char buf[192];
    TEST_ASSERT_TRUE(proto_access_projection(buf, sizeof buf, MODULE, CRED, hex, ""));
    TEST_ASSERT_EQUAL_STRING("access|door-001|04:A3:2B:1C|00010203040506070809abcdef0d0e0f|", buf);
    TEST_ASSERT_TRUE(proto_access_projection(buf, sizeof buf, MODULE, CRED, hex,
                                             "2026-01-02T03:04:05.000006Z"));
    TEST_ASSERT_EQUAL_STRING(
        "access|door-001|04:A3:2B:1C|00010203040506070809abcdef0d0e0f|2026-01-02T03:04:05.000006Z", buf);

    char small[16];
    TEST_ASSERT_FALSE(proto_hex_lower(nonce, sizeof nonce, small, sizeof small));
    TEST_ASSERT_EQUAL_CHAR('\0', small[0]);
}

/* PROTO-03: lowercase hex of the raw UID bytes. */
void test_provision_projection(void) {
    const uint8_t uid4[]  = {0x04, 0xA3, 0x2B, 0x1C};
    const uint8_t uid10[] = {0x04, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99};
    char buf[128];
    TEST_ASSERT_TRUE(proto_provision_projection(buf, sizeof buf, MODULE, uid4, sizeof uid4));
    TEST_ASSERT_EQUAL_STRING("provision|door-001|04a32b1c", buf);
    TEST_ASSERT_TRUE(proto_provision_projection(buf, sizeof buf, MODULE, uid10, sizeof uid10));
    TEST_ASSERT_EQUAL_STRING("provision|door-001|04112233445566778899", buf);
    TEST_ASSERT_TRUE(proto_provision_projection(buf, sizeof buf, MODULE, nullptr, 0));
    TEST_ASSERT_EQUAL_STRING("provision|door-001|", buf);
}

/* Every builder refuses NULL arguments and truncation, leaving "". */
void test_projections_refuse_null_and_truncation(void) {
    char buf[160] = "stale";
    TEST_ASSERT_FALSE(proto_heartbeat_projection(buf, sizeof buf, nullptr, 1));
    TEST_ASSERT_EQUAL_CHAR('\0', buf[0]);
    TEST_ASSERT_FALSE(proto_access_projection(buf, sizeof buf, MODULE, CRED, NONCE, nullptr));
    TEST_ASSERT_FALSE(proto_provision_projection(buf, sizeof buf, nullptr, nullptr, 0));
    TEST_ASSERT_FALSE(access_response_projection(buf, sizeof buf, MODULE, nullptr, NONCE, true));
    TEST_ASSERT_FALSE(access_response_projection(nullptr, 0, MODULE, CRED, NONCE, true));

    char tiny[10] = "stale";
    TEST_ASSERT_FALSE(proto_heartbeat_projection(tiny, sizeof tiny, MODULE, 42));
    TEST_ASSERT_EQUAL_CHAR('\0', tiny[0]);

    const size_t len = strlen(GOLDEN_GRANT);
    std::string exact(len + 1, 'x');
    TEST_ASSERT_TRUE(access_response_projection(&exact[0], len + 1, MODULE, CRED, NONCE, true));
    std::string shy(len, 'x');
    TEST_ASSERT_FALSE(access_response_projection(&shy[0], len, MODULE, CRED, NONCE, true));
    TEST_ASSERT_EQUAL_CHAR('\0', shy[0]);
}

/* ── Response projection and signature check ─────────────────────────────── */

/* PROTO-04 / PROTO-20 (D1): golden vector, bound to the nonce. */
void test_response_projection_golden(void) {
    char buf[PROTO_RESPONSE_PROJECTION_CAP];
    TEST_ASSERT_TRUE(access_response_projection(buf, sizeof buf, MODULE, CRED, NONCE, true));
    TEST_ASSERT_EQUAL_STRING(GOLDEN_GRANT, buf);
    TEST_ASSERT_TRUE(access_response_projection(buf, sizeof buf, MODULE, CRED, NONCE, false));
    TEST_ASSERT_EQUAL_STRING(GOLDEN_DENY, buf);

    char other[PROTO_RESPONSE_PROJECTION_CAP];
    TEST_ASSERT_TRUE(access_response_projection(other, sizeof other, MODULE, CRED,
                                                "ffffffffffffffffffffffffffffffff", false));
    TEST_ASSERT_NOT_EQUAL(0, strcmp(buf, other));

    /* The longest inputs Nanopb allows fit PROTO_RESPONSE_PROJECTION_CAP. */
    const std::string module(32, 'm');
    TEST_ASSERT_TRUE(access_response_projection(buf, sizeof buf, module.c_str(),
                                                "00:11:22:33:44:55:66:77:88:99", NONCE, true));
}

/* PROTO-05 / 06: a correct signature verifies for a grant and for a deny. */
void test_valid_signature_verifies(void) {
    TEST_ASSERT_NULL(check(server_sig(MODULE, CRED, NONCE, true), MODULE, CRED, NONCE, true));
    TEST_ASSERT_NULL(check(server_sig(MODULE, CRED, NONCE, false), MODULE, CRED, NONCE, false));
}

/* PROTO-07 */
void test_missing_signature(void) {
    TEST_ASSERT_EQUAL_STRING("missing_response_sig",
        access_check_response_sig(fake_hmac, nullptr, nullptr, MODULE, CRED, NONCE, true));
    TEST_ASSERT_EQUAL_STRING("missing_response_sig", check("", MODULE, CRED, NONCE, true));
}

/* PROTO-08: a signed deny presented as a grant. */
void test_signature_for_deny_cannot_grant(void) {
    TEST_ASSERT_EQUAL_STRING("invalid_response_sig",
                             check(server_sig(MODULE, CRED, NONCE, false), MODULE, CRED, NONCE, true));
}

/* PROTO-09 / 20: a signature for another credential, module or request. */
void test_signature_for_other_request(void) {
    const std::string sig = server_sig(MODULE, CRED, NONCE, true);
    TEST_ASSERT_EQUAL_STRING("invalid_response_sig", check(sig, MODULE, "04:A3:2B:1D", NONCE, true));
    TEST_ASSERT_EQUAL_STRING("invalid_response_sig", check(sig, "door-002", CRED, NONCE, true));
    TEST_ASSERT_EQUAL_STRING("invalid_response_sig",
                             check(sig, MODULE, CRED, "ffffffffffffffffffffffffffffffff", true));
}

/* PROTO-10: only the exact 64 lowercase hex chars verify. */
void test_signature_format_is_exact(void) {
    const std::string sig = server_sig(MODULE, CRED, NONCE, true);
    std::string upper = sig;
    for (char &c : upper) c = static_cast<char>(toupper(static_cast<unsigned char>(c)));
    TEST_ASSERT_NOT_EQUAL(0, strcmp(upper.c_str(), sig.c_str()));
    TEST_ASSERT_EQUAL_STRING("invalid_response_sig", check(upper, MODULE, CRED, NONCE, true));
    TEST_ASSERT_EQUAL_STRING("invalid_response_sig", check(sig.substr(0, 63), MODULE, CRED, NONCE, true));
    TEST_ASSERT_EQUAL_STRING("invalid_response_sig", check(sig + "0", MODULE, CRED, NONCE, true));
}

/* A failing or absent HMAC never verifies. */
void test_hmac_failure_denies(void) {
    const std::string sig = server_sig(MODULE, CRED, NONCE, true);
    g_hmac_fails = true;
    TEST_ASSERT_EQUAL_STRING("sig_compute_error", check(sig, MODULE, CRED, NONCE, true));
    g_hmac_fails = false;
    TEST_ASSERT_EQUAL_STRING("sig_compute_error",
        access_check_response_sig(nullptr, nullptr, sig.c_str(), MODULE, CRED, NONCE, true));
    TEST_ASSERT_FALSE(proto_sig_hex_equal(nullptr, sig.c_str()));
}

/* ── Replies to events ────────────────────────────────────────────────────── */

/* PROTO-11: the decision carries the log fingerprint and a reason truncated
 * to the event's 32 chars. */
void test_fill_decision(void) {
    event_access_decision_t d;
    memset(&d, 0xA5, sizeof d);
    const std::string long_reason(40, 'r');
    proto_fill_decision(&d, "1a2b3c4d", true, true, long_reason.c_str());
    TEST_ASSERT_EQUAL_STRING("1a2b3c4d", d.credential_id);
    TEST_ASSERT_EQUAL_STRING(std::string(sizeof d.reason - 1, 'r').c_str(), d.reason);
    TEST_ASSERT_TRUE(d.granted);
    TEST_ASSERT_TRUE(d.known);

    proto_fill_decision(&d, nullptr, false, false, nullptr);
    TEST_ASSERT_EQUAL_STRING("", d.credential_id);
    TEST_ASSERT_EQUAL_STRING("", d.reason);
    TEST_ASSERT_FALSE(d.granted);
}

/* PROTO-12 */
void test_provision_status_mapping(void) {
    TEST_ASSERT_EQUAL(PROVISION_RESULT_PENDING_CREATED,    proto_provision_reason(7));
    TEST_ASSERT_EQUAL(PROVISION_RESULT_DUPLICATE_ACTIVE,   proto_provision_reason(2));
    TEST_ASSERT_EQUAL(PROVISION_RESULT_DUPLICATE_INACTIVE, proto_provision_reason(3));
    TEST_ASSERT_EQUAL(PROVISION_RESULT_DUPLICATE_PENDING,  proto_provision_reason(4));
    TEST_ASSERT_EQUAL(PROVISION_RESULT_UNAUTHORIZED,       proto_provision_reason(5));
    for (const int s : {0, 1, 6, 8, -1, 99}) {
        TEST_ASSERT_EQUAL(PROVISION_RESULT_COMM_ERROR, proto_provision_reason(s));
    }
}

/* ── Wire timestamps ──────────────────────────────────────────────────────── */

static time_t utc(int y, int mo, int d, int h, int mi, int s) {
    struct tm tm = {};
    tm.tm_year = y - 1900; tm.tm_mon = mo - 1; tm.tm_mday = d;
    tm.tm_hour = h; tm.tm_min = mi; tm.tm_sec = s;
    return timegm(&tm);
}

/* PROTO-13: with and without a fraction, 1 to 9 fractional digits. */
void test_parse_server_time(void) {
    struct timeval tv;
    TEST_ASSERT_TRUE(proto_parse_server_time("2026-01-02T03:04:05Z", &tv));
    TEST_ASSERT_EQUAL_INT64(utc(2026, 1, 2, 3, 4, 5), tv.tv_sec);
    TEST_ASSERT_EQUAL_INT64(0, tv.tv_usec);

    const struct { const char *s; long usec; } fracs[] = {
        {"2026-01-02T03:04:05.1Z", 100000},
        {"2026-01-02T03:04:05.12Z", 120000},
        {"2026-01-02T03:04:05.123456Z", 123456},
        {"2026-01-02T03:04:05.1234567Z", 123456},
        {"2026-01-02T03:04:05.123456789Z", 123456},
    };
    for (const auto &f : fracs) {
        TEST_ASSERT_TRUE_MESSAGE(proto_parse_server_time(f.s, &tv), f.s);
        TEST_ASSERT_EQUAL_INT64_MESSAGE(utc(2026, 1, 2, 3, 4, 5), tv.tv_sec, f.s);
        TEST_ASSERT_EQUAL_INT64_MESSAGE(f.usec, tv.tv_usec, f.s);
    }

    TEST_ASSERT_TRUE(proto_parse_server_time("2028-02-29T23:59:59Z", &tv));   /* leap day */
    TEST_ASSERT_EQUAL_INT64(utc(2028, 2, 29, 23, 59, 59), tv.tv_sec);
    TEST_ASSERT_FALSE(proto_parse_server_time("", &tv));
    TEST_ASSERT_FALSE(proto_parse_server_time(nullptr, &tv));
    TEST_ASSERT_FALSE(proto_parse_server_time("not a time", &tv));
}

/* PROTO-14 (F7): anything but a valid UTC timestamp is rejected instead of
 * being normalised or read as UTC. */
void test_parse_rejects_malformed(void) {
    struct timeval tv;
    const char *bad[] = {
        "2026-13-45T99:99:99Z",            /* out-of-range fields */
        "2026-02-29T00:00:00Z",            /* not a leap year */
        "2026-04-31T00:00:00Z",
        "2026-01-02T24:00:00Z",
        "2026-01-02T03:04:60Z",
        "2026-01-02T03:04:05+02:00",       /* offset */
        "2026-01-02T03:04:05-07:00",
        "2026-01-02T03:04:05",             /* no Z */
        "2026-01-02T03:04:05.123",
        "2026-01-02T03:04:05ZX",           /* trailing bytes */
        "2026-01-02T03:04:05.Z",           /* empty fraction */
        "2026-01-02T03:04:05.1234567890Z", /* ten digits */
        "2026-1-02T03:04:05Z",             /* not zero-padded */
        "2026-01-02 03:04:05Z",
        "2100-02-29T00:00:00Z",            /* century, not a leap year */
        "1969-12-31T23:59:59Z",            /* before the epoch */
    };
    for (const char *s : bad) {
        TEST_ASSERT_FALSE_MESSAGE(proto_parse_server_time(s, &tv), s);
    }
}

/* The date arithmetic agrees with libc's timegm on every day from 1970 to
 * 2100 (leap years and century rules included), and 2000-02-29 parses. */
void test_parse_matches_timegm_every_day(void) {
    struct timeval tv;
    TEST_ASSERT_TRUE(proto_parse_server_time("2000-02-29T00:00:00Z", &tv));
    char s[32];
    /* 32-bit time_t (the host-m32 preset) stops at 2037. */
    const int last_year = sizeof(time_t) >= 8 ? 2100 : 2037;
    for (time_t day = utc(1970, 1, 1, 12, 34, 56); day <= utc(last_year, 12, 31, 12, 34, 56);
         day += 86400) {
        struct tm tm;
        gmtime_r(&day, &tm);
        strftime(s, sizeof s, "%Y-%m-%dT%H:%M:%SZ", &tm);
        TEST_ASSERT_TRUE_MESSAGE(proto_parse_server_time(s, &tv), s);
        TEST_ASSERT_EQUAL_INT64_MESSAGE(day, tv.tv_sec, s);
    }
}

/* PROTO-15 (F7): the result does not depend on the process time zone. */
void test_parse_independent_of_tz(void) {
    struct timeval in_utc, in_la;
    TEST_ASSERT_TRUE(proto_parse_server_time("2026-07-04T12:00:00.5Z", &in_utc));
    setenv("TZ", "America/Los_Angeles", 1);
    tzset();
    TEST_ASSERT_TRUE(proto_parse_server_time("2026-07-04T12:00:00.5Z", &in_la));
    TEST_ASSERT_EQUAL_INT64(in_utc.tv_sec, in_la.tv_sec);
    TEST_ASSERT_EQUAL_INT64(in_utc.tv_usec, in_la.tv_usec);
}

/* PROTO-16: RTT/2 is added, carrying microseconds into seconds. */
void test_add_half_rtt(void) {
    struct timeval tv = {100, 999900};
    proto_add_half_rtt(&tv, 400);                 /* +200 us */
    TEST_ASSERT_EQUAL_INT64(101, tv.tv_sec);
    TEST_ASSERT_EQUAL_INT64(100, tv.tv_usec);

    tv = {100, 600000};
    proto_add_half_rtt(&tv, 3000000);             /* +1.5 s */
    TEST_ASSERT_EQUAL_INT64(102, tv.tv_sec);
    TEST_ASSERT_EQUAL_INT64(100000, tv.tv_usec);

    tv = {100, 5};
    proto_add_half_rtt(&tv, 0);
    TEST_ASSERT_EQUAL_INT64(100, tv.tv_sec);
    TEST_ASSERT_EQUAL_INT64(5, tv.tv_usec);
}

/* PROTO-17: requested_at formats as the server expects and parses back. */
void test_requested_at_round_trip(void) {
    const struct timeval tv = {utc(2026, 1, 2, 3, 4, 5), 123};
    char buf[32];
    TEST_ASSERT_TRUE(proto_format_requested_at(buf, sizeof buf, &tv));
    TEST_ASSERT_EQUAL_STRING("2026-01-02T03:04:05.000123Z", buf);
    struct timeval back;
    TEST_ASSERT_TRUE(proto_parse_server_time(buf, &back));
    TEST_ASSERT_EQUAL_INT64(tv.tv_sec, back.tv_sec);
    TEST_ASSERT_EQUAL_INT64(tv.tv_usec, back.tv_usec);

    char small[27] = "stale";                     /* needs 28 with the NUL */
    TEST_ASSERT_FALSE(proto_format_requested_at(small, sizeof small, &tv));
    TEST_ASSERT_EQUAL_CHAR('\0', small[0]);
}

int main(void) {
    UNITY_BEGIN();
    RUN_TEST(test_heartbeat_projection);
    RUN_TEST(test_access_projection);
    RUN_TEST(test_provision_projection);
    RUN_TEST(test_projections_refuse_null_and_truncation);
    RUN_TEST(test_response_projection_golden);
    RUN_TEST(test_valid_signature_verifies);
    RUN_TEST(test_missing_signature);
    RUN_TEST(test_signature_for_deny_cannot_grant);
    RUN_TEST(test_signature_for_other_request);
    RUN_TEST(test_signature_format_is_exact);
    RUN_TEST(test_hmac_failure_denies);
    RUN_TEST(test_fill_decision);
    RUN_TEST(test_provision_status_mapping);
    RUN_TEST(test_parse_server_time);
    RUN_TEST(test_parse_rejects_malformed);
    RUN_TEST(test_parse_matches_timegm_every_day);
    RUN_TEST(test_parse_independent_of_tz);
    RUN_TEST(test_add_half_rtt);
    RUN_TEST(test_requested_at_round_trip);
    return UNITY_END();
}
