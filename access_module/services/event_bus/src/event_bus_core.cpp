#include "event_bus_core.hpp"
#include "error_codes.hpp"

#include <string.h>

void bus_core_init(bus_core_t &core, bus_core_sub_t *storage, size_t capacity)
{
    core.subs     = storage;
    core.capacity = capacity;
    core.count    = 0;
}

portunus_err_t bus_core_subscribe(bus_core_t &core, portunus_event_id_t id,
                                  bus_core_handler_t handler, void *ctx)
{
    if (handler == nullptr) {
        return PORTUNUS_ERR_INVALID_ARG;
    }
    if (core.count >= core.capacity) {
        return PORTUNUS_ERR_MAX_SUBSCRIBERS;
    }
    core.subs[core.count++] = {id, handler, ctx};
    return PORTUNUS_OK;
}

size_t bus_core_snapshot(const bus_core_t &core, bus_core_sub_t *out)
{
    if (core.count > 0) {
        memcpy(out, core.subs, core.count * sizeof(bus_core_sub_t));
    }
    return core.count;
}

void bus_core_dispatch(const bus_core_sub_t *snapshot, size_t count,
                       const portunus_event_t &event)
{
    for (size_t i = 0; i < count; ++i) {
        if (snapshot[i].id == event.id) {
            snapshot[i].handler(&event, snapshot[i].ctx);
        }
    }
}
