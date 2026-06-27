#pragma once
#include "system_fsm.hpp"

/* Friend fixture declared in system_fsm.hpp. Provides direct access to
 * process_event and check_unlock_timer without starting FreeRTOS tasks,
 * so behavioral tests can drive the FSM synchronously under FakeClock. */
class SystemFSMTestFixture {
public:
    explicit SystemFSMTestFixture(SystemFSM &fsm) : m_fsm(fsm) {}

    /* Drive an event directly into the FSM without going through the queue. */
    void inject(const portunus_event_t &event) {
        m_fsm.process_event(event);
    }

    /* Tick the hold timer — call after advancing FakeClock. */
    void tick_unlock_timer() {
        if (m_fsm.m_strike_energized) {
            m_fsm.check_unlock_timer();
        }
    }

    /* Expose internal state for assertions. */
    bool strike_energized()    const { return m_fsm.m_strike_energized; }
    int64_t unlock_deadline()  const { return m_fsm.m_unlock_deadline_ms; }

private:
    SystemFSM &m_fsm;
};
