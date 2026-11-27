/* Hardware tier, phase 5b: the full firmware stack on the bench ESP32-S3
 * against a throwaway server run by scripts/hw_test.py on the host.
 *
 * The board runs the production composition (wifi_mgr, event bus, drivers,
 * SystemFSM, server_comm, heartbeat). A card tap is injected as an
 * EVENT_CREDENTIAL_READ, exactly what the poll task publishes for a real
 * read; HW-04 covers the RF side. A grant is seen at the strike sense pin
 * (the phase 5a jumper), so the whole path is tested: TLS, HMAC, the
 * server's credential match and signature, the FSM, and the strike.
 *
 * Runner protocol (lines over the built-in USB port):
 *   app:    HW_TESTS_READY ...            (repeated until answered)
 *   runner: CFG <host> <port> <module_id> <hmac_hex>
 *   app:    HW_NEED server=wrong_ca|good   (repeated until answered)
 *   runner: OK
 *   app:    ... Unity output ..., HW_TESTS_DONE
 * The config lives in RAM only. NVS is opened read-only, for the WiFi
 * credentials; they and the HMAC secret are never printed.
 */

#include "unity.h"

#include "system_fsm.hpp"
#include "heartbeat_service.hpp"
#include "server_comm.hpp"
#include "wifi_mgr.hpp"
#include "event_bus.hpp"
#include "event_types.hpp"
#include "access_point_gpio.hpp"
#include "door_strike_levels.hpp"
#include "reader_mfrc522.hpp"
#include "feedback_led.hpp"
#include "esp_timer_clock.hpp"
#include "portunus_nvs.hpp"
#include "pin_config.hpp"
#include "timing_config.hpp"
#include "network_config.hpp"
#include "error_codes.hpp"

#include "driver/gpio.h"
#include "driver/usb_serial_jtag.h"
#include "driver/usb_serial_jtag_vfs.h"
#include "esp_event.h"
#include "esp_log.h"
#include "esp_timer.h"
#include "esp_wifi.h"
#include "freertos/FreeRTOS.h"
#include "freertos/task.h"
#include "nvs_flash.h"

#include <atomic>
#include <stdarg.h>
#include <inttypes.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>

static constexpr gpio_num_t SENSE  = static_cast<gpio_num_t>(CONFIG_PORTUNUS_HW_SENSE_PIN);
static constexpr int        ACTIVE = strike_level(DOOR_STRIKE_ACTIVE_LOW, true);

/* Enrolled by the runner for this module (7-byte UID); the other is never
 * enrolled. Keep in sync with ENROLLED_UID in scripts/hw_test.py. */
static const credential_t ENROLLED = {{0x04, 0x48, 0x57, 0x54, 0x45, 0x53, 0x54}, 7};
static const credential_t UNKNOWN  = {{0xDE, 0xAD, 0xBE, 0xEF}, 4};

static portunus_device_config_t s_cfg = {};
static int64_t                  s_t0_us = 0;          /* start of the composition */
static bool                     s_first_grant_seen = false;

/* ── Console ──────────────────────────────────────────────────────────────── */

/* Unity prints through a ROM routine that drops bytes while the USB FIFO is
 * busy: drain everything else first (see test/hardware). */
static void drain_console(void)
{
    fflush(stdout);
    fsync(fileno(stdout));
}

/* Print `request` every 500 ms until the runner answers with a line. The
 * USB driver is installed only for the exchange, then removed again so
 * Unity's direct writes do not fight it. */
static void runner_exchange(const char *request, char *reply, size_t cap)
{
    usb_serial_jtag_driver_config_t cfg = USB_SERIAL_JTAG_DRIVER_CONFIG_DEFAULT();
    ESP_ERROR_CHECK(usb_serial_jtag_driver_install(&cfg));
    usb_serial_jtag_vfs_use_driver();

    size_t len = 0;
    bool   done = false;
    while (!done) {
        printf("%s\n", request);
        fflush(stdout);
        const int64_t until = esp_timer_get_time() + 500000;
        uint8_t c;
        while (!done && esp_timer_get_time() < until) {
            if (usb_serial_jtag_read_bytes(&c, 1, pdMS_TO_TICKS(50)) != 1) continue;
            if (c == '\n') {
                done = len > 0;
            } else if (c != '\r' && len + 1 < cap) {
                reply[len++] = (char)c;
            }
        }
    }
    reply[len] = '\0';

    fflush(stdout);
    usb_serial_jtag_vfs_use_nonblocking();
    ESP_ERROR_CHECK(usb_serial_jtag_driver_uninstall());
    drain_console();
}

static void sleep_ms(int ms)
{
    const TickType_t ticks = pdMS_TO_TICKS(ms);
    vTaskDelay(ticks > 0 ? ticks : 1);
}

static int64_t now_ms(void) { return esp_timer_get_time() / 1000; }

/* ── Decisions, as the FSM receives them ──────────────────────────────────── */

static std::atomic<int>  s_decisions{0};
static std::atomic<bool> s_last_granted{false};
static char              s_last_reason[33];

static void on_decision(const portunus_event_t *event, void *)
{
    strlcpy(s_last_reason, event->payload.access_decision.reason, sizeof(s_last_reason));
    s_last_granted = event->id == EVENT_ACCESS_GRANTED;
    s_decisions.fetch_add(1, std::memory_order_release);
}

struct tap_result_t {
    bool decided;
    bool granted;
    char reason[33];
    bool    strike_moved;   /* the sense pin reached the unlocked level */
    int64_t moved_at_ms;    /* when it first did */
};

static void watch_strike(tap_result_t &r)
{
    if (!r.strike_moved && gpio_get_level(SENSE) == ACTIVE) {
        r.strike_moved = true;
        r.moved_at_ms  = now_ms();
    }
}

/* Inject a card read, wait up to `timeout_ms` for its decision, and watch
 * the strike for `watch_ms` more. */
static tap_result_t tap(const credential_t &card, int timeout_ms, int watch_ms)
{
    tap_result_t r = {};
    const int before = s_decisions.load(std::memory_order_acquire);

    portunus_event_t e;
    memset(&e, 0, sizeof(e));
    e.id                                   = EVENT_CREDENTIAL_READ;
    e.payload.credential_read.credential   = card;
    e.payload.credential_read.timestamp_ms = now_ms();
    TEST_ASSERT_EQUAL(PORTUNUS_OK, event_bus_publish(&e));

    const int64_t decide_by = now_ms() + timeout_ms;
    while (now_ms() < decide_by) {
        watch_strike(r);
        if (s_decisions.load(std::memory_order_acquire) != before) {
            r.decided = true;
            r.granted = s_last_granted;
            strlcpy(r.reason, s_last_reason, sizeof(r.reason));
            break;
        }
        sleep_ms(10);
    }
    const int64_t watch_until = now_ms() + watch_ms;
    while (now_ms() < watch_until) {
        watch_strike(r);
        sleep_ms(10);
    }
    printf("tap: decided=%d granted=%d reason=%s strike_moved=%d\n",
           r.decided, r.granted, r.reason, r.strike_moved);
    return r;
}

/* Wait for the strike to return to locked (up to the hold plus margin). */
static void wait_relocked(void)
{
    const int64_t until = now_ms() + UNLOCK_HOLD_MS + 3000;
    while (gpio_get_level(SENSE) == ACTIVE && now_ms() < until) sleep_ms(20);
    TEST_ASSERT_NOT_EQUAL_MESSAGE(ACTIVE, gpio_get_level(SENSE), "strike did not re-lock");
}

/* Tap the enrolled card until it is granted (the link or session may need
 * time to come back), then let the strike re-lock. */
static void tap_until_granted(int within_ms)
{
    const int64_t until = now_ms() + within_ms;
    tap_result_t  r     = {};
    while (now_ms() < until) {
        r = tap(ENROLLED, ACCESS_DECISION_TIMEOUT_MS, 300);
        if (r.granted) break;
        sleep_ms(2000);
    }
    TEST_ASSERT_TRUE_MESSAGE(r.granted, "enrolled card never granted");
    TEST_ASSERT_TRUE_MESSAGE(r.strike_moved, "grant did not reach the strike");
    wait_relocked();
}

/* ── Composition (as main.cpp) ────────────────────────────────────────────── */

static ReaderMfrc522   s_reader;
static AccessPointGpio s_access;
static FeedbackLed     s_feedback;
static SystemFSM       s_fsm(&s_reader, &s_access, &s_feedback, &default_clock());

static void start_firmware(void)
{
    s_t0_us = esp_timer_get_time();
    TEST_ASSERT_EQUAL(PORTUNUS_OK, wifi_mgr_init(&s_cfg));
    TEST_ASSERT_EQUAL_MESSAGE(PORTUNUS_OK, wifi_mgr_start(), "WiFi did not connect");
    TEST_ASSERT_EQUAL(PORTUNUS_OK, event_bus_init());
    TEST_ASSERT_EQUAL(PORTUNUS_OK, event_bus_subscribe(EVENT_ACCESS_GRANTED, on_decision, nullptr));
    TEST_ASSERT_EQUAL(PORTUNUS_OK, event_bus_subscribe(EVENT_ACCESS_DENIED, on_decision, nullptr));
    TEST_ASSERT_EQUAL(PORTUNUS_OK, s_fsm.init());
    TEST_ASSERT_EQUAL(PORTUNUS_OK, server_comm_init(&s_cfg));
    TEST_ASSERT_EQUAL(PORTUNUS_OK, heartbeat_service_start());
    TEST_ASSERT_EQUAL(PORTUNUS_OK, s_fsm.start());
}

void setUp(void) { drain_console(); }
void tearDown(void) { drain_console(); }

/* ── HW-08: end to end ────────────────────────────────────────────────────── */

/* HW-08: the module joins WiFi and its first heartbeat is accepted over
 * gRPC, TLS (pinned test CA) and HMAC: the server's time syncs the clock. */
void test_heartbeat_accepted(void)
{
    start_firmware();
    const int64_t until = now_ms() + 30000;
    while (!server_comm_clock_synced() && now_ms() < until) sleep_ms(100);
    TEST_ASSERT_TRUE_MESSAGE(server_comm_clock_synced(), "no heartbeat accepted within 30 s");
    printf("start_to_first_heartbeat_ms=%" PRId64 "\n", (esp_timer_get_time() - s_t0_us) / 1000);
}

/* HW-08 (hazard #1 seam, F6): the enrolled card, sent as the firmware
 * formats it, matches the hash the runner enrolled through the admin API;
 * the signed grant opens the strike for the hold time and it re-locks. */
void test_enrolled_card_unlocks(void)
{
    const tap_result_t r = tap(ENROLLED, ACCESS_DECISION_TIMEOUT_MS, 300);
    TEST_ASSERT_TRUE_MESSAGE(r.decided, "no decision");
    TEST_ASSERT_TRUE_MESSAGE(r.granted, r.reason);
    TEST_ASSERT_TRUE_MESSAGE(r.strike_moved, "grant did not reach the strike");
    if (!s_first_grant_seen) {
        s_first_grant_seen = true;
        printf("start_to_first_grant_ms=%" PRId64 "\n", (esp_timer_get_time() - s_t0_us) / 1000);
    }

    wait_relocked();
    const int held = (int)(now_ms() - r.moved_at_ms);
    printf("hold_ms=%d\n", held);
    /* The FSM checks the hold timer every poll (100 ms). */
    TEST_ASSERT_INT_WITHIN(500, UNLOCK_HOLD_MS, held);
}

/* HW-08: an unknown card is denied by the server and the strike stays put. */
void test_unknown_card_denied(void)
{
    const tap_result_t r = tap(UNKNOWN, ACCESS_DECISION_TIMEOUT_MS, 1000);
    TEST_ASSERT_TRUE(r.decided);
    TEST_ASSERT_FALSE(r.granted);
    TEST_ASSERT_FALSE(r.strike_moved);
}

/* ── HW-07: TLS ───────────────────────────────────────────────────────────── */

/* HW-07: with the server on a cert from a CA the module does not pin, every
 * request fails closed (denied, strike untouched), the client backs off,
 * and it recovers once the right cert is back. */
void test_wrong_ca_fails_closed_then_recovers(void)
{
    char reply[16];
    runner_exchange("HW_NEED server=wrong_ca", reply, sizeof(reply));
    TEST_ASSERT_EQUAL_STRING("OK", reply);

    for (int i = 0; i < 2; ++i) {
        const tap_result_t r = tap(ENROLLED, ACCESS_DECISION_TIMEOUT_MS, 500);
        TEST_ASSERT_TRUE(r.decided);
        TEST_ASSERT_FALSE_MESSAGE(r.granted, "granted through a server with an unpinned CA");
        TEST_ASSERT_FALSE(r.strike_moved);
    }

    runner_exchange("HW_NEED server=good", reply, sizeof(reply));
    TEST_ASSERT_EQUAL_STRING("OK", reply);
    tap_until_granted(90000);   /* reconnect backoff tops out at 30 s */
}

/* ── HW-06: link loss ─────────────────────────────────────────────────────── */

/* HW-06 (F17): a dropped link (forced esp_wifi_disconnect; the router is
 * not ours to power-cycle) is denied locally as no_network, and the
 * reconnect task brings it back with no help. */
void test_link_drop_denies_then_reconnects(void)
{
    TEST_ASSERT_EQUAL(ESP_OK, esp_wifi_disconnect());
    const int64_t until = now_ms() + 3000;
    while (wifi_mgr_is_connected() && now_ms() < until) sleep_ms(10);
    TEST_ASSERT_FALSE(wifi_mgr_is_connected());

    const tap_result_t r = tap(ENROLLED, ACCESS_DECISION_TIMEOUT_MS, 200);
    TEST_ASSERT_TRUE(r.decided);
    TEST_ASSERT_FALSE(r.granted);
    TEST_ASSERT_EQUAL_STRING("no_network", r.reason);
    TEST_ASSERT_FALSE(r.strike_moved);

    const int64_t back_by = now_ms() + 30000;
    while (!wifi_mgr_is_connected() && now_ms() < back_by) sleep_ms(100);
    TEST_ASSERT_TRUE_MESSAGE(wifi_mgr_is_connected(), "WiFi did not reconnect within 30 s");
    tap_until_granted(60000);
}

/* ── WIFI-HW: wifi_mgr lifecycle on the real driver ───────────────────────── */

static std::atomic<int> s_wifi_up_events{0};
static std::atomic<int> s_reconnect_attempts{0};
static vprintf_like_t   s_prev_vprintf = nullptr;

/* Counts the reconnect task's attempts from its log line: a stale attempt
 * on a stopped driver fails without any WiFi event, so events alone cannot
 * show it. Forwards everything to the console as before. */
static int counting_vprintf(const char *fmt, va_list args)
{
    char    line[160];
    va_list copy;
    va_copy(copy, args);
    vsnprintf(line, sizeof(line), fmt, copy);
    va_end(copy);
    if (strstr(line, "Reconnect: connecting") != nullptr) {
        ++s_reconnect_attempts;
    }
    return s_prev_vprintf(fmt, args);
}

static void count_up_events(void *, esp_event_base_t base, int32_t id, void *)
{
    if ((base == WIFI_EVENT && id == WIFI_EVENT_STA_CONNECTED) ||
        (base == IP_EVENT && id == IP_EVENT_STA_GOT_IP)) {
        ++s_wifi_up_events;
    }
}

/* WIFI-HW: after wifi_mgr_stop() no reconnect happens (well past the first
 * backoff); start() connects again; deinit() then init() and start() work,
 * and the stack still grants afterwards. */
void test_wifi_stop_start_deinit_init(void)
{
    esp_event_handler_instance_t wifi_h = nullptr, ip_h = nullptr;
    TEST_ASSERT_EQUAL(ESP_OK, esp_event_handler_instance_register(
        WIFI_EVENT, WIFI_EVENT_STA_CONNECTED, count_up_events, nullptr, &wifi_h));
    TEST_ASSERT_EQUAL(ESP_OK, esp_event_handler_instance_register(
        IP_EVENT, IP_EVENT_STA_GOT_IP, count_up_events, nullptr, &ip_h));

    s_reconnect_attempts = 0;
    s_prev_vprintf = esp_log_set_vprintf(counting_vprintf);
    wifi_mgr_stop();
    s_wifi_up_events = 0;
    sleep_ms(8 * PORTUNUS_WIFI_RECONNECT_INTERVAL_MS);
    esp_log_set_vprintf(s_prev_vprintf);
    TEST_ASSERT_FALSE(wifi_mgr_is_connected());
    TEST_ASSERT_EQUAL_INT_MESSAGE(0, s_wifi_up_events.load(), "reconnected after wifi_mgr_stop()");
    TEST_ASSERT_EQUAL_INT_MESSAGE(0, s_reconnect_attempts.load(),
                                  "reconnect attempted after wifi_mgr_stop()");

    TEST_ASSERT_EQUAL(PORTUNUS_OK, wifi_mgr_start());
    TEST_ASSERT_TRUE(wifi_mgr_is_connected());
    TEST_ASSERT_GREATER_THAN_INT(0, s_wifi_up_events.load());

    esp_event_handler_instance_unregister(WIFI_EVENT, WIFI_EVENT_STA_CONNECTED, wifi_h);
    esp_event_handler_instance_unregister(IP_EVENT, IP_EVENT_STA_GOT_IP, ip_h);

    TEST_ASSERT_EQUAL(PORTUNUS_OK, wifi_mgr_deinit());
    TEST_ASSERT_FALSE(wifi_mgr_is_connected());
    TEST_ASSERT_EQUAL(PORTUNUS_OK, wifi_mgr_init(&s_cfg));
    TEST_ASSERT_EQUAL(PORTUNUS_OK, wifi_mgr_start());
    tap_until_granted(60000);
}

/* ── HW-09: stacks ────────────────────────────────────────────────────────── */

/* HW-09: after everything above, every firmware task still has a margin of
 * free stack. On ESP-IDF the high-water mark is in bytes. */
void test_stack_margins(void)
{
    static const char *const REQUIRED[] = {
        "fsm", "credential_poll", "server_comm", "heartbeat",
        "led_pattern", "wifi_reconn", "evt_dispatch",
    };
    constexpr uint32_t MARGIN_BYTES = 512;

    UBaseType_t   n     = uxTaskGetNumberOfTasks() + 4;
    TaskStatus_t *tasks = static_cast<TaskStatus_t *>(calloc(n, sizeof(TaskStatus_t)));
    TEST_ASSERT_NOT_NULL(tasks);
    n = uxTaskGetSystemState(tasks, n, nullptr);

    for (UBaseType_t i = 0; i < n; ++i) {
        printf("stack: %-16s free=%u\n", tasks[i].pcTaskName,
               (unsigned)tasks[i].usStackHighWaterMark);
    }
    for (const char *name : REQUIRED) {
        const TaskStatus_t *found = nullptr;
        for (UBaseType_t i = 0; i < n; ++i) {
            if (strcmp(tasks[i].pcTaskName, name) == 0) found = &tasks[i];
        }
        TEST_ASSERT_NOT_NULL_MESSAGE(found, name);
        TEST_ASSERT_GREATER_OR_EQUAL_UINT32_MESSAGE(MARGIN_BYTES, found->usStackHighWaterMark, name);
    }
    free(tasks);
}

/* ── Entry ────────────────────────────────────────────────────────────────── */

/* WiFi credentials from the board's NVS, read-only; everything else from
 * the runner. NVS is never erased, even if it fails to open. */
static void load_config(void)
{
    const esp_err_t err = nvs_flash_init();
    if (err != ESP_OK) {
        printf("HW_FATAL nvs_flash_init failed: %s (NVS left untouched)\n", esp_err_to_name(err));
        abort();
    }
    portunus_device_config_t nvs = {};
    if (portunus_nvs_load(&nvs) != ESP_OK) {
        printf("HW_FATAL no device config in NVS (WiFi credentials needed)\n");
        abort();
    }
    strlcpy(s_cfg.wifi_ssid, nvs.wifi_ssid, sizeof(s_cfg.wifi_ssid));
    strlcpy(s_cfg.wifi_psk, nvs.wifi_psk, sizeof(s_cfg.wifi_psk));
    memset(&nvs, 0, sizeof(nvs));   /* the board's own HMAC secret is not used */

    char line[200];
    char host[64], module[33], hmac[65];
    unsigned port = 0;
    runner_exchange("HW_TESTS_READY net", line, sizeof(line));
    if (sscanf(line, "CFG %63s %u %32s %64s", host, &port, module, hmac) != 4) {
        printf("HW_FATAL bad config line from runner\n");
        abort();
    }
    strlcpy(s_cfg.server_host, host, sizeof(s_cfg.server_host));
    s_cfg.grpc_port = (uint16_t)port;
    strlcpy(s_cfg.module_id, module, sizeof(s_cfg.module_id));
    strlcpy(s_cfg.hmac_secret, hmac, sizeof(s_cfg.hmac_secret));
    memset(line, 0, sizeof(line));
    memset(hmac, 0, sizeof(hmac));
    printf("config: server=%s:%u module=%s\n", s_cfg.server_host, port, s_cfg.module_id);
}

extern "C" void app_main(void)
{
    gpio_config_t sense = {};
    sense.pin_bit_mask = 1ULL << SENSE;
    sense.mode         = GPIO_MODE_INPUT;
    ESP_ERROR_CHECK(gpio_config(&sense));

    load_config();

    UnityPrint("\n\n");   /* Unity's first bytes after the console switch are lost */
    UNITY_BEGIN();
    RUN_TEST(test_heartbeat_accepted);
    RUN_TEST(test_enrolled_card_unlocks);
    RUN_TEST(test_unknown_card_denied);
    RUN_TEST(test_wrong_ca_fails_closed_then_recovers);
    RUN_TEST(test_link_drop_denies_then_reconnects);
    RUN_TEST(test_wifi_stop_start_deinit_init);
    RUN_TEST(test_stack_margins);
    UNITY_END();

    drain_console();
    printf("HW_TESTS_DONE\n");
    fflush(stdout);
    for (;;) {
        sleep_ms(1000);
    }
}
