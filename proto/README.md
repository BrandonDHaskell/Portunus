# Portunus Protocol Buffers

This directory contains the shared `.proto` contract between the **Portunus
server** (Go) and the **ESP32 access module** (C / Nanopb).  Both sides
generate code from the same `.proto` file to keep the wire format in sync.

## Directory layout

```
proto/
├── portunus/v1/
│   └── portunus.proto        ← message definitions (source of truth)
├── nanopb/
│   └── portunus.options      ← fixed-size string constraints for ESP32
└── README.md                 ← you are here
```

## Wire format

The access module sends **Nanopb-encoded protobuf** over **HTTP/1.1 POST**
with `Content-Type: application/x-protobuf`.  This is *not* gRPC — there is
no HTTP/2 framing or `service` block.  The same endpoint paths are used
(`/v1/heartbeat`, `/v1/access_request`); the server distinguishes the format
by `Content-Type`.

## Code generation

### Prerequisites

| Tool | Version | Install |
|------|---------|---------|
| `protoc` | ≥ 3.21 | [github.com/protocolbuffers/protobuf/releases](https://github.com/protocolbuffers/protobuf/releases) |
| `protoc-gen-go` | ≥ 1.28 | `go install google.golang.org/protobuf/cmd/protoc-gen-go@latest` |
| `nanopb` generator | ≥ 0.4.8 | `pip install nanopb` or ESP-IDF managed component |

### Go (server)

Run from the **project root** (`Portunus/`):

```bash
protoc \
  --proto_path=proto \
  --go_out=server/api --go_opt=paths=source_relative \
  proto/portunus/v1/portunus.proto
```

This produces:

```
server/api/portunusv1/portunus.pb.go
```

Add the generated package to the server's imports as needed.

### C / Nanopb (access module)

Run from the **project root** (`Portunus/`):

```bash
# Using the nanopb_generator installed via pip
python -m grpc_tools.protoc \
  --proto_path=proto \
  --nanopb_out="--options-path=proto/nanopb:access_module/components/proto" \
  proto/portunus/v1/portunus.proto
```

Or, if using the standalone `nanopb_generator.py`:

```bash
nanopb_generator \
  -I proto \
  -D access_module/components/proto \
  -f proto/nanopb/portunus.options \
  proto/portunus/v1/portunus.proto
```

Either command produces:

```
access_module/components/proto/portunus/v1/portunus.pb.c
access_module/components/proto/portunus/v1/portunus.pb.h
```

Flatten or relocate the files into the component's `include/` and source
root as needed for your `CMakeLists.txt`.

### Verifying a roundtrip

A quick way to confirm both sides agree on the encoding:

1. On the host, write a small Go program that encodes a `HeartbeatRequest`
   and writes the raw bytes to a file.
2. Compile the Nanopb `.pb.c` on the host (Nanopb is plain C — no ESP-IDF
   required) and decode the same file.
3. Assert field equality.

## Compatibility rules

1. **Only append fields** — never reorder or reuse a field number.
2. **Reserve removed fields** — use `reserved 7;` so the number is never
   reused.
3. **Every new string field needs a `max_size`** entry in
   `nanopb/portunus.options`.  Without it, Nanopb falls back to dynamic
   allocation, which is not suitable for the ESP32.
4. **`optional` means the sender may omit the field** — the ESP32 leaves
   optional fields unset when the corresponding hardware (e.g. reed switch)
   is not present.
5. **Test both sides after any change** — regenerate Go and Nanopb code,
   run the roundtrip check, then build both projects.

## Message ↔ existing type mapping

| Proto message | Server Go type (`internal/portunus/types`) | ESP32 notes |
|---|---|---|
| `HeartbeatRequest` | `types.HeartbeatRequest` | Built by `server_comm` from `event_heartbeat_t` + device config |
| `HeartbeatResponse` | `types.HeartbeatResponse` | Decoded by `server_comm`; logged |
| `AccessRequest` | `types.AccessRequest` | Built by `server_comm` from `credential_t` UID + device config |
| `AccessResponse` | `types.AccessResponse` | Decoded by `server_comm`; drives `EVENT_ACCESS_GRANTED/DENIED` |

### Fields added beyond the current Go types

`HeartbeatRequest` includes two ESP32 telemetry fields that the server does
not yet store:

| Field | Proto # | Source on ESP32 |
|---|---|---|
| `free_heap_bytes` | 7 | `esp_get_free_heap_size()` |
| `sequence` | 8 | Monotonic counter in `heartbeat_service` |

These are zero-cost for the server to ignore (proto3 skips unknown fields
by default).  When the server is ready to persist them, add the
corresponding fields to `types.HeartbeatRequest` and regenerate the Go
stubs.