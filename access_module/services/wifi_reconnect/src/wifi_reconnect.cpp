/**
 * @file wifi_reconnect.cpp
 * @brief WiFi reconnect task: backoff off the event loop, cooperative stop.
 *
 * Disconnects arrive as task notifications (counting, so one that lands
 * during an attempt is handled next). The backoff wait is on an event group,
 * so stop() can end it at once instead of after up to the 60 s ceiling,
 * without being mistaken for a disconnect. suspend() needs no wake-up: an
 * attempt whose wait it overlapped is dropped when the wait ends.
 */

#include "wifi_reconnect.hpp"
#include "wifi_backoff.hpp"
#include "error_codes.hpp"

#include "esp_log.h"

#include <inttypes.h>

static const char *TAG = "wifi_reconn";

static constexpr EventBits_t STOP_BIT = 1 << 0;   /* stop(): end the wait and exit */

WifiReconnect::WifiReconnect(const wifi_reconnect_ops_t &ops, uint32_t initial_ms,
                             uint32_t ceiling_ms)
    : m_ops(ops), m_initial_ms(initial_ms), m_ceiling_ms(ceiling_ms), m_interval_ms(initial_ms)
{
}

WifiReconnect::~WifiReconnect()
{
    if (stop(1000) != PORTUNUS_OK) {
        /* The task still uses this object's members; there is nothing safe
         * to free. Only reachable if `connect` never returns. */
        ESP_LOGE(TAG, "Reconnect task did not stop; leaking it");
    }
}

portunus_err_t WifiReconnect::start(const char *task_name, uint32_t stack, UBaseType_t priority)
{
    if (m_task != nullptr) {
        return PORTUNUS_ERR_ALREADY_INIT;
    }
    m_events = xEventGroupCreate();
    m_exited = xSemaphoreCreateBinary();
    if (m_events == nullptr || m_exited == nullptr) {
        if (m_events != nullptr) vEventGroupDelete(m_events);
        if (m_exited != nullptr) vSemaphoreDelete(m_exited);
        m_events = nullptr;
        m_exited = nullptr;
        return PORTUNUS_ERR_NO_MEMORY;
    }
    m_stop_requested = false;
    m_suspended      = false;
    m_interval_ms    = m_initial_ms;

    if (xTaskCreate(task_entry, task_name, stack, this, priority, &m_task) != pdPASS) {
        m_task = nullptr;
        vEventGroupDelete(m_events);
        vSemaphoreDelete(m_exited);
        m_events = nullptr;
        m_exited = nullptr;
        return PORTUNUS_ERR_TASK_CREATE;
    }
    return PORTUNUS_OK;
}

void WifiReconnect::notify_disconnected()
{
    if (m_suspended || m_stop_requested || m_task == nullptr) {
        return;
    }
    xTaskNotifyGive(m_task);
}

void WifiReconnect::notify_connected()
{
    m_interval_ms = m_initial_ms;
}

void WifiReconnect::suspend()
{
    m_suspended = true;
    ++m_generation;
    if (m_task != nullptr) {
        (void)ulTaskNotifyValueClear(m_task, UINT32_MAX);   /* forget queued disconnects */
    }
}

void WifiReconnect::resume()
{
    m_interval_ms = m_initial_ms;
    m_suspended   = false;
}

portunus_err_t WifiReconnect::stop(int timeout_ms)
{
    if (m_task == nullptr) {
        return PORTUNUS_OK;
    }
    m_stop_requested = true;
    xEventGroupSetBits(m_events, STOP_BIT);
    xTaskNotifyGive(m_task);
    if (xSemaphoreTake(m_exited, pdMS_TO_TICKS(timeout_ms)) != pdTRUE) {
        ESP_LOGE(TAG, "Reconnect task did not stop within %d ms", timeout_ms);
        return PORTUNUS_ERR_TIMEOUT;
    }
    m_task = nullptr;
    vEventGroupDelete(m_events);
    vSemaphoreDelete(m_exited);
    m_events = nullptr;
    m_exited = nullptr;
    return PORTUNUS_OK;
}

void WifiReconnect::task_entry(void *arg)
{
    static_cast<WifiReconnect *>(arg)->run();
}

void WifiReconnect::run()
{
    ESP_LOGI(TAG, "Reconnect task started");

    while (!m_stop_requested) {
        (void)ulTaskNotifyTake(pdTRUE, portMAX_DELAY);
        if (m_stop_requested) {
            break;
        }
        if (m_suspended) {
            continue;
        }

        const uint32_t generation = m_generation;
        const uint32_t delay_ms   = m_interval_ms;
        ESP_LOGI(TAG, "Reconnect: waiting %" PRIu32 " ms before retry", delay_ms);
        (void)xEventGroupWaitBits(m_events, STOP_BIT, pdFALSE, pdFALSE, pdMS_TO_TICKS(delay_ms));
        if (m_stop_requested) {
            break;
        }
        /* Suspended during the wait, even if resumed since: drop it. */
        if (m_suspended || generation != m_generation) {
            ESP_LOGI(TAG, "Reconnect: cancelled");
            continue;
        }

        /* If the link came back during the wait, the connect handler has
         * already reset the interval: keep that reset (compare-and-swap)
         * rather than overwrite it with a backoff from the stale value. */
        const bool connected = m_ops.is_connected(m_ops.ctx);
        uint32_t   expected  = delay_ms;
        m_interval_ms.compare_exchange_strong(
            expected, wifi_backoff_after_wait(delay_ms, connected, m_ceiling_ms));
        if (connected) {
            ESP_LOGI(TAG, "Reconnect: already connected, skipping attempt");
            continue;
        }

        ESP_LOGI(TAG, "Reconnect: connecting");
        m_ops.connect(m_ops.ctx);
    }

    xSemaphoreGive(m_exited);
    vTaskDelete(NULL);
}
