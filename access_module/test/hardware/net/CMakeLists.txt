# Hardware tier, phase 5b: the full firmware stack on the bench ESP32-S3
# against a throwaway Portunus server that scripts/hw_test.py runs on the
# host (task test:firmware:hardware:net). A separate project from the
# phase 5a app so that one stays free of the networking stack and its CA.
cmake_minimum_required(VERSION 3.16)

set(AM ${CMAKE_CURRENT_LIST_DIR}/../../..)  # access_module root

set(EXTRA_COMPONENT_DIRS
    ${AM}/drivers
    ${AM}/components
    ${AM}/services
    ${AM}/core
)

set(COMPONENTS main)

include($ENV{IDF_PATH}/tools/cmake/project.cmake)
project(portunus_hw_net_tests)
