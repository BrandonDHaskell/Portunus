# services/server_comm — HTTP + protobuf bridge between event bus and server
#
# Subscribes to heartbeat and credential events, encodes protobuf via
# nanopb, POSTs to the Portunus server, decodes the response, and
# publishes access decision events back to the event bus.
#
# Depends on: event_bus (subscribe/publish), wifi_mgr (connection check),
# proto (nanopb message types), esp_http_client (HTTP I/O), esp_wifi
# and esp_netif (for RSSI and IP in heartbeat requests), plus common
# types and config for error codes and network parameters.

idf_component_register(
    SRCS
        "src/server_comm.cpp"
    INCLUDE_DIRS
        "include"
    REQUIRES
        freertos
        esp_http_client
        esp_wifi
        esp_netif
        esp_timer
        event_bus
        wifi_mgr
        proto
        types
        config
)