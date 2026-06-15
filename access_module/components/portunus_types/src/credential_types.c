#include "credential_types.h"

#include <stdint.h>

void credential_uid_to_hex(const credential_t *cred, char *buf, size_t buf_len)
{
    static const char hex[] = "0123456789ABCDEF";
    size_t pos = 0;

    for (uint8_t i = 0; i < cred->uid_len && pos + 3 < buf_len; i++) {
        if (i > 0) buf[pos++] = ':';
        buf[pos++] = hex[cred->uid[i] >> 4];
        buf[pos++] = hex[cred->uid[i] & 0x0F];
    }
    buf[pos] = '\0';
}

void credential_uid_to_log_id(const credential_t *cred, char *buf, size_t buf_len)
{
    static const char hex[] = "0123456789abcdef";

    if (buf_len < CREDENTIAL_LOG_ID_LEN || cred->uid_len == 0) {
        if (buf_len > 0) buf[0] = '\0';
        return;
    }

    uint32_t h = 2166136261u;  /* FNV-1a 32-bit offset basis */
    for (uint8_t i = 0; i < cred->uid_len; i++) {
        h ^= cred->uid[i];
        h *= 16777619u;  /* FNV prime */
    }

    buf[0] = hex[(h >> 28) & 0xF];
    buf[1] = hex[(h >> 24) & 0xF];
    buf[2] = hex[(h >> 20) & 0xF];
    buf[3] = hex[(h >> 16) & 0xF];
    buf[4] = hex[(h >> 12) & 0xF];
    buf[5] = hex[(h >>  8) & 0xF];
    buf[6] = hex[(h >>  4) & 0xF];
    buf[7] = hex[(h >>  0) & 0xF];
    buf[8] = '\0';
}