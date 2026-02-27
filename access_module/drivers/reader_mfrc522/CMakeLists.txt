# drivers/reader_mfrc522 — MFRC522 RFID reader implementing ICredentialReader
#
# Public interface: ReaderMfrc522 class (ICredentialReader)
# Internal HAL: mfrc522_hal.cpp (SPI register-level driver)
#
# The mfrc522.h header is an internal HAL — only this component's
# code calls it.  External code uses ICredentialReader.

idf_component_register(
    SRCS
        "src/reader_mfrc522.cpp"
        "src/mfrc522_hal.cpp"
    INCLUDE_DIRS
        "include"
    REQUIRES
        portunus_interfaces
        portunus_config
        portunus_types
        driver
)
