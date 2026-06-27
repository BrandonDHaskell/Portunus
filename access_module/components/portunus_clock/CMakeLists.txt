idf_component_register(
    SRCS "src/esp_timer_clock.cpp"
    INCLUDE_DIRS "include"
    REQUIRES portunus_interfaces
    PRIV_REQUIRES esp_timer)
