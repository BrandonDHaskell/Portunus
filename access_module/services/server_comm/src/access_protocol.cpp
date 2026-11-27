#include "access_protocol.hpp"

#include <stdio.h>
#include <stdlib.h>
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

bool proto_parse_server_time(const char *s, struct timeval *out)
{
    if (s == nullptr || s[0] == '\0' || out == nullptr) {
        return false;
    }

    int year, mon, mday, hour, min, sec;
    if (sscanf(s, "%4d-%2d-%2dT%2d:%2d:%2d", &year, &mon, &mday, &hour, &min, &sec) < 6) {
        return false;
    }

    struct tm tm = {};
    tm.tm_year  = year - 1900;
    tm.tm_mon   = mon - 1;
    tm.tm_mday  = mday;
    tm.tm_hour  = hour;
    tm.tm_min   = min;
    tm.tm_sec   = sec;
    tm.tm_isdst = 0;

    /* Relies on the process TZ being UTC0 (set in server_comm_init). */
    const time_t t = mktime(&tm);
    if (t == (time_t)-1) {
        return false;
    }

    long usec = 0;
    const char *dot = strchr(s, '.');
    if (dot != nullptr) {
        char frac[10] = {'0', '0', '0', '0', '0', '0', '0', '0', '0', '\0'};
        int i = 0;
        const char *p = dot + 1;
        while (i < 9 && *p >= '0' && *p <= '9') {
            frac[i++] = *p++;
        }
        usec = strtol(frac, nullptr, 10) / 1000;  /* ns to us */
    }

    out->tv_sec  = t;
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
