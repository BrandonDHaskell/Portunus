# Stub wifi_mgr for the Tier B host test build.
#
# CONFIG_PORTUNUS_ENABLE_WIFI is not set in sdkconfig.defaults, so
# system_fsm.cpp never compiles the wifi_mgr include or API calls.
# This stub exists only so IDF's dependency resolver can satisfy the
# unconditional PRIV_REQUIRES wifi_mgr in the FSM components without
# pulling in the real wifi_mgr's ESP networking dependencies.

set(AM ${CMAKE_CURRENT_LIST_DIR}/../../../..)  # access_module root

idf_component_register(
    SRCS ""
    INCLUDE_DIRS "${AM}/services/wifi_mgr/include"
)
