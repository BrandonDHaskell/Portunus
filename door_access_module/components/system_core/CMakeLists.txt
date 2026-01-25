idf_component_register(
  SRCS
    "src/portunus_event_bus.cpp"
    "src/portunus_module_registry.cpp"
    "src/portunus_system_fsm.cpp"
    "src/modules/wifi_sta_module.cpp"
    "src/modules/rfid_reader_module.cpp"
    "src/modules/heartbeat_module.cpp"
    "src/modules/door_strike_module.cpp"
    "src/modules/status_led_module.cpp"
    "src/modules/device_state_monitor_module.cpp"
  INCLUDE_DIRS "include"
  REQUIRES freertos esp_timer log
           wifi_manager rfid_reader heartbeat device_state
           door_strike status_led reed_switch
)