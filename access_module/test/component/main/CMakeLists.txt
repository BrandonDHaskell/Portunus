set(AM ${CMAKE_CURRENT_LIST_DIR}/../../..)  # access_module root

# One test app per variant: the module type (or the heartbeat test switch)
# in sdkconfig picks the test source. Every component under test is always
# required: IDF resolves REQUIRES in an early pass that runs before a fresh
# build has an sdkconfig, so a Kconfig-conditional REQUIRES would pick the
# wrong one on first configure. provisioning_fsm compiles no sources outside
# the PEU variant, and the other variants never reference heartbeat_service,
# so its esp_timer call (no linux implementation) is not linked there.
if(PORTUNUS_TEST_SERVER_COMM)
    set(_test_src "test_server_comm_main.cpp")
elseif(PORTUNUS_TEST_NVS)
    set(_test_src "test_nvs_main.cpp")
elseif(CONFIG_PORTUNUS_TEST_HEARTBEAT)
    set(_test_src "test_heartbeat_main.cpp")
elseif(CONFIG_PORTUNUS_TEST_WIFI_RECONNECT)
    set(_test_src "test_wifi_reconnect_main.cpp")
elseif(CONFIG_PORTUNUS_MODULE_TYPE_PROVISIONING_CONSOLE)
    set(_test_src "test_peu_main.cpp")
else()
    set(_test_src "test_main.cpp")
endif()

idf_component_register(
    SRCS ${_test_src}
    INCLUDE_DIRS
        "."
        "${CMAKE_CURRENT_LIST_DIR}/../support"
        "${AM}/components/portunus_interfaces/include"
        "${AM}/components/portunus_types/include"
    REQUIRES
        unity
        system_fsm
        provisioning_fsm
        heartbeat_service
        wifi_reconnect
        linux_time_shim
        wifi_mgr            # the stub, for wifi_mgr_fake_set_connected
        event_bus
        portunus_types
        portunus_config
        portunus_interfaces
)

# PORTUNUS_TEST_REAL_BUS selects the event_bus sources (components/event_bus)
# but test_main.cpp also branches on it in the preprocessor. A CMake option is
# not a compiler definition, so forward it explicitly; without this the
# real-bus variant compiles the fake-bus test paths and fails to link.
if(PORTUNUS_TEST_REAL_BUS)
    target_compile_definitions(${COMPONENT_LIB} PRIVATE PORTUNUS_TEST_REAL_BUS=1)
endif()

# The NVS variant links portunus_nvs after registration instead of through
# REQUIRES: the early REQUIRES pass sees neither sdkconfig nor CMake cache
# options, and an unconditional REQUIRES would compile nvs_flash and
# esp_partition into every variant. The top-level CMakeLists.txt adds them to
# COMPONENTS for this build only.
if(PORTUNUS_TEST_NVS)
    target_link_libraries(${COMPONENT_LIB} PRIVATE idf::portunus_nvs idf::nvs_flash)
endif()

# The server_comm variants link the real server_comm, the fakes it talks to,
# and Nanopb (to encode scripted responses) the same way: after
# registration, added to COMPONENTS by the top-level CMakeLists.txt.
if(PORTUNUS_TEST_SERVER_COMM)
    target_link_libraries(${COMPONENT_LIB} PRIVATE
        idf::server_comm idf::grpc_client idf::wifi_mgr idf::portunus_proto idf::mbedtls
        idf::portunus_config)
    target_compile_definitions(${COMPONENT_LIB} PRIVATE
        PORTUNUS_HMAC_VECTORS="${AM}/../proto/testdata/hmac_vectors.txt"
        PORTUNUS_PROTO_FILE="${AM}/../proto/portunus/v1/portunus.proto")
endif()
