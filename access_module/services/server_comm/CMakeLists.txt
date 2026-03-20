# services/server_comm — HTTP + protobuf bridge between event bus and server
#
# Subscribes to heartbeat and credential events, encodes protobuf via
# nanopb, POSTs to the Portunus server, decodes the response, and
# publishes access decision events back to the event bus.
#
# Depends on: event_bus (subscribe/publish), wifi_mgr (connection check),
# portunus_proto (nanopb message portunus_types), esp_http_client (HTTP I/O),
# mbedtls (provides esp_crt_bundle.h and the HMAC-SHA256 mbedtls/md.h API),
# esp_wifi and esp_netif (for RSSI and IP in heartbeat requests), plus common
# portunus_types and portunus_config for error codes and network parameters.
#
# ── Component naming rules learned the hard way ─────────────────────────────
#
# esp_crt_bundle — NOT a standalone component. The header <esp_crt_bundle.h>
#   and esp_crt_bundle_attach() live inside the "mbedtls" component at
#   components/mbedtls/esp_crt_bundle/include/. Adding "mbedtls" to REQUIRES
#   is all that is needed. Do not add "esp_crt_bundle".
#
# esp_tls — The IDF component directory is "esp-tls" (hyphen), so the CMake
#   name is "esp-tls", not "esp_tls". However, esp_http_client already lists
#   "esp-tls" in its own REQUIRES, making it a transitive dependency of this
#   component. There is no need to declare it here at all.
#
# The Kconfig symbol CONFIG_MBEDTLS_CERTIFICATE_BUNDLE must be =y (default in
# IDF 5.x) for esp_crt_bundle_attach to link. This is enforced in
# sdkconfig.defaults so the build fails fast if someone disables it.

idf_component_register(
    SRCS
        "src/server_comm.cpp"
    INCLUDE_DIRS
        "include"
    REQUIRES
        freertos
        esp_http_client
        mbedtls
        esp_wifi
        esp_netif
        esp_timer
        event_bus
        wifi_mgr
        portunus_proto
        portunus_types
        portunus_config
)