cmake_minimum_required(VERSION 3.16)
project(portunus_access_module_host_tests C CXX)

set(CMAKE_C_STANDARD 11)
set(CMAKE_C_STANDARD_REQUIRED ON)
set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
add_compile_options(-Wall -Wextra)
enable_testing()

include(FetchContent)
FetchContent_Declare(unity
    GIT_REPOSITORY https://github.com/ThrowTheSwitch/Unity.git
    GIT_TAG        v2.6.0)
FetchContent_MakeAvailable(unity)

# access_module root, two levels up from test/host
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
