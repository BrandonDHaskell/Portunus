idf_component_register(
  SRCS "src/rfid_reader.cpp"
  INCLUDE_DIRS "include"
  REQUIRES freertos mfrc522 esp_timer
)