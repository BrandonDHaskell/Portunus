# services/event_bus — Inter-component messaging backbone
#
# FreeRTOS queue-backed publish/subscribe event bus. For the MVP a single
# dispatcher queue is used (see Section 3.5 of the project plan). The
# queue topology can be revisited if subscriber count or event throughput
# grows beyond what the single queue can handle.
#
# Depends on common/types for typed event IDs (event_types.h).

idf_component_register(
    SRCS
        "src/event_bus.cpp"
    INCLUDE_DIRS
        "include"
    REQUIRES
        freertos
        types
        config
)