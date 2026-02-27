/**
 * @file i_access_point.h
 * @brief Abstract interface for door access point hardware.
 *
 * Wraps the door strike actuator and door state sensor (reed switch)
 * behind a hardware-independent interface.  The FSM calls unlock()
 * and lock() to control the strike and is_open() to monitor door
 * state.  All timing and policy decisions live in the FSM — this
 * interface is a thin hardware wrapper.
 *
 * Current implementation: GpioAccessPoint (wraps door_strike + reed_switch drivers).
 *
 * Architecture layer: Module (see project plan §3.1–3.2).
 */

#pragma once

#include "portunus_types.h"

/**
 * @brief Abstract access point interface.
 *
 * Design rule: no timing, no policy, no awareness of WHY an operation
 * is being called.  The FSM owns all "when" and "how long" decisions.
 * This keeps the module interface stable even as business logic evolves.
 */
class IAccessPoint {
public:
    virtual ~IAccessPoint() = default;

    /**
     * @brief Initialise the door strike and door state sensor hardware.
     *
     * The strike must be in the locked (de-energized) state after init.
     * Fail-secure by default.
     *
     * @return PORTUNUS_OK on success, or a driver-specific error code.
     */
    virtual portunus_err_t init() = 0;

    /**
     * @brief Energize the door strike (unlock).
     *
     * Returns immediately.  The strike stays energized until lock()
     * is called — the FSM manages the hold timer.
     *
     * @return PORTUNUS_OK on success.
     */
    virtual portunus_err_t unlock() = 0;

    /**
     * @brief De-energize the door strike (lock).
     *
     * Returns immediately.
     *
     * @return PORTUNUS_OK on success.
     */
    virtual portunus_err_t lock() = 0;

    /**
     * @brief Read the current door state from the sensor.
     *
     * Returns the debounced state.  Debounce logic lives in the
     * concrete implementation or the underlying driver, not in the
     * FSM.
     *
     * @return true if the door is physically open, false if closed.
     */
    virtual bool is_open() = 0;
};
