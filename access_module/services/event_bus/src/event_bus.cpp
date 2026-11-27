/**
 * @file event_bus.cpp
 * @brief Single-dispatcher-queue event bus implementation.
 *
 * MVP topology: one FreeRTOS queue, one dispatcher task, static subscriber
 * table. See project plan §3.5 for the rationale and scaling notes.
 */

#include "event_bus.hpp"
#include "event_bus_core.hpp"
#include "queue_pressure.hpp"
#include "error_codes.hpp"
#include "timing_config.hpp"

#include "freertos/FreeRTOS.h"
#include "freertos/task.h"
#include "freertos/queue.h"
#include "freertos/semphr.h"
#include "esp_log.h"

#include <atomic>
#include <inttypes.h>


static const char *TAG = "event_bus";
static SemaphoreHandle_t s_subscriber_mutex = NULL;
/* Held by the dispatcher while it delivers one event, so unsubscribe can wait
 * out a delivery that may still reach the handler being removed. */
static SemaphoreHandle_t s_dispatch_mutex = NULL;

/* ── Subscriber table (rules in event_bus_core, locking here) ──────────────── */

static bus_core_sub_t s_subscriber_storage[MAX_EVENT_SUBSCRIBERS];
static bus_core_t     s_subscribers = {s_subscriber_storage, MAX_EVENT_SUBSCRIBERS, 0};

/* ── Queue and task handles ────────────────────────────────────────────────── */

static QueueHandle_t s_event_queue  = NULL;

/* QUEUE_FULL refusals (any publishing task: atomic) and the dispatcher's
 * view of how full the queue stays (dispatcher only). D-T5. */
static std::atomic<uint32_t> s_dropped{0};
static queue_pressure_t      s_queue_pressure = {};
static TaskHandle_t  s_dispatch_task = NULL;

#define DISPATCH_TASK_STACK_SIZE  4096
#define DISPATCH_TASK_PRIORITY    5

/* ── Dispatcher task ───────────────────────────────────────────────────────── */

static void event_bus_dispatch_task(void *arg)
{
    (void)arg;
    portunus_event_t event;

    ESP_LOGI(TAG, "Dispatcher task started");

    for (;;) {
        if (xQueueReceive(s_event_queue, &event, portMAX_DELAY) == pdTRUE) {
            switch (queue_pressure_sample(s_queue_pressure, uxQueueMessagesWaiting(s_event_queue),
                                          EVENT_QUEUE_LENGTH)) {
            case queue_pressure_event_t::HIGH:
                ESP_LOGW(TAG, "Event queue consistently near full (%d deep, %" PRIu32
                         " dropped so far): a subscriber is slow", EVENT_QUEUE_LENGTH,
                         s_dropped.load());
                break;
            case queue_pressure_event_t::RECOVERED:
                ESP_LOGI(TAG, "Event queue pressure cleared");
                break;
            default:
                break;
            }

            /* Snapshot the subscriber table under the lock, then dispatch
               without it.  This prevents deadlock if a callback calls
               event_bus_subscribe() (e.g. a component that registers
               new subscriptions in response to EVENT_SYSTEM_BOOT_COMPLETE). */
            bus_core_sub_t snapshot[MAX_EVENT_SUBSCRIBERS];
            xSemaphoreTake(s_dispatch_mutex, portMAX_DELAY);
            xSemaphoreTake(s_subscriber_mutex, portMAX_DELAY);
            const size_t count = bus_core_snapshot(s_subscribers, snapshot);
            xSemaphoreGive(s_subscriber_mutex);

            bus_core_dispatch(snapshot, count, event);
            xSemaphoreGive(s_dispatch_mutex);
        }
    }
}

/* ── Public API ────────────────────────────────────────────────────────────── */

portunus_err_t event_bus_init(void)
{
    if (s_event_queue != NULL) {
        ESP_LOGW(TAG, "Event bus already initialised");
        return PORTUNUS_ERR_ALREADY_INIT;
    }

    /* Clear subscriber table. */
    bus_core_init(s_subscribers, s_subscriber_storage, MAX_EVENT_SUBSCRIBERS);

    /* Create the subscriber table and dispatch mutexes. */
    s_subscriber_mutex = xSemaphoreCreateMutex();
    s_dispatch_mutex   = xSemaphoreCreateMutex();
    if (s_subscriber_mutex == NULL || s_dispatch_mutex == NULL) {
        ESP_LOGE(TAG, "Failed to create event bus mutexes");
        return PORTUNUS_FAIL;
    }

    /* Create the dispatcher queue. */
    s_event_queue = xQueueCreate(EVENT_QUEUE_LENGTH, sizeof(portunus_event_t));
    if (s_event_queue == NULL) {
        ESP_LOGE(TAG, "Failed to create event queue");
        return PORTUNUS_ERR_QUEUE_CREATE;
    }

    /* Start the dispatcher task. */
    BaseType_t ret = xTaskCreate(
        event_bus_dispatch_task,
        "evt_dispatch",
        DISPATCH_TASK_STACK_SIZE,
        NULL,
        DISPATCH_TASK_PRIORITY,
        &s_dispatch_task
    );

    if (ret != pdPASS) {
        ESP_LOGE(TAG, "Failed to create dispatcher task");
        vQueueDelete(s_event_queue);
        s_event_queue = NULL;
        return PORTUNUS_ERR_TASK_CREATE;
    }

    ESP_LOGI(TAG, "Event bus initialised (queue depth=%d, max subscribers=%d)",
             EVENT_QUEUE_LENGTH, MAX_EVENT_SUBSCRIBERS);
    return PORTUNUS_OK;
}

portunus_err_t event_bus_publish(const portunus_event_t *event)
{
    if (event == NULL) {
        return PORTUNUS_ERR_INVALID_ARG;
    }
    if (s_event_queue == NULL) {
        return PORTUNUS_ERR_NOT_INIT;
    }

    TickType_t timeout = pdMS_TO_TICKS(EVENT_QUEUE_TIMEOUT_MS);
    if (xQueueSendToBack(s_event_queue, event, timeout) != pdTRUE) {
        ESP_LOGW(TAG, "Event queue full, dropping event id=0x%04x", event->id);
        s_dropped++;
        return PORTUNUS_ERR_QUEUE_FULL;
    }

    return PORTUNUS_OK;
}

portunus_err_t event_bus_publish_from_isr(const portunus_event_t *event,
                                          BaseType_t *higher_priority_woken)
{
    if (event == NULL) {
        return PORTUNUS_ERR_INVALID_ARG;
    }
    if (s_event_queue == NULL) {
        return PORTUNUS_ERR_NOT_INIT;
    }

    if (xQueueSendToBackFromISR(s_event_queue, event, higher_priority_woken) != pdTRUE) {
        s_dropped++;   /* lock-free atomic: safe from an ISR */
        return PORTUNUS_ERR_QUEUE_FULL;
    }

    return PORTUNUS_OK;
}

#ifdef CONFIG_PORTUNUS_TEST_HOOKS
void event_bus_reset_for_test(void)
{
    if (s_dispatch_task != NULL) {
        vTaskDelete(s_dispatch_task);
        s_dispatch_task = NULL;
    }
    if (s_event_queue != NULL) {
        vQueueDelete(s_event_queue);
        s_event_queue = NULL;
    }
    if (s_subscriber_mutex != NULL) {
        vSemaphoreDelete(s_subscriber_mutex);
        s_subscriber_mutex = NULL;
    }
    if (s_dispatch_mutex != NULL) {
        vSemaphoreDelete(s_dispatch_mutex);
        s_dispatch_mutex = NULL;
    }
    bus_core_init(s_subscribers, s_subscriber_storage, MAX_EVENT_SUBSCRIBERS);
    s_dropped        = 0;
    s_queue_pressure = {};
}
#endif

portunus_err_t event_bus_unsubscribe(portunus_event_id_t event_id,
                                     event_bus_handler_t handler,
                                     void *ctx)
{
    if (handler == NULL) {
        return PORTUNUS_ERR_INVALID_ARG;
    }
    if (s_subscriber_mutex == NULL) {
        return PORTUNUS_ERR_NOT_INIT;
    }

    xSemaphoreTake(s_subscriber_mutex, portMAX_DELAY);
    const portunus_err_t err = bus_core_unsubscribe(s_subscribers, event_id, handler, ctx);
    const size_t total = s_subscribers.count;
    xSemaphoreGive(s_subscriber_mutex);
    if (err != PORTUNUS_OK) {
        return err;
    }

    /* Later snapshots no longer include it; wait out one already taken. The
     * dispatcher itself cannot wait for the delivery it is running. */
    if (xTaskGetCurrentTaskHandle() != s_dispatch_task) {
        xSemaphoreTake(s_dispatch_mutex, portMAX_DELAY);
        xSemaphoreGive(s_dispatch_mutex);
    }
    ESP_LOGI(TAG, "Subscriber removed for event 0x%04x (total: %d)", event_id, (int)total);
    return PORTUNUS_OK;
}

uint32_t event_bus_dropped_events(void)
{
    return s_dropped.load();
}

portunus_err_t event_bus_subscribe(portunus_event_id_t event_id,
                                   event_bus_handler_t handler,
                                   void *ctx)
{
    if (handler == NULL) {
        return PORTUNUS_ERR_INVALID_ARG;
    }
    /* Before init there is no mutex to take: FreeRTOS asserts on NULL.
     * Components subscribe from their start() at boot, so refuse with an
     * error their fatal-subscription handling already reports. */
    if (s_subscriber_mutex == NULL) {
        return PORTUNUS_ERR_NOT_INIT;
    }

    xSemaphoreTake(s_subscriber_mutex, portMAX_DELAY);
    const portunus_err_t err = bus_core_subscribe(s_subscribers, event_id, handler, ctx);
    const size_t total = s_subscribers.count;
    xSemaphoreGive(s_subscriber_mutex);

    if (err == PORTUNUS_ERR_MAX_SUBSCRIBERS) {
        ESP_LOGE(TAG, "Subscriber table full (%d/%d)", (int)total, MAX_EVENT_SUBSCRIBERS);
    } else if (err == PORTUNUS_OK) {
        ESP_LOGI(TAG, "Subscriber registered for event 0x%04x (total: %d)",
                 event_id, (int)total);
    }
    return err;
}