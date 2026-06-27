# main — Application entry point and composition root
#
# This is the only component that depends on both the FSM and the
# concrete driver implementations. It constructs driver instances
# and injects them into the FSM.

idf_component_register(
    SRCS "main.cpp"
    INCLUDE_DIRS "."
    REQUIRES
        nvs_flash
        system_fsm
        provisioning_fsm
        event_bus
        portunus_types
        portunus_config
        portunus_clock
    PRIV_REQUIRES
        reader_mfrc522
        access_point_gpio
        feedback_led
        heartbeat_service
        wifi_mgr
        server_comm
        portunus_nvs
        arm_button_gpio
)
