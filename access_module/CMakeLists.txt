# Portunus Access Module — Root CMakeLists.txt
#
# ESP-IDF project layout:
#   components/    Auto-scanned by ESP-IDF (shared types, config, interfaces, proto)
#   drivers/       EXTRA_COMPONENT_DIRS — hardware drivers implementing Portunus interfaces
#   services/      EXTRA_COMPONENT_DIRS — infrastructure services (event bus, WiFi, comms)
#   core/          EXTRA_COMPONENT_DIRS — system FSM orchestrator
#   main/          ESP-IDF special component (app entry / composition root)
#
# Adding a new driver or service: create a directory under the appropriate
# group with a CMakeLists.txt — it will be auto-discovered.  No edits here.

cmake_minimum_required(VERSION 3.16)

set(EXTRA_COMPONENT_DIRS
    ${CMAKE_CURRENT_LIST_DIR}/drivers
    ${CMAKE_CURRENT_LIST_DIR}/services
    ${CMAKE_CURRENT_LIST_DIR}/core
)

include($ENV{IDF_PATH}/tools/cmake/project.cmake)

project(portunus_access_module)
