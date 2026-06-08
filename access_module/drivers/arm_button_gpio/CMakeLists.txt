# drivers/arm_button_gpio — GPIO momentary button implementing IArm (PEU only)
#
# Compiled only when CONFIG_PORTUNUS_ENABLE_ARM_BUTTON is set; the component
# still registers so its include directory is always available to components
# that require it, even in ACCESS_POINT builds where the class is never used.

if(CONFIG_PORTUNUS_ENABLE_ARM_BUTTON)
    set(_srcs "src/arm_button_gpio.cpp")
else()
    set(_srcs "")
endif()

idf_component_register(
    SRCS
        ${_srcs}
    INCLUDE_DIRS
        "include"
    REQUIRES
        portunus_interfaces
        portunus_types
    PRIV_REQUIRES
        driver
)
