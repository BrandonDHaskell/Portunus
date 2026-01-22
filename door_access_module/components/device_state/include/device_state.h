#pragma once
#include <stdbool.h>
#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

typedef struct {
    bool strike_unlocked;

#if CONFIG_PORTUNUS_ENABLE_REED_SWITCH
    bool door_open;
#endif

    int wifi_rssi;          // 0 if unknown
    uint32_t last_error;    // bitfield for future use
} door_module_status_t;

void device_state_init();
void device_state_set_strike_unlocked(bool unlocked);
void device_state_set_wifi_rssi(int rssi);
void device_state_set_last_error(uint32_t err);

#if CONFIG_PORTUNUS_ENABLE_REED_SWITCH
void device_state_set_door_open(bool open);
#endif

door_module_status_t device_state_get_snapshot();

#ifdef __cplusplus
}
#endif
