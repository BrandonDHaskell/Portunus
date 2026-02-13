package httpapi

import (
	"io"
	"net/http"

	"google.golang.org/protobuf/proto"
)

// maxProtoBody caps the request body size for protobuf payloads.
// The largest ESP32 message (HeartbeatRequest) encodes to ~142 bytes,
// so 4 KiB is generous.
const maxProtoBody = 4096

// isProtobuf returns true if the request's Content-Type indicates a
// protobuf payload. The ESP32 sends "application/x-protobuf".
func isProtobuf(r *http.Request) bool {
	ct := r.Header.Get("Content-Type")
	return ct == "application/x-protobuf" ||
		ct == "application/protobuf" ||
		ct == "application/octet-stream"
}

// readProto reads the request body and unmarshals it into msg.
func readProto(r *http.Request, msg proto.Message) error {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxProtoBody))
	if err != nil {
		return err
	}
	return proto.Unmarshal(body, msg)
}

// writeProto marshals msg and writes it with the given HTTP status.
func writeProto(w http.ResponseWriter, status int, msg proto.Message) {
	data, err := proto.Marshal(msg)
	if err != nil {
		// Fall back to a plain-text error if marshalling fails.
		http.Error(w, "proto marshal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/x-protobuf")
	w.WriteHeader(status)
	_, _ = w.Write(data)
}
