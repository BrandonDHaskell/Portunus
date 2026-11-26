/* Synchronous fake event bus for the component tier.
 *
 * Follows the real bus's contract (services/event_bus/src/event_bus.cpp)
 * wherever a test could observe the difference: dispatch runs over a snapshot
 * of the subscriber table, so a handler may subscribe; subscribe before init
 * and past MAX_EVENT_SUBSCRIBERS fail with the real error codes. Publishing
 * dispatches inline on the caller's task instead of a dispatcher task, which
 * keeps behavioural tests deterministic.
 *
 * State is guarded by a FreeRTOS mutex so a real task under test (the
 * heartbeat service) and the test task can use the bus together. Handlers run
 * outside the lock. */
#include "event_bus.hpp"
#include "event_bus_fake.hpp"
#include "error_codes.hpp"
#include "timing_config.hpp"

#include "freertos/FreeRTOS.h"
#include "freertos/semphr.h"

#include <vector>

namespace {
struct Sub { portunus_event_id_t id; event_bus_handler_t h; void *ctx; };
std::vector<portunus_event_t> g_published;
std::vector<Sub>              g_subs;
bool                          g_inited    = false;
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
}  // namespace

extern "C" {

portunus_err_t event_bus_init(void) {
    Guard g;
    if (g_inited) return PORTUNUS_ERR_ALREADY_INIT;
    g_published.clear();
    g_subs.clear();
    g_inited = true;
    return PORTUNUS_OK;
}

portunus_err_t event_bus_publish(const portunus_event_t *event) {
    if (event == nullptr) return PORTUNUS_ERR_INVALID_ARG;

    std::vector<Sub> snapshot;
    {
        Guard g;
        if (!g_inited) return PORTUNUS_ERR_NOT_INIT;
        if (g_fail_left > 0) {
            --g_fail_left;
            return g_fail_err;  /* dropped, as the real bus drops on QUEUE_FULL */
        }
        g_published.push_back(*event);
        snapshot = g_subs;
    }
    /* Inline dispatch over the snapshot: a handler that subscribes cannot
     * invalidate this loop, and its new handler sees the next event only. */
    for (const Sub &s : snapshot) {
        if (s.id == event->id) s.h(event, s.ctx);
    }
    return PORTUNUS_OK;
}

portunus_err_t event_bus_publish_from_isr(const portunus_event_t *event,
                                          BaseType_t *higher_priority_woken) {
    (void)higher_priority_woken;
    return event_bus_publish(event);
}

portunus_err_t event_bus_subscribe(portunus_event_id_t event_id,
                                   event_bus_handler_t handler, void *ctx) {
    if (handler == nullptr) return PORTUNUS_ERR_INVALID_ARG;
    Guard g;
    if (!g_inited) return PORTUNUS_ERR_NOT_INIT;
    if (g_subs.size() >= MAX_EVENT_SUBSCRIBERS) return PORTUNUS_ERR_MAX_SUBSCRIBERS;
    g_subs.push_back({event_id, handler, ctx});
    return PORTUNUS_OK;
}

} /* extern "C" */

void event_bus_fake_reset(void) {
    Guard g;
    g_published.clear();
    g_subs.clear();
    g_inited    = false;
    g_fail_left = 0;
    g_fail_err  = PORTUNUS_OK;
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
    return g_subs.size();
}
