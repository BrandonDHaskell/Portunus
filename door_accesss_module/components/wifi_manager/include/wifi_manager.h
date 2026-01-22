#pragma once
#include <stdbool.h>
#include "freertos/FreeRTOS.h"

#ifdef __cplusplus
extern "C" {
#endif

void wifi_manager_init_sta();
bool wifi_manager_wait_connected(TickType_t timeout_ticks);
bool wifi_manager_get_ip4(char* out, size_t out_len);
int  wifi_manager_get_rssi(); // 0 if unknown

#ifdef __cplusplus
}
#endif
