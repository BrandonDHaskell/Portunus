#include "wifi_backoff.hpp"

uint32_t wifi_backoff_after_wait(uint32_t current_ms, bool connected, uint32_t ceiling_ms)
{
    if (connected) {
        return current_ms;
    }
    return (current_ms > ceiling_ms / 2) ? ceiling_ms : current_ms * 2;
}
