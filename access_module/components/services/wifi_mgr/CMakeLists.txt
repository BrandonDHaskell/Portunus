# services/wifi_mgr — WiFi station manager with automatic reconnection
#
# Manages the ESP32 WiFi STA interface: initialisation, connection, and
# exponential-backoff reconnection on disconnect.  Configuration (SSID,
# password, timeouts) is sourced from Kconfig via common/config.
#
# Depends on ESP-IDF WiFi, networking, and event components, plus
# common/types for Portunus error codes and common/config for
# network_config.h timing/credential constants.

idf_component_register(
    SRCS
        "src/wifi_mgr.cpp"
    INCLUDE_DIRS
        "include"
    REQUIRES
        freertos
        esp_wifi
        esp_netif
        esp_event
        types
        config
)