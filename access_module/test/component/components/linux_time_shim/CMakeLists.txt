# esp_timer_get_time() for the component tier. ESP-IDF's esp_timer is
# header-only on the linux target; heartbeat_service and server_comm call it.
idf_component_register(SRCS "esp_timer_shim.c" REQUIRES esp_timer)
