package tests

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/BrandonDHaskell/Portunus/server/internal/httpapi"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/service"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/store/memory"
)

func TestHeartbeat_OK(t *testing.T) {
	st := memory.New()
	svc := service.NewHeartbeatService(st)

	logger := log.New(bytes.NewBuffer(nil), "", 0)
	srv := httpapi.NewServer(httpapi.Dependencies{
		Logger:           logger,
		Addr:             ":0",
		HeartbeatService: svc,
	})

	ts := httptest.NewServer(getHandler(srv))
	defer ts.Close()

	body := []byte(`{"module_id":"door-001","uptime_s":42}`)
	resp, err := http.Post(ts.URL+"/v1/heartbeat", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

// Small helper: recreate handler by building a new server with the same deps.
// (If you later expose srv.Handler(), you can remove this.)
func getHandler(_ *httpapi.Server) http.Handler {
	// For now, we’ll just rebuild in tests if needed in later iterations.
	// Leaving this placeholder so the test file compiles once you decide how to expose handlers.
	return http.NewServeMux()
}
