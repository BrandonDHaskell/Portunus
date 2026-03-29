# Portunus Protocol Buffers

This directory contains the shared Protocol Buffers contract for the current
Portunus system.

The `.proto` file is the single source of truth for the message schema used
between the **Go server** and the **ESP32 access module**. In the current
project, that contract is used in two ways:

1. **HTTP/1.1 + TLS + protobuf**
   - The firmware encodes messages with **Nanopb**.
   - The server accepts `application/x-protobuf` on the device endpoints.
   - Requests are protected with the `X-Portunus-Sig` HMAC header when HMAC is
     enabled on the server.

2. **gRPC over HTTP/2 + TLS**
   - The `.proto` includes the canonical `PortunusService` RPC service.
   - The server exposes an optional gRPC listener.
   - The firmware can use the lightweight `grpc_client` service when built with
     `CONFIG_PORTUNUS_USE_GRPC=y`.

The important point is that **both transport paths use the same protobuf
messages**. That keeps the server and firmware aligned even though the project
currently supports both a direct HTTP/protobuf path and an optional gRPC path.

## Directory layout

```text
proto/
├── nanopb/
│   └── portunus.options
├── portunus/
│   └── v1/
│       └── portunus.proto
└── README.md
```

### What each file does

- `portunus/v1/portunus.proto`
  - Defines the Portunus wire contract.
  - Contains both the message types and the `PortunusService` RPC definition.
- `nanopb/portunus.options`
  - Provides fixed-size string limits for Nanopb so the ESP32 firmware can use
    static buffers instead of dynamic allocation.

## Current contract

The current schema defines four messages and one service:

### Messages

- `HeartbeatRequest`
- `HeartbeatResponse`
- `AccessRequest`
- `AccessResponse`

### Service

- `PortunusService`
  - `SendHeartbeat`
  - `RequestAccess`

These RPCs are the canonical service contract even when the firmware uses the
HTTP/protobuf fallback.

## How the current codebase uses this contract

### Server

The active Go package used by the server lives under:

```text
server/api/portunus/v1/
```

The server imports that package from code such as:

- `server/cmd/portunus-server/main.go`
- `server/internal/httpapi/convert.go`
- `server/internal/httpapi/server.go`
- `server/internal/grpcapi/server.go`

In the current implementation:

- the **HTTP API** accepts protobuf requests on:
  - `POST /v1/heartbeat`
  - `POST /v1/access_request`
- the **gRPC API** optionally exposes:
  - `/portunus.v1.PortunusService/SendHeartbeat`
  - `/portunus.v1.PortunusService/RequestAccess`

### Access module

The firmware uses the Nanopb-generated C sources committed under:

```text
access_module/components/portunus_proto/portunus/v1/
```

That component is consumed by the access module services that serialize and
parse device traffic:

- `services/server_comm/` for HTTP/protobuf and transport selection
- `services/grpc_client/` for the gRPC transport implementation

In the current firmware:

- heartbeat events are encoded as `HeartbeatRequest`
- credential reads are encoded as `AccessRequest`
- server replies are decoded as `HeartbeatResponse` or `AccessResponse`

## Current transport behavior

### HTTP/protobuf path

When gRPC is **not** enabled in firmware, the access module sends Nanopb-
encoded protobuf payloads over HTTPS using the device routes documented above.

Current behavior:

- request body: raw protobuf bytes
- content type: `application/x-protobuf`
- authentication: `X-Portunus-Sig` HMAC header when enabled
- response body: raw protobuf bytes

This path is implemented by the firmware `server_comm` service and the server's
HTTP handlers.

### gRPC path

When `CONFIG_PORTUNUS_USE_GRPC=y`, the access module uses the `grpc_client`
service to call the RPC methods defined in `portunus.proto` over HTTP/2.

Current behavior:

- transport: gRPC unary calls over HTTP/2 + TLS
- service: `portunus.v1.PortunusService`
- methods:
  - `SendHeartbeat`
  - `RequestAccess`
- request/response payloads: the same protobuf message types used by the
  HTTP/protobuf path

The server's gRPC implementation lives in `server/internal/grpcapi/`.

## Generated code locations in this repo

The current repo snapshot includes generated code in these locations:

### Go

```text
server/api/portunus/v1/portunus.pb.go
server/api/portunus/v1/portunus_grpc.pb.go
```

### Nanopb

```text
access_module/components/portunus_proto/portunus/v1/portunus.pb.c
access_module/components/portunus_proto/portunus/v1/portunus.pb.h
```

Those files are part of the active implementation and should be treated as
build artifacts derived from `proto/portunus/v1/portunus.proto`.

## Regenerating protobuf code

From the project root, the repo currently provides Taskfile commands for proto
regeneration:

```bash
task proto:gen
task proto:gen:go
task proto:gen:nanopb
task proto:check
```

The helper script used by those commands lives at:

```text
scripts/proto_gen.py
```

Before changing the schema, make sure your local environment has:

- `protoc`
- `protoc-gen-go`
- `protoc-gen-go-grpc`
- a Nanopb generator (`nanopb_generator` or `grpc_tools.protoc` with Nanopb)

## Nanopb sizing rules

The ESP32 firmware relies on `proto/nanopb/portunus.options` to keep protobuf
string fields bounded and heap-friendly.

If you add a new string field to `portunus.proto`, also add a corresponding
`max_size` entry in `portunus.options`.

Without that, Nanopb may generate code that expects dynamic allocation, which
is not the intended model for the access module.

## Message-to-runtime mapping

### Heartbeat

`HeartbeatRequest` currently carries:

- `module_id`
- `firmware_version`
- `uptime_s`
- optional `door_closed`
- optional `rssi_dbm`
- `ip`
- `free_heap_bytes`
- `sequence`

The current Go server converts that protobuf message into
`server/internal/portunus/types.HeartbeatRequest` and persists the heartbeat
through the heartbeat service/store path.

### Access

`AccessRequest` currently carries:

- `module_id`
- `card_id`
- optional `door_closed`
- optional `requested_at`

The current Go server converts that protobuf message into
`server/internal/portunus/types.AccessRequest` and evaluates it through the
access service.

## Compatibility rules

When updating the schema, follow these rules:

1. **Only append fields.** Never reorder or reuse an existing field number.
2. **Reserve removed fields.** If a field is retired, reserve its number.
3. **Keep Nanopb in mind.** New string fields need a `max_size` entry.
4. **Preserve optional semantics.** Some device-side values may be absent,
   such as reed-switch state or local timestamps.
5. **Regenerate both sides together.** The server and firmware must advance in
   lockstep whenever the contract changes.

## Practical guidance for contributors

If you are changing `portunus.proto`, think of it as changing the contract for
all of the following at once:

- the server HTTP protobuf endpoints
- the server gRPC API
- the firmware HTTP/protobuf path
- the firmware gRPC path
- the Nanopb memory layout used on the ESP32

That makes this directory one of the most sensitive integration points in the
Portunus project. Change it deliberately, regenerate the bindings, and verify
both the server and access module still build and speak the same contract.