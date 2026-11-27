# Stub wifi_mgr for the component-tier test build.
#
# The test builds keep CONFIG_PORTUNUS_ENABLE_WIFI at its production default
# (the module-type choice depends on TLS, which depends on WiFi), so the FSMs
# call wifi_mgr_is_connected(). This stub satisfies that call (always offline;
# tests set the network capability through their fixtures) and the FSMs'
# PRIV_REQUIRES wifi_mgr, without the real wifi_mgr's networking stack.
# portunus_nvs's include directory is added for wifi_mgr.hpp, without
# requiring the component (that would build nvs_flash into every variant).

set(AM ${CMAKE_CURRENT_LIST_DIR}/../../../..)  # access_module root

idf_component_register(
    SRCS "wifi_mgr_stub.cpp"
    INCLUDE_DIRS
        "${AM}/services/wifi_mgr/include"
        "${AM}/components/portunus_nvs/include"
        "include"
    REQUIRES portunus_types
)
