cmake_minimum_required(VERSION 3.16)
project(portunus_access_module_host_tests C CXX)

set(CMAKE_C_STANDARD 17)
set(CMAKE_C_STANDARD_REQUIRED ON)
set(CMAKE_CXX_STANDARD 23)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
add_compile_options(-Wall -Wextra)
enable_testing()

include(FetchContent)
FetchContent_Declare(unity
    GIT_REPOSITORY https://github.com/ThrowTheSwitch/Unity.git
    GIT_TAG        v2.6.0)
FetchContent_MakeAvailable(unity)

# access_module root, two levels up from test/unit
set(AM ${CMAKE_CURRENT_LIST_DIR}/../..)

add_executable(test_credential_types
    test_credential_types.c
    ${AM}/components/portunus_types/src/credential_types.c)
target_include_directories(test_credential_types PRIVATE
    ${AM}/components/portunus_types/include)
target_link_libraries(test_credential_types PRIVATE unity)
add_test(NAME credential_types COMMAND test_credential_types)

add_executable(test_system_fsm_decide
    test_system_fsm_decide.cpp
    ${AM}/core/system_fsm/src/system_fsm_decide.cpp)
target_include_directories(test_system_fsm_decide PRIVATE
    ${AM}/core/system_fsm/include
    ${AM}/components/portunus_types/include
    ${AM}/components/portunus_interfaces/include)
target_link_libraries(test_system_fsm_decide PRIVATE unity)
add_test(NAME system_fsm_decide COMMAND test_system_fsm_decide)

add_executable(test_grpc_wire
    test_grpc_wire.cpp
    ${AM}/services/grpc_client/src/grpc_wire.cpp)
target_include_directories(test_grpc_wire PRIVATE
    ${AM}/services/grpc_client/include)
target_link_libraries(test_grpc_wire PRIVATE unity)
add_test(NAME grpc_wire COMMAND test_grpc_wire)

add_executable(test_reader_health
    test_reader_health.cpp
    ${AM}/core/system_fsm/src/reader_health.cpp)
target_include_directories(test_reader_health PRIVATE
    ${AM}/core/system_fsm/include)
target_link_libraries(test_reader_health PRIVATE unity)
add_test(NAME reader_health COMMAND test_reader_health)

add_executable(test_wifi_backoff
    test_wifi_backoff.cpp
    ${AM}/services/wifi_mgr/src/wifi_backoff.cpp)
target_include_directories(test_wifi_backoff PRIVATE
    ${AM}/services/wifi_mgr/include)
target_link_libraries(test_wifi_backoff PRIVATE unity)
add_test(NAME wifi_backoff COMMAND test_wifi_backoff)

add_executable(test_door_strike_levels test_door_strike_levels.cpp)
target_include_directories(test_door_strike_levels PRIVATE
    ${AM}/drivers/access_point_gpio/include)
target_link_libraries(test_door_strike_levels PRIVATE unity)
add_test(NAME door_strike_levels COMMAND test_door_strike_levels)

add_executable(test_led_pattern
    test_led_pattern.cpp
    ${AM}/drivers/feedback_led/src/led_pattern.cpp)
target_include_directories(test_led_pattern PRIVATE
    ${AM}/drivers/feedback_led/include
    ${AM}/components/portunus_interfaces/include
    ${AM}/components/portunus_types/include)
target_link_libraries(test_led_pattern PRIVATE unity)
add_test(NAME led_pattern COMMAND test_led_pattern)
