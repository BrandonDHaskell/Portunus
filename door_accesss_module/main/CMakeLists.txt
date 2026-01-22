idf_component_register(
    SRCS "app_main.cpp"
    INCLUDE_DIRS "."
    REQUIRES nvs_flash portunus_config device_state wifi_manager http_client heartbeat
             door_strike rfid_reader reed_switch status_led
)
