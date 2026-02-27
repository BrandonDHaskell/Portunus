# main — Application entry point and composition root
#
# This is the only component that depends on both the FSM and the
# concrete driver implementations. It constructs driver instances
# and injects them into the FSM.

# Build requirements are driven by Kconfig feature toggles.
# This keeps "disabled" hardware/service components from being built
# (and accidentally pulling in Kconfig symbols that are not defined
# when a feature is off).

set(_reqs
    nvs_flash
    system_fsm
    event_bus
    portunus_types
    portunus_config
)

if(CONFIG_PORTUNUS_ENABLE_MFRC522)
    list(APPEND _priv_reqs reader_mfrc522)
endif()

if(CONFIG_PORTUNUS_ENABLE_DOOR_STRIKE OR CONFIG_PORTUNUS_ENABLE_REED_SWITCH)
    list(APPEND _priv_reqs access_point_gpio)
endif()

if(CONFIG_PORTUNUS_ENABLE_LED)
    list(APPEND _priv_reqs feedback_led)
endif()

if(CONFIG_PORTUNUS_ENABLE_HEARTBEAT)
    list(APPEND _priv_reqs heartbeat_service)
endif()

if(CONFIG_PORTUNUS_ENABLE_WIFI)
    list(APPEND _priv_reqs wifi_mgr server_comm)
endif()

idf_component_register(
    SRCS "main.cpp"
    INCLUDE_DIRS "."
    PRIV_REQUIRES ${_priv_reqs}
)
