# On-target hardware tier (chunk 5): Unity on the ESP32-S3 against the real
# drivers. Built and flashed by scripts/hw_test.py (task test:firmware:hardware);
# CI only compiles it. See README.md for the wiring.
cmake_minimum_required(VERSION 3.16)

set(AM ${CMAKE_CURRENT_LIST_DIR}/../..)  # access_module root

set(EXTRA_COMPONENT_DIRS
    ${AM}/drivers
    ${AM}/components
)

# Only what main requires: the networking stack, NVS and the FSMs stay out.
set(COMPONENTS main)

include($ENV{IDF_PATH}/tools/cmake/project.cmake)
project(portunus_hw_tests)
