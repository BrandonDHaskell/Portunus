#include "event_bus.hpp"
#include "event_bus_fake.hpp"
#include "error_codes.hpp"
#include <vector>

namespace {
struct Sub { portunus_event_id_t id; event_bus_handler_t h; void *ctx; };
std::vector<portunus_event_t> g_published;
std::vector<Sub>              g_subs;
bool                          g_inited = false;
}

extern "C" {

portunus_err_t event_bus_init(void) {
    if (g_inited) return PORTUNUS_ERR_ALREADY_INIT;
    g_published.clear();
    g_subs.clear();
    g_inited = true;
    return PORTUNUS_OK;
}

portunus_err_t event_bus_publish(const portunus_event_t *event) {
    if (event == nullptr) return PORTUNUS_ERR_INVALID_ARG;
    if (!g_inited)        return PORTUNUS_ERR_NOT_INIT;
    g_published.push_back(*event);
    /* Synchronous, deterministic dispatch — no dispatcher task on host. */
    for (auto &s : g_subs) {
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
    g_subs.push_back({event_id, handler, ctx});
    return PORTUNUS_OK;
}

} /* extern "C" */

void   event_bus_fake_reset(void) { g_published.clear(); g_subs.clear(); g_inited = false; }
size_t event_bus_fake_count(void) { return g_published.size(); }

const portunus_event_t *event_bus_fake_at(size_t i) {
    return i < g_published.size() ? &g_published[i] : nullptr;
}

size_t event_bus_fake_count_of(portunus_event_id_t id) {
    size_t n = 0;
    for (auto &e : g_published) if (e.id == id) ++n;
    return n;
}
