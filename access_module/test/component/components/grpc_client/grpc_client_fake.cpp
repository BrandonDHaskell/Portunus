#include "grpc_client.hpp"
#include "grpc_client_fake.hpp"
#include "error_codes.hpp"

#include "freertos/FreeRTOS.h"
#include "freertos/semphr.h"

#include <deque>
#include <map>
#include <string.h>

struct grpc_client { int unused; };

namespace {
struct Scripted { grpc_fake_reply reply; grpc_fake_responder responder; };
std::map<std::string, std::deque<Scripted>>        g_replies;
std::vector<grpc_fake_call>                        g_calls;
int                                                g_live = 0;
grpc_client                                        g_instance;

SemaphoreHandle_t lock_handle() {
    static SemaphoreHandle_t m = xSemaphoreCreateMutex();
    return m;
}
struct Guard {
    Guard()  { xSemaphoreTake(lock_handle(), portMAX_DELAY); }
    ~Guard() { xSemaphoreGive(lock_handle()); }
};
}  // namespace

void grpc_fake_reset(void) {
    Guard g;
    g_replies.clear();
    g_calls.clear();
    g_live = 0;
}

void grpc_fake_push_reply(const char *method, const grpc_fake_reply &reply) {
    Guard g;
    g_replies[method].push_back({reply, nullptr});
}

void grpc_fake_push_responder(const char *method, grpc_fake_responder responder) {
    Guard g;
    g_replies[method].push_back({grpc_fake_reply{}, responder});
}

size_t grpc_fake_call_count(void) {
    Guard g;
    return g_calls.size();
}

bool grpc_fake_get_call(size_t i, grpc_fake_call *out) {
    Guard g;
    if (i >= g_calls.size() || out == nullptr) return false;
    *out = g_calls[i];
    return true;
}

int grpc_fake_live_clients(void) {
    Guard g;
    return g_live;
}

extern "C" {

portunus_err_t grpc_client_init(const grpc_client_config_t *cfg, grpc_client_handle_t *handle) {
    if (cfg == nullptr || handle == nullptr) return PORTUNUS_ERR_INVALID_ARG;
    Guard g;
    ++g_live;
    *handle = &g_instance;
    return PORTUNUS_OK;
}

void grpc_client_destroy(grpc_client_handle_t handle) {
    if (handle == nullptr) return;
    Guard g;
    --g_live;
}

portunus_err_t grpc_client_connect(grpc_client_handle_t) { return PORTUNUS_OK; }
void           grpc_client_disconnect(grpc_client_handle_t) {}
bool           grpc_client_is_connected(grpc_client_handle_t) { return true; }
portunus_err_t grpc_client_send_ping(grpc_client_handle_t) { return PORTUNUS_OK; }

portunus_err_t grpc_client_unary_call(grpc_client_handle_t handle, const char *service_method,
                                      const uint8_t *req_buf, size_t req_len,
                                      const grpc_metadata_t *metadata, size_t metadata_count,
                                      uint8_t *resp_buf, size_t resp_cap,
                                      int *resp_len, int *grpc_status, char *out_sig_hex) {
    if (handle == nullptr || service_method == nullptr || resp_len == nullptr ||
        grpc_status == nullptr || metadata_count > GRPC_MAX_CALL_METADATA) {
        return PORTUNUS_ERR_INVALID_ARG;
    }
    if (out_sig_hex != nullptr) memset(out_sig_hex, 0, 65);   /* PORTUNUS_HMAC_HEX_LEN */

    Guard g;
    grpc_fake_call call;
    call.method = service_method;
    call.request.assign(req_buf, req_buf + req_len);
    for (size_t i = 0; i < metadata_count; ++i) {
        if (strcmp(metadata[i].key, "x-portunus-sig") == 0) call.sig = metadata[i].value;
    }
    g_calls.push_back(call);

    auto it = g_replies.find(service_method);
    if (it == g_replies.end() || it->second.empty()) return PORTUNUS_ERR_HTTP_CONNECT;
    const Scripted next = it->second.front();
    it->second.pop_front();
    const grpc_fake_reply reply = next.responder ? next.responder(call) : next.reply;

    *grpc_status = reply.status;
    if (reply.err != PORTUNUS_OK) return reply.err;
    if (reply.body.size() > resp_cap) return PORTUNUS_ERR_PROTO_DECODE;
    memcpy(resp_buf, reply.body.data(), reply.body.size());
    *resp_len = static_cast<int>(reply.body.size());
    if (out_sig_hex != nullptr) strncpy(out_sig_hex, reply.sig.c_str(), 64);
    return PORTUNUS_OK;
}

}  // extern "C"
