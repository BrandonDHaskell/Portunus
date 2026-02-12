# services/heartbeat_service — Periodic health reporting
#
# Emits heartbeat events at a configurable interval via the event bus.
# For the MVP, heartbeats are logged to the serial console and published
# as event bus messages. Server transmission is deferred to Phase 3.
#
# Depends on event_bus for publishing heartbeat events, common/types for
# event type definitions, and common/config for timing parameters.

idf_component_register(
    SRCS
        "src/heartbeat_service.cpp"
    INCLUDE_DIRS
        "include"
    REQUIRES
        freertos
        esp_timer
        event_bus
        types
        config
)