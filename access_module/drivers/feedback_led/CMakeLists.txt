# drivers/feedback_led — Single GPIO LED implementing IFeedback
#
# Public interface: FeedbackLed class (IFeedback)
# Internal HAL: led_hal.cpp (GPIO on/off/toggle)
#
# Owns a FreeRTOS pattern task for non-blocking LED blink sequences.

idf_component_register(
    SRCS
        "src/feedback_led.cpp"
        "src/led_hal.cpp"
    INCLUDE_DIRS
        "include"
    REQUIRES
        portunus_interfaces
        portunus_config
        portunus_types
        driver
        freertos
)
