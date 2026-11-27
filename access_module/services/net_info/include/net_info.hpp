#pragma once

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

/** The station's IPv4 address as a dotted quad. False if the interface is
 *  down or has no address yet; buf is then left unchanged. */
bool net_info_sta_ip(char *buf, size_t len);

/** RSSI of the AP the station is associated with, in dBm. False if not
 *  associated. */
bool net_info_rssi(int32_t *out);

#ifdef __cplusplus
}
#endif
