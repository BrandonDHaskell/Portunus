/* Component tier, NVS variant: ESP-IDF linux target.
 *
 * Built with -DPORTUNUS_TEST_NVS=ON, which adds portunus_nvs and nvs_flash to
 * the build. On the linux target nvs_flash runs on esp_partition's emulated
 * flash, a temporary file created per run, with the partition table from this
 * build directory, so each test starts from an erased NVS.
 */

#include "unity.h"
#include "portunus_nvs.hpp"
#include "nvs_flash.h"
#include "nvs.h"

#include <stdlib.h>

void setUp(void) {
    TEST_ASSERT_EQUAL(ESP_OK, nvs_flash_erase());
    TEST_ASSERT_EQUAL(ESP_OK, nvs_flash_init());
}

void tearDown(void) {
    nvs_flash_deinit();
}

/* The variant builds and NVS initialises on the emulated flash. */
void test_nvs_runs_on_emulated_flash(void) {
    portunus_device_config_t cfg;
    TEST_ASSERT_EQUAL(ESP_ERR_NVS_NOT_FOUND, portunus_nvs_load(&cfg));
}

extern "C" void app_main(void) {
    UNITY_BEGIN();
    RUN_TEST(test_nvs_runs_on_emulated_flash);
    exit(UNITY_END());
}
