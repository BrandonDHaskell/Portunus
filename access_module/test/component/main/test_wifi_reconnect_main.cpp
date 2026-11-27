/* Component tier, WiFi reconnect variant: ESP-IDF linux target.
 *
 * Built with sdkconfig.defaults.wifi (CONFIG_PORTUNUS_TEST_WIFI_RECONNECT).
 * Runs the real WifiReconnect task on the POSIX FreeRTOS scheduler with fake
 * connect / is_connected callbacks; wifi_mgr's driver calls are not linked
 * (no esp_wifi on linux). Intervals are tens of ms so waits stay short.
 */

#include "unity.h"
#include "wifi_reconnect.hpp"
#include "error_codes.hpp"

#include "freertos/FreeRTOS.h"
#include "freertos/semphr.h"
#include "freertos/task.h"

#include <atomic>
#include <stdlib.h>

namespace {

struct FakeLink {
    std::atomic<int>  connects{0};
    std::atomic<bool> connected{false};
    std::atomic<bool> in_connect{false};
    SemaphoreHandle_t block = nullptr;   /* if set, connect waits on it */
};

FakeLink g_link;

void fake_connect(void *ctx) {
    auto *l = static_cast<FakeLink *>(ctx);
    l->in_connect = true;
    if (l->block != nullptr) {
        xSemaphoreTake(l->block, portMAX_DELAY);
    }
    ++l->connects;
    l->in_connect = false;
}

bool fake_is_connected(void *ctx) {
    return static_cast<FakeLink *>(ctx)->connected;
}

const wifi_reconnect_ops_t OPS = {fake_connect, fake_is_connected, &g_link};

constexpr uint32_t STACK    = 4096;
constexpr int      PRIORITY = 3;

/* Steps of one 10 ms tick: a shorter step rounds to vTaskDelay(0). */
template <typename Pred>
bool wait_until(Pred pred, int timeout_ms = 2000) {
    for (int t = 0; t < timeout_ms; t += 10) {
        if (pred()) return true;
        vTaskDelay(pdMS_TO_TICKS(10));
    }
    return pred();
}

/* On the heap and stopped in tearDown: a failed assertion longjmps out of
 * the test, past any destructor, and a stack instance would leave its task
 * running on freed memory. */
WifiReconnect *g_r = nullptr;

WifiReconnect &make(uint32_t initial_ms, uint32_t ceiling_ms) {
    g_r = new WifiReconnect(OPS, initial_ms, ceiling_ms);
    return *g_r;
}

void sleep_ms(int ms) { vTaskDelay(pdMS_TO_TICKS(ms)); }

int elapsed_ms(TickType_t since) {
    return (int)((xTaskGetTickCount() - since) * portTICK_PERIOD_MS);
}

}  // namespace

void setUp(void) {
    g_link.connects   = 0;
    g_link.connected  = false;
    g_link.in_connect = false;
    g_link.block      = nullptr;
}

void tearDown(void) {
    if (g_link.block != nullptr) {
        xSemaphoreGive(g_link.block);       /* let a stuck connect return */
    }
    if (g_r != nullptr) {
        if (g_r->stop(1000) == PORTUNUS_OK) {
            delete g_r;
        }                                   /* else leaked: its task still runs */
        g_r = nullptr;
    }
    if (g_link.block != nullptr) {
        vSemaphoreDelete(g_link.block);
        g_link.block = nullptr;
    }
}

/* WIFI-01: a disconnect leads to one connect, after the backoff wait. */
void test_disconnect_connects_after_backoff(void) {
    WifiReconnect &r = make(100, 800);
    TEST_ASSERT_EQUAL(PORTUNUS_OK, r.start("wifi_reconn", STACK, PRIORITY));
    const TickType_t t0 = xTaskGetTickCount();
    r.notify_disconnected();
    sleep_ms(50);
    TEST_ASSERT_EQUAL_INT(0, g_link.connects.load());       /* still waiting */
    TEST_ASSERT_TRUE(wait_until([] { return g_link.connects >= 1; }));
    TEST_ASSERT_GREATER_OR_EQUAL_INT(90, elapsed_ms(t0));
    sleep_ms(150);
    TEST_ASSERT_EQUAL_INT(1, g_link.connects.load());       /* exactly one */
    TEST_ASSERT_EQUAL(PORTUNUS_OK, r.stop(1000));
}

/* WIFI-02: each failed attempt doubles the interval up to the ceiling; a
 * connect resets it. */
void test_backoff_doubles_and_resets(void) {
    WifiReconnect &r = make(20, 80);
    TEST_ASSERT_EQUAL(PORTUNUS_OK, r.start("wifi_reconn", STACK, PRIORITY));
    const uint32_t want[] = {40, 80, 80};
    for (int i = 0; i < 3; ++i) {
        r.notify_disconnected();
        TEST_ASSERT_TRUE(wait_until([i] { return g_link.connects >= i + 1; }));
        TEST_ASSERT_EQUAL_UINT32(want[i], r.interval_ms());
    }
    r.notify_connected();
    TEST_ASSERT_EQUAL_UINT32(20, r.interval_ms());
    TEST_ASSERT_EQUAL(PORTUNUS_OK, r.stop(1000));
}

/* WIFI-03: while suspended (wifi_mgr_stop), a disconnect causes no attempt,
 * and an attempt already waiting is dropped. resume() re-arms. */
void test_suspend_blocks_attempts(void) {
    WifiReconnect &r = make(200, 800);
    TEST_ASSERT_EQUAL(PORTUNUS_OK, r.start("wifi_reconn", STACK, PRIORITY));

    r.notify_disconnected();
    sleep_ms(50);                                           /* inside the wait */
    r.suspend();
    r.notify_disconnected();                                /* the stop's own disconnect */
    sleep_ms(500);
    TEST_ASSERT_EQUAL_INT(0, g_link.connects.load());

    r.resume();
    TEST_ASSERT_EQUAL_UINT32(200, r.interval_ms());
    const TickType_t t0 = xTaskGetTickCount();
    r.notify_disconnected();
    TEST_ASSERT_TRUE(wait_until([] { return g_link.connects >= 1; }));
    TEST_ASSERT_GREATER_OR_EQUAL_INT(180, elapsed_ms(t0));  /* a full wait: no leftover cancel */
    TEST_ASSERT_EQUAL(PORTUNUS_OK, r.stop(1000));
}

/* WIFI-04: stop() during a long backoff ends the task at once, without an
 * attempt. It used to sleep the whole wait (up to 60 s). */
void test_stop_cuts_backoff_short(void) {
    WifiReconnect &r = make(60000, 60000);
    TEST_ASSERT_EQUAL(PORTUNUS_OK, r.start("wifi_reconn", STACK, PRIORITY));
    r.notify_disconnected();
    sleep_ms(50);
    const TickType_t t0 = xTaskGetTickCount();
    TEST_ASSERT_EQUAL(PORTUNUS_OK, r.stop(1000));
    TEST_ASSERT_LESS_THAN_INT(200, elapsed_ms(t0));
    TEST_ASSERT_FALSE(r.running());
    TEST_ASSERT_EQUAL_INT(0, g_link.connects.load());
}

/* WIFI-05: a task stuck in connect is not force-deleted: stop() reports
 * TIMEOUT and leaves it running; once connect returns, stop() succeeds. */
void test_stop_timeout_leaves_task(void) {
    g_link.block = xSemaphoreCreateBinary();
    WifiReconnect &r = make(10, 80);
    TEST_ASSERT_EQUAL(PORTUNUS_OK, r.start("wifi_reconn", STACK, PRIORITY));
    r.notify_disconnected();
    TEST_ASSERT_TRUE(wait_until([] { return g_link.in_connect.load(); }));

    TEST_ASSERT_EQUAL(PORTUNUS_ERR_TIMEOUT, r.stop(100));
    TEST_ASSERT_TRUE(r.running());

    xSemaphoreGive(g_link.block);
    TEST_ASSERT_EQUAL(PORTUNUS_OK, r.stop(1000));
    TEST_ASSERT_FALSE(r.running());
    TEST_ASSERT_EQUAL_INT(1, g_link.connects.load());
}

/* WIFI-06: start() twice is refused; after stop() it starts again and works. */
void test_restart_after_stop(void) {
    WifiReconnect &r = make(20, 80);
    TEST_ASSERT_EQUAL(PORTUNUS_OK, r.start("wifi_reconn", STACK, PRIORITY));
    TEST_ASSERT_EQUAL(PORTUNUS_ERR_ALREADY_INIT, r.start("wifi_reconn", STACK, PRIORITY));
    TEST_ASSERT_EQUAL(PORTUNUS_OK, r.stop(1000));
    TEST_ASSERT_EQUAL(PORTUNUS_OK, r.stop(1000));           /* not running: no-op */
    r.notify_disconnected();                                /* not running: ignored */

    TEST_ASSERT_EQUAL(PORTUNUS_OK, r.start("wifi_reconn", STACK, PRIORITY));
    r.notify_disconnected();
    TEST_ASSERT_TRUE(wait_until([] { return g_link.connects >= 1; }));
    TEST_ASSERT_EQUAL(PORTUNUS_OK, r.stop(1000));
}

/* WIFI-07: if the link came back during the wait, no attempt is made and
 * the interval is not advanced. */
void test_connected_during_wait_skips_attempt(void) {
    WifiReconnect &r = make(50, 400);
    TEST_ASSERT_EQUAL(PORTUNUS_OK, r.start("wifi_reconn", STACK, PRIORITY));
    r.notify_disconnected();
    g_link.connected = true;
    sleep_ms(200);
    TEST_ASSERT_EQUAL_INT(0, g_link.connects.load());
    TEST_ASSERT_EQUAL_UINT32(50, r.interval_ms());
    TEST_ASSERT_EQUAL(PORTUNUS_OK, r.stop(1000));
}

/* WIFI-08: disconnects queued before suspend() are forgotten: after a quick
 * suspend/resume (wifi_mgr_stop then start), no stale attempt follows. */
void test_suspend_forgets_queued_disconnects(void) {
    WifiReconnect &r = make(100, 800);
    TEST_ASSERT_EQUAL(PORTUNUS_OK, r.start("wifi_reconn", STACK, PRIORITY));
    r.notify_disconnected();
    sleep_ms(30);                                           /* first one is waiting */
    r.notify_disconnected();                                /* second one queued */
    r.notify_disconnected();
    r.suspend();
    r.resume();
    sleep_ms(500);                                          /* several intervals */
    TEST_ASSERT_EQUAL_INT(0, g_link.connects.load());
    TEST_ASSERT_EQUAL(PORTUNUS_OK, r.stop(1000));
}

extern "C" void app_main(void) {
    UNITY_BEGIN();
    RUN_TEST(test_disconnect_connects_after_backoff);
    RUN_TEST(test_backoff_doubles_and_resets);
    RUN_TEST(test_suspend_blocks_attempts);
    RUN_TEST(test_stop_cuts_backoff_short);
    RUN_TEST(test_stop_timeout_leaves_task);
    RUN_TEST(test_restart_after_stop);
    RUN_TEST(test_connected_during_wait_skips_attempt);
    RUN_TEST(test_suspend_forgets_queued_disconnects);
    exit(UNITY_END());
}
