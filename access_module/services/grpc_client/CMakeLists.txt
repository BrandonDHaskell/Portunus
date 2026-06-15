# services/grpc_client — Lightweight gRPC client over HTTP/2 for ESP-IDF
#
# Implements unary gRPC calls using:
#   - nghttp2 (espressif/nghttp IDF component) for HTTP/2 framing
#   - esp-tls for the TLS transport with ALPN "h2"
#   - Manual gRPC wire format (5-byte length-prefixed protobuf)

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
