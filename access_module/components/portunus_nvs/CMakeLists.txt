# components/portunus_nvs — runtime device configuration from NVS
#
# Reads deployment secrets (HMAC key, WiFi credentials) and site-specific
# values (module ID, server host/port) from the "portunus" NVS namespace at
# boot, replacing the former Kconfig compile-time constants.
#
# Consumers (wifi_mgr, server_comm) depend on this component and receive
# a populated portunus_device_config_t via their init functions.

idf_component_register(
    SRCS
        "src/portunus_nvs.cpp"
    INCLUDE_DIRS
        "include"
    REQUIRES
        nvs_flash
)
