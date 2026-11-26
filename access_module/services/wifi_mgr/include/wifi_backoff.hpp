/* Pure WiFi reconnect backoff step extracted from wifi_mgr's reconnect task.
 * No ESP-IDF or FreeRTOS dependency: the unit tier links wifi_backoff.cpp
 * and the reconnect task calls the same function. */
#pragma once

#include <stdint.h>

/**
 * @brief Interval for the next reconnect wait, after waking from this one.
 *
 * If the link came back during the wait (`connected`), the interval is left
 * as is: the IP handler has already reset it, and advancing here would leave
 * the next real disconnect with a stale, too-long wait. Otherwise it doubles,
 * capped at `ceiling_ms`, without uint32 overflow.
 */
uint32_t wifi_backoff_after_wait(uint32_t current_ms, bool connected, uint32_t ceiling_ms);
