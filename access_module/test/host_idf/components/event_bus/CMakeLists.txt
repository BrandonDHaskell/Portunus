# Fake (or real) event_bus for host_idf Tier B tests.
#
# When PORTUNUS_TEST_REAL_BUS=OFF (default): registers the synchronous
# capture-and-dispatch fake. Behavioral tests get deterministic, inline dispatch.
#
# When PORTUNUS_TEST_REAL_BUS=ON: registers the real event_bus implementation
# from services/event_bus. Concurrency tests exercise the real dispatcher task,
# queue-full drop behavior, and snapshot-under-lock paths.
#
# Toggle via: idf.py -DPORTUNUS_TEST_REAL_BUS=ON ...

set(AM ${CMAKE_CURRENT_LIST_DIR}/../../../..)  # access_module root

if(PORTUNUS_TEST_REAL_BUS)
    idf_component_register(
        SRCS "${AM}/services/event_bus/src/event_bus.cpp"
        INCLUDE_DIRS "${AM}/services/event_bus/include"
        REQUIRES portunus_types portunus_config
        PRIV_REQUIRES freertos)
else()
    idf_component_register(
        SRCS "src/event_bus_fake.cpp"
        INCLUDE_DIRS "${AM}/services/event_bus/include" "include"
        REQUIRES portunus_types portunus_config
        PRIV_REQUIRES freertos)
endif()
