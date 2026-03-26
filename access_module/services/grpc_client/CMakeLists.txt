# services/grpc_client — Lightweight gRPC client over HTTP/2 for ESP-IDF
#
# Implements unary gRPC calls using:
#   - nghttp2 (espressif/nghttp IDF component) for HTTP/2 framing
#   - esp-tls for the TLS transport with ALPN "h2"
#   - Manual gRPC wire format (5-byte length-prefixed protobuf)
#
# This component is only built when CONFIG_PORTUNUS_USE_GRPC is enabled.
# When disabled, server_comm falls back to the HTTP/1.1 + esp_http_client path.
#
# ── nghttp2 dependency ──────────────────────────────────────────────────────
# The espressif/nghttp component is pulled in via idf_component.yml
# (IDF component manager).  It provides the <nghttp2/nghttp2.h> header
# and the libnghttp2 library.  No manual download is needed.

if(CONFIG_PORTUNUS_USE_GRPC)

    idf_component_register(
        SRCS
            "src/grpc_client.cpp"
        INCLUDE_DIRS
            "include"
        REQUIRES
            freertos
            esp-tls
            mbedtls
            esp_timer
            portunus_types
    )

else()

    # Register as an empty component so CMake doesn't break when
    # grpc_client appears in another component's REQUIRES list behind
    # an #if guard.  No sources are compiled.
    idf_component_register(
        INCLUDE_DIRS
            "include"
    )

endif()