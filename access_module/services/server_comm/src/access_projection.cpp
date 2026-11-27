#include "access_projection.hpp"

#include <stdio.h>

bool access_response_projection(char *buf, size_t cap, const char *module_id,
                                const char *credential_id, const char *nonce_hex,
                                bool granted)
{
    if (buf == nullptr || cap == 0) {
        return false;
    }
    buf[0] = '\0';
    if (module_id == nullptr || credential_id == nullptr || nonce_hex == nullptr) {
        return false;
    }
    const int n = snprintf(buf, cap, "access_resp|%s|%s|%s|%d",
                           module_id, credential_id, nonce_hex, granted ? 1 : 0);
    if (n < 0 || static_cast<size_t>(n) >= cap) {
        buf[0] = '\0';   /* truncated: never verify against a partial string */
        return false;
    }
    return true;
}
