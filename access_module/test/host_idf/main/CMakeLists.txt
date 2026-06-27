set(AM ${CMAKE_CURRENT_LIST_DIR}/../../..)  # access_module root

idf_component_register(
    SRCS "test_main.cpp"
    INCLUDE_DIRS
        "."
        "${CMAKE_CURRENT_LIST_DIR}/../support"
        "${AM}/components/portunus_interfaces/include"
        "${AM}/components/portunus_types/include"
        "${AM}/core/system_fsm/include"
    REQUIRES
        unity
        system_fsm
        event_bus
        portunus_types
        portunus_config
        portunus_interfaces
)
