#pragma once
#include "system_fsm.hpp"

/* Friend fixture declared in system_fsm.hpp. Drives the FSM synchronously
 * under FakeClock without starting FreeRTOS tasks: each method is one step of
 * SystemFSM::run() executed on the test task. */
class SystemFSMTestFixture {
public:
    explicit SystemFSMTestFixture(SystemFSM &fsm) : m_fsm(fsm) {}

    /* Drive an event directly into process_event (bypasses the queue). */
    void inject(const portunus_event_t &event) { m_fsm.process_event(event); }

    /* Deliver through the event-bus bridge, exactly as the dispatcher would. */
    void bridge(const portunus_event_t &event) { SystemFSM::on_event_bus_event(&event, &m_fsm); }

    /* Pending events in the FSM's internal queue (bridge back-pressure). */
    UBaseType_t queued() const { return uxQueueMessagesWaiting(m_fsm.m_event_queue); }

    /* One reed-switch poll (run() step 3). */
    void poll_reed() { if (m_fsm.m_caps.has_access_point) m_fsm.poll_reed_switch(); }

    /* One hold-timer check (run() step 4); call after advancing FakeClock. */
    void tick_unlock_timer() { if (m_fsm.m_strike_energized) m_fsm.check_unlock_timer(); }

    /* One re-lock retry (run() step 3b), as run() calls it. */
    void tick_relock_retry() { if (m_fsm.m_relock_retry) m_fsm.retry_relock(); }

    /* One decision-timeout check (run() step 5); call after advancing FakeClock. */
    void tick_decision_timeout() { m_fsm.check_decision_timeout(); }

    /* Network capability seam: tests set it explicitly instead of inheriting
     * whatever CONFIG_PORTUNUS_ENABLE_WIFI happens to be in sdkconfig. */
    void set_network(bool online) { m_fsm.m_caps.has_network = online; }

    bool    strike_energized() const { return m_fsm.m_strike_energized; }
    int64_t unlock_deadline()  const { return m_fsm.m_unlock_deadline_ms; }
    int     pending_decisions() const { return m_fsm.m_pending_decisions; }
    bool    last_door_open()   const { return m_fsm.m_last_door_open; }
    bool    relock_retry()     const { return m_fsm.m_relock_retry; }
    bool    lock_fault()       const { return m_fsm.m_lock_fault; }

private:
    SystemFSM &m_fsm;
};
