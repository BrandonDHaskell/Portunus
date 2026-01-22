#pragma once
#include <stddef.h>
#include "esp_err.h"

#ifdef __cplusplus
extern "C" {
#endif

esp_err_t http_post_json(const char* url,
                         const char* json_body,
                         char* resp_buf,
                         size_t resp_buf_len);

#ifdef __cplusplus
}
#endif
