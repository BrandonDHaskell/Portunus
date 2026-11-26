cmake_minimum_required(VERSION 3.16)

# Build flag: set to ON to compile the real event_bus (concurrency tests).
# Default OFF = fake synchronous bus (behavioral tests).
# Usage: idf.py -DPORTUNUS_TEST_REAL_BUS=ON ...
option(PORTUNUS_TEST_REAL_BUS "Use real event_bus for concurrency tests" OFF)

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
)

# Limit the build to exactly what main requires (and their transitive deps).
# Without this IDF builds the full networking/TLS/NVS stack for linux.
set(COMPONENTS main)

include($ENV{IDF_PATH}/tools/cmake/project.cmake)
project(portunus_host_tests)
