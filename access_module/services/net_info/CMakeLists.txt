# services/net_info: station network details for heartbeats
#
# The WiFi station's IPv4 address and the signal strength of the connected
# AP. Split out of server_comm so the component tier can replace it with a
# fake: esp_netif and esp_wifi do not exist on the IDF linux target.

idf_component_register(
    SRCS "src/net_info.cpp"
    INCLUDE_DIRS "include"
    PRIV_REQUIRES esp_wifi esp_netif
)
