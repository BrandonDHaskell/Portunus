# services/grpc_client — Lightweight gRPC client over HTTP/2 for ESP-IDF
#
# Implements unary gRPC calls using:
#   - nghttp2 (espressif/nghttp IDF component) for HTTP/2 framing
#   - esp-tls for the TLS transport with ALPN "h2"
#   - Manual gRPC wire format (5-byte length-prefixed protobuf)
#
# When CONFIG_PORTUNUS_USE_GRPC is disabled, the component registers
# with no sources (header-only).  server_comm's C preprocessor guards
# ensure grpc_client.h is never actually included in that case.

if(CONFIG_PORTUNUS_USE_GRPC)
    set(_grpc_srcs "src/grpc_client.cpp")
else()
    set(_grpc_srcs "")
endif()

idf_component_register(
    SRCS
        ${_grpc_srcs}
    INCLUDE_DIRS
        "include"
    REQUIRES
        freertos
        esp-tls
        mbedtls
        esp_timer
        portunus_types
)