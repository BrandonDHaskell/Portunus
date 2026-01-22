#include "http_client.h"

#include <cstring>

#include "esp_http_client.h"
#include "esp_log.h"

static const char* TAG = "http_client";

extern "C" esp_err_t http_post_json(const char* url,
                                    const char* json_body,
                                    char* resp_buf,
                                    size_t resp_buf_len) {
    if (!url || !json_body) return ESP_ERR_INVALID_ARG;

    esp_http_client_config_t cfg{};
    cfg.url = url;
    cfg.method = HTTP_METHOD_POST;
    cfg.timeout_ms = 5000;

    esp_http_client_handle_t client = esp_http_client_init(&cfg);
    if (!client) return ESP_ERR_NO_MEM;

    esp_http_client_set_header(client, "Content-Type", "application/json");
    esp_http_client_set_post_field(client, json_body, std::strlen(json_body));

    esp_err_t err = esp_http_client_perform(client);
    if (err != ESP_OK) {
        ESP_LOGW(TAG, "POST failed: %s", esp_err_to_name(err));
        esp_http_client_cleanup(client);
        return err;
    }

    int status = esp_http_client_get_status_code(client);
    int len = esp_http_client_get_content_length(client);
    ESP_LOGI(TAG, "POST status=%d content_length=%d", status, len);

    if (resp_buf && resp_buf_len > 0) {
        std::memset(resp_buf, 0, resp_buf_len);
        int read = esp_http_client_read_response(client, resp_buf, (int)resp_buf_len - 1);
        if (read < 0) read = 0;
        resp_buf[read] = '\0';
    }

    esp_http_client_cleanup(client);
    return ESP_OK;
}
