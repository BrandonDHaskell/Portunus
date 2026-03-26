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

# ── Transport-dependent requirements ────────────────────────────────────────
# When gRPC is enabled, we depend on grpc_client (which brings in nghttp2 +
# esp-tls).  When disabled, we depend on esp_http_client for HTTP/1.1.
set(_transport_deps "")
if(CONFIG_PORTUNUS_USE_GRPC)
    list(APPEND _transport_deps grpc_client)
else()
    list(APPEND _transport_deps esp_http_client)
endif()

idf_component_register(
    SRCS
        "src/server_comm.cpp"
    INCLUDE_DIRS
        "include"
    REQUIRES
        freertos
        mbedtls
        esp_wifi
        esp_netif
        esp_timer
        event_bus
        wifi_mgr
        portunus_proto
        portunus_types
        portunus_config
        ${_transport_deps}
)

# ── Embed custom CA certificate for LAN TLS pinning ─────────────────────────
# When CONFIG_PORTUNUS_TLS_USE_CUSTOM_CA is enabled, the CA cert PEM at
# access_module/certs/ca_cert.pem is compiled into the firmware as a
# null-terminated string.  server_comm.cpp references it via:
#   extern const char ca_cert_pem_start[] asm("_binary_ca_cert_pem_start");
#   extern const char ca_cert_pem_end[]   asm("_binary_ca_cert_pem_end");
#
# Generate the cert with:  ./scripts/generate_certs.sh --ip <SERVER_IP>
set(CA_CERT_PATH "${CMAKE_SOURCE_DIR}/certs/ca_cert.pem")
if(CONFIG_PORTUNUS_TLS_USE_CUSTOM_CA)
    if(NOT EXISTS "${CA_CERT_PATH}")
        message(FATAL_ERROR
            "Custom CA cert not found at ${CA_CERT_PATH}\n"
            "Run ./scripts/generate_certs.sh --ip <SERVER_IP> to generate it,\n"
            "or disable CONFIG_PORTUNUS_TLS_USE_CUSTOM_CA in menuconfig."
        )
    endif()
    target_add_binary_data(${COMPONENT_LIB} "${CA_CERT_PATH}" TEXT)
endif()