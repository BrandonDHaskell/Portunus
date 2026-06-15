#include "portunus_nvs.hpp"

#include "nvs.h"
#include "esp_log.h"

#include <string.h>

static const char *TAG = "portunus_nvs";

esp_err_t portunus_nvs_load(portunus_device_config_t *out)
{
    nvs_handle_t h;
    esp_err_t err = nvs_open(PORTUNUS_NVS_NAMESPACE, NVS_READONLY, &h);
    if (err != ESP_OK) {
        ESP_LOGE(TAG, "nvs_open(\"%s\") failed: %s",
                 PORTUNUS_NVS_NAMESPACE, esp_err_to_name(err));
        return err;
    }

    struct { const char *key; char *buf; size_t cap; } strings[] = {
        { "module_id",   out->module_id,   sizeof(out->module_id)   },
        { "wifi_ssid",   out->wifi_ssid,   sizeof(out->wifi_ssid)   },
        { "wifi_psk",    out->wifi_psk,    sizeof(out->wifi_psk)    },
        { "server_host", out->server_host, sizeof(out->server_host) },
        { "hmac_secret", out->hmac_secret, sizeof(out->hmac_secret) },
    };

    for (auto &s : strings) {
        size_t len = s.cap;
        err = nvs_get_str(h, s.key, s.buf, &len);
        if (err != ESP_OK) {
            ESP_LOGE(TAG, "Missing NVS key '%s': %s", s.key, esp_err_to_name(err));
            goto done;
        }
    }

    err = nvs_get_u16(h, "grpc_port", &out->grpc_port);
    if (err != ESP_OK) {
        ESP_LOGE(TAG, "Missing NVS key 'grpc_port': %s", esp_err_to_name(err));
    }

done:
    nvs_close(h);
    return err;
}
