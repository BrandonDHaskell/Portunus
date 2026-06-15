# services/wifi_mgr — WiFi station manager with automatic reconnection
#
# Manages the ESP32 WiFi STA interface: initialisation, connection, and
# exponential-backoff reconnection on disconnect.  Timing constants come from
# portunus_config; credentials (SSID, PSK) are passed in at init time from
# portunus_nvs rather than baked in via Kconfig.

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
        portunus_types
        portunus_config
        portunus_nvs
)