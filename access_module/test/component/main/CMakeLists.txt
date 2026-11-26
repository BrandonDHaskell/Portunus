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

# PORTUNUS_TEST_REAL_BUS selects the event_bus sources (components/event_bus)
# but test_main.cpp also branches on it in the preprocessor. A CMake option is
# not a compiler definition, so forward it explicitly; without this the
# real-bus variant compiles the fake-bus test paths and fails to link.
if(PORTUNUS_TEST_REAL_BUS)
    target_compile_definitions(${COMPONENT_LIB} PRIVATE PORTUNUS_TEST_REAL_BUS=1)
endif()
