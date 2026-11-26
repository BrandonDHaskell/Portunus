#include "portunus_nvs.hpp"

#include "nvs.h"
#include "esp_log.h"

#include <string.h>

static const char *TAG = "portunus_nvs";

/* Overwrite a buffer in a way the compiler cannot drop as a dead store. */
static void secure_zero(void *p, size_t n)
{
    volatile unsigned char *v = static_cast<volatile unsigned char *>(p);
    while (n--) {
        *v++ = 0;
    }
}

esp_err_t portunus_nvs_load(portunus_device_config_t *out)
{
    /* Every key is read into s_staged and copied to *out only once all of
     * them succeeded, so *out is unchanged on error as the header promises.
     * Static rather than a local: about 460 bytes against the 3584-byte main
     * task stack. This runs once at boot from app_main and is not reentrant. */
    static portunus_device_config_t s_staged;
    memset(&s_staged, 0, sizeof(s_staged));

    nvs_handle_t h;
    esp_err_t err = nvs_open(PORTUNUS_NVS_NAMESPACE, NVS_READONLY, &h);
    if (err != ESP_OK) {
        ESP_LOGE(TAG, "nvs_open(\"%s\") failed: %s",
                 PORTUNUS_NVS_NAMESPACE, esp_err_to_name(err));
        return err;
    }

    struct { const char *key; char *buf; size_t cap; } strings[] = {
        { "module_id",   s_staged.module_id,   sizeof(s_staged.module_id)   },
        { "wifi_ssid",   s_staged.wifi_ssid,   sizeof(s_staged.wifi_ssid)   },
        { "wifi_psk",    s_staged.wifi_psk,    sizeof(s_staged.wifi_psk)    },
        { "server_host", s_staged.server_host, sizeof(s_staged.server_host) },
        { "hmac_secret", s_staged.hmac_secret, sizeof(s_staged.hmac_secret) },
    };

    for (auto &s : strings) {
        size_t len = s.cap;
        err = nvs_get_str(h, s.key, s.buf, &len);
        if (err != ESP_OK) {
            ESP_LOGE(TAG, "Missing NVS key '%s': %s", s.key, esp_err_to_name(err));
            goto done;
        }
    }

    err = nvs_get_u16(h, "grpc_port", &s_staged.grpc_port);
    if (err != ESP_OK) {
        ESP_LOGE(TAG, "Missing NVS key 'grpc_port': %s", esp_err_to_name(err));
    }

done:
    nvs_close(h);
    if (err == ESP_OK) {
        memcpy(out, &s_staged, sizeof(*out));
    }
    /* Do not leave a second copy of the HMAC secret and WiFi PSK in RAM. */
    secure_zero(&s_staged, sizeof(s_staged));
    return err;
}
