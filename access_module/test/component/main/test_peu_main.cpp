/* Component tier, provisioning console (PEU) variant: ESP-IDF linux target.
 *
 * Built from the same app with sdkconfig.defaults.peu layered on, which
 * selects CONFIG_PORTUNUS_MODULE_TYPE_PROVISIONING_CONSOLE; main/CMakeLists.txt
 * then compiles this file against provisioning_fsm instead of test_main.cpp.
 * Always uses the fake event bus.
 */

#include "unity.h"
#include "provisioning_fsm.hpp"
#include "event_bus.hpp"
#include "error_codes.hpp"

#include "../support/fake_clock.hpp"
#include "../support/fake_credential_reader.hpp"
#include "../support/fake_feedback.hpp"
#include "../components/event_bus/include/event_bus_fake.hpp"

#include <stdlib.h>

void setUp(void) {
    event_bus_fake_reset();
    event_bus_init();
}

void tearDown(void) {}

/* The PEU build links and the FSM initialises with a working reader. */
void test_peu_init_with_reader(void) {
    FakeCredentialReader reader;
    FakeFeedback         feedback;
    FakeClock            clock;
    ProvisioningFSM      fsm(&reader, &feedback, nullptr, &clock);
    TEST_ASSERT_EQUAL(PORTUNUS_OK, fsm.init());
}

extern "C" void app_main(void) {
    UNITY_BEGIN();
    RUN_TEST(test_peu_init_with_reader);
    int failures = UNITY_END();
    exit(failures);
}
