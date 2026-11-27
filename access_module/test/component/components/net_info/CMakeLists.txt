# Fake net_info for the component tier: shadows services/net_info (esp_netif
# and esp_wifi do not exist on the IDF linux target).
set(AM ${CMAKE_CURRENT_LIST_DIR}/../../../..)  # access_module root

idf_component_register(
    SRCS "net_info_fake.cpp"
    INCLUDE_DIRS "${AM}/services/net_info/include"
)
