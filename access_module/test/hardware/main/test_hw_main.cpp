/* Hardware tier (chunk 5): Unity on the ESP32-S3 against the real drivers.
 *
 * Two builds: A (active-high strike, normally-open reed) and B (active-low
 * strike, normally-closed reed, sdkconfig.defaults.active_low). Loopback
 * jumpers let the board measure itself:
 *   strike pin (CONFIG_PORTUNUS_DOOR_STRIKE_PIN) -> sense input (HW_SENSE_PIN)
 *   reed drive (HW_REED_DRIVE_PIN, open-drain)   -> reed pin (REED_SWITCH_PIN)
 * The MFRC522 is on its production SPI pins and reset pin.
 *
 * Protocol with scripts/hw_test.py: the app prints HW_TESTS_READY every
 * 500 ms until the runner sends a byte, so no output is lost to the reset
 * after flashing; it prints HW_TESTS_DONE after Unity's summary.
 *
 * Never prints a card UID (only its length), and never touches NVS.
 */

#include "unity.h"

#include "access_point_gpio.hpp"
#include "door_strike_levels.hpp"
#include "reader_mfrc522.hpp"
#include "reader_health.hpp"
#include "pin_config.hpp"
#include "timing_config.hpp"
#include "error_codes.hpp"

#include "driver/gpio.h"
#include "driver/pulse_cnt.h"
#include "driver/usb_serial_jtag.h"
#include "driver/usb_serial_jtag_vfs.h"
#include "esp_heap_caps.h"
#include "esp_log.h"
#include "esp_rom_sys.h"
#include "esp_timer.h"
#include "freertos/FreeRTOS.h"
#include "freertos/task.h"

#include <stdio.h>
#include <unistd.h>

static constexpr gpio_num_t STRIKE     = static_cast<gpio_num_t>(PIN_DOOR_STRIKE);
static constexpr gpio_num_t SENSE      = static_cast<gpio_num_t>(CONFIG_PORTUNUS_HW_SENSE_PIN);
static constexpr gpio_num_t REED       = static_cast<gpio_num_t>(PIN_REED_SWITCH);
static constexpr gpio_num_t REED_DRIVE = static_cast<gpio_num_t>(CONFIG_PORTUNUS_HW_REED_DRIVE_PIN);
static constexpr gpio_num_t READER_RST = static_cast<gpio_num_t>(PIN_MFRC522_RST);

static constexpr int ACTIVE   = strike_level(DOOR_STRIKE_ACTIVE_LOW, true);
static constexpr int INACTIVE = strike_level(DOOR_STRIKE_ACTIVE_LOW, false);

static AccessPointGpio     s_access;
static ReaderMfrc522       s_reader;
static pcnt_unit_handle_t  s_edges = nullptr;   /* counts both edges on SENSE */

/* ── Helpers ──────────────────────────────────────────────────────────────── */

/* At least one tick (10 ms at the default 100 Hz): pdMS_TO_TICKS() of less
 * rounds to 0, and vTaskDelay(0) does not wait at all. */
static void sleep_ms(int ms)
{
    const TickType_t ticks = pdMS_TO_TICKS(ms);
    vTaskDelay(ticks > 0 ? ticks : 1);
}

static int edge_count(void)
{
    int n = 0;
    TEST_ASSERT_EQUAL(ESP_OK, pcnt_unit_get_count(s_edges, &n));
    return n;
}

/* Count every edge on the sense pin in hardware, glitch filter off, so a
 * pulse too short for an interrupt to see is still two edges. */
static void start_edge_counter(void)
{
    pcnt_unit_config_t unit_cfg = {};
    unit_cfg.low_limit  = -1000;
    unit_cfg.high_limit = 1000;
    ESP_ERROR_CHECK(pcnt_new_unit(&unit_cfg, &s_edges));

    pcnt_chan_config_t chan_cfg = {};
    chan_cfg.edge_gpio_num  = SENSE;
    chan_cfg.level_gpio_num = -1;
    pcnt_channel_handle_t chan = nullptr;
    ESP_ERROR_CHECK(pcnt_new_channel(s_edges, &chan_cfg, &chan));
    ESP_ERROR_CHECK(pcnt_channel_set_edge_action(chan, PCNT_CHANNEL_EDGE_ACTION_INCREASE,
                                                 PCNT_CHANNEL_EDGE_ACTION_INCREASE));
    /* Hold the sense net at the strike's inactive level until the driver
     * drives it, so an undriven strike pin reads as "locked", not noise. */
    gpio_set_pull_mode(SENSE, INACTIVE ? GPIO_PULLUP_ONLY : GPIO_PULLDOWN_ONLY);

    ESP_ERROR_CHECK(pcnt_unit_enable(s_edges));
    ESP_ERROR_CHECK(pcnt_unit_clear_count(s_edges));
    ESP_ERROR_CHECK(pcnt_unit_start(s_edges));
}

/* Release (true) or pull low (false) the reed pin through the open-drain
 * jumper. */
static void reed_drive(bool release)
{
    gpio_set_level(REED_DRIVE, release ? 1 : 0);
}

/* Debounced door state after polling every millisecond for `ms` of real
 * time (esp_timer, the clock the debouncer uses). Busy-waits: tick sleeps
 * are too coarse for a window of a few tens of ms. */
static bool door_open_after(int ms)
{
    const int64_t end = esp_timer_get_time() + (int64_t)ms * 1000;
    bool open = s_access.is_open();
    while (esp_timer_get_time() < end) {
        esp_rom_delay_us(1000);
        open = s_access.is_open();
    }
    return open;
}

/* Unity prints through a ROM routine that drops a character if the USB
 * FIFO is busy; drain the logs (fsync waits until the host has read the
 * FIFO) so Unity's result lines are not lost behind them. */
static void drain_console(void)
{
    fflush(stdout);
    fsync(fileno(stdout));
}

void setUp(void) { drain_console(); }

/* Never leave the relay energised after a test, whatever it asserted. */
void tearDown(void)
{
    (void)s_access.lock();
    reed_drive(true);
    drain_console();
}

/* ── HW-01 / HW-02: strike, at the pin ────────────────────────────────────── */

/* HW-01/02 (F1): init latches the inactive level before enabling the
 * output, so the strike never pulses on at boot. The net starts at the
 * inactive level (sense pull), so a pulse would be two edges; one edge at
 * most is allowed for an external stage that pulls the other way. */
void test_strike_init_has_no_glitch(void)
{
    const int before = edge_count();
    TEST_ASSERT_EQUAL(PORTUNUS_OK, s_access.init());
    sleep_ms(10);
    TEST_ASSERT_LESS_OR_EQUAL_INT_MESSAGE(1, edge_count() - before,
                                          "strike pin pulsed during init");
    TEST_ASSERT_EQUAL_INT_MESSAGE(INACTIVE, gpio_get_level(SENSE),
                                  "strike not at its locked level after init "
                                  "(is the strike-to-sense jumper fitted?)");
}

/* HW-01/02 (F1): unlock and lock return OK and the level at the sense pad
 * (a separate input, so a wiring fault shows) follows, once per call. On
 * active-low wiring every lock() used to fail its readback. */
void test_strike_unlock_lock_at_pin(void)
{
    for (int i = 0; i < 10; ++i) {
        const int edges = edge_count();
        TEST_ASSERT_EQUAL(PORTUNUS_OK, s_access.unlock());
        sleep_ms(20);
        TEST_ASSERT_EQUAL_INT(ACTIVE, gpio_get_level(SENSE));
        TEST_ASSERT_EQUAL(PORTUNUS_OK, s_access.lock());
        sleep_ms(20);
        TEST_ASSERT_EQUAL_INT(INACTIVE, gpio_get_level(SENSE));
        TEST_ASSERT_EQUAL_INT(2, edge_count() - edges);   /* one clean on, one clean off */
    }
}

/* ── HW-03: reed switch through the drive jumper ──────────────────────────── */

/* The real reed switch shares the pin: with the drive released the pull-up
 * must win, or the switch is closed (magnet near) and the test cannot
 * drive the open level. */
static void require_reed_released(void)
{
    reed_drive(true);
    sleep_ms(5);
    TEST_ASSERT_EQUAL_INT_MESSAGE(1, gpio_get_level(REED),
                                  "reed pin low with the drive released: move the magnet "
                                  "away from the reed switch (or check the jumper)");
}

/* HW-03: debounced transitions for this build's wiring (NO in build A, NC
 * in build B): pin low means closed for NO, open for NC. */
void test_reed_debounced_transitions(void)
{
    require_reed_released();
    const bool open_when_high = !REED_SWITCH_NC;   /* NO: high = switch open = door open */

    for (int i = 0; i < 3; ++i) {
        reed_drive(false);
        TEST_ASSERT_EQUAL(!open_when_high, door_open_after(REED_SWITCH_DEBOUNCE_MS * 3));
        reed_drive(true);
        TEST_ASSERT_EQUAL(open_when_high, door_open_after(REED_SWITCH_DEBOUNCE_MS * 3));
    }
}

/* HW-03: a bounce shorter than the debounce window does not change the
 * debounced state. */
void test_reed_ignores_short_bounce(void)
{
    require_reed_released();
    const bool settled = door_open_after(REED_SWITCH_DEBOUNCE_MS * 3);

    for (int i = 0; i < 5; ++i) {
        reed_drive(false);
        TEST_ASSERT_EQUAL(settled, door_open_after(REED_SWITCH_DEBOUNCE_MS / 4));
        reed_drive(true);
        TEST_ASSERT_EQUAL(settled, door_open_after(REED_SWITCH_DEBOUNCE_MS / 4));
    }
}

/* ── HW-04 / HW-05: MFRC522 ───────────────────────────────────────────────── */

/* HW-04: the reader answers on SPI (VersionReg) and init succeeds. */
void test_reader_detected(void)
{
    TEST_ASSERT_EQUAL_MESSAGE(PORTUNUS_OK, s_reader.init(),
                              "MFRC522 not detected: check SPI wiring and power");
}

/* HW-05 (D-T7): holding the reset pin low (hard power-down) stands in for
 * an unplugged reader. Every read must be what the FSM's reader_classify()
 * counts as a hardware error (not "no card", not a card error), so five of
 * them degrade the reader. init() brings it back. */
void test_reader_power_down_and_recover(void)
{
    TEST_ASSERT_EQUAL(PORTUNUS_OK, s_reader.init());
    gpio_set_level(READER_RST, 0);
    sleep_ms(10);

    credential_t cred;
    for (int poll = 0; poll < 5; ++poll) {
        const portunus_err_t err = s_reader.read(&cred);
        printf("powered-down read %d: 0x%04x\n", poll, (unsigned)err);
        TEST_ASSERT_TRUE_MESSAGE(reader_classify(err) == reader_read_result_t::HW_ERROR,
                                 "a powered-down reader does not read as a hardware fault "
                                 "(is the MFRC522 RST wired to the reset GPIO?)");
    }

    TEST_ASSERT_EQUAL(PORTUNUS_OK, s_reader.init());     /* releases reset */
    const portunus_err_t err = s_reader.read(&cred);
    TEST_ASSERT_TRUE_MESSAGE(err == PORTUNUS_OK || err == PORTUNUS_ERR_NO_CREDENTIAL,
                             "reader did not recover after init");
}

/* HW-04: a card resting on the reader is read (4 or 7 UID bytes) and, once
 * halted, not read again while it stays there. Ignored if no card rests on
 * the reader. */
void test_resting_card_read_once(void)
{
    TEST_ASSERT_EQUAL(PORTUNUS_OK, s_reader.init());
    credential_t cred = {};
    bool         got  = false;
    for (int i = 0; i < 20 && !got; ++i) {
        got = s_reader.read(&cred) == PORTUNUS_OK;
        if (!got) sleep_ms(50);
    }
    if (!got) {
        TEST_IGNORE_MESSAGE("no card resting on the reader");
    }
    printf("resting card: %u-byte UID\n", (unsigned)cred.uid_len);
    TEST_ASSERT_TRUE(cred.uid_len == 4 || cred.uid_len == 7);

    s_reader.halt();
    for (int i = 0; i < 20; ++i) {
        TEST_ASSERT_EQUAL_MESSAGE(PORTUNUS_ERR_NO_CREDENTIAL, s_reader.read(&cred),
                                  "halted card read again while still on the reader");
        sleep_ms(50);
    }
}

/* ── HW-10: heap ──────────────────────────────────────────────────────────── */

/* HW-10: free heap is flat across many reader polls and strike cycles. */
void test_heap_flat_across_cycles(void)
{
    TEST_ASSERT_EQUAL(PORTUNUS_OK, s_reader.init());
    esp_log_level_set("door_strike", ESP_LOG_WARN);   /* 2 lines per cycle otherwise */

    credential_t cred;
    for (int i = 0; i < 20; ++i) {                    /* warm-up: lazy allocations */
        (void)s_reader.read(&cred);
    }
    const size_t before = heap_caps_get_free_size(MALLOC_CAP_DEFAULT);

    for (int i = 0; i < 1000; ++i) {
        (void)s_reader.read(&cred);
        if (i % 50 == 0) {
            vTaskDelay(1);                            /* let IDLE0 feed the task watchdog */
        }
        if (i % 20 == 0) {                            /* 50 strike cycles: spare the relay */
            TEST_ASSERT_EQUAL(PORTUNUS_OK, s_access.unlock());
            TEST_ASSERT_EQUAL(PORTUNUS_OK, s_access.lock());
        }
    }
    const size_t after = heap_caps_get_free_size(MALLOC_CAP_DEFAULT);
    esp_log_level_set("door_strike", ESP_LOG_INFO);
    printf("free heap: before=%u after=%u\n", (unsigned)before, (unsigned)after);
    TEST_ASSERT_GREATER_OR_EQUAL_UINT32(before, after);
}

/* ── Entry ────────────────────────────────────────────────────────────────── */

static void configure_reed_drive(void)
{
    gpio_set_level(REED_DRIVE, 1);                    /* released before output is enabled */
    gpio_config_t cfg = {};
    cfg.pin_bit_mask = 1ULL << REED_DRIVE;
    cfg.mode         = GPIO_MODE_OUTPUT_OD;
    ESP_ERROR_CHECK(gpio_config(&cfg));
}

/* Wait for the runner's go byte, announcing readiness until it comes. The
 * USB serial driver is needed only to read it: Unity writes straight to the
 * console hardware (esp_system_console_put_char), and with the driver still
 * installed the two fight over the FIFO and Unity's lines are lost. So the
 * console goes back to direct mode before the tests run. */
static void wait_for_runner(void)
{
    usb_serial_jtag_driver_config_t cfg = USB_SERIAL_JTAG_DRIVER_CONFIG_DEFAULT();
    ESP_ERROR_CHECK(usb_serial_jtag_driver_install(&cfg));
    usb_serial_jtag_vfs_use_driver();
    uint8_t byte;
    do {
        printf("HW_TESTS_READY strike=active-%s reed=%s\n",
               DOOR_STRIKE_ACTIVE_LOW ? "low" : "high", REED_SWITCH_NC ? "NC" : "NO");
        fflush(stdout);
    } while (usb_serial_jtag_read_bytes(&byte, 1, pdMS_TO_TICKS(500)) != 1);

    fflush(stdout);
    usb_serial_jtag_vfs_use_nonblocking();
    ESP_ERROR_CHECK(usb_serial_jtag_driver_uninstall());
    drain_console();
}

extern "C" void app_main(void)
{
    /* Before anything drives the strike pin, so init's edges are counted. */
    start_edge_counter();
    configure_reed_drive();
    wait_for_runner();

    /* Unity's ROM output path loses its first few bytes after the console
     * switch: spend them on a throwaway line, not the first result. */
    UnityPrint("\n\n");
    UNITY_BEGIN();
    RUN_TEST(test_strike_init_has_no_glitch);
    RUN_TEST(test_strike_unlock_lock_at_pin);
    RUN_TEST(test_reed_debounced_transitions);
    RUN_TEST(test_reed_ignores_short_bounce);
    RUN_TEST(test_reader_detected);
    RUN_TEST(test_reader_power_down_and_recover);
    RUN_TEST(test_resting_card_read_once);
    RUN_TEST(test_heap_flat_across_cycles);
    UNITY_END();

    printf("HW_TESTS_DONE\n");
    fflush(stdout);
    for (;;) {
        sleep_ms(1000);
    }
}
