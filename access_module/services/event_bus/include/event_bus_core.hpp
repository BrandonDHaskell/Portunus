/* Event bus contract shared by the production bus (event_bus.cpp: queue,
 * dispatcher task, mutex) and the component tier's fake (inline dispatch,
 * recording). The subscriber table, its limits and errors, and the
 * snapshot-then-dispatch rule live here once, so the two front ends cannot
 * drift. No FreeRTOS: callers own locking and storage, and the unit tier
 * links this file directly. Internal to event_bus and its test double. */
#pragma once

#include "event_types.hpp"
#include "portunus_types.hpp"

#include <stddef.h>

/** Same signature as event_bus_handler_t (event_bus.hpp), without FreeRTOS. */
typedef void (*bus_core_handler_t)(const portunus_event_t *event, void *ctx);

struct bus_core_sub_t {
    portunus_event_id_t id;
    bus_core_handler_t  handler;
    void               *ctx;
};

struct bus_core_t {
    bus_core_sub_t *subs;      /**< Caller-owned storage of `capacity` entries. */
    size_t          capacity;
    size_t          count;
};

/** Empty table over caller-owned storage. */
void bus_core_init(bus_core_t &core, bus_core_sub_t *storage, size_t capacity);

/** Append a subscriber.
 *  @return PORTUNUS_OK, PORTUNUS_ERR_INVALID_ARG (NULL handler) or
 *          PORTUNUS_ERR_MAX_SUBSCRIBERS (table full). */
portunus_err_t bus_core_subscribe(bus_core_t &core, portunus_event_id_t id,
                                  bus_core_handler_t handler, void *ctx);

/** Copy the table into `out` (at least `capacity` entries) and return the
 *  count. Take it under the caller's lock, then dispatch without the lock:
 *  a handler may subscribe, and the new subscriber gets the next event. */
size_t bus_core_snapshot(const bus_core_t &core, bus_core_sub_t *out);

/** Call every snapshot entry subscribed to event.id, in registration order. */
void bus_core_dispatch(const bus_core_sub_t *snapshot, size_t count,
                       const portunus_event_t &event);
