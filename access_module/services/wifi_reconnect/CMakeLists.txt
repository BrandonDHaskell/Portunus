# services/wifi_reconnect: the WiFi reconnect task, without the WiFi driver.
#
# wifi_mgr owns the driver and its events; this component owns the task that
# waits out the backoff and calls back to reconnect. It depends only on
# FreeRTOS, so the component tier tests its lifecycle on the linux target,
# where esp_wifi does not exist.

idf_component_register(
    SRCS
        "src/wifi_reconnect.cpp"
        "src/wifi_backoff.cpp"
    INCLUDE_DIRS
        "include"
    REQUIRES
        freertos
        log
        portunus_types
)
