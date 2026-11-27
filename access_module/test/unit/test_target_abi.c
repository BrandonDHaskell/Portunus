/* Unit tier: the host-m32 preset must keep modelling the ESP32-S3's data
 * types, or the 32-bit run stops testing what the device actually runs.
 * Measured on xtensa-esp32s3-elf-gcc (ESP-IDF 5.5.1): 4-byte pointers,
 * size_t and long; 8-byte time_t; unsigned char. On the 64-bit presets this
 * only records the host's model. */
#include "unity.h"

#include <limits.h>
#include <stddef.h>
#include <stdint.h>
#include <time.h>

void setUp(void) {}
void tearDown(void) {}

void test_data_model(void) {
#ifdef PORTUNUS_ESP32_DATA_MODEL
    TEST_ASSERT_EQUAL_size_t(4, sizeof(void *));
    TEST_ASSERT_EQUAL_size_t(4, sizeof(size_t));
    TEST_ASSERT_EQUAL_size_t(4, sizeof(long));
    TEST_ASSERT_EQUAL_size_t(8, sizeof(time_t));
    TEST_ASSERT_EQUAL_INT(0, CHAR_MIN);           /* char is unsigned */
#else
    TEST_ASSERT_TRUE(sizeof(time_t) >= 4);
    TEST_IGNORE_MESSAGE("not the ESP32 data-model preset (host-m32)");
#endif
}

int main(void) {
    UNITY_BEGIN();
    RUN_TEST(test_data_model);
    return UNITY_END();
}
