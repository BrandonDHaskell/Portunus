#include "net_info.hpp"

#include "esp_netif.h"
#include "esp_wifi.h"

#include <stdio.h>

bool net_info_sta_ip(char *buf, size_t len)
{
    esp_netif_t *netif = esp_netif_get_handle_from_ifkey("WIFI_STA_DEF");
    if (netif == NULL) { return false; }

    esp_netif_ip_info_t info;
    if (esp_netif_get_ip_info(netif, &info) != ESP_OK) { return false; }
    if (info.ip.addr == 0) { return false; }

    snprintf(buf, len, IPSTR, IP2STR(&info.ip));
    return true;
}

bool net_info_rssi(int32_t *out)
{
    wifi_ap_record_t ap;
    if (esp_wifi_sta_get_ap_info(&ap) != ESP_OK) { return false; }
    *out = ap.rssi;
    return true;
}
