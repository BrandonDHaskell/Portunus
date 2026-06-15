/**
 * @file i_arm.h
 * @brief Abstract interface for the PEU arm trigger input.
 *
 * The arm trigger signals to the PEU FSM that an operator wants to begin an
 * enrolment session. Typically implemented as a momentary push-button, but
 * can be replaced by any hardware (capacitive touch, RFID command, etc.)
 * without changing the FSM.
 *
 * poll_arm() returns true on a single edge — only once per button press, not
 * continuously while held. It must be called regularly (e.g. every 100 ms)
 * from the FSM poll task.
 */

#pragma once

#include "portunus_types.hpp"

/**
 * @brief Abstract arm-trigger interface.
 */
class IArm {
public:
    virtual ~IArm() = default;

    /**
     * @brief Initialise the arm trigger hardware.
     * @return PORTUNUS_OK on success, driver-specific error otherwise.
     */
    virtual portunus_err_t init() = 0;

    /**
     * @brief Poll for an arm-trigger event.
     *
     * Returns true exactly once per press (edge-detected, debounced).
     * Returns false when idle or when the button is held between polls.
     * Must not block.
     *
     * @return true if a new arm trigger event has occurred since the last call.
     */
    virtual bool poll_arm() = 0;
};
