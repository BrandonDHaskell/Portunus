/* Component tier, NVS variant: ESP-IDF linux target.
 *
 * Built with -DPORTUNUS_TEST_NVS=ON, which adds portunus_nvs and nvs_flash to
 * the build. On the linux target nvs_flash runs on esp_partition's emulated
 * flash, a temporary file created per run, with the partition table from this
 * build directory. Each test starts from an erased NVS and writes the keys it
 * needs. All values are obvious placeholders, not deployment secrets.
 */

#include "unity.h"
#include "portunus_nvs.hpp"
#include "nvs_flash.h"
#include "nvs.h"

#include <stdlib.h>
#include <string.h>
#include <string>

static void reset_nvs(void) {
    nvs_flash_deinit();
    TEST_ASSERT_EQUAL(ESP_OK, nvs_flash_erase());
    TEST_ASSERT_EQUAL(ESP_OK, nvs_flash_init());
}

void setUp(void) { reset_nvs(); }
void tearDown(void) { nvs_flash_deinit(); }

/* ── Provisioned values ───────────────────────────────────────────────────── */

struct Values {
    std::string module_id   = "test-module";
    std::string wifi_ssid   = "test-ssid";
    std::string wifi_psk    = "test-psk";
    std::string server_host = "10.0.0.2";
    uint16_t    grpc_port   = 50051;
    std::string hmac_secret = std::string(64, 'a');
};

static const char *const KEYS[] = {
    "module_id", "wifi_ssid", "wifi_psk", "server_host", "grpc_port", "hmac_secret",
};

/* Write every key except `skip` (nullptr writes all six). */
static void provision(const Values &v, const char *skip = nullptr) {
    nvs_handle_t h;
    TEST_ASSERT_EQUAL(ESP_OK, nvs_open(PORTUNUS_NVS_NAMESPACE, NVS_READWRITE, &h));
    auto put = [&](const char *key, const std::string &val) {
        if (skip && strcmp(skip, key) == 0) return;
        TEST_ASSERT_EQUAL(ESP_OK, nvs_set_str(h, key, val.c_str()));
    };
    put("module_id", v.module_id);
    put("wifi_ssid", v.wifi_ssid);
    put("wifi_psk", v.wifi_psk);
    put("server_host", v.server_host);
    put("hmac_secret", v.hmac_secret);
    if (!(skip && strcmp(skip, "grpc_port") == 0)) {
        TEST_ASSERT_EQUAL(ESP_OK, nvs_set_u16(h, "grpc_port", v.grpc_port));
    }
    TEST_ASSERT_EQUAL(ESP_OK, nvs_commit(h));
    nvs_close(h);
}

static void assert_loaded(const Values &v, const portunus_device_config_t &cfg) {
    TEST_ASSERT_EQUAL_STRING(v.module_id.c_str(), cfg.module_id);
    TEST_ASSERT_EQUAL_STRING(v.wifi_ssid.c_str(), cfg.wifi_ssid);
    TEST_ASSERT_EQUAL_STRING(v.wifi_psk.c_str(), cfg.wifi_psk);
    TEST_ASSERT_EQUAL_STRING(v.server_host.c_str(), cfg.server_host);
    TEST_ASSERT_EQUAL_UINT16(v.grpc_port, cfg.grpc_port);
    TEST_ASSERT_EQUAL_STRING(v.hmac_secret.c_str(), cfg.hmac_secret);
}

/* ── Tests ────────────────────────────────────────────────────────────────── */

/* NVS-01: all six keys present load exactly, including the documented empty
 * values (open network, HMAC disabled) and strings that exactly fill their
 * buffers. */
void test_all_keys_load_exactly(void) {
    portunus_device_config_t cfg;
    Values v;
    provision(v);
    TEST_ASSERT_EQUAL(ESP_OK, portunus_nvs_load(&cfg));
    assert_loaded(v, cfg);

    reset_nvs();
    Values empty;
    empty.wifi_psk    = "";
    empty.hmac_secret = "";
    provision(empty);
    TEST_ASSERT_EQUAL(ESP_OK, portunus_nvs_load(&cfg));
    assert_loaded(empty, cfg);

    reset_nvs();
    Values full;
    full.module_id   = std::string(PORTUNUS_NVS_MODULE_ID_LEN - 1, 'm');
    full.wifi_ssid   = std::string(PORTUNUS_NVS_WIFI_SSID_LEN - 1, 's');
    full.wifi_psk    = std::string(PORTUNUS_NVS_WIFI_PSK_LEN - 1, 'p');
    full.server_host = std::string(PORTUNUS_NVS_SERVER_HOST_LEN - 1, 'h');
    full.grpc_port   = 65535;
    provision(full);
    TEST_ASSERT_EQUAL(ESP_OK, portunus_nvs_load(&cfg));
    assert_loaded(full, cfg);
}

/* NVS-02: each of the six keys missing on its own is ESP_ERR_NVS_NOT_FOUND. */
void test_each_missing_key_not_found(void) {
    for (const char *key : KEYS) {
        reset_nvs();
        provision(Values{}, key);
        portunus_device_config_t cfg;
        TEST_ASSERT_EQUAL_MESSAGE(ESP_ERR_NVS_NOT_FOUND, portunus_nvs_load(&cfg), key);
    }
}

/* NVS-03: a string one character too long for its buffer is
 * ESP_ERR_NVS_INVALID_LENGTH, not a silently truncated value. */
void test_oversize_string_invalid_length(void) {
    Values v;
    v.module_id = std::string(PORTUNUS_NVS_MODULE_ID_LEN, 'm');   /* 33 chars */
    provision(v);
    portunus_device_config_t cfg;
    TEST_ASSERT_EQUAL(ESP_ERR_NVS_INVALID_LENGTH, portunus_nvs_load(&cfg));

    reset_nvs();
    Values w;
    w.hmac_secret = std::string(PORTUNUS_NVS_HMAC_SECRET_LEN, 'a'); /* 65 chars */
    provision(w);
    TEST_ASSERT_EQUAL(ESP_ERR_NVS_INVALID_LENGTH, portunus_nvs_load(&cfg));
}

/* NVS-04 (F14): on any error `out` is left exactly as the caller had it, as
 * the header promises. Checked for every missing key (the earlier keys would
 * have been read by then), an oversize late key, and a missing namespace. */
void test_out_unchanged_on_error(void) {
    portunus_device_config_t before;
    memset(&before, 0xA5, sizeof before);

    auto check = [&](const char *what) {
        portunus_device_config_t cfg = before;
        TEST_ASSERT_NOT_EQUAL(ESP_OK, portunus_nvs_load(&cfg));
        TEST_ASSERT_EQUAL_MEMORY_MESSAGE(&before, &cfg, sizeof cfg, what);
    };

    for (const char *key : KEYS) {
        reset_nvs();
        provision(Values{}, key);
        check(key);
    }

    reset_nvs();
    Values v;
    v.hmac_secret = std::string(PORTUNUS_NVS_HMAC_SECRET_LEN, 'a');
    provision(v);
    check("oversize hmac_secret");

    reset_nvs();
    check("namespace absent");
}

/* NVS-05: with no "portunus" namespace the nvs_open error is returned as is,
 * even when other namespaces exist. */
void test_namespace_absent_propagates_open_error(void) {
    portunus_device_config_t cfg;
    TEST_ASSERT_EQUAL(ESP_ERR_NVS_NOT_FOUND, portunus_nvs_load(&cfg));

    nvs_handle_t h;
    TEST_ASSERT_EQUAL(ESP_OK, nvs_open("other", NVS_READWRITE, &h));
    TEST_ASSERT_EQUAL(ESP_OK, nvs_set_str(h, "module_id", "not-ours"));
    TEST_ASSERT_EQUAL(ESP_OK, nvs_commit(h));
    nvs_close(h);
    TEST_ASSERT_EQUAL(ESP_ERR_NVS_NOT_FOUND, portunus_nvs_load(&cfg));
}

extern "C" void app_main(void) {
    UNITY_BEGIN();
    RUN_TEST(test_all_keys_load_exactly);
    RUN_TEST(test_each_missing_key_not_found);
    RUN_TEST(test_oversize_string_invalid_length);
    RUN_TEST(test_out_unchanged_on_error);
    RUN_TEST(test_namespace_absent_propagates_open_error);
    exit(UNITY_END());
}
