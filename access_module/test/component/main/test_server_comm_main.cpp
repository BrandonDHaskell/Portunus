/* Component tier, server_comm variants: ESP-IDF linux target.
 *
 * Built with -DPORTUNUS_TEST_SERVER_COMM=ON. The real server_comm runs its
 * own task against fakes: grpc_client (scripted per RPC method), wifi_mgr
 * (connectivity knob), net_info, and the fake event bus. Tests publish
 * events, then wait (bounded) for what server_comm publishes back.
 *
 *   sc         access point, HMAC on      SC-01..05, 07..11, 13, 14, 18, PROTO-19
 *   sc-nohmac  access point, HMAC off     SC-12
 *   sc-peu     provisioning console       SC-03, SC-06 and the provision path
 */

#include "unity.h"
#include "server_comm.hpp"
#include "access_protocol.hpp"
#include "security_config.hpp"
#include "event_bus.hpp"
#include "error_codes.hpp"
#include "event_types.hpp"
#include "credential_types.h"
#include "sdkconfig.h"

#include "grpc_client_fake.hpp"
#include "wifi_mgr_fake.hpp"
#include "../components/event_bus/include/event_bus_fake.hpp"

#include "portunus/v1/portunus.pb.h"
#include <pb_decode.h>
#include <pb_encode.h>
#include "mbedtls/md.h"

#include "freertos/FreeRTOS.h"
#include "freertos/task.h"

#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <string>
#include <vector>

static const char *HEARTBEAT_RPC = "/portunus.v1.PortunusService/SendHeartbeat";
static const char *ACCESS_RPC    = "/portunus.v1.PortunusService/RequestAccess";
static const char *PROVISION_RPC = "/portunus.v1.PortunusService/ProvisionCredential";

/* The shared vector file's key, so PROTO-19 can compare signatures with it. */
static const char *SECRET = "portunus-test-vector-key-not-a-secret";
static const char *MODULE = "door-001";

/* ── Clock-setter fake ────────────────────────────────────────────────────── */

static int            g_set_time_calls  = 0;
static struct timeval g_set_time_last   = {};
static int fake_set_time(const struct timeval *tv) {
    ++g_set_time_calls;
    g_set_time_last = *tv;
    return 0;
}

void setUp(void) {
    event_bus_fake_reset();
    event_bus_init();
    grpc_fake_reset();
    wifi_mgr_fake_set_connected(true);
    g_set_time_calls = 0;
    g_set_time_last  = {};
    server_comm_set_clock_setter_for_test(fake_set_time);
}

void tearDown(void) {
    server_comm_reset_for_test();
}

/* ── Helpers ──────────────────────────────────────────────────────────────── */

static portunus_err_t start(const char *secret = SECRET) {
    portunus_device_config_t cfg = {};
    snprintf(cfg.module_id, sizeof cfg.module_id, "%s", MODULE);
    snprintf(cfg.server_host, sizeof cfg.server_host, "%s", "10.0.0.2");
    cfg.grpc_port = 50051;
    snprintf(cfg.hmac_secret, sizeof cfg.hmac_secret, "%s", secret);
    return server_comm_init(&cfg);
}

/* Poll until pred() holds or timeout_ms passes. */
template <typename Pred>
static bool wait_for(Pred pred, int timeout_ms = 2000) {
    for (int t = 0; t < timeout_ms; t += 10) {
        if (pred()) return true;
        vTaskDelay(pdMS_TO_TICKS(10));
    }
    return pred();
}

static void settle(int ms = 300) { vTaskDelay(pdMS_TO_TICKS(ms)); }

static std::string hmac_hex(const std::string &data, const char *key = SECRET) {
    unsigned char raw[32];
    mbedtls_md_hmac(mbedtls_md_info_from_type(MBEDTLS_MD_SHA256),
                    reinterpret_cast<const unsigned char *>(key), strlen(key),
                    reinterpret_cast<const unsigned char *>(data.data()), data.size(), raw);
    char hex[65];
    for (int i = 0; i < 32; ++i) snprintf(&hex[2 * i], 3, "%02x", raw[i]);
    return hex;
}

template <typename Msg>
static std::vector<uint8_t> encode(const pb_msgdesc_t *fields, const Msg &msg) {
    std::vector<uint8_t> buf(256);
    pb_ostream_t os = pb_ostream_from_buffer(buf.data(), buf.size());
    TEST_ASSERT_TRUE(pb_encode(&os, fields, &msg));
    buf.resize(os.bytes_written);
    return buf;
}

static grpc_fake_reply heartbeat_reply(const char *server_time) {
    portunus_v1_HeartbeatResponse r = portunus_v1_HeartbeatResponse_init_zero;
    r.ok = true;
    r.known = true;
    snprintf(r.server_time, sizeof r.server_time, "%s", server_time);
    grpc_fake_reply reply;
    reply.body = encode(portunus_v1_HeartbeatResponse_fields, r);
    return reply;
}

static portunus_v1_AccessRequest decode_access(const grpc_fake_call &call) {
    portunus_v1_AccessRequest req = portunus_v1_AccessRequest_init_zero;
    pb_istream_t is = pb_istream_from_buffer(call.request.data(), call.request.size());
    TEST_ASSERT_TRUE(pb_decode(&is, portunus_v1_AccessRequest_fields, &req));
    return req;
}

enum class Sig { VALID, MISSING, WRONG };

/* Answers a RequestAccess the way the server would, signing over the
 * request's own nonce. */
static grpc_fake_responder access_responder(bool granted, const char *reason, Sig sig) {
    return [granted, reason, sig](const grpc_fake_call &call) {
        portunus_v1_AccessRequest req = portunus_v1_AccessRequest_init_zero;
        pb_istream_t is = pb_istream_from_buffer(call.request.data(), call.request.size());
        pb_decode(&is, portunus_v1_AccessRequest_fields, &req);
        portunus_v1_AccessResponse r = portunus_v1_AccessResponse_init_zero;
        r.ok = true;
        r.known = true;
        r.granted = granted;
        snprintf(r.reason, sizeof r.reason, "%s", reason);
        snprintf(r.module_id, sizeof r.module_id, "%s", req.module_id);
        grpc_fake_reply reply;
        std::vector<uint8_t> body(256);
        pb_ostream_t os = pb_ostream_from_buffer(body.data(), body.size());
        pb_encode(&os, portunus_v1_AccessResponse_fields, &r);
        body.resize(os.bytes_written);
        reply.body = body;
        char nonce_hex[33];
        proto_hex_lower(req.nonce.bytes, req.nonce.size, nonce_hex, sizeof nonce_hex);
        char proj[PROTO_RESPONSE_PROJECTION_CAP];
        access_response_projection(proj, sizeof proj, req.module_id, req.credential_id,
                                   nonce_hex, sig == Sig::WRONG ? !granted : granted);
        if (sig != Sig::MISSING) reply.sig = hmac_hex(proj);
        return reply;
    };
}

static const credential_t CARD = {{0x04, 0xA3, 0x2B, 0x1C}, 4};

static std::string card_log_id(void) {
    char id[CREDENTIAL_LOG_ID_LEN];
    credential_uid_to_log_id(&CARD, id, sizeof id);
    return id;
}

static void publish_heartbeat(uint32_t seq) {
    portunus_event_t e;
    memset(&e, 0, sizeof e);
    e.id = EVENT_HEARTBEAT;
    e.payload.heartbeat.sequence = seq;
    event_bus_publish(&e);
}

/* The single access decision server_comm publishes for a card read. */
static portunus_event_t wait_decision(void) {
    TEST_ASSERT_TRUE_MESSAGE(wait_for([] {
        return event_bus_fake_count_of(EVENT_ACCESS_GRANTED) +
               event_bus_fake_count_of(EVENT_ACCESS_DENIED) >= 1;
    }), "no access decision published");
    settle(100);
    TEST_ASSERT_EQUAL_size_t(1, event_bus_fake_count_of(EVENT_ACCESS_GRANTED) +
                                event_bus_fake_count_of(EVENT_ACCESS_DENIED));
    portunus_event_t e;
    for (size_t i = 0; event_bus_fake_copy(i, &e); ++i) {
        if (e.id == EVENT_ACCESS_GRANTED || e.id == EVENT_ACCESS_DENIED) return e;
    }
    TEST_FAIL();
    return e;
}

static std::vector<grpc_fake_call> calls_to(const char *method) {
    std::vector<grpc_fake_call> out;
    grpc_fake_call c;
    for (size_t i = 0; grpc_fake_get_call(i, &c); ++i) {
        if (c.method == method) out.push_back(c);
    }
    return out;
}

/* ── Every variant ────────────────────────────────────────────────────────── */

/* SC-02 */
void test_second_init_refused(void) {
    TEST_ASSERT_EQUAL(PORTUNUS_OK, start());
    TEST_ASSERT_EQUAL(PORTUNUS_ERR_ALREADY_INIT, start());
}

/* SC-03: exactly the subscriptions SERVER_COMM_SUBSCRIPTION_COUNT promises. */
void test_subscriptions(void) {
    TEST_ASSERT_EQUAL(PORTUNUS_OK, start());
    TEST_ASSERT_EQUAL_size_t(SERVER_COMM_SUBSCRIPTION_COUNT, event_bus_fake_subscriber_count());
    TEST_ASSERT_EQUAL_size_t(1, event_bus_fake_subscribers_for(EVENT_HEARTBEAT));
    TEST_ASSERT_EQUAL_size_t(1, event_bus_fake_subscribers_for(EVENT_CREDENTIAL_READ_ERROR));
    TEST_ASSERT_EQUAL_size_t(1, event_bus_fake_subscribers_for(EVENT_CREDENTIAL_READER_RECOVERED));
#ifdef CONFIG_PORTUNUS_MODULE_TYPE_ACCESS_POINT
    TEST_ASSERT_EQUAL_size_t(1, event_bus_fake_subscribers_for(EVENT_CREDENTIAL_READ));
    TEST_ASSERT_EQUAL_size_t(0, event_bus_fake_subscribers_for(EVENT_PROVISION_REQUEST));
#else
    TEST_ASSERT_EQUAL_size_t(1, event_bus_fake_subscribers_for(EVENT_PROVISION_REQUEST));
    TEST_ASSERT_EQUAL_size_t(0, event_bus_fake_subscribers_for(EVENT_CREDENTIAL_READ));
#endif
}

/* SC-05: offline, a heartbeat makes no RPC and publishes nothing. */
void test_offline_heartbeat_is_dropped(void) {
    TEST_ASSERT_EQUAL(PORTUNUS_OK, start());
    wifi_mgr_fake_set_connected(false);
    publish_heartbeat(1);
    settle();
    TEST_ASSERT_EQUAL_size_t(0, grpc_fake_call_count());
    TEST_ASSERT_EQUAL_size_t(1, event_bus_fake_count());   /* the heartbeat itself */
}

/* SC-13: a heartbeat with server_time sets the clock (plus RTT/2) and marks
 * it synced; a failed or unparseable one does not. */
void test_heartbeat_syncs_clock(void) {
    TEST_ASSERT_EQUAL(PORTUNUS_OK, start());
    publish_heartbeat(1);                                  /* nothing scripted: fails */
    TEST_ASSERT_TRUE(wait_for([] { return grpc_fake_call_count() >= 1; }));
    settle(100);
    TEST_ASSERT_FALSE(server_comm_clock_synced());
    TEST_ASSERT_EQUAL_INT(0, g_set_time_calls);

    grpc_fake_push_reply(HEARTBEAT_RPC, heartbeat_reply("not-a-time"));
    publish_heartbeat(2);
    TEST_ASSERT_TRUE(wait_for([] { return grpc_fake_call_count() >= 2; }));
    settle(100);
    TEST_ASSERT_FALSE(server_comm_clock_synced());

    grpc_fake_push_reply(HEARTBEAT_RPC, heartbeat_reply("2026-01-02T03:04:05.250Z"));
    publish_heartbeat(3);
    TEST_ASSERT_TRUE(wait_for([] { return server_comm_clock_synced(); }));
    TEST_ASSERT_EQUAL_INT(1, g_set_time_calls);
    struct timeval want;
    TEST_ASSERT_TRUE(proto_parse_server_time("2026-01-02T03:04:05.250Z", &want));
    TEST_ASSERT_EQUAL_INT64(want.tv_sec, g_set_time_last.tv_sec);
    TEST_ASSERT_GREATER_OR_EQUAL_INT64(want.tv_usec, g_set_time_last.tv_usec);  /* + RTT/2 */
    TEST_ASSERT_LESS_THAN_INT64(want.tv_usec + 500000, g_set_time_last.tv_usec);
}

/* SC-18: RPC paths are /{package}.{service}/{rpc} from portunus.proto. */
static std::string rpc_path(const char *rpc) {
    FILE *f = fopen(PORTUNUS_PROTO_FILE, "r");
    TEST_ASSERT_NOT_NULL_MESSAGE(f, PORTUNUS_PROTO_FILE);
    char line[256], package[64] = "", service[64] = "", name[64];
    bool has_rpc = false;
    while (fgets(line, sizeof line, f) != nullptr) {
        sscanf(line, " package %63[^;];", package);
        sscanf(line, " service %63s", service);
        if (sscanf(line, " rpc %63[^( ]", name) == 1 && strcmp(name, rpc) == 0) has_rpc = true;
    }
    fclose(f);
    TEST_ASSERT_TRUE_MESSAGE(has_rpc, rpc);
    return std::string("/") + package + "." + service + "/" + rpc;
}

void test_rpc_paths_match_proto(void) {
    TEST_ASSERT_EQUAL_STRING(rpc_path("SendHeartbeat").c_str(), HEARTBEAT_RPC);
    TEST_ASSERT_EQUAL(PORTUNUS_OK, start());
    publish_heartbeat(1);
    TEST_ASSERT_TRUE(wait_for([] { return grpc_fake_call_count() >= 1; }));
    grpc_fake_call c;
    TEST_ASSERT_TRUE(grpc_fake_get_call(0, &c));
    TEST_ASSERT_EQUAL_STRING(HEARTBEAT_RPC, c.method.c_str());
#ifdef CONFIG_PORTUNUS_MODULE_TYPE_ACCESS_POINT
    TEST_ASSERT_EQUAL_STRING(rpc_path("RequestAccess").c_str(), ACCESS_RPC);
#else
    TEST_ASSERT_EQUAL_STRING(rpc_path("ProvisionCredential").c_str(), PROVISION_RPC);
#endif
}

/* ── Access point ─────────────────────────────────────────────────────────── */

#ifdef CONFIG_PORTUNUS_MODULE_TYPE_ACCESS_POINT

static void publish_card(void) {
    portunus_event_t e;
    memset(&e, 0, sizeof e);
    e.id = EVENT_CREDENTIAL_READ;
    e.payload.credential_read.credential = CARD;
    event_bus_publish(&e);
}

/* Card read on a synced clock, so the only RPC is RequestAccess. */
static void sync_clock(void) {
    grpc_fake_push_reply(HEARTBEAT_RPC, heartbeat_reply("2026-01-02T03:04:05Z"));
    publish_heartbeat(0);
    TEST_ASSERT_TRUE(wait_for([] { return server_comm_clock_synced(); }));
}

static void assert_denied(const portunus_event_t &e, const char *reason) {
    TEST_ASSERT_EQUAL(EVENT_ACCESS_DENIED, e.id);
    TEST_ASSERT_EQUAL_STRING(reason, e.payload.access_decision.reason);
    TEST_ASSERT_EQUAL_STRING(card_log_id().c_str(), e.payload.access_decision.credential_id);
    TEST_ASSERT_FALSE(e.payload.access_decision.granted);
}

/* SC-15 (D-T5): with the comm task stuck in an RPC and its queue full, a
 * further card read is denied at once (comm_busy) and counted as dropped. */
void test_full_queue_denies_card(void) {
    TEST_ASSERT_EQUAL(PORTUNUS_OK, start());
    grpc_fake_hold();
    publish_card();                               /* comm task blocks in its RPC */
    TEST_ASSERT_TRUE(grpc_fake_wait_held_call(2000));
    for (int i = 0; i < 8; ++i) publish_card();   /* fills the 8-deep comm queue */
    TEST_ASSERT_EQUAL_size_t(0, event_bus_fake_count_of(EVENT_ACCESS_DENIED));
    publish_card();                               /* no room: answered immediately */
    TEST_ASSERT_EQUAL_size_t(1, event_bus_fake_count_of(EVENT_ACCESS_DENIED));
    portunus_event_t e;
    for (size_t i = 0; event_bus_fake_copy(i, &e); ++i) {
        if (e.id == EVENT_ACCESS_DENIED) break;
    }
    TEST_ASSERT_EQUAL_STRING("comm_busy", e.payload.access_decision.reason);
    TEST_ASSERT_EQUAL_UINT32(1, server_comm_dropped_events());

    grpc_fake_release();                          /* drain before teardown */
    TEST_ASSERT_TRUE(wait_for([] {
        return event_bus_fake_count_of(EVENT_ACCESS_DENIED) >= 1 + 9;
    }, 5000));
}

/* SC-04: offline, a card read is answered with one deny (no_network) carrying
 * the log fingerprint, and no RPC. */
void test_offline_card_denied(void) {
    TEST_ASSERT_EQUAL(PORTUNUS_OK, start());
    wifi_mgr_fake_set_connected(false);
    publish_card();
    assert_denied(wait_decision(), "no_network");
    TEST_ASSERT_EQUAL_size_t(0, grpc_fake_call_count());
}

/* SC-07: transport failure or reconnect backoff denies with grpc_error. */
void test_transport_failure_denies(void) {
    for (const portunus_err_t err : {PORTUNUS_ERR_HTTP_CONNECT, PORTUNUS_ERR_RECONNECT_BACKOFF}) {
        setUp();
        TEST_ASSERT_EQUAL(PORTUNUS_OK, start());
        sync_clock();
        grpc_fake_reply r;
        r.err = err;
        grpc_fake_push_reply(ACCESS_RPC, r);
        publish_card();
        assert_denied(wait_decision(), "grpc_error");
        tearDown();
    }
}

/* SC-08 */
void test_bad_status_denies(void) {
    TEST_ASSERT_EQUAL(PORTUNUS_OK, start());
    sync_clock();
    grpc_fake_reply r;
    r.status = 13;   /* INTERNAL */
    grpc_fake_push_reply(ACCESS_RPC, r);
    publish_card();
    assert_denied(wait_decision(), "grpc_status_error");
}

/* SC-09 */
void test_undecodable_body_denies(void) {
    TEST_ASSERT_EQUAL(PORTUNUS_OK, start());
    sync_clock();
    grpc_fake_reply r;
    r.body = {0xFF, 0xFF, 0xFF, 0xFF};
    grpc_fake_push_reply(ACCESS_RPC, r);
    publish_card();
    assert_denied(wait_decision(), "decode_error");
}

#if PORTUNUS_HMAC_ENABLED

/* SC-01: HMAC on with an empty secret refuses to start: no client, no
 * subscriptions, nothing answered. */
void test_empty_secret_refused(void) {
    TEST_ASSERT_EQUAL(PORTUNUS_FAIL, start(""));
    TEST_ASSERT_EQUAL_size_t(0, event_bus_fake_subscriber_count());
    TEST_ASSERT_EQUAL_INT(0, grpc_fake_live_clients());
    publish_card();
    settle();
    TEST_ASSERT_EQUAL_size_t(0, grpc_fake_call_count());
}

/* SC-10: a grant without a signature, or with one that does not verify, is
 * denied with the signature reason. */
void test_bad_signature_denies(void) {
    const struct { Sig sig; const char *reason; } cases[] = {
        {Sig::MISSING, "missing_response_sig"},
        {Sig::WRONG,   "invalid_response_sig"},
    };
    for (const auto &c : cases) {
        setUp();
        TEST_ASSERT_EQUAL(PORTUNUS_OK, start());
        sync_clock();
        grpc_fake_push_responder(ACCESS_RPC, access_responder(true, "credential_allowed", c.sig));
        publish_card();
        assert_denied(wait_decision(), c.reason);
        tearDown();
    }
}

/* SC-11: a signed grant is published as GRANTED with the log fingerprint and
 * the server's reason; a signed deny keeps the server's reason. */
void test_signed_decisions(void) {
    TEST_ASSERT_EQUAL(PORTUNUS_OK, start());
    sync_clock();
    grpc_fake_push_responder(ACCESS_RPC, access_responder(true, "credential_allowed", Sig::VALID));
    publish_card();
    const portunus_event_t e = wait_decision();
    TEST_ASSERT_EQUAL(EVENT_ACCESS_GRANTED, e.id);
    TEST_ASSERT_TRUE(e.payload.access_decision.granted);
    TEST_ASSERT_TRUE(e.payload.access_decision.known);
    TEST_ASSERT_EQUAL_STRING("credential_allowed", e.payload.access_decision.reason);
    TEST_ASSERT_EQUAL_STRING(card_log_id().c_str(), e.payload.access_decision.credential_id);

    tearDown();
    setUp();
    TEST_ASSERT_EQUAL(PORTUNUS_OK, start());
    sync_clock();
    grpc_fake_push_responder(ACCESS_RPC, access_responder(false, "credential_not_found", Sig::VALID));
    publish_card();
    const portunus_event_t d = wait_decision();
    TEST_ASSERT_EQUAL(EVENT_ACCESS_DENIED, d.id);
    TEST_ASSERT_EQUAL_STRING("credential_not_found", d.payload.access_decision.reason);
}

/* SC-14: requested_at is empty while the clock has never synced (the forced
 * pre-access heartbeat failed) and populated once it has. */
void test_requested_at_needs_sync(void) {
    TEST_ASSERT_EQUAL(PORTUNUS_OK, start());
    grpc_fake_push_responder(ACCESS_RPC, access_responder(false, "denied", Sig::VALID));
    publish_card();                                        /* pre-access heartbeat fails */
    wait_decision();
    auto access = calls_to(ACCESS_RPC);
    TEST_ASSERT_EQUAL_size_t(1, access.size());
    TEST_ASSERT_EQUAL_STRING("", decode_access(access[0]).requested_at);
    TEST_ASSERT_EQUAL_size_t(1, calls_to(HEARTBEAT_RPC).size());

    tearDown();
    setUp();
    TEST_ASSERT_EQUAL(PORTUNUS_OK, start());
    sync_clock();
    grpc_fake_push_responder(ACCESS_RPC, access_responder(false, "denied", Sig::VALID));
    publish_card();
    wait_decision();
    access = calls_to(ACCESS_RPC);
    TEST_ASSERT_EQUAL_size_t(1, access.size());
    const portunus_v1_AccessRequest req = decode_access(access[0]);
    struct timeval tv;
    TEST_ASSERT_TRUE_MESSAGE(proto_parse_server_time(req.requested_at, &tv), req.requested_at);
}

/* PROTO-19: the firmware's mbedTLS HMAC on a real request equals the HMAC
 * column of proto/testdata/hmac_vectors.txt, which the Go server's tests
 * also check. */
void test_request_hmac_matches_shared_vectors(void) {
    FILE *f = fopen(PORTUNUS_HMAC_VECTORS, "r");
    TEST_ASSERT_NOT_NULL_MESSAGE(f, PORTUNUS_HMAC_VECTORS);
    char line[512], key[128] = "", want[65] = "";
    while (fgets(line, sizeof line, f) != nullptr) {
        char kind[32], mod[64], seq[16], proj[128], sig[65];
        if (sscanf(line, "key %127s", key) == 1) continue;
        if (sscanf(line, "%31s %63s %15s %127s %64s", kind, mod, seq, proj, sig) == 5 &&
            strcmp(kind, "heartbeat") == 0 && strcmp(mod, MODULE) == 0 && strcmp(seq, "0") == 0) {
            snprintf(want, sizeof want, "%s", sig);
        }
    }
    fclose(f);
    TEST_ASSERT_EQUAL_STRING(SECRET, key);
    TEST_ASSERT_EQUAL_size_t(64, strlen(want));

    TEST_ASSERT_EQUAL(PORTUNUS_OK, start(key));
    publish_heartbeat(0);
    TEST_ASSERT_TRUE(wait_for([] { return grpc_fake_call_count() >= 1; }));
    grpc_fake_call c;
    TEST_ASSERT_TRUE(grpc_fake_get_call(0, &c));
    TEST_ASSERT_EQUAL_STRING(want, c.sig.c_str());
}

#else  /* !PORTUNUS_HMAC_ENABLED */

/* SC-12: without HMAC (dev only) a grant needs no signature, and requests
 * carry none: such doors trust TLS alone. */
void test_no_hmac_grant_trusts_tls(void) {
    TEST_ASSERT_EQUAL(PORTUNUS_OK, start(""));
    sync_clock();
    grpc_fake_push_responder(ACCESS_RPC, access_responder(true, "credential_allowed", Sig::MISSING));
    publish_card();
    const portunus_event_t e = wait_decision();
    TEST_ASSERT_EQUAL(EVENT_ACCESS_GRANTED, e.id);
    for (const auto &c : calls_to(ACCESS_RPC)) TEST_ASSERT_EQUAL_STRING("", c.sig.c_str());
}

#endif /* PORTUNUS_HMAC_ENABLED */
#endif /* CONFIG_PORTUNUS_MODULE_TYPE_ACCESS_POINT */

/* ── Provisioning console ─────────────────────────────────────────────────── */

#ifdef CONFIG_PORTUNUS_MODULE_TYPE_PROVISIONING_CONSOLE

static void publish_provision_request(void) {
    portunus_event_t e;
    memset(&e, 0, sizeof e);
    e.id = EVENT_PROVISION_REQUEST;
    memcpy(e.payload.provision_request.credential_uid, CARD.uid, CARD.uid_len);
    e.payload.provision_request.credential_uid_len = CARD.uid_len;
    event_bus_publish(&e);
}

static portunus_event_t wait_provision_result(void) {
    TEST_ASSERT_TRUE_MESSAGE(wait_for([] {
        return event_bus_fake_count_of(EVENT_PROVISION_SUCCESS) +
               event_bus_fake_count_of(EVENT_PROVISION_FAILED) >= 1;
    }), "no provision result published");
    portunus_event_t e;
    for (size_t i = 0; event_bus_fake_copy(i, &e); ++i) {
        if (e.id == EVENT_PROVISION_SUCCESS || e.id == EVENT_PROVISION_FAILED) return e;
    }
    TEST_FAIL();
    return e;
}

/* SC-06 (F3): offline, a provision request is answered at once with
 * COMM_ERROR instead of leaving the console to time out. */
void test_offline_provision_fails_fast(void) {
    TEST_ASSERT_EQUAL(PORTUNUS_OK, start());
    wifi_mgr_fake_set_connected(false);
    publish_provision_request();
    const portunus_event_t e = wait_provision_result();
    TEST_ASSERT_EQUAL(EVENT_PROVISION_FAILED, e.id);
    TEST_ASSERT_EQUAL(PROVISION_RESULT_COMM_ERROR, e.payload.provision_result.reason);
    TEST_ASSERT_EQUAL_size_t(0, grpc_fake_call_count());
}

/* SC-15 (D-T5): with the comm task stuck and its queue full, a further
 * provision request fails at once with COMM_ERROR instead of leaving the
 * console to wait out its capture timeout. */
void test_full_queue_fails_provision_fast(void) {
    TEST_ASSERT_EQUAL(PORTUNUS_OK, start());
    grpc_fake_hold();
    publish_provision_request();
    TEST_ASSERT_TRUE(grpc_fake_wait_held_call(2000));
    for (int i = 0; i < 8; ++i) publish_provision_request();
    TEST_ASSERT_EQUAL_size_t(0, event_bus_fake_count_of(EVENT_PROVISION_FAILED));
    publish_provision_request();
    TEST_ASSERT_EQUAL_size_t(1, event_bus_fake_count_of(EVENT_PROVISION_FAILED));
    portunus_event_t e;
    for (size_t i = 0; event_bus_fake_copy(i, &e); ++i) {
        if (e.id == EVENT_PROVISION_FAILED) break;
    }
    TEST_ASSERT_EQUAL(PROVISION_RESULT_COMM_ERROR, e.payload.provision_result.reason);
    TEST_ASSERT_EQUAL_STRING("comm_busy", e.payload.provision_result.detail);
    TEST_ASSERT_EQUAL_UINT32(1, server_comm_dropped_events());

    grpc_fake_release();
    TEST_ASSERT_TRUE(wait_for([] {
        return event_bus_fake_count_of(EVENT_PROVISION_FAILED) >= 1 + 9;
    }, 5000));
}

/* The provision path end to end: a PENDING_CREATED reply succeeds; a
 * transport failure is a COMM_ERROR. */
void test_provision_round_trip(void) {
    TEST_ASSERT_EQUAL(PORTUNUS_OK, start());
    portunus_v1_ProvisionCredentialResponse r = portunus_v1_ProvisionCredentialResponse_init_zero;
    r.status = portunus_v1_ProvisionStatus_PROVISION_STATUS_PENDING_CREATED;
    snprintf(r.member_uuid, sizeof r.member_uuid, "%s", "11111111-2222-3333-4444-555555555555");
    grpc_fake_reply reply;
    reply.body = encode(portunus_v1_ProvisionCredentialResponse_fields, r);
    grpc_fake_push_reply(PROVISION_RPC, reply);
    publish_provision_request();
    portunus_event_t e = wait_provision_result();
    TEST_ASSERT_EQUAL(EVENT_PROVISION_SUCCESS, e.id);
    TEST_ASSERT_EQUAL(PROVISION_RESULT_PENDING_CREATED, e.payload.provision_result.reason);
    TEST_ASSERT_EQUAL_STRING("11111111-2222-3333-4444-555555555555",
                             e.payload.provision_result.member_uuid);
    grpc_fake_call c;
    TEST_ASSERT_TRUE(grpc_fake_get_call(0, &c));
    TEST_ASSERT_EQUAL_STRING(hmac_hex("provision|door-001|04a32b1c").c_str(), c.sig.c_str());

    tearDown();
    setUp();
    TEST_ASSERT_EQUAL(PORTUNUS_OK, start());
    publish_provision_request();                           /* nothing scripted */
    e = wait_provision_result();
    TEST_ASSERT_EQUAL(EVENT_PROVISION_FAILED, e.id);
    TEST_ASSERT_EQUAL(PROVISION_RESULT_COMM_ERROR, e.payload.provision_result.reason);
}

#endif /* CONFIG_PORTUNUS_MODULE_TYPE_PROVISIONING_CONSOLE */

extern "C" void app_main(void) {
    UNITY_BEGIN();
    RUN_TEST(test_second_init_refused);
    RUN_TEST(test_subscriptions);
    RUN_TEST(test_offline_heartbeat_is_dropped);
    RUN_TEST(test_heartbeat_syncs_clock);
    RUN_TEST(test_rpc_paths_match_proto);
#ifdef CONFIG_PORTUNUS_MODULE_TYPE_ACCESS_POINT
    RUN_TEST(test_offline_card_denied);
    RUN_TEST(test_full_queue_denies_card);
    RUN_TEST(test_transport_failure_denies);
    RUN_TEST(test_bad_status_denies);
    RUN_TEST(test_undecodable_body_denies);
#if PORTUNUS_HMAC_ENABLED
    RUN_TEST(test_empty_secret_refused);
    RUN_TEST(test_bad_signature_denies);
    RUN_TEST(test_signed_decisions);
    RUN_TEST(test_requested_at_needs_sync);
    RUN_TEST(test_request_hmac_matches_shared_vectors);
#else
    RUN_TEST(test_no_hmac_grant_trusts_tls);
#endif
#endif
#ifdef CONFIG_PORTUNUS_MODULE_TYPE_PROVISIONING_CONSOLE
    RUN_TEST(test_offline_provision_fails_fast);
    RUN_TEST(test_full_queue_fails_provision_fast);
    RUN_TEST(test_provision_round_trip);
#endif
    exit(UNITY_END());
}
