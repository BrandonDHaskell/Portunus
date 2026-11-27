#include "net_info.hpp"

#include <stdio.h>

/* A fixed, plausible station: tests that care about heartbeat fields read
 * these values back from the encoded request. */
bool net_info_sta_ip(char *buf, size_t len) {
    snprintf(buf, len, "%s", "10.0.0.99");
    return true;
}

bool net_info_rssi(int32_t *out) {
    *out = -55;
    return true;
}
