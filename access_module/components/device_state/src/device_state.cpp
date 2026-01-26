#include "device_state.h"

#include "freertos/FreeRTOS.h"
#include "freertos/semphr.h"

static SemaphoreHandle_t s_mutex;
static door_module_status_t s_status;

extern "C" void device_state_init() {
    s_mutex = xSemaphoreCreateMutex();
    s_status = {};
    s_status.wifi_rssi = 0;
}

static void lock()   { xSemaphoreTake(s_mutex, portMAX_DELAY); }
static void unlock() { xSemaphoreGive(s_mutex); }

extern "C" void device_state_set_strike_unlocked(bool unlocked_v) {
    lock();
    s_status.strike_unlocked = unlocked_v;
    unlock();
}

extern "C" void device_state_set_wifi_rssi(int rssi) {
    lock();
    s_status.wifi_rssi = rssi;
    unlock();
}

extern "C" void device_state_set_last_error(uint32_t err) {
    lock();
    s_status.last_error = err;
    unlock();
}

#if CONFIG_PORTUNUS_ENABLE_REED_SWITCH
extern "C" void device_state_set_door_open(bool open_v) {
    lock();
    s_status.door_open = open_v;
    unlock();
}
#endif

extern "C" door_module_status_t device_state_get_snapshot() {
    lock();
    door_module_status_t copy = s_status;
    unlock();
    return copy;
}
