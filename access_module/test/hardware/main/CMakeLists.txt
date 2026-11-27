set(AM ${CMAKE_CURRENT_LIST_DIR}/../../..)  # access_module root

# reader_health.cpp is the FSM's pure fault classifier (no ESP-IDF calls),
# compiled in so HW-05 judges reads exactly as the firmware does.
idf_component_register(
    SRCS
        "test_hw_main.cpp"
        "${AM}/core/system_fsm/src/reader_health.cpp"
    INCLUDE_DIRS
        "."
        "${AM}/core/system_fsm/include"
    REQUIRES
        unity
        access_point_gpio
        reader_mfrc522
        portunus_config
        portunus_types
        portunus_interfaces
        driver
        esp_driver_pcnt
        esp_driver_usb_serial_jtag
        vfs
        esp_timer
)
