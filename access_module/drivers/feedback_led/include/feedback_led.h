/**
 * @file feedback_led.h
 * @brief IFeedback implementation using a single status LED.
 *
 * Owns a FreeRTOS task that executes LED blink patterns.  indicate()
 * is non-blocking — it sends the new pattern to the task via a
 * FreeRTOS task notification, which preempts any in-progress pattern.
 *
 * Pattern types:
 *   - One-shot (ACCESS_GRANTED, ACCESS_DENIED): run to completion,
 *     then return to idle (LED off).
 *   - Continuous (SYSTEM_READY, SYSTEM_ERROR): loop until replaced.
 *   - Held (CARD_READ): LED stays on until replaced.
 *   - NONE: cancel current pattern, LED off immediately.
 *
 * Interface: IFeedback (portunus_interfaces)
 */

#pragma once

#include "i_feedback.h"

#include "freertos/FreeRTOS.h"
#include "freertos/task.h"

/**
 * @brief Concrete feedback module backed by a single GPIO LED.
 */
class FeedbackLed : public IFeedback {
public:
    FeedbackLed() = default;

    portunus_err_t init() override;
    void           indicate(feedback_type_t type) override;

private:
    TaskHandle_t m_task_handle = nullptr;

    static void pattern_task(void *arg);
    void run_patterns();
};
