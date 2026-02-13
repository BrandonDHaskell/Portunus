# main/ — Application entry point (ESP-IDF convention)

idf_component_register(
    SRCS
        "main.cpp"
    INCLUDE_DIRS
        "."
    REQUIRES
        nvs_flash
        event_bus
        heartbeat_service
        mfrc522
        wifi_mgr
        types
        config
)