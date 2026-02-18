/**
 * @file heartbeat_service.cpp
 * @brief Heartbeat service implementation.
 *
 * A FreeRTOS task wakes at HEARTBEAT_INTERVAL_MS, collects basic health
 * telemetry, and publishes an EVENT_HEARTBEAT to the event bus.
 */

#include "heartbeat_service.h"
#include "event_bus.h"
#include "event_types.h"
#include "error_codes.h"
#include "timing_config.h"

#include "freertos/FreeRTOS.h"
#include "freertos/task.h"
#include "esp_log.h"
#include "esp_timer.h"
#include "esp_system.h"

#include <string.h>

static const char *TAG = "heartbeat";

#define HEARTBEAT_TASK_STACK_SIZE  3072
#define HEARTBEAT_TASK_PRIORITY    3       /* Lower priority than event dispatcher */

static TaskHandle_t s_heartbeat_task = NULL;
static uint32_t     s_sequence       = 0;

static void heartbeat_task(void *arg)
{
    (void)arg;
    TickType_t interval = pdMS_TO_TICKS(HEARTBEAT_INTERVAL_MS);
    TickType_t last_wake = xTaskGetTickCount();

    ESP_LOGI(TAG, "Heartbeat task started (interval=%d ms)", HEARTBEAT_INTERVAL_MS);

    for (;;) {
        vTaskDelayUntil(&last_wake, interval);

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
    }
}

portunus_err_t heartbeat_service_start(void)
{
    if (s_heartbeat_task != NULL) {
        ESP_LOGW(TAG, "Heartbeat service already running");
        return PORTUNUS_ERR_ALREADY_INIT;
    }

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

void heartbeat_service_stop(void)
{
    if (s_heartbeat_task != NULL) {
        vTaskDelete(s_heartbeat_task);
        s_heartbeat_task = NULL;
        s_sequence = 0;
        ESP_LOGI(TAG, "Heartbeat service stopped");
    }
}