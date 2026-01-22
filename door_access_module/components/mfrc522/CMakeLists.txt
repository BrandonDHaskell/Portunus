idf_component_register(
  SRCS "src/mfrc522.cpp"
  INCLUDE_DIRS "include"
  REQUIRES driver esp_timer esp_common log esp_rom
)
