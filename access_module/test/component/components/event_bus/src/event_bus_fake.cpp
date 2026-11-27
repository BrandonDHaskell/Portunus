/* Synchronous fake event bus for the component tier.
 *
 * The subscriber table, its limit and errors, and snapshot-then-dispatch come
 * from event_bus_core, the same code the production bus uses, so the two
 * cannot drift. This file adds only what tests need: inline dispatch on the
 * caller's task instead of a dispatcher task (deterministic behavioural
 * tests), a record of every published event, and fail_next.
 *
 * State is guarded by a FreeRTOS mutex so a real task under test (the
 * heartbeat service) and the test task can use the bus together. Handlers run
 * outside the lock. */
#include "event_bus.hpp"
#include "event_bus_core.hpp"
#include "event_bus_fake.hpp"
#include "error_codes.hpp"
#include "timing_config.hpp"

#include "freertos/FreeRTOS.h"
#include "freertos/semphr.h"

#include <vector>

namespace {
bus_core_sub_t                g_storage[MAX_EVENT_SUBSCRIBERS];
bus_core_t                    g_subs      = {g_storage, MAX_EVENT_SUBSCRIBERS, 0};
std::vector<portunus_event_t> g_published;
bool                          g_inited    = false;
uint32_t                      g_dropped   = 0;
size_t                        g_fail_left = 0;
portunus_err_t                g_fail_err  = PORTUNUS_OK;

SemaphoreHandle_t lock_handle() {
    /* Created on first use and kept across resets: deleting a mutex another
     * task might still be waiting on is worse than one allocation per run. */
    static SemaphoreHandle_t m = xSemaphoreCreateMutex();
    return m;
}

struct Guard {
    Guard()  { xSemaphoreTake(lock_handle(), portMAX_DELAY); }
    ~Guard() { xSemaphoreGive(lock_handle()); }
};

/* Held for each inline delivery, as the real dispatcher holds its dispatch
 * mutex. Recursive: a handler may publish or unsubscribe on the same task. */
SemaphoreHandle_t dispatch_lock() {
    static SemaphoreHandle_t m = xSemaphoreCreateRecursiveMutex();
    return m;
}
}  // namespace

extern "C" {

portunus_err_t event_bus_init(void) {
    Guard g;
    if (g_inited) return PORTUNUS_ERR_ALREADY_INIT;
    g_published.clear();
    bus_core_init(g_subs, g_storage, MAX_EVENT_SUBSCRIBERS);
    g_inited = true;
    return PORTUNUS_OK;
}

portunus_err_t event_bus_publish(const portunus_event_t *event) {
    if (event == nullptr) return PORTUNUS_ERR_INVALID_ARG;

    bus_core_sub_t snapshot[MAX_EVENT_SUBSCRIBERS];
    size_t         count;
    {
        Guard g;
        if (!g_inited) return PORTUNUS_ERR_NOT_INIT;
        if (g_fail_left > 0) {
            --g_fail_left;
            if (g_fail_err == PORTUNUS_ERR_QUEUE_FULL) ++g_dropped;
            return g_fail_err;  /* dropped, as the real bus drops on QUEUE_FULL */
        }
        g_published.push_back(*event);
        count = bus_core_snapshot(g_subs, snapshot);
    }
    /* Inline, outside the table lock, as the real dispatcher runs handlers. */
    xSemaphoreTakeRecursive(dispatch_lock(), portMAX_DELAY);
    bus_core_dispatch(snapshot, count, *event);
    xSemaphoreGiveRecursive(dispatch_lock());
    return PORTUNUS_OK;
}

portunus_err_t event_bus_publish_from_isr(const portunus_event_t *event,
                                          BaseType_t *higher_priority_woken) {
    (void)higher_priority_woken;
    return event_bus_publish(event);
}

portunus_err_t event_bus_unsubscribe(portunus_event_id_t event_id,
                                     event_bus_handler_t handler, void *ctx) {
    if (handler == nullptr) return PORTUNUS_ERR_INVALID_ARG;
    portunus_err_t err;
    {
        Guard g;
        if (!g_inited) return PORTUNUS_ERR_NOT_INIT;
        err = bus_core_unsubscribe(g_subs, event_id, handler, ctx);
    }
    if (err == PORTUNUS_OK) {
        /* Wait out a delivery on another task (recursive: no-op on our own). */
        xSemaphoreTakeRecursive(dispatch_lock(), portMAX_DELAY);
        xSemaphoreGiveRecursive(dispatch_lock());
    }
    return err;
}

uint32_t event_bus_dropped_events(void) {
    Guard g;
    return g_dropped;
}

portunus_err_t event_bus_subscribe(portunus_event_id_t event_id,
                                   event_bus_handler_t handler, void *ctx) {
    if (handler == nullptr) return PORTUNUS_ERR_INVALID_ARG;   /* same order as the real bus */
    Guard g;
    if (!g_inited) return PORTUNUS_ERR_NOT_INIT;
    return bus_core_subscribe(g_subs, event_id, handler, ctx);
}

} /* extern "C" */

void event_bus_fake_reset(void) {
    Guard g;
    g_published.clear();
    bus_core_init(g_subs, g_storage, MAX_EVENT_SUBSCRIBERS);
    g_inited    = false;
    g_fail_left = 0;
    g_fail_err  = PORTUNUS_OK;
    g_dropped   = 0;
}

void event_bus_fake_fail_next(size_t n, portunus_err_t err) {
    Guard g;
    g_fail_left = n;
    g_fail_err  = err;
}

size_t event_bus_fake_count(void) {
    Guard g;
    return g_published.size();
}

const portunus_event_t *event_bus_fake_at(size_t i) {
    Guard g;
    return i < g_published.size() ? &g_published[i] : nullptr;
}

bool event_bus_fake_copy(size_t i, portunus_event_t *out) {
    Guard g;
    if (i >= g_published.size() || out == nullptr) return false;
    *out = g_published[i];
    return true;
}

size_t event_bus_fake_count_of(portunus_event_id_t id) {
    Guard g;
    size_t n = 0;
    for (const auto &e : g_published) if (e.id == id) ++n;
    return n;
}

size_t event_bus_fake_subscriber_count(void) {
    Guard g;
    return g_subs.count;
}

size_t event_bus_fake_subscribers_for(portunus_event_id_t id) {
    Guard g;
    size_t n = 0;
    for (size_t i = 0; i < g_subs.count; ++i) if (g_subs.subs[i].id == id) ++n;
    return n;
}
