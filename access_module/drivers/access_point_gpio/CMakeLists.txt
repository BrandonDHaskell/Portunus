# drivers/access_point_gpio — GPIO door hardware implementing IAccessPoint
#
# Public interface: AccessPointGpio class (IAccessPoint)
# Internal HAL (feature-gated):
#   - door_strike.cpp (GPIO output)
#   - reed_switch.cpp (GPIO input + debounce)
#
# Each HAL source is only compiled when its corresponding Kconfig feature
# is enabled. This keeps builds working across different bench-test
# configurations.

set(_srcs
    "src/access_point_gpio.cpp"
)

if(CONFIG_PORTUNUS_ENABLE_DOOR_STRIKE)
    list(APPEND _srcs "src/door_strike.cpp")
endif()

if(CONFIG_PORTUNUS_ENABLE_REED_SWITCH)
    list(APPEND _srcs "src/reed_switch.cpp")
endif()

idf_component_register(
    SRCS
        ${_srcs}
    INCLUDE_DIRS
        "include"
    REQUIRES
        portunus_interfaces
        portunus_config
        portunus_types
        driver
        esp_timer
        freertos
)