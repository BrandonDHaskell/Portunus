/**
 * @file heartbeat_service.cpp
 * @brief Heartbeat service implementation.
 *
 * A FreeRTOS task wakes at HEARTBEAT_INTERVAL_MS, collects basic health
 * telemetry, and publishes an EVENT_HEARTBEAT to the event bus.
 */

#include "heartbeat_service.hpp"
#include "event_bus.hpp"
#include "event_types.hpp"
#include "error_codes.hpp"
#include "timing_config.hpp"

#include "freertos/FreeRTOS.h"
#include "freertos/task.h"
#include "freertos/semphr.h"
#include "esp_log.h"
#include "esp_timer.h"
#include "esp_system.h"

#include <string.h>
#include <inttypes.h>
#include <atomic>

static const char *TAG = "heartbeat";

#define HEARTBEAT_TASK_STACK_SIZE  3072
#define HEARTBEAT_TASK_PRIORITY    3       /* Lower priority than event dispatcher */

static TaskHandle_t s_heartbeat_task = NULL;
static uint32_t     s_sequence       = 0;

/* Cooperative shutdown (D-T9): stop sets the flag and wakes the task, which
 * exits at the top of its loop and signals s_exited. */
static std::atomic<bool> s_stop_requested{false};
static SemaphoreHandle_t s_exited = NULL;

/* The task blocks at most in one event_bus_publish (EVENT_QUEUE_TIMEOUT_MS). */
#define HEARTBEAT_STOP_TIMEOUT_MS  1000

static void heartbeat_task(void *arg)
{
    (void)arg;
    TickType_t interval = pdMS_TO_TICKS(HEARTBEAT_INTERVAL_MS);
    TickType_t last_wake = xTaskGetTickCount();

    ESP_LOGI(TAG, "Heartbeat task started (interval=%d ms)", HEARTBEAT_INTERVAL_MS);

    /* Publish the first heartbeat immediately rather than one interval after
     * boot: it is what registers the module as alive and syncs its clock. */
    while (!s_stop_requested) {
        int64_t now_us = esp_timer_get_time();

        portunus_event_t event;
        memset(&event, 0, sizeof(event));
        event.id = EVENT_HEARTBEAT;
        event.payload.heartbeat.sequence        = s_sequence;
        event.payload.heartbeat.uptime_sec      = (uint32_t)(now_us / 1000000);
        event.payload.heartbeat.free_heap_bytes = esp_get_free_heap_size();

        portunus_err_t err = event_bus_publish(&event);
        if (err != PORTUNUS_OK) {
            ESP_LOGW(TAG, "Failed to publish heartbeat #%" PRIu32 ": err=%d",
                     s_sequence, (int)err);
            /* Sequence not incremented — the same number will be retried
               on the next interval so the server sees no gaps. */
        } else {
            s_sequence++;
        }

        if (event.payload.heartbeat.sequence % 100 == 0) {
            ESP_LOGI(TAG, "Heartbeat #%" PRIu32 " | uptime=%" PRIu32 "s | heap=%" PRIu32,
                     event.payload.heartbeat.sequence,
                     event.payload.heartbeat.uptime_sec,
                     event.payload.heartbeat.free_heap_bytes);
        } else {
            ESP_LOGD(TAG, "Heartbeat #%" PRIu32 " | uptime=%" PRIu32 "s | heap=%" PRIu32,
                 event.payload.heartbeat.sequence,
                 event.payload.heartbeat.uptime_sec,
                 event.payload.heartbeat.free_heap_bytes);
        }

        /* Same drift-free period as vTaskDelayUntil, but a notification
         * (from stop) ends the wait at once. */
        const TickType_t elapsed = xTaskGetTickCount() - last_wake;
        ulTaskNotifyTake(pdTRUE, elapsed < interval ? interval - elapsed : 0);
        last_wake += interval;
    }

    xSemaphoreGive(s_exited);
    vTaskDelete(NULL);
}

portunus_err_t heartbeat_service_start(void)
{
    if (s_heartbeat_task != NULL) {
        ESP_LOGW(TAG, "Heartbeat service already running");
        return PORTUNUS_ERR_ALREADY_INIT;
    }
    if (s_exited == NULL) {
        s_exited = xSemaphoreCreateBinary();
        if (s_exited == NULL) {
            return PORTUNUS_ERR_TASK_CREATE;
        }
    }
    s_stop_requested = false;

    BaseType_t ret = xTaskCreate(
        heartbeat_task,
        "heartbeat",
        HEARTBEAT_TASK_STACK_SIZE,
        NULL,
        HEARTBEAT_TASK_PRIORITY,
        &s_heartbeat_task
    );

    if (ret != pdPASS) {
        ESP_LOGE(TAG, "Failed to create heartbeat task");
        return PORTUNUS_ERR_TASK_CREATE;
    }

    return PORTUNUS_OK;
}

portunus_err_t heartbeat_service_stop(void)
{
    if (s_heartbeat_task == NULL) {
        return PORTUNUS_OK;
    }
    s_stop_requested = true;
    xTaskNotifyGive(s_heartbeat_task);
    if (xSemaphoreTake(s_exited, pdMS_TO_TICKS(HEARTBEAT_STOP_TIMEOUT_MS)) != pdTRUE) {
        ESP_LOGE(TAG, "Heartbeat task did not stop within %d ms", HEARTBEAT_STOP_TIMEOUT_MS);
        return PORTUNUS_ERR_TIMEOUT;
    }
    s_heartbeat_task = NULL;
    s_sequence       = 0;
    ESP_LOGI(TAG, "Heartbeat service stopped");
    return PORTUNUS_OK;
}
