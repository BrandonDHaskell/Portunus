# core/provisioning_fsm — Two-scan provisioning console FSM
#
# provisioning_fsm.cpp is only compiled when the PROVISIONING_CONSOLE variant
# is selected.  The component is still registered (with empty SRCS) for the
# ACCESS_POINT build so that nothing in EXTRA_COMPONENT_DIRS breaks CMake
# dependency resolution.  This mirrors the pattern used by grpc_client.

if(CONFIG_PORTUNUS_MODULE_TYPE_PROVISIONING_CONSOLE)
    set(_prov_srcs "src/provisioning_fsm.cpp")
else()
    set(_prov_srcs "")
endif()

idf_component_register(
    SRCS ${_prov_srcs}
    INCLUDE_DIRS "include"
    REQUIRES
        portunus_interfaces
        portunus_types
    PRIV_REQUIRES
        event_bus
        freertos
        esp_timer
        wifi_mgr
        mbedtls
)
