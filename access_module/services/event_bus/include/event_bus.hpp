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
 *   - event_bus_subscribe() is safe to call from any task at any time
 *     after event_bus_init(). The subscriber table is mutex-protected.
 */

#pragma once

#include "event_types.hpp"
#include "portunus_types.hpp"
#include "freertos/FreeRTOS.h"
#include "sdkconfig.h"

#include <stdint.h>

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
 * @return PORTUNUS_OK on success, PORTUNUS_ERR_MAX_SUBSCRIBERS if the table is
 *         full, PORTUNUS_ERR_INVALID_ARG for a NULL handler, or
 *         PORTUNUS_ERR_NOT_INIT before event_bus_init().
 */
portunus_err_t event_bus_subscribe(portunus_event_id_t event_id,
                                   event_bus_handler_t handler,
                                   void *ctx);

/**
 * @brief Remove a subscription made with event_bus_subscribe() (same id,
 * handler and ctx; the first match if subscribed more than once).
 *
 * When it returns, the handler is not running and will not run again, so the
 * caller may free ctx: it waits for a dispatch already delivering to it.
 * Called from inside a handler (the dispatcher task) it cannot wait for the
 * delivery it is part of; the handler is then only guaranteed not to run for
 * later events.
 *
 * @return PORTUNUS_OK, PORTUNUS_ERR_INVALID_ARG (NULL handler),
 *         PORTUNUS_ERR_NOT_INIT, or PORTUNUS_ERR_NOT_SUBSCRIBED.
 */
portunus_err_t event_bus_unsubscribe(portunus_event_id_t event_id,
                                     event_bus_handler_t handler,
                                     void *ctx);

/**
 * @brief Publishes refused with PORTUNUS_ERR_QUEUE_FULL since init (D-T5).
 * Each refusal is also reported to its publisher, which answers it; this
 * count is for logs and telemetry.
 */
uint32_t event_bus_dropped_events(void);

/**
 * @brief Count an event a subscriber lost after the bus delivered it, because
 * the subscriber's own queue was full. Safe from any task, including a
 * handler running on the dispatcher.
 */
void event_bus_note_subscriber_drop(void);

/**
 * @brief Every event lost since init: publishes the bus refused plus drops
 * subscribers reported. Each loss is counted once. Reported in heartbeats.
 */
uint32_t event_bus_lost_events(void);

#ifdef CONFIG_PORTUNUS_TEST_HOOKS
/**
 * @brief Test only: return the bus to its pre-init state.
 *
 * Deletes the dispatcher task, queue and mutex and forgets every subscriber,
 * so the next event_bus_init() starts clean. Call only while the bus is idle
 * (no handler running, no publisher mid-call). CONFIG_PORTUNUS_TEST_HOOKS is
 * defined only by the component tier's Kconfig, never by firmware builds
 * (decision D-T2).
 */
void event_bus_reset_for_test(void);
#endif

#ifdef __cplusplus
}
#endif