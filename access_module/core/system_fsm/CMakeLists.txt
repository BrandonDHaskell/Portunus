# core/system_fsm — Central state machine orchestrator
#
# Programs against portunus_interfaces only — never depends on specific
# drivers. Concrete driver instances are injected via constructor.

set(_reqs
    portunus_interfaces
    portunus_types
    portunus_config
    freertos
    esp_timer
    event_bus
)

idf_component_register(
    SRCS "src/system_fsm.cpp"
    INCLUDE_DIRS "include"
    REQUIRES
        portunus_interfaces
        portunus_types
    PRIV_REQUIRES
        event_bus
        portunus_config
        freertos
        esp_timer
        wifi_mgr
)
