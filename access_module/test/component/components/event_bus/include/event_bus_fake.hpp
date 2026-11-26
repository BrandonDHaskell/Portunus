#pragma once
#include "event_types.hpp"
#include <stddef.h>

void   event_bus_fake_reset(void);
size_t event_bus_fake_count(void);
const portunus_event_t *event_bus_fake_at(size_t i);
size_t event_bus_fake_count_of(portunus_event_id_t id);
