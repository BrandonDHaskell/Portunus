#pragma once
#include "i_access_point.hpp"

class FakeAccessPoint : public IAccessPoint {
public:
    /* Knobs */
    portunus_err_t init_result   = PORTUNUS_OK;
    portunus_err_t unlock_result = PORTUNUS_OK;
    portunus_err_t lock_result   = PORTUNUS_OK;
    bool           door_open     = false;  /* is_open() returns this */

    /* Counters */
    int unlocks = 0;
    int locks   = 0;

    /* Observed state */
    bool locked = true;  /* starts locked; updated by unlock/lock */

    portunus_err_t init() override { return init_result; }

    portunus_err_t unlock() override {
        if (unlock_result == PORTUNUS_OK) {
            unlocks++;
            locked = false;
        }
        return unlock_result;
    }

    portunus_err_t lock() override {
        if (lock_result == PORTUNUS_OK) {
            locks++;
            locked = true;
        }
        return lock_result;
    }

    bool is_open() override { return door_open; }
};
