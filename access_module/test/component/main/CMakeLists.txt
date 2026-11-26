set(AM ${CMAKE_CURRENT_LIST_DIR}/../../..)  # access_module root

# One test app per module variant: the module type in sdkconfig picks the
# test source. Both FSM components are always required: IDF resolves
# REQUIRES in an early pass that runs before a fresh build has an sdkconfig,
# so a Kconfig-conditional REQUIRES would pick the wrong one on first
# configure. provisioning_fsm compiles no sources outside the PEU variant.
if(CONFIG_PORTUNUS_MODULE_TYPE_PROVISIONING_CONSOLE)
    set(_test_src "test_peu_main.cpp")
else()
    set(_test_src "test_main.cpp")
endif()

idf_component_register(
    SRCS ${_test_src}
    INCLUDE_DIRS
        "."
        "${CMAKE_CURRENT_LIST_DIR}/../support"
        "${AM}/components/portunus_interfaces/include"
        "${AM}/components/portunus_types/include"
    REQUIRES
        unity
        system_fsm
        provisioning_fsm
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
