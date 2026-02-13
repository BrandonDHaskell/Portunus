# Portunus Access Module — Root CMakeLists.txt

cmake_minimum_required(VERSION 3.16)

# ── MVP component directories (leaf-only) ────────────────────────────────────
# Each path points to a leaf directory containing idf_component_register().
set(EXTRA_COMPONENT_DIRS
    ${CMAKE_CURRENT_LIST_DIR}/components/common/types
    ${CMAKE_CURRENT_LIST_DIR}/components/common/config
    ${CMAKE_CURRENT_LIST_DIR}/components/drivers/mfrc522
    ${CMAKE_CURRENT_LIST_DIR}/components/services/event_bus
    ${CMAKE_CURRENT_LIST_DIR}/components/services/heartbeat_service
    ${CMAKE_CURRENT_LIST_DIR}/components/services/wifi_mgr
    ${CMAKE_CURRENT_LIST_DIR}/components/proto
)

include($ENV{IDF_PATH}/tools/cmake/project.cmake)

project(portunus_access_module)