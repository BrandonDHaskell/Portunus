/**
 * @file event_bus.h
 * @brief FreeRTOS queue-backed publish/subscribe event bus.
 *
 * Architecture: single dispatcher queue (MVP topology — see project plan §3.5).
 *
 * Publishers call event_bus_publish() to enqueue an event. A dedicated
 * dispatcher task dequeues events and invokes all registered subscriber
 * callbacks whose event ID filter matches. Callbacks execute on the
 * dispatcher task's stack, so they must be short and non-blocking.
 *
 * Thread safety:
 *   - event_bus_publish() is safe to call from any task or ISR (uses
 *     xQueueSendToBack / xQueueSendToBackFromISR).
 *   - event_bus_subscribe() must be called before the dispatcher is
 *     started (i.e. during initialisation) or externally serialised.
 */

#pragma once

#include "event_types.h"
#include "portunus_types.h"
#include "freertos/FreeRTOS.h"

#ifdef __cplusplus
extern "C" {
#endif

/**
 * @brief Subscriber callback signature.
 *
 * @param event  Pointer to the received event (valid only for the duration
 *               of the callback — do not store the pointer).
 * @param ctx    Opaque user context provided at subscription time.
 */
typedef void (*event_bus_handler_t)(const portunus_event_t *event, void *ctx);

/**
 * @brief Initialise the event bus.
 *
 * Creates the dispatcher queue and starts the dispatcher FreeRTOS task.
 * Must be called exactly once before any publish or subscribe calls.
 *
 * @return PORTUNUS_OK on success, or an error code.
 */
portunus_err_t event_bus_init(void);

/**
 * @brief Publish an event to the bus.
 *
 * The event is copied into the dispatcher queue by value. If the queue
 * is full the call blocks for up to EVENT_QUEUE_TIMEOUT_MS before
 * returning PORTUNUS_ERR_QUEUE_FULL.
 *
 * Safe to call from any task. For ISR context use event_bus_publish_from_isr().
 *
 * @param event  Pointer to the event to publish (copied, caller retains ownership).
 * @return PORTUNUS_OK on success, PORTUNUS_ERR_QUEUE_FULL if the queue is full.
 */
portunus_err_t event_bus_publish(const portunus_event_t *event);

/**
 * @brief Publish an event from an ISR context.
 *
 * @param event                Pointer to the event to publish.
 * @param higher_priority_woken  Set to pdTRUE if a higher-priority task was woken.
 * @return PORTUNUS_OK on success.
 */
portunus_err_t event_bus_publish_from_isr(const portunus_event_t *event,
                                          BaseType_t *higher_priority_woken);

/**
 * @brief Register a subscriber callback for a specific event type.
 *
 * @param event_id  The event type to listen for.
 * @param handler   Callback function invoked on the dispatcher task.
 * @param ctx       Opaque context pointer passed to the handler (may be NULL).
 * @return PORTUNUS_OK on success, PORTUNUS_ERR_MAX_SUBSCRIBERS if the table is full.
 */
portunus_err_t event_bus_subscribe(portunus_event_id_t event_id,
                                   event_bus_handler_t handler,
                                   void *ctx);

#ifdef __cplusplus
}
#endif