# Fake grpc_client for the component tier's server_comm variants: shadows
# services/grpc_client by component name. Same public header (and the real,
# pure grpc_wire), scripted replies instead of HTTP/2+TLS.
set(AM ${CMAKE_CURRENT_LIST_DIR}/../../../..)  # access_module root

idf_component_register(
    SRCS "grpc_client_fake.cpp" "${AM}/services/grpc_client/src/grpc_wire.cpp"
    INCLUDE_DIRS "${AM}/services/grpc_client/include" "include"
    REQUIRES portunus_types freertos
)
