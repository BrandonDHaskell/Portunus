/* Unit tier: the event bus contract shared by the production bus and the
 * component tier's fake (event_bus_core). No FreeRTOS, no sdkconfig. */
#include "unity.h"
#include "event_bus_core.hpp"
#include "error_codes.hpp"

#include <string.h>
#include <vector>

void setUp(void) {}
void tearDown(void) {}

static constexpr size_t CAP = 4;

namespace {
std::vector<int> g_calls;      /* tag of each handler call, in order */

void tagger(const portunus_event_t *, void *ctx) {
    g_calls.push_back(static_cast<int>(reinterpret_cast<intptr_t>(ctx)));
}
void *tag(int t) { return reinterpret_cast<void *>(static_cast<intptr_t>(t)); }

portunus_event_t event(portunus_event_id_t id) {
    portunus_event_t e;
    memset(&e, 0, sizeof e);
    e.id = id;
    return e;
}

/* Snapshot then dispatch, as both front ends do. */
void publish(const bus_core_t &core, portunus_event_id_t id) {
    bus_core_sub_t snap[CAP];
    const size_t n = bus_core_snapshot(core, snap);
    bus_core_dispatch(snap, n, event(id));
}
}  // namespace

/* BUS-04: exactly `capacity` subscribers fit; the next is refused and
 * changes nothing. */
void test_capacity_limit(void) {
    bus_core_sub_t storage[CAP];
    bus_core_t core;
    bus_core_init(core, storage, CAP);
    for (size_t i = 0; i < CAP; ++i) {
        TEST_ASSERT_EQUAL(PORTUNUS_OK, bus_core_subscribe(core, EVENT_HEARTBEAT, tagger, tag(1)));
    }
    TEST_ASSERT_EQUAL(PORTUNUS_ERR_MAX_SUBSCRIBERS,
                      bus_core_subscribe(core, EVENT_HEARTBEAT, tagger, tag(2)));
    TEST_ASSERT_EQUAL_size_t(CAP, core.count);
}

/* BUS-02: a NULL handler is refused before it can take a slot. */
void test_null_handler_refused(void) {
    bus_core_sub_t storage[CAP];
    bus_core_t core;
    bus_core_init(core, storage, CAP);
    TEST_ASSERT_EQUAL(PORTUNUS_ERR_INVALID_ARG,
                      bus_core_subscribe(core, EVENT_HEARTBEAT, nullptr, nullptr));
    TEST_ASSERT_EQUAL_size_t(0, core.count);
}

/* BUS-06: handlers run in registration order, only for their event id, and
 * a handler subscribed twice runs twice. */
void test_registration_order_and_filter(void) {
    bus_core_sub_t storage[CAP];
    bus_core_t core;
    bus_core_init(core, storage, CAP);
    bus_core_subscribe(core, EVENT_ACCESS_GRANTED, tagger, tag(1));
    bus_core_subscribe(core, EVENT_ACCESS_DENIED,  tagger, tag(2));
    bus_core_subscribe(core, EVENT_ACCESS_GRANTED, tagger, tag(3));
    bus_core_subscribe(core, EVENT_ACCESS_GRANTED, tagger, tag(1));

    g_calls.clear();
    publish(core, EVENT_ACCESS_GRANTED);
    TEST_ASSERT_TRUE((g_calls == std::vector<int>{1, 3, 1}));
    g_calls.clear();
    publish(core, EVENT_ACCESS_DENIED);
    TEST_ASSERT_TRUE((g_calls == std::vector<int>{2}));
    g_calls.clear();
    publish(core, EVENT_HEARTBEAT);
    TEST_ASSERT_TRUE(g_calls.empty());
}

namespace {
bus_core_t *g_core = nullptr;
void subscribe_late(const portunus_event_t *, void *) {
    g_calls.push_back(0);
    bus_core_subscribe(*g_core, EVENT_ACCESS_GRANTED, tagger, tag(9));
}
}  // namespace

/* BUS-08: a handler that subscribes during dispatch does not change the
 * event in flight (it runs over a snapshot); the new handler sees the next. */
void test_subscribe_during_dispatch_affects_next_event(void) {
    bus_core_sub_t storage[CAP];
    bus_core_t core;
    bus_core_init(core, storage, CAP);
    g_core = &core;
    bus_core_subscribe(core, EVENT_ACCESS_GRANTED, subscribe_late, nullptr);

    g_calls.clear();
    publish(core, EVENT_ACCESS_GRANTED);
    TEST_ASSERT_TRUE((g_calls == std::vector<int>{0}));
    TEST_ASSERT_EQUAL_size_t(2, core.count);

    g_calls.clear();
    publish(core, EVENT_ACCESS_GRANTED);
    TEST_ASSERT_TRUE((g_calls == std::vector<int>{0, 9}));
}

/* D-T9: unsubscribe removes exactly the first (id, handler, ctx) match, keeps
 * the rest in registration order, and frees the slot. */
void test_unsubscribe(void) {
    bus_core_sub_t storage[CAP];
    bus_core_t core;
    bus_core_init(core, storage, CAP);
    bus_core_subscribe(core, EVENT_ACCESS_GRANTED, tagger, tag(1));
    bus_core_subscribe(core, EVENT_ACCESS_GRANTED, tagger, tag(2));
    bus_core_subscribe(core, EVENT_ACCESS_GRANTED, tagger, tag(1));
    bus_core_subscribe(core, EVENT_ACCESS_GRANTED, tagger, tag(3));

    TEST_ASSERT_EQUAL(PORTUNUS_OK, bus_core_unsubscribe(core, EVENT_ACCESS_GRANTED, tagger, tag(1)));
    g_calls.clear();
    publish(core, EVENT_ACCESS_GRANTED);
    TEST_ASSERT_TRUE((g_calls == std::vector<int>{2, 1, 3}));

    TEST_ASSERT_EQUAL(PORTUNUS_ERR_NOT_SUBSCRIBED,
                      bus_core_unsubscribe(core, EVENT_ACCESS_DENIED, tagger, tag(2)));   /* wrong id */
    TEST_ASSERT_EQUAL(PORTUNUS_ERR_NOT_SUBSCRIBED,
                      bus_core_unsubscribe(core, EVENT_ACCESS_GRANTED, tagger, tag(9)));  /* wrong ctx */
    TEST_ASSERT_EQUAL(PORTUNUS_ERR_INVALID_ARG,
                      bus_core_unsubscribe(core, EVENT_ACCESS_GRANTED, nullptr, nullptr));
    TEST_ASSERT_EQUAL_size_t(3, core.count);

    TEST_ASSERT_EQUAL(PORTUNUS_OK, bus_core_subscribe(core, EVENT_HEARTBEAT, tagger, tag(4)));  /* slot freed */
    TEST_ASSERT_EQUAL(PORTUNUS_ERR_MAX_SUBSCRIBERS, bus_core_subscribe(core, EVENT_HEARTBEAT, tagger, tag(5)));
}

/* Re-initialising empties the table over the same storage. */
void test_init_clears(void) {
    bus_core_sub_t storage[CAP];
    bus_core_t core;
    bus_core_init(core, storage, CAP);
    bus_core_subscribe(core, EVENT_HEARTBEAT, tagger, tag(1));
    bus_core_init(core, storage, CAP);
    TEST_ASSERT_EQUAL_size_t(0, core.count);
    g_calls.clear();
    publish(core, EVENT_HEARTBEAT);
    TEST_ASSERT_TRUE(g_calls.empty());
}

int main(void) {
    UNITY_BEGIN();
    RUN_TEST(test_capacity_limit);
    RUN_TEST(test_null_handler_refused);
    RUN_TEST(test_registration_order_and_filter);
    RUN_TEST(test_subscribe_during_dispatch_affects_next_event);
    RUN_TEST(test_init_clears);
    RUN_TEST(test_unsubscribe);
    return UNITY_END();
}
