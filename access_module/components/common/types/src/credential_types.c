#include "credential_types.h"

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