/**
 * @file event_bus.cpp
 * @brief Single-dispatcher-queue event bus implementation.
 *
 * MVP topology: one FreeRTOS queue, one dispatcher task, static subscriber
 * table. See project plan §3.5 for the rationale and scaling notes.
 */

#include "event_bus.h"
#include "error_codes.h"
#include "timing_config.h"

#include "freertos/FreeRTOS.h"
#include "freertos/task.h"
#include "freertos/queue.h"
#include "freertos/semphr.h"
#include "esp_log.h"

#include <string.h>

static const char *TAG = "event_bus";
static SemaphoreHandle_t s_subscriber_mutex = NULL;

/* ── Subscriber table ──────────────────────────────────────────────────────── */

typedef struct {
    portunus_event_id_t event_id;
    event_bus_handler_t handler;
    void               *ctx;
    bool                active;
} subscriber_entry_t;

static subscriber_entry_t s_subscribers[MAX_EVENT_SUBSCRIBERS];
static size_t             s_subscriber_count = 0;

/* ── Queue and task handles ────────────────────────────────────────────────── */

static QueueHandle_t s_event_queue  = NULL;
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
            /* Snapshot the subscriber table under the lock, then dispatch
               without it.  This prevents deadlock if a callback calls
               event_bus_subscribe() (e.g. a component that registers
               new subscriptions in response to EVENT_SYSTEM_BOOT_COMPLETE). */
            xSemaphoreTake(s_subscriber_mutex, portMAX_DELAY);
            size_t count = s_subscriber_count;
            subscriber_entry_t snapshot[MAX_EVENT_SUBSCRIBERS];
            memcpy(snapshot, s_subscribers, count * sizeof(subscriber_entry_t));
            xSemaphoreGive(s_subscriber_mutex);

            for (size_t i = 0; i < count; i++) {
                if (snapshot[i].active &&
                    snapshot[i].event_id == event.id) {
                    snapshot[i].handler(&event, snapshot[i].ctx);
                }
            }
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
    memset(s_subscribers, 0, sizeof(s_subscribers));
    s_subscriber_count = 0;

    /* Create subscriber table mutex. */
    s_subscriber_mutex = xSemaphoreCreateMutex();
    if (s_subscriber_mutex == NULL) {
        ESP_LOGE(TAG, "Failed to create subscriber mutex");
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
        return PORTUNUS_ERR_QUEUE_FULL;
    }

    return PORTUNUS_OK;
}

portunus_err_t event_bus_subscribe(portunus_event_id_t event_id,
                                   event_bus_handler_t handler,
                                   void *ctx)
{
    if (handler == NULL) {
        return PORTUNUS_ERR_INVALID_ARG;
    }

    xSemaphoreTake(s_subscriber_mutex, portMAX_DELAY);

    if (s_subscriber_count >= MAX_EVENT_SUBSCRIBERS) {
        ESP_LOGE(TAG, "Subscriber table full (%d/%d)",
                 (int)s_subscriber_count, MAX_EVENT_SUBSCRIBERS);
        xSemaphoreGive(s_subscriber_mutex);
        return PORTUNUS_ERR_MAX_SUBSCRIBERS;
    }

    subscriber_entry_t *entry = &s_subscribers[s_subscriber_count];
    entry->event_id = event_id;
    entry->handler  = handler;
    entry->ctx      = ctx;
    entry->active   = true;
    s_subscriber_count++;

    ESP_LOGI(TAG, "Subscriber registered for event 0x%04x (total: %d)",
             event_id, (int)s_subscriber_count);

    xSemaphoreGive(s_subscriber_mutex);
    return PORTUNUS_OK;
}