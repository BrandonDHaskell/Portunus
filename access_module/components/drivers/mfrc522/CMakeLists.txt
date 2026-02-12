# drivers/mfrc522 — MFRC522 RFID reader driver
#
# SPI-based driver for the MFRC522 13.56 MHz RFID reader module.
# Reads MIFARE card UIDs via the ESP-IDF SPI master peripheral.
#
# Depends on common/config for pin assignments and common/types for
# shared type definitions. Uses ESP-IDF driver API for SPI and GPIO.

idf_component_register(
    SRCS
        "src/mfrc522.cpp"
    INCLUDE_DIRS
        "include"
    REQUIRES
        driver
        config
        types
)