#pragma once
#include "provisioning_fsm.hpp"

/* Friend fixture declared in provisioning_fsm.hpp. Drives the PEU FSM
 * synchronously under FakeClock without starting FreeRTOS tasks: each method
 * is one step of ProvisioningFSM::start() or run() executed on the test task. */
class ProvisioningFSMTestFixture {
public:
    explicit ProvisioningFSMTestFixture(ProvisioningFSM &fsm) : m_fsm(fsm) {}

    /* The state start() enters, without subscribing or creating tasks. */
    void enter_initial_state() { m_fsm.enter_initial_state(); }

    /* Drive an event directly into process_event (bypasses the queue). */
    void inject(const portunus_event_t &event) { m_fsm.process_event(event); }

    /* Deliver through the event-bus bridge, exactly as the dispatcher would. */
    void bridge(const portunus_event_t &event) { ProvisioningFSM::on_event_bus_event(&event, &m_fsm); }

    /* run() step 1 with no wait; false if the queue was empty. */
    bool drain_one() { return m_fsm.receive_and_process(0); }

    /* run() step 1b: react to events lost since the last check. */
    void check_dropped() { m_fsm.check_dropped_events(); }

    uint32_t dropped() const { return m_fsm.m_dropped.load(); }

    /* One timeout check (run() step 2); call after advancing FakeClock. */
    void tick_timeout() { m_fsm.check_timeout(); }

    int64_t deadline() const { return m_fsm.m_deadline_ms; }
    bool    has_arm()  const { return m_fsm.m_has_arm; }

private:
    ProvisioningFSM &m_fsm;
};
