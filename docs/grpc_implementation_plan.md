# Portunus — gRPC Implementation Plan

## 1. Current State Analysis

### What Exists Today

The Portunus project has a clean separation between the **server** (Go) and the **access_module** (ESP32-S3, C++ on ESP-IDF). Communication currently works over **HTTP/1.1** with Nanopb-encoded protobuf payloads:

| Layer | Server | Access Module |
|-------|--------|---------------|
| Transport | Go `net/http` (HTTP/1.1 + optional TLS) | ESP-IDF `esp_http_client` (HTTP/1.1 + optional TLS) |
| Encoding | `google.golang.org/protobuf` | Nanopb (C, code-generated from same `.proto`) |
| Auth | HMAC-SHA256 via `X-Portunus-Sig` header | HMAC-SHA256 via mbedTLS |
| Endpoints | `POST /v1/heartbeat`, `POST /v1/access_request` | Calls above via `http_post_proto()` |

The `.proto` file already defines a `PortunusService` with two RPCs (`SendHeartbeat`, `RequestAccess`), but the proto comment explicitly notes:

> *"The ESP32 does not support HTTP/2, so it cannot speak native gRPC."*

The Admin API (`/admin/v1/*`) is JSON-only over HTTP/1.1 and is consumed by human operators / CLI tools — it is **not** part of the ESP32 communication path.

### What the Project Plan States

From `project_plan.md` §2.1:

> *"REST APIs are currently operational for heartbeat and card reader data; gRPC will be adopted as the target protocol."*

This confirms the intent to migrate the module↔server transport from HTTP/1.1+protobuf to proper gRPC.

---

## 2. The ESP32 and gRPC: What Espressif Supports

### The Core Problem

**gRPC requires HTTP/2.** The standard ESP-IDF `esp_http_client` only speaks HTTP/1.1. There is no first-party `esp_grpc_client` component from Espressif. However, the building blocks exist:

### Available ESP-IDF Building Blocks

1. **nghttp2** — Espressif publishes an official `espressif/nghttp` component on the IDF Component Registry (latest v1.65.0). This is a C implementation of the HTTP/2 framing layer. It is the same library that ESP-IDF's internal `sh2lib` helper wraps.

2. **esp-tls** — ESP-IDF's transport-layer-security abstraction (wraps mbedTLS). Provides the TLS channel that HTTP/2 (and gRPC) requires. Already used by the current `esp_http_client` when `PORTUNUS_USE_TLS=1`.

3. **sh2lib** — A thin ESP-IDF helper library (found in older ESP-IDF examples at `examples/protocols/http2_request/components/sh2lib/`) that wraps nghttp2 + esp-tls into a simpler API for making HTTP/2 requests. Espressif used it in the ESP Voice Assistant SDK for Dialogflow gRPC calls.

4. **Nanopb** — Already in use in the Portunus access_module for protobuf encoding/decoding.

### How gRPC Actually Works on ESP32

The community-proven approach (used in `chrisomatic/esp-grpc` and Espressif's own Voice Assistant SDK) is to manually implement the gRPC wire protocol:

```
┌──────────────────────────────────────────────────────────┐
│                    gRPC Wire Format                       │
│                                                          │
│  HTTP/2 HEADERS frame:                                   │
│    :method     = POST                                    │
│    :scheme     = https                                   │
│    :path       = /portunus.v1.PortunusService/...        │
│    content-type = application/grpc                        │
│    te          = trailers                                │
│                                                          │
│  HTTP/2 DATA frame:                                      │
│    ┌─────────┬──────────────┬────────────────────────┐   │
│    │ 1 byte  │   4 bytes    │     N bytes             │   │
│    │ compress│ msg length   │ protobuf payload        │   │
│    │ (0x00)  │ (big-endian) │ (Nanopb-encoded)        │   │
│    └─────────┴──────────────┴────────────────────────┘   │
│                                                          │
│  HTTP/2 HEADERS frame (trailers):                        │
│    grpc-status  = 0                                      │
│    grpc-message = (optional)                             │
└──────────────────────────────────────────────────────────┘
```

The gRPC protocol is just protobuf messages with a 5-byte length-prefix, sent inside HTTP/2 DATA frames with specific pseudo-headers and trailers. The ESP32 already has everything it needs: TLS, HTTP/2 framing (nghttp2), and protobuf (Nanopb).

---

## 3. Recommended Implementation Strategy

### Architecture: gRPC Transport Layer on ESP32

Replace the current `http_post_proto()` function with a new `grpc_client` component that speaks the gRPC wire protocol over HTTP/2. The event bus architecture, Nanopb encoding, HMAC signing, and TLS configuration all remain unchanged.

```
BEFORE (current):
  event_bus → server_comm → esp_http_client (HTTP/1.1) → server

AFTER (target):
  event_bus → server_comm → grpc_client (HTTP/2 via nghttp2) → server
```

### Component Breakdown

#### A. New Component: `grpc_client`

Location: `access_module/services/grpc_client/`

This component encapsulates the nghttp2 + esp-tls HTTP/2 connection and the gRPC wire format. It exposes a simple API that `server_comm` calls instead of `esp_http_client`.

**Public API (`grpc_client.h`):**

```c
typedef struct {
    const char *host;           // Server hostname/IP
    uint16_t    port;           // Server port (e.g. 443 or 8443)
    const char *ca_cert_pem;    // CA certificate for TLS (NULL to use bundle)
    bool        skip_verify;    // Dev mode: skip cert check
    int         timeout_ms;     // Connect/request timeout
} grpc_client_config_t;

typedef struct grpc_client *grpc_client_handle_t;

// Lifecycle
portunus_err_t grpc_client_init(const grpc_client_config_t *cfg,
                                 grpc_client_handle_t *out_handle);
void           grpc_client_destroy(grpc_client_handle_t handle);

// Unary RPC call (covers both Portunus RPCs)
// service_method: e.g. "/portunus.v1.PortunusService/SendHeartbeat"
// req_buf/req_len: Nanopb-encoded protobuf request (no gRPC prefix)
// resp_buf/resp_cap: buffer for the protobuf response (prefix stripped)
// resp_len: actual response protobuf length
// grpc_status: gRPC status code (0 = OK)
portunus_err_t grpc_client_unary_call(grpc_client_handle_t handle,
                                       const char *service_method,
                                       const uint8_t *req_buf, size_t req_len,
                                       uint8_t *resp_buf, size_t resp_cap,
                                       int *resp_len, int *grpc_status);
```

**Internal implementation sketch:**

```c
// 1. Open TLS connection via esp_tls_conn_new()
// 2. Create nghttp2 session (nghttp2_session_client_new)
// 3. Send HTTP/2 SETTINGS, receive server SETTINGS
// 4. For each unary call:
//    a. Build gRPC DATA payload: [0x00][4-byte length BE][protobuf bytes]
//    b. Submit HTTP/2 request via nghttp2_submit_request() with headers:
//       :method=POST, :path=<service_method>, :scheme=https,
//       content-type=application/grpc, te=trailers,
//       x-portunus-sig=<hmac>  (custom header, preserved for auth)
//    c. Pump nghttp2 send/recv until stream completes
//    d. Parse trailers for grpc-status
//    e. Strip 5-byte gRPC prefix from response DATA, return protobuf
// 5. Optionally keep connection alive for multiple RPCs
```

**Key dependencies:** `nghttp2` (via IDF component manager), `esp-tls`, `mbedtls`, `freertos`.

#### B. Modifications to `server_comm`

The changes to `server_comm.cpp` are minimal:

1. Replace `#include "esp_http_client.h"` with `#include "grpc_client.h"`.

2. Replace `http_post_proto()` calls with `grpc_client_unary_call()`:

```c
// BEFORE:
portunus_err_t err = http_post_proto(s_heartbeat_url,
                                      req_buf, ostream.bytes_written,
                                      resp_buf, sizeof(resp_buf),
                                      &resp_len, &status);
if (err != PORTUNUS_OK || status != 200) { /* handle error */ }

// AFTER:
int grpc_status = 0;
portunus_err_t err = grpc_client_unary_call(
    s_grpc_handle,
    "/portunus.v1.PortunusService/SendHeartbeat",
    req_buf, ostream.bytes_written,
    resp_buf, sizeof(resp_buf),
    &resp_len, &grpc_status);
if (err != PORTUNUS_OK || grpc_status != 0) { /* handle error */ }
```

3. In `server_comm_init()`, create a `grpc_client_handle_t` instead of building URL strings:

```c
grpc_client_config_t grpc_cfg = {
    .host       = PORTUNUS_SERVER_HOST,
    .port       = PORTUNUS_GRPC_SERVER_PORT,
    .ca_cert_pem = PORTUNUS_TLS_USE_CUSTOM_CA ? ca_cert_pem_start : NULL,
    .skip_verify = PORTUNUS_TLS_SKIP_VERIFY,
    .timeout_ms  = PORTUNUS_SERVER_REQUEST_TIMEOUT_MS,
};
grpc_client_init(&grpc_cfg, &s_grpc_handle);
```

4. The HMAC signature can either be kept as a custom gRPC metadata header (simplest) or replaced by gRPC's built-in per-RPC credentials mechanism. Keeping it as a custom header is recommended initially since the server already validates `X-Portunus-Sig`.

#### C. Server-Side: Add gRPC Listener (Go)

The Go server needs to accept gRPC connections. The recommended approach:

**Option 1 — Separate gRPC port (simplest):**

Add a dedicated gRPC listener alongside the existing HTTP server. The existing HTTP/1.1+protobuf and JSON endpoints remain for backward compatibility and admin API use.

```go
// In main.go, after existing HTTP setup:
import "google.golang.org/grpc"

grpcServer := grpc.NewServer(
    grpc.UnaryInterceptor(hmacGRPCInterceptor(cfg.HMACSecret)),
)
pb.RegisterPortunusServiceServer(grpcServer, &portunusGRPCHandler{
    heartbeatSvc: heartbeatSvc,
    accessSvc:    accessSvc,
})

lis, _ := net.Listen("tcp", cfg.GRPCAddr) // e.g. :50051
go grpcServer.Serve(lis)
```

**Option 2 — cmux on same port (advanced):**

Use `soheilhy/cmux` to multiplex HTTP/1.1 and HTTP/2 (gRPC) on the same port. The muxer inspects the first bytes of each connection to route it.

**Recommendation:** Start with Option 1 (separate port). It requires minimal changes to the existing server and avoids multiplexer complexity. The Admin API stays on the HTTP port; the ESP32 modules connect to the gRPC port.

**New Go files needed:**

1. `server/internal/grpcapi/server.go` — gRPC service implementation that delegates to the existing service layer.
2. `server/api/portunus/v1/portunus_grpc.pb.go` — Generated by `protoc-gen-go-grpc` from the existing `.proto`.
3. HMAC interceptor (unary server interceptor) that reads `x-portunus-sig` from gRPC metadata.

#### D. Kconfig Changes

Add new menu items under "Network Configuration":

```kconfig
config PORTUNUS_USE_GRPC
    bool "Use gRPC (HTTP/2) transport for server communication"
    default y
    depends on PORTUNUS_ENABLE_WIFI && PORTUNUS_USE_TLS
    help
        When enabled, the access module communicates with the server
        using gRPC over HTTP/2+TLS instead of HTTP/1.1+protobuf.
        Requires TLS to be enabled (gRPC mandates encrypted transport).

config PORTUNUS_GRPC_SERVER_PORT
    int "gRPC server port"
    default 50051
    range 1 65535
    depends on PORTUNUS_USE_GRPC
    help
        TCP port the Portunus server listens on for gRPC connections.
```

#### E. IDF Component Manager

Add `espressif/nghttp` to the access_module's dependency manifest:

```yaml
# access_module/components/grpc_client/idf_component.yml
dependencies:
  espressif/nghttp: "^1.65.0"
```

---

## 4. Resource & Feasibility Considerations

### ESP32-S3 Memory Budget

| Resource | HTTP/1.1 (current) | gRPC/HTTP/2 (projected) |
|----------|-------------------|------------------------|
| Stack (comm_task) | 6 KiB | 8–10 KiB (nghttp2 session state) |
| Heap (nghttp2 session) | 0 | ~8–12 KiB |
| Heap (TLS) | ~40 KiB (already allocated) | Same (reused) |
| Flash (nghttp2 code) | 0 | ~30–50 KiB |

The ESP32-S3 WROOM-1 has 512 KiB SRAM and 8 MiB flash (typical). The additional ~60 KiB combined overhead is well within budget. The `COMM_TASK_STACK_SIZE` should increase from 6144 to 8192 or 10240.

### Connection Keep-Alive

A major advantage of HTTP/2 is connection multiplexing. The gRPC client should maintain a persistent connection to the server rather than opening a new TCP+TLS handshake per request. This reduces latency for heartbeat and access requests from ~200–500ms (TCP+TLS setup) to ~5–20ms (reuse existing stream). The `comm_task` would open the connection once on boot (or on first use) and reopen on failure.

### ALPN Negotiation

gRPC over TLS requires ALPN (Application-Layer Protocol Negotiation) to announce `h2` during the TLS handshake. ESP-IDF's `esp-tls` supports ALPN configuration:

```c
esp_tls_cfg_t tls_cfg = {
    .alpn_protos = (const char *[]){"h2", NULL},
    .cacert_buf  = ca_cert_pem_start,
    .cacert_bytes = ca_cert_pem_end - ca_cert_pem_start,
};
```

The Go `grpc.NewServer()` handles ALPN automatically on the server side.

---

## 5. Implementation Phases

### Phase 1: Server-side gRPC (Go) — Low risk, independent

1. Run `protoc --go-grpc_out=...` to generate `portunus_grpc.pb.go` from the existing `.proto`.
2. Create `server/internal/grpcapi/server.go` implementing `PortunusServiceServer` by delegating to existing service layer methods.
3. Add gRPC server startup in `main.go` with a separate port (configurable via `PORTUNUS_GRPC_ADDR`).
4. Port the HMAC validation from HTTP middleware to a gRPC unary interceptor (reads `x-portunus-sig` from `metadata.MD`).
5. Add TLS to the gRPC listener using the same cert/key files.
6. Test with `grpcurl` or a Go test client.

### Phase 2: ESP32 gRPC client component — Core work

1. Add the `espressif/nghttp` dependency to the IDF component manager.
2. Implement `grpc_client.c` with:
   - TLS connection via `esp_tls_conn_new()` with ALPN `"h2"`.
   - nghttp2 session setup (send/recv callbacks wired to esp-tls).
   - gRPC frame construction (5-byte prefix + protobuf).
   - Header construction (`:method`, `:path`, `:scheme`, `content-type`, `te`, `x-portunus-sig`).
   - Response parsing (trailer extraction for `grpc-status`, DATA payload stripping).
   - Connection keep-alive with automatic reconnect.
3. Add Kconfig options for gRPC enable/port.
4. Unit-test the gRPC frame encoding/decoding logic on host (no hardware needed).

### Phase 3: Integration — Wire it together

1. Modify `server_comm.cpp` to call `grpc_client_unary_call()` instead of `http_post_proto()`.
2. Add `#if PORTUNUS_USE_GRPC` / `#else` guards to keep HTTP/1.1 fallback path.
3. Increase `COMM_TASK_STACK_SIZE` to 8192+.
4. Integration test: ESP32 → gRPC server on LAN.
5. Validate HMAC signatures flow correctly through gRPC metadata.

### Phase 4: Cleanup & Documentation

1. Update `docs/architecture.md`, `docs/setup_server.md`, `docs/setup_firmware.md`.
2. Update `proto/portunus/v1/portunus.proto` comments to reflect that ESP32 now uses gRPC.
3. Deprecate (but keep) the HTTP/1.1 protobuf endpoints for backward compatibility.
4. Add `generate_certs.sh` note about ALPN requirements.

---

## 6. Fallback / Dual-Mode Strategy

The `#if PORTUNUS_USE_GRPC` compile-time switch means the firmware can be built in either mode. The server should continue to serve both protocols:

- **HTTP/1.1 + protobuf** on the existing port (`:8080` or `:8443`) — for legacy modules, admin API, and JSON clients.
- **gRPC** on a new port (`:50051`) — for ESP32 modules with gRPC-enabled firmware.

This allows a gradual rollout: flash new firmware module-by-module without disrupting running deployments.

---

## 7. File Change Summary

### New Files

| File | Description |
|------|-------------|
| `access_module/services/grpc_client/CMakeLists.txt` | Component registration, REQUIRES nghttp2 + esp-tls + mbedtls |
| `access_module/services/grpc_client/include/grpc_client.h` | Public API (init, destroy, unary_call) |
| `access_module/services/grpc_client/src/grpc_client.c` | nghttp2 + gRPC wire format implementation |
| `access_module/services/grpc_client/idf_component.yml` | Dependency on `espressif/nghttp` |
| `server/internal/grpcapi/server.go` | gRPC service handler (delegates to existing services) |
| `server/internal/grpcapi/interceptors.go` | HMAC + logging gRPC interceptors |
| `server/api/portunus/v1/portunus_grpc.pb.go` | Generated gRPC Go stubs |

### Modified Files

| File | Change |
|------|--------|
| `access_module/services/server_comm/src/server_comm.cpp` | Add `#if PORTUNUS_USE_GRPC` path calling `grpc_client` |
| `access_module/services/server_comm/CMakeLists.txt` | Add conditional REQUIRES on `grpc_client` |
| `access_module/main/Kconfig.projbuild` | Add `PORTUNUS_USE_GRPC` and `PORTUNUS_GRPC_SERVER_PORT` |
| `access_module/components/portunus_config/include/network_config.h` | Add `PORTUNUS_GRPC_SERVER_PORT` define |
| `server/cmd/portunus-server/main.go` | Add gRPC server startup alongside HTTP |
| `server/internal/config/config.go` | Add `GRPCAddr` config field |
| `server/go.mod` | Add `google.golang.org/grpc` dependency |
| `proto/portunus/v1/portunus.proto` | Update comments (ESP32 now supports gRPC) |

---

## 8. Testing Strategy

1. **Unit (host-side):** Test gRPC 5-byte frame encoding/decoding, HMAC computation, header construction — no hardware needed.
2. **Integration (local):** Run Go gRPC server on dev machine, ESP32 on LAN. Verify heartbeat and access flows end-to-end.
3. **Backward compatibility:** Confirm HTTP/1.1 path still works when `PORTUNUS_USE_GRPC=n`.
4. **Failure modes:** Test TLS cert expiry, server unreachable, HMAC mismatch, gRPC status codes (UNAVAILABLE, UNAUTHENTICATED, INTERNAL).
5. **Load:** Sustained heartbeats every 10s + card reads under load — verify no memory leaks from nghttp2 session management.