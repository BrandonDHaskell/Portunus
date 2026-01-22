idf_component_register(
    SRCS "src/heartbeat.cpp"
    INCLUDE_DIRS "include"
    REQUIRES freertos json esp_timer portunus_config device_state wifi_manager http_client
)
