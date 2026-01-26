#pragma once
#include <stdbool.h>

#ifdef __cplusplus
extern "C" {
#endif

void status_led_init();
void status_led_set(bool on);

#ifdef __cplusplus
}
#endif
