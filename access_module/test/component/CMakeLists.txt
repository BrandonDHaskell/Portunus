cmake_minimum_required(VERSION 3.16)

# Build flag: set to ON to compile the real event_bus (concurrency tests).
# Default OFF = fake synchronous bus (behavioral tests).
# Usage: idf.py -DPORTUNUS_TEST_REAL_BUS=ON ...
option(PORTUNUS_TEST_REAL_BUS "Use real event_bus for concurrency tests" OFF)

# Build flag: set to ON for the NVS variant (portunus_nvs against IDF's
# file-backed flash emulation). A CMake option rather than Kconfig so the
# REQUIRES it switches is known in IDF's early dependency pass, and only this
# build compiles nvs_flash and mbedtls.
option(PORTUNUS_TEST_NVS "Build the portunus_nvs test app" OFF)

# Build flag: ON for the server_comm variants (real server_comm over fake
# grpc_client, wifi_mgr and net_info). A CMake option for the same reason as
# PORTUNUS_TEST_NVS: only these builds compile server_comm, mbedTLS and Nanopb.
option(PORTUNUS_TEST_SERVER_COMM "Build the server_comm test app" OFF)

# Provide a minimal libbsd shim so the IDF linux target builds when libbsd-dev
# is not installed. strlcpy/strlcat are already in glibc on modern Debian via
# _GNU_SOURCE; we only need the <bsd/string.h> and <bsd/sys/cdefs.h> headers.
set(BSD_SHIM ${CMAKE_CURRENT_LIST_DIR}/bsd_shim)
add_compile_options(-I${BSD_SHIM})
set(CMAKE_C_FLAGS "${CMAKE_C_FLAGS} -I${BSD_SHIM}")
set(CMAKE_CXX_FLAGS "${CMAKE_CXX_FLAGS} -I${BSD_SHIM}")

set(AM ${CMAKE_CURRENT_LIST_DIR}/../..)  # access_module root

set(EXTRA_COMPONENT_DIRS
    # Fake (or real) event_bus — shadows services/event_bus via component name
    ${CMAKE_CURRENT_LIST_DIR}/components
    # Production components the FSM depends on
    ${AM}/components
    ${AM}/core
    # Only this service: the whole services/ directory would also register
    # the real event_bus under the fake's component name.
    ${AM}/services/heartbeat_service
    ${AM}/services/server_comm
)

# Limit the build to exactly what main requires (and their transitive deps).
# Without this IDF builds the full networking/TLS/NVS stack for linux.
set(COMPONENTS main)
if(PORTUNUS_TEST_NVS)
    list(APPEND COMPONENTS portunus_nvs nvs_flash)
endif()
if(PORTUNUS_TEST_SERVER_COMM)
    list(APPEND COMPONENTS server_comm)
endif()

include($ENV{IDF_PATH}/tools/cmake/project.cmake)
project(portunus_host_tests)
