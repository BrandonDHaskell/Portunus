#pragma once
/* Test controls for the fake grpc_client. Thread-safe: server_comm calls the
 * fake from its own task while the test task scripts and inspects it. */
#include "portunus_types.hpp"

#include <stdint.h>
#include <functional>
#include <string>
#include <vector>

struct grpc_fake_reply {
    portunus_err_t       err    = PORTUNUS_OK;   /* returned by unary_call */
    int                  status = 0;             /* gRPC status */
    std::vector<uint8_t> body;                   /* protobuf response */
    std::string          sig;                    /* x-portunus-sig trailer */
};

struct grpc_fake_call {
    std::string          method;
    std::vector<uint8_t> request;
    std::string          sig;                    /* x-portunus-sig sent, "" if none */
};

/** Builds a reply from the call it answers (e.g. to sign over the request's
 *  nonce). Runs on server_comm's task with the fake's lock held: keep it
 *  pure. */
using grpc_fake_responder = std::function<grpc_fake_reply(const grpc_fake_call &)>;

void grpc_fake_reset(void);

/** Queue a reply for the next call to `method`. A call with nothing queued
 *  for its method fails with PORTUNUS_ERR_HTTP_CONNECT, like an unreachable
 *  server. */
void grpc_fake_push_reply(const char *method, const grpc_fake_reply &reply);
void grpc_fake_push_responder(const char *method, grpc_fake_responder responder);

size_t grpc_fake_call_count(void);
bool   grpc_fake_get_call(size_t i, grpc_fake_call *out);
int    grpc_fake_live_clients(void);   /* inits minus destroys */
