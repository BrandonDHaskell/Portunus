/**
 * @file wifi_reconnect.hpp
 * @brief WiFi reconnect task with exponential backoff and a cooperative stop.
 *
 * wifi_mgr's event handlers report link changes here; the task waits out the
 * backoff off the event loop and then calls `connect`. Kept apart from the
 * driver so its lifecycle can be tested on the linux target.
 *
 * Threading: notify_disconnected() and notify_connected() may be called from
 * the event loop task; the rest from one controlling task.
 */
#pragma once

#include "portunus_types.hpp"

#include "freertos/FreeRTOS.h"
#include "freertos/event_groups.h"
#include "freertos/semphr.h"
#include "freertos/task.h"

#include <atomic>
#include <stdint.h>

struct wifi_reconnect_ops_t {
    /** Start a connection attempt (esp_wifi_connect()). May block briefly. */
    void (*connect)(void *ctx);
    /** Whether the link is up; checked after the wait, to skip a needless attempt. */
    bool (*is_connected)(void *ctx);
    void *ctx;
};

class WifiReconnect {
public:
    WifiReconnect(const wifi_reconnect_ops_t &ops, uint32_t initial_ms, uint32_t ceiling_ms);
    ~WifiReconnect();
    WifiReconnect(const WifiReconnect &)            = delete;
    WifiReconnect &operator=(const WifiReconnect &) = delete;

    /** Create the task. PORTUNUS_ERR_ALREADY_INIT if running. */
    portunus_err_t start(const char *task_name, uint32_t stack, UBaseType_t priority);

    /** The link dropped: after the backoff, reconnect. Ignored while suspended. */
    void notify_disconnected();
    /** The link is up: reset the backoff. */
    void notify_connected();

    /** Stop reconnecting (wifi_mgr_stop): an attempt waiting out its backoff
     *  is dropped and pending disconnects are forgotten, so none follows. */
    void suspend();
    /** Allow reconnects again, from the initial interval. */
    void resume();

    /**
     * End the task cooperatively and wait up to `timeout_ms` for it.
     * @return PORTUNUS_OK (also if not running), or PORTUNUS_ERR_TIMEOUT if
     *         the task is still inside `connect`: it is left running, and
     *         stop() may be called again.
     */
    portunus_err_t stop(int timeout_ms);

    bool     running() const { return m_task != nullptr; }
    uint32_t interval_ms() const { return m_interval_ms; }

private:
    static void task_entry(void *arg);
    void        run();

    const wifi_reconnect_ops_t m_ops;
    const uint32_t             m_initial_ms;
    const uint32_t             m_ceiling_ms;

    TaskHandle_t       m_task   = nullptr;
    EventGroupHandle_t m_events = nullptr;   /* STOP_BIT ends a backoff wait early */
    SemaphoreHandle_t  m_exited = nullptr;   /* given by the task as it exits */

    /* Reset by the event loop on connect, advanced by the task: atomic. */
    std::atomic<uint32_t> m_interval_ms;
    std::atomic<bool>     m_stop_requested{false};
    std::atomic<bool>     m_suspended{false};
    /* Bumped by suspend(): an attempt whose wait began before it is dropped,
     * even if resume() follows before the task looks. */
    std::atomic<uint32_t> m_generation{0};
};
