/* Detects a FreeRTOS queue that stays close to full, a sign that its consumer
 * cannot keep up (slow network, a stuck peer, failing hardware) before it
 * starts dropping. Pure: the consumer task feeds it the depth it observes on
 * each receive, so one detector is only ever touched by one task. */
#pragma once

#include <stddef.h>
#include <stdint.h>

/** Samples at or above 75% of capacity, in a row, before warning. */
static constexpr uint8_t QUEUE_PRESSURE_STREAK = 5;

enum class queue_pressure_event_t : uint8_t {
    NONE,
    HIGH,       /**< Stayed at >= 75% for QUEUE_PRESSURE_STREAK samples: warn. */
    RECOVERED,  /**< Fell below 50% after a warning: log, and re-arm. */
};

struct queue_pressure_t {
    uint8_t streak = 0;      /**< Consecutive high samples. */
    bool    warned = false;  /**< HIGH reported, not yet RECOVERED. */
};

/** Feed the depth seen on a receive (items still queued) and the capacity.
 *  Reports HIGH once per episode and RECOVERED once it drains. */
queue_pressure_event_t queue_pressure_sample(queue_pressure_t &qp, size_t depth, size_t capacity);
