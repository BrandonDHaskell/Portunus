#pragma once
#include "i_arm.hpp"
#include "error_codes.hpp"

/* Arm button double. The component tier injects EVENT_ARM_REQUESTED directly
 * instead of running the poll task, so poll_arm() never reports a press;
 * init_result exercises the "button failed, run always-armed" path. */
class FakeArm : public IArm {
public:
    portunus_err_t init_result = PORTUNUS_OK;

    portunus_err_t init() override { return init_result; }
    bool poll_arm() override { return false; }
};
