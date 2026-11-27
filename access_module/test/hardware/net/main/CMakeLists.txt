idf_component_register(
    SRCS "test_hw_net_main.cpp"
    INCLUDE_DIRS "."
    REQUIRES
        unity
        system_fsm
        heartbeat_service
        server_comm
        wifi_mgr
        event_bus
        access_point_gpio
        reader_mfrc522
        feedback_led
        portunus_clock
        portunus_nvs
        portunus_config
        portunus_types
        portunus_interfaces
        nvs_flash
        driver
        esp_driver_usb_serial_jtag
        vfs
        esp_wifi
        esp_event
        esp_timer
)
