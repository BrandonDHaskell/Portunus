/**
 * @file access_point_gpio.h
 * @brief IAccessPoint implementation using GPIO door strike and reed switch.
 *
 * Wraps GPIO-based door strike control and reed switch sensing behind
 * the standard IAccessPoint interface.  The door strike and reed switch
 * HAL code is internal to this component.
 *
 * Interface: IAccessPoint (portunus_interfaces)
 */

#pragma once

#include "i_access_point.h"

/**
 * @brief Concrete access point backed by GPIO door strike + reed switch.
 */
class AccessPointGpio : public IAccessPoint {
public:
    AccessPointGpio() = default;

    portunus_err_t init() override;
    portunus_err_t unlock() override;
    portunus_err_t lock() override;
    bool           is_open() override;
};
