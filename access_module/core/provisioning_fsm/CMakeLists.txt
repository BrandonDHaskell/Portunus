# core/provisioning_fsm — Two-scan provisioning console FSM
#
# Built only when CONFIG_PORTUNUS_MODULE_TYPE_PROVISIONING_CONSOLE=y.
# Requires mbedtls for SHA-256 credential hashing.

idf_component_register(
    SRCS "src/provisioning_fsm.cpp"
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
