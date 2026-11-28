#include "access_protocol.hpp"

#include <stdio.h>
#include <string.h>
#include <time.h>

/* snprintf into buf; false (and "") on NULL buf, encoding error or truncation. */
static bool fit(char *buf, size_t cap, int n)
{
    if (n < 0 || static_cast<size_t>(n) >= cap) {
        buf[0] = '\0';
        return false;
    }
    return true;
}

static bool usable(char *buf, size_t cap)
{
    if (buf == nullptr || cap == 0) {
        return false;
    }
    buf[0] = '\0';
    return true;
}

/* ── HMAC projections ─────────────────────────────────────────────────────── */

bool proto_heartbeat_projection(char *buf, size_t cap, const char *module_id, uint32_t sequence)
{
    if (!usable(buf, cap) || module_id == nullptr) {
        return false;
    }
    return fit(buf, cap, snprintf(buf, cap, "heartbeat|%s|%lu", module_id,
                                  static_cast<unsigned long>(sequence)));
}

bool proto_access_projection(char *buf, size_t cap, const char *module_id,
                             const char *credential_id, const char *nonce_hex,
                             const char *requested_at)
{
    if (!usable(buf, cap) || module_id == nullptr || credential_id == nullptr ||
        nonce_hex == nullptr || requested_at == nullptr) {
        return false;
    }
    return fit(buf, cap, snprintf(buf, cap, "access|%s|%s|%s|%s",
                                  module_id, credential_id, nonce_hex, requested_at));
}

bool proto_hex_lower(const uint8_t *bytes, size_t n, char *out, size_t cap)
{
    if (!usable(out, cap) || (bytes == nullptr && n > 0) || cap < 2 * n + 1) {
        return false;
    }
    static const char digits[] = "0123456789abcdef";
    for (size_t i = 0; i < n; ++i) {
        out[2 * i]     = digits[bytes[i] >> 4];
        out[2 * i + 1] = digits[bytes[i] & 0x0F];
    }
    out[2 * n] = '\0';
    return true;
}

bool proto_provision_projection(char *buf, size_t cap, const char *module_id,
                                const uint8_t *uid, size_t uid_len)
{
    if (!usable(buf, cap) || module_id == nullptr) {
        return false;
    }
    char uid_hex[2 * 32 + 1];
    if (uid_len > 32 || !proto_hex_lower(uid, uid_len, uid_hex, sizeof(uid_hex))) {
        return false;
    }
    return fit(buf, cap, snprintf(buf, cap, "provision|%s|%s", module_id, uid_hex));
}

bool access_response_projection(char *buf, size_t cap, const char *module_id,
                                const char *credential_id, const char *nonce_hex,
                                bool granted)
{
    if (!usable(buf, cap) || module_id == nullptr || credential_id == nullptr ||
        nonce_hex == nullptr) {
        return false;
    }
    return fit(buf, cap, snprintf(buf, cap, "access_resp|%s|%s|%s|%d",
                                  module_id, credential_id, nonce_hex, granted ? 1 : 0));
}

/* ── Access-decision signature ────────────────────────────────────────────── */

bool proto_sig_hex_equal(const char *a, const char *b)
{
    if (a == nullptr || b == nullptr) {
        return false;
    }
    /* Length first, so a short string is never read past its NUL. The length
     * of a signature is not secret. */
    if (strnlen(a, PROTO_HMAC_HEX_CHARS + 1) != PROTO_HMAC_HEX_CHARS ||
        strnlen(b, PROTO_HMAC_HEX_CHARS + 1) != PROTO_HMAC_HEX_CHARS) {
        return false;
    }
    uint8_t diff = 0;
    for (size_t i = 0; i < PROTO_HMAC_HEX_CHARS; ++i) {
        diff |= static_cast<uint8_t>(a[i]) ^ static_cast<uint8_t>(b[i]);
    }
    return diff == 0;
}

const char *access_check_response_sig(proto_hmac_fn hmac, void *hmac_ctx,
                                      const char *received_sig, const char *module_id,
                                      const char *credential_id, const char *nonce_hex,
                                      bool granted)
{
    if (received_sig == nullptr || received_sig[0] == '\0') {
        return "missing_response_sig";
    }
    char proj[PROTO_RESPONSE_PROJECTION_CAP];
    if (hmac == nullptr ||
        !access_response_projection(proj, sizeof(proj), module_id, credential_id,
                                    nonce_hex, granted)) {
        return "sig_compute_error";
    }
    char expected[PROTO_HMAC_HEX_CHARS + 1];
    if (!hmac(reinterpret_cast<const uint8_t *>(proj), strlen(proj), expected, hmac_ctx)) {
        return "sig_compute_error";
    }
    return proto_sig_hex_equal(received_sig, expected) ? nullptr : "invalid_response_sig";
}

/* ── Server replies to firmware events ────────────────────────────────────── */

void proto_fill_decision(event_access_decision_t *out, const char *log_id,
                         bool granted, bool known, const char *reason)
{
    memset(out, 0, sizeof(*out));
    if (log_id != nullptr) {
        strncpy(out->credential_id, log_id, sizeof(out->credential_id) - 1);
    }
    if (reason != nullptr) {
        strncpy(out->reason, reason, sizeof(out->reason) - 1);
    }
    out->granted = granted;
    out->known   = known;
}

provision_result_reason_t proto_provision_reason(int status)
{
    switch (status) {
    case PROTO_PROVISION_PENDING_CREATED:    return PROVISION_RESULT_PENDING_CREATED;
    case PROTO_PROVISION_DUPLICATE_ACTIVE:   return PROVISION_RESULT_DUPLICATE_ACTIVE;
    case PROTO_PROVISION_DUPLICATE_INACTIVE: return PROVISION_RESULT_DUPLICATE_INACTIVE;
    case PROTO_PROVISION_DUPLICATE_PENDING:  return PROVISION_RESULT_DUPLICATE_PENDING;
    case PROTO_PROVISION_UNAUTHORIZED:       return PROVISION_RESULT_UNAUTHORIZED;
    default:                                 return PROVISION_RESULT_COMM_ERROR;
    }
}

/* ── Wire timestamps ──────────────────────────────────────────────────────── */

/* Exactly n ASCII digits at s; stops at the first non-digit, so it never
 * reads past a NUL. */
static bool read_digits(const char *s, int n, int *out)
{
    int v = 0;
    for (int i = 0; i < n; ++i) {
        const unsigned char c = static_cast<unsigned char>(s[i]);
        if (c < '0' || c > '9') {
            return false;
        }
        v = v * 10 + (c - '0');
    }
    *out = v;
    return true;
}

static bool is_leap(int y)
{
    return (y % 4 == 0 && y % 100 != 0) || y % 400 == 0;
}

static int days_in_month(int y, int m)
{
    static const int days[12] = {31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31};
    return (m == 2 && is_leap(y)) ? 29 : days[m - 1];
}

/* Days from 1970-01-01 to y-m-d in the proleptic Gregorian calendar
 * (H. Hinnant, "chrono-Compatible Low-Level Date Algorithms"). */
static int64_t days_from_civil(int y, int m, int d)
{
    y -= m <= 2;
    const int64_t era = (y >= 0 ? y : y - 399) / 400;
    const int64_t yoe = y - era * 400;
    const int64_t doy = (153 * (m + (m > 2 ? -3 : 9)) + 2) / 5 + d - 1;
    const int64_t doe = yoe * 365 + yoe / 4 - yoe / 100 + doy;
    return era * 146097 + doe - 719468;
}

bool proto_parse_server_time(const char *s, struct timeval *out)
{
    /* Exactly "YYYY-MM-DDTHH:MM:SS", an optional fraction of 1 to 9 digits,
     * then "Z" and the end of the string: what the server's
     * time.Now().UTC().Format(time.RFC3339Nano) produces. Anything else
     * (offsets, out-of-range fields, trailing bytes) is rejected rather than
     * normalised, and UTC is computed from the fields directly, so the
     * result does not depend on the process TZ. */
    if (s == nullptr || out == nullptr) {
        return false;
    }
    int year, mon, mday, hour, min, sec;
    if (!read_digits(s, 4, &year) || s[4] != '-' ||
        !read_digits(s + 5, 2, &mon) || s[7] != '-' ||
        !read_digits(s + 8, 2, &mday) || s[10] != 'T' ||
        !read_digits(s + 11, 2, &hour) || s[13] != ':' ||
        !read_digits(s + 14, 2, &min) || s[16] != ':' ||
        !read_digits(s + 17, 2, &sec)) {
        return false;
    }
    if (year < 1970 || mon < 1 || mon > 12 || mday < 1 || mday > days_in_month(year, mon) ||
        hour > 23 || min > 59 || sec > 59) {
        return false;
    }
    /* A 32-bit time_t (not ESP-IDF 5, but a 32-bit host) would wrap after
     * 2038-01-19: refuse rather than return a wrong time. */
    if (sizeof(time_t) < 8 && year > 2037) {
        return false;
    }

    const char *p = s + 19;
    long usec = 0;
    if (*p == '.') {
        ++p;
        long ns = 0;
        int n = 0;
        while (n < 9 && *p >= '0' && *p <= '9') {
            ns = ns * 10 + (*p - '0');
            ++p;
            ++n;
        }
        if (n == 0 || (*p >= '0' && *p <= '9')) {
            return false;                  /* empty fraction, or more than 9 digits */
        }
        for (; n < 9; ++n) {
            ns *= 10;
        }
        usec = ns / 1000;
    }
    if (p[0] != 'Z' || p[1] != '\0') {
        return false;
    }

    out->tv_sec  = static_cast<time_t>(days_from_civil(year, mon, mday) * 86400 +
                                       int64_t{hour} * 3600 + int64_t{min} * 60 + sec);
    out->tv_usec = static_cast<suseconds_t>(usec);
    return true;
}

void proto_add_half_rtt(struct timeval *tv, int64_t rtt_us)
{
    const int64_t half = rtt_us / 2;
    tv->tv_usec += static_cast<suseconds_t>(half % 1000000LL);
    tv->tv_sec  += static_cast<time_t>(half / 1000000LL);
    if (tv->tv_usec >= 1000000L) {
        tv->tv_sec  += 1;
        tv->tv_usec -= 1000000L;
    }
}

bool proto_format_requested_at(char *buf, size_t cap, const struct timeval *tv)
{
    if (!usable(buf, cap) || tv == nullptr) {
        return false;
    }
    struct tm tm_utc;
    const time_t secs = tv->tv_sec;
    if (gmtime_r(&secs, &tm_utc) == nullptr) {
        return false;
    }
    char date[24];
    if (strftime(date, sizeof(date), "%Y-%m-%dT%H:%M:%S", &tm_utc) == 0) {
        return false;
    }
    return fit(buf, cap, snprintf(buf, cap, "%s.%06ldZ", date, static_cast<long>(tv->tv_usec)));
}
