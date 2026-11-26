#pragma once
#include "event_types.hpp"
#include "portunus_types.hpp"
#include <stddef.h>

/* Test controls for the fake event bus (components/event_bus). */

/** Forget all published events and subscribers and return to "not
 *  initialised"; also clears any pending fail_next. */
void event_bus_fake_reset(void);

/** The next n publishes return err and are neither recorded nor
 *  dispatched, as the real bus drops an event it cannot queue. */
void event_bus_fake_fail_next(size_t n, portunus_err_t err);

size_t event_bus_fake_count(void);
size_t event_bus_fake_count_of(portunus_event_id_t id);
size_t event_bus_fake_subscriber_count(void);

/** Pointer into the record. Only safe while no other task publishes;
 *  with a live publisher task use event_bus_fake_copy. */
const portunus_event_t *event_bus_fake_at(size_t i);

/** Copy published event i into *out; false if there is no such event. */
bool event_bus_fake_copy(size_t i, portunus_event_t *out);
