idf_component_register(
    SRCS "src/reed_switch.cpp"
    INCLUDE_DIRS "include"
    REQUIRES driver device_state
)
